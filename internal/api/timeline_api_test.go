package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	rec, out := do(t, s, "PUT", "/api/v1/projects/"+p.ID+"/timeline", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid timeline rejected: %d %v", rec.Code, out)
	}
	// The saved response carries the clip count the UI prints next to the
	// button; a zero here reads as "your edit deleted everything".
	if n, _ := out["clips"].(float64); n != 1 {
		t.Fatalf("save reported clips=%v, want 1: %v", out["clips"], out)
	}

	rec, out = do(t, s, "GET", "/api/v1/projects/"+p.ID+"/timeline", "")
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

// TestTimelineRevisionGuard: PUTs carry the revision they read; a stale
// revision (another tab saved, or the timeline was regenerated) is refused
// with 409 instead of silently destroying the other writer's document.
func TestTimelineRevisionGuard(t *testing.T) {
	s := testServer(t)
	if _, err := s.DB.CreateProject(t.Context(), "rev"); err != nil {
		t.Fatal(err)
	}
	p, _ := s.DB.GetProjectByName(t.Context(), "rev")
	asset := storageAssetFor(p.ID)
	if err := s.DB.UpsertAsset(t.Context(), &asset); err != nil {
		t.Fatal(err)
	}

	mk := func(rev int64) string {
		tl := &timeline.Timeline{
			Version:  timeline.Version,
			Revision: rev,
			Canvas:   timeline.Canvas{Width: 640, Height: 360, FPS: 30},
			Tracks: []timeline.Track{{
				ID:   "v1",
				Kind: "video",
				Clips: []timeline.Clip{{
					ID: "c1", AssetID: asset.ID, SourceStart: 0, SourceEnd: 5,
					TimelineStart: 0, Speed: 1, Volume: 1,
				}},
			}},
		}
		return marshalTimeline(t, tl)
	}

	// First save: no stored document, revision 0 accepted → doc at revision 1.
	rec, out := do(t, s, "PUT", "/api/v1/projects/"+p.ID+"/timeline", mk(0))
	if rec.Code != http.StatusOK || out["revision"].(float64) != 1 {
		t.Fatalf("first save: %d %v (want revision 1)", rec.Code, out)
	}

	// Matching revision → accepted, revision increments.
	if rec, out := do(t, s, "PUT", "/api/v1/projects/"+p.ID+"/timeline", mk(1)); rec.Code != http.StatusOK || out["revision"].(float64) != 2 {
		t.Fatalf("second save: %d %v (want revision 2)", rec.Code, out)
	}

	// Stale revision → 409, stored document untouched.
	if rec, _ := do(t, s, "PUT", "/api/v1/projects/"+p.ID+"/timeline", mk(1)); rec.Code != http.StatusConflict {
		t.Fatalf("stale save: %d (want 409)", rec.Code)
	}
	rec, out = do(t, s, "GET", "/api/v1/projects/"+p.ID+"/timeline", "")
	if rec.Code != http.StatusOK || out["timeline"].(map[string]any)["revision"].(float64) != 2 {
		t.Fatalf("stale save clobbered the document: %v", out)
	}

	// A blind save without any revision (old-style overwrite) is refused too.
	if rec, _ := do(t, s, "PUT", "/api/v1/projects/"+p.ID+"/timeline", mk(0)); rec.Code != http.StatusConflict {
		t.Fatalf("revision-less save: %d (want 409)", rec.Code)
	}
}

