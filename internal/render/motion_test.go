package render

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/testmedia"
	"github.com/xiabee/XCut/internal/timeline"
)

// A framing plan is only worth having if the rendered pixels change. The three
// tests below go from the cheapest to the most convincing: the filter text, the
// command the product actually handed FFmpeg, and what came out of the encoder.

func TestMotionFilterText(t *testing.T) {
	// 640x360 canvas (16:9), zoom 0.5 → window is half the source height, and a
	// 16:9 window on it: 0.5 * 16/9 = 0.88889 of the source width.
	got := motionFilter(&timeline.Motion{Zoom: 0.5, From: []float64{0.25, 0.25}, To: []float64{0.75, 0.75}},
		640, 360, 4)
	if !strings.HasPrefix(got, "crop=") {
		t.Fatalf("filter does not start with a crop stage: %s", got)
	}
	if !strings.Contains(got, "min(trunc(ih*0.88889/2)*2,iw)") {
		t.Fatalf("window width lost the canvas aspect (want 0.5*16/9): %s", got)
	}
	if !strings.Contains(got, "trunc(ih*0.50000/2)*2") {
		t.Fatalf("window height lost the zoom: %s", got)
	}
	for _, tc := range []struct{ axis, clamp string }{{"x=", "iw-ow)"}, {"y=", "ih-oh)"}} {
		i := strings.Index(got, tc.axis)
		if i < 0 {
			t.Fatalf("no %s in %s", tc.axis, got)
		}
		expr := got[i:]
		if !strings.Contains(expr, "*t/") {
			t.Fatalf("a drifting %s expression has no time term, so the frame would never move: %s", tc.axis, got)
		}
		if !strings.Contains(expr, "min(max(") {
			t.Fatalf("%s is not clamped at the near edge: %s", tc.axis, got)
		}
		if !strings.Contains(expr, tc.clamp) {
			t.Fatalf("%s is not clamped at the far edge (%s): %s", tc.axis, tc.clamp, got)
		}
	}

	// A still punch-in must not ask for per-frame work it cannot do.
	still := motionFilter(&timeline.Motion{Zoom: 0.6}, 320, 240, 3)
	if strings.Contains(still, "*t/") {
		t.Fatalf("a plan with one center still animates: %s", still)
	}
	if !strings.Contains(still, "crop=") {
		t.Fatalf("a punch-in lost its crop stage: %s", still)
	}

	// The vertical reframe is the same arithmetic with a tall canvas: a 9:16
	// window over any source takes 0.5625 of its height as width, which is what
	// turns a 16:9 broadcast into a portrait shot rather than a letterbox.
	vert := motionFilter(&timeline.Motion{Zoom: 1}, 270, 480, 4)
	if !strings.Contains(vert, "trunc(ih*0.56250/2)*2") {
		t.Fatalf("a 9:16 canvas gave the window the wrong width: %s", vert)
	}
	if !strings.Contains(vert, "trunc(ih*1.00000/2)*2") {
		t.Fatalf("a 9:16 plan should still span the source's height: %s", vert)
	}
}

// The command line is what FFmpeg actually executes, and the absence of a crop
// stage for a clip that asked for none is the regression half of this feature.
func TestRenderCommandCarriesTheFramingPlan(t *testing.T) {
	requireTools(t)
	src := fixture(t)

	renderWith := func(t *testing.T, tl *timeline.Timeline) string {
		t.Helper()
		// A fresh file per render: the child appends, so reusing one would make
		// the "no crop for a plain clip" half read the *previous* command.
		argvPath := filepath.Join(t.TempDir(), "argv.log")
		t.Setenv("XCUT_FAKE_FFMPEG", "1")
		t.Setenv("XCUT_FAKE_FFMPEG_ARGV", argvPath)
		tools := media.Tools{FFmpeg: os.Args[0], FFprobe: "ffprobe", Threads: 2}
		out := filepath.Join(t.TempDir(), "out.mp4")
		err := Render(context.Background(), tl, Options{Tools: tools, TempDir: t.TempDir()}, out)
		if err == nil {
			t.Fatal("the stand-in FFmpeg must fail the render")
		}
		raw, rerr := os.ReadFile(argvPath)
		if rerr != nil {
			t.Fatalf("the child never reported its arguments: %v", rerr)
		}
		return string(raw)
	}

	motion := twoClipTimeline(src)
	motion.Tracks[0].Clips[0].Motion = &timeline.Motion{Zoom: 0.5, From: []float64{0.3, 0.3}, To: []float64{0.7, 0.7}}
	got := renderWith(t, motion)
	if !strings.Contains(got, "crop=") {
		t.Fatalf("no crop stage reached the command line:\n%s", got)
	}
	if !strings.Contains(got, "*t/") {
		t.Fatalf("the crop was passed as a still window; a drift must carry its time term:\n%s", got)
	}

	plain := renderWith(t, twoClipTimeline(src))
	if strings.Contains(plain, "crop=") {
		t.Fatalf("a clip with no framing plan grew a crop stage:\n%s", plain)
	}
}

