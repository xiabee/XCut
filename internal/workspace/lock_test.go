package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLockAcquireReleaseReacquire(t *testing.T) {
	w := New(t.TempDir())
	release, err := w.Acquire("test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(w.Root, lockFileName)); err != nil {
		t.Fatalf("lock file missing while held: %v", err)
	}
	release()
	if _, err := os.Stat(filepath.Join(w.Root, lockFileName)); !os.IsNotExist(err) {
		t.Fatalf("lock file survived release: %v", err)
	}
	release2, err := w.Acquire("test-again")
	if err != nil {
		t.Fatalf("reacquire after release: %v", err)
	}
	release2()
}

func TestLockConflictForeignHost(t *testing.T) {
	w := New(t.TempDir())
	// Simulate a live foreign owner: same PID (alive) but a different host —
	// never removable as stale (fresh timestamp), always a conflict.
	owner := LockInfo{PID: os.Getpid(), Host: "some-other-machine",
		Command: "serve", CreatedAt: time.Now().UTC()}
	b, _ := json.Marshal(owner)
	if err := os.WriteFile(filepath.Join(w.Root, lockFileName), b, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Acquire("test"); err == nil {
		t.Fatal("acquire must conflict on a foreign-held lock")
	} else {
		msg := err.Error()
		if !contains(msg, "pid") {
			t.Errorf("conflict message should name the owner: %v", err)
		}
	}
	// The conflicting lock file must still exist (we never delete live locks).
	if _, err := os.Stat(filepath.Join(w.Root, lockFileName)); err != nil {
		t.Fatal("foreign lock was deleted")
	}
}

func TestLockStaleRemovedWhenOwnerDead(t *testing.T) {
	w := New(t.TempDir())
	// A PID that cannot exist on any supported platform.
	dead := 1 << 30
	owner := LockInfo{PID: dead, Host: hostname(), Command: "render",
		CreatedAt: time.Now().UTC()}
	b, _ := json.Marshal(owner)
	if err := os.WriteFile(filepath.Join(w.Root, lockFileName), b, 0o644); err != nil {
		t.Fatal(err)
	}
	release, err := w.Acquire("test")
	if err != nil {
		t.Fatalf("stale lock of a dead owner must be reclaimed: %v", err)
	}
	release()
}

func TestLockImpossiblyOldIsStale(t *testing.T) {
	w := New(t.TempDir())
	owner := LockInfo{PID: os.Getpid(), Host: "remote-host",
		Command: "serve", CreatedAt: time.Now().UTC().Add(-48 * time.Hour)}
	b, _ := json.Marshal(owner)
	if err := os.WriteFile(filepath.Join(w.Root, lockFileName), b, 0o644); err != nil {
		t.Fatal(err)
	}
	release, err := w.Acquire("test")
	if err != nil {
		t.Fatalf("48h-old lock must be reclaimed even on another host: %v", err)
	}
	release()
}

func TestLockRecursiveReentry(t *testing.T) {
	w := New(t.TempDir())
	release1, err := w.Acquire("outer")
	if err != nil {
		t.Fatal(err)
	}
	release2, err := w.Acquire("inner")
	if err != nil {
		t.Fatalf("recursive re-entry must succeed: %v", err)
	}
	release2()
	// After the inner no-op release, the lock must still be held.
	if _, err := os.Stat(filepath.Join(w.Root, lockFileName)); err != nil {
		t.Fatal("lock file vanished after inner release")
	}
	release1()
	if _, err := os.Stat(filepath.Join(w.Root, lockFileName)); !os.IsNotExist(err) {
		t.Fatal("lock file survived outer release")
	}
}

func TestProcessAliveSmoke(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Fatal("own process must be alive")
	}
	if processAlive(1 << 30) {
		t.Fatal("impossible pid must be dead")
	}
	if processAlive(0) || processAlive(-1) {
		t.Fatal("non-pids must be dead")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