// TestRevisionRefusalsTellTheTwoCasesApart: two different client mistakes both land on
// 409, and the walk over the editing surface found them sharing one sentence. A tab that
// saved while you were typing really did move the document under you; a script that PUTs
// an authored-from-scratch document never read anything, and being told "it changed since
// you loaded it" sends it looking for a conflict that never happened. Each refusal names
// its own case.
func TestRevisionRefusalsTellTheTwoCasesApart(t *testing.T) {
	s := testServer(t)
	if _, err := s.DB.CreateProject(t.Context(), "rev-words"); err != nil {
		t.Fatal(err)
	}
	p, _ := s.DB.GetProjectByName(t.Context(), "rev-words")
	asset := storageAssetFor(p.ID)
	if err := s.DB.UpsertAsset(t.Context(), &asset); err != nil {
		t.Fatal(err)
	}
	mk := func(rev int64) string {
		return marshalTimeline(t, &timeline.Timeline{
			Version: timeline.Version, Revision: rev,
			Canvas: timeline.Canvas{Width: 640, Height: 360, FPS: 30},
			Tracks: []timeline.Track{{ID: "v1", Kind: "video", Clips: []timeline.Clip{{
				ID: "c1", AssetID: asset.ID, SourceStart: 0, SourceEnd: 5,
				TimelineStart: 0, Speed: 1, Volume: 1,
			}}}},
		})
	}
	// Two saves, so the stored document sits at revision 2.
	for _, rev := range []int64{0, 1} {
		if rec, out := do(t, s, "PUT", "/api/v1/projects/"+p.ID+"/timeline", mk(rev)); rec.Code != http.StatusOK {
			t.Fatalf("seed save at revision %d: %d %v", rev, rec.Code, out)
		}
	}

	// Stale: the client read revision 1 and someone else saved since.
	rec, out := do(t, s, "PUT", "/api/v1/projects/"+p.ID+"/timeline", mk(1))
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale save: %d (want 409) %v", rec.Code, out)
	}
	stale := fmt.Sprint(out["message"])
	if !strings.Contains(stale, "changed since you loaded it") {
		t.Errorf("the stale refusal does not describe a document that moved: %s", stale)
	}
	if !strings.Contains(stale, "revision 1") || !strings.Contains(stale, "revision 2") {
		t.Errorf("the stale refusal does not name both revisions: %s", stale)
	}

	// Blind: the document carries no revision at all — it was never read.
	rec, out = do(t, s, "PUT", "/api/v1/projects/"+p.ID+"/timeline", mk(0))
	if rec.Code != http.StatusConflict {
		t.Fatalf("revision-less save: %d (want 409) %v", rec.Code, out)
	}
	blind := fmt.Sprint(out["message"])
	if !strings.Contains(blind, "carries no revision") {
		t.Errorf("the blind refusal should say the document was never read: %s", blind)
	}
	if strings.Contains(blind, "changed since you loaded it") {
		t.Errorf("the blind refusal blames a change nobody could have missed: %s", blind)
	}
	if !strings.Contains(blind, "revision 2") {
		t.Errorf("the blind refusal does not say what revision is stored: %s", blind)
	}
}

// concurrently — exactly one may win, the rest must 409 (the guard's
// check-and-write is serialized; lost updates are impossible by design).
func TestTimelineRevisionGuardConcurrent(t *testing.T) {
	s := testServer(t)
	if _, err := s.DB.CreateProject(t.Context(), "rev-race"); err != nil {
		t.Fatal(err)
	}
	p, _ := s.DB.GetProjectByName(t.Context(), "rev-race")
	asset := storageAssetFor(p.ID)
	if err := s.DB.UpsertAsset(t.Context(), &asset); err != nil {
		t.Fatal(err)
	}

	mk := func(rev int64) string {
		tl := &timeline.Timeline{
			Version:  timeline.Version,
			Revision: rev,
			Canvas:   timeline.Canvas{Width: 640, Height: 360, FPS: 30},
			Tracks: []timeline.Track{{
				ID:   "v1",
				Kind: "video",
				Clips: []timeline.Clip{{
					ID: "c1", AssetID: asset.ID, SourceStart: 0, SourceEnd: 5,
					TimelineStart: 0, Speed: 1, Volume: 1,
				}},
			}},
		}
		return marshalTimeline(t, tl)
	}
	// Seed revision 1.
	if rec, _ := do(t, s, "PUT", "/api/v1/projects/"+p.ID+"/timeline", mk(0)); rec.Code != http.StatusOK {
		t.Fatalf("seed save: %d", rec.Code)
	}

	const n = 8
	var wg sync.WaitGroup
	codes := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rec, _ := do(t, s, "PUT", "/api/v1/projects/"+p.ID+"/timeline", mk(1))
			codes[i] = rec.Code
		}(i)
	}
	wg.Wait()
	wins, conflicts := 0, 0
	for _, c := range codes {
		switch c {
		case http.StatusOK:
			wins++
		case http.StatusConflict:
			conflicts++
		default:
			t.Fatalf("unexpected status %d (want 200 or 409)", c)
		}
	}
	if wins != 1 || conflicts != n-1 {
		t.Fatalf("wins=%d conflicts=%d, want exactly 1 win", wins, conflicts)
	}
}

