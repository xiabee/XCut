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

// SandboxEnforcement has nothing to measure where there is no wrapper: applied=false says
// no cap is in force, known=false says that is a platform fact rather than a refusal —
// on Windows the job object (jobobject_windows.go) is the mechanism, and doctor words
// that row from its own evidence.
func SandboxEnforcement() (applied, known bool, detail string) {
	return false, false, "no systemd scope mechanism on this platform"
}
