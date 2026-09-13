//go:build !windows

package media

import "os"

// attachJob is the non-Windows no-op: the kill-on-parent-death backstop
// there would be PR_SET_PDEATHSIG, which is per-thread and unreliable
// under the Go runtime's threading — the context-kill path stays the
// cleanup mechanism, and crash reconciliation handles orphans at startup.
func attachJob(*os.Process) {}
