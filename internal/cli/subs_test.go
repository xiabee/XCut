package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/worker"
)

// fakeTranscriptSidecar points XCUT_AI_BIN at the package's own test binary,
// which TestMain turns into the sidecar when XCUT_TEST_SIDECAR is set: it
// advertises an available transcript analyzer and answers analyze with a
// canned result. With wordTimings off it answers the way most backends do:
// where each line falls, and nothing about the syllables inside it.
func fakeTranscriptSidecar(t *testing.T, wordTimings bool) string {
	t.Helper()
	second := `{"start": 2.0, "end": 3.0, "text": "世界", "words": [
            {"start": 2.0, "end": 3.0, "word": "世界"}]}`
	first := `{"start": 0.5, "end": 1.5, "text": "你好", "words": [
            {"start": 0.5, "end": 1.0, "word": "你"},
            {"start": 1.0, "end": 1.5, "word": "好"}]}`
	if !wordTimings {
		first = `{"start": 0.5, "end": 1.5, "text": "你好"}`
		second = `{"start": 2.0, "end": 3.0, "text": "世界"}`
	}
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("XCUT_TEST_SIDECAR", "canned")
	t.Setenv("XCUT_TEST_SIDECAR_SEGMENTS", "["+first+","+second+"]")
	return bin
}

// TestSubtitlesCommandEndToEnd: the subtitles command drives the sidecar
// handshake, parses the transcript, and writes SRT — and karaoke ASS on
// --ass — next to the media file.
func TestSubtitlesCommandEndToEnd(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XCUT_WORKSPACE", root)
	t.Setenv("XCUT_AI_BIN", fakeTranscriptSidecar(t, true))

	media := filepath.Join(root, "song.mp4")
	if err := os.WriteFile(media, []byte("not really a video"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"subtitles", media, "--out", filepath.Join(root, "song.srt")},
		&stdout, &stderr)
	if code != 0 {
		t.Fatalf("subtitles failed (%d): %s", code, stderr.String())
	}
	srt, err := os.ReadFile(filepath.Join(root, "song.srt"))
	if err != nil {
		t.Fatal(err)
	}
	want := "1\n00:00:00,500 --> 00:00:01,500\n你好\n\n2\n00:00:02,000 --> 00:00:03,000\n世界\n"
	if string(srt) != want {
		t.Fatalf("srt =\n%q\nwant\n%q", srt, want)
	}

	code = Run([]string{"subtitles", media, "--out", filepath.Join(root, "song.ass"), "--ass"},
		&stdout, &stderr)
	if code != 0 {
		t.Fatalf("subtitles --ass failed (%d): %s", code, stderr.String())
	}
	ass, err := os.ReadFile(filepath.Join(root, "song.ass"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ass), "{\\kf50}你 {\\kf50}好") {
		t.Fatalf("karaoke fills wrong:\n%s", ass)
	}
	if !strings.Contains(string(ass), "Dialogue: 0,0:00:02.00,0:00:03.00,Karaoke,,0,0,0,,{\\kf100}世界") {
		t.Fatalf("second karaoke line wrong:\n%s", ass)
	}
}

// TestSubtitlesASSWithoutWordTimings: --ass asks for the styled file, and only
// the karaoke fill needs syllable timings. Answering the ordinary transcript
// with "karaoke output needs them" left the caller with no styled caption at
// all — the same words, two qualities of output, decided by the sidecar.
func TestSubtitlesASSWithoutWordTimings(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XCUT_WORKSPACE", root)
	t.Setenv("XCUT_AI_BIN", fakeTranscriptSidecar(t, false))
	media := filepath.Join(root, "talk.mp4")
	if err := os.WriteFile(media, []byte("not really a video"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, "talk.ass")
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"subtitles", media, "--out", out, "--ass"}, &stdout, &stderr); code != 0 {
		t.Fatalf("subtitles --ass failed (%d): %s", code, stderr.String())
	}
	file, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	body := string(file)
	// 1.70 is the 1.2 s dwell the line needed, not the 1.5 s the speech took.
	for _, want := range []string{"Style: Caption,", "Dialogue: 0,0:00:00.50,0:00:01.70,Caption,,0,0,0,,你好", "世界"} {
		if !strings.Contains(body, want) {
			t.Errorf("the caption file lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, `\k`) {
		t.Errorf("no syllable was timed, yet the file carries karaoke tags:\n%s", body)
	}
	if !strings.Contains(stdout.String(), "caption ass") {
		t.Errorf("the report does not name what was written: %q", stdout.String())
	}
}

// TestSubtitlesCommandHonestWhenNoBackend: against the real reference
// sidecar without any Whisper backend installed, the command must fail with
// a hint naming the installable backends — never silently produce an empty
// subtitle file.
func TestSubtitlesCommandHonestWhenNoBackend(t *testing.T) {
	script := referenceSidecar(t)
	caps, err := worker.Capabilities(t.Context(), script)
	if err != nil {
		t.Skipf("reference sidecar unusable: %v", err)
	}
	m := transcriptModelFor(caps)
	if m == nil || m.Available {
		t.Skip("a real speech backend is installed; the absent-path assertion does not apply")
	}

	root := t.TempDir()
	t.Setenv("XCUT_WORKSPACE", root)
	t.Setenv("XCUT_AI_BIN", script)
	media := filepath.Join(root, "song.mp4")
	if err := os.WriteFile(media, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"subtitles", media}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("subtitles must fail when the sidecar has no speech backend")
	}
	for _, hint := range []string{"whisper", "faster"} {
		if !strings.Contains(strings.ToLower(stderr.String()), hint) {
			t.Fatalf("error must name installable backends, got: %s", stderr.String())
		}
	}
}

// referenceSidecar locates the repo's reference sidecar script.
func referenceSidecar(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Skip("cannot locate repo root")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	script := filepath.Join(root, "scripts", "xcut-ai-sidecar.py")
	if _, err := os.Stat(script); err != nil {
		t.Skipf("reference sidecar missing: %v", err)
	}
	return script
}

func transcriptModelFor(c *worker.AICapabilities) *worker.AIModel {
	for i := range c.Models {
		if c.Models[i].Name == "transcript" {
			return &c.Models[i]
		}
	}
	return nil
}
