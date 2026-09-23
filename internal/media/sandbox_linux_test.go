//go:build linux

package media

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// withCapForTest sets the cap for one case and restores whatever was there, so a case
// that armed the wrapper cannot leave the package's other tests running under it.
func withCapForTest(t *testing.T, mb int) {
	t.Helper()
	before := processMemoryLimitMB()
	t.Cleanup(func() {
		SetProcessMemoryLimitMB(int(before))
		resetSandbox()
	})
	resetSandbox()
	SetProcessMemoryLimitMB(mb)
}

// resetSandbox clears the memoized posture so each case resolves against the state it
// sets up. Package-internal by design: the memo exists because the answer must not be
// recomputed per child, and a test that cannot clear it would be testing the cache
// rather than the decision.
func resetSandbox() {
	sandboxMu.Lock()
	sandbox = sandboxState{}
	sandboxMB = -1
	sandboxMu.Unlock()
}

// requireScope skips when this host will not start a scope at all — over ssh a session
// can have no user manager, and then the assertions below would be measuring the host
// instead of the code. Callers set the cap first: the posture is resolved per cap.
func requireScope(t *testing.T) {
	t.Helper()
	resetSandbox()
	if !SandboxArmed() {
		t.Skipf("no systemd scope on this host (%s)", SandboxPosture())
	}
}

func TestNoCapMeansNoWrap(t *testing.T) {
	withCapForTest(t, 0)

	gotBin, gotArgs := wrapChild("/usr/bin/ffprobe", "-version")
	if gotBin != "/usr/bin/ffprobe" || len(gotArgs) != 1 || gotArgs[0] != "-version" {
		t.Errorf("an uncapped child was wrapped: %s %v", gotBin, gotArgs)
	}
	if SandboxArmed() {
		t.Error("SandboxArmed is true with no cap configured")
	}
	if p := SandboxPosture(); !strings.Contains(p, "no cap configured") {
		t.Errorf("posture = %q, want it to name the unset knob", p)
	}
}

func TestMissingSystemdRunDegradesToUnwrapped(t *testing.T) {
	withCapForTest(t, 256)
	// PATH narrowed to an empty directory: the arm where the wrapper binary is absent,
	// without uninstalling anything from the host.
	t.Setenv("PATH", t.TempDir())
	resetSandbox()

	gotBin, _ := wrapChild("/bin/true")
	if gotBin != "/bin/true" {
		t.Errorf("a host with no systemd-run got a different binary: %s", gotBin)
	}
	if SandboxArmed() {
		t.Error("SandboxArmed is true with no wrapper installed")
	}
	p := SandboxPosture()
	if !strings.Contains(p, "systemd-run") || strings.Contains(p, "armed") {
		t.Errorf("posture = %q, want it to name the missing wrapper and not claim it is armed", p)
	}
}

func TestWrappedChildKeepsPureStdoutAndItsOwnExitStatus(t *testing.T) {

	withCapForTest(t, 256)
	requireScope(t)
	echo, err := exec.LookPath("echo")
	if err != nil {
		t.Skip("no echo to run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	stdout, stderr, err := Run(ctx, echo, "hello-scope")
	if err != nil {
		t.Fatalf("wrapped run failed: %v (stderr %q)", err, stderr)
	}
	// The D13 lesson: one extra line in stdout turns ffprobe's JSON into an
	// unparseable document, and the tests that hand back a parsed struct cannot see
	// it. So the bytes are compared, not the parse.
	if got := strings.TrimSpace(string(stdout)); got != "hello-scope" {
		t.Errorf("child stdout = %q, want exactly the payload — the wrapper must not write there", got)
	}
	if strings.Contains(string(stderr), "Running as unit") {
		t.Errorf("the wrapper announced itself on stderr: %q", stderr)
	}

	if falseBin, err := exec.LookPath("false"); err == nil {
		if _, _, err := Run(ctx, falseBin); err == nil {
			t.Error("a wrapped child that exited 1 came back without an error")
		}
	}
}

// TestCapKillsAChildThatOverrunsIt is the claim the posture line makes: the cap is not
// decoration. It asks an interpreter to allocate four times the cap and expects the
// kernel, not this package, to end the child.
func TestCapKillsAChildThatOverrunsIt(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3 to allocate with")
	}
	out, err := exec.Command(py, "-V").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "Python 3") {
		t.Skipf("python3 is not a python 3 (output %q, err %v)", out, err)
	}
	// Small enough that the interpreter's own footprint fits inside it and the
	// allocation is what trips the boundary.
	withCapForTest(t, 128)
	requireScope(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	stdout, stderr, err := Run(ctx, py, "-c", "x=bytearray(512*1024*1024); print(len(x))")
	if err == nil {
		t.Fatalf("a 512 MB allocation survived a 128 MB cap (stdout %q, stderr %q)", stdout, stderr)
	}
	// SIGKILL from the cgroup killer, propagated as 137 through the wrapper. A python
	// MemoryError or a refused scope is a different story, and this is where the
	// difference shows instead of being read as a pass.
	if msg := err.Error(); !strings.Contains(msg, "signal: killed") && !strings.Contains(msg, "exit status 137") {
		t.Errorf("the child died for the wrong reason: %v (stderr %q)", err, stderr)
	}
}

// TestWrapPrefixIsWellFormed pins the argv shape the probes above cannot distinguish:
// the child's own name must sit immediately after "--", or every FFmpeg argument would
// be consumed as a systemd-run property.
func TestWrapPrefixIsWellFormed(t *testing.T) {
	withCapForTest(t, 256)
	requireScope(t)

	gotBin, gotArgs := wrapChild("/usr/bin/ffmpeg", "-hide_banner", "-i", "in.mp4")
	if filepath.Base(gotBin) != "systemd-run" {
		t.Fatalf("wrapped binary = %s, want systemd-run", gotBin)
	}
	joined := strings.Join(gotArgs, "\x00")
	for _, want := range []string{"-q", "--scope", "-p", "MemoryMax=256M", "-p", "MemorySwapMax=0", "--"} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv is missing %q: %v", want, gotArgs)
		}
	}
	dash := -1
	for i, a := range gotArgs {
		if a == "--" {
			dash = i
		}
	}
	if dash < 0 || dash+1 >= len(gotArgs) || gotArgs[dash+1] != "/usr/bin/ffmpeg" {
		t.Fatalf("the child does not follow \"--\": %v", gotArgs)
	}
	if got := strings.Join(gotArgs[dash+2:], " "); got != "-hide_banner -i in.mp4" {
		t.Errorf("the child's own argv was altered: %q", got)
	}
}
