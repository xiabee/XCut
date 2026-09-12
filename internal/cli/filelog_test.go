package cli

import (
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/config"
)

func TestServeLogLevelFlagWins(t *testing.T) {
	base := &App{Cfg: config.Default()}
	if got := serveLogLevel(base); got != slog.LevelInfo {
		t.Fatalf("default level = %v, want info", got)
	}
	if got := serveLogLevel(&App{Cfg: base.Cfg, Verbose: true}); got != slog.LevelDebug {
		t.Fatalf("-v level = %v, want debug", got)
	}
	if got := serveLogLevel(&App{Cfg: base.Cfg, Quiet: true}); got != slog.LevelWarn {
		t.Fatalf("-q level = %v, want warn", got)
	}
	// Config-derived levels still apply without flags.
	cfg := config.Default()
	cfg.Log.Level = "debug"
	if got := serveLogLevel(&App{Cfg: cfg}); got != slog.LevelDebug {
		t.Fatalf("config debug level = %v, want debug", got)
	}
}

// TestRotatePreservesHistoryUnderPinnedReader: a reader holding the live
// log open (no delete-share on Windows) used to make the rotation rename
// fail and the subsequent O_TRUNC wipe the history in place. Now a failed
// rename keeps appending to the held file — the history survives either
// way: moved to .1 (POSIX / rename worked) or still in place (Windows /
// rename pinned).
func TestRotatePreservesHistoryUnderPinnedReader(t *testing.T) {
	dir := t.TempDir()
	fl, err := newFileLogger(dir, "serve", 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fl.Close() })
	fl.Write([]byte("HISTORY-MARKER"))

	// Pin the live file the way an external tailer would.
	pinned, err := os.Open(fl.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pinned.Close() })

	fl.rotateLocked()

	live, err := os.ReadFile(fl.path)
	if err != nil {
		t.Fatal(err)
	}
	var rotated []byte
	if b, err := os.ReadFile(fl.path + ".1"); err == nil {
		rotated = b
	}
	combined := string(live) + string(rotated)
	if !strings.Contains(combined, "HISTORY-MARKER") {
		t.Fatalf("rotation history was destroyed:\nlive=%q rotated=%q", live, rotated)
	}
	// The logger must still be writable after the postponed rotation.
	if _, err := fl.Write([]byte("POST-ROTATE")); err != nil {
		t.Fatalf("logger dead after postponed rotation: %v", err)
	}
}
