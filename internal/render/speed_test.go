package render

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/testmedia"
	"github.com/xiabee/XCut/internal/timeline"
)

// avgRGB decodes one frame at `at` seconds into 4x4 RGB and returns the
// per-channel averages — enough to tell the fixture's flat scene colors
// apart (red/green/blue/white).
func avgRGB(t *testing.T, path string, at float64) (r, g, b float64) {
	t.Helper()
	args := []string{
		"-hide_banner", "-nostdin", "-v", "error",
		"-ss", f3(at), "-i", path,
		"-frames:v", "1", "-vf", "scale=4x4",
		"-f", "rawvideo", "-pix_fmt", "rgb24", "-",
	}
	stdout, stderr, err := media.Run(context.Background(), testToolsX().FFmpeg, args...)
	if err != nil {
		t.Fatalf("frame grab at %.2fs: %v: %s", at, err, media.Tail(stderr, 200))
	}
	if len(stdout) < 16*3 {
		t.Fatalf("frame grab produced %d bytes", len(stdout))
	}
	var sr, sg, sb float64
	for i := 0; i < 16; i++ {
		sr += float64(stdout[i*3])
		sg += float64(stdout[i*3+1])
		sb += float64(stdout[i*3+2])
	}
	return sr / 16, sg / 16, sb / 16
}

func isGreen(r, g, b float64) bool { return g > 80 && g > r+40 && g > b+40 }

// speedTimeline builds a single-clip timeline over [0,srcEnd] at the given
// speed on the standard test canvas.
func speedTimeline(t *testing.T, dir string, srcEnd, speed float64) *timeline.Timeline {
	t.Helper()
	pathA, err := testmedia.Generate(dir, "a.mp4", testmedia.DefaultFixture(), 320, 240, 10)
	if err != nil {
		t.Fatal(err)
	}
	c := timeline.Clip{
		ID: "c1", AssetID: "c1", SourcePath: pathA,
		SourceStart: 0, SourceEnd: srcEnd, Speed: speed, Volume: 1,
	}
	return &timeline.Timeline{
		Version: timeline.Version,
		Canvas:  timeline.Canvas{Width: 320, Height: 240, FPS: 10},
		Tracks:  []timeline.Track{{ID: "v1", Kind: "video", Clips: []timeline.Clip{c}}},
	}
}

// TestRenderSpeedShowsFullRange: a speed=2 clip over the 4s source (red
// 0-2s, green 2-4s) must render ~2s AND show the range's second half — at
// t=1.5s the frame maps to source t=3.0s (green scene). The old truncating
// behavior would show red there.
func TestRenderSpeedShowsFullRange(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	dir := t.TempDir()
	tl := speedTimeline(t, dir, 4, 2)
	if diff := tl.Duration() - 2.0; diff < -1e-9 || diff > 1e-9 {
		t.Fatalf("timeline duration %g, want 2", tl.Duration())
	}

	out := filepath.Join(dir, "speed2.mp4")
	if err := Render(context.Background(), tl, Options{Tools: testToolsX(), TempDir: dir}, out); err != nil {
		t.Fatal(err)
	}
	probe, err := media.ProbeFile(context.Background(), testToolsX(), out)
	if err != nil {
		t.Fatal(err)
	}
	if d := probe.DurationSec; d < 1.7 || d > 2.3 {
		t.Fatalf("speed=2 output duration %.2fs, want ~2s", d)
	}
	// t=1.5s output → t=3.0s source → the green scene (truncation shows red).
	if r, g, b := avgRGB(t, out, 1.5); !isGreen(r, g, b) {
		t.Fatalf("frame at 1.5s is rgb(%.0f,%.0f,%.0f), want the green scene — speed is not applied", r, g, b)
	}
}

// TestRenderSpeedHalfExpands: a speed=0.5 clip over the 4s source renders
// 8s; output t=1.0 maps to source 0.5 (red) and t=6.0 to source 3.0 (green).
func TestRenderSpeedHalfExpands(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	dir := t.TempDir()
	tl := speedTimeline(t, dir, 4, 0.5)
	if diff := tl.Duration() - 8.0; diff < -1e-9 || diff > 1e-9 {
		t.Fatalf("timeline duration %g, want 8", tl.Duration())
	}

	out := filepath.Join(dir, "speed05.mp4")
	if err := Render(context.Background(), tl, Options{Tools: testToolsX(), TempDir: dir}, out); err != nil {
		t.Fatal(err)
	}
	probe, err := media.ProbeFile(context.Background(), testToolsX(), out)
	if err != nil {
		t.Fatal(err)
	}
	if d := probe.DurationSec; d < 7.4 || d > 8.6 {
		t.Fatalf("speed=0.5 output duration %.2fs, want ~8s", d)
	}
	if r, g, b := avgRGB(t, out, 1.0); !(r > 150 && r > g+80 && r > b+80) {
		t.Fatalf("frame at 1.0s is rgb(%.0f,%.0f,%.0f), want the red scene", r, g, b)
	}
	if r, g, b := avgRGB(t, out, 6.0); !isGreen(r, g, b) {
		t.Fatalf("frame at 6.0s is rgb(%.0f,%.0f,%.0f), want the green scene — speed is not applied", r, g, b)
	}
}

