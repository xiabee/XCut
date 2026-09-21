//go:build !windows

package workspace

import (
	"golang.org/x/sys/unix"

	"github.com/xiabee/XCut/internal/xcerr"
)

// DiskFree returns free bytes on the filesystem containing dir.
func DiskFree(dir string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, xcerr.E(xcerr.CodeInternal, "cannot query disk space", err)
	}
	if st.Bsize <= 0 {
		// A negative block size would wrap into an astronomically large free-space
		// figure and open every disk guard that reads this, so refuse it. The guard
		// is a regression gate, not a tested case: it needs a filesystem that lies.
		return 0, xcerr.E(xcerr.CodeInternal, "disk reports an implausible block size", nil)
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil // #nosec G115 -- Bsize is proven positive just above; Bavail is already uint64
}
