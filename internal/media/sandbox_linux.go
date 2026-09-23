//go:build linux

package media

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// The Linux answer to the Windows job object's memory cap. Measured on this project's
// three Linux hosts on 2026-09-23, which is what the wording below is standing on:
//
//   - stdout stays pure through the wrapper (the "Running as unit:" line goes to
//     stderr, and -q removes it), so ffprobe JSON parsing is untouched — a wrapper
//     that polluted stdout would be the D13 failure all over again;
//   - the child's exit status propagates verbatim: 1 for an ordinary failure, 137
//     when the cgroup killer takes it;
//   - on the cgroup v2 hosts the cap binds: 400 MB allocated under
//     MemoryMax=100M MemorySwapMax=0 died with SIGKILL;
//   - as a non-root user the *system* manager refuses the scope ("Interactive
//     authentication required"), so off root the wrap goes through --user;
//   - and on Kylin V10 SP1 (systemd 245, cgroup v1) the scope was accepted while the
//     same 400 MB allocation **succeeded**. That is why the posture this package
//     reports says "armed", never "enforced": a start that works is not a limit that
//     holds, and a claim of the second from evidence of the first is how a cap becomes
//     fiction.
type sandboxState struct {
	run    string   // systemd-run, resolved
	prefix []string // argv up to and including "--"; nil = run children unwrapped
	reason string
}

var (
	sandboxMu sync.Mutex
	sandbox   sandboxState
	sandboxMB int64 = -1 // the cap sandbox was resolved for; -1 = never
)

// SandboxPosture describes, in the words a support ticket reads, what bounds an FFmpeg
// child's memory here. Empty means the platform has no mechanism to report on.
func SandboxPosture() string {
	return sandboxFor(processMemoryLimitMB()).reason
}

// SandboxArmed reports whether children are actually being wrapped.
func SandboxArmed() bool {
	return sandboxFor(processMemoryLimitMB()).prefix != nil
}

// wrapChild returns the binary and argv to exec, wrapped when a memory cap is
// configured and this host will start a scope for it.
func wrapChild(bin string, args ...string) (string, []string) {
	st := sandboxFor(processMemoryLimitMB())
	if st.prefix == nil {
		return bin, args
	}
	full := make([]string, 0, len(st.prefix)+1+len(args))
	full = append(full, st.prefix...)
	full = append(full, bin)
	full = append(full, args...)
	return st.run, full
}

func sandboxFor(mb int64) sandboxState {
	sandboxMu.Lock()
	defer sandboxMu.Unlock()
	if sandboxMB == mb && sandboxMB >= 0 {
		return sandbox
	}
	sandboxMB = mb
	sandbox = resolveSandbox(mb)
	return sandbox
}

func resolveSandbox(mb int64) sandboxState {
	if mb <= 0 {
		return sandboxState{reason: "no cap configured (resource.ffmpeg_max_memory_mb = 0)"}
	}
	bin, err := exec.LookPath("systemd-run")
	if err != nil {
		return sandboxState{reason: "uncapped: systemd-run is not installed, so there is no cgroup wrapper here"}
	}
	probe, perr := exec.LookPath("true")
	if perr != nil {
		probe = "/bin/true"
	}
	if _, err := os.Stat(probe); err != nil {
		return sandboxState{reason: "uncapped: no trivial target to probe the scope with"}
	}

	capArg := "MemoryMax=" + strconv.FormatInt(mb, 10) + "M"
	prefix := scopeArgs(capArg, "")

	// Ask before every render pays for it: start the wrapper around a command that
	// does nothing. A host without a session bus, or an old systemd that rejects the
	// property, fails here once and the children then run unwrapped — a render that
	// dies with "Failed to start transient scope unit" would be the worse failure,
	// because it would look like FFmpeg breaking.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, append(append([]string{}, prefix...), probe)...).CombinedOutput()
	if err != nil {
		return sandboxState{reason: "uncapped: systemd refused the scope (" + firstLine(string(out)) + ")"}
	}
	return sandboxState{
		run:    bin,
		prefix: prefix,
		reason: "each FFmpeg child starts in its own systemd scope with " + capArg +
			" and no swap — the scope started; whether the manager attached the limit is" +
			" SandboxEnforcement's question, which `xcut doctor` asks",
	}
}

