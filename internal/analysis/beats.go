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
	// Search range, deliberately wide (30–300 BPM) so the choice is made by what
	// explains the onsets and not by an assumed tempo range.
	minBeatPeriod = 0.2
	maxBeatPeriod = 2.0
	// A step of ~2% keeps the scan to ~115 candidates while never missing a
	// period by more than that.
	beatPeriodStep = 1.02
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

	best := BeatGrid{Coverage: -1}
	for p := minBeatPeriod; p <= maxBeatPeriod; p *= beatPeriodStep {
		phase, coverage := bestPhase(ts, p)
		if coverage < minBeatCoverage {
			continue
		}
		// Longer period wins; on a tie the better coverage wins.
		if p > best.Period || (p == best.Period && coverage > best.Coverage) {
			best = BeatGrid{Period: p, Phase: phase, BPM: 60 / p, Coverage: coverage}
		}
	}
	if best.Coverage < 0 {
		return grid, false
	}
	// The candidate ladder only gets the period within a step (~2%), and a period
	// that is slightly long drifts off the real beats over a long file — the grid
	// would fit the first clicks and miss the last. Two least-squares passes
	// against the onsets themselves land on the period they actually share.
	best.Period, best.Phase = refineGrid(ts, best.Period, best.Phase)
	best.BPM = 60 / best.Period
	best.Coverage = coverageAt(ts, best.Period, best.Phase)
	if best.Coverage < minBeatCoverage {
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
func bestPhase(ts []float64, period float64) (float64, float64) {
	tol := beatTolerance * period
	bestPhase, bestCount := 0.0, 0
	for _, o := range ts {
		candidate := math.Mod(o, period)
		count := 0
		for _, other := range ts {
			d := math.Abs(math.Mod(other, period) - candidate)
			if d > period/2 {
				d = period - d
			}
			if d <= tol {
				count++
			}
		}
		if count > bestCount {
			bestCount, bestPhase = count, candidate
		}
	}
	return bestPhase, float64(bestCount) / float64(len(ts))
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
