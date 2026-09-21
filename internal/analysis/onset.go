package analysis

import (
	"context"
	"encoding/binary"
	"log/slog"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/xcerr"
)

// AudioOnsetAnalyzer detects percussive audio events (hits, onsets) from a
// mono 16 kHz pulse-code stream: per-hop peak envelope → positive flux →
// robust adaptive threshold (median + k·MAD) → local-maximum peak picking.
//
// RMS loudness says "loud here"; onsets say "something struck here" — a
// badminton smash or a musical note attack, distinct from crowd noise or
// sustained tones (docs/EVAL.md scenarios depend on the difference).
//
// Deterministic: identical audio ⇒ identical onset times. The decode is a
// streaming pass — memory stays flat regardless of duration.
type AudioOnsetAnalyzer struct{}

func (AudioOnsetAnalyzer) Name() string { return "audio_onset" }
func (AudioOnsetAnalyzer) Version() int { return 1 }

// DSP constants (versioned with the analyzer: changing these is a behavior
// change and must bump Version).
const (
	onsetSampleRate = 16000 // decode rate: mono s16le
	onsetHopSamples = 320   // 20 ms envelope hop
	// Robust threshold: flux above median + k·MAD of its neighborhood.
	onsetMADK = 6.0
	// Absolute floor (dB of rise) — noise ticks in digital silence never pass.
	onsetFluxFloorDB = 3.0
	// Local-maximum half-width in hops (±120 ms).
	onsetPeakHalf = 6
	// Neighborhood half-window for statistics (±0.5 s).
	onsetStatHalf = 25
	// Peak strength normalization: a rise this many dB above threshold is 1.0.
	onsetStrengthDB = 12.0
	// Analyzer wall-clock budget for the decode+DSP pass.
	onsetTimeout = 10 * time.Minute
)

// AudioOnset runs the onset analyzer (named constructor for ResolveAnalyzers).
func (a AudioOnsetAnalyzer) Analyze(ctx context.Context, opts Options, path string, hasAudio bool, log *slog.Logger) ([]FeatureTrack, error) {
	if !hasAudio {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, onsetTimeout)
	defer cancel()

	p := newOnsetProcessor()
	err := media.StreamStdout(ctx, opts.Tools.FFmpeg, p.consume,
		"-hide_banner", "-nostdin", "-v", "error",
		"-threads", "1",
		"-i", path,
		"-vn", "-ac", "1", "-ar", strconv.Itoa(onsetSampleRate),
		"-f", "s16le", "-",
	)
	if err != nil {
		if ctx.Err() != nil {
			return nil, xcerr.E(xcerr.CodeAnalyzerFailure, "audio onset analysis timed out", ctx.Err())
		}
		return nil, xcerr.E(xcerr.CodeAnalyzerFailure, "audio onset analysis failed", err)
	}

	samples := p.finish()
	track := FeatureTrack{
		Analyzer: a.Name(),
		Version:  a.Version(),
		Kind:     "audio_onset",
		Unit:     "strength",
		Samples:  samples,
	}
	log.Debug("audio_onset analyzed", "onsets", len(samples), "envelope_hops", len(p.db))
	return []FeatureTrack{track}, nil
}

// onsetProcessor turns a chunked s16le stream into a peak-envelope, then
// onsets. All state is owned by one goroutine (StreamStdout serializes calls).
type onsetProcessor struct {
	rem   []byte // bytes carried between chunks (sample alignment)
	db    []float64
	cur   int32 // running peak of the current hop (int32: |−32768| overflows int16)
	inHop int   // samples consumed of the current hop
}

func newOnsetProcessor() *onsetProcessor {
	return &onsetProcessor{rem: make([]byte, 0, 2)}
}

const s16minAbs = 32768.0

