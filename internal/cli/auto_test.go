package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/storage"
)

func assetRow(id, path string) storage.Asset {
	return storage.Asset{ID: id, Path: path}
}

// TestMatchAssetIDs pins the auto scoping guard: every input must resolve to
// a stored asset, or the run fails loudly — an empty/partial match used to
// fall through to BuildTimeline's unscoped default and silently compose the
// cut from the whole project again.
func TestMatchAssetIDs(t *testing.T) {
	// matchAssetIDs resolves each input through filepath.Abs, so the fixtures
	// have to be absolute *in the shape this OS understands*: a hardcoded
	// `D:\vid\A.mp4` is already absolute on Windows but merely a relative
	// filename on POSIX, where the test would silently exercise a different
	// path than the one it thinks it imported.
	dir := t.TempDir()
	a, b, c := filepath.Join(dir, "A.mp4"), filepath.Join(dir, "B.mp4"), filepath.Join(dir, "C.mp4")
	assets := []storage.Asset{
		assetRow("a1", a),
		assetRow("a2", b),
	}

	ids, err := matchAssetIDs(assets, []string{a, b})
	if err != nil || len(ids) != 2 {
		t.Fatalf("full match: ids=%v err=%v", ids, err)
	}

	// A path that was never imported (or drifted spelling) must refuse.
	if _, err := matchAssetIDs(assets, []string{a, c}); err == nil {
		t.Fatal("partial match must fail")
	} else if !strings.Contains(err.Error(), "not every input resolved") {
		t.Fatalf("error should name the resolution problem: %v", err)
	}
	if _, err := matchAssetIDs(assets, []string{filepath.Join(dir, "missing.mp4")}); err == nil {
		t.Fatal("empty match must fail")
	}
	// Duplicate inputs collapse to one want entry.
	ids, err = matchAssetIDs(assets, []string{a, a})
	if err != nil || len(ids) != 1 {
		t.Fatalf("duplicate inputs: ids=%v err=%v", ids, err)
	}
}
