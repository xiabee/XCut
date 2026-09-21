package workspace

import (
	"runtime"
	"syscall"
	"testing"
	"time"
)

// TestRetryTransientWaitsOnlyForSharingConflicts pins the two decisions the loop
// makes, in both directions: a conflict that clears must be absorbed, and
// anything else must fail at once. The second one is the one that would be
// cheap to get wrong — retrying ERROR_FILE_NOT_FOUND would turn every "no
// timeline yet" answer into a multi-second stall on the way to a fast 404.
func TestRetryTransientWaitsOnlyForSharingConflicts(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the retry engages only on the Windows sharing errors")
	}

	calls := 0
	start := time.Now()
	if err := RetryTransient(func() error {
		calls++
		if calls < 3 {
			return syscall.Errno(5) // ERROR_ACCESS_DENIED, as a held name answers
		}
		return nil
	}); err != nil {
		t.Fatalf("a conflict that cleared on the third attempt was reported: %v", err)
	}
	if waited := time.Since(start); calls < 3 || waited < 60*time.Millisecond {
		t.Errorf("expected >=3 attempts across the backoff, got %d in %s", calls, waited)
	}

	missing := 0
	start = time.Now()
	err := RetryTransient(func() error {
		missing++
		return syscall.Errno(2) // ERROR_FILE_NOT_FOUND
	})
	if err == nil {
		t.Fatal("a missing file was retried into success")
	}
	if spent := time.Since(start); spent > 100*time.Millisecond || missing != 1 {
		t.Errorf("non-sharing error: %d attempts in %s, want one and immediately", missing, spent)
	}
}
