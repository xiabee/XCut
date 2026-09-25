package render

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/timeline"
)

// TestRenderParallelClipsVerified drives the worker pool explicitly: more
// ClipWorkers than clips exercises the clamp, and the reel must be identical
// in shape to the serial render's (same duration, verified by the renderer
// itself and re-probed here).
func TestRenderParallelClipsVerified(t *testing.T) {
	tools := requireTools(t)
	src := fixture(t)
	tl := twoClipTimeline(src)
	out := filepath.Join(t.TempDir(), "parallel.mp4")

	err := Render(context.Background(), tl, Options{
		Tools:       tools,
		TempDir:     t.TempDir(),
		ClipWorkers: 4,
	}, out)
	if err != nil {
		t.Fatal(err)
	}
	probe, err := media.ProbeFile(context.Background(), tools, out)
	if err != nil {
		t.Fatal(err)
	}
	if probe.DurationSec < 5.4 || probe.DurationSec > 6.6 {
		t.Fatalf("parallel render duration %.2f, want ~6", probe.DurationSec)
	}
}

// TestVideoFilterChainOrder pins the speed-motivated filter order: the fps
// resample runs before scale/pad (dropping frames is cheaper than resampling
// them), setpts (speed) stays in front of fps, and the motion crop lands
// between fps and scale.
func TestVideoFilterChainOrder(t *testing.T) {
	tl := twoClipTimeline("src.mp4")
	tl.Tracks[0].Clips[0].Motion = &timeline.Motion{Zoom: 1.2, From: []float64{0.3, 0.3}, To: []float64{0.7, 0.7}}

	c := tl.Tracks[0].Clips[0]
	c.Speed = 1.25
	got := videoFilterChain(tl, c, 3.0, [2]float64{0.5, 0.5})
	wantFps := "fps=15"
	wantMotion := "crop="
	wantScale := "scale=320:240"
	iFps := index(got, wantFps)
	iMotion := index(got, wantMotion)
	iScale := index(got, wantScale)
	if iFps < 0 || iMotion < 0 || iScale < 0 {
		t.Fatalf("chain missing stages: %q", got)
	}
	if !(iFps < iMotion && iMotion < iScale) {
		t.Fatalf("fps must precede crop must precede scale: %q", got)
	}
	if !strings.HasPrefix(got, "setpts=") {
		t.Fatalf("speed must be the first stage: %q", got)
	}
}

func index(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
