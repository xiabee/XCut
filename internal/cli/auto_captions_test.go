package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/testmedia"
)

// TestAutoCaptionsInTheOneShot: `xcut auto --subs=on` is the whole operator story in
// one command — reel, captions, burn. Two facts matter: the transcript runs after the
// timeline (so the caption box is styled for the canvas the same run just declared),
// and a run that did not ask for captions produces none.
func TestAutoCaptionsInTheOneShot(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	root := t.TempDir()
	t.Setenv("XCUT_WORKSPACE", root)
	t.Setenv("XCUT_AI_BIN", fakeTranscriptSidecar(t, false)) // line timings, no syllables

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
	out := run(0, "auto", fixture, "--project", "captioned", "--style", "generic_highlight",
		"--duration", "4", "--subs=on", "--out", filepath.Join(root, "reel.mp4"))
	if !strings.Contains(out, "==> subtitles (transcribed") {
		t.Fatalf("the one-shot never said it transcribed:\n%s", out)
	}

	files, err := filepath.Glob(filepath.Join(root, "projects", "*", "subtitles.ass"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("want the run's one caption file, found %d: %v", len(files), files)
	}
	ass, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	body := string(ass)
	// The ordering property, in the CLI's own words: generic_highlight's canvas,
	// not the writer's shipped 1280×720 reference.
	for _, want := range []string{"PlayResX: 1920", "PlayResY: 1080", "Style: Caption,"} {
		if !strings.Contains(body, want) {
			t.Errorf("the captions the one shot wrote lack %q:\n%.400s", want, body)
		}
	}
	if strings.Contains(body, `\k`) {
		t.Errorf("nothing timed a syllable, yet the burn file carries karaoke tags:\n%.400s", body)
	}
	if fi, err := os.Stat(filepath.Join(root, "reel.mp4")); err != nil || fi.Size() == 0 {
		t.Errorf("the run rendered nothing: %v", err)
	}

	// The same command without --subs must leave the captions alone — the flag has
	// to be the only thing deciding whether a sidecar gets called.
	out = run(0, "auto", fixture, "--project", "silent", "--style", "generic_highlight",
		"--duration", "4", "--out", filepath.Join(root, "silent.mp4"))
	if strings.Contains(out, "==> subtitles") {
		t.Fatalf("a run that did not ask for captions transcribed anyway:\n%s", out)
	}
	after, err := filepath.Glob(filepath.Join(root, "projects", "*", "subtitles.ass"))
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 1 {
		t.Errorf("caption files = %d after a --subs-less run, want still 1: %v", len(after), after)
	}
}

// TestAutoCaptionsNameTheirSource: transcription answers about one media
// file, but `xcut auto` accepts several inputs — with two, the captions come
// from the first and the header has to say so. A line that said "this run's
// input" over a two-input reel would be a lie about the second file's audio,
// and the user would find out from watching the reel.
func TestAutoCaptionsNameTheirSource(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	root := t.TempDir()
	t.Setenv("XCUT_WORKSPACE", root)
	t.Setenv("XCUT_AI_BIN", fakeTranscriptSidecar(t, false))

	first, err := testmedia.Generate(root, "scenes.mp4", testmedia.DefaultFixture(), 320, 240, 8)
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	second, err := testmedia.Generate(root, "other.mp4", testmedia.DefaultFixture(), 320, 240, 8)
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"init"}, &stdout, &stderr); code != 0 {
		t.Fatalf("init: %s", stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"auto", first, second, "--project", "twoinputs", "--style", "generic_highlight",
		"--duration", "4", "--subs=on", "--out", filepath.Join(root, "two.mp4")}, &stdout, &stderr); code != 0 {
		t.Fatalf("auto with two inputs exited %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "transcribed from scenes.mp4") {
		t.Errorf("the header does not name the file the captions come from:\n%s", out)
	}
	if !strings.Contains(out, "the first of this run's 2 inputs; the others' audio is not captioned") {
		t.Errorf("the header does not say the second input's audio was left out:\n%s", out)
	}
}

