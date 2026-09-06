//go:build windows

package workspace

import (
	"path/filepath"

	"golang.org/x/sys/windows"

	"github.com/xiabee/XCut/internal/xcerr"
)

// DiskFree returns free bytes on the volume containing dir.
func DiskFree(dir string) (uint64, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return 0, xcerr.E(xcerr.CodeValidation, "cannot resolve directory", err)
	}
	var free, total, avail uint64
	// utf16PtrFromString + GetDiskFreeSpaceEx
	p16, err := windows.UTF16PtrFromString(abs)
	if err != nil {
		return 0, xcerr.E(xcerr.CodeValidation, "invalid directory path", err)
	}
	if err := windows.GetDiskFreeSpaceEx(p16, &avail, &total, &free); err != nil {
		return 0, xcerr.E(xcerr.CodeInternal, "cannot query disk space", err)
	}
	return free, nil
}
