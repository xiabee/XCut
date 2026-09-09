package render

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/testmedia"
	"github.com/xiabee/XCut/internal/timeline"
	"github.com/xiabee/XCut/internal/xcerr"
)

func requireTools(t *testing.T) media.Tools {
	t.Helper()
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	return media.Tools{FFmpeg: "ffmpeg", FFprobe: "ffprobe", Threads: 2}
}

func fixture(t *testing.T) string {
	t.Helper()
	p, err := testmedia.Generate(t.TempDir(), "src.mp4", testmedia.DefaultFixture(), 320, 240, 10)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func twoClipTimeline(src string) *timeline.Timeline {
	return &timeline.Timeline{
		Version: timeline.Version,
		Canvas:  timeline.Canvas{Width: 320, Height: 240, FPS: 15},
		Tracks: []timeline.Track{
			{ID: "v1", Kind: "video", Clips: []timeline.Clip{
				{ID: "c1", AssetID: "a", SourcePath: src, SourceStart: 0, SourceEnd: 3, TimelineStart: 0, Speed: 1, Volume: 1},
				{ID: "c2", AssetID: "a", SourcePath: src, SourceStart: 4, SourceEnd: 7, TimelineStart: 3, Speed: 1, Volume: 0.5},
			}},
		},
	}
}

func TestRenderProducesVerifiedMP4(t *testing.T) {
	tools := requireTools(t)
	src := fixture(t)
	tl := twoClipTimeline(src)
	outDir := t.TempDir()
	out := filepath.Join(outDir, "out.mp4")

	tmp := t.TempDir()
	err := Render(context.Background(), tl, Options{Tools: tools, TempDir: tmp}, out)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("output missing: %v", err)
	}
	// Independent verification (not the renderer's own).
	probe, err := media.ProbeFile(context.Background(), tools, out)
	if err != nil {
		t.Fatal(err)
	}
	if probe.VideoCodec == "" || !probe.HasAudio {
		t.Fatalf("streams missing: %+v", probe)
	}
	if probe.DurationSec < 5.4 || probe.DurationSec > 6.6 {
		t.Fatalf("duration %.2f, want ~6", probe.DurationSec)
	}
	if probe.Width != 320 || probe.Height != 240 {
		t.Fatalf("size %dx%d", probe.Width, probe.Height)
	}
	// Temp scratch must not be required anymore (caller cleans; we just check
	// partial never leaked into the output dir).
	if _, err := os.Stat(out + ".partial"); !os.IsNotExist(err) {
		t.Fatal("partial file left behind after success")
	}
}

func TestRenderFailureLeavesNoFinalFile(t *testing.T) {
	tools := requireTools(t)
	outDir := t.TempDir()
	out := filepath.Join(outDir, "out.mp4")

	// Missing source.
	tl := twoClipTimeline(filepath.Join(outDir, "missing.mp4"))
	err := Render(context.Background(), tl, Options{Tools: tools, TempDir: t.TempDir()}, out)
	if err == nil {
		t.Fatal("expected error for missing source")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("final file must not exist after failure")
	}

	// Corrupt source.
	bad := filepath.Join(t.TempDir(), "bad.mp4")
	_ = os.WriteFile(bad, []byte("garbage"), 0o644)
	tl2 := twoClipTimeline(bad)
	err = Render(context.Background(), tl2, Options{Tools: tools, TempDir: t.TempDir()}, out)
	if err == nil {
		t.Fatal("expected error for corrupt source")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("final file must not exist after failure")
	}
}

func TestRenderRejectsUnsupportedTransition(t *testing.T) {
	tools := requireTools(t)
	src := fixture(t)
	tl := twoClipTimeline(src)
	tl.Tracks[0].Clips[1].Transition = &timeline.Transition{Type: "swirl", Duration: 0.5}
	err := Render(context.Background(), tl, Options{Tools: tools, TempDir: t.TempDir()}, filepath.Join(t.TempDir(), "o.mp4"))
	if err == nil {
		t.Fatal("expected unsupported transition error")
	}
}

func TestRenderFadeTransition(t *testing.T) {
	tools := requireTools(t)
	src := fixture(t)
	tl := twoClipTimeline(src)
	// Fade between clip 1 and clip 2: fade-out end of c1, fade-in start of c2.
	tl.Tracks[0].Clips[0].Transition = &timeline.Transition{Type: "fade", Duration: 0.6}
	out := filepath.Join(t.TempDir(), "fade.mp4")
	err := Render(context.Background(), tl, Options{Tools: tools, TempDir: t.TempDir()}, out)
	if err != nil {
		t.Fatal(err)
	}
	probe, err := media.ProbeFile(context.Background(), tools, out)
	if err != nil {
		t.Fatal(err)
	}
	if probe.DurationSec < 5.4 || probe.DurationSec > 6.6 {
		t.Fatalf("fade render duration %.2f, want ~6", probe.DurationSec)
	}
}

func TestRenderRespectsContextCancellation(t *testing.T) {
	tools := requireTools(t)
	src := fixture(t)
	tl := twoClipTimeline(src)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := Render(ctx, tl, Options{Tools: tools, TempDir: t.TempDir()}, filepath.Join(t.TempDir(), "o.mp4"))
	if err == nil {
		t.Fatal("expected cancellation error")
	}
	_ = time.Millisecond
}

func TestRenderTempBudgetAborts(t *testing.T) {
	tools := requireTools(t)
	src := fixture(t)
	tl := twoClipTimeline(src)
	out := filepath.Join(t.TempDir(), "out.mp4")

	// A 1-byte budget fails at the first post-clip check — before any
	// combine stage, with no final file.
	err := Render(context.Background(), tl, Options{
		Tools: tools, TempDir: t.TempDir(), TempBudgetBytes: 1,
	}, out)
	if err == nil {
		t.Fatal("expected budget error")
	}
	if !xcerr.IsCode(err, xcerr.CodeResourceLimit) {
		t.Fatalf("err = %v, want resource_limit", err)
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Fatal("final file must not exist after budget abort")
	}

	// A generous budget renders normally.
	ok := Render(context.Background(), twoClipTimeline(src), Options{
		Tools: tools, TempDir: t.TempDir(), TempBudgetBytes: 1 << 30,
	}, out)
	if ok != nil {
		t.Fatalf("render over-budget-free scratch failed: %v", ok)
	}
}

func TestDurationTolerance(t *testing.T) {
	// Two frames + 5% relative: tight enough to catch truncated short
	// renders, loose enough for container round-off.
	if tol := durationTolerance(2.0, 15); !(tol > 0.2 && tol < 0.3) {
		t.Fatalf("tolerance(2s,15fps) = %.3f, want ~0.23", tol)
	}
	// A 1.52s probe for a 2s timeline must fail (old floor passed it).
	if diff := absF(1.52 - 2.0); diff <= durationTolerance(2.0, 15) {
		t.Fatal("24%% truncated short render would pass verify")
	}
	// Frame-boundary round-off stays inside the window.
	if diff := absF(1.97 - 2.0); diff > durationTolerance(2.0, 15) {
		t.Fatal("healthy 2s render would fail verify")
	}
	// Long content stays generous (5% dominates).
	if tol := durationTolerance(3600, 30); !(tol > 100 && tol < 200) {
		t.Fatalf("tolerance(1h,30fps) = %.1f, want ~180", tol)
	}
}
