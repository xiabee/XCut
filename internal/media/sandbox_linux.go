//go:build linux

package media

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// The Linux answer to the Windows job object. Two independent halves, resolved
// separately and chained when both arm (D18 for the memory half, D20 for the
// network half):
//
//   - memory: each child starts in its own transient systemd scope
//     (MemoryMax/MemorySwapMax). Measured on this project's three Linux hosts
//     on 2026-09-23; on the cgroup v2 hosts the cap binds, on Kylin V10 SP1
//     (hybrid cgroups) the scope is accepted while the limit goes nowhere —
//     which is why "the scope started" is never allowed to say "the limit
//     holds", and SandboxEnforcement asks the manager itself.
//   - network: each child runs inside a fresh network namespace via unshare
//     (no interfaces at all, plus PID and mount namespaces riding along).
//     There is no manager between the child and this boundary: the kernel
//     creates the namespaces or the probe fails, so a passing probe is
//     enforcement, not a promise.
//
// Both halves share the properties the wording of D18 bought with measurements:
// stdout stays pure through every wrapper (a wrapper that polluted stdout would
// be the D13 failure all over again), and each child's exit status propagates
// verbatim through both — unshare(1) forwards its child's status, and the scope
// wraps the whole subtree (the cgroup contains unshare and its descendants).
type sandboxState struct {
	run    string   // outermost wrapper binary, resolved
	prefix []string // argv up to and including the last "--"; nil = run children unwrapped
	reason string
}

// wrapPart is one resolved half of the chain. A half that could not resolve
// carries only its reason; the other half still arms, and a refusal never
// spreads from one boundary to the other.
type wrapPart struct {
	kind   string // "memory" or "net" — what the posture words turn on
	bin    string
	args   []string // ends with the "--" that separates it from what it wraps
	reason string   // non-empty = this half is unavailable here
}

// describe words the half in the posture's honest register: the namespace half
// is kernel-enforced at creation, the memory half only started a scope. mb
// rides along so the memory half names the number it asked for.
func (p wrapPart) describe(mb int64) string {
	if p.kind == "net" {
		return "a fresh network namespace (no interfaces, so nothing connects — measured: even loopback is dead; PID and mount namespaces ride along), enforced by the kernel at namespace creation"
	}
	return "a systemd scope with MemoryMax=" + strconv.FormatInt(mb, 10) + "M and no swap"
}

var (
	sandboxMu  sync.Mutex
	sandbox    sandboxState
	sandboxMB  int64         = -1 // the cap the sandbox was resolved for; -1 = never
	sandboxPol SandboxPolicy      // the policy it was resolved for (paired with sandboxMB)
)

// SandboxPosture describes, in the words a support ticket reads, what bounds an
// FFmpeg child here. Empty means the platform has no mechanism to report on.
func SandboxPosture() string {
	return sandboxFor(processMemoryLimitMB(), currentSandboxPolicy()).reason
}

// SandboxArmed reports whether children are actually being wrapped.
func SandboxArmed() bool {
	return sandboxFor(processMemoryLimitMB(), currentSandboxPolicy()).prefix != nil
}

// wrapChild returns the binary and argv to exec, wrapped when a memory cap or
// the network-isolation policy (or both) is configured and this host will
// honor it.
func wrapChild(bin string, args ...string) (string, []string) {
	st := sandboxFor(processMemoryLimitMB(), currentSandboxPolicy())
	if st.prefix == nil {
		return bin, args
	}
	full := make([]string, 0, len(st.prefix)+1+len(args))
	full = append(full, st.prefix...)
	full = append(full, bin)
	full = append(full, args...)
	return st.run, full
}

func sandboxFor(mb int64, pol SandboxPolicy) sandboxState {
	sandboxMu.Lock()
	defer sandboxMu.Unlock()
	if sandboxMB == mb && sandboxPol == pol && sandboxMB >= 0 {
		return sandbox
	}
	sandboxMB = mb
	sandboxPol = pol
	sandbox = resolveSandbox(mb, pol)
	return sandbox
}

func resolveSandbox(mb int64, pol SandboxPolicy) sandboxState {
	if mb <= 0 && !pol.NetIsolation {
		return sandboxState{reason: "no cap configured (resource.ffmpeg_max_memory_mb = 0) and no sandbox policy (resource.ffmpeg_sandbox = none)"}
	}

	var parts []wrapPart
	if mb > 0 {
		parts = append(parts, memoryPart(mb))
	}
	if pol.NetIsolation {
		parts = append(parts, netPart())
	}

	var chain []string // interleaved: args..., bin, args..., bin(args end with "--")
	var run string
	var armed []string // words for the halves that actually survived resolution
	var refused []string
	for _, p := range parts {
		if p.reason != "" {
			refused = append(refused, p.reason)
			continue
		}
		armed = append(armed, p.describe(mb))
		if len(chain) == 0 {
			// The outermost wrapper's binary is the exec target; its args go first.
			run = p.bin
			chain = append(chain, p.args...)
			continue
		}
		// A second wrapper nests inside the first: its binary rides the outer
		// argv right after the outer "--", then its own args and separator.
		chain = append(chain, p.bin)
		chain = append(chain, p.args...)
	}
	if len(chain) == 0 {
		return sandboxState{reason: strings.Join(refused, "; ")}
	}

	// The posture describes the halves that survived, never the halves that were
	// asked for — a refused boundary described as armed is the fiction this
	// package exists to refuse.
	s := "each FFmpeg child starts inside " + armed[0]
	for _, more := range armed[1:] {
		s += " and inside " + more
	}
	if len(armed) == 1 && mb > 0 {
		// The memory half alone keeps D18's wording: the scope started is not
		// the limit attached, and the doctor is the one who asks.
		s += " — the scope started; whether the manager attached the limit is" +
			" SandboxEnforcement's question, which `xcut doctor` asks"
	}
	if len(refused) > 0 {
		return sandboxState{run: run, prefix: chain, reason: s + "; " + strings.Join(refused, "; ")}
	}
	return sandboxState{run: run, prefix: chain, reason: s}
}