// scopeArgs builds the systemd-run argv up to and including the "--" that separates the
// wrapper's properties from the child. unit is empty for ordinary children (let systemd
// name the scope) and set for the doctor probe, which has to know which unit to ask
// about afterwards.
func scopeArgs(capArg, unit string) []string {
	a := []string{"-q"}
	if os.Geteuid() != 0 {
		// Measured: the system manager will not take a scope from an unprivileged
		// caller without an interactive polkit prompt, which a server cannot answer.
		a = append(a, "--user")
	}
	a = append(a, "--scope")
	if unit != "" {
		a = append(a, "--unit="+unit)
	}
	return append(a, "-p", capArg, "-p", "MemorySwapMax=0", "--")
}

var probeSeq atomic.Uint64

// SandboxEnforcement asks the manager — not systemd-run's exit status — whether the
// limit it was handed is the limit it applied, by starting a named scope around a
// two-second sleep and reading the property back off the live unit.
//
// This exists because of a measurement, not a hypothesis: on Kylin V10 SP1 (hybrid
// cgroup) `systemd-run --scope -p MemoryMax=100M` exits 0, and the child then allocates
// 400 MB and lives, while `systemctl show <unit> -p MemoryMax --value` answers
// "infinity" with an empty ControlGroup. A caller that trusted the exit code reported a
// cap that was never attached. applied=false with detail naming the manager's own answer
// is the honest version of that sentence.
//
// known=false means the question could not be asked here (no systemctl, the unit gone,
// a session without a manager) and the caller should fall back to what SandboxPosture
// says, which is less.
func SandboxEnforcement() (applied, known bool, detail string) {
	mb := processMemoryLimitMB()
	if mb <= 0 {
		return false, false, "no cap configured, so there is nothing to measure"
	}
	if !SandboxArmed() {
		return false, false, SandboxPosture()
	}
	sr, err := exec.LookPath("systemd-run")
	if err != nil {
		return false, false, "systemd-run disappeared since the posture was resolved"
	}
	systemctl, err := exec.LookPath("systemctl")
	if err != nil {
		return false, false, "no systemctl to ask: the posture is all this host will say"
	}
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		return false, false, "no sleep binary to hold the probe scope open"
	}

	unit := "xcut-cap-probe-" + strconv.Itoa(os.Getpid()) + "-" +
		strconv.FormatUint(probeSeq.Add(1), 10) + ".scope"
	capArg := "MemoryMax=" + strconv.FormatInt(mb, 10) + "M"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// The scope only exists while its process runs, so the query has to land inside
	// the sleep's window; 400 ms is enough for systemd to have created the unit (the
	// measured answer arrives with the unit live) and far inside 2 s.
	probe := exec.CommandContext(ctx, sr, append(scopeArgs(capArg, unit), sleep, "2")...)
	if err := probe.Start(); err != nil {
		return false, false, "the probe scope would not start: " + err.Error()
	}
	time.Sleep(400 * time.Millisecond)

	show := exec.CommandContext(ctx, systemctl, "show", unit, "-p", "MemoryMax", "--value")
	if os.Geteuid() != 0 {
		show = exec.CommandContext(ctx, systemctl, "--user", "show", unit, "-p", "MemoryMax", "--value")
	}
	out, err := show.Output()
	_ = probe.Wait() // let the scope finish rather than leak it; the unit is gone after this

	value := strings.TrimSpace(string(out))
	if err != nil {
		return false, false, "systemd could not be asked about the probe unit: " + firstLine(err.Error())
	}
	return memoryMaxAnswer(value, mb)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return "no output"
	}
	return s
}
