//go:build windows

package config

import (
	"syscall"
	"unsafe"
)

// totalMemoryBytes reports the machine's physical memory via
// kernel32!GlobalMemoryStatusEx — the same source Task Manager's "total" uses.
// x/sys/windows does not wrap this call, so it follows the repo's LazyDLL
// pattern (internal/media/jobobject_windows.go). 0 = could not report; the
// sizer then skips the RAM guard.
func totalMemoryBytes() uint64 {
	// memoryStatusEx mirrors Win32 MEMORYSTATUSEX exactly (field order matters).
	type memoryStatusEx struct {
		Length               uint32
		MemoryLoad           uint32
		TotalPhys            uint64
		AvailPhys            uint64
		TotalPageFile        uint64
		AvailPageFile        uint64
		TotalVirtual         uint64
		AvailVirtual         uint64
		AvailExtendedVirtual uint64
	}
	var st memoryStatusEx
	st.Length = uint32(unsafe.Sizeof(st))

	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	proc := kernel32.NewProc("GlobalMemoryStatusEx")
	if r1, _, _ := proc.Call(uintptr(unsafe.Pointer(&st))); r1 == 0 {
		return 0
	}
	return st.TotalPhys
}
