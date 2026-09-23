package workspace

import (
	"errors"
	"os"
	"runtime"
	"syscall"
	"time"
)

// transientIOWaitBudget is how long an operation may keep waiting on a
// transient Windows sharing conflict before the caller is told. Waiting is
// risk-free here — nothing is deleted, and a failed rename leaves the source
// intact — so the only cost of a longer budget is a slower failure, while a
// too-short one turns a momentary holder into a user-visible failure. Two
// shapes were hit at the old ~420 ms (6 escalating sleeps of 20..120 ms): a
// rename over a destination another handle still held, and the read of a
// document whose name was being replaced underneath it — the latter surfaced
// as a 500 on a GET in api.TestTimelineRegenAndPutRevisionUniqueness (the
// test's own label said PUT, which is a second bug, fixed in that test).
const transientIOWaitBudget = 2 * time.Second

// RetryTransient runs op until it succeeds, retrying only the errors Windows
// answers with when a file is briefly held by someone else. Every other error
// — and op itself once the budget has run out — returns immediately, so a
// genuinely missing file stays a fast NotFound rather than a 2 s stall.
func RetryTransient(op func() error) error {
	var err error
	deadline := time.Now().Add(transientIOWaitBudget)
	for delay := 20 * time.Millisecond; ; delay *= 2 {
		if err = op(); err == nil {
			return nil
		}
		if runtime.GOOS != "windows" || !isWindowsTransientIO(err) {
			return err
		}
		if !time.Now().Before(deadline) {
			return err
		}
		time.Sleep(delay)
	}
}

// RetryableRename renames src onto dst, absorbing the Windows reality that
// Defender (and the indexer) briefly hold freshly written files: an
// os.Rename over such a file fails with a sharing violation / access
// denied, which documents rewritten in rapid succession (timeline saves,
// render publishes) would surface as spurious failures.
func RetryableRename(src, dst string) error {
	return RetryTransient(func() error { return os.Rename(src, dst) })
}

// isWindowsTransientIO matches the errnos Windows answers with when a file is
// briefly held by someone else: a rename over a held destination, or a read of
// a name being replaced underneath it, both surface as one of these.
func isWindowsTransientIO(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	return errno == 5 || errno == 32 || errno == 33
}

// RetryableReplace publishes src onto dst with replace semantics, for the
// media outputs a re-render replaces while a client may still be playing the
// previous one. After the retryable rename is exhausted, on Windows it
// POSIX-deletes a delete-sharing holder's dst (freeing the name; the holder
// keeps reading the old bytes until EOF — serve opens downloads this way)
// and renames once more. Trade-off, deliberately accepted: once the delete
// lands, a still-failing rename means the previous output is gone and the
// job fails loudly — the user re-renders. The timeline document keeps plain
// RetryableRename (pipeline.WriteAtomic): nothing holds it for long, and its
// revision integrity is worth more than the narrow convenience.
func RetryableReplace(src, dst string) error {
	err := RetryableRename(src, dst)
	if err == nil {
		return nil
	}
	if runtime.GOOS != "windows" || !isWindowsTransientIO(err) {
		return err
	}
	if posixRemove(dst) != nil {
		return err // holder keeps the name pinned (no delete-share); report it
	}
	return os.Rename(src, dst)
}