// TestTimelineRegenAndPutRevisionUniqueness: manual PUTs and regeneration
// writes interleave on one document. Regeneration writes hold the same lock
// as PUTs (Pipe.TimelineWriteLock), so every write bumps from what is
// current at write time — the stored revision must end at exactly
// 1 + (accepted PUTs) + (regenerations), proving no write ever collided on
// a revision (a collision silently destroys a document the API reported as
// saved).
func TestTimelineRegenAndPutRevisionUniqueness(t *testing.T) {
	s := testServer(t)
	if _, err := s.DB.CreateProject(t.Context(), "rev-uniq"); err != nil {
		t.Fatal(err)
	}
	p, _ := s.DB.GetProjectByName(t.Context(), "rev-uniq")
	asset := storageAssetFor(p.ID)
	if err := s.DB.UpsertAsset(t.Context(), &asset); err != nil {
		t.Fatal(err)
	}

	put := func(rev int64) (int, string) {
		tl := &timeline.Timeline{
			Version:  timeline.Version,
			Revision: rev,
			Canvas:   timeline.Canvas{Width: 640, Height: 360, FPS: 30},
			Tracks: []timeline.Track{{
				ID:   "v1",
				Kind: "video",
				Clips: []timeline.Clip{{
					ID: "manual", AssetID: asset.ID, SourceStart: 0, SourceEnd: 5,
					TimelineStart: 0, Speed: 1, Volume: 1,
				}},
			}},
		}
		rec, _ := do(t, s, "PUT", "/api/v1/projects/"+p.ID+"/timeline", marshalTimeline(t, tl))
		// The body is the diagnosis: a bare status told us "500" on a CI node
		// and nothing about which of the read/rename/validation steps said it.
		return rec.Code, "PUT: " + strings.TrimSpace(rec.Body.String())
	}

	// Seed revision 1.
	if code, body := put(0); code != http.StatusOK {
		t.Fatalf("seed save: %d %s", code, body)
	}

	const writers, iters, regens = 4, 3, 4
	var wg sync.WaitGroup
	codes := make([]int, writers*iters)
	details := make([]string, writers*iters)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < iters; j++ {
				// Read-then-write, as the editor UI does.
				rec, out := do(t, s, "GET", "/api/v1/projects/"+p.ID+"/timeline", "")
				if rec.Code != http.StatusOK {
					// Label the verb. This branch used to store only the code,
					// so a 500 from the read was reported as "unexpected PUT
					// status" with an empty detail — and sent the search for a
					// write bug to where no bug was.
					codes[i*iters+j] = rec.Code
					details[i*iters+j] = "GET: " + strings.TrimSpace(rec.Body.String())
					continue
				}
				rev := int64(out["timeline"].(map[string]any)["revision"].(float64))
				codes[i*iters+j], details[i*iters+j] = put(rev)
			}
		}(i)
	}
	for k := 0; k < regens; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			gen := &timeline.Timeline{
				Version: timeline.Version,
				Canvas:  timeline.Canvas{Width: 640, Height: 360, FPS: 30},
				Tracks: []timeline.Track{{
					ID:   "v1",
					Kind: "video",
					Clips: []timeline.Clip{{
						ID: "gen", AssetID: asset.ID, SourceStart: 0, SourceEnd: 2,
						TimelineStart: 0, Speed: 1, Volume: 1,
					}},
				}},
			}
			if err := s.Pipe.WriteRegeneratedTimeline(p, gen); err != nil {
				t.Errorf("regeneration: %v", err)
			}
		}()
	}
	wg.Wait()

	accepted := 0
	for k, c := range codes {
		switch c {
		case http.StatusOK:
			accepted++
		case http.StatusConflict: // another writer moved first — fine
		case 0:
			t.Fatal("a writer never issued its PUT")
		default:
			t.Fatalf("unexpected status %d: %s", c, details[k])
		}
	}

	rec, out := do(t, s, "GET", "/api/v1/projects/"+p.ID+"/timeline", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("final get: %d", rec.Code)
	}
	want := int64(1 + accepted + regens)
	got := int64(out["timeline"].(map[string]any)["revision"].(float64))
	if got != want {
		t.Fatalf("final revision = %d, want %d (accepted=%d regens=%d) — a write collided on a revision", got, want, accepted, regens)
	}
}

