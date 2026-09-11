package workspace

import (
	"errors"
	"os"
	"runtime"
	"syscall"
	"time"

	"github.com/xiabee/XCut/internal/xcerr"
)

// RetryableRename renames src onto dst, absorbing the Windows reality that
// Defender (and the indexer) briefly hold freshly written files: an
// os.Rename over such a file fails with a sharing violation / access
// denied, which documents rewritten in rapid succession (timeline saves,
// render publishes) would surface as spurious failures. A short escalating
// backoff clears the scanner window; other platforms and other errors fail
// immediately.
func RetryableRename(src, dst string) error {
	var err error
	for attempt := 0; attempt < 6; attempt++ {
		if err = os.Rename(src, dst); err == nil {
			return nil
		}
		if runtime.GOOS != "windows" || !isWindowsRettableRename(err) {
			return err
		}
		time.Sleep(time.Duration(20*(attempt+1)) * time.Millisecond)
	}
	return err
}

// isWindowsRettableRename matches the errnos a rename over a held file
// produces on Windows: ERROR_ACCESS_DENIED (5), ERROR_SHARING_VIOLATION (32),
// ERROR_LOCK_VIOLATION (33).
func isWindowsRettableRename(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	return errno == 5 || errno == 32 || errno == 33
}

// RenameAtomic is RetryableRename wrapped in the package's user-safe error
// model, for publish paths that want a ready-made message.
func RenameAtomic(src, dst string) error {
	if err := RetryableRename(src, dst); err != nil {
		return xcerr.E(xcerr.CodeInternal, "cannot finalize output file", err)
	}
	return nil
}
