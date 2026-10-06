package media

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/xiabee/XCut/internal/config"
)

// exeSuffix keeps the fixtures honest about the platform's executable shape:
// NeighborBin and LookPath both refuse a data file named "ffmpeg" on Windows.
func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// TestResolveToolsFindsTheRepoPinnedLayout: the quality gate prefers a
// repo-local FFmpeg under .tools/ffmpeg/bin (check.ps1 PATH-prepends it,
// fetch-stock-ffmpeg.sh fills it), and a bare run in the same checkout used
// to see "ffmpeg not found" instead. With nothing configured, nothing on
// PATH, and nothing next to the executable, the resolver must find that
// layout relative to the working directory — without it ever overriding a
// choice the user or the environment made.
func TestResolveToolsFindsTheRepoPinnedLayout(t *testing.T) {
	// Swaps the working directory and the environment: not parallel.
	dir := t.TempDir()
	bin := filepath.Join(dir, ".tools", "ffmpeg", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	suffix := exeSuffix()
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		if err := os.WriteFile(filepath.Join(bin, name+suffix), []byte("stub"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	t.Setenv("PATH", "")
	t.Chdir(dir)

	got := ResolveTools(config.Default())
	if got.FFmpeg != filepath.Join(bin, "ffmpeg"+suffix) {
		t.Errorf("the resolver missed the pinned layout, got %q", got.FFmpeg)
	}
	if got.FFprobe != filepath.Join(bin, "ffprobe"+suffix) {
		t.Errorf("ffprobe missed the pinned layout, got %q", got.FFprobe)
	}
}

// TestResolveToolsExplicitConfigWinsOverTheLayout: a configured binary is the
// user's call. The repo layout is the last fallback, so a checkout carrying
// .tools/ffmpeg must not reach between an explicit absolute path and its
// owner — even when that path names something LookPath would reject.
func TestResolveToolsExplicitConfigOverTheLayout(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, ".tools", "ffmpeg", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	suffix := exeSuffix()
	if err := os.WriteFile(filepath.Join(bin, "ffmpeg"+suffix), []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}

	mine := filepath.Join(t.TempDir(), "mine"+suffix)
	if err := os.WriteFile(mine, []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", "")
	t.Chdir(dir)

	cfg := config.Default()
	cfg.FFmpeg.Bin = mine
	got := ResolveTools(cfg)
	if got.FFmpeg != mine {
		t.Errorf("the repo layout displaced an explicit config: %q", got.FFmpeg)
	}
}

// TestResolveToolsBareNamesWhenNothingResolves: with no config, no PATH and
// no layout, the resolver hands back the bare names for PATH lookup at exec
// time — exactly what it did before the repo-layout fallback existed.
func TestResolveToolsBareNamesWhenNothingResolves(t *testing.T) {
	t.Setenv("PATH", "")
	t.Chdir(t.TempDir())

	got := ResolveTools(config.Default())
	if got.FFmpeg != "ffmpeg" || got.FFprobe != "ffprobe" {
		t.Errorf("bare-name fallback drifted: ffmpeg=%q ffprobe=%q", got.FFmpeg, got.FFprobe)
	}
}
