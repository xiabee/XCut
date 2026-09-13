package api

import (
	"context"
	"testing"

	"github.com/xiabee/XCut/internal/storage"
)

// seedAsset inserts a project + asset row directly and returns both ids.
func seedAsset(t *testing.T, s *Server, project, path string) (pid, aid string) {
	t.Helper()
	p, err := s.DB.CreateProject(context.Background(), project)
	if err != nil {
		t.Fatal(err)
	}
	a := &storage.Asset{ProjectID: p.ID, Path: path, Filename: path, Fingerprint: "fp-" + path}
	if err := s.DB.UpsertAsset(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	return p.ID, a.ID
}

// TestAssetROIRoundTrip: GET null → PUT → GET reads the rect back →
// DELETE clears → a second DELETE is 404 (nothing stored to clear, same
// semantic as the style-roi endpoint).
func TestAssetROIRoundTrip(t *testing.T) {
	s := testServer(t)
	pid, aid := seedAsset(t, s, "roi-api", "cam1.mp4")

	base := "/api/v1/projects/" + pid + "/assets/" + aid + "/roi"
	rec, out := do(t, s, "GET", base, "")
	if rec.Code != 200 || out["roi"] != nil {
		t.Fatalf("initial get: %d %v", rec.Code, out)
	}

	rec, out = do(t, s, "PUT", base, `{"x":0.05,"y":0.1,"w":0.55,"h":0.65}`)
	if rec.Code != 200 {
		t.Fatalf("put: %d %v", rec.Code, out)
	}
	roi, ok := out["roi"].(map[string]any)
	if !ok || roi["x"] != 0.05 || roi["h"] != 0.65 {
		t.Fatalf("put response roi: %v", out["roi"])
	}

	rec, out = do(t, s, "GET", base, "")
	if rec.Code != 200 {
		t.Fatalf("get after put: %d %v", rec.Code, out)
	}
	if roi, _ = out["roi"].(map[string]any); roi == nil || roi["w"] != 0.55 {
		t.Fatalf("stored roi lost: %v", out["roi"])
	}

	rec, out = do(t, s, "DELETE", base, "")
	if rec.Code != 200 || out["roi"] != nil {
		t.Fatalf("delete: %d %v", rec.Code, out)
	}
	rec, _ = do(t, s, "DELETE", base, "")
	if rec.Code != 404 {
		t.Fatalf("second delete must be 404, got %d", rec.Code)
	}
}

// TestAssetROIValidationAndScoping: malformed rects are rejected, unknown
// assets 404, and an asset id from ANOTHER project must not be reachable
// through this project's path (the scoping guard).
func TestAssetROIValidationAndScoping(t *testing.T) {
	s := testServer(t)
	pid, aid := seedAsset(t, s, "roi-scope", "cam2.mp4")
	_, otherAid := seedAsset(t, s, "roi-scope-other", "cam3.mp4")

	base := "/api/v1/projects/" + pid + "/assets/" + aid + "/roi"
	rec, out := do(t, s, "PUT", base, `{"x":0.9,"y":0,"w":0.5,"h":0.5}`)
	if rec.Code != 400 {
		t.Fatalf("out-of-frame rect must be 400, got %d (%v)", rec.Code, out)
	}
	rec, _ = do(t, s, "PUT", base, `{"x":"NaN-ish","y":0,"w":0.1,"h":0.1}`)
	if rec.Code != 400 {
		t.Fatalf("garbage body must be 400, got %d", rec.Code)
	}

	rec, _ = do(t, s, "GET", "/api/v1/projects/"+pid+"/assets/asst_missing/roi", "")
	if rec.Code != 404 {
		t.Fatalf("unknown asset must be 404, got %d", rec.Code)
	}
	rec, _ = do(t, s, "GET", "/api/v1/projects/"+pid+"/assets/"+otherAid+"/roi", "")
	if rec.Code != 404 {
		t.Fatalf("cross-project asset id must be 404, got %d", rec.Code)
	}

	// badminton preset's roi endpoint still independent: PUT on the asset
	// must not touch the style override file (separate stores).
	rec, out = do(t, s, "PUT", base, `{"x":0.1,"y":0.1,"w":0.4,"h":0.4}`)
	if rec.Code != 200 {
		t.Fatalf("valid put failed: %d %v", rec.Code, out)
	}
	rec, out = do(t, s, "GET", "/api/v1/styles/badminton_highlight/roi", "")
	if rec.Code != 200 || out["roi"] != nil {
		t.Fatalf("asset roi leaked into the preset: %d %v", rec.Code, out)
	}
}