// TestRenderSpeedAudioNotSilent: the sped clip keeps audible audio (atempo,
// not a muted or empty track).
func TestRenderSpeedAudioNotSilent(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	dir := t.TempDir()
	tl := speedTimeline(t, dir, 4, 2)
	out := filepath.Join(dir, "speed2-audio.mp4")
	if err := Render(context.Background(), tl, Options{Tools: testToolsX(), TempDir: dir}, out); err != nil {
		t.Fatal(err)
	}
	_, stderr, err := media.Run(context.Background(), testToolsX().FFmpeg,
		"-hide_banner", "-nostdin", "-i", out, "-map", "0:a", "-af", "volumedetect", "-f", "null", "-")
	if err != nil {
		t.Fatalf("volumedetect: %v: %s", err, media.Tail(stderr, 200))
	}
	if !strings.Contains(string(stderr), "mean_volume") {
		t.Fatalf("volumedetect output missing mean_volume: %s", media.Tail(stderr, 300))
	}
	if strings.Contains(string(stderr), "mean_volume: -inf dB") {
		t.Fatal("sped clip audio is silent — atempo chain not applied")
	}
}

// TestRenderSpeedWithXfade: a sped clip joined by xfade to a normal clip
// validates, renders, and the durations compose (4/2 + 4 − 1 = 5s).
func TestRenderSpeedWithXfade(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	dir := t.TempDir()
	pathA, err := testmedia.Generate(dir, "a.mp4", testmedia.DefaultFixture()[:3], 320, 240, 10) // 6s
	if err != nil {
		t.Fatal(err)
	}
	c1 := timeline.Clip{ID: "c1", AssetID: "c1", SourcePath: pathA,
		SourceStart: 0, SourceEnd: 4, Speed: 2, Volume: 1, // 2s of content
		Transition: &timeline.Transition{Type: "xfade", Duration: 1}}
	c2 := timeline.Clip{ID: "c2", AssetID: "c2", SourcePath: pathA,
		SourceStart: 0, SourceEnd: 4, Speed: 1, Volume: 1, TimelineStart: 1} // 2 − 1 overlap
	tl := &timeline.Timeline{
		Version: timeline.Version,
		Canvas:  timeline.Canvas{Width: 320, Height: 240, FPS: 10},
		Tracks:  []timeline.Track{{ID: "v1", Kind: "video", Clips: []timeline.Clip{c1, c2}}},
	}
	if err := tl.Validate(nil); err != nil {
		t.Fatalf("speed+xfade timeline must validate: %v", err)
	}
	if diff := tl.Duration() - 5.0; diff < -1e-9 || diff > 1e-9 {
		t.Fatalf("timeline duration %g, want 5", tl.Duration())
	}

	out := filepath.Join(dir, "speed-xfade.mp4")
	if err := Render(context.Background(), tl, Options{Tools: testToolsX(), TempDir: dir}, out); err != nil {
		t.Fatal(err)
	}
	probe, err := media.ProbeFile(context.Background(), testToolsX(), out)
	if err != nil {
		t.Fatal(err)
	}
	if d := probe.DurationSec; d < 4.5 || d > 5.5 {
		t.Errorf("speed+xfade output duration %.2fs, want ~5s", d)
	}
}

// TestAtempoChain: factors stay inside atempo's portable [0.5, 2] window
// and multiply back to the requested speed.
func TestAtempoChain(t *testing.T) {
	cases := map[string]struct {
		speed float64
		want  int // number of atempo factors
	}{
		"half":   {0.5, 1},
		"slow":   {0.3, 2},
		"normal": {1, 1},
		"fast":   {1.5, 1},
		"double": {2, 1},
		"triple": {3, 2},
		"max":    {10, 4},
		"tiny":   {0.1, 4},
	}
	for name, tc := range cases {
		chain := atempoChain(tc.speed)
		if n := strings.Count(chain, "atempo="); n != tc.want {
			t.Errorf("%s: chain %q has %d factors, want %d", name, chain, n, tc.want)
		}
		product := 1.0
		for _, p := range strings.Split(chain, ",") {
			var f float64
			if _, err := fmt.Sscanf(p, "atempo=%f", &f); err != nil {
				t.Fatalf("%s: bad factor %q", name, p)
			}
			if f < 0.5 || f > 2 {
				t.Errorf("%s: factor %g outside [0.5,2]", name, f)
			}
			product *= f
		}
		if diff := product - tc.speed; diff < -1e-6*tc.speed || diff > 1e-6*tc.speed {
			t.Errorf("%s: chain product %g != speed %g", name, product, tc.speed)
		}
	}
}

