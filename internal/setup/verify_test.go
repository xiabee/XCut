package setup

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/xcerr"
)

// The stub child below is reached by running *this* test binary as the tool under
// verification, which means it is started with the one argument verifyTool
// always appends: "-version". Go parses flags before TestMain runs, so an
// unregistered "-version" dies in the flag package and the dispatch below never
// gets a turn. Registering it here (a bool, ignored) is what lets a test hold a
// real executable that answers on command — no shell, no compiler, no committed
// fixture, which is the constraint AGENTS.md rule 2 leaves standing.
var stubVersionFlag = flag.Bool("version", false, "ignored: present so the stub child can start")

// stubMode is the identity the stub child answers with. Empty in a normal run.
const stubEnvVar = "XCUT_TEST_FFPROBE_STUB"

func TestMain(m *testing.M) {
	switch os.Getenv(stubEnvVar) {
	case "ok":
		fmt.Fprintln(os.Stdout, "ffprobe version 7.1-stub Copyright (c) 2007-2026 the FFmpeg developers")
		os.Exit(0)
	case "wrong-tool":
		fmt.Fprintln(os.Stdout, "Hello from a file someone renamed to ffprobe.exe")
		os.Exit(0)
	case "uppercase":
		fmt.Fprintln(os.Stdout, "FFProbe VERSION 7.1-stub")
		os.Exit(0)
	case "ffmpeg-banner":
		// The tool that answers as its sibling: what a check of one binary only
		// cannot see, because the file it asks about is the other one.
		fmt.Fprintln(os.Stdout, "ffmpeg version 7.1-stub Copyright (c) 2000-2026 the FFmpeg developers")
		os.Exit(0)
	case "loud-but-failing":
		fmt.Fprintln(os.Stderr, "ffprobe version 7.1-stub")
		fmt.Fprintln(os.Stderr, "and then it failed")
		os.Exit(3)
	case "":
		// not a stub run: fall through and run the suite
	default:
		fmt.Fprintln(os.Stderr, "unknown stub mode")
		os.Exit(2)
	}
	os.Exit(m.Run())
}

// stubToolPath is the executable to hand a verifier: this test binary,
// told by env which identity to answer with.
func stubToolPath(t *testing.T, mode string) string {
	t.Helper()
	t.Setenv(stubEnvVar, mode)
	return os.Args[0]
}

func TestVerifierAcceptsAToolThatAnswersAsFFprobe(t *testing.T) {
	if err := verifyTool(stubToolPath(t, "ok"), "ffprobe"); err != nil {
		t.Fatalf("a tool that names itself ffprobe was refused: %v", err)
	}
	// Case-insensitive on purpose: banner capitalization is the tool's, not ours.
	if err := verifyTool(stubToolPath(t, "uppercase"), "ffprobe"); err != nil {
		t.Fatalf("the identity check was case-sensitive: %v", err)
	}
}

// TestVerifierRefusesAFileThatOnlyRuns is why the check exists at all. A
// non-zero exit was always caught; what an exit code cannot see is a file that
// runs happily and is not the tool. Before this, the gate would have published
// it as installed and the UI would have said FFmpeg was ready.
func TestVerifierRefusesAFileThatOnlyRuns(t *testing.T) {
	err := verifyTool(stubToolPath(t, "wrong-tool"), "ffprobe")
	if err == nil {
		t.Fatal("a file that runs and says it is not ffprobe was accepted")
	}
	if code := xcerr.CodeOf(err); code != xcerr.CodeFFmpegFailure {
		t.Errorf("refusal code %q, want ffmpeg_failure", code)
	}
	// The answer it got is the diagnostic: "not ffprobe" without the banner
	// leaves whoever hits it with nothing to compare against.
	if !strings.Contains(err.Error(), "Hello from a file") {
		t.Errorf("refusal did not carry what the tool answered: %v", err)
	}
}

func TestVerifierReportsAToolThatFails(t *testing.T) {
	err := verifyTool(stubToolPath(t, "loud-but-failing"), "ffprobe")
	if err == nil {
		t.Fatal("a tool exiting 3 was accepted")
	}
	if !strings.Contains(err.Error(), "and then it failed") {
		t.Errorf("the tool's own stderr was dropped: %v", err)
	}
}

