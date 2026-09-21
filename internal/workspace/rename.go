package workspace

import (
	"errors"
	"os"
	"runtime"
	"syscall"
	"time"

	"github.com/xiabee/XCut/internal/xcerr"
)

// renameWaitBudget is how long a rename may keep waiting on a transient holder
// before the caller is told. The wait itself is risk-free — nothing is deleted
// and the source keeps its bytes, so a longer budget costs only a slower
// failure. A too-short budget costs the user a visible failure on a save that
// Defender was a moment from releasing: win-devops reported one
// `unexpected PUT status 500` in the concurrent-save test under the previous
// ~420 ms of escalating sleeps (6 attempts, 20..120 ms).
const renameWaitBudget = 2 * time.Second

// RetryableRename renames src onto dst, absorbing the Windows reality that
// Defender (and the indexer) briefly hold freshly written files: an
// os.Rename over such a file fails with a sharing violation / access
// denied, which documents rewritten in rapid succession (timeline saves,
// render publishes) would surface as spurious failures. Escalating backoff
// clears the scanner window; other platforms and other errors fail
// immediately.
func RetryableRename(src, dst string) error {
	var err error
	deadline := time.Now().Add(renameWaitBudget)
	for delay := 20 * time.Millisecond; ; delay *= 2 {
		if err = os.Rename(src, dst); err == nil {
			return nil
		}
		if runtime.GOOS != "windows" || !isWindowsRettableRename(err) {
			return err
		}
		if !time.Now().Before(deadline) {
			return err
		}
		time.Sleep(delay)
	}
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

// RetryableReplace publishes src onto dst with replace semantics, for the
// media outputs a re-render replaces while a client may still be playing the
// previous one. After the retryable rename is exhausted, on Windows it
// POSIX-deletes a delete-sharing holder's dst (freeing the name; the holder
// keeps reading the old bytes until EOF — serve opens downloads this way)
// and renames once more. Trade-off, deliberately accepted: once the delete
// lands, a still-failing rename means the previous output is gone and the
// job fails loudly — the user re-renders. The timeline document keeps plain
// RenameAtomic: nothing holds it for long, and its revision integrity is
// worth more than the narrow convenience.
func RetryableReplace(src, dst string) error {
	err := RetryableRename(src, dst)
	if err == nil {
		return nil
	}
	if runtime.GOOS != "windows" || !isWindowsRettableRename(err) {
		return err
	}
	if posixRemove(dst) != nil {
		return err // holder keeps the name pinned (no delete-share); report it
	}
	return os.Rename(src, dst)
}
