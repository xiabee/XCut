package event

import (
	"math"
	"sort"

	"github.com/xiabee/XCut/internal/analysis"
)

// Rally segmentation: group audio transients (hit-like onsets) into
// candidate rallies — the generic shape of any hit-driven sport rally
// (badminton, tennis, volleyball): dense transients + visual motion, with
// quiet gaps between. It is a data-selected mode of the event builder
// (Config.Mode == ModeRally), never a style-specific code path.
//
// Pipeline: hits (onset samples) → cluster by gap → pad with lead/tail →
// filter by hit count and motion support → scored, explainable segments.
const (
	ModeActivity = "activity"
	ModeRally    = "rally"
)

// Rally defaults applied when Config selects rally mode without parameters
// (presets may set them explicitly; zero values never disable a rally run).
const (
	defaultRallyGap = 2.5  // s the onset rate must stay at/below exit before a rally closes
	defaultRallyPad = 1.2  // s padded before/after the first/last hit
	defaultMinHits  = 4    // transients required inside one rally
	defaultMaxRally = 30.0 // s — longer dense spans chunk into pieces of this size

	defaultEnterRate = 1.0 // hits/sec that open a rally
	defaultExitRate  = 0.5 // hits/sec below which a rally is ending

	rateWindow = 2.0 // s sliding window the onset rate is measured over
	rateStep   = 0.5 // s window advance per step
)

