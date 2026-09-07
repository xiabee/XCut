//go:build !windows

package workspace

import (
	"syscall"
)

// processAlive reports whether a process with the given PID exists on this
// host (signal 0 probe; EPERM counts as alive — the process exists but is
// owned by someone else).
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	switch err {
	case nil:
		return true
	case syscall.ESRCH:
		return false
	case syscall.EPERM:
		return true
	default:
		return true // conservative
	}
}
