package player

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/xcerr"
)

// The bands are pure, saturated colors on distinct hue bins (red h≈0, green
// h≈120, blue h≈240 — HueBins=18 puts them in bins 0, 6, 12), so every
// assertion below is exact: no antialiasing, uniform rows, and the scorer's
// horizontal sampling stride hits the same pixel value everywhere.
const (
	bandRed   = 0
	bandGreen = 1
	bandBlue  = 2
)

var bandRGB = [3][3]byte{
	bandRed:   {220, 30, 30},
	bandGreen: {30, 220, 30},
	bandBlue:  {30, 30, 220},
}

// bandFrame paints rows [0,25) head-color, [25,65) torso-color, [65,H)
// legs-color — the builder's declared fractions. The `bands` argument says
// which color each region gets.
func bandFrame(t *testing.T, w, h int, head, torso, legs int) []byte {
	t.Helper()
	f := make([]byte, w*h*3)
	paint := func(lo, hi int, c int) {
		for y := lo; y < hi; y++ {
			for x := 0; x < w; x++ {
				px := (y*w + x) * 3
				f[px], f[px+1], f[px+2] = bandRGB[c][0], bandRGB[c][1], bandRGB[c][2]
			}
		}
	}
	paint(0, h*1/4, head)
	paint(h*1/4, h*65/100, torso)
	paint(h*65/100, h, legs)
	return f
}

// trainedSignature builds the multi-region model from the correctly ordered
// frame: head histogram = red, torso = green, legs = blue.
func trainedSignature(t *testing.T, w, h int) MultiRegionSignature {
	t.Helper()
	b := NewMultiRegionBuilder()
	if err := b.AddFeed(w, h, bandFrame(t, w, h, bandRed, bandGreen, bandBlue)); err != nil {
		t.Fatal(err)
	}
	mr, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	return mr
}

// TestMultiRegionBandsTrainAtTheDeclaredFractions pins the band split by its
// consequences: a correctly ordered frame scores 1.0 (every band matches its
// own histogram), a torso-only frame scores exactly the torso weight, and a
// head/torso swap scores exactly the legs weight. Under the thirds split this
// model was scored with before, the swap case scored 0.550 (measured, mutation
// run 2026-09-29) — the assertion is the geometry fix's guard.
func TestMultiRegionBandsTrainAtTheDeclaredFractions(t *testing.T) {
	const w, h = 12, 100
	mr := trainedSignature(t, w, h)

	score, err := mr.ScoreFrame(w, h, bandFrame(t, w, h, bandRed, bandGreen, bandBlue))
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(score-1) > 1e-6 {
		t.Errorf("correctly ordered frame scored %f, want 1", score)
	}

	// Only the torso band carries the shirt color; head and legs are black,
	// and the near-black skip leaves those bands with no samples → score 0.
	torsoOnly := make([]byte, w*h*3)
	paintRows := func(lo, hi, c int) {
		for y := lo; y < hi; y++ {
			for x := 0; x < w; x++ {
				px := (y*w + x) * 3
				torsoOnly[px], torsoOnly[px+1], torsoOnly[px+2] = bandRGB[c][0], bandRGB[c][1], bandRGB[c][2]
			}
		}
	}
	paintRows(h*1/4, h*65/100, bandGreen)
	score, err = mr.ScoreFrame(w, h, torsoOnly)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(score-regionWeights[1]) > 1e-6 {
		t.Errorf("torso-only frame scored %f, want exactly the torso weight %f", score, regionWeights[1])
	}

	// Head/torso colors swapped: head band (green) misses the red histogram,
	// torso band (red) misses the green histogram, legs still match — the
	// score collapses to the legs weight.
	score, err = mr.ScoreFrame(w, h, bandFrame(t, w, h, bandGreen, bandRed, bandBlue))
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(score-regionWeights[2]) > 1e-6 {
		t.Errorf("head/torso swap scored %f, want exactly the legs weight %f (the shipped thirds split scores 0.550 here)", score, regionWeights[2])
	}
}

// TestMultiRegionBuildRejectsAStarvedBand: a spot too short to give every
// band its two rows fails loudly at Build — the wiring precondition (spot
// height ≥ 8) the builder's skip rule implies.
func TestMultiRegionBuildRejectsAStarvedBand(t *testing.T) {
	b := NewMultiRegionBuilder()
	const w, h = 12, 6 // head [0,1) is skipped by the hi-lo<2 rule
	if err := b.AddFeed(w, h, bandFrame(t, w, h, bandRed, bandGreen, bandBlue)); err != nil {
		t.Fatal(err)
	}
	_, err := b.Build()
	if err == nil || !strings.Contains(err.Error(), "band 0") {
		t.Fatalf("starved head band: err=%v, want a band-0 failure", err)
	}
}

// TestScoreFrameRefusesBrokenInputs: a frame of the wrong size and an
// un-built (zero-value) signature are validation errors, not panics.
func TestScoreFrameRefusesBrokenInputs(t *testing.T) {
	const w, h = 12, 100
	mr := trainedSignature(t, w, h)

	if _, err := mr.ScoreFrame(w, h, make([]byte, w*h*3+1)); err == nil {
		t.Fatal("wrong-size frame must be refused")
	}

	var zero MultiRegionSignature
	_, err := zero.ScoreFrame(w, h, make([]byte, w*h*3))
	if err == nil || !errors.Is(err, errEmptySignature) {
		t.Fatalf("zero-value signature: err=%v, want errEmptySignature", err)
	}
	if !xcerr.IsCode(err, xcerr.CodeValidation) {
		t.Fatalf("zero-value signature error code: %v, want validation", err)
	}
}
