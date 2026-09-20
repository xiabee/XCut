package render

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/testmedia"
)

func TestEscapeSubsPath(t *testing.T) {
	// What the filtergraph needs is OS-independent: an already-slashed path
	// with a drive letter must still have its colon escaped, and a quote must
	// still be escaped.
	if got := escapeSubsPath(`C:/temp dir/lyrics.ass`); got != `C\:/temp dir/lyrics.ass` {
		t.Fatalf("drive-colon escape = %q", got)
	}
	if got := escapeSubsPath(`/tmp/it's.ass`); got != `/tmp/it\'s.ass` {
		t.Fatalf("quote escape = %q", got)
	}
	// Turning separators into slashes is filepath.ToSlash's job, and it is by
	// definition platform-specific (a no-op where the separator already is
	// '/'), so only Windows can assert the conversion.
	if runtime.GOOS == "windows" {
		if got := escapeSubsPath(`C:\temp dir\lyrics.ass`); got != `C\:/temp dir/lyrics.ass` {
			t.Fatalf("windows separator conversion = %q", got)
		}
	}
}

// TestBurnSubtitles: the burn pass must survive a real libass render with
// duration preserved — and it must work when outPath IS inputPath (the
// pipeline burns the just-rendered file in place).
func TestBurnSubtitles(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	dir := t.TempDir()
	src, err := testmedia.Generate(dir, "fx.mp4", testmedia.DefaultFixture(), 320, 240, 10)
	if err != nil {
		t.Fatal(err)
	}
	subs := filepath.Join(dir, "subs.srt")
	srt := "1\n00:00:01,000 --> 00:00:03,000\nhello subs\n"
	if err := os.WriteFile(subs, []byte(srt), 0o644); err != nil {
		t.Fatal(err)
	}
	tools := media.Tools{FFmpeg: "ffmpeg", FFprobe: "ffprobe", Threads: 2}

	inProbe, err := media.ProbeFile(context.Background(), tools, src)
	if err != nil {
		t.Fatal(err)
	}

	// Separate output first.
	out := filepath.Join(dir, "burned.mp4")
	if err := BurnSubtitles(context.Background(), tools, src, subs, out); err != nil {
		t.Fatal(err)
	}
	outProbe, err := media.ProbeFile(context.Background(), tools, out)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(outProbe.DurationSec-inProbe.DurationSec) > 1.0 {
		t.Fatalf("burned duration %.2fs vs input %.2fs", outProbe.DurationSec, inProbe.DurationSec)
	}

	// In-place: the pipeline burns the rendered file itself.
	if err := BurnSubtitles(context.Background(), tools, src, subs, src); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(src + ".subs.partial"); !os.IsNotExist(err) {
		t.Fatal("burn partial left behind")
	}
}

// TestBurnSubtitlesMissingSubs: a missing subtitle file is a clean NotFound,
// not an ffmpeg syntax error.
func TestBurnSubtitlesMissingSubs(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	dir := t.TempDir()
	src, err := testmedia.Generate(dir, "fx.mp4", testmedia.DefaultFixture(), 320, 240, 10)
	if err != nil {
		t.Fatal(err)
	}
	tools := media.Tools{FFmpeg: "ffmpeg", FFprobe: "ffprobe", Threads: 2}
	err = BurnSubtitles(context.Background(), tools, src, filepath.Join(dir, "nope.srt"), filepath.Join(dir, "out.mp4"))
	if err == nil {
		t.Fatal("missing subs must fail")
	}
}
