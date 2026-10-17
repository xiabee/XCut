//go:build !unix

package media

import (
	"os"
	"os/exec"
)

// Windows has no process-group mechanism to arm; the kill-on-close job object
// (attachJob) already takes the whole subtree on every exit path this package
// has. Cancellation keeps the default process kill and the direct-stop path
// keeps the direct kill — byte for byte the behavior that shipped before the
// Linux tree stop existed.
func armProcessGroup(*exec.Cmd) {}

func stopProcessTree(p *os.Process) error {
	if p == nil {
		return nil
	}
	return p.Kill()
}