// buildRallies clusters onsets into rally segments. The stats pointer (nil
// in tests that don't care) collects gate disposition counts.
func buildRallies(motion, audio, onsets *analysis.FeatureTrack, duration float64, cfg Config, stats *BuildStats) ([]Segment, error) {
	hits := onsetSamples(onsets)
	if len(hits) == 0 {
		return nil, nil
	}
	if stats != nil {
		stats.Onsets = len(hits)
	}

	gap := firstNonZero(cfg.RallyGap, defaultRallyGap)
	pad := firstNonZero(cfg.RallyPad, defaultRallyPad)
	minHits := cfg.MinHits
	if minHits <= 0 {
		minHits = defaultMinHits
	}
	enter := firstNonZero(cfg.RallyEnterRate, defaultEnterRate)
	exit := firstNonZero(cfg.RallyExitRate, defaultExitRate)

	// Density walk: the onset rate over a sliding window, with hysteresis —
	// open at the enter rate, close only once the rate has stayed below the
	// exit rate for `gap` seconds. Real court audio fires on footsteps and
	// speech through every break, so absolute-quiet splits never trigger;
	// density is the signal that survives (rallies are 2-4 hits/sec, breaks
	// are ambient noise alone).
	countBetween := func(lo, hi float64) int {
		// Half-open [lo, hi): a hit exactly hi seconds after lo belongs to
		// the next window, or evenly spaced noise can look dense.
		loI := sort.Search(len(hits), func(i int) bool { return hits[i].T >= lo })
		hiI := sort.Search(len(hits), func(i int) bool { return hits[i].T >= hi })
		return hiI - loI
	}

	lastT := hits[len(hits)-1].T
	enterCount := int(enter * rateWindow)
	if float64(enterCount) < enter*rateWindow {
		enterCount++ // ceil: the documented rate is a floor
	}
	exitCount := int(exit * rateWindow)

	type rawSpan struct{ start, end float64 }
	var spans []rawSpan
	inside := false
	openT, belowSince, lastInsideT := 0.0, -1.0, 0.0
	for t := 0.0; t <= lastT+rateWindow; t += rateStep {
		r := countBetween(t, t+rateWindow)
		if !inside {
			if r >= enterCount {
				inside, openT, belowSince, lastInsideT = true, t, -1, t
				if stats != nil {
					stats.SpansOpened++
				}
			}
			continue
		}
		if r > exitCount {
			belowSince, lastInsideT = -1, t
			continue
		}
		// At or below the exit rate: the rally is ending. The span stays
		// open only until `gap` seconds have passed in this state.
		if belowSince < 0 {
			belowSince = t
		}
		if t-belowSince >= gap {
			spans = append(spans, rawSpan{start: openT, end: lastInsideT + rateWindow})
			inside = false
		}
	}
	if inside {
		spans = append(spans, rawSpan{start: openT, end: lastInsideT + rateWindow})
	}

	// Each span's hits become candidate rallies; a dense span longer than
	// one rally chunks into consecutive pieces instead of being truncated
	// (the old cap silently discarded everything past 30s of continuous
	// play).
	var cands []rallyChunk
	for _, sp := range spans {
		loI := sort.Search(len(hits), func(i int) bool { return hits[i].T >= sp.start })
		hiI := sort.Search(len(hits), func(i int) bool { return hits[i].T >= sp.end })
		spanHits := hits[loI:hiI]
		if len(spanHits) < minHits {
			if stats != nil {
				stats.SpansDroppedMinHits++
			}
			continue
		}
		start := spanHits[0].T - pad
		end := spanHits[len(spanHits)-1].T + pad
		if start < 0 {
			start = 0
		}
		if end > duration {
			end = duration
		}
		for _, ch := range chunkBounds(start, end, defaultMaxRally) {
			loC := sort.Search(len(spanHits), func(i int) bool { return spanHits[i].T >= ch[0] })
			hiC := sort.Search(len(spanHits), func(i int) bool { return spanHits[i].T >= ch[1] })
			cands = append(cands, rallyChunk{
				bounds: ch,
				hits:   spanHits[loC:hiC],
				motion: intervalMean(motion, ch[0], ch[1]),
			})
		}
	}

	// The motion floor in cfg is absolute, but the signal's SCALE is not
	// stable within one video: auto-exposure/shutter drift can halve the
	// whole track several-fold late in a clip (measured on real fixed-camera
	// match footage: ~0.055 early vs ~0.017 late with the play unchanged —
	// the match point itself fell below a floor calibrated on the bright
	// first half; per-frame normalization does NOT remove this, the loss is
	// in the decoded content). The floor therefore clamps to the video's own
	// active level (P75 of chunk means), at most 4x of relief and never
	// below it: a globally weaker section cannot be pushed under a gate
	// calibrated on the strong one, while a uniformly static video (where
	// P75 collapses) keeps most of the configured floor. PROVISIONAL
	// constants (0.4 ratio, P75, 4x cap) pending annotated-footage
	// evaluation (docs/EVAL.md path).
	effectiveFloor := cfg.MotionFloor
	if means := chunkMotionMeans(cands); len(means) > 0 {
		relaxed := 0.4 * percentile(means, 0.75)
		limit := 0.25 * cfg.MotionFloor
		if relaxed < effectiveFloor {
			effectiveFloor = max(relaxed, limit)
		}
	}

	var segments []Segment
	for _, c := range cands {
		if stats != nil {
			stats.ChunksConsidered++
		}
		if len(c.hits) < minHits {
			if stats != nil {
				stats.ChunksDroppedMinHits++
			}
			continue
		}
		if seg, gate, ok := scoreRallyWithFloor(motion, audio, cfg, c.bounds[0], c.bounds[1], c.hits, effectiveFloor); ok {
			segments = append(segments, seg)
		} else if stats != nil {
			switch gate {
			case "min_duration":
				stats.ChunksDroppedMinDuration++
			case "motion_floor":
				stats.ChunksDroppedMotionFloor++
			}
		}
	}
	sort.Slice(segments, func(i, j int) bool { return segments[i].Start < segments[j].Start })
	if stats != nil {
		stats.Segments = len(segments)
	}
	return segments, nil
}

// rallyChunk is one chunk candidate: its bounds, the onsets inside, and
// the chunk's mean motion (measured before gating so the adaptive floor
// baseline can use all chunks).
type rallyChunk struct {
	bounds [2]float64
	hits   []analysis.Sample
	motion float64
}

// chunkMotionMeans collects the per-chunk motion means for the adaptive
// floor baseline.
func chunkMotionMeans(cands []rallyChunk) []float64 {
	means := make([]float64, 0, len(cands))
	for _, c := range cands {
		if !math.IsNaN(c.motion) && !math.IsInf(c.motion, 0) {
			means = append(means, c.motion)
		}
	}
	return means
}

// percentile returns the q-quantile (0..1) of vs by linear interpolation.
// vs must be non-empty.
func percentile(vs []float64, q float64) float64 {
	sorted := make([]float64, len(vs))
	copy(sorted, vs)
	sort.Float64s(sorted)
	pos := q * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return sorted[lo]
	}
	return sorted[lo] + (sorted[hi]-sorted[lo])*(pos-float64(lo))
}

