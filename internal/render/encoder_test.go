package render

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/config"
	"github.com/xiabee/XCut/internal/xcerr"
)

// TestEveryAllowedEncoderNameHasArgs cross-checks the two halves of the
// encoder contract: config names what a user may configure, and this package
// must know how to drive every one of those names. "auto" is resolved before
// args are built, so it is the one name with no clause of its own.
func TestEveryAllowedEncoderNameHasArgs(t *testing.T) {
	for _, name := range config.EncoderNames {
		if name == config.EncoderAuto {
			continue
		}
		args := encoderVideoArgs(name, 22)
		if len(args) < 2 || args[0] != "-c:v" || args[1] != name {
			t.Errorf("encoderVideoArgs(%q) = %v, want it to start with -c:v %s", name, args, name)
		}
	}
}

func TestEncoderVideoArgs(t *testing.T) {
	cases := []struct {
		name string
		want []string
	}{
		{"", []string{"-c:v", "libx264", "-preset", "veryfast", "-crf", "22"}},
		{"libx264", []string{"-c:v", "libx264", "-preset", "veryfast", "-crf", "22"}},
		{"h264_nvenc", []string{"-c:v", "h264_nvenc", "-rc", "vbr", "-cq", "30", "-b:v", "0"}},
		{"hevc_nvenc", []string{"-c:v", "hevc_nvenc", "-rc", "vbr", "-cq", "30", "-b:v", "0", "-tag:v", "hvc1"}},
		{"h264_qsv", []string{"-c:v", "h264_qsv"}},
	}
	for _, c := range cases {
		got := encoderVideoArgs(c.name, 22)
		if strings.Join(got, " ") != strings.Join(c.want, " ") {
			t.Errorf("encoderVideoArgs(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestSelectEncoderResolvesAgainstTheMachine pins the decision table with an
// injected prober — no GPU required: auto takes the first candidate the
// machine can actually start, degrades to libx264 when none answers, and a
// named encoder the machine refuses degrades too (wanting speed must not
// cost the render).
func TestSelectEncoderResolvesAgainstTheMachine(t *testing.T) {
	candidates := hwEncoderCandidates()
	usable := candidates[len(candidates)-1] // the LAST candidate: auto must walk the whole list
	probe := func(_ context.Context, _ string, name string) error {
		if name == usable {
			return nil
		}
		return errors.New("not usable")
	}

	enc, note, err := selectEncoderWith(context.Background(), "ffmpeg", config.EncoderAuto, probe)
	if err != nil || enc.Name != usable || !enc.HW || note != "" {
		t.Fatalf("auto with a usable %s: got %v note=%q err=%v", usable, enc, note, err)
	}

	enc, note, err = selectEncoderWith(context.Background(), "ffmpeg", config.EncoderAuto,
		func(context.Context, string, string) error { return errors.New("not usable") })
	if err != nil || enc.Name != config.EncoderSoftware || enc.HW || note == "" {
		t.Fatalf("auto with nothing usable: got %v note=%q err=%v (the fallback must say why)", enc, note, err)
	}

	enc, note, err = selectEncoderWith(context.Background(), "ffmpeg", "h264_nvenc", probe)
	if err != nil {
		t.Fatalf("named-unusable must degrade, not fail: %v", err)
	}
	if enc.Name != config.EncoderSoftware || note == "" || !strings.Contains(note, "h264_nvenc") {
		t.Fatalf("named-unusable: got %v note=%q (the note must name the refused encoder)", enc, note)
	}

	enc, note, err = selectEncoderWith(context.Background(), "ffmpeg", usable, probe)
	if err != nil || enc.Name != usable || !enc.HW || note != "" {
		t.Fatalf("named-usable: got %v note=%q err=%v", enc, note, err)
	}

	if _, _, err := selectEncoderWith(context.Background(), "ffmpeg", "not_an_encoder", probe); xcerr.CodeOf(err) != xcerr.CodeValidation {
		t.Fatalf("an unknown name must be a validation error, got: %v", err)
	}
}

// TestProbeEncoderWithTheRealToolchain is the hardware half: against the real
// ffmpeg, the shipped software encoder probes usable and a made-up name does
// not — on a GPU box the auto path's first candidate answers too, which is
// the whole feature.
func TestProbeEncoderWithTheRealToolchain(t *testing.T) {
	tools := requireTools(t)
	if err := ProbeEncoder(context.Background(), tools.FFmpeg, config.EncoderSoftware); err != nil {
		t.Fatalf("libx264 must probe usable on a working ffmpeg: %v", err)
	}
	if err := ProbeEncoder(context.Background(), tools.FFmpeg, "definitely_not_an_encoder"); err == nil {
		t.Fatal("a made-up encoder name must fail the probe")
	}
	// The auto path's first candidate on this platform: pass or fail is a
	// machine fact — both answer the question "what would auto pick here".
	if err := ProbeEncoder(context.Background(), tools.FFmpeg, hwEncoderCandidates()[0]); err != nil {
		t.Logf("%s not usable here (user-safe refusal): %v", hwEncoderCandidates()[0], xcerr.UserMessage(err))
	} else {
		t.Logf("%s usable on this machine", hwEncoderCandidates()[0])
	}
}

// TestNormalizedClipsCarryTheConfiguredEncoder reads the assembled command
// line back from the child (the fake-ffmpeg pattern): a render with a
// hardware encoder must drive that encoder with its own quality clause, and
// must not carry the x264-only preset any more.
func TestNormalizedClipsCarryTheConfiguredEncoder(t *testing.T) {
	tools := requireTools(t)
	src := fixture(t)
	tools.FFmpeg = os.Args[0]
	t.Setenv("XCUT_FAKE_FFMPEG", "1")
	argvFile := filepath.Join(t.TempDir(), "argv.txt")
	t.Setenv("XCUT_FAKE_FFMPEG_ARGV", argvFile)

	out := filepath.Join(t.TempDir(), "out.mp4")
	_ = Render(context.Background(), twoClipTimeline(src), Options{Tools: tools, TempDir: t.TempDir(), Encoder: "h264_nvenc", CRF: 22}, out)

	blob, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("the child never recorded its arguments: %v", err)
	}
	args := blob
	for _, want := range []string{"-c:v h264_nvenc", "-rc vbr", "-cq 30", "-b:v 0"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("assembled arguments lost %q:\n%.400s", want, args)
		}
	}
	if strings.Contains(string(args), "-preset veryfast") {
		t.Errorf("the x264 preset leaked into the nvenc command line:\n%.400s", args)
	}
}
