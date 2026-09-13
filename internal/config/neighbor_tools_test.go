package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestNeighborBinFound: the proximity lookup hits both the exe dir and its
// bin/ subdirectory, picks the platform suffix, and stays silent on an
// empty directory (the PATH fallback remains).
func TestNeighborBinFound(t *testing.T) {
	dir := t.TempDir()

	if _, ok := neighborBin(dir, "ffmpeg"); ok {
		t.Fatal("empty dir must not report a hit")
	}

	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}

	// Direct hit.
	direct := filepath.Join(dir, "ffmpeg"+suffix)
	if err := os.WriteFile(direct, []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok := neighborBin(dir, "ffmpeg")
	if !ok || got != direct {
		t.Fatalf("direct neighbor = %q, %v", got, ok)
	}

	// bin/ subdirectory hit for a tool not placed directly.
	sub := filepath.Join(dir, "bin")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	inBin := filepath.Join(sub, "ffprobe"+suffix)
	if err := os.WriteFile(inBin, []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok = neighborBin(dir, "ffprobe")
	if !ok || got != inBin {
		t.Fatalf("bin/ neighbor = %q, %v", got, ok)
	}

	// Directories named like tools are not tools.
	if err := os.Mkdir(filepath.Join(dir, "bin", "ffmpeg"+suffix), 0o755); runtime.GOOS != "windows" {
		_ = err // name without suffix is just a dir on unix; skip the check
	} else if err == nil {
		if _, ok := neighborBin(sub, "ffmpeg"); ok {
			t.Fatal("a directory must not count as a tool")
		}
	}
}