// chunkBounds splits [start, end] into ceil(dur/max) consecutive pieces of
// at most max seconds (a piece is dropped only by hit-count/motion gates,
// never by the cap itself).
func chunkBounds(start, end, max float64) [][2]float64 {
	dur := end - start
	if dur <= max {
		return [][2]float64{{start, end}}
	}
	n := int(math.Ceil(dur / max))
	if n < 1 {
		n = 1
	}
	out := make([][2]float64, 0, n)
	step := dur / float64(n)
	for i := 0; i < n; i++ {
		lo := start + float64(i)*step
		hi := lo + step
		if i == n-1 {
			hi = end
		}
		out = append(out, [2]float64{lo, hi})
	}
	return out
}

// scoreRally applies the watchability gates (duration floor, adaptive
// motion floor) and builds the explainable rally segment for one hit
// window. On refusal it names the gate that fired so the caller's
// rejection counters are derived from the decision itself, not from a
// re-derivation of it.
func scoreRally(motion, audio *analysis.FeatureTrack, cfg Config, start, end float64, chunkHits []analysis.Sample) (Segment, string, bool) {
	return scoreRallyWithFloor(motion, audio, cfg, start, end, chunkHits, cfg.MotionFloor)
}

// scoreRallyWithFloor is scoreRally with an explicit motion floor (the
// adaptive baseline clamps cfg.MotionFloor before gating).
func scoreRallyWithFloor(motion, audio *analysis.FeatureTrack, cfg Config, start, end float64, chunkHits []analysis.Sample, motionFloor float64) (Segment, string, bool) {
	if end-start < cfg.MinDuration {
		return Segment{}, "min_duration", false
	}
	// Motion support: a rally with no on-screen motion is not watchable.
	meanMotion := intervalMean(motion, start, end)
	if meanMotion < motionFloor || math.IsNaN(meanMotion) || math.IsInf(meanMotion, 0) {
		return Segment{}, "motion_floor", false
	}
	meanDB := silentDB
	if audio != nil {
		meanDB = intervalMeanDB(audio, start, end)
	}
	count := len(chunkHits)
	dur := end - start
	density := float64(count) / dur
	hitsN := clamp(float64(count)/12.0, 0, 1)
	densN := clamp(density/1.5, 0, 1)
	motionN := clamp(meanMotion/0.30, 0, 1)
	durN := clamp(dur/12.0, 0, 1)
	sc := 0.40*hitsN + 0.30*densN + 0.20*motionN + 0.10*durN
	if math.IsNaN(sc) || math.IsInf(sc, 0) {
		sc = 0
	}
	return Segment{
		Start:       round4(start),
		End:         round4(end),
		Score:       round4(sc),
		MeanMotion:  round4(meanMotion),
		MeanAudioDB: round4(meanDB),
		Kind:        ModeRally,
		HitCount:    count,
		HitDensity:  round4(density),
	}, "", true
}

// onsetSamples extracts sorted, deduplicated onset samples.
func onsetSamples(onsets *analysis.FeatureTrack) []analysis.Sample {
	if onsets == nil || len(onsets.Samples) == 0 {
		return nil
	}
	out := make([]analysis.Sample, len(onsets.Samples))
	copy(out, onsets.Samples)
	sortSamples(out)
	// Collapse duplicates at the same time (should not happen; cheap guard).
	dedup := out[:0]
	for i, s := range out {
		if i == 0 || s.T != out[i-1].T {
			dedup = append(dedup, s)
		}
	}
	return dedup
}

// intervalMean is the mean frame_diff value within [start, end].
func intervalMean(t *analysis.FeatureTrack, start, end float64) float64 {
	if t == nil || len(t.Samples) == 0 {
		return 0
	}
	sum, n := 0.0, 0
	for _, s := range t.Samples {
		if s.T >= start && s.T <= end {
			sum += s.V
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

// intervalMeanDB is the mean RMS dBFS within [start, end] (silentDB when the
// track has no samples there).
func intervalMeanDB(t *analysis.FeatureTrack, start, end float64) float64 {
	if t == nil || len(t.Samples) == 0 {
		return silentDB
	}
	sum, n := 0.0, 0
	for _, s := range t.Samples {
		if s.T >= start && s.T <= end {
			sum += s.V
			n++
		}
	}
	if n == 0 {
		return silentDB
	}
	return sum / float64(n)
}

func firstNonZero(v, def float64) float64 {
	if v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0) {
		return v
	}
	return def
}
