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

// The five cases above run on seconds of audio, and that is exactly the problem:
// a real music bed is minutes long, and a 2% candidate ladder's error is not a
// rounding detail once a hundred beats have accumulated it. These pin the two
// symptoms that came out of using --music at product length, plus the control that
// says the estimator did not become gullible on the way.

func lattice(n int, step float64) []float64 {
	out := make([]float64, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, float64(i)*step)
	}
	return out
}

// The tempos are not decoration. A minute of clicks at 100 BPM (a 0.6 s lattice) is
// the case the candidate ladder alone answers worst — it returns a 0.2 s grid, three
// times too fast — while 120 BPM sits close enough to a rung for the ladder to get
// there by itself. Testing one tempo would pin one coincidence.
func TestBeatGridBelievesLongClickTrains(t *testing.T) {
	for _, bpm := range []int{60, 73, 90, 98, 100, 107, 120, 150, 187, 222, 300} {
		step := 60.0 / float64(bpm)
		onsets := lattice(int(math.Round(60.0/step)), step)
		got, ok := EstimateBeatGrid(onsets, 60)
		if !ok {
			t.Errorf("%d bpm (%d clicks over 60 s): refused — a metronome is the easiest grid there is", bpm, len(onsets))
			continue
		}
		if err := math.Abs(got.Period-step) / step; err > 0.01 {
			t.Errorf("%d bpm: period = %.6f, want %.6f within 1%% (err %.2f%%)", bpm, got.Period, step, err*100)
		}
		if got.Coverage < minBeatCoverage {
			t.Errorf("%d bpm: coverage = %.3f on a perfect lattice", bpm, got.Coverage)
		}
		last := got.Beats[len(got.Beats)-1]
		if last > onsets[len(onsets)-1]+got.Period/2+1e-9 {
			t.Errorf("%d bpm: last beat %.3f is past the last click %.3f plus half a period", bpm, last, onsets[len(onsets)-1])
		}
	}
}

// TestBeatGridDoesNotScoreBetweenTheBeats is the trap the long-file fix walked
// into once: a least-squares phase averaged over onsets that alternate either side
// of the true beat lands exactly halfway, and at a quarter period every one of them
// sits *at* the tolerance distance — so a grid twice as slow as the music claims
// full coverage. The phase has to be anchored on an onset, which is what bestPhase
// does by trying the residues the media actually produced.
func TestBeatGridDoesNotScoreBetweenTheBeats(t *testing.T) {
	onsets := lattice(120, 0.5)
	got, ok := EstimateBeatGrid(onsets, 60)
	if !ok {
		t.Fatal("refused")
	}
	if math.Abs(got.Period-1.0) < 0.01 {
		t.Fatalf("period = %.4f: a 1 s grid explains half of a 0.5 s lattice, not all of it", got.Period)
	}
	if math.Abs(got.Period-0.5) > 0.005 {
		t.Fatalf("period = %.6f, want 0.5 (BPM %.1f)", got.Period, got.BPM)
	}
}

func TestBeatGridStillRefusesALongIrregularTrain(t *testing.T) {
	rnd := rand.New(rand.NewSource(7))
	var onsets []float64
	t0 := 0.0
	for i := 0; i < 300; i++ {
		t0 += 0.05 + rnd.Float64()*1.4
		if t0 > 60 {
			break
		}
		onsets = append(onsets, t0)
	}
	if _, ok := EstimateBeatGrid(onsets, 60); ok {
		t.Fatalf("%d irregular onsets across 60 s produced a grid — believing long files must not mean believing anything", len(onsets))
	}
}

// TestBeatGridWholeTemposOverAMinute is the sampled form of a 271-case sweep (every
// whole BPM from 30 to 300, run by hand: 0 refused, worst relative period error
// 0.61%). A minute of clicks at each tempo must come back at that tempo.
func TestBeatGridWholeTemposOverAMinute(t *testing.T) {
	// The short-lattice half of the range: many beats at a period the endpoint scan
	// never reaches, so this is the ladder's own work. The long-lattice half is in
	// TestBeatGridBelievesLongClickTrains.
	for _, bpm := range []int{30, 37, 44, 53, 67} {
		step := 60.0 / float64(bpm)
		n := int(60.0/step + 0.5)
		got, ok := EstimateBeatGrid(lattice(n, step), 60)
		if !ok {
			t.Errorf("%d bpm: a perfect minute of clicks was refused", bpm)
			continue
		}
		if err := math.Abs(got.Period-step) / step; err > 0.01 {
			t.Errorf("%d bpm: period = %.6f, want %.6f (err %.2f%%)", bpm, got.Period, step, err*100)
		}
	}
}

// TestBeatGridLeastSquaresSharpensALongJitteredBed keeps the fit in the pipeline.
// The span-anchored period alone already believes a jittered bed — so no other test
// would notice if the fit were dropped — but it lands five times further from the
// truth on a minute and a half of 120 BPM with +-90 ms of spread (measured: 0.499977
// with the least-squares fit, 0.499872 without). The threshold sits between those two
// numbers on purpose: it is a claim about the fit, not about tolerance.
func TestBeatGridLeastSquaresSharpensALongJitteredBed(t *testing.T) {
	rnd := rand.New(rand.NewSource(11))
	var onsets []float64
	for i := 0; i < 240; i++ {
		onsets = append(onsets, float64(i)*0.5+(rnd.Float64()*2-1)*0.09)
	}
	got, ok := EstimateBeatGrid(onsets, 120)
	if !ok {
		t.Fatal("a jittered 120 BPM bed was refused")
	}
	if err := math.Abs(got.Period - 0.5); err > 5e-5 {
		t.Fatalf("period = %.8f (err %.6f): the anchored span alone gets this far, the fit is what closes it", got.Period, err)
	}
}

// TestBeatGridFitsAPrefixAndBeatsTheWholeBed says what the fit budget is for, and
// what it does not touch: the grid is decided on the first maxBeatFitOnsets onsets,
// and the beats are then laid across all the evidence. A 2000-click bed is 14 minutes
// of 120 BPM, four times past the cap, so a grid that stopped at the fitted prefix
// would run out of beats while the music was still playing.
func TestBeatGridFitsAPrefixAndBeatsTheWholeBed(t *testing.T) {
	onsets := lattice(2000, 0.5)
	got, ok := EstimateBeatGrid(onsets, 1000)
	if !ok {
		t.Fatal("a 2000-click bed was refused")
	}
	if math.Abs(got.Period-0.5) > 1e-6 {
		t.Fatalf("period = %.8f, want exactly 0.5 from a fit on the prefix", got.Period)
	}
	last := onsets[len(onsets)-1]
	if got.Beats[len(got.Beats)-1] < last-0.5 {
		t.Fatalf("beats stop at %.1f, more than half a period short of the last click at %.1f", got.Beats[len(got.Beats)-1], last)
	}
	if len(got.Beats) < len(onsets)-2 {
		t.Fatalf("beats = %d for %d clicks; the grid was clipped to the fitted prefix", len(got.Beats), len(onsets))
	}
}