// TestVerifierOnAFileThatIsNotAnExecutable covers the arm reached when the
// archive extracted something that cannot run at all: the error has to say the
// tool failed to verify rather than swallow the reason. POSIX refuses the exec;
// Windows refuses with a different error for the same bytes, so the case is
// stated where its own claim holds.
func TestVerifierOnAFileThatIsNotAnExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows answers a plain text file with an exec error this case does not describe")
	}
	p := filepath.Join(t.TempDir(), "ffprobe")
	if err := os.WriteFile(p, []byte("not a binary"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := verifyTool(p, "ffprobe")
	if err == nil {
		t.Fatal("a plain text file verified as ffprobe")
	}
	if code := xcerr.CodeOf(err); code != xcerr.CodeFFmpegFailure {
		t.Errorf("code %q, want ffmpeg_failure", code)
	}
}

// TestVerifyToolsRefusesAToolAnsweringAsItsSibling is this cycle's point. The pair
// check replaced one that only ever asked ffprobe, so a file that answers as ffmpeg
// in the ffprobe slot — a wrong entry kept by the zip reader, a renamed sibling —
// used to be published as installed and then run by every render. Each answer is
// fine for its own slot, which is what makes the refusal the finding rather than
// the format.
func TestVerifyToolsRefusesAToolAnsweringAsItsSibling(t *testing.T) {
	stub := stubToolPath(t, "ffmpeg-banner")
	if err := verifyTool(stub, "ffmpeg"); err != nil {
		t.Fatalf("an ffmpeg answering as ffmpeg was refused: %v", err)
	}
	err := verifyTools(stub, stub)
	if err == nil {
		t.Fatal("verifyTools accepted the ffprobe slot answering as ffmpeg")
	}
	if !strings.Contains(err.Error(), "ffprobe") {
		t.Errorf("the refusal does not say which slot failed: %v", err)
	}
}

// TestInstallVerifiesBothInstalledTools pins the wiring the pair needs: the
// installer hands the verifier the two files it extracted, not one of them.
func TestInstallVerifiesBothInstalledTools(t *testing.T) {
	requireWindowsInstaller(t)
	root := t.TempDir()
	in := installerFor(t, buildZip(t, root), filepath.Join(root, "bin"), filepath.Join(root, "scratch"))
	var asked []string
	in.Verify = func(ffmpegPath, ffprobePath string) error {
		asked = append(asked, ffmpegPath, ffprobePath)
		return nil
	}
	if err := in.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	st := waitPhase(t, in, PhaseDone)
	if len(asked) != 2 {
		t.Fatalf("the verifier saw %d paths (%v), want the two tools the installer extracted", len(asked), asked)
	}
	if asked[0] == asked[1] {
		t.Fatalf("both slots were the same file: %v", asked)
	}
	if got := filepath.Base(asked[0]); got != "ffmpeg.exe" {
		t.Errorf("the first slot was %q, want the ffmpeg the renderer will exec", got)
	}
	if got := filepath.Base(asked[1]); got != "ffprobe.exe" {
		t.Errorf("the second slot was %q, want ffprobe.exe", got)
	}
	if st.FFmpegPath != asked[0] || st.FFprobePath != asked[1] {
		t.Errorf("what was verified is not what was published as installed: asked %v, status %v/%v",
			asked, st.FFmpegPath, st.FFprobePath)
	}
}

// TestVerifierChecksTheInstalledFFmpegToo is the other direction, and the one that
// a "verify the ffprobe, skip the rest" implementation cannot survive: the ffprobe
// slot is handed a tool that answers perfectly, so nothing about the refusal can come
// from it. Only asking ffmpeg finds that ffmpeg is not there.
func TestVerifierChecksTheInstalledFFmpegToo(t *testing.T) {
	good := stubToolPath(t, "ok") // answers as ffprobe, which is what its slot claims
	err := verifyTools(filepath.Join(t.TempDir(), "absent", "ffmpeg.exe"), good)
	if err == nil {
		t.Fatal("an ffmpeg that does not exist was accepted because ffprobe answered")
	}
	if !strings.Contains(err.Error(), "ffmpeg") {
		t.Errorf("the refusal does not name ffmpeg as the tool that failed: %v", err)
	}
}

func TestTrimOutputBoundsLongAnswersWithoutCuttingShortOnes(t *testing.T) {
	short := []byte("ffprobe version 7.1")
	if got := string(trimOutput(short)); got != string(short) {
		t.Fatalf("trimOutput cut a short answer to %q", got)
	}
	if got := len(trimOutput(make([]byte, 5000))); got != 400 {
		t.Fatalf("a 5000-byte answer was trimmed to %d bytes, want the 400 errors carry", got)
	}
}
