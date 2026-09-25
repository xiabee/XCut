//go:build linux

package config

import (
	"golang.org/x/sys/unix"
)

// totalMemoryBytes reports the machine's physical memory via sysinfo.
func totalMemoryBytes() uint64 {
	var si unix.Sysinfo_t
	if err := unix.Sysinfo(&si); err != nil {
		return 0
	}
	return uint64(si.Totalram) * uint64(si.Unit)
}
