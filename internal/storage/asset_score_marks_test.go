package storage

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/xcerr"
)

func marksAsset(t *testing.T, name string) (*DB, string, string) {
	t.Helper()
	db := testDB(t)
	ctx := context.Background()
	p, err := db.CreateProject(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	a := &Asset{ProjectID: p.ID, Path: "match.mp4", Filename: "match.mp4", Fingerprint: "fp"}
	if err := db.UpsertAsset(ctx, a); err != nil {
		t.Fatal(err)
	}
	return db, p.ID, a.ID
}

// TestAssetScoreMarksRoundTrip: the marks and the crop they came from survive
// the storage round trip, a re-import keeps them, and nil clears them.
func TestAssetScoreMarksRoundTrip(t *testing.T) {
	db, projectID, assetID := marksAsset(t, "marks-roundtrip")
	ctx := context.Background()

	marks := &ScoreMarks{Crop: []float64{0.72, 0.03, 0.27, 0.09}, Times: []float64{18.75, 44.5, 61}, At: 1700}
	if err := db.SetAssetScoreMarks(ctx, assetID, marks); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetAsset(ctx, assetID)
	if err != nil || got == nil {
		t.Fatalf("GetAsset: %+v, %v", got, err)
	}
	if got.ScoreMarks == nil || len(got.ScoreMarks.Times) != 3 || got.ScoreMarks.Times[2] != 61 {
		t.Fatalf("marks round trip = %+v", got.ScoreMarks)
	}
	if got.ScoreMarks.Crop[0] != 0.72 || got.ScoreMarks.At != 1700 {
		t.Fatalf("crop/at round trip = %+v", got.ScoreMarks)
	}
	listed, err := db.ListAssets(ctx, projectID)
	if err != nil || len(listed) != 1 || listed[0].ScoreMarks == nil {
		t.Fatalf("ListAssets marks: %+v, %v", listed, err)
	}

	// Re-import: probe data refreshes, the user's scan stays.
	if err := db.UpsertAsset(ctx, &Asset{ProjectID: projectID, Path: "match.mp4",
		Filename: "match.mp4", Fingerprint: "fp2", DurationSec: 600}); err != nil {
		t.Fatal(err)
	}
	got, _ = db.GetAsset(ctx, assetID)
	if got.ScoreMarks == nil || len(got.ScoreMarks.Times) != 3 {
		t.Fatalf("reimport dropped the marks: %+v", got.ScoreMarks)
	}

	// An empty scan is a result, not an absence: "scanned here, the score never
	// changed" must read differently from never scanned.
	if err := db.SetAssetScoreMarks(ctx, assetID, &ScoreMarks{Crop: marks.Crop}); err != nil {
		t.Fatalf("an empty scan is storable: %v", err)
	}
	got, _ = db.GetAsset(ctx, assetID)
	if got.ScoreMarks == nil || len(got.ScoreMarks.Times) != 0 {
		t.Fatalf("empty scan round trip = %+v", got.ScoreMarks)
	}

	if err := db.SetAssetScoreMarks(ctx, assetID, nil); err != nil {
		t.Fatal(err)
	}
	got, _ = db.GetAsset(ctx, assetID)
	if got.ScoreMarks != nil {
		t.Fatalf("cleared marks = %+v, want nil", got.ScoreMarks)
	}
	// …and the column really holds '' rather than a bare null/JSON, so nothing
	// downstream has to guess.
	var raw string
	if err := db.QueryRowContext(ctx, `SELECT score_marks FROM assets WHERE id = ?`, assetID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != "" {
		t.Fatalf("cleared column = %q, want empty string", raw)
	}
}

// TestAssetScoreMarksRejected: everything the timeline would otherwise have to
// defend against is refused at the boundary that can name it.
func TestAssetScoreMarksRejected(t *testing.T) {
	db, _, assetID := marksAsset(t, "marks-guard")
	ctx := context.Background()

	okCrop := []float64{0.1, 0.1, 0.5, 0.2}
	for _, tc := range []struct {
		name  string
		marks *ScoreMarks
	}{
		{"crop past the frame", &ScoreMarks{Crop: []float64{0.8, 0.8, 0.5, 0.5}}},
		{"crop with three numbers", &ScoreMarks{Crop: []float64{0.1, 0.1, 0.5}}},
		{"unsorted times", &ScoreMarks{Crop: okCrop, Times: []float64{30, 10}}},
		{"duplicate times", &ScoreMarks{Crop: okCrop, Times: []float64{10, 10}}},
		{"negative time", &ScoreMarks{Crop: okCrop, Times: []float64{-1}}},
		{"nan time", &ScoreMarks{Crop: okCrop, Times: []float64{math.NaN()}}},
		{"inf time", &ScoreMarks{Crop: okCrop, Times: []float64{math.Inf(1)}}},
		{"nan crop", &ScoreMarks{Crop: []float64{math.NaN(), 0, 0.5, 0.5}}},
		{"over budget", &ScoreMarks{Crop: okCrop, Times: make([]float64, MaxScoreMarks+1)}},
	} {
		err := db.SetAssetScoreMarks(ctx, assetID, tc.marks)
		if !xcerr.IsCode(err, xcerr.CodeValidation) {
			t.Errorf("%s: err=%v, want validation", tc.name, err)
		}
		if err != nil && !strings.Contains(xcerr.UserMessage(err), "crop") &&
			!strings.Contains(xcerr.UserMessage(err), "times") {
			t.Errorf("%s: message %q names neither the crop nor the times", tc.name, xcerr.UserMessage(err))
		}
	}
	// Nothing was stored by the rejected writes.
	got, _ := db.GetAsset(ctx, assetID)
	if got.ScoreMarks != nil {
		t.Fatalf("rejected writes left %+v", got.ScoreMarks)
	}
	if err := db.SetAssetScoreMarks(ctx, "asst_missing", nil); !xcerr.IsCode(err, xcerr.CodeNotFound) {
		t.Fatalf("unknown asset clear: err=%v, want not_found", err)
	}
	if err := db.SetAssetScoreMarks(ctx, assetID, nil); err != nil {
		t.Fatalf("idempotent clear: %v", err)
	}
}

// TestAssetScoreMarksWireShape: the field is absent, not null, for assets that
// were never scanned — the API serves this struct to a UI that polls it.
func TestAssetScoreMarksWireShape(t *testing.T) {
	db, projectID, assetID := marksAsset(t, "marks-wire")
	ctx := context.Background()

	b, err := json.Marshal(mustAsset(t, db, ctx, assetID))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "score_marks") {
		t.Fatalf("unscanned asset carries the field: %s", b)
	}
	if err := db.SetAssetScoreMarks(ctx, assetID, &ScoreMarks{Crop: []float64{0, 0, 0.2, 0.2}, Times: []float64{4}}); err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(mustAsset(t, db, ctx, assetID))
	if !strings.Contains(string(b), `"score_marks"`) || !strings.Contains(string(b), `"crop"`) {
		t.Fatalf("scanned asset lost the field: %s", b)
	}
	listed, err := db.ListAssets(ctx, projectID)
	if err != nil || len(listed) != 1 {
		t.Fatal(err)
	}
}

func mustAsset(t *testing.T, db *DB, ctx context.Context, id string) *Asset {
	t.Helper()
	a, err := db.GetAsset(ctx, id)
	if err != nil || a == nil {
		t.Fatalf("GetAsset(%s): %v", id, err)
	}
	return a
}
