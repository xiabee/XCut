package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
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

// TestE2EAutoWithProxy: proxy_enabled + a small analysis width route
// analysis through a generated proxy; the full auto pipeline must succeed
// and leave exactly one fingerprint-keyed proxy in the workspace cache.
func TestE2EAutoWithProxy(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	root := t.TempDir()
	t.Setenv("XCUT_WORKSPACE", root)
	t.Setenv("XCUT_PROXY_ENABLED", "1")

	// Workspace config layer: analyze at 160 wide (fixture is 320 wide →
	// the proxy decision fires).
	wsCfg := filepath.Join(root, "config.json")
	if err := os.WriteFile(wsCfg, []byte(`{"resource":{"analysis_width":160}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	fixture := filepath.Join(root, "fixture.mp4")
	if _, err := testmedia.Generate(root, "fixture.mp4", testmedia.DefaultFixture(), 320, 240, 10); err != nil {
		t.Fatal(err)
	}

	run := func(args ...string) int {
		var stdout, stderr bytes.Buffer
		return Run(args, &stdout, &stderr)
	}
	run("init")
	if code := run("auto", fixture, "--project", "proxy-e2e", "--style", "generic_highlight"); code != 0 {
		t.Fatalf("auto with proxies failed (exit %d)", code)
	}

	entries, err := os.ReadDir(filepath.Join(root, "cache", "proxy"))
	if err != nil {
		t.Fatalf("proxy cache dir missing: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("proxy cache has %d entries, want exactly the fixture's proxy", len(entries))
	}

	// The source must be untouched and a rerun must reuse the proxy
	// (still exactly one entry).
	before, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if code := run("auto", fixture, "--project", "proxy-e2e", "--style", "generic_highlight"); code != 0 {
		t.Fatalf("second auto failed (exit %d)", code)
	}
	entries, err = os.ReadDir(filepath.Join(root, "cache", "proxy"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("rerun produced %d proxy entries, want 1 (reuse)", len(entries))
	}
	after, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("source media was modified by proxy-backed analysis")
	}
}

// TestCLIErrorOutputStaysUserSafe: a failing command must print the
// user-safe message only — the wrapped cause (absolute paths, tool
// diagnostics) belongs to the -v debug log, not the default error line.
func TestCLIErrorOutputStaysUserSafe(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XCUT_WORKSPACE", root)

	var stdout, stderr bytes.Buffer
	run := func(args ...string) int {
		stdout.Reset()
		stderr.Reset()
		return Run(args, &stdout, &stderr)
	}
	if code := run("init"); code != 0 {
		t.Fatalf("init failed: %s", stderr.String())
	}
	if code := run("project", "create", "errfmt"); code != 0 {
		t.Fatalf("project create failed: %s", stderr.String())
	}

	// The missing file lives under a marker directory: the os.Stat cause
	// embeds the absolute path, the user-safe message ("file does not
	// exist") must not.
	marker := filepath.Join(root, "LEAKMARKER", "clip.mp4")
	if code := run("import", "errfmt", marker); code == 0 {
		t.Fatal("import of a missing file must fail")
	}
	if !bytes.Contains(stderr.Bytes(), []byte("file does not exist")) {
		t.Fatalf("stderr must carry the user-safe message, got: %s", stderr.String())
	}
	// The dispatch's own error line must stay user-safe. Structured log
	// lines (level=...) are the diagnostics channel by design and do carry
	// the full cause — that is what "-v debug" documents, not a leak.
	for _, line := range strings.Split(stderr.String(), "\n") {
		if strings.HasPrefix(line, "xcut ") && strings.Contains(line, "LEAKMARKER") {
			t.Fatalf("user-facing error line leaked the raw cause: %s", line)
		}
	}
}