// memoryPart resolves the systemd scope half for the given cap. The probe asks
// before every render pays for it: a host without a session bus, or an old
// systemd that rejects the property, fails here once and the memory half then
// runs without its wrap — a render that dies with "Failed to start transient
// scope unit" would be the worse failure, because it would look like FFmpeg
// breaking.
func memoryPart(mb int64) wrapPart {
	bin, err := exec.LookPath("systemd-run")
	if err != nil {
		return wrapPart{reason: "memory cap unwrapped: systemd-run is not installed, so there is no cgroup wrapper here"}
	}
	probe, perr := exec.LookPath("true")
	if perr != nil {
		probe = "/bin/true"
	}
	if _, err := os.Stat(probe); err != nil {
		return wrapPart{reason: "memory cap unwrapped: no trivial target to probe the scope with"}
	}
	capArg := "MemoryMax=" + strconv.FormatInt(mb, 10) + "M"
	args := scopeArgs([]string{capArg, "MemorySwapMax=0"}, "")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, append(append([]string{}, args...), probe)...).CombinedOutput()
	if err != nil {
		return wrapPart{reason: "memory cap unwrapped: systemd refused the scope (" + firstLine(string(out)) + ")"}
	}
	return wrapPart{kind: "memory", bin: bin, args: args}
}

// netPart resolves the network-namespace half. There is deliberately no
// fallback below this rung: if the kernel refuses the namespaces, the half
// steps aside and says so — inventing a weaker boundary and calling it the
// configured one is how a posture becomes fiction.
func netPart() wrapPart {
	bin, err := exec.LookPath("unshare")
	if err != nil {
		return wrapPart{reason: "network isolation unavailable: unshare is not installed here"}
	}
	probe := "/bin/true"
	if p, err := exec.LookPath("true"); err == nil {
		if _, err := os.Stat(p); err == nil {
			probe = p
		}
	}
	args := unshareArgs()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, append(append([]string{}, args...), probe)...).CombinedOutput()
	if err != nil {
		return wrapPart{reason: "network isolation refused by this kernel (" + firstLine(string(out)) + "): children run without it"}
	}
	return wrapPart{kind: "net", bin: bin, args: args}
}

// unshareArgs builds the namespace wrap: an unprivileged user namespace maps
// root inside (the only way an unprivileged caller may create the others), the
// network namespace is the boundary this rung exists for, and the PID/mount
// pair rides along — the child gets its own /proc (mount-proc needs the mount
// namespace) and cannot see or signal host processes. The trailing "--" hands
// everything after it to the child verbatim.
//
// Nothing here reaches for a kill relay: unshare(1)'s --kill-child was probed
// for exactly that role and measured inert (the flag is accepted and the child
// still survives its parent's death, with and without the PID namespace —
// probed on bookworm's util-linux 2.38), and the cancellation path does not
// lean on unmeasured flags. The kill that reaches the child lives in
// processgroup_unix.go: a group kill for same-namespace members plus direct
// descendant kills, which the kernel permits across the namespace boundary
// (measured) where group signals are not (also measured: group signals resolve
// per pid namespace, so the wrap's --pid shields the child from them).
func unshareArgs() []string {
	return []string{"--user", "--map-root-user", "--fork", "--mount", "--mount-proc", "--pid", "--net", "--"}
}

// scopeArgs builds the systemd-run argv up to and including the "--" that separates the
// wrapper's properties from the child. unit is empty for ordinary children (let systemd
// name the scope) and set for the doctor probe, which has to know which unit to ask
// about afterwards.
func scopeArgs(props []string, unit string) []string {
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
	for _, p := range props {
		a = append(a, "-p", p)
	}
	return append(a, "--")
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
	probe := exec.CommandContext(ctx, sr, append(scopeArgs([]string{capArg, "MemorySwapMax=0"}, unit), sleep, "2")...)
	if err := probe.Start(); err != nil {
		return false, false, "the probe scope would not start: " + err.Error()
	}
	time.Sleep(400 * time.Millisecond)

	show := exec.CommandContext(ctx, systemctl, "show", unit, "-p", "MemoryMax", "--value")
	if os.Geteuid() != 0 {
		show = exec.CommandContext(ctx, systemctl, "--user", "show", unit, "-p", "MemoryMax", "--value")
	}
	var queryErr bytes.Buffer
	show.Stderr = &queryErr
	out, err := show.Output()
	_ = probe.Wait() // let the scope finish rather than leak it; the unit is gone after this

	value := strings.TrimSpace(string(out))
	if err != nil {
		// The manager's own words, not just "exit status 1": this string is what an
		// operator pastes into a ticket, and memoryPart already quotes refusals.
		return false, false, "systemd could not be asked about the probe unit (" +
			firstLine(queryErr.String()) + "): " + err.Error()
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
