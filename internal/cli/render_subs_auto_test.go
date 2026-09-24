package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/subs"
	"github.com/xiabee/XCut/internal/testmedia"
)

// TestRenderSubsAutoFixesAReStyledProject: the export tap has quietly re-laid captions out
// for the reel's frame for a while, and `xcut render --subs <file>` never had any part of
// that — it burned whatever path it was handed, including a caption box sized for the style
// the project no longer uses. `--subs auto` is the CLI asking the same question, and this
// walks the state a user actually lands in: caption one reel, restyle it, render.
//
// The named-file behaviour is asserted in the same run, because the difference between the
// two is the claim: pointing at a file means *that file*, wrong frame and all.
func TestRenderSubsAutoFixesAReStyledProject(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	requirePythonForFake(t)
	root := t.TempDir()
	t.Setenv("XCUT_WORKSPACE", root)
	t.Setenv("XCUT_AI_BIN", fakeTranscriptSidecar(t, false))

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
	run(0, "auto", fixture, "--project", "restyled", "--style", "generic_highlight",
		"--duration", "4", "--subs=on", "--out", filepath.Join(root, "wide.mp4"))

	files, gerr := filepath.Glob(filepath.Join(root, "projects", "*", "subtitles.ass"))
	if gerr != nil || len(files) != 1 {
		t.Fatalf("want the run's one caption file, found %v (err %v)", files, gerr)
	}
	before := readASSFrame(t, files[0])
	if before != "1920x1080" {
		t.Fatalf("the staged captions declare %s, want the 1920x1080 generic_highlight made them for", before)
	}

	// The reel changes shape. Nothing about the captions has been touched yet.
	run(0, "timeline", "restyled", "--style", "beat_shortform", "--duration", "4")

	out := run(0, "render", "restyled", "--subs", "auto", "--out", filepath.Join(root, "tall.mp4"))
	if !strings.Contains(out, "captions are styled for 1920x1080 and this reel is 1080x1920") {
		t.Errorf("the render never said which two frames it just reconciled:\n%s", out)
	}
	if !strings.Contains(out, "re-laid out") {
		t.Errorf("the render did not name the fix it applied (want the tap's own sentence):\n%s", out)
	}
	if got := readASSFrame(t, files[0]); got != "1080x1920" {
		t.Errorf("the captions still declare %s after --subs auto, want the reel's 1080x1920", got)
	}
	if _, serr := os.Stat(filepath.Join(root, "tall.mp4")); serr != nil {
		t.Errorf("no reel where the render said it wrote one: %v", serr)
	}
}

// readASSFrame asks the file through the product's own reader, the same one the tap
// compares frames with: a hand-rolled parser here could drift from it and pass.
func readASSFrame(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w, h, ok := subs.ReadASSFrame(f)
	if !ok {
		t.Fatalf("%s declares no frame (PlayResX/Y missing)", filepath.Base(path))
	}
	return fmt.Sprintf("%dx%d", w, h)
}
