//go:build !windows

package cli

// openExistingInstance is unreachable off Windows: the double-click path
// that consults it only exists there. Keep the stub so the caller
// compiles on every platform.
func openExistingInstance(string) bool { return false }