// TestDriftRenderActuallyMovesTheFrame is the claim that matters: content leaves
// the frame. The fixture is a testsrc2 patch in the TOP-LEFT quadrant over a still
// black canvas, so a window that drifts to the bottom-right must go bright to
// dark — and the same source rendered without a plan must not.
func TestDriftRenderActuallyMovesTheFrame(t *testing.T) {
	tools := requireTools(t)
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	const size = 240 // even on both axes; the content fills 0..120
	src, err := testmedia.GenerateMotionCorner(t.TempDir(), "corner.mp4", size, size, 15, 6)
	if err != nil {
		t.Fatal(err)
	}
	plan := func(m *timeline.Motion) *timeline.Timeline {
		return &timeline.Timeline{
			Version: timeline.Version,
			Canvas:  timeline.Canvas{Width: size, Height: size, FPS: 15},
			Tracks: []timeline.Track{{ID: "v1", Kind: "video", Clips: []timeline.Clip{{
				ID: "c1", AssetID: "a", SourcePath: src, SourceStart: 0, SourceEnd: 6,
				TimelineStart: 0, Speed: 1, Volume: 0, Motion: m,
			}}}},
		}
	}

	drift := meanYAVG(t, tools, render(t, tools, plan(&timeline.Motion{
		Zoom: 0.5, From: []float64{0.25, 0.25}, To: []float64{0.75, 0.75}})))
	still := meanYAVG(t, tools, render(t, tools, plan(nil)))

	if len(drift) < 20 || len(still) < 20 {
		t.Fatalf("too few frames sampled to compare anything: drift=%d still=%d", len(drift), len(still))
	}
	head, tail := quarter(drift, 0), quarter(drift, 3)
	if head <= 0 {
		t.Fatalf("the drift render showed no content to begin with (YAVG %.2f) — fixture or plan changed", head)
	}
	if ratio := tail / head; ratio > 0.5 {
		t.Fatalf("a window drifting from the content quadrant to the far corner left %.0f%% of the brightness: the crop did not move (head %.2f tail %.2f)",
			ratio*100, head, tail)
	}
	sHead, sTail := quarter(still, 0), quarter(still, 3)
	if d := math.Abs(sTail - sHead); d > sHead*0.25 {
		t.Fatalf("the frameless render's brightness changed by %.0f%% on its own (%.2f → %.2f): the comparison would not be about the crop",
			d/sHead*100, sHead, sTail)
	}
	t.Logf("drift YAVG %.2f → %.2f (%.0f%%); still %.2f → %.2f",
		head, tail, tail/head*100, sHead, sTail)
}

// render writes into a throwaway directory and returns the output path.
func render(t *testing.T, tools media.Tools, tl *timeline.Timeline) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "out.mp4")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := Render(ctx, tl, Options{Tools: tools, TempDir: t.TempDir()}, out); err != nil {
		t.Fatalf("render: %v", err)
	}
	return out
}

// meanYAVG reads the per-frame luma mean back out of the rendered file with
// FFmpeg's own signalstats, so the measurement is of the pixels and not of the
// filter text.
func meanYAVG(t *testing.T, tools media.Tools, path string) []float64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	// `file=-` rather than a path: a filter argument splits options on ":", so a
	// Windows path ("C:\Users\…") would be parsed as three options and a
	// backslash-escaped path still carries the colon. The null muxer writes
	// nothing to stdout, so the stats can have it.
	out, err := media.RunCombined(ctx, tools.FFmpeg,
		"-hide_banner", "-v", "error", "-i", path,
		"-vf", "signalstats,metadata=print:key=lavfi.signalstats.YAVG:file=-",
		"-f", "null", "-")
	if err != nil {
		t.Fatalf("signalstats over %s: %v (%s)", filepath.Base(path), err, media.Tail(out, 300))
	}
	var vals []float64
	for _, line := range strings.Split(string(out), "\n") {
		i := strings.Index(line, "YAVG=")
		if i < 0 {
			continue
		}
		v, perr := strconv.ParseFloat(strings.TrimSpace(line[i+len("YAVG="):]), 64)
		if perr != nil {
			continue
		}
		vals = append(vals, v)
	}
	if len(vals) == 0 {
		t.Fatal("signalstats printed no YAVG values")
	}
	return vals
}

// quarter averages the first (which=0) or last (which=3) quarter of the frames.
func quarter(vals []float64, which int) float64 {
	n := len(vals) / 4
	if n < 1 {
		n = 1
	}
	var from, to int
	switch which {
	case 0:
		from, to = 0, n
	default:
		from, to = len(vals)-n, len(vals)
	}
	var sum float64
	for _, v := range vals[from:to] {
		sum += v
	}
	return sum / float64(to-from)
}
