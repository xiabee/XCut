package style

import (
	"math"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/event"
	"github.com/xiabee/XCut/internal/timeline"
)

// A framing plan changes what the viewer sees without changing what was chosen,
// so the policy is tested on both halves: which plan a mode produces, and that a
// style which asks for nothing still produces none.

func framingItems() []AssetEvents {
	return []AssetEvents{{
		Asset: AssetInfo{ID: "a1", Path: "a.mp4", DurationSec: 60,
			ROI: &MotionROI{X: 0.3, Y: 0.2, W: 0.5, H: 0.5}},
		Segments: []event.Segment{
			seg(2, 12, 0.5, -12), seg(20, 30, 0.5, -12), seg(38, 48, 0.5, -12),
		},
	}}
}

func framingReel(t *testing.T, mode string, zoom float64, items []AssetEvents) []timeline.Clip {
	t.Helper()
	p := testPreset()
	p.TargetDuration = 30
	p.CameraMotion = CameraMotion{Mode: mode, Zoom: zoom}
	if err := p.Validate(); err != nil {
		t.Fatalf("preset %q rejected: %v", mode, err)
	}
	tl, err := Build(p, "prj", items)
	if err != nil {
		t.Fatal(err)
	}
	return tl.Tracks[0].Clips
}

func TestFramingPolicyPerMode(t *testing.T) {
	for _, mode := range []string{"", FramingNone} {
		clips := framingReel(t, mode, 0, framingItems())
		if len(clips) == 0 {
			t.Fatalf("mode %q produced no clips at all", mode)
		}
		for i, c := range clips {
			if c.Motion != nil {
				t.Fatalf("mode %q gave clip %d a plan (%+v); asking for nothing must change nothing", mode, i, c.Motion)
			}
			if _, ok := c.Metadata["framing"]; ok {
				t.Fatalf("clip %d claims framing with the mode off", i)
			}
		}
	}

	punch := framingReel(t, FramingPunchIn, 0.7, framingItems())
	if len(punch) != 3 {
		t.Fatalf("want 3 clips, got %d", len(punch))
	}
	for i, c := range punch {
		if c.Motion == nil || c.Motion.Zoom != 0.7 || c.Motion.From != nil || c.Motion.To != nil {
			t.Fatalf("clip %d: punch_in plan = %+v, want zoom 0.7 with no centers", i, c.Motion)
		}
		if c.Metadata["framing"] != FramingPunchIn {
			t.Fatalf("clip %d metadata framing = %q", i, c.Metadata["framing"])
		}
	}

	drift := framingReel(t, FramingDrift, 0.8, framingItems())
	if drift[0].Motion == nil || drift[1].Motion == nil {
		t.Fatal("drift mode left a clip unframed")
	}
	// The pans alternate, and the endpoints are the plan's whole story.
	if drift[0].Motion.From[0] >= drift[0].Motion.To[0] {
		t.Fatalf("first drift should run left to right, got %+v", drift[0].Motion)
	}
	if drift[1].Motion.From[0] <= drift[1].Motion.To[0] {
		t.Fatalf("second drift should run the other way, got %+v", drift[1].Motion)
	}
	if drift[0].ID != "clip_1" || drift[0].Motion.Zoom != 0.8 {
		t.Fatalf("clip ordinal or zoom wrong: %+v", drift[0])
	}

	// roi aims at the region's center: x 0.3 + 0.5/2 = 0.55, y 0.2 + 0.5/2 = 0.45.
	roi := framingReel(t, FramingROI, 0.6, framingItems())
	want := [2]float64{0.55, 0.45}
	for i, c := range roi {
		if c.Motion == nil || len(c.Motion.From) != 2 {
			t.Fatalf("clip %d: roi plan missing: %+v", i, c.Motion)
		}
		if math.Abs(c.Motion.From[0]-want[0]) > 1e-9 || math.Abs(c.Motion.From[1]-want[1]) > 1e-9 {
			t.Fatalf("clip %d centered at %v, want the ROI center %v", i, c.Motion.From, want)
		}
		if c.Motion.To == nil || c.Motion.To[0] != c.Motion.From[0] {
			t.Fatalf("clip %d: an ROI plan is still, not a pan: %+v", i, c.Motion)
		}
	}

	// No region on the asset is not a reason to invent one.
	bare := framingItems()
	bare[0].Asset.ROI = nil
	for i, c := range framingReel(t, FramingROI, 0.6, bare) {
		if c.Motion != nil {
			t.Fatalf("clip %d got a plan aimed at nothing: %+v", i, c.Motion)
		}
	}
}

func TestCameraMotionValidated(t *testing.T) {
	p := testPreset()
	p.CameraMotion = CameraMotion{Mode: "zoomy", Zoom: 0.5}
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "camera_motion.mode") {
		t.Fatalf("an unknown mode must be named, got %v", err)
	}
	p = testPreset()
	p.CameraMotion = CameraMotion{Mode: FramingPunchIn}
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "camera_motion.zoom") {
		t.Fatalf("a mode without a window must be refused, got %v", err)
	}
	p = testPreset()
	p.CameraMotion = CameraMotion{Mode: FramingPunchIn, Zoom: 1.4}
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "camera_motion.zoom") {
		t.Fatalf("a window bigger than the frame must be refused, got %v", err)
	}
	// "none" needs no zoom: asking for nothing is a complete sentence.
	p = testPreset()
	p.CameraMotion = CameraMotion{Mode: FramingNone}
	if err := p.Validate(); err != nil {
		t.Fatalf("mode none rejected: %v", err)
	}
}
