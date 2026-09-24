package cli

import (
	"io"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/config"
)

// TestEncoderFlagValidatesAndOverrides: --encoder is the CLI layer of the
// config precedence (flag > config file), validated at parse time so a typo
// is refused before any work starts, and the override lands on the App's
// config where the render body's encoder resolution reads it.
func TestEncoderFlagValidatesAndOverrides(t *testing.T) {
	cfg := config.Default()
	a := &App{Cfg: cfg, Stdout: io.Discard, Stderr: io.Discard}

	// Validation happens before anything opens: no workspace, no db.
	err := cmdRender(a, []string{"whatever-project", "--encoder", "not_an_encoder"})
	if err == nil {
		t.Fatal("an unknown --encoder must be refused")
	}
	if !strings.Contains(err.Error(), "h264_vaapi") {
		t.Fatalf("the refusal should list the accepted values, got: %v", err)
	}

	// A valid name overrides the App config for this run.
	err = cmdRender(a, []string{"whatever-project", "--encoder", "H264_NVENC"})
	if err == nil {
		// The command fails later (no such project/db) — the flag itself was
		// accepted, which is what this assert cares about.
		if a.Cfg.Render.Encoder == "h264_nvenc" {
			return
		}
	}
	if a.Cfg.Render.Encoder != "h264_nvenc" {
		t.Fatalf("--encoder H264_NVENC did not reach the App config (got %q, err=%v)", a.Cfg.Render.Encoder, err)
	}
}
