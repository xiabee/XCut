package cli

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/testmedia"
)

// boundariesSidecar points the command at the repo's reference sidecar, which
// implements the scoreboard op. Skips when python or ffmpeg is missing — the
// scan is genuinely optional, and so is this test.
func boundariesSidecar(t *testing.T) string {
	t.Helper()
	// python3 first, the way worker.pythonInterpreter() resolves it: a Debian /
	// Ubuntu node has no bare `python`, and looking that up alone would skip
	// this path on the second platform instead of exercising it.
	if !commandOnPath("python3") && !commandOnPath("python") {
		t.Skip("no python interpreter on PATH")
	}
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Skip("cannot locate repo root")
	}
	return filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(thisFile))),
		"scripts", "xcut-ai-sidecar.py")
}

func commandOnPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// TestBoundariesScanListClear walks the operator's whole path on a fixture whose
// marks are known by construction: scan the scoreboard corner, see the marks,
// scan a still corner and see the scan replaced (with the misaim note), refuse
// a crop off the frame, then clear. The pipeline leg — marks actually ending
// clips — is covered in internal/pipeline.
func TestBoundariesScanListClear(t *testing.T) {
	script := boundariesSidecar(t)
	root := t.TempDir()
	t.Setenv("XCUT_WORKSPACE", root)
	t.Setenv("XCUT_AI_BIN", script)

	fixture, err := testmedia.GenerateScoreboard(root, "board.mp4", 320, 240, 10, 16, []float64{4, 12})
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}

	var stdout, stderr bytes.Buffer
	run := func(wantExit int, args ...string) string {
		t.Helper()
		stdout.Reset()
		stderr.Reset()
		code := Run(args, &stdout, &stderr)
		if code != wantExit {
			t.Fatalf("xcut %v exited %d, want %d\nstdout:\n%s\nstderr:\n%s",
				args, code, wantExit, stdout.String(), stderr.String())
		}
		return stdout.String()
	}

	run(0, "init")
	run(0, "project", "create", "bnd")
	run(0, "import", "bnd", fixture)

	// The scoreboard corner: two changes, hand-derived from the fixture above.
	out := run(0, "boundaries", "bnd", "--crop", "0,0,0.4,0.3")
	if !strings.Contains(out, "found 2 point boundaries") {
		t.Fatalf("scan output:\n%s", out)
	}
	out = run(0, "boundaries", "bnd")
	if !strings.Contains(out, "2 marks at 0.000,0.000,0.400,0.300") {
		t.Fatalf("listing after scan:\n%s", out)
	}

	// A still region is a result, not a failure — and it must replace the
	// earlier scan rather than add to it, with a note that says what 0 means.
	out = run(0, "boundaries", "bnd", "--crop", "0.6,0.65,0.4,0.35")
	if !strings.Contains(out, "found 0 point boundaries") ||
		!strings.Contains(out, "nothing changed in that region") {
		t.Fatalf("misaimed scan output:\n%s", out)
	}
	out = run(0, "boundaries", "bnd")
	if !strings.Contains(out, "0 marks at 0.600,0.650,0.400,0.350") {
		t.Fatalf("listing after the re-scan:\n%s", out)
	}

	run(1, "boundaries", "bnd", "--crop", "0.8,0.8,0.5,0.5")
	run(1, "boundaries", "bnd", "--crop", "0,0,0.4,0.3", "--clear")

	out = run(0, "boundaries", "bnd", "--clear")
	if !strings.Contains(out, "cleared score marks") || strings.Contains(out, "marks at") {
		t.Fatalf("clear output:\n%s", out)
	}
}
