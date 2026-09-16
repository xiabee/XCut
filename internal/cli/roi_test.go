package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/testmedia"
)

// TestROISetClearList: the CLI surface of the per-asset motion ROI — the
// UI picker writes the same field, but scripts have no browser. Drives a
// real workspace with a real imported fixture, skipped without ffmpeg.
func TestROISetClearList(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	root := t.TempDir()
	t.Setenv("XCUT_WORKSPACE", root)
	fixture := filepath.Join(root, "fixture.mp4")
	if _, err := testmedia.Generate(root, "fixture.mp4", testmedia.DefaultFixture(), 160, 120, 4); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	run := func(args ...string) {
		stdout.Reset()
		stderr.Reset()
		if code := Run(args, &stdout, &stderr); code != 0 {
			t.Fatalf("xcut %v failed (exit %d)\nstdout:\n%s\nstderr:\n%s", args, code, stdout.String(), stderr.String())
		}
	}

	run("init")
	run("project", "create", "roip")
	run("import", "roip", fixture)

	// Single-asset project: no --asset needed. Set, verify listed, clear.
	run("roi", "roip", "--set", "0.03,0.38,0.60,0.62")
	if out := stdout.String(); !strings.Contains(out, "0.030,0.380,0.600,0.620") {
		t.Fatalf("set output missing the ROI:\n%s", out)
	}
	// The timeline stage consumes the asset row; a bad ROI must be refused
	// before it can poison segmentation.
	var code int
	code = Run([]string{"roi", "roip", "--set", "1.2,0,0.1,0.1"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("out-of-range ROI accepted")
	}
	code = Run([]string{"roi", "roip", "--set", "0.5,0.5,0.6,0.6"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("ROI exceeding 1.0 accepted")
	}
	run("roi", "roip", "--clear")
	if out := stdout.String(); !strings.Contains(out, "ROI —") {
		t.Fatalf("clear output missing:\n%s", out)
	}
	if err := os.RemoveAll(filepath.Join(root, "no-such")); err != nil {
		t.Fatal(err)
	}
}
