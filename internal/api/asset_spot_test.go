package api

import (
	"context"
	"testing"

	"github.com/xiabee/XCut/internal/storage"
)

// TestAssetSpotRoundTrip: GET null → PUT seeds the rect → GET reads it back
// → DELETE clears → a second DELETE is 404. The wire shape is the asset
// row's own PlayerSpot JSON, so a client cannot meet two spellings of one
// thing.
func TestAssetSpotRoundTrip(t *testing.T) {
	s := testServer(t)
	pid, aid := seedAsset(t, s, "spot-api", "cam1.mp4")

	base := "/api/v1/projects/" + pid + "/assets/" + aid + "/player-spot"
	rec, out := do(t, s, "GET", base, "")
	if rec.Code != 200 || out["spot"] != nil {
		t.Fatalf("initial get: %d %v", rec.Code, out)
	}

	rec, out = do(t, s, "PUT", base, `{"rect":[0.3,0.2,0.16,0.3],"at":12.5}`)
	if rec.Code != 200 {
		t.Fatalf("put: %d %v", rec.Code, out)
	}
	spot, ok := out["spot"].(map[string]any)
	if !ok || spot["at"] != 12.5 {
		t.Fatalf("put response spot: %v", out["spot"])
	}
	// A seeded spot has no measured signature yet — that is the analyze
	// pass's half, and the response must not pretend otherwise.
	if bins := spot["bins"]; bins != nil {
		t.Fatalf("a freshly seeded spot answered with bins %v", bins)
	}

	rec, out = do(t, s, "GET", base, "")
	if rec.Code != 200 {
		t.Fatalf("get after put: %d %v", rec.Code, out)
	}
	if spot, _ = out["spot"].(map[string]any); spot == nil || spot["at"] != 12.5 {
		t.Fatalf("stored spot lost: %v", out["spot"])
	}

	rec, out = do(t, s, "DELETE", base, "")
	if rec.Code != 200 || out["spot"] != nil {
		t.Fatalf("delete: %d %v", rec.Code, out)
	}
	rec, _ = do(t, s, "DELETE", base, "")
	if rec.Code != 404 {
		t.Fatalf("second delete must be 404, got %d", rec.Code)
	}
}

// TestAssetSpotValidationAndScoping: the storage layer's own validity rule
// answers the malformed shapes (so the API cannot drift from what the CLI
// and the analyze pass accept), unknown assets 404, and a cross-project
// asset id stays unreachable.
func TestAssetSpotValidationAndScoping(t *testing.T) {
	s := testServer(t)
	pid, aid := seedAsset(t, s, "spot-scope", "cam2.mp4")
	_, otherAid := seedAsset(t, s, "spot-scope-other", "cam3.mp4")

	base := "/api/v1/projects/" + pid + "/assets/" + aid + "/player-spot"
	rec, out := do(t, s, "PUT", base, `{"rect":[0.9,0.9,0.5,0.5],"at":0}`)
	if rec.Code != 400 {
		t.Fatalf("out-of-frame rect must be 400, got %d (%v)", rec.Code, out)
	}
	rec, _ = do(t, s, "PUT", base, `{"rect":"corner","at":0}`)
	if rec.Code != 400 {
		t.Fatalf("garbage rect must be 400, got %d", rec.Code)
	}

	rec, _ = do(t, s, "GET", "/api/v1/projects/"+pid+"/assets/asst_missing/player-spot", "")
	if rec.Code != 404 {
		t.Fatalf("unknown asset must be 404, got %d", rec.Code)
	}
	rec, _ = do(t, s, "GET", "/api/v1/projects/"+pid+"/assets/"+otherAid+"/player-spot", "")
	if rec.Code != 404 {
		t.Fatalf("cross-project asset id must be 404, got %d", rec.Code)
	}
}

// TestAssetSpotRedrawDropsTheSignature: seeding a rect writes a bins-less
// spot — the CLI's --set does the same — so the next analyze re-measures
// against the rect the user actually drew instead of replaying a signature
// measured from the old one.
func TestAssetSpotRedrawDropsTheSignature(t *testing.T) {
	s := testServer(t)
	pid, aid := seedAsset(t, s, "spot-redraw", "cam4.mp4")
	if err := s.DB.SetAssetPlayerSpot(context.Background(), aid, &storage.PlayerSpot{
		Rect:      []float64{0.1, 0.1, 0.2, 0.2},
		At:        1,
		Bins:      []float64{1},
		SampledAt: 1770000000,
	}); err != nil {
		t.Fatal(err)
	}

	base := "/api/v1/projects/" + pid + "/assets/" + aid + "/player-spot"
	rec, out := do(t, s, "PUT", base, `{"rect":[0.3,0.3,0.2,0.2],"at":5}`)
	if rec.Code != 200 {
		t.Fatalf("redraw put: %d %v", rec.Code, out)
	}
	got, err := s.DB.GetAsset(context.Background(), aid)
	if err != nil || got == nil || got.PlayerSpot == nil {
		t.Fatalf("asset after redraw: %+v (%v)", got, err)
	}
	if len(got.PlayerSpot.Bins) != 0 || got.PlayerSpot.SampledAt != 0 {
		t.Fatalf("a redraw kept the old signature (bins %d, sampled %d) — the next analyze would replay a model measured from the old rect",
			len(got.PlayerSpot.Bins), got.PlayerSpot.SampledAt)
	}
	if got.PlayerSpot.Rect[0] != 0.3 || got.PlayerSpot.At != 5 {
		t.Fatalf("redraw rect/at = %v (at %.2f), want the new values", got.PlayerSpot.Rect, got.PlayerSpot.At)
	}
}
