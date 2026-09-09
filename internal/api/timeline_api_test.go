package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/pipeline"
	"github.com/xiabee/XCut/internal/storage"
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

// TestTimelineRestoreBackup: PUT (manual save) does NOT create a backup;
// a regeneration (BuildTimeline) does; the restore endpoint swaps them and
// reports 404 when no backup exists.
func TestTimelineRestoreBackup(t *testing.T) {
	s := testServer(t)
	if _, err := s.DB.CreateProject(t.Context(), "rb"); err != nil {
		t.Fatal(err)
	}
	p, _ := s.DB.GetProjectByName(t.Context(), "rb")

	// No timeline yet → restore must 404.
	if rec, _ := do(t, s, "POST", "/api/v1/projects/"+p.ID+"/timeline/restore-backup", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("restore without backup: %d, want 404", rec.Code)
	}

	// Seed a timeline (as the style builder would write it).
	tl := &timeline.Timeline{
		Version: timeline.Version,
		Canvas:  timeline.Canvas{Width: 640, Height: 360, FPS: 30},
		Tracks: []timeline.Track{{ID: "v1", Kind: "video", Clips: []timeline.Clip{{
			ID: "c1", AssetID: "a", SourceStart: 0, SourceEnd: 1, Speed: 1, Volume: 1,
		}}}},
	}
	b, err := jsonMarshalTL(tl)
	if err != nil {
		t.Fatal(err)
	}
	tlPath, err := s.Pipe.TimelinePath(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(tlPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tlPath, b, 0o644); err != nil {
		t.Fatal(err)
	}

	// has_backup must be false before any regeneration.
	rec, out := do(t, s, "GET", "/api/v1/projects/"+p.ID+"/timeline", "")
	if rec.Code != http.StatusOK || out["has_backup"] != false {
		t.Fatalf("has_backup before regeneration: %v (%d)", out["has_backup"], rec.Code)
	}

	// Regenerate: pipeline backs up the current document.
	b2, err := jsonMarshalTL(&timeline.Timeline{
		Version: timeline.Version,
		Canvas:  timeline.Canvas{Width: 640, Height: 360, FPS: 30},
		Tracks: []timeline.Track{{ID: "v1", Kind: "video", Clips: []timeline.Clip{{
			ID: "regen", AssetID: "a", SourceStart: 0, SourceEnd: 2, Speed: 1, Volume: 1,
		}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := pipeline.WriteAtomic(tlPath, b2); err != nil {
		t.Fatal(err)
	}
	// Simulate the backup the regeneration writes (the job itself is
	// covered by TestTimelineBackupAndRestore).
	cur, err := os.ReadFile(tlPath)
	if err != nil {
		t.Fatal(err)
	}
	bakPath, err := s.Pipe.TimelineBackupPath(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bakPath, b, 0o644); err != nil {
		t.Fatal(err)
	}
	_ = cur

	// has_backup flips true; restore swaps the documents.
	rec2, out2 := do(t, s, "GET", "/api/v1/projects/"+p.ID+"/timeline", "")
	if rec2.Code != http.StatusOK || out2["has_backup"] != true {
		t.Fatalf("has_backup after regeneration: %v (%d)", out2["has_backup"], rec2.Code)
	}
	if rec3, _ := do(t, s, "POST", "/api/v1/projects/"+p.ID+"/timeline/restore-backup", ""); rec3.Code != http.StatusOK {
		t.Fatalf("restore: %d", rec3.Code)
	}
	got, err := os.ReadFile(tlPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `"c1"`) {
		t.Fatalf("restored timeline should hold the backup document, got %s", got)
	}
}

// TestTimelinePutRejectsAbsurdSpeed: a hand-edited timeline with a tiny
// speed must be a 400 validation error, not a 500 from serializing the
// +Inf clip duration it used to produce.
func TestTimelinePutRejectsAbsurdSpeed(t *testing.T) {
	s := testServer(t)
	if _, err := s.DB.CreateProject(t.Context(), "speed-guard"); err != nil {
		t.Fatal(err)
	}
	p, _ := s.DB.GetProjectByName(t.Context(), "speed-guard")
	asset := storageAssetFor(p.ID)
	if err := s.DB.UpsertAsset(t.Context(), &asset); err != nil {
		t.Fatal(err)
	}

	for name, speed := range map[string]float64{"tiny": 0.001, "subnormal": 1e-320} {
		tl := &timeline.Timeline{
			Version: timeline.Version,
			Canvas:  timeline.Canvas{Width: 640, Height: 360, FPS: 30},
			Tracks: []timeline.Track{{
				ID:   "v1",
				Kind: "video",
				Clips: []timeline.Clip{{
					ID: "c1", AssetID: asset.ID, SourceStart: 0, SourceEnd: 10,
					TimelineStart: 0, Speed: speed, Volume: 1,
				}},
			}},
		}
		rec, out := do(t, s, "PUT", "/api/v1/projects/"+p.ID+"/timeline", marshalTimeline(t, tl))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s speed: PUT returned %d (want 400): %v", name, rec.Code, out)
		}
		if !strings.Contains(rec.Body.String(), "validation") {
			t.Fatalf("%s speed: error must be validation-coded: %s", name, rec.Body.String())
		}
	}
}

// TestProjectDeleteWithActiveJobs: deleting a project that still has
// queued/running work must 409 (the cascade would silently kill the job),
// and must succeed once the work is terminal.
func TestProjectDeleteWithActiveJobs(t *testing.T) {
	s := testServer(t)
	if _, err := s.DB.CreateProject(t.Context(), "del-guard"); err != nil {
		t.Fatal(err)
	}
	p, _ := s.DB.GetProjectByName(t.Context(), "del-guard")

	if _, err := s.DB.CreateJob(t.Context(), "render", p.ID, "CPU_HEAVY", ""); err != nil {
		t.Fatal(err)
	}
	if rec, out := do(t, s, "DELETE", "/api/v1/projects/"+p.ID, ""); rec.Code != http.StatusConflict {
		t.Fatalf("delete with active job: %d (want 409): %v", rec.Code, out)
	}

	// Terminal state frees the project for deletion.
	js, _ := s.DB.ListJobs(t.Context(), p.ID)
	for i := range js {
		if err := s.DB.FinishJob(t.Context(), js[i].ID, storage.StatusCancelled, "cancelled", "test"); err != nil {
			t.Fatal(err)
		}
	}
	if rec, out := do(t, s, "DELETE", "/api/v1/projects/"+p.ID, ""); rec.Code != http.StatusOK {
		t.Fatalf("delete after terminal jobs: %d (want 200): %v", rec.Code, out)
	}
}
