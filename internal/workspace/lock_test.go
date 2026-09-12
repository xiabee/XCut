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

// TestLockReusedPIDStartStampReclaimed: a lock whose owner PID is alive but
// whose start stamp differs (Windows PID reuse) must be reclaimed on the
// stale-cleanup retry — the PID alone used to keep a crashed owner's lock
// alive for up to 24h. Simulated with the CURRENT process as the "reused"
// owner: definitely alive, definitely a different stamp than the fake.
func TestLockReusedPIDStartStampReclaimed(t *testing.T) {
	w := New(t.TempDir())
	foreign := LockInfo{
		PID:       os.Getpid(), // alive by construction
		Host:      hostname(),
		Command:   "previous-crashed-owner",
		CreatedAt: time.Now().UTC().Add(-time.Hour), // far inside the 24h window
		PIDStart:  pidStartTime() + "-stale",
	}
	b, _ := json.Marshal(foreign)
	if err := os.WriteFile(filepath.Join(w.Root, lockFileName), b, 0o644); err != nil {
		t.Fatal(err)
	}
	release, err := w.Acquire("test")
	if err != nil {
		t.Fatalf("stale lock with a reused-but-alive PID not reclaimed: %v", err)
	}
	release()
}

// TestLockOwnPIDMatchingStampReenters: acquiring over a lock file carrying
// OUR OWN pid + host + start stamp is recursive re-entry — the start stamp
// is what distinguishes re-entry from a reused PID now owned by an
// unrelated process.
func TestLockOwnPIDMatchingStampReenters(t *testing.T) {
	w := New(t.TempDir())
	own := LockInfo{
		PID:       os.Getpid(),
		Host:      hostname(),
		Command:   "earlier-command-in-this-process",
		CreatedAt: time.Now().UTC().Add(-time.Minute),
		PIDStart:  pidStartTime(),
	}
	b, _ := json.Marshal(own)
	if err := os.WriteFile(filepath.Join(w.Root, lockFileName), b, 0o644); err != nil {
		t.Fatal(err)
	}
	release, err := w.Acquire("test")
	if err != nil {
		t.Fatalf("own-pid matching-stamp lock must re-enter: %v", err)
	}
	release()
}

// TestLockCorruptFileSelfHeals: a torn create/write (crash, power loss)
// leaves a zero-byte or partial lock no readLock can parse — that used to
// conflict forever (no owner to liveness-check) and only a human could fix
// it. Old debris self-heals; fresh debris (a possible in-flight create)
// keeps the conflict.
func TestLockCorruptFileSelfHeals(t *testing.T) {
	p := filepath.Join(New(t.TempDir()).Root, lockFileName)

	// Fresh garbage: still a conflict (a live writer may be mid-create).
	if err := os.WriteFile(p, []byte("{trunca"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := New(filepath.Dir(p))
	if _, err := w.Acquire("test"); err == nil {
		t.Fatal("fresh torn lock must still conflict")
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal("fresh torn lock must not be deleted")
	}

	// Aged garbage: debris — removed and the lock is taken.
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	release, err := w.Acquire("test")
	if err != nil {
		t.Fatalf("aged torn lock must self-heal: %v", err)
	}
	release()
}

// TestLockEmptyFileSelfHeals: the zero-byte variant of the same debris.
func TestLockEmptyFileSelfHeals(t *testing.T) {
	w := New(t.TempDir())
	p := filepath.Join(w.Root, lockFileName)
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	release, err := w.Acquire("test")
	if err != nil {
		t.Fatalf("empty lock file must self-heal: %v", err)
	}
	release()
}
