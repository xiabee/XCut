package media

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/xiabee/XCut/internal/testmedia"
	"github.com/xiabee/XCut/internal/xcerr"
)

func requireFFmpeg(t *testing.T) Tools {
	t.Helper()
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	return Tools{FFmpeg: "ffmpeg", FFprobe: "ffprobe"}
}

func TestProbeFixture(t *testing.T) {
	tools := requireFFmpeg(t)
	dir := t.TempDir()
	path, err := testmedia.Generate(dir, "probe.mp4", testmedia.DefaultFixture(), 320, 240, 10)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	p, err := ProbeFile(ctx, tools, path)
	if err != nil {
		t.Fatal(err)
	}
	if p.DurationSec < 7.5 || p.DurationSec > 8.9 {
		t.Fatalf("duration = %.2f, want ~8", p.DurationSec)
	}
	if p.Width != 320 || p.Height != 240 {
		t.Fatalf("size = %dx%d", p.Width, p.Height)
	}
	if p.VideoCodec == "" || !p.HasAudio || p.AudioCodec == "" {
		t.Fatalf("codecs missing: %+v", p)
	}
	if p.FPS <= 0 {
		t.Fatalf("fps not parsed: %v", p.FPS)
	}
	if len(p.Raw) == 0 {
		t.Fatal("raw probe output missing")
	}
}

func TestProbeErrors(t *testing.T) {
	tools := requireFFmpeg(t)
	ctx := context.Background()

	// Nonexistent file → NotFound.
	if _, err := ProbeFile(ctx, tools, filepath.Join(t.TempDir(), "nope.mp4")); !xcerr.IsCode(err, xcerr.CodeNotFound) {
		t.Fatalf("missing file: got %v, want NotFound", err)
	}

	// Corrupt file (text with .mp4 name) → UnsupportedMedia.
	bad := filepath.Join(t.TempDir(), "bad.mp4")
	if err := os.WriteFile(bad, []byte("this is not a video file at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ProbeFile(ctx, tools, bad); !xcerr.IsCode(err, xcerr.CodeUnsupportedMedia) {
		t.Fatalf("corrupt file: got %v, want UnsupportedMedia", err)
	}

	// Audio-only file → UnsupportedMedia (no video stream).
	wav := filepath.Join(t.TempDir(), "tone.wav")
	ctxGen, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, _, err := Run(ctxGen, tools.FFmpeg, "-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-y", wav); err != nil {
		t.Fatalf("cannot generate wav: %v", err)
	}
	if _, err := ProbeFile(ctxGen, tools, wav); !xcerr.IsCode(err, xcerr.CodeUnsupportedMedia) {
		t.Fatalf("audio-only: got %v, want UnsupportedMedia", err)
	}
}

func TestFingerprintStability(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.bin")
	if err := os.WriteFile(p, []byte("hello fingerprint world"), 0o644); err != nil {
		t.Fatal(err)
	}
	fp1, err := Fingerprint(p)
	if err != nil {
		t.Fatal(err)
	}
	fp2, err := Fingerprint(p)
	if err != nil {
		t.Fatal(err)
	}
	if fp1 != fp2 {
		t.Fatal("fingerprint not stable for unchanged file")
	}

	// Content change → new fingerprint.
	if err := os.WriteFile(p, []byte("hello fingerprint worLd"), 0o644); err != nil {
		t.Fatal(err)
	}
	fp3, err := Fingerprint(p)
	if err != nil {
		t.Fatal(err)
	}
	if fp3 == fp1 {
		t.Fatal("content change did not change fingerprint")
	}
}

func TestFingerprintMissingFile(t *testing.T) {
	if _, err := Fingerprint(filepath.Join(t.TempDir(), "gone")); !xcerr.IsCode(err, xcerr.CodeNotFound) {
		t.Fatalf("got %v, want NotFound", err)
	}
}
