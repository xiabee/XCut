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
