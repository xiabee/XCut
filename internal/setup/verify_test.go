package setup

import (
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
// verification, which means it is started with the one argument verifyFFprobe
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

// stubFFprobePath is the executable to hand verifyFFprobe: this test binary,
// told by env which identity to answer with.
func stubFFprobePath(t *testing.T, mode string) string {
	t.Helper()
	t.Setenv(stubEnvVar, mode)
	return os.Args[0]
}

func TestVerifyFFprobeAcceptsAToolThatAnswersAsFFprobe(t *testing.T) {
	if err := verifyFFprobe(stubFFprobePath(t, "ok")); err != nil {
		t.Fatalf("a tool that names itself ffprobe was refused: %v", err)
	}
	// Case-insensitive on purpose: banner capitalization is the tool's, not ours.
	if err := verifyFFprobe(stubFFprobePath(t, "uppercase")); err != nil {
		t.Fatalf("the identity check was case-sensitive: %v", err)
	}
}

// TestVerifyFFprobeRefusesAFileThatOnlyRuns is why the check exists at all. A
// non-zero exit was always caught; what an exit code cannot see is a file that
// runs happily and is not the tool. Before this, the gate would have published
// it as installed and the UI would have said FFmpeg was ready.
func TestVerifyFFprobeRefusesAFileThatOnlyRuns(t *testing.T) {
	err := verifyFFprobe(stubFFprobePath(t, "wrong-tool"))
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

func TestVerifyFFprobeReportsAToolThatFails(t *testing.T) {
	err := verifyFFprobe(stubFFprobePath(t, "loud-but-failing"))
	if err == nil {
		t.Fatal("a tool exiting 3 was accepted")
	}
	if !strings.Contains(err.Error(), "and then it failed") {
		t.Errorf("the tool's own stderr was dropped: %v", err)
	}
}

// TestVerifyFFprobeOnAFileThatIsNotAnExecutable covers the arm reached when the
// archive extracted something that cannot run at all: the error has to say the
// tool failed to verify rather than swallow the reason. POSIX refuses the exec;
// Windows refuses with a different error for the same bytes, so the case is
// stated where its own claim holds.
func TestVerifyFFprobeOnAFileThatIsNotAnExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows answers a plain text file with an exec error this case does not describe")
	}
	p := filepath.Join(t.TempDir(), "ffprobe")
	if err := os.WriteFile(p, []byte("not a binary"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := verifyFFprobe(p)
	if err == nil {
		t.Fatal("a plain text file verified as ffprobe")
	}
	if code := xcerr.CodeOf(err); code != xcerr.CodeFFmpegFailure {
		t.Errorf("code %q, want ffmpeg_failure", code)
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
