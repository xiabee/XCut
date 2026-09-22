package style

import (
	"strconv"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/timeline"
)

// MotionFor is the only place a camera-motion name becomes geometry, because both
// the reel builder and the per-clip picker ask it. These cases pin the numbers —
// a client that offered its own arithmetic would be a second opinion nobody
// reconciles with the first.

func TestMotionForEachMode(t *testing.T) {
	roi := &MotionROI{X: 0.2, Y: 0.1, W: 0.4, H: 0.3} // centre 0.4, 0.25
	cases := []struct {
		mode    string
		ordinal int
		want    *timeline.Motion
	}{
		{"", 0, nil},
		{FramingNone, 3, nil},
		{FramingPunchIn, 0, &timeline.Motion{Zoom: 0.85}},
		{FramingDrift, 0, &timeline.Motion{Zoom: 0.85, From: []float64{0.35, 0.5}, To: []float64{0.65, 0.5}}},
		// The alternation is the whole point of the ordinal: a reel that pans every
		// clip the same way reads as a metronome.
		{FramingDrift, 1, &timeline.Motion{Zoom: 0.85, From: []float64{0.65, 0.5}, To: []float64{0.35, 0.5}}},
		{FramingROI, 0, &timeline.Motion{Zoom: 0.85, From: []float64{0.4, 0.25}, To: []float64{0.4, 0.25}}},
	}
	for _, c := range cases {
		got, err := MotionFor(c.mode, 0.85, roi, c.ordinal)
		if err != nil {
			t.Fatalf("MotionFor(%q, ordinal %d): %v", c.mode, c.ordinal, err)
		}
		if motionString(got) != motionString(c.want) {
			t.Errorf("MotionFor(%q, ordinal %d) = %s, want %s",
				c.mode, c.ordinal, motionString(got), motionString(c.want))
		}
	}
}

// TestMotionForCentresAnOffFrameRegion: a region can hang off the edge (a user drew
// it loosely, or the source was reframed). The window still has to be a position
// inside the picture — the renderer clamps too, but the plan should not arrive
// asking for it.
func TestMotionForCentresAnOffFrameRegion(t *testing.T) {
	got, err := MotionFor(FramingROI, 0.8, &MotionROI{X: -0.5, Y: 0.9, W: 2.0, H: 0.5}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("a region that exists produced no plan")
	}
	for _, pair := range [][]float64{got.From, got.To} {
		for _, v := range pair {
			if v < 0 || v > 1 {
				t.Errorf("window coordinate %g is outside the frame: %v / %v", v, got.From, got.To)
			}
		}
	}
}

func TestMotionForRefusesWhatItCannotDraw(t *testing.T) {
	for _, c := range []struct {
		mode, want string
		zoom       float64
		roi        *MotionROI
	}{
		{mode: FramingROI, want: "region", zoom: 0.85},
		{mode: FramingDrift, want: "window", zoom: 0},
		{mode: FramingDrift, want: "window", zoom: -0.2},
		{mode: FramingPunchIn, want: "window", zoom: 1.5},
		{mode: "orbit", want: "not one of", zoom: 0.85},
	} {
		_, err := MotionFor(c.mode, c.zoom, c.roi, 0)
		if err == nil {
			t.Errorf("MotionFor(%q, zoom %g, roi %v) produced a plan; it cannot draw one",
				c.mode, c.zoom, c.roi)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("MotionFor(%q) refused with %q, which does not say what to do about it",
				c.mode, err.Error())
		}
	}
}

// TestFramingPlanIsMotionForWithThePresetsInputs: the two callers must not be allowed
// to drift. The builder's plan for a preset is the picker's plan for the same mode,
// zoom and region — the same assertion made from the other side would be a copy.
func TestFramingPlanIsMotionForWithThePresetsInputs(t *testing.T) {
	roi := &MotionROI{X: 0.1, Y: 0.2, W: 0.5, H: 0.4}
	for _, mode := range []string{"", FramingNone, FramingPunchIn, FramingDrift, FramingROI} {
		for _, ordinal := range []int{0, 1, 2} {
			for _, hasROI := range []bool{true, false} {
				a := AssetInfo{DurationSec: 10}
				if hasROI {
					a.ROI = roi
				}
				p := &Preset{}
				p.CameraMotion.Mode = mode
				p.CameraMotion.Zoom = 0.9
				want, werr := MotionFor(mode, 0.9, a.ROI, ordinal)
				got := framingPlan(p, a, ordinal)
				if werr == nil && motionString(got) != motionString(want) {
					t.Errorf("framingPlan(%s, ordinal %d, roi=%v) = %s, MotionFor gives %s",
						mode, ordinal, hasROI, motionString(got), motionString(want))
				}
				if werr != nil && got != nil {
					t.Errorf("framingPlan drew %s while MotionFor refuses it (%v)", mode, werr)
				}
			}
		}
	}
}

func motionString(m *timeline.Motion) string {
	if m == nil {
		return "nil"
	}
	return strings.Join([]string{num(m.Zoom), coords(m.From), coords(m.To)}, " ")
}

func coords(v []float64) string {
	if v == nil {
		return "-"
	}
	parts := make([]string, len(v))
	for i, f := range v {
		parts[i] = num(f)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func num(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}
