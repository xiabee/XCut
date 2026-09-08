package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/xiabee/XCut/internal/testmedia"
)

// TestE2EAutoPipeline exercises the entire product surface through the real
// CLI layer against a real FFmpeg: init → project create → auto (import →
// analyze → timeline → render) → ffprobe-verifiable output. Skipped when
// ffmpeg is absent.
func TestE2EAutoPipeline(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}

	// Isolated workspace + config inside the test temp dir.
	root := t.TempDir()
	t.Setenv("XCUT_WORKSPACE", root)
	// No config file: defaults apply (loopback listen, safe resource limits).

	// Fixture: 4 color scenes with sine tones, 320x240@10.
	fixture := filepath.Join(root, "fixture.mp4")
	if _, err := testmedia.Generate(root, "fixture.mp4", testmedia.DefaultFixture(), 320, 240, 10); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(root, "out", "highlight.mp4")
	var stdout, stderr bytes.Buffer
	run := func(args ...string) {
		stdout.Reset()
		stderr.Reset()
		code := Run(args, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("xcut %v failed (exit %d)\nstdout:\n%s\nstderr:\n%s",
				args, code, stdout.String(), stderr.String())
		}
	}

	run("init")
	// generic_highlight: renders on any audio. badminton_highlight v2 is
	// rally-mode — it correctly yields nothing on this tone-only fixture
	// (no transients); the rally pipeline has its own fixture-based test.
	run("auto", fixture, "--project", "e2e", "--style", "generic_highlight", "--out", out)

	// Output must exist (and was ffprobe-verified inside the renderer).
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("render output missing: %v", err)
	}

	// The final output was already ffprobe-verified by the renderer; here we
	// assert the job bookkeeping saw the full lifecycle.
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"jobs", "e2e"}, &stdout, &stderr); code != 0 {
		t.Fatalf("jobs failed: %s", stderr.String())
	}
	for _, want := range []string{"import", "analyze", "timeline", "render"} {
		if !bytes.Contains(stdout.Bytes(), []byte(want)) {
			t.Fatalf("jobs output missing %q:\n%s", want, stdout.String())
		}
	}
}

// TestE2ERenderRefusesSourceOverwrite: pointing --out at an imported source
// file must fail the command and leave the original media byte-identical
// (imports are referenced in place — there is no copy to fall back on).
func TestE2ERenderRefusesSourceOverwrite(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	root := t.TempDir()
	t.Setenv("XCUT_WORKSPACE", root)

	fixture := filepath.Join(root, "fixture.mp4")
	if _, err := testmedia.Generate(root, "fixture.mp4", testmedia.DefaultFixture(), 320, 240, 10); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}

	run := func(args ...string) int {
		var stdout, stderr bytes.Buffer
		return Run(args, &stdout, &stderr)
	}
	run("init")
	if code := run("auto", fixture, "--project", "guard", "--style", "generic_highlight"); code != 0 {
		t.Fatalf("auto setup failed (exit %d)", code)
	}

	// The refusal must be a clean command failure — no output file, no
	// damaged source.
	if code := run("render", "guard", "--out", fixture); code == 0 {
		t.Fatal("render --out pointing at the imported source must fail")
	}
	after, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("source media was modified by a refused render")
	}
	if _, err := os.Stat(filepath.Join(root, "fixture.mp4.partial")); err == nil {
		t.Fatal("refused render must not leave a .partial next to the source")
	}
}
