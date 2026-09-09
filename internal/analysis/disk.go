package analysis

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

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

// evictDirTo prunes a flat cache directory down to at most maxBytes by
// removing oldest-modified entries first (deterministic LRU approximation:
// cache entries are immutable once written, so mtime = creation time).
//
// In-flight write scratch (.tmp-* prefix, from the atomic write path) is
// counted toward total but is never a removal candidate: deleting another
// writer's half-written temp makes its final rename fail with ENOENT and
// kills the whole analysis (Linux unlinks open files). The scratch bytes
// still push eviction of finalized entries, so crash debris cannot wedge
// the budget — `xcut cleanup` reclaims the debris itself.
func evictDirTo(dir string, maxBytes int64) (removed int, freed int64, err error) {
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, xcerr.E(xcerr.CodeInternal, "cannot read cache dir", err)
	}
	type item struct {
		path  string
		size  int64
		mtime int64
	}
	items := make([]item, 0, len(dirEntries))
	var total int64
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
			continue // in-flight scratch: never a victim (see doc comment)
		}
		items = append(items, item{filepath.Join(dir, e.Name()), fi.Size(), fi.ModTime().UnixNano()})
	}
	if total <= maxBytes {
		return 0, 0, nil
	}
	sort.Slice(items, func(i, j int) bool { return items[i].mtime < items[j].mtime })
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