// TestRenderSpeedOffsetSeek: a sped clip starting mid-file (SourceStart>0,
// past the first keyframe) must not lose its tail to the input -t window —
// [4,8] covers blue 4-6 + white 6-8; speed=4 renders 1s. Frame at 0.75s
// maps to source 7.0s → white; losing the tail would show blue.
func TestRenderSpeedOffsetSeek(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	dir := t.TempDir()
	pathA, err := testmedia.Generate(dir, "a.mp4", testmedia.DefaultFixture(), 320, 240, 10) // 8s
	if err != nil {
		t.Fatal(err)
	}
	tl := &timeline.Timeline{
		Version: timeline.Version,
		Canvas:  timeline.Canvas{Width: 320, Height: 240, FPS: 10},
		Tracks: []timeline.Track{{ID: "v1", Kind: "video", Clips: []timeline.Clip{{
			ID: "c1", AssetID: "c1", SourcePath: pathA,
			SourceStart: 4, SourceEnd: 8, Speed: 4, Volume: 1, // blue+white, 1s out
		}}}},
	}
	out := filepath.Join(dir, "offset.mp4")
	if err := Render(context.Background(), tl, Options{Tools: testToolsX(), TempDir: dir}, out); err != nil {
		t.Fatal(err)
	}
	probe, err := media.ProbeFile(context.Background(), testToolsX(), out)
	if err != nil {
		t.Fatal(err)
	}
	if d := probe.DurationSec; d < 0.85 || d > 1.15 {
		t.Fatalf("offset speed=4 output duration %.2fs, want ~1s", d)
	}
	// Frame at 0.75s maps to source 7.0s — white. Losing the tail to the seek
	// window would show blue here instead.
	r, g, b := avgRGB(t, out, 0.75)
	if !(r > 150 && g > 150 && b > 150) {
		t.Fatalf("frame at 0.75s is rgb(%.0f,%.0f,%.0f), want the white scene (source 7.0s)", r, g, b)
	}
}

// TestRenderRefusesUnsupportedShapes: the renderer loudly refuses timeline
// constructs it cannot honor (audio tracks, multi-track, effects) instead
// of silently mis-rendering them — same policy as unknown transitions.
func TestRenderRefusesUnsupportedShapes(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	dir := t.TempDir()
	pathA, err := testmedia.Generate(dir, "a.mp4", testmedia.DefaultFixture()[:2], 320, 240, 10)
	if err != nil {
		t.Fatal(err)
	}
	clip := func(id string) timeline.Clip {
		return timeline.Clip{ID: id, AssetID: id, SourcePath: pathA,
			SourceStart: 0, SourceEnd: 1, Speed: 1, Volume: 1}
	}
	base := func() *timeline.Timeline {
		return &timeline.Timeline{
			Version: timeline.Version,
			Canvas:  timeline.Canvas{Width: 320, Height: 240, FPS: 10},
			Tracks:  []timeline.Track{{ID: "v1", Kind: "video", Clips: []timeline.Clip{clip("c1")}}},
		}
	}

	cases := map[string]func(*timeline.Timeline){
		"audio track": func(tl *timeline.Timeline) {
			tl.Tracks[0].Kind = "audio"
		},
		"multi track": func(tl *timeline.Timeline) {
			tl.Tracks = append(tl.Tracks, timeline.Track{ID: "v2", Kind: "video", Clips: []timeline.Clip{clip("c2")}})
		},
		"effects": func(tl *timeline.Timeline) {
			tl.Tracks[0].Clips[0].Effects = []string{"sepia"}
		},
	}
	for name, mutate := range cases {
		tl := base()
		mutate(tl)
		if err := tl.Validate(nil); err != nil {
			t.Fatalf("%s: IR must stay valid (renderer refuses, not validation): %v", name, err)
		}
		out := filepath.Join(dir, "refused-"+strings.ReplaceAll(name, " ", "-")+".mp4")
		err := Render(context.Background(), tl, Options{Tools: testToolsX(), TempDir: dir}, out)
		if err == nil {
			t.Fatalf("%s: render must be refused", name)
		}
		if !strings.Contains(err.Error(), "not supported by the renderer yet") {
			t.Fatalf("%s: refusal should name the capability, got: %v", name, err)
		}
		if _, serr := os.Stat(out); serr == nil {
			t.Fatalf("%s: refused render must not create output", name)
		}
	}
}
