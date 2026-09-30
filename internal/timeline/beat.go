package timeline

import (
	"math"
	"sort"
	"strconv"
)

// maxBeatTicks caps the derived tick list — and it is a work bound as much as
// a wire bound: enumeration stops when the cap is reached, so even a
// hand-built pathological document costs O(cap), not O(beats) of memory per
// GET (AGENTS rule 4). The ticks that survive are the ones enumerated first —
// clip order, which for a built reel is output order — and a ruler that
// cannot show more than a few hundred marks gains nothing past the ceiling.
const maxBeatTicks = 4096

// BeatTicks is the reel's cut-to grid made drawable: the tempo, and the grid's
// positions in OUTPUT time — each beat mapped through the clip whose material
// carries it, so a tick lands where the viewer actually meets that beat after
// trims, reordering and speed. nil (rendered as a null on the wire) means the
// document states no grid, which is "no evidence", not "phase zero".
type BeatTicks struct {
	BPM   float64   `json:"bpm"`
	Ticks []float64 `json:"ticks"`
}

// BeatTicks reads the grid this document was cut against (MetaBeatBPM +
// MetaBeatPhase) and maps its beats through every clip's own source→timeline
// mapping. Both metadata keys must parse to positive numbers, or there is no
// answer: a phase this document does not state must not be invented, and a
// tempo without its phase draws ticks at the wrong places — worse than none.
func (t *Timeline) BeatTicks() *BeatTicks {
	if t == nil {
		return nil
	}
	bpm, err := strconv.ParseFloat(t.Metadata[MetaBeatBPM], 64)
	if err != nil || !(bpm > 0) {
		return nil
	}
	phase, err := strconv.ParseFloat(t.Metadata[MetaBeatPhase], 64)
	if err != nil || !(phase >= 0) {
		return nil
	}
	period := 60 / bpm
	out := make([]float64, 0, 64)
walk:
	for _, tr := range t.Tracks {
		for _, c := range tr.Clips {
			speed := c.Speed
			if speed <= 0 {
				continue
			}
			// Beats k live at phase + k·period; the ones inside this clip's
			// source range are k in [start−phase, end−phase] over the period.
			first := int(math.Ceil((c.SourceStart - phase) / period))
			last := int(math.Floor((c.SourceEnd - phase) / period))
			for k := first; k <= last; k++ {
				if len(out) >= maxBeatTicks {
					break walk
				}
				at := phase + float64(k)*period
				// Six decimals (a microsecond) keeps the wire clean of
				// floating-point tails like 0.6000000000000001 — the ruler
				// draws these verbatim, and the dedupe below compares them.
				pos := math.Round((c.TimelineStart+(at-c.SourceStart)/speed)*1e6) / 1e6
				out = append(out, pos)
			}
		}
	}
	sort.Float64s(out)
	// The same beat can arrive twice — two tracks sharing material, or a
	// boundary both neighbours claim. Near-identical output positions are one
	// mark, not two. The cap was already enforced where the beats were
	// enumerated, so this list never needs re-truncating.
	dedup := out[:0]
	for i, v := range out {
		if i == 0 || v-dedup[len(dedup)-1] > 1e-6 {
			dedup = append(dedup, v)
		}
	}
	return &BeatTicks{BPM: bpm, Ticks: dedup}
}
