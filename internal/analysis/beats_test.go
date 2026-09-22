package analysis

import (
	"math"
	"math/rand"
	"testing"
)

// Beat grid estimation is where "cut on the beat" starts, and it is the kind of
// code that looks fine while inventing a rhythm the media never had. These cases
// are chosen so a wrong answer is visible: a perfect click train, a jittered one,
// too few onsets to conclude anything, and the every-other-beat case where the
// honest answer is the longer period.

func TestBeatGridRecoversAClickTrain(t *testing.T) {
	const period = 0.5
	onsets := make([]float64, 0, 24)
	for k := 1; k < 24; k++ {
		onsets = append(onsets, float64(k)*period)
	}
	got, ok := EstimateBeatGrid(onsets, 12)
	if !ok {
		t.Fatal("a 23-onset click train was refused")
	}
	if math.Abs(got.Period-period)/period > 0.05 {
		t.Fatalf("period = %.4f, want %.4f within 5%%", got.Period, period)
	}
	if len(got.Beats) == 0 {
		t.Fatal("no beats emitted for a click train")
	}
	for i, b := range got.Beats {
		if b < -1e-9 {
			t.Fatalf("beat %d is negative (%.3f)", i, b)
		}
		if math.Mod(b+period/2, period) > period-1e-9 {
			t.Fatalf("beat %.3f is off the %.1f s grid by more than half a period", b, period)
		}
	}
	last := got.Beats[len(got.Beats)-1]
	if last > onsets[len(onsets)-1]+got.Period/2+1e-9 {
		t.Fatalf("grid extrapolates past the last onset: last beat %.3f, last onset %.3f", last, onsets[len(onsets)-1])
	}
}

func TestBeatGridHandlesJitter(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	const period = 0.4
	var onsets []float64
	for k := 1; k <= 30; k++ {
		onsets = append(onsets, float64(k)*period+(rng.Float64()*2-1)*0.03)
	}
	got, ok := EstimateBeatGrid(onsets, 12)
	if !ok {
		t.Fatal("30 jittered onsets were refused")
	}
	if math.Abs(got.Period-period)/period > 0.1 {
		t.Fatalf("period = %.4f, want %.4f within 10%% under ±30 ms jitter", got.Period, period)
	}
}

func TestBeatGridRefusesTooFewOnsets(t *testing.T) {
	// Two onsets fix a period exactly — and that period is a coincidence, not a
	// rhythm. Saying "no grid" is what keeps a silent or sparse stretch from
	// pulling cuts onto beats nobody played.
	for _, onsets := range [][]float64{nil, {1}, {1, 1.5}, {1, 1.5, 2}} {
		if got, ok := EstimateBeatGrid(onsets, 10); ok {
			t.Fatalf("%v produced a grid (period %.4f, %d beats); want a refusal", onsets, got.Period, len(got.Beats))
		}
	}
}

func TestBeatGridPrefersTheLongestExplainingPeriod(t *testing.T) {
	// A drummer hitting every beat and one hitting every other beat produce these
	// onsets identically only if the shorter period is chosen. The grid cannot
	// know about the half-beat it never heard, so it must report 1.0 s.
	var onsets []float64
	for k := 1; k <= 12; k++ {
		onsets = append(onsets, float64(k))
	}
	got, ok := EstimateBeatGrid(onsets, 13)
	if !ok {
		t.Fatal("a steady 1 s train was refused")
	}
	if math.Abs(got.Period-1.0) > 0.05 {
		t.Fatalf("period = %.4f, want 1.0: the longest period that explains every onset", got.Period)
	}
	if len(got.Beats) != 13 {
		t.Fatalf("beats = %d, want one per second across 13 s: %v", len(got.Beats), got.Beats)
	}
}

func TestBeatGridStopsAtTheHorizon(t *testing.T) {
	var onsets []float64
	for k := 1; k <= 40; k++ {
		onsets = append(onsets, float64(k)*0.25)
	}
	got, ok := EstimateBeatGrid(onsets, 4)
	if !ok {
		t.Fatal("refused")
	}
	for _, b := range got.Beats {
		if b > 4+1e-9 {
			t.Fatalf("beat %.3f past the 4 s horizon", b)
		}
	}
	if len(got.Beats) == 0 {
		t.Fatal("no beats inside the horizon")
	}
}
