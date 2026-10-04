package style

import (
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/timeline"
)

// MotionFor is the only place a camera-motion name becomes geometry, because both
// the reel builder and the per-clip picker ask it. These cases pin the numbers —
// a client that offered its own arithmetic would be a second opinion nobody
// reconciles with the first.

// sameAspect is a 16:9 reel over a 16:9 source: the window's width fraction is
// the zoom itself, so a region that fits by height fits by width too — the
// shape where the fit guarantee has the least to do.
var sameAspect = FitFrame{CanvasW: 1920, CanvasH: 1080, SrcW: 1920, SrcH: 1080}

// verticalOverWide is sports_vertical's real fight: a 9:16 canvas over a 16:9
// source. The window is a third of the picture wide at zoom 1, which is where
// "centered on the region" and "the region is inside" part ways.
var verticalOverWide = FitFrame{CanvasW: 720, CanvasH: 1280, SrcW: 1920, SrcH: 1080}

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
		// The region already fits the same-aspect window at 0.85 (need = max(0.3,
		// 0.4) < 0.85), so the fit guarantee leaves the window it always drew.
		{FramingROI, 0, &timeline.Motion{Zoom: 0.85, From: []float64{0.4, 0.25}, To: []float64{0.4, 0.25}}},
	}
	for _, c := range cases {
		got, fit, err := MotionFor(c.mode, 0.85, roi, c.ordinal, sameAspect)
		if err != nil {
			t.Fatalf("MotionFor(%q, ordinal %d): %v", c.mode, c.ordinal, err)
		}
		if motionString(got) != motionString(c.want) {
			t.Errorf("MotionFor(%q, ordinal %d) = %s, want %s",
				c.mode, c.ordinal, motionString(got), motionString(c.want))
		}
		if c.mode == FramingROI && !fit.Fitted {
			t.Errorf("a region that fits came back unfitted: %+v", fit)
		}
	}
}

