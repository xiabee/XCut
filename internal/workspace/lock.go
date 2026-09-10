package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/xiabee/XCut/internal/xcerr"
)

// Workspace lock: concurrent XCut processes must not write one workspace
// (SQLite handles concurrent access, but job bookkeeping, temp cleanup and
// cache eviction make cross-process writes unsafe). Writers take an exclusive
// lock file <root>/xcut.lock; readers (list/show/doctor) never block.
//
// Crash safety: the lock carries the owning PID; a later acquirer verifies
// liveness and removes the file of a dead owner, so a crashed process can
// never deadlock the workspace permanently. A lock older than staleAfter is
// removed regardless (covers PID reuse and remote-host edge cases).

const lockFileName = "xcut.lock"

// staleAfter is the age at which a lock is considered dead even if its owner
// cannot be checked (remote host) — generous by design.
const staleAfter = 24 * time.Hour

// lockTTL seconds before the owner refreshes the lock file mtime. Not used
// for liveness (PID check is), only as evidence for the age heuristic.

// LockInfo describes a lock owner (serialized into the lock file).
type LockInfo struct {
	PID       int       `json:"pid"`
	Host      string    `json:"host"`
	Command   string    `json:"command"`
	CreatedAt time.Time `json:"created_at"`
	// PIDStart is the owner's process start stamp as an opaque
	// platform-specific string (empty for lock files written before this
	// field existed). Windows reuses PIDs aggressively: a crashed owner's
	// PID can be live again under an unrelated process within minutes, so
	// liveness is judged by (PID, start stamp) equality, not the PID alone.
	PIDStart string `json:"pid_start,omitempty"`
}

// lock holds this process's acquired locks (recursive re-entry allowed).
var (
	locksMu   sync.Mutex
	heldLocks = map[string]*LockInfo{} // by absolute lock path
)

// Acquire takes the exclusive workspace lock. It fails with CodeConflict
// (user-safe message naming the owner) when another live process holds it,
// and allows recursive acquisition by the same process. The returned release
// function must be called when writes are done.
func (w *Workspace) Acquire(command string) (release func(), err error) {
	if err := w.Ensure(); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(w.Root, lockFileName)

	locksMu.Lock()
	if heldLocks[lockPath] != nil {
		locksMu.Unlock()
		return func() {}, nil // recursive re-entry
	}
	locksMu.Unlock()

	for attempt := 0; attempt < 2; attempt++ {
		release, err = w.tryAcquire(lockPath, command)
		if err == nil {
			return release, nil
		}
		if attempt == 0 {
			// One stale-cleanup retry after a failed first take.
			if staleErr := w.removeStaleLock(lockPath); staleErr != nil {
				return nil, err // real conflict, report it
			}
			continue
		}
	}
	return nil, err
}

// pidStartTime returns the current process's start stamp (see LockInfo).
var pidStartTime = sync.OnceValue(func() string { return currentProcessStart() })

// tryAcquire creates the lock file atomically (O_EXCL). A concurrent loser
// gets a conflict error.
func (w *Workspace) tryAcquire(lockPath, command string) (func(), error) {
	info := LockInfo{
		PID:       os.Getpid(),
		Host:      hostname(),
		Command:   command,
		CreatedAt: time.Now().UTC(),
		PIDStart:  pidStartTime(),
	}
	b, err := json.Marshal(info)
	if err != nil {
		return nil, xcerr.E(xcerr.CodeInternal, "cannot serialize lock", err)
	}

	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsExist(err) {
			if owner, rerr := readLock(lockPath); rerr == nil {
				if owner.PID == os.Getpid() && owner.Host == hostname() &&
					(owner.PIDStart == "" || owner.PIDStart == pidStartTime()) {
					// Our own lock from a prior handle — re-entry. An empty
					// PIDStart means a pre-existing lock file from an older
					// build; the PID+host match keeps that path working.
					locksMu.Lock()
					heldLocks[lockPath] = &owner
					locksMu.Unlock()
					return func() {}, nil
				}
				return nil, w.conflict(owner)
			}
			return nil, xcerr.E(xcerr.CodeConflict,
				"workspace is locked by another process (cannot read "+lockFileName+")", nil)
		}
		return nil, xcerr.E(xcerr.CodeInternal, "cannot create workspace lock", err)
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		_ = os.Remove(lockPath)
		return nil, xcerr.E(xcerr.CodeInternal, "cannot write workspace lock", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(lockPath)
		return nil, xcerr.E(xcerr.CodeInternal, "cannot finalize workspace lock", err)
	}

	locksMu.Lock()
	heldLocks[lockPath] = &info
	locksMu.Unlock()
	return func() { w.release(lockPath) }, nil
}

func (w *Workspace) release(lockPath string) {
	locksMu.Lock()
	info := heldLocks[lockPath]
	delete(heldLocks, lockPath)
	locksMu.Unlock()
	if info == nil {
		return
	}
	// Remove only if we still own the file (never delete a successor's lock).
	if owner, err := readLock(lockPath); err == nil && owner.PID == info.PID && owner.CreatedAt.Equal(info.CreatedAt) {
		_ = os.Remove(lockPath)
	}
}

// removeStaleLock deletes the lock file when its owner is provably dead
// (no such process on this host, or the PID now belongs to a different
// process — reuse) or impossibly old. Returns nil when a stale lock was
// removed and the caller may retry.
func (w *Workspace) removeStaleLock(lockPath string) error {
	owner, err := readLock(lockPath)
	if err != nil {
		return err // unreadable → treat as a live conflict
	}
	if owner.Host == hostname() {
		if !processAlive(owner.PID) {
			_ = os.Remove(lockPath)
			return nil
		}
		// The PID is alive, but is it the SAME process? A reused PID must
		// not keep a crashed owner's lock alive for a day. If either side
		// lacks a start stamp (legacy lock file), fall back to the age rule.
		if owner.PIDStart != "" {
			if cur, ok := processStartTime(owner.PID); ok && cur != owner.PIDStart {
				_ = os.Remove(lockPath)
				return nil
			}
		}
	}
	if time.Since(owner.CreatedAt) > staleAfter {
		_ = os.Remove(lockPath)
		return nil
	}
	return w.conflict(owner)
}

func (w *Workspace) conflict(owner LockInfo) error {
	return xcerr.E(xcerr.CodeConflict,
		fmt.Sprintf("workspace is locked by another process (pid %d on %s, started %s, command %q)",
			owner.PID, owner.Host, owner.CreatedAt.Local().Format("2006-01-02 15:04:05"), owner.Command), nil)
}

func readLock(path string) (LockInfo, error) {
	var info LockInfo
	b, err := os.ReadFile(path)
	if err != nil {
		return info, err
	}
	if err := json.Unmarshal(b, &info); err != nil {
		return info, err
	}
	if info.PID <= 0 || info.CreatedAt.IsZero() {
		return info, fmt.Errorf("malformed lock")
	}
	return info, nil
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return h
}
