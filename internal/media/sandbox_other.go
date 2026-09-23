//go:build !linux

package media

// wrapChild leaves the child alone: the per-child memory cap on Windows lives in the
// job object (jobobject_windows.go), and no other non-Linux platform this project
// targets has an equivalent to reach for.
func wrapChild(bin string, args ...string) (string, []string) { return bin, args }

// SandboxPosture is empty where there is no mechanism to describe; `xcut doctor`
// already words those platforms' rows itself.
func SandboxPosture() string { return "" }

// SandboxArmed reports that nothing is wrapping children here.
func SandboxArmed() bool { return false }
