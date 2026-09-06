package api

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/xiabee/XCut/internal/timeline"
)

func TestTimelineGetPut(t *testing.T) {
	s := testServer(t)
	if _, err := s.DB.CreateProject(t.Context(), "tl"); err != nil {
		t.Fatal(err)
	}
	p, _ := s.DB.GetProjectByName(t.Context(), "tl")

	// GET before generation → 404.
	if rec, _ := do(t, s, "GET", "/api/v1/projects/"+p.ID+"/timeline", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("get before generate: %d", rec.Code)
	}

	// PUT a valid single-clip timeline → 200, then GET round-trips it.
	tl := &timeline.Timeline{
		Version: timeline.Version,
		Canvas:  timeline.Canvas{Width: 640, Height: 360, FPS: 30},
		Tracks: []timeline.Track{{
			ID:   "v1",
			Kind: "video",
			Clips: []timeline.Clip{{
				ID: "c1", AssetID: "ghost", SourceStart: 0, SourceEnd: 1,
				TimelineStart: 0, Speed: 1, Volume: 1,
			}},
		}},
	}
	// Unknown asset must be rejected (validated against the project's assets).
	body := marshalTimeline(t, tl)
	if rec, out := do(t, s, "PUT", "/api/v1/projects/"+p.ID+"/timeline", body); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid timeline accepted: %d %v", rec.Code, out)
	}

	// Import a real asset row so validation passes.
	asset := storageAssetFor(p.ID)
	if err := s.DB.UpsertAsset(t.Context(), &asset); err != nil {
		t.Fatal(err)
	}
	assets, _ := s.DB.ListAssets(t.Context(), p.ID)
	tl.Tracks[0].Clips[0].AssetID = assets[0].ID
	tl.Tracks[0].Clips[0].SourceEnd = 5 // within the asset duration
	body = marshalTimeline(t, tl)
	if rec, _ := do(t, s, "PUT", "/api/v1/projects/"+p.ID+"/timeline", body); rec.Code != http.StatusOK {
		t.Fatalf("valid timeline rejected: %d", rec.Code)
	}

	rec, out := do(t, s, "GET", "/api/v1/projects/"+p.ID+"/timeline", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get: %d", rec.Code)
	}
	got := out["timeline"].(map[string]any)
	clips := got["tracks"].([]any)[0].(map[string]any)["clips"].([]any)
	if len(clips) != 1 {
		t.Fatalf("clips = %d", len(clips))
	}

	// Corrupt document → 400, stored file untouched.
	if rec, _ := do(t, s, "PUT", "/api/v1/projects/"+p.ID+"/timeline", `{"version":1,`); rec.Code != http.StatusBadRequest {
		t.Fatalf("corrupt put: %d", rec.Code)
	}
	rec, _ = do(t, s, "GET", "/api/v1/projects/"+p.ID+"/timeline", "")
	if rec.Code != http.StatusOK {
		t.Fatal("stored timeline corrupted by failed put")
	}
	_ = filepath.Join
}

func marshalTimeline(t *testing.T, tl *timeline.Timeline) string {
	t.Helper()
	b, err := jsonMarshalTL(tl)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
