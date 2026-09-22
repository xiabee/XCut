package analysis

import (
	"math"
	"sort"
)

// BeatGrid is a periodic pulse inferred from onset times — the input "cut on the
// beat" needs. It is derived, never stored: estimating it from the cached onset
// track costs microseconds, so adding a cache entry would be one more thing to
// invalidate for no measurable gain.
type BeatGrid struct {
	Period   float64   `json:"period"`   // seconds between beats
	Phase    float64   `json:"phase"`    // position of the grid's first beat in [0, Period)
	BPM      float64   `json:"bpm"`      // 60 / Period
	Coverage float64   `json:"coverage"` // share of onsets sitting on the grid
	Beats    []float64 `json:"beats"`    // beat times within the requested horizon
}

const (
	// Below this many onsets a period is a coincidence: three points can always
	// be fit by some grid, which is exactly the confidence the estimator must not
	// claim from sparse or silent stretches.
	minBeatOnsets = 4
	// How many onsets the fit looks at: the scan costs time per onset per candidate,
	// and a tempo fitted on ten minutes of 120 BPM is the tempo a three-hour recording
	// has. Bounding it keeps a long bed from making this the slowest stage of a render;
	// the longest bed measured here (three minutes of clicks, 359 onsets) is nowhere
	// near the cap, so nothing in the recorded evidence is truncated.
	maxBeatFitOnsets = 1200
	// Search range, deliberately wide (30–300 BPM) so the choice is made by what
	// explains the onsets and not by an assumed tempo range.
	minBeatPeriod = 0.2
	maxBeatPeriod = 2.0
	// An onset counts as explained when it lands within a quarter period of the
	// grid; anything looser lets a period pass that fits nothing in particular.
	beatTolerance = 0.25
	// The grid must explain this share of onsets to be believed.
	minBeatCoverage = 0.9
)

// EstimateBeatGrid estimates the beat grid of a set of onset times, reporting the beats
// that fall in [0, horizon]. It returns ok=false when the onsets are too few,
// too irregular, or the horizon is not positive.
//
// Among the periods that explain the onsets it takes the *longest*: a drummer
// hitting every beat and one hitting every other beat give the same evidence for
// the shorter grid only when the missing clicks exist, and this function does not
// get to invent them.
func EstimateBeatGrid(onsets []float64, horizon float64) (BeatGrid, bool) {
	grid := BeatGrid{}
	if horizon <= 0 {
		return grid, false
	}
	ts := sortedUnique(onsets)
	if len(ts) < minBeatOnsets {
		return grid, false
	}
	// The fit is bounded. Tempo is a property of the track, not of how much of it was
	// scanned, so the first maxBeatFitOnsets onsets decide the grid and the beats are
	// then projected across the rest of the evidence; the tail clamp still reads the
	// real last onset, so nothing is claimed past the music.
	fit := ts
	if len(fit) > maxBeatFitOnsets {
		fit = fit[:maxBeatFitOnsets]
	}

	best := BeatGrid{Coverage: -1}
	// One candidate source, anchored on the ends of the evidence: "these n intervals
	// happened across this span" proposes a period with no error to accumulate. The
	// alternative this function used — a ladder of rungs ~2% apart, each folded to a
	// single phase — cannot see a long file: a 1% period error stops the phase being
	// constant by the hundredth beat, so a perfect minute of metronome folded to 0.80
	// and the estimate was called disbelief. Every anchored period is then fitted to
	// the onsets themselves, and the phase taken back from that fit by anchoring it on
	// a real onset. The last part is not decoration: the least-squares phase is a mean,
	// and on a lattice whose clicks alternate either side of the true beat it lands
	// exactly halfway, where every click sits at precisely the tolerance distance — so
	// a grid twice as slow as the music scores full coverage and wins the longest-wins
	// rule with nothing behind it.
	if span := fit[len(fit)-1] - fit[0]; span > 0 {
		for n := 1; n < len(fit); n++ {
			p := span / float64(n)
			if p < minBeatPeriod || p > maxBeatPeriod {
				continue
			}
			fitted, _ := refineGrid(fit, p, math.Mod(fit[0], p))
			ph, coverage := bestPhase(fit, fitted)
			if coverage < minBeatCoverage {
				continue
			}
			if fitted > best.Period || (fitted == best.Period && coverage > best.Coverage) {
				best = BeatGrid{Period: fitted, Phase: ph, BPM: 60 / fitted, Coverage: coverage}
			}
		}
	}
	if best.Coverage < 0 {
		return grid, false
	}

	// Stop at the music, not at the horizon: the last onset plus half a period is
	// as far as the evidence reaches, so a reel is never cut to beats that the
	// faded-out tail never played.
	reach := math.Min(horizon, ts[len(ts)-1]+best.Period/2)
	for b := best.Phase; b <= reach+1e-9; b += best.Period {
		if b >= -1e-9 {
			best.Beats = append(best.Beats, b)
		}
	}
	if len(best.Beats) == 0 {
		return grid, false
	}
	return best, true
}