// TestMotionForCentresAnOffFrameRegion: a region can hang off the edge (a user drew
// it loosely, or the source was reframed). Both shapes must stay sane: with no
// frame known the plan is the centered window it always was (drawn middle clamped
// into the picture); with the shapes known the window covers the region's visible
// part — which for a two-frame-wide draw means the whole picture, at zoom 1.
func TestMotionForCentresAnOffFrameRegion(t *testing.T) {
	roi := &MotionROI{X: -0.5, Y: 0.9, W: 2.0, H: 0.5}
	got, fit, err := MotionFor(FramingROI, 0.8, roi, 0, FitFrame{})
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
	if got.Zoom != 0.8 {
		t.Errorf("unknown-frame plan zoom = %g, want the asked 0.8", got.Zoom)
	}
	if fit.Fitted {
		t.Errorf("a plan with no canvas to fit to claims fitted: %+v", fit)
	}
	if !strings.Contains(fit.Note, "unknown") {
		t.Errorf("unfitted note %q does not say why", fit.Note)
	}

	got, fit, err = MotionFor(FramingROI, 0.8, roi, 0, sameAspect)
	if err != nil {
		t.Fatal(err)
	}
	// Visible part: x 0..1, y 0.9..1. The window at zoom 1 is the whole picture,
	// which is the only window that holds a full-width draw.
	if got.Zoom != 1 {
		t.Errorf("fitted plan zoom = %g, want 1 (a full-width region needs the whole frame)", got.Zoom)
	}
	if !fit.Fitted {
		t.Errorf("a region that fits at zoom 1 came back unfitted: %+v", fit)
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
		_, _, err := MotionFor(c.mode, c.zoom, c.roi, 0, sameAspect)
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

// TestMotionForROIFit cases pin the guarantee's arithmetic. In every fitted case
// the plan must actually cover the region's on-frame part — computed here from
// the renderer's own formula, not from the function under test.
func TestMotionForROIFit(t *testing.T) {
	cases := []struct {
		name       string
		zoom       float64
		roi        MotionROI
		frame      FitFrame
		wantZoom   float64
		wantFit    bool
		wantInNote string
	}{
		{
			name:     "asked zoom already fits",
			zoom:     0.85,
			roi:      MotionROI{X: 0.2, Y: 0.1, W: 0.4, H: 0.3},
			frame:    sameAspect,
			wantZoom: 0.85,
			wantFit:  true,
		},
		{
			name:     "vertical canvas raises zoom to hold a narrow region",
			zoom:     0.85,
			roi:      MotionROI{X: 0.35, Y: 0.25, W: 0.3, H: 0.5},
			frame:    verticalOverWide,
			wantZoom: 0.9482, // ceil4(0.3 * (1920/1080) / (720/1280)) — rounded up so the window never rounds below the region
			wantFit:  true,
		},
		{
			name:     "preset's zoom 1 leaves nothing to raise",
			zoom:     1,
			roi:      MotionROI{X: 0.35, Y: 0.25, W: 0.3, H: 0.5},
			frame:    verticalOverWide,
			wantZoom: 1,
			wantFit:  true,
		},
		{
			name:       "whole-court draw cannot fit a 9:16 window",
			zoom:       0.85,
			roi:        MotionROI{X: 0.05, Y: 0.15, W: 0.9, H: 0.7},
			frame:      verticalOverWide,
			wantZoom:   0.85,
			wantFit:    false,
			wantInNote: "larger than this canvas",
		},
		{
			name:       "region entirely off the picture",
			zoom:       0.85,
			roi:        MotionROI{X: 1.5, Y: 0.2, W: 0.3, H: 0.3},
			frame:      sameAspect,
			wantZoom:   0.85,
			wantFit:    false,
			wantInNote: "outside the frame",
		},
	}
	for _, c := range cases {
		roi := c.roi
		got, fit, err := MotionFor(FramingROI, c.zoom, &roi, 0, c.frame)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got == nil {
			t.Fatalf("%s: no plan", c.name)
		}
		if got.Zoom != c.wantZoom {
			t.Errorf("%s: zoom = %g, want %g", c.name, got.Zoom, c.wantZoom)
		}
		if fit.Fitted != c.wantFit {
			t.Errorf("%s: fitted = %v (note %q), want %v", c.name, fit.Fitted, fit.Note, c.wantFit)
		}
		if c.wantInNote != "" && !strings.Contains(fit.Note, c.wantInNote) {
			t.Errorf("%s: note %q does not say %q", c.name, fit.Note, c.wantInNote)
		}
		if !fit.Fitted {
			continue
		}
		// The renderer's window: width fraction min(z*canvasA/srcA, 1) (the crop
		// is ih*z*canvasAspect pixels wide over an iw = ih*srcAspect source),
		// height z, centered at the plan's center and clamped into the frame.
		// Coverage is checked here, in the test, from that formula.
		srcA := float64(c.frame.SrcW) / float64(c.frame.SrcH)
		canvasA := float64(c.frame.CanvasW) / float64(c.frame.CanvasH)
		wWin := math.Min(got.Zoom*canvasA/srcA, 1)
		vX0, vX1 := math.Max(c.roi.X, 0), math.Min(c.roi.X+c.roi.W, 1)
		vY0, vY1 := math.Max(c.roi.Y, 0), math.Min(c.roi.Y+c.roi.H, 1)
		for _, pair := range [][]float64{got.From, got.To} {
			cx, cy := pair[0], pair[1]
			e0, e1 := math.Max(0, cx-wWin/2), math.Min(1, cx+wWin/2)
			if e0 > vX0+1e-9 || e1 < vX1-1e-9 {
				t.Errorf("%s: window x [%g, %g] does not cover the region [%g, %g]",
					c.name, e0, e1, vX0, vX1)
			}
			f0, f1 := math.Max(0, cy-got.Zoom/2), math.Min(1, cy+got.Zoom/2)
			if f0 > vY0+1e-9 || f1 < vY1-1e-9 {
				t.Errorf("%s: window y [%g, %g] does not cover the region [%g, %g]",
					c.name, f0, f1, vY0, vY1)
			}
		}
	}
}

// TestFramingPlanIsMotionForWithThePresetsInputs: the two callers must not be allowed
// to drift. The builder's plan for a preset is the picker's plan for the same mode,
// zoom and region — the same assertion made from the other side would be a copy.
func TestFramingPlanIsMotionForWithThePresetsInputs(t *testing.T) {
	roi := &MotionROI{X: 0.1, Y: 0.2, W: 0.5, H: 0.4}
	frames := []FitFrame{
		{}, // shapes unknown: the centered plan, both sides
		{CanvasW: 720, CanvasH: 1280, SrcW: 1920, SrcH: 1080},
		{CanvasW: 1920, CanvasH: 1080, SrcW: 1920, SrcH: 1080},
	}
	for _, frame := range frames {
		for _, mode := range []string{"", FramingNone, FramingPunchIn, FramingDrift, FramingROI} {
			for _, ordinal := range []int{0, 1, 2} {
				for _, hasROI := range []bool{true, false} {
					a := AssetInfo{DurationSec: 10, Width: frame.SrcW, Height: frame.SrcH}
					if hasROI {
						a.ROI = roi
					}
					p := &Preset{}
					p.CameraMotion.Mode = mode
					p.CameraMotion.Zoom = 0.9
					canvas := timeline.Canvas{Width: frame.CanvasW, Height: frame.CanvasH}
					want, wantFit, werr := MotionFor(mode, 0.9, a.ROI, ordinal, frame)
					got, gotFit := framingPlan(p, a, ordinal, canvas)
					if werr == nil && motionString(got) != motionString(want) {
						t.Errorf("framingPlan(%s, ordinal %d, roi=%v, frame %+v) = %s, MotionFor gives %s",
							mode, ordinal, hasROI, frame, motionString(got), motionString(want))
					}
					if werr != nil && got != nil {
						t.Errorf("framingPlan drew %s while MotionFor refuses it (%v)", mode, werr)
					}
					if werr == nil && gotFit != wantFit {
						t.Errorf("framingPlan fit = %+v, MotionFor fit = %+v", gotFit, wantFit)
					}
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
