//go:build windows

package workspace

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// currentProcessStart returns this process's creation time as an opaque
// string (Windows FILETIME, 100ns units since 1601-01-01 UTC). Only equality
// between an acquired stamp and a later query is meaningful; the format is
// never parsed.
func currentProcessStart() string {
	s, _ := processStartTime(os.Getpid())
	return s
}

// processStartTime returns the start stamp of the process with the given
// PID (same opaque format). ok=false when the process does not exist or the
// stamp cannot be read (caller then falls back to conservative behavior).
func processStartTime(pid int) (string, bool) {
	const processQueryLimitedInformation = 0x1000
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	open := kernel32.NewProc("OpenProcess")
	handle, _, err := open.Call(
		uintptr(processQueryLimitedInformation),
		0,
		uintptr(pid),
	)
	if handle == 0 {
		// Access denied means the process exists but is protected — a stamp
		// is unobtainable, and the caller must treat the process as alive.
		_ = err
		return "", false
	}
	defer closeHandle(handle)

	var create, exit, kernel, user syscall.Filetime
	getTimes := kernel32.NewProc("GetProcessTimes")
	r, _, cerr := getTimes.Call(
		handle,
		uintptr(unsafe.Pointer(&create)),
		uintptr(unsafe.Pointer(&exit)),
		uintptr(unsafe.Pointer(&kernel)),
		uintptr(unsafe.Pointer(&user)),
	)
	if r == 0 {
		_ = cerr
		return "", false
	}
	stamp := (uint64(create.HighDateTime) << 32) | uint64(create.LowDateTime)
	return fmt.Sprintf("%d", stamp), true
}
