//go:build linux

package media

import (
	"context"
	"os"
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

// cgroupV2Unified is a host precondition, not the claim being tested. On a hybrid
// hierarchy — Kylin V10 SP1 mounts v1 controllers under a tmpfs with `unified` at
// /sys/fs/cgroup/unified — systemd does not attach scope limits at all (measured, and
// recorded in docs/OPERATIONS.md), so asserting the manager's answer there would be
// reporting the host rather than the code. The skip names the mount line it read, and
// the fstype is read from /proc/mounts rather than compared against a magic number,
// because a magic number is the sort of thing I get wrong (the first version of this
// check skipped on a host where the cap binds).
func cgroupV2Unified(t *testing.T) bool {
	t.Helper()
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		t.Skipf("cannot read /proc/mounts: %v", err)
	}
	// /proc/mounts is "dev mountpoint fstype opts dump pass", so the mount point and
	// its type are adjacent fields.
	text := string(data)
	if strings.Contains(text, " /sys/fs/cgroup cgroup2 ") {
		return true
	}
	t.Skipf("/sys/fs/cgroup is not mounted as cgroup2 — on a hybrid or v1 hierarchy systemd does not attach scope limits; see docs/OPERATIONS.md")
	return false
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
//
// It ran on Kylin V10 SP1 first and failed there — "a 512 MB allocation survived a
// 128 MB cap" — which is the measurement behind D18's wording, not a flake. The v2
// precondition below keeps the case asserting where the mechanism exists and skipping by
// name where it does not; a permanently red leg would hide the next real failure.
func TestCapKillsAChildThatOverrunsIt(t *testing.T) {
	if !cgroupV2Unified(t) {
		return
	}
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

// TestSandboxEnforcementAsksTheManager is the D18 lesson turned into a check: a scope
// that starts is not a limit that holds. The probe asks systemd what it applied to a
// live unit and the assertion is the number, so the Kylin-shaped answer ("infinity",
// with systemd-run exiting 0) fails here rather than passing on an exit code.
func TestSandboxEnforcementAsksTheManager(t *testing.T) {
	if !cgroupV2Unified(t) {
		return
	}
	withCapForTest(t, 128)
	requireScope(t)

	applied, known, detail := SandboxEnforcement()
	if !known {
		t.Skipf("this host will not answer the question (%s)", detail)
	}
	if !applied {
		t.Fatalf("systemd answered, and the answer was not the cap: %s", detail)
	}
	if !strings.Contains(detail, "134217728") {
		t.Errorf("detail = %q, want 134217728 — the 128 MB asked for, in bytes", detail)
	}
}

// TestEnforcementWithoutACapIsNotARefusal keeps the three-way answer honest: "there is
// nothing to measure" must not read as "the host refused", because doctor maps the
// second to WARN and the first to a quiet row.
func TestEnforcementWithoutACapIsNotARefusal(t *testing.T) {
	withCapForTest(t, 0)

	applied, known, detail := SandboxEnforcement()
	if applied || known {
		t.Errorf("no cap configured yet the probe claimed applied=%v known=%v", applied, known)
	}
	if !strings.Contains(detail, "no cap") {
		t.Errorf("detail = %q, want it to say the question does not apply", detail)
	}
}

// fakeTool writes an executable shell stub into a fresh directory and returns its path.
// The repo's convention for "a host tool that behaves badly" (internal/api does the same
// with generated ffmpeg scripts), because the arms below are unreachable on a healthy
// machine and are exactly the ones an operator on a broken machine reads about.
func fakeTool(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestScopeRefusalRunsChildrenUnwrappedAndQuotesTheReason covers the fallback that
// decides whether a render survives: systemd-run exists but the manager will not take a
// scope (no session bus, a container, an old systemd rejecting the property).
func TestScopeRefusalRunsChildrenUnwrappedAndQuotesTheReason(t *testing.T) {
	withCapForTest(t, 256)
	dir := fakeTool(t, "systemd-run",
		"echo 'Failed to connect to bus: No such file or directory' >&2; exit 1")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	resetSandbox()

	if gotBin, _ := wrapChild("/usr/bin/ffprobe", "-version"); gotBin != "/usr/bin/ffprobe" {
		t.Errorf("a refused scope still wrapped the child: %s", gotBin)
	}
	if SandboxArmed() {
		t.Error("SandboxArmed is true after the manager refused")
	}
	p := SandboxPosture()
	if !strings.Contains(p, "systemd refused the scope") {
		t.Errorf("posture = %q, want the refusal named as the reason", p)
	}
	// The manager's own words, not a generic "unavailable": this string is what a
	// support ticket is triaged from.
	if !strings.Contains(p, "Failed to connect to bus") {
		t.Errorf("posture = %q, want the refusal's first line quoted back", p)
	}
}

// TestEnforcementStaysSilentWhenSystemdCannotBeAsked is the arm that must NOT become a
// WARN: a host that answers nothing is not a host that refused the limit.
func TestEnforcementStaysSilentWhenSystemdCannotBeAsked(t *testing.T) {
	withCapForTest(t, 256)
	dir := fakeTool(t, "systemd-run", "exit 0")
	// PATH holds only the stub, so systemctl (and the probe's sleep) cannot be found.
	t.Setenv("PATH", dir)
	resetSandbox()

	if !SandboxArmed() {
		t.Skipf("the stub scope did not resolve (%s)", SandboxPosture())
	}
	applied, known, detail := SandboxEnforcement()
	if applied || known {
		t.Errorf("a host that cannot be asked reported applied=%v known=%v (%q)", applied, known, detail)
	}
	if !strings.Contains(detail, "systemctl") {
		t.Errorf("detail = %q, want it to name what was missing", detail)
	}
}

// TestEnforcementQuotesTheManagerWhenTheQueryFails is the same distinction when the
// binary exists but the query fails: known=false, and the words are the manager's.
func TestEnforcementQuotesTheManagerWhenTheQueryFails(t *testing.T) {
	withCapForTest(t, 256)
	dir := fakeTool(t, "systemd-run", "exit 0")
	errDir := fakeTool(t, "systemctl", "echo 'Failed to connect to bus: Host is down' >&2; exit 1")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+errDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	resetSandbox()

	if !SandboxArmed() {
		t.Skipf("the stub scope did not resolve (%s)", SandboxPosture())
	}
	applied, known, detail := SandboxEnforcement()
	if applied || known {
		t.Errorf("a failing query must not read as an answer: applied=%v known=%v (%q)", applied, known, detail)
	}
	if !strings.Contains(detail, "Host is down") {
		t.Errorf("detail = %q, want the manager's error quoted", detail)
	}
}
