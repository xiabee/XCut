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
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
