package analysis

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/xiabee/XCut/internal/xcerr"
)

// dirUsage counts entries and bytes of a flat cache directory. A missing
// directory is an empty cache, not an error.
func dirUsage(dir string) (count int, bytes int64, err error) {
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, xcerr.E(xcerr.CodeInternal, "cannot read cache dir", err)
	}
	var total int64
	for _, e := range dirEntries {
		if e.IsDir() {
			continue
		}
		if fi, ierr := e.Info(); ierr == nil {
			total += fi.Size()
		}
	}
	return len(dirEntries), total, nil
}

// touchRecency refreshes an entry's mtime to now, promoting it in the LRU
// order. Best-effort: a failed touch only means the entry keeps its old
// recency and may be evicted sooner — never a reason to fail a cache hit.
func touchRecency(path string) {
	now := time.Now()
	_ = os.Chtimes(path, now, now)
}

// evictItem is one removable entry: its path, size and recency stamp.
type evictItem struct {
	path  string
	size  int64
	mtime int64
}

// scanEvictable walks a flat cache directory: returns the removable items
// (in-flight .tmp-* scratch excluded, but its bytes counted in total) sorted
// oldest-recency-first, plus the directory's total byte size.
func scanEvictable(dir string) (items []evictItem, total int64, err error) {
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, xcerr.E(xcerr.CodeInternal, "cannot read cache dir", err)
	}
	for _, e := range dirEntries {
		if e.IsDir() {
			continue
		}
		fi, ierr := e.Info()
		if ierr != nil {
			continue
		}
		total += fi.Size()
		if strings.HasPrefix(e.Name(), ".tmp-") {
			continue // in-flight scratch: never a victim (see evictDirTo)
		}
		items = append(items, evictItem{filepath.Join(dir, e.Name()), fi.Size(), fi.ModTime().UnixNano()})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].mtime < items[j].mtime })
	return items, total, nil
}

// evictDirTo prunes a flat cache directory down to at most maxBytes by
// removing least-recently-used entries first. mtime is the recency stamp:
// creation time when written, last use once a hit has touched it.
//
// In-flight write scratch (.tmp-* prefix, from the atomic write path) is
// counted toward total but is never a removal candidate: deleting another
// writer's half-written temp makes its final rename fail with ENOENT and
// kills the whole analysis (Linux unlinks open files). The scratch bytes
// still push eviction of finalized entries, so crash debris cannot wedge
// the budget — `xcut cleanup` reclaims the debris itself.
func evictDirTo(dir string, maxBytes int64) (removed int, freed int64, err error) {
	items, total, err := scanEvictable(dir)
	if err != nil || total <= maxBytes {
		return 0, 0, err
	}
	for _, it := range items {
		if total <= maxBytes {
			break
		}
		if rmErr := os.Remove(it.path); rmErr == nil {
			removed++
			freed += it.size
			total -= it.size
		}
	}
	return removed, freed, nil
}

// planEvictDirTo reports what evictDirTo would remove at this budget without
// touching anything (dry-run reporting). Same walk, same order, no deletes.
func planEvictDirTo(dir string, maxBytes int64) (count int, bytes int64, err error) {
	items, total, err := scanEvictable(dir)
	if err != nil || total <= maxBytes {
		return 0, 0, err
	}
	for _, it := range items {
		if total <= maxBytes {
			break
		}
		count++
		bytes += it.size
		total -= it.size
	}
	return count, bytes, nil
}
