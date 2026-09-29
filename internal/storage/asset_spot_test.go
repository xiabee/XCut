package storage

import (
	"context"
	"math"
	"testing"

	"github.com/xiabee/XCut/internal/xcerr"
)

// TestValidSpotRect pins the validator the spot parsers share: exactly four
// components, no NaN anywhere, a positive extent, and the rect inside the
// frame (with the epsilon the parsers' float arithmetic needs).
func TestValidSpotRect(t *testing.T) {
	good := [][]float64{
		{0, 0, 1, 1},
		{0.1, 0.2, 0.3, 0.4},
		{0.5, 0.5, 0.5, 0.5},
		{0.4297, 0.7778, 0.1406, 0.0972},
	}
	for _, r := range good {
		if !ValidSpotRect(r) {
			t.Fatalf("ValidSpotRect(%v) = false, want true", r)
		}
	}
	bad := [][]float64{
		{0.1, 0.2, 0.3},           // three components
		{0.1, 0.2, 0.3, 0.4, 0.5}, // five
		{0.9, 0.9, 0.5, 0.5},      // overflows right and bottom
		{-0.1, 0.2, 0.3, 0.4},     // negative origin
		{0.1, 0.2, 0, 0.4},        // zero width
		{0.1, 0.2, 0.3, -1},       // negative height
	}
	for _, r := range bad {
		if ValidSpotRect(r) {
			t.Fatalf("ValidSpotRect(%v) = true, want false", r)
		}
	}
	nan := []float64{0.1, 0.2, math.NaN(), 0.4}
	if !ValidCropRectIsNaN(nan) || ValidSpotRect(nan) {
		t.Fatal("a NaN component passed the validators")
	}
}

// TestAssetPlayerSpotRoundTrip: SetAssetPlayerSpot stores the person filter's
// model on the asset row, GetAsset/ListAssets read it back whole (rect, drawn
// moment, histogram, sampled-at), an invalid rect is refused before the row
// is touched, an unknown asset is NotFound, and SetAssetPlayerSpot(nil)
// clears it.
func TestAssetPlayerSpotRoundTrip(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	p, err := db.CreateProject(ctx, "spot-roundtrip")
	if err != nil {
		t.Fatal(err)
	}
	a := &Asset{ProjectID: p.ID, Path: "cam1.mp4", Filename: "cam1.mp4", Fingerprint: "fp1"}
	if err := db.UpsertAsset(ctx, a); err != nil {
		t.Fatal(err)
	}

	spot := &PlayerSpot{
		Rect:      []float64{0.42, 0.2, 0.16, 0.3},
		At:        12.5,
		Bins:      []float64{0.5, 0.25, 0.25},
		SampledAt: 1770000000,
	}
	if err := db.SetAssetPlayerSpot(ctx, a.ID, spot); err != nil {
		t.Fatal(err)
	}

	got, err := db.GetAsset(ctx, a.ID)
	if err != nil || got == nil {
		t.Fatalf("GetAsset: %+v, %v", got, err)
	}
	if got.PlayerSpot == nil {
		t.Fatal("the spot did not survive the row")
	}
	if got.PlayerSpot.At != 12.5 || got.PlayerSpot.SampledAt != 1770000000 {
		t.Fatalf("spot metadata = at %.2f sampled %d, want 12.5 / 1770000000",
			got.PlayerSpot.At, got.PlayerSpot.SampledAt)
	}
	if len(got.PlayerSpot.Bins) != 3 || got.PlayerSpot.Bins[0] != 0.5 {
		t.Fatalf("spot bins = %v, want the histogram round-tripped", got.PlayerSpot.Bins)
	}
	listed, err := db.ListAssets(ctx, p.ID)
	if err != nil || len(listed) != 1 || listed[0].PlayerSpot == nil {
		t.Fatalf("ListAssets spot: %+v, %v", listed, err)
	}

	if err := db.SetAssetPlayerSpot(ctx, a.ID, &PlayerSpot{
		Rect: []float64{0.9, 0.9, 0.5, 0.5},
	}); err == nil {
		t.Fatal("an overflowing rect was stored")
	}
	got, _ = db.GetAsset(ctx, a.ID)
	if got.PlayerSpot == nil || got.PlayerSpot.At != 12.5 {
		t.Fatal("the refused write clobbered the stored spot")
	}

	if err := db.SetAssetPlayerSpot(ctx, "no-such-asset", spot); !xcerr.IsCode(err, xcerr.CodeNotFound) {
		t.Fatalf("unknown asset = %v, want not_found", err)
	}

	if err := db.SetAssetPlayerSpot(ctx, a.ID, nil); err != nil {
		t.Fatal(err)
	}
	got, err = db.GetAsset(ctx, a.ID)
	if err != nil || got == nil || got.PlayerSpot != nil {
		t.Fatalf("clear left %+v behind (err %v)", got, err)
	}
}
