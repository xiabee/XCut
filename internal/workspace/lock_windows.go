//go:build windows

package workspace

import (
	"syscall"
	"unsafe"
)

// processAlive reports whether a process with the given PID exists on this
// host (conservative: ambiguous errors count as alive so a lock is never
// removed while its owner might be running).
func processAlive(pid int) bool {
	const processQueryLimitedInformation = 0x1000
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	open := kernel32.NewProc("OpenProcess")
	handle, _, err := open.Call(
		uintptr(processQueryLimitedInformation),
		0,
		uintptr(pid),
	)
	if handle == 0 {
		// Access denied means the process exists but is protected.
		if err == syscall.ERROR_ACCESS_DENIED {
			return true
		}
		return false
	}
	defer closeHandle(handle)
	var exitCode uint32
	getExit := kernel32.NewProc("GetExitCodeProcess")
	getExit.Call(handle, uintptr(unsafe.Pointer(&exitCode)))
	// STILL_ACTIVE == 259
	return exitCode == 259
}

func closeHandle(h uintptr) {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	kernel32.NewProc("CloseHandle").Call(h)
}
