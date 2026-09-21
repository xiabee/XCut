package cli

import (
	"bytes"
	"os"
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
	// The spacing line is what lets a person judge the scan without re-running
	// anything: two changes 8 s apart, from 4 s to 12 s.
	if !strings.Contains(out, "2 boundaries, 4.0s..12.0s, median gap 8.0s, tightest 8.0s") {
		t.Fatalf("scan output missing the spacing summary:\n%s", out)
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
	// Clearing the region is what removes the measurement with it, so the
	// listing must fall back to "nothing set" rather than showing old marks.
	if !strings.Contains(out, "cleared score region and marks") || strings.Contains(out, "marks at") {
		t.Fatalf("clear output:\n%s", out)
	}
}

// TestAutoAppliesScoreboardRegion is the one-shot operator's path: a crop passed
// to `auto` must land on the imported assets, be measured by the analyze stage,
// and survive into the project — the same pair of facts the step-by-step flow
// writes, with no manual `xcut boundaries` call in between.
func TestAutoAppliesScoreboardRegion(t *testing.T) {
	script := boundariesSidecar(t)
	root := t.TempDir()
	t.Setenv("XCUT_WORKSPACE", root)
	t.Setenv("XCUT_AI_BIN", script)
	// Changing colours across the frame: enough signal for generic_highlight
	// to cut, and the cropped corner changes too, so a mark exists to find.
	fixture, err := testmedia.Generate(root, "scenes.mp4", testmedia.DefaultFixture(), 320, 240, 10)
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}

	var stdout, stderr bytes.Buffer
	run := func(wantExit int, args ...string) string {
		t.Helper()
		stdout.Reset()
		stderr.Reset()
		if code := Run(args, &stdout, &stderr); code != wantExit {
			t.Fatalf("xcut %v exited %d, want %d\nstdout:\n%s\nstderr:\n%s",
				args, code, wantExit, stdout.String(), stderr.String())
		}
		return stdout.String()
	}

	run(0, "init")
	out := run(0, "auto", fixture, "--project", "one", "--style", "generic_highlight",
		"--duration", "4", "--score-crop", "0,0,0.4,0.3",
		"--out", filepath.Join(root, "reel.mp4"))
	if !strings.Contains(out, "scoreboard region 0,0,0.4,0.3 on 1 asset(s)") {
		t.Fatalf("auto output lacks the region line:\n%s", out)
	}

	// The measurement is the point: analyze must have stored boundaries for the
	// region auto set, on the asset row the timeline then reads.
	out = run(0, "boundaries", "one")
	if !strings.Contains(out, "marks at 0.000,0.000,0.400,0.300") {
		t.Fatalf("analyze did not measure the region auto set:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(root, "reel.mp4")); err != nil {
		t.Fatalf("auto rendered nothing: %v", err)
	}

	// The one-shot has to carry the same explanation `xcut timeline` gives: an
	// ambitious --duration answered by a much shorter reel must not fail
	// silently, because that is exactly the command a first-time user runs.
	out = run(0, "auto", fixture, "--project", "long", "--style", "generic_highlight",
		"--duration", "600", "--out", filepath.Join(root, "long.mp4"))
	if !strings.Contains(out, "the cut took") {
		t.Fatalf("auto did not explain a reel shorter than asked:\n%s", out)
	}

	// A malformed region is refused before any media work starts — and the
	// refusal has to name the unit. Pixel coordinates are the natural instinct
	// (ffmpeg's crop filter takes them, and they are what a screen ruler reads),
	// so "x+w <= 1" alone left a reader guessing.
	for _, tc := range []struct{ crop, needle string }{
		// A pixel rectangle for a 1280x720 frame: the shape a reader reaches for
		// first, and the one that must be told what unit the flag wants.
		{"550,560,180,70", "fractions of the frame (0..1), not pixels"},
		{"0.8,0.8,0.5,0.5", "fractions of the frame (0..1), not pixels"},
		{"not,a,rect", "must be x,y,w,h (normalized 0..1)"},
	} {
		stdout.Reset()
		stderr.Reset()
		if code := Run([]string{"auto", fixture, "--project", "bad", "--score-crop", tc.crop}, &stdout, &stderr); code != 1 {
			t.Fatalf("region %q was accepted (exit %d)", tc.crop, code)
		}
		msg := stderr.String() + stdout.String()
		if !strings.Contains(msg, tc.needle) {
			t.Errorf("refusal of %q does not say %q, it says:\n%s", tc.crop, tc.needle, msg)
		}
	}
}
