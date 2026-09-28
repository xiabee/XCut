package render

import (
	"context"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/subs"
	"github.com/xiabee/XCut/internal/testmedia"
)

// The burn pass's output was verified by duration alone: libass's own layout
// — where the caption actually lands on the frame — was B5's last unmeasured
// claim. These tests burn a caption onto a black fixture, pull the frame back
// out as pixels, and measure it against the geometry KaraokeStyle.frame
// promises for a 720×1280 canvas (scale 1: Fontsize 48, margins 60/60/40,
// outline 2, shadow 1, Alignment 2 bottom-centre). Bounds are generous
// (outline + shadow + antialiasing bleed) because the claim under test is the
// layout contract — position, size, centring, margin line — not a font
// rasterizer's exact pixel grid.

const (
	pxCanvasW = 720
	pxCanvasH = 1280
	pxFont    = 48
	pxMarginL = 60
	pxMarginR = 60
	pxMarginV = 40
	// outline (2) + shadow (1) + antialiasing slack.
	pxBleed = 5
)

// brightBand returns the bounding box of the bright (caption) pixels in the
// frame. Black background, white text: anything over the threshold is glyph.
func brightBand(t *testing.T, path string) (image.Rectangle, int) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open frame: %v", err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatalf("decode frame png: %v", err)
	}
	b := img.Bounds()
	minX, minY := b.Max.X, b.Max.Y
	maxX, maxY := -1, -1
	count := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			// 8-bit sum > 240 of 765: glyph pixels only — the dark outline
			// (#101010) and the translucent shadow stay under it.
			if r+g+bl > 240*0x101 {
				count++
				if x < minX {
					minX = x
				}
				if x > maxX {
					maxX = x
				}
				if y < minY {
					minY = y
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}
	if count == 0 || maxX < 0 {
		t.Fatalf("no caption pixels found in %s — libass rendered nothing visible", path)
	}
	return image.Rect(minX, minY, maxX+1, maxY+1), count
}

// grabFrame extracts one PNG frame at the given time from a burned video.
func grabFrame(t *testing.T, tools media.Tools, video string, at float64) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "frame.png")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_, stderr, err := media.Run(ctx, tools.FFmpeg,
		"-hide_banner", "-nostdin", "-v", "error", "-y",
		"-ss", strconv.FormatFloat(at, 'f', 2, 64), "-i", video,
		"-frames:v", "1", out)
	if err != nil {
		t.Fatalf("frame extraction failed: %v: %s", err, media.Tail(stderr, 300))
	}
	return out
}

