package api

import (
	"context"
	"testing"

	"github.com/xiabee/XCut/internal/storage"
)

// TestAssetScoreRoundTrip: GET empty → PUT a region → GET reads it back with no
// marks yet (the scan is the analyze job's, not this handler's) → marks that
// arrive later are reported → moving the region drops them → DELETE clears, and
// a second DELETE is 404.
func TestAssetScoreRoundTrip(t *testing.T) {
	s := testServer(t)
	pid, aid := seedAsset(t, s, "score-api", "match.mp4")
	base := "/api/v1/projects/" + pid + "/assets/" + aid + "/score"
	ctx := context.Background()

	rec, out := do(t, s, "GET", base, "")
	if rec.Code != 200 || out["crop"] != nil || out["marks"].(float64) != 0 {
		t.Fatalf("initial get: %d %v", rec.Code, out)
	}

	rec, out = do(t, s, "PUT", base, `{"x":0.42,"y":0.77,"w":0.14,"h":0.1}`)
	if rec.Code != 200 {
		t.Fatalf("put: %d %v", rec.Code, out)
	}
	crop, _ := out["crop"].([]any)
	if len(crop) != 4 || crop[0] != 0.42 || crop[3] != 0.1 {
		t.Fatalf("put response crop: %v", out["crop"])
	}
	if out["marks"].(float64) != 0 {
		t.Fatalf("a fresh region must report no marks yet: %v", out)
	}

	// The analyze job's write, simulated at the store: boundaries measured
	// against the current region are reported, not inferred from silence.
	if err := s.DB.SetAssetScoreMarks(ctx, aid, &storage.ScoreMarks{
		Crop: []float64{0.42, 0.77, 0.14, 0.1}, Times: []float64{17.2, 28.2, 42.2}, At: 1700,
	}); err != nil {
		t.Fatal(err)
	}
	rec, out = do(t, s, "GET", base, "")
	if rec.Code != 200 || out["marks"].(float64) != 3 || out["stale"] != false {
		t.Fatalf("get with marks: %d %v", rec.Code, out)
	}

	// Moving the region invalidates the measurement in the same write, so the
	// reel cannot keep ending clips at points from the old part of the frame.
	rec, out = do(t, s, "PUT", base, `{"x":0.1,"y":0.1,"w":0.14,"h":0.1}`)
	if rec.Code != 200 {
		t.Fatalf("re-put: %d %v", rec.Code, out)
	}
	if out["marks"].(float64) != 0 {
		t.Fatalf("moved region must drop the old marks: %v", out)
	}

	rec, out = do(t, s, "DELETE", base, "")
	if rec.Code != 200 || out["crop"] != nil {
		t.Fatalf("delete: %d %v", rec.Code, out)
	}
	rec, _ = do(t, s, "DELETE", base, "")
	if rec.Code != 404 {
		t.Fatalf("second delete must be 404, got %d", rec.Code)
	}
}

// TestAssetScoreReportsStale: marks measured against a different region than the
// one now stored are reported as stale, which is what tells the UI to say
// "re-analyze" instead of looking satisfied.
func TestAssetScoreReportsStale(t *testing.T) {
	s := testServer(t)
	pid, aid := seedAsset(t, s, "score-stale", "match.mp4")
	ctx := context.Background()
	if err := s.DB.SetAssetScoreCrop(ctx, aid, []float64{0.1, 0.1, 0.2, 0.2}); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.SetAssetScoreMarks(ctx, aid, &storage.ScoreMarks{
		Crop: []float64{0.6, 0.6, 0.2, 0.2}, Times: []float64{4}, At: 1,
	}); err != nil {
		t.Fatal(err)
	}

	rec, out := do(t, s, "GET", "/api/v1/projects/"+pid+"/assets/"+aid+"/score", "")
	if rec.Code != 200 {
		t.Fatalf("get: %d %v", rec.Code, out)
	}
	if out["stale"] != true || out["marks"].(float64) != 1 {
		t.Fatalf("stale region/marks mismatch: %v", out)
	}
}

// TestAssetScoreValidationAndScoping: a rectangle outside the frame is 400
// before anything is stored, garbage bodies are 400, and an asset belonging to
// another project is not reachable through this project's path.
func TestAssetScoreValidationAndScoping(t *testing.T) {
	s := testServer(t)
	pid, aid := seedAsset(t, s, "score-scope", "match.mp4")
	_, otherAid := seedAsset(t, s, "score-scope-other", "other.mp4")
	base := "/api/v1/projects/" + pid + "/assets/" + aid + "/score"

	rec, out := do(t, s, "PUT", base, `{"x":0.9,"y":0,"w":0.5,"h":0.5}`)
	if rec.Code != 400 {
		t.Fatalf("out-of-frame region must be 400, got %d (%v)", rec.Code, out)
	}
	rec, _ = do(t, s, "PUT", base, `{"x":"nope"}`)
	if rec.Code != 400 {
		t.Fatalf("garbage body must be 400, got %d", rec.Code)
	}
	rec, _ = do(t, s, "GET", "/api/v1/projects/"+pid+"/assets/asst_missing/score", "")
	if rec.Code != 404 {
		t.Fatalf("unknown asset must be 404, got %d", rec.Code)
	}
	rec, _ = do(t, s, "PUT", "/api/v1/projects/"+pid+"/assets/"+otherAid+"/score", `{"x":0.1,"y":0.1,"w":0.2,"h":0.2}`)
	if rec.Code != 404 {
		t.Fatalf("cross-project asset id must be 404, got %d", rec.Code)
	}
	// And the rejected writes left nothing behind.
	_, got := do(t, s, "GET", base, "")
	if got["crop"] != nil {
		t.Fatalf("rejected writes stored a region: %v", got)
	}
}