// TestTimelinePutSurvivesAssetReimport: re-importing the same file must
// keep the asset's ID — otherwise every stored timeline clip referencing it
// starts failing validation ("unknown asset") and the render bricks until
// the timeline is regenerated.
func TestTimelinePutSurvivesAssetReimport(t *testing.T) {
	s := testServer(t)
	if _, err := s.DB.CreateProject(t.Context(), "reimport"); err != nil {
		t.Fatal(err)
	}
	p, _ := s.DB.GetProjectByName(t.Context(), "reimport")

	asset := storageAssetFor(p.ID)
	if err := s.DB.UpsertAsset(t.Context(), &asset); err != nil {
		t.Fatal(err)
	}
	tl := &timeline.Timeline{
		Version: timeline.Version,
		Canvas:  timeline.Canvas{Width: 640, Height: 360, FPS: 30},
		Tracks: []timeline.Track{{
			ID:   "v1",
			Kind: "video",
			Clips: []timeline.Clip{{
				ID: "c1", AssetID: asset.ID, SourceStart: 0, SourceEnd: 5,
				TimelineStart: 0, Speed: 1, Volume: 1,
			}},
		}},
	}
	if rec, _ := do(t, s, "PUT", "/api/v1/projects/"+p.ID+"/timeline", marshalTimeline(t, tl)); rec.Code != http.StatusOK {
		t.Fatalf("save before reimport: %d", rec.Code)
	}

	// Re-import the same path (fresh metadata, no ID — as importInto does).
	reimported := storageAssetFor(p.ID)
	reimported.DurationSec = 9 // probe data changed on disk
	if err := s.DB.UpsertAsset(t.Context(), &reimported); err != nil {
		t.Fatal(err)
	}
	if reimported.ID != asset.ID {
		t.Fatalf("reimport re-keyed the asset: %s -> %s", asset.ID, reimported.ID)
	}

	// The stored document still validates against the project's assets.
	rec, out := do(t, s, "GET", "/api/v1/projects/"+p.ID+"/timeline", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get after reimport: %d", rec.Code)
	}
	rev := int64(out["timeline"].(map[string]any)["revision"].(float64))
	tl.Revision = rev
	if rec, body := do(t, s, "PUT", "/api/v1/projects/"+p.ID+"/timeline", marshalTimeline(t, tl)); rec.Code != http.StatusOK {
		t.Fatalf("stored timeline no longer valid after reimport: %d %s", rec.Code, body)
	}
}

// TestPutFailureCarriesItsReason is the control for the assertion above: a
// rejected PUT must explain itself in the response body, otherwise
// "unexpected PUT status %d: %s" prints an empty diagnosis and the instrumented
// line is decoration.
func TestPutFailureCarriesItsReason(t *testing.T) {
	s := testServer(t)
	if _, err := s.DB.CreateProject(t.Context(), "put-reason"); err != nil {
		t.Fatal(err)
	}
	p, _ := s.DB.GetProjectByName(t.Context(), "put-reason")

	rec, _ := do(t, s, "PUT", "/api/v1/projects/"+p.ID+"/timeline", `{"version":1,"canvas":{`)
	if rec.Code == http.StatusOK {
		t.Fatalf("a truncated JSON body was accepted: %d", rec.Code)
	}
	body := strings.TrimSpace(rec.Body.String())
	if body == "" || body == "{}" {
		t.Fatalf("status %d answered with no explanation: %q", rec.Code, body)
	}
	if !strings.Contains(strings.ToLower(body), "invalid") &&
		!strings.Contains(strings.ToLower(body), "timeline") {
		t.Fatalf("body %q does not name what was wrong", body)
	}
}

