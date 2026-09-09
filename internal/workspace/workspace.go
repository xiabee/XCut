// Package workspace owns the XCut data directory layout, path-safety guards,
// and temp-file lifecycle.
//
// Layout:
//
//	<root>/
//	  config.json        (default config, written by `xcut init`)
//	  xcut.db            (SQLite state)
//	  projects/<id>/     (timelines, exports)
//	  cache/             (analysis artifacts, keyed by cache key)
//	  temp/              (per-job scratch dirs; disposable)
//	  logs/
package workspace

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/xiabee/XCut/internal/xcerr"
)

// Workspace is a handle on an XCut data directory.
type Workspace struct {
	Root string
	// MaxTempBytes caps scratch space under temp/ (resource.max_temp_gb).
	// NewTempDir refuses to add new scratch once the budget is reached.
	// 0 disables the gate (tests).
	MaxTempBytes int64
}

// Subdirectory names (never change: they appear on disk).
const (
	DirProjects = "projects"
	DirCache    = "cache"
	DirTemp     = "temp"
	DirLogs     = "logs"
)

// New returns a Workspace rooted at dir (already resolved/absolute).
func New(root string) *Workspace { return &Workspace{Root: root} }

// DBPath is the SQLite database location.
func (w *Workspace) DBPath() string { return filepath.Join(w.Root, "xcut.db") }

// ProjectsDir / CacheDir / TempDir / LogsDir.
func (w *Workspace) ProjectsDir() string { return filepath.Join(w.Root, DirProjects) }
func (w *Workspace) CacheDir() string    { return filepath.Join(w.Root, DirCache) }
func (w *Workspace) TempDir() string     { return filepath.Join(w.Root, DirTemp) }
func (w *Workspace) LogsDir() string     { return filepath.Join(w.Root, DirLogs) }

// Ensure creates the directory skeleton. Idempotent.
func (w *Workspace) Ensure() error {
	for _, d := range []string{w.Root, w.ProjectsDir(), w.CacheDir(), w.TempDir(), w.LogsDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return xcerr.E(xcerr.CodeInternal, "cannot create workspace directory "+filepath.Base(d), err)
		}
	}
	return nil
}

// Writable verifies the workspace accepts writes (probe file create/delete).
func (w *Workspace) Writable() error {
	probe := filepath.Join(w.Root, ".write-probe")
	if err := os.WriteFile(probe, []byte("ok"), 0o644); err != nil {
		return xcerr.E(xcerr.CodeInternal, "workspace is not writable", err)
	}
	_ = os.Remove(probe)
	return nil
}

