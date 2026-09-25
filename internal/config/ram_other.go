//go:build !windows && !linux

package config

// totalMemoryBytes has no portable answer on this platform; the sizer skips
// the RAM guard and sizes from CPUs alone.
func totalMemoryBytes() uint64 { return 0 }