// TestBurnedCaptionLandsWhereTheGeometryClaims measures a one-line caption
// against the margin line, the side margins, the centring and the font size
// the ASS header declares for this canvas.
func TestBurnedCaptionLandsWhereTheGeometryClaims(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	dir := t.TempDir()
	src, err := testmedia.Generate(dir, "black.mp4",
		[]testmedia.Scene{{Seconds: 2, Color: "black", Frequency: 440}},
		pxCanvasW, pxCanvasH, 15)
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	tr := &subs.Transcript{Segments: []subs.Segment{
		{Start: 0.2, End: 1.2, Text: "HELLO CAPTION"},
	}}
	if err := subs.WriteCaptionASS(tr, subs.KaraokeStyle{Width: pxCanvasW, Height: pxCanvasH}, &sb); err != nil {
		t.Fatalf("write caption ass: %v", err)
	}
	assPath := filepath.Join(dir, "cap.ass")
	if err := os.WriteFile(assPath, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	tools := media.Tools{FFmpeg: "ffmpeg", FFprobe: "ffprobe", Threads: 2}
	burned := filepath.Join(dir, "burned.mp4")
	if err := BurnSubtitles(context.Background(), tools, "", src, assPath, burned); err != nil {
		t.Fatalf("burn: %v", err)
	}

	band, count := brightBand(t, grabFrame(t, tools, burned, 0.7))
	if count < 50 {
		t.Fatalf("only %d caption pixels — the caption is barely there", count)
	}
	// Bottom line: the block stays in the bottom band and never crosses the
	// margin line. Measured on libass: MarginV lands the *baseline* near the
	// margin, so glyph ink ends roughly half an em above it — the bound is
	// one line of slack above the line, hard stop at it. A caption sized or
	// placed for another frame fails here first.
	if band.Max.Y > pxCanvasH-pxMarginV+pxBleed {
		t.Errorf("caption bottom y=%d runs under the margin line (%d+%d)", band.Max.Y, pxCanvasH, pxMarginV)
	}
	if band.Max.Y < pxCanvasH-pxMarginV-pxFont {
		t.Errorf("caption bottom y=%d floats more than a line above the margin line", band.Max.Y)
	}
	// One line, glyphs about the declared size — not a band from another
	// scale.
	height := band.Dy()
	if height < pxFont/2 || height > 2*pxFont {
		t.Errorf("caption band height %d is not one line of a %dpx font", height, pxFont)
	}
	// Bottom-centre: horizontally centred, inside the side margins.
	if center := (band.Min.X + band.Max.X) / 2; math.Abs(float64(center-pxCanvasW/2)) > pxFont/2 {
		t.Errorf("caption centre x=%d is off the frame centre by more than half an em", center)
	}
	if band.Min.X < pxMarginL-pxBleed {
		t.Errorf("caption starts at x=%d, left of the margin (%d)", band.Min.X, pxMarginL)
	}
	if band.Max.X > pxCanvasW-pxMarginR+pxBleed {
		t.Errorf("caption ends at x=%d, right of the margin (%d)", band.Max.X, pxCanvasW-pxMarginR)
	}
}

// TestBurnedCaptionWrapsAboveTheMarginLine: the wrapped multi-line cue from
// the same layout rule must stay two-ish lines tall — not a stack running off
// the frame — and its bottom edge must respect the same margin line.
func TestBurnedCaptionWrapsAboveTheMarginLine(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	dir := t.TempDir()
	src, err := testmedia.Generate(dir, "black.mp4",
		[]testmedia.Scene{{Seconds: 2.5, Color: "black", Frequency: 440}},
		pxCanvasW, pxCanvasH, 15)
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	// 19 runes against a 12-rune line budget at this canvas: the shared
	// layout wraps it into a two-line cue, cued late enough that the frame
	// grab lands inside it.
	tr := &subs.Transcript{Segments: []subs.Segment{
		{Start: 1.3, End: 2.3, Text: "HELLO WORLD AGAIN X"},
	}}
	if err := subs.WriteCaptionASS(tr, subs.KaraokeStyle{Width: pxCanvasW, Height: pxCanvasH}, &sb); err != nil {
		t.Fatalf("write caption ass: %v", err)
	}
	assPath := filepath.Join(dir, "cap.ass")
	if err := os.WriteFile(assPath, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	tools := media.Tools{FFmpeg: "ffmpeg", FFprobe: "ffprobe", Threads: 2}
	burned := filepath.Join(dir, "burned.mp4")
	if err := BurnSubtitles(context.Background(), tools, "", src, assPath, burned); err != nil {
		t.Fatalf("burn: %v", err)
	}

	band, _ := brightBand(t, grabFrame(t, tools, burned, 1.7))
	height := band.Dy()
	// Two lines of a 48px font — taller than one line, nowhere near a
	// runaway stack.
	if height <= pxFont*3/2 {
		t.Errorf("wrapped cue band height %d does not read as two lines of a %dpx font", height, pxFont)
	}
	if height > pxFont*4 {
		t.Errorf("wrapped cue band height %d is a runaway stack, not a two-line cue", height)
	}
	if band.Max.Y > pxCanvasH-pxMarginV+pxBleed {
		t.Errorf("wrapped caption bottom y=%d runs under the margin line", band.Max.Y)
	}
}
