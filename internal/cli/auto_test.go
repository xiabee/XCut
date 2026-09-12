package cli

import (
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
	assets := []storage.Asset{
		assetRow("a1", `D:\vid\A.mp4`),
		assetRow("a2", `D:\vid\B.mp4`),
	}

	ids, err := matchAssetIDs(assets, []string{`D:\vid\A.mp4`, `D:\vid\B.mp4`})
	if err != nil || len(ids) != 2 {
		t.Fatalf("full match: ids=%v err=%v", ids, err)
	}

	// A path that was never imported (or drifted spelling) must refuse.
	if _, err := matchAssetIDs(assets, []string{`D:\vid\A.mp4`, `D:\vid\C.mp4`}); err == nil {
		t.Fatal("partial match must fail")
	} else if !strings.Contains(err.Error(), "not every input resolved") {
		t.Fatalf("error should name the resolution problem: %v", err)
	}
	if _, err := matchAssetIDs(assets, []string{`D:\vid\missing.mp4`}); err == nil {
		t.Fatal("empty match must fail")
	}
	// Duplicate inputs collapse to one want entry.
	ids, err = matchAssetIDs(assets, []string{`D:\vid\A.mp4`, `D:\vid\A.mp4`})
	if err != nil || len(ids) != 1 {
		t.Fatalf("duplicate inputs: ids=%v err=%v", ids, err)
	}
}
