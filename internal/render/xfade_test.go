package render

import (
	"context"
	"math"
	"path/filepath"
	"strings"
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
	// Validate is pure IR checking — it never opens the source file, so a
	// placeholder path keeps this test ffmpeg-free (the skip contract).
	const pathA = "a.mp4"
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

// xfTimeline builds an n-clip timeline from join specs. joins[i] describes
// the join between clip i and i+1: (type, duration); "cut"/"fade" joins
// place clips back-to-back, "xfade" overlaps by duration.
func xfTimeline(t *testing.T, dir string, joins [][2]any) (*timeline.Timeline, []timeline.Clip) {
	t.Helper()
	pathA, err := testmedia.Generate(dir, "a.mp4", testmedia.DefaultFixture()[:3], 320, 240, 10) // 6s
	if err != nil {
		t.Fatal(err)
	}
	clip := func(id string, start float64) timeline.Clip {
		return timeline.Clip{
			ID: id, AssetID: id, SourcePath: pathA,
			SourceStart: start, SourceEnd: start + 4, Speed: 1, Volume: 1,
		}
	}
	clips := []timeline.Clip{clip("c1", 0), clip("c2", 0), clip("c3", 2)}
	cursor := 4.0 // end of c1 without overlap
	for i, j := range joins {
		typ, _ := j[0].(string)
		d, _ := j[1].(float64)
		switch typ {
		case "xfade":
			clips[i].Transition = &timeline.Transition{Type: "xfade", Duration: d}
			cursor -= d
		case "fade":
			clips[i].Transition = &timeline.Transition{Type: "fade", Duration: d}
		default:
			clips[i].Transition = &timeline.Transition{Type: "cut"}
		}
		clips[i+1].TimelineStart = cursor
		cursor += 4
	}
	tl := &timeline.Timeline{
		Version: timeline.Version,
		Canvas:  timeline.Canvas{Width: 320, Height: 240, FPS: 10},
		Tracks:  []timeline.Track{{ID: "v1", Kind: "video", Clips: clips}},
	}
	if err := tl.Validate(nil); err != nil {
		t.Fatalf("mixed timeline must validate: %v", err)
	}
	return tl, clips
}

// TestBuildJoinGraphMixed: the graph must treat non-xfade joins as concat
// steps that consume no timeline time, and keep xfade offsets equal to the
// accumulated output duration minus the transition window. No ffmpeg needed
// — durations are injected directly.
func TestBuildJoinGraphMixed(t *testing.T) {
	clipX := func(d float64) timeline.Clip {
		return timeline.Clip{Transition: &timeline.Transition{Type: "xfade", Duration: d}}
	}
	clipC := func() timeline.Clip {
		return timeline.Clip{Transition: &timeline.Transition{Type: "cut"}}
	}
	clipF := func(d float64) timeline.Clip {
		return timeline.Clip{Transition: &timeline.Transition{Type: "fade", Duration: d}}
	}
	plain := func() timeline.Clip { return timeline.Clip{} }
	durs := []float64{4, 4, 4, 4}

	// xfade(1) → cut → xfade(2): offsets 3 and (4+4−1+4)−2 = 9.
	clips := []timeline.Clip{clipX(1), clipC(), clipX(2), plain()}
	filter, lastV, lastA := buildJoinGraph(clips, durs)
	for _, want := range []string{
		"[0:v][1:v]xfade=transition=fade:duration=1.000:offset=3.000[j1]",
		"[j1][ja1][2:v][2:a]concat=n=2:v=1:a=1[j2][ja2]",
		"[j2][3:v]xfade=transition=fade:duration=2.000:offset=9.000[j3]",
		"[0:a][1:a]acrossfade=d=1.000[ja1]",
		"[ja2][3:a]acrossfade=d=2.000[ja3]",
	} {
		if !strings.Contains(filter, want) {
			t.Errorf("filter missing %q:\n%s", want, filter)
		}
	}
	if lastV != "[j3]" || lastA != "[ja3]" {
		t.Errorf("final labels %s/%s, want [j3]/[ja3]", lastV, lastA)
	}

	// fade joins are pre-baked: they must be concat steps, never xfade.
	clips = []timeline.Clip{clipX(1), clipF(1), clipC(), plain()}
	filter, _, _ = buildJoinGraph(clips, durs)
	if n := strings.Count(filter, "concat=n=2:v=1:a=1"); n != 2 {
		t.Errorf("fade+cut joins must both be concat steps, got %d:\n%s", n, filter)
	}
	if n := strings.Count(filter, "xfade="); n != 1 {
		t.Errorf("exactly one xfade expected, got %d:\n%s", n, filter)
	}
}

// TestRenderMixedTransitions: xfade join + hard cut in one timeline renders
// to Σ durations − Σ xfade = 12 − 1 = 11s (cut consumes nothing).
func TestRenderMixedTransitions(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	dir := t.TempDir()
	tl, _ := xfTimeline(t, dir, [][2]any{{"xfade", 1.0}, {"cut", 0}})
	if want := 11.0; math.Abs(tl.Duration()-want) > 1e-9 {
		t.Fatalf("timeline duration %g, want %g", tl.Duration(), want)
	}

	out := filepath.Join(dir, "mixed.mp4")
	if err := Render(context.Background(), tl, Options{Tools: testToolsX(), TempDir: dir}, out); err != nil {
		t.Fatal(err)
	}
	probe, err := media.ProbeFile(context.Background(), testToolsX(), out)
	if err != nil {
		t.Fatal(err)
	}
	if d := probe.DurationSec; d < 10.5 || d > 11.5 {
		t.Errorf("mixed output duration %.2fs, want ~11s", d)
	}
	if probe.Width != 320 || probe.Height != 240 {
		t.Errorf("size %dx%d, want 320x240", probe.Width, probe.Height)
	}
}

// TestRenderMixedXfadeFade: an xfade join followed by a through-black fade
// join (pre-baked at normalize time) also renders in one graph.
func TestRenderMixedXfadeFade(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	dir := t.TempDir()
	tl, _ := xfTimeline(t, dir, [][2]any{{"xfade", 1.0}, {"fade", 1.0}})
	if want := 11.0; math.Abs(tl.Duration()-want) > 1e-9 {
		t.Fatalf("timeline duration %g, want %g", tl.Duration(), want)
	}

	out := filepath.Join(dir, "mixed-fade.mp4")
	if err := Render(context.Background(), tl, Options{Tools: testToolsX(), TempDir: dir}, out); err != nil {
		t.Fatal(err)
	}
	probe, err := media.ProbeFile(context.Background(), testToolsX(), out)
	if err != nil {
		t.Fatal(err)
	}
	if d := probe.DurationSec; d < 10.5 || d > 11.5 {
		t.Errorf("mixed fade output duration %.2fs, want ~11s", d)
	}
}
