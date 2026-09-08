package media

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestHostileFilenamesAreJustArguments proves user text never becomes shell:
// a filename with shell/ffmpeg metacharacters is passed as a single argv
// element and round-trips through fingerprint + probe unharmed.
func TestHostileFilenamesAreJustArguments(t *testing.T) {
	tools := requireFFmpeg(t)
	dir := t.TempDir()

	// '&' '"' '\'' '$()' '`' spaces — all legal in NTFS/FAT names, all
	// dangerous only through a shell, which XCut never uses.
	name := `a&b "$(echo pwned)" 'q' ;|c.mp4`
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("not really a video"), 0o644); err != nil {
		t.Skipf("filesystem rejects hostile name: %v", err)
	}
	if !strings.Contains(filepath.Base(path), "&") {
		t.Skip("filesystem sanitized the name")
	}

	fp, err := Fingerprint(path)
	if err != nil || fp == "" {
		t.Fatalf("fingerprint failed: %v", err)
	}
	// Probe must fail cleanly as UnsupportedMedia (garbage content), with no
	// shell side effects.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := ProbeFile(ctx, tools, path); !xcerrIsUnsupported(err) {
		t.Fatalf("got %v, want UnsupportedMedia", err)
	}
}

func xcerrIsUnsupported(err error) bool {
	return err != nil && strings.Contains(err.Error(), "unsupported_media")
}

// TestProbeMissingToolchainError: a missing ffprobe binary must be reported
// as an environment problem ("not runnable"), never as unsupported media —
// that mislabel sent a real user chasing the wrong file.
func TestProbeMissingToolchainError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.mp4")
	if err := os.WriteFile(path, []byte("not really an mp4"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ProbeFile(context.Background(),
		Tools{FFprobe: "definitely-missing-ffprobe-binary", FFmpeg: "ffmpeg"}, path)
	if err == nil {
		t.Fatal("probe with missing ffprobe must fail")
	}
	if !strings.Contains(err.Error(), "not runnable") {
		t.Fatalf("error must say ffprobe is not runnable, got: %v", err)
	}
	if strings.Contains(err.Error(), "supported media") {
		t.Fatal("missing toolchain must not be mislabeled as unsupported media")
	}
}