// SafeJoin resolves name under root, rejecting absolute paths, parent
// traversal, Windows drive/UNC tricks, reserved device names, and symlink
// escapes. The returned path is guaranteed (best effort on symlinks) to stay
// inside the workspace.
func (w *Workspace) SafeJoin(name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", xcerr.E(xcerr.CodeValidation, "empty path", nil)
	}
	p := filepath.Clean(name)

	// Absolute in any form: /x, \x, C:\x, C:x (drive-relative), \\unc.
	if filepath.IsAbs(p) {
		return "", xcerr.E(xcerr.CodeValidation, "absolute paths are not allowed here", nil)
	}
	// Backslash-rooted ("\x") is absolute on Windows and a legal-but-hostile
	// filename elsewhere; reject on ALL platforms so paths generated on one OS
	// can never be misinterpreted on another (workspaces may live on shares).
	if strings.HasPrefix(p, `\`) || strings.HasPrefix(p, `/`) {
		return "", xcerr.E(xcerr.CodeValidation, "rooted paths are not allowed here", nil)
	}
	if runtime.GOOS == "windows" {
		if vol := filepath.VolumeName(p); vol != "" {
			return "", xcerr.E(xcerr.CodeValidation, "drive-qualified paths are not allowed here", nil)
		}
	}
	for _, elem := range strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' }) {
		switch elem {
		case "..":
			return "", xcerr.E(xcerr.CodeValidation, "path traversal is not allowed", nil)
		}
		if isWindowsReservedName(elem) {
			return "", xcerr.E(xcerr.CodeValidation, "reserved device name in path", nil)
		}
	}

	full := filepath.Join(w.Root, p)

	// Containment double-check after cleaning.
	rel, err := filepath.Rel(w.Root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", xcerr.E(xcerr.CodeValidation, "path escapes the workspace", nil)
	}

	// Symlink escape check on the deepest existing ancestor.
	if err := w.checkSymlinkEscape(full); err != nil {
		return "", err
	}
	return full, nil
}

// checkSymlinkEscape resolves symlinks on the longest existing prefix of full
// and verifies it remains under the (resolved) root.
func (w *Workspace) checkSymlinkEscape(full string) error {
	realRoot, err := filepath.EvalSymlinks(w.Root)
	if err != nil {
		// Root must exist for meaningful checks; if it cannot be resolved,
		// fail closed for safety-critical joins.
		return xcerr.E(xcerr.CodeInternal, "cannot resolve workspace root", err)
	}
	probe := full
	for {
		if resolved, err := filepath.EvalSymlinks(probe); err == nil {
			rel, err := filepath.Rel(realRoot, resolved)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return xcerr.E(xcerr.CodeValidation, "path escapes the workspace via symlink", nil)
			}
			return nil
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return nil
		}
		probe = parent
	}
}

func isWindowsReservedName(elem string) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	base := strings.ToUpper(elem)
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	switch base {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return true
	}
	return false
}

// NewTempDir creates temp/<prefix>-<rand> for a job's scratch files. When
// MaxTempBytes is set and temp/ already sits at or over the budget, it
// refuses (resource_limit): failed renders keep their scratch for debugging,
// so without this gate repeated failures could fill the disk silently.
func (w *Workspace) NewTempDir(prefix string) (string, error) {
	if w.MaxTempBytes > 0 {
		if used := w.TempUsage(); used >= w.MaxTempBytes {
			return "", xcerr.E(xcerr.CodeResourceLimit,
				fmt.Sprintf("temp budget exhausted (%s in use, budget %s) — run 'xcut cleanup' to reclaim failed-run scratch, or raise resource.max_temp_gb",
					humanBytes(used), humanBytes(w.MaxTempBytes)), nil)
		}
	}
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", xcerr.E(xcerr.CodeInternal, "cannot generate temp dir id", err)
	}
	dir := filepath.Join(w.TempDir(), prefix+"-"+hex.EncodeToString(buf))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", xcerr.E(xcerr.CodeInternal, "cannot create temp dir", err)
	}
	return dir, nil
}

// TempUsage reports bytes currently stored under temp/ (best-effort walk;
// 0 when the directory does not exist).
func (w *Workspace) TempUsage() int64 {
	var total int64
	_ = filepath.WalkDir(w.TempDir(), func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, ierr := d.Info(); ierr == nil {
				total += fi.Size()
			}
		}
		return nil
	})
	return total
}

// humanBytes renders a byte count for user-facing messages.
func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// CleanupTemp removes everything under temp/ (it is disposable by definition).
// dry-run reports what would be removed. Returns entries and reclaimed bytes.
func (w *Workspace) CleanupTemp(dryRun bool) (removed []string, bytes int64, err error) {
	entries, rerr := os.ReadDir(w.TempDir())
	if rerr != nil {
		if os.IsNotExist(rerr) {
			return nil, 0, nil
		}
		return nil, 0, xcerr.E(xcerr.CodeInternal, "cannot read temp dir", rerr)
	}
	for _, e := range entries {
		p := filepath.Join(w.TempDir(), e.Name())
		size := int64(0)
		if fi, err := e.Info(); err == nil && !e.IsDir() {
			size = fi.Size()
		} else {
			_ = filepath.WalkDir(p, func(_ string, d os.DirEntry, err error) error {
				if err == nil && !d.IsDir() {
					if fi, ierr := d.Info(); ierr == nil {
						size += fi.Size()
					}
				}
				return nil
			})
		}
		removed = append(removed, e.Name())
		bytes += size
		if !dryRun {
			if err := os.RemoveAll(p); err != nil {
				return removed, bytes, xcerr.E(xcerr.CodeInternal, "cannot remove temp entry "+e.Name(), err)
			}
		}
	}
	return removed, bytes, nil
}

// CleanupPartials removes stale atomic-write debris anywhere under
// projects/: render partials (*.partial) and WriteAtomic temp files
// (.tmp-*). Both are only ever renamed into place after their content is
// complete/verified, so a leftover one is crash debris — never valid output
// and never user data. dry-run reports without removing. Returns the number
// of entries and their bytes.
func (w *Workspace) CleanupPartials(dryRun bool) (count int, bytes int64, err error) {
	root := w.ProjectsDir()
	if _, serr := os.Stat(root); os.IsNotExist(serr) {
		return 0, 0, nil
	}
	werr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		isDebris := strings.HasSuffix(d.Name(), ".partial") || strings.HasPrefix(d.Name(), ".tmp-")
		if !isDebris {
			return nil
		}
		size := int64(0)
		if fi, ierr := d.Info(); ierr == nil {
			size = fi.Size()
		}
		count++
		bytes += size
		if !dryRun {
			if rerr := os.Remove(path); rerr != nil {
				return xcerr.E(xcerr.CodeInternal, "cannot remove partial "+filepath.Base(path), rerr)
			}
		}
		return nil
	})
	if werr != nil {
		return count, bytes, xcerr.E(xcerr.CodeInternal, "cannot scan projects dir for partials", werr)
	}
	return count, bytes, nil
}