// docWithClips is a document whose clips all sit inside the project's media, varying
// only the framing zoom — so a rejected save can only be about the zoom.
func docWithClips(t *testing.T, assetID string, n int, zoom float64) *timeline.Timeline {
	t.Helper()
	tl := &timeline.Timeline{
		Version: timeline.Version,
		Canvas:  timeline.Canvas{Width: 640, Height: 360, FPS: 30},
		Tracks:  []timeline.Track{{ID: "v1", Kind: "video"}},
	}
	for i := 0; i < n; i++ {
		tl.Tracks[0].Clips = append(tl.Tracks[0].Clips, timeline.Clip{
			ID: fmt.Sprintf("c%d", i+1), AssetID: assetID,
			SourceStart: float64(i) * 2, SourceEnd: float64(i)*2 + 1.5,
			TimelineStart: float64(i) * 1.5, Speed: 1, Volume: 1,
			Motion: &timeline.Motion{Zoom: zoom},
		})
	}
	return tl
}

// TestTimelinePUTSaysWhatItRejected: the walk over the editing surface sent a zoom of
// 3.0 and was told "timeline validation failed (1 problem(s))" — the same sentence an
// unknown asset and an empty document produce. A refused save has to point at the
// field, because the alternative is that the user guesses and re-sends.
func TestTimelinePUTSaysWhatItRejected(t *testing.T) {
	s := testServer(t)
	if _, err := s.DB.CreateProject(t.Context(), "reject"); err != nil {
		t.Fatal(err)
	}
	p, _ := s.DB.GetProjectByName(t.Context(), "reject")
	asset := storageAssetFor(p.ID)
	if err := s.DB.UpsertAsset(t.Context(), &asset); err != nil {
		t.Fatal(err)
	}
	assets, _ := s.DB.ListAssets(t.Context(), p.ID)
	aid := assets[0].ID

	// The shape is known good before it is broken: the same document at zoom 0.8
	// saves, so anything the next request is refused for is the zoom.
	if rec, out := do(t, s, "PUT", "/api/v1/projects/"+p.ID+"/timeline",
		marshalTimeline(t, docWithClips(t, aid, 2, 0.8))); rec.Code != http.StatusOK {
		t.Fatalf("a valid document was refused: %d %v", rec.Code, out)
	}
	rec, out := do(t, s, "PUT", "/api/v1/projects/"+p.ID+"/timeline",
		marshalTimeline(t, docWithClips(t, aid, 2, 3)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a zoom of 3.0 (larger than the source) was accepted: %d %v", rec.Code, out)
	}
	msg := fmt.Sprint(out["message"])
	for _, want := range []string{"zoom", "3", "c1"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not name %q: %s", want, msg)
		}
	}
	if strings.Contains(msg, "problem(s)") {
		t.Errorf("the refusal still counts instead of naming: %s", msg)
	}

	// Four problems, three named, the fourth counted — the message stays readable on
	// a document that is wrong in many places at once.
	rec, out = do(t, s, "PUT", "/api/v1/projects/"+p.ID+"/timeline",
		marshalTimeline(t, docWithClips(t, aid, 4, 3)))
	msg = fmt.Sprint(out["message"])
	if !strings.Contains(msg, "and 1 more") {
		t.Errorf("the fourth problem is neither named nor counted: %s", msg)
	}
	if strings.Contains(msg, "c4") {
		t.Errorf("problems past the cap should be counted, not printed: %s", msg)
	}
}
