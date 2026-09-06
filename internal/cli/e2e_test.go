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
	run("auto", fixture, "--project", "e2e", "--style", "badminton_highlight", "--out", out)

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
