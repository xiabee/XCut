package storage

import (
	"context"
	"testing"

	"github.com/xiabee/XCut/internal/xcerr"
)

// TestAssetROIRoundTrip: SetAssetROI stores the per-source court region on
// the asset row, GetAsset/ListAssets read it back, a re-import (UpsertAsset
// with the same project+path) must NOT clear user-authored data, and
// SetAssetROI(nil) clears it.
func TestAssetROIRoundTrip(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	p, err := db.CreateProject(ctx, "roi-roundtrip")
	if err != nil {
		t.Fatal(err)
	}
	a := &Asset{ProjectID: p.ID, Path: "cam1.mp4", Filename: "cam1.mp4", Fingerprint: "fp1"}
	if err := db.UpsertAsset(ctx, a); err != nil {
		t.Fatal(err)
	}

	roi := &MotionROI{X: 0.1, Y: 0.15, W: 0.6, H: 0.7}
	if err := db.SetAssetROI(ctx, a.ID, roi); err != nil {
		t.Fatal(err)
	}

	got, err := db.GetAsset(ctx, a.ID)
	if err != nil || got == nil {
		t.Fatalf("GetAsset: %+v, %v", got, err)
	}
	if got.MotionROI == nil || *got.MotionROI != *roi {
		t.Fatalf("roi round trip = %+v, want %+v", got.MotionROI, roi)
	}
	listed, err := db.ListAssets(ctx, p.ID)
	if err != nil || len(listed) != 1 || listed[0].MotionROI == nil {
		t.Fatalf("ListAssets roi: %+v, %v", listed, err)
	}

	// Re-import the same file: probe data refreshes, the user's ROI stays.
	if err := db.UpsertAsset(ctx, &Asset{
		ProjectID: p.ID, Path: "cam1.mp4", Filename: "cam1.mp4",
		Fingerprint: "fp1-new", DurationSec: 12.5,
	}); err != nil {
		t.Fatal(err)
	}
	got, err = db.GetAsset(ctx, a.ID)
	if err != nil || got == nil {
		t.Fatalf("GetAsset after reimport: %+v, %v", got, err)
	}
	if got.MotionROI == nil || *got.MotionROI != *roi {
		t.Fatalf("reimport must keep the roi, got %+v", got.MotionROI)
	}
	if got.Fingerprint != "fp1-new" {
		t.Fatalf("reimport must refresh the fingerprint, got %q", got.Fingerprint)
	}

	// Clear.
	if err := db.SetAssetROI(ctx, a.ID, nil); err != nil {
		t.Fatal(err)
	}
	got, err = db.GetAsset(ctx, a.ID)
	if err != nil || got == nil || got.MotionROI != nil {
		t.Fatalf("cleared roi: %+v, %v", got, err)
	}
}

// TestAssetROIInvalidAndMissing: the storage layer validates the rect and
// reports NotFound for unknown asset ids.
func TestAssetROIInvalidAndMissing(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	p, _ := db.CreateProject(ctx, "roi-guard")
	a := &Asset{ProjectID: p.ID, Path: "c.mp4", Filename: "c.mp4", Fingerprint: "fp"}
	db.UpsertAsset(ctx, a)

	if err := db.SetAssetROI(ctx, a.ID, &MotionROI{X: 0.5, Y: 0.5, W: 0.8, H: 0.8}); !xcerr.IsCode(err, xcerr.CodeValidation) {
		t.Fatalf("out-of-frame roi: err=%v, want validation", err)
	}
	if err := db.SetAssetROI(ctx, "asst_missing", nil); !xcerr.IsCode(err, xcerr.CodeNotFound) {
		t.Fatalf("unknown asset clear: err=%v, want not_found", err)
	}
	// A clear on an asset without a roi is a no-op success (idempotent),
	// unlike the preset endpoint's 404 — deleting nothing stored per-row is
	// still "the row now has no roi".
	if err := db.SetAssetROI(ctx, a.ID, nil); err != nil {
		t.Fatalf("idempotent clear failed: %v", err)
	}
}
