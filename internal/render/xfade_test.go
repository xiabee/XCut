package render

import (
	"context"
	"math"
	"path/filepath"
	"testing"

	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/testmedia"
	"github.com/xiabee/XCut/internal/timeline"
)

func testToolsX() media.Tools { return media.Tools{FFmpeg: "ffmpeg", FFprobe: "ffprobe", Threads: 2} }

// buildXfadeTimeline makes a 3-clip timeline: 4s source windows joined by
// 1s xfades → timeline spans [0, 10] (Σ 12s − 2×1s).
func buildXfadeTimeline(t *testing.T, dir string) *timeline.Timeline {
	t.Helper()
	pathA, err := testmedia.Generate(dir, "a.mp4", testmedia.DefaultFixture()[:3], 320, 240, 10) // 6s
	if err != nil {
		t.Fatal(err)
	}
	pathB, err := testmedia.Generate(dir, "b.mp4", testmedia.DefaultFixture()[2:], 320, 240, 10) // 4s
	if err != nil {
		t.Fatal(err)
	}
	clip := func(id, path string, start float64) timeline.Clip {
		return timeline.Clip{
			ID: id, AssetID: id, SourcePath: path,
			SourceStart: start, SourceEnd: start + 4, Speed: 1, Volume: 1,
		}
	}
	c1 := clip("c1", pathA, 0)
	c2 := clip("c2", pathB, 0)
	c3 := clip("c3", pathA, 2) // within the 6s source
	// Outgoing-clip convention: the transition on clip i joins i → i+1.
	c1.Transition = &timeline.Transition{Type: "xfade", Duration: 1}
	c2.Transition = &timeline.Transition{Type: "xfade", Duration: 1}
	c2.TimelineStart = 3 // 4 − 1
	c3.TimelineStart = 6 // 7 − 1
	return &timeline.Timeline{
		Version: timeline.Version,
		Canvas:  timeline.Canvas{Width: 320, Height: 240, FPS: 10},
		Tracks:  []timeline.Track{{ID: "v1", Kind: "video", Clips: []timeline.Clip{c1, c2, c3}}},
	}
}

// TestRenderXfade: three 4s clips joined by 1s xfades must render to a
// ~10s file (Σ durations − Σ transitions) with both streams, on canvas.
func TestRenderXfade(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	dir := t.TempDir()
	tl := buildXfadeTimeline(t, dir)

	if err := tl.Validate(nil); err != nil {
		t.Fatalf("xfade timeline must validate: %v", err)
	}
	if want := 10.0; math.Abs(tl.Duration()-want) > 1e-9 {
		t.Fatalf("timeline duration %g, want %g", tl.Duration(), want)
	}

	out := filepath.Join(dir, "xfade.mp4")
	if err := Render(context.Background(), tl, Options{Tools: testToolsX(), TempDir: dir}, out); err != nil {
		t.Fatal(err)
	}

	probe, err := media.ProbeFile(context.Background(), testToolsX(), out)
	if err != nil {
		t.Fatal(err)
	}
	// Duration semantics: Σ 12s − 2s = 10s (tolerance for AAC/xfade edges).
	if d := probe.DurationSec; d < 9.5 || d > 10.5 {
		t.Errorf("xfade output duration %.2fs, want ~10s", d)
	}
	if probe.VideoCodec == "" {
		t.Error("no video stream")
	}
	if probe.Width != 320 || probe.Height != 240 {
		t.Errorf("size %dx%d, want 320x240", probe.Width, probe.Height)
	}
}

// TestValidateXfadeRules: overlaps are allowed only when an xfade transition
// with a matching duration sits on the outgoing clip.
func TestValidateXfadeRules(t *testing.T) {
	dir := t.TempDir()
	pathA, err := testmedia.Generate(dir, "a.mp4", testmedia.DefaultFixture(), 320, 240, 10)
	if err != nil {
		t.Fatal(err)
	}
	clip := func(id string, timelineStart float64) timeline.Clip {
		return timeline.Clip{
			ID: id, AssetID: id, SourcePath: pathA,
			SourceStart: 0, SourceEnd: 4, Speed: 1, Volume: 1,
			TimelineStart: timelineStart,
		}
	}

	ok := &timeline.Timeline{
		Version: timeline.Version,
		Canvas:  timeline.Canvas{Width: 320, Height: 240, FPS: 10},
		Tracks: []timeline.Track{{ID: "v1", Kind: "video", Clips: []timeline.Clip{
			func() timeline.Clip {
				c := clip("c1", 0)
				c.Transition = &timeline.Transition{Type: "xfade", Duration: 1}
				return c
			}(),
			clip("c2", 3),
		}}},
	}
	if err := ok.Validate(nil); err != nil {
		t.Fatalf("valid xfade rejected: %v", err)
	}

	// Mismatched overlap: placement says 1.5s, transition says 1s.
	badOverlap := &timeline.Timeline{
		Version: timeline.Version,
		Canvas:  timeline.Canvas{Width: 320, Height: 240, FPS: 10},
		Tracks: []timeline.Track{{ID: "v1", Kind: "video", Clips: []timeline.Clip{
			func() timeline.Clip {
				c := clip("c1", 0)
				c.Transition = &timeline.Transition{Type: "xfade", Duration: 1}
				return c
			}(),
			clip("c2", 2.5),
		}}},
	}
	if err := badOverlap.Validate(nil); err == nil {
		t.Fatal("xfade overlap mismatch must be rejected")
	}

	// Plain overlap without a transition stays invalid.
	plain := &timeline.Timeline{
		Version: timeline.Version,
		Canvas:  timeline.Canvas{Width: 320, Height: 240, FPS: 10},
		Tracks: []timeline.Track{{ID: "v1", Kind: "video", Clips: []timeline.Clip{
			clip("c1", 0), clip("c2", 3),
		}}},
	}
	if err := plain.Validate(nil); err == nil {
		t.Fatal("transition-less overlap must stay rejected")
	}
}