// consume absorbs one chunk of little-endian s16 mono PCM.
func (p *onsetProcessor) consume(chunk []byte) error {
	data := chunk
	if len(p.rem) > 0 {
		need := 2 - len(p.rem)
		if len(data) < need {
			p.rem = append(p.rem, data...)
			return nil
		}
		p.rem = append(p.rem, data[:need]...)
		p.absorb(binary.LittleEndian.Uint16(p.rem))
		p.rem = p.rem[:0]
		data = data[need:]
	}
	// Process aligned pairs without allocating.
	i := 0
	for ; i+2 <= len(data); i += 2 {
		p.absorb(binary.LittleEndian.Uint16(data[i:]))
	}
	p.rem = append(p.rem, data[i:]...)
	return nil
}

func (p *onsetProcessor) absorb(raw uint16) {
	v := int16(raw) // #nosec G115 -- s16le PCM samples are two's-complement by definition
	a := int32(v)
	if a < 0 {
		a = -a // int32 math: |−32768| overflows back to negative in int16
	}
	if a > p.cur {
		p.cur = a
	}
	p.inHop++
	if p.inHop >= onsetHopSamples {
		db := 20 * math.Log10(math.Max(float64(p.cur), 1)/s16minAbs)
		if db < -90 {
			db = -90
		}
		p.db = append(p.db, db)
		p.cur = 0
		p.inHop = 0
	}
}

// finish drains the final partial hop and picks onsets.
func (p *onsetProcessor) finish() []Sample {
	if p.inHop > 0 {
		db := 20 * math.Log10(math.Max(float64(p.cur), 1)/s16minAbs)
		if db < -90 {
			db = -90
		}
		p.db = append(p.db, db)
	}
	return detectOnsets(p.db, onsetSampleRate/float64(onsetHopSamples))
}

// detectOnsets is the pure decision core: envelope (dB per hop) → onsets.
// hopRate is envelope samples per second.
func detectOnsets(db []float64, hopRate float64) []Sample {
	n := len(db)
	if n < 3 {
		return nil
	}

	// Positive flux: how much louder each hop got.
	flux := make([]float64, n)
	for i := 1; i < n; i++ {
		if d := db[i] - db[i-1]; d > 0 {
			flux[i] = d
		}
	}

	halfStat, halfPeak := onsetStatHalf, onsetPeakHalf
	if halfStat >= n {
		halfStat = n - 1
	}
	win := make([]float64, 0, 2*halfStat+1)

	var out []Sample
	for i := 1; i < n; i++ {
		f := flux[i]
		if f < onsetFluxFloorDB {
			continue
		}
		lo, hi := maxInt(0, i-halfStat), minInt(n-1, i+halfStat)
		win = win[:0]
		for j := lo; j <= hi; j++ {
			win = append(win, flux[j])
		}
		med := median(win)
		mad := medianAbsDev(win, med)
		thr := med + onsetMADK*mad
		if thr < onsetFluxFloorDB {
			thr = onsetFluxFloorDB
		}
		if f < thr {
			continue
		}
		// Local maximum (earliest wins ties — a strictly-greater-only check
		// emitted one onset per hop across a plateau of equal flux).
		isPeak := true
		for j := maxInt(0, i-halfPeak); j <= minInt(n-1, i+halfPeak); j++ {
			if flux[j] > f || (flux[j] == f && j < i) {
				isPeak = false
				break
			}
		}
		if !isPeak {
			continue
		}
		strength := (f - thr) / onsetStrengthDB
		if strength > 1 {
			strength = 1
		}
		out = append(out, Sample{
			T: round4(float64(i) / hopRate),
			V: round4(math.Max(strength, 0.01)),
		})
	}
	return out
}

func median(sorted []float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	s := make([]float64, len(sorted))
	copy(s, sorted)
	sort.Float64s(s)
	h := len(s) / 2
	if len(s)%2 == 1 {
		return s[h]
	}
	return (s[h-1] + s[h]) / 2
}

func medianAbsDev(v []float64, med float64) float64 {
	if len(v) == 0 {
		return 0
	}
	d := make([]float64, len(v))
	for i, x := range v {
		d[i] = math.Abs(x - med)
	}
	return median(d)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func round4(v float64) float64 {
	return math.Round(v*10000) / 10000
}