// bestPhase folds the onsets modulo a candidate period and finds the single
// cluster that explains the most of them. Requiring one cluster is what rejects
// sub-harmonics: a period that is twice too long leaves its onsets in two places,
// and no single phase can call both of them on-beat.
//
// The cluster is found by sliding a window over the sorted residues rather than by
// trying every onset against every other one. Same answer — the window is centred
// on an onset's residue either way — and it matters because this runs once per
// candidate period: the quadratic form made a ten-minute bed take minutes.
func bestPhase(ts []float64, period float64) (float64, float64) {
	tol := beatTolerance * period
	n := len(ts)
	res := make([]float64, n)
	for i, o := range ts {
		r := math.Mod(o, period)
		if r < 0 {
			r += period
		}
		res[i] = r
	}
	sort.Float64s(res)
	// The list doubled with one period added, so a cluster that wraps around the
	// circle is a contiguous run like any other.
	ext := make([]float64, 2*n)
	copy(ext, res)
	for i := 0; i < n; i++ {
		ext[n+i] = res[i] + period
	}
	bestCount, bestAt := 0, res[0]
	low, high := 0, 0
	for _, c := range res {
		for low < 2*n && ext[low] < c-tol {
			low++
		}
		if high < low {
			high = low
		}
		for high < 2*n && ext[high] <= c+tol {
			high++
		}
		// Strictly greater keeps the first winner, as the anchored scan did.
		if cnt := high - low; cnt > bestCount {
			bestCount, bestAt = cnt, c
		}
	}
	return bestAt, float64(bestCount) / float64(n)
}

func sortedUnique(ts []float64) []float64 {
	out := make([]float64, 0, len(ts))
	for _, t := range ts {
		if t < 0 || math.IsNaN(t) || math.IsInf(t, 0) {
			continue
		}
		out = append(out, t)
	}
	sort.Float64s(out)
	dedup := out[:0]
	for i, t := range out {
		if i == 0 || t-out[i-1] > 1e-3 {
			dedup = append(dedup, t)
		}
	}
	return dedup
}

// refineGrid fits onset times to phase + k*period by least squares over the
// integer beat index k, re-indexing once with the improved period.
func refineGrid(ts []float64, period, phase float64) (float64, float64) {
	for i := 0; i < 2; i++ {
		var sk, so, skk, sko float64
		n := float64(len(ts))
		for _, o := range ts {
			k := math.Round((o - phase) / period)
			sk += k
			so += o
			skk += k * k
			sko += k * o
		}
		den := skk*n - sk*sk
		if den == 0 {
			break
		}
		p := (sko*n - sk*so) / den
		if p <= 0 {
			break
		}
		ph := (so - p*sk) / n
		period, phase = p, math.Mod(ph, p)
		if phase < 0 {
			phase += period
		}
	}
	return period, phase
}

// coverageAt reports the share of onsets within beatTolerance of the grid.
func coverageAt(ts []float64, period, phase float64) float64 {
	tol := beatTolerance * period
	explained := 0
	for _, o := range ts {
		d := math.Abs(math.Mod(o-phase+period, period))
		if d > period/2 {
			d = period - d
		}
		if d <= tol {
			explained++
		}
	}
	return float64(explained) / float64(len(ts))
}
