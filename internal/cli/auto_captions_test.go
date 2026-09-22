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
	requirePythonForFake(t)
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
	requirePythonForFake(t)
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
