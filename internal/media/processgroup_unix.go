//go:build unix

package media

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// The sandbox wraps turn the direct child into a wrapper — systemd-run or
// unshare — with the real work one or two forks deeper. Killing the direct
// child (exec.CommandContext's default cancellation) stops at the wrapper and
// strands the encoder; the sandbox suite's cancellation legs measured exactly
// that before this existed, in both the netns-only and the scope+net chain
// shape. Two kernel mechanisms are composed here, because each was measured to
// cover exactly one of the two distances the kill has to travel:
//
//   - the process group reaches every member in the caller's own PID
//     namespace: the bare child, the scope's client-side fork, unshare
//     itself. It does NOT reach the namespace-wrapped encoder: group signals
//     resolve per pid namespace, so the wrap's --pid shields the child from
//     them even though `ps` shows the same pgid (probed; the child survived a
//     SIGKILL to its own group while the identical shape without --pid died).
//   - a direct kill to a descendant's pid crosses the namespace boundary —
//     the caller's capabilities in the ancestor user namespace carry it
//     (probed: the same child that survived the group kill died from a direct
//     one). Descendants are enumerated from /proc while the tree is still
//     intact (the ppid chain dissolves once the direct child is reaped), then
//     killed individually.
//
// unshare(1)'s own --kill-child was probed for this role first and measured
// inert — accepted on the command line, never delivered — so it ships nowhere.
//
// Windows answers the same problem with the kill-on-close job object and keeps
// the no-op stub.
func armProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return stopProcessTree(cmd.Process)
	}
}

// stopProcessTree SIGKILLs the child's descendants first (the ppid chain is
// only trustworthy while the direct child lives), then the process group, then
// the child itself. ESRCH anywhere means that target was already gone — the
// done answer, not an error.
func stopProcessTree(p *os.Process) error {
	if p == nil {
		return nil
	}
	for _, d := range descendantPids(p.Pid) {
		_ = syscall.Kill(d, syscall.SIGKILL)
	}
	_ = syscall.Kill(-p.Pid, syscall.SIGKILL)
	if err := syscall.Kill(p.Pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
		return err
	}
	return nil
}

// descendantPids walks /proc's ppid chains from root. On a host without /proc
// (macOS) it finds nothing and the group kill carries the load alone — the
// wrap rung this enumeration exists for is Linux-only anyway.
func descendantPids(root int) []int {
	children := make(map[int][]int)
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		ppid, ok := parentPid(pid)
		if !ok {
			continue
		}
		children[ppid] = append(children[ppid], pid)
	}
	var out []int
	frontier := []int{root}
	seen := map[int]bool{root: true}
	for len(frontier) > 0 {
		cur := frontier[0]
		frontier = frontier[1:]
		for _, c := range children[cur] {
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
				frontier = append(frontier, c)
			}
		}
	}
	return out
}

// parentPid reads the ppid field out of /proc/<pid>/stat. The field sits after
// the comm in parentheses, and a comm may contain spaces and parentheses of
// its own, so the parse starts after the LAST ')'.
func parentPid(pid int) (int, bool) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, false
	}
	s := string(data)
	i := strings.LastIndexByte(s, ')')
	if i < 0 || i+2 >= len(s) {
		return 0, false
	}
	fields := strings.Fields(s[i+2:]) // state, ppid, ...
	if len(fields) < 2 {
		return 0, false
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, false
	}
	return ppid, true
}
