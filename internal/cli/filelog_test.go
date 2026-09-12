package cli

import (
	"log/slog"
	"testing"

	"github.com/xiabee/XCut/internal/config"
)

// TestServeLogLevelFlagWins: `xcut serve -v` must raise the serve logger to
// debug even when config.json says info — flags are the most specific
// config layer, and the old code ignored them entirely in serve mode.
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