// TestAutoSubsRefusesWithoutASidecar: --subs=on is a request for a measurement the
// machine may not be able to take. The command must stop with the reason rather than
// render an uncaptioned reel and call it done.
func TestAutoSubsRefusesWithoutASidecar(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	root := t.TempDir()
	t.Setenv("XCUT_WORKSPACE", root)
	t.Setenv("XCUT_AI_BIN", "")
	fixture, err := testmedia.Generate(root, "scenes.mp4", testmedia.DefaultFixture(), 320, 240, 6)
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	// PATH narrowed to the directory the media tools actually live in: ffmpeg stays
	// runnable (so the run fails where it is supposed to, not at the import), and a
	// sidecar installed somewhere else on this machine cannot make the "no sidecar"
	// premise come and go between runs.
	tools := map[string]bool{}
	for _, exe := range []string{"ffmpeg", "ffprobe"} {
		p, lerr := exec.LookPath(exe)
		if lerr != nil {
			t.Skipf("%s not on PATH: %v", exe, lerr)
		}
		tools[filepath.Dir(p)] = true
	}
	var dirs []string
	for d := range tools {
		dirs = append(dirs, d)
	}
	t.Setenv("PATH", strings.Join(dirs, string(os.PathListSeparator)))
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"init"}, &stdout, &stderr); code != 0 {
		t.Fatalf("init: %s", stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code := Run([]string{"auto", fixture, "--project", "bare", "--style", "generic_highlight",
		"--duration", "3", "--subs=on", "--out", filepath.Join(root, "bare.mp4")}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("--subs=on succeeded with no sidecar anywhere:\n%s", stdout.String())
	}
	// The command's own line, not the job logger's echo: a swallowed error still
	// prints "job failed … sidecar …" into the same stream, and a test that reads
	// that noise cannot tell a reported failure from a hidden one.
	reported := ""
	for _, line := range strings.Split(stderr.String(), "\n") {
		if strings.HasPrefix(line, "xcut auto:") {
			reported = line
		}
	}
	if reported == "" {
		t.Fatalf("the command never reported its own failure:\n%s", stderr.String())
	}
	if !strings.Contains(strings.ToLower(reported), "sidecar") {
		t.Errorf("the reported failure does not name the missing piece: %s", reported)
	}
	if _, err := os.Stat(filepath.Join(root, "bare.mp4")); err == nil {
		t.Error("a run that never got its captions still rendered a reel, so the user cannot tell them apart")
	}
}

// TestAutoSubsOffIsNotAFilename: `off` is a value a user writes to be explicit, and
// here it used to be read as a subtitle file called "off". The sidecar is installed in
// this test on purpose — if the flag were ignored the run would still succeed silently,
// and the case would prove nothing.
func TestAutoSubsOffIsNotAFilename(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	root := t.TempDir()
	t.Setenv("XCUT_WORKSPACE", root)
	t.Setenv("XCUT_AI_BIN", fakeTranscriptSidecar(t, false)) // available, and must not be called

	fixture, err := testmedia.Generate(root, "scenes.mp4", testmedia.DefaultFixture(), 320, 240, 8)
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := Run([]string{"auto", fixture, "--project", "explicit", "--style", "generic_highlight",
		"--duration", "3", "--subs=off", "--out", filepath.Join(root, "explicit.mp4")},
		&stdout, &stderr)
	if code != 0 {
		t.Fatalf("--subs=off exited %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "==> subtitles") {
		t.Errorf("--subs=off transcribed anyway:\n%s", stdout.String())
	}
	if strings.Contains(stderr.String()+stdout.String(), `"off"`) {
		t.Errorf("the word off was handed to something as a filename:\n%s\n%s",
			stdout.String(), stderr.String())
	}
	files, _ := filepath.Glob(filepath.Join(root, "projects", "*", "subtitles.*"))
	if len(files) != 0 {
		t.Errorf("--subs=off produced caption files: %v", files)
	}
}

// TestAutoSubsAutoRefusesForeignTranscript: the transcript is bound to the
// asset it was heard from, and this run's reel is scoped to this run's inputs
// — so a second `auto` sharing the default project name must not burn the
// first run's captions over footage whose audio they never heard. The refusal
// names the way out (--subs on, or a file). The positive control proves the
// same flag still burns when the run does include the bound asset.
func TestAutoSubsAutoRefusesForeignTranscript(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	root := t.TempDir()
	t.Setenv("XCUT_WORKSPACE", root)
	t.Setenv("XCUT_AI_BIN", fakeTranscriptSidecar(t, false))

	ab, err := testmedia.Generate(root, "ab.mp4", testmedia.DefaultFixture(), 320, 240, 8)
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	c, err := testmedia.Generate(root, "c.mp4", testmedia.DefaultFixture(), 320, 240, 8)
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
	// Run one: two inputs, captions transcribed from the first. The binding
	// is to ab.mp4's asset.
	run(0, "auto", ab, c, "--project", "shared", "--style", "generic_highlight",
		"--duration", "4", "--subs=on", "--out", filepath.Join(root, "r1.mp4"))

	// Run two: c alone with --subs auto — the transcript's asset (ab) is not
	// in this run's inputs, and the run must say so instead of burning. The
	// refusal is a returned error, which lands on stderr.
	run(1, "auto", c, "--project", "shared", "--style", "generic_highlight",
		"--duration", "4", "--subs=auto", "--out", filepath.Join(root, "r2.mp4"))
	refusal := stdout.String() + stderr.String()
	for _, want := range []string{"outside this run's inputs", "--subs on"} {
		if !strings.Contains(refusal, want) {
			t.Errorf("the refusal does not say %q:\n%s\n--stderr--\n%s", want, stdout.String(), stderr.String())
		}
	}
	if _, err := os.Stat(filepath.Join(root, "r2.mp4")); err == nil {
		t.Errorf("the refused run rendered anyway")
	}

	// Positive control: the same flag, and this run does include the bound
	// asset (ab first) — the captions burn and the reel lands.
	run(0, "auto", ab, "--project", "shared", "--style", "generic_highlight",
		"--duration", "4", "--subs=auto", "--out", filepath.Join(root, "r3.mp4"))
	if fi, err := os.Stat(filepath.Join(root, "r3.mp4")); err != nil || fi.Size() == 0 {
		t.Errorf("the in-scope --subs auto run rendered nothing: %v", err)
	}
}
