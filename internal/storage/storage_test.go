package storage

import (
	"context"
	"github.com/xiabee/XCut/internal/xcerr"
	"path/filepath"
	"testing"
	"time"
)

func testDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestProjectLifecycle(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	p, err := db.CreateProject(ctx, "badminton-2026")
	if err != nil {
		t.Fatal(err)
	}
	if p.ID == "" || p.Name != "badminton-2026" {
		t.Fatalf("bad project: %+v", p)
	}

	// Duplicate name rejected.
	if _, err := db.CreateProject(ctx, "badminton-2026"); err == nil {
		t.Fatal("expected duplicate name error")
	}

	// By name + list.
	got, err := db.GetProjectByName(ctx, "badminton-2026")
	if err != nil || got == nil || got.ID != p.ID {
		t.Fatalf("GetProjectByName = %+v, %v", got, err)
	}
	ps, _ := db.ListProjects(ctx)
	if len(ps) != 1 {
		t.Fatalf("ListProjects = %d", len(ps))
	}

	// Missing → nil, no error.
	missing, err := db.GetProjectByName(ctx, "nope")
	if err != nil || missing != nil {
		t.Fatalf("missing project: %+v, %v", missing, err)
	}

	// Delete cascades to assets.
	if err := db.UpsertAsset(ctx, &Asset{ProjectID: p.ID, Path: "x.mp4", Filename: "x.mp4", Fingerprint: "fp"}); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteProject(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	assets, err := db.ListAssets(ctx, p.ID)
	if err != nil || len(assets) != 0 {
		t.Fatalf("assets after delete: %d, %v", len(assets), err)
	}
}

func TestAssetUpsert(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	p, _ := db.CreateProject(ctx, "p1")

	a1 := &Asset{ProjectID: p.ID, Path: `D:\vids\match.mp4`, Filename: "match.mp4", Fingerprint: "fp1", DurationSec: 10.5, Width: 1920, Height: 1080, FPS: 29.97, VideoCodec: "h264", HasAudio: true, AudioCodec: "aac"}
	if err := db.UpsertAsset(ctx, a1); err != nil {
		t.Fatal(err)
	}

	// Same path → update, not duplicate.
	a1b := &Asset{ProjectID: p.ID, Path: `D:\vids\match.mp4`, Filename: "match.mp4", Fingerprint: "fp2", DurationSec: 11, Width: 1920, Height: 1080, FPS: 30, VideoCodec: "h264"}
	if err := db.UpsertAsset(ctx, a1b); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetAsset(ctx, a1b.ID)
	if err != nil || got == nil {
		t.Fatalf("GetAsset: %+v, %v", got, err)
	}
	if got.Fingerprint != "fp2" || got.DurationSec != 11 {
		t.Fatalf("upsert did not update: %+v", got)
	}
	list, _ := db.ListAssets(ctx, p.ID)
	if len(list) != 1 {
		t.Fatalf("expected 1 asset after upsert, got %d", len(list))
	}
}

func TestJobLifecycleAndReconcile(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	p, err := db.CreateProject(ctx, "jp")
	if err != nil {
		t.Fatal(err)
	}

	j, err := db.CreateJob(ctx, "import", p.ID, "IO_HEAVY", `{"path":"a.mp4"}`)
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != StatusQueued {
		t.Fatalf("status = %s", j.Status)
	}

	if err := db.SetJobRunning(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.SetJobProgress(ctx, j.ID, 1.7); err != nil {
		t.Fatal(err)
	}
	got, _ := db.GetJob(ctx, j.ID)
	if got.Status != StatusRunning || got.Progress != 1 || got.Attempt != 1 {
		t.Fatalf("running job: %+v", got)
	}

	if err := db.FinishJob(ctx, j.ID, StatusFailed, "validation", "bad input"); err != nil {
		t.Fatal(err)
	}
	got, _ = db.GetJob(ctx, j.ID)
	if got.Status != StatusFailed || got.ErrorCode != "validation" || got.FinishedAt == nil {
		t.Fatalf("finished job: %+v", got)
	}

	// Invalid terminal status rejected.
	if err := db.FinishJob(ctx, j.ID, "running", "", ""); err == nil {
		t.Fatal("expected invalid terminal status error")
	}

	// Stale reconciliation: a queued job created "3h ago" must be orphaned.
	old, _ := db.CreateJob(ctx, "render", p.ID, "CPU_HEAVY", "")
	if _, err := db.Exec(`UPDATE jobs SET created_at = ? WHERE id = ?`, time.Now().Add(-3*time.Hour).Unix(), old.ID); err != nil {
		t.Fatal(err)
	}
	ids, err := db.ReconcileStale(ctx, 2*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != old.ID {
		t.Fatalf("reconciled %v, want [%s]", ids, old.ID)
	}
	got, _ = db.GetJob(ctx, old.ID)
	if got.Status != StatusFailed || got.ErrorCode != "orphaned" {
		t.Fatalf("orphaned job: %+v", got)
	}

	// Fresh jobs are not touched.
	fresh, _ := db.CreateJob(ctx, "render", p.ID, "CPU_HEAVY", "")
	ids, _ = db.ReconcileStale(ctx, 2*time.Hour)
	if len(ids) != 0 {
		t.Fatalf("fresh job reconciled: %v", ids)
	}
	_ = fresh
}

func TestExclusiveActiveJobConflict(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	p, err := db.CreateProject(ctx, "exclusive")
	if err != nil {
		t.Fatal(err)
	}

	first, err := db.CreateJob(ctx, "render", p.ID, "CPU_HEAVY", "")
	if err != nil {
		t.Fatal(err)
	}

	// Same type, still active → conflict (storage-layer guarantee).
	if _, err := db.CreateJob(ctx, "render", p.ID, "CPU_HEAVY", ""); !xcerr.IsCode(err, xcerr.CodeConflict) {
		t.Fatalf("duplicate render err = %v, want conflict", err)
	}

	// A different exclusive type is fine.
	if _, err := db.CreateJob(ctx, "analyze", p.ID, "CPU_HEAVY", ""); err != nil {
		t.Fatalf("analyze while render active: %v", err)
	}

	// Import is not exclusive: concurrent imports must stay legitimate.
	if _, err := db.CreateJob(ctx, "import", p.ID, "IO_HEAVY", `{"path":"a"}`); err != nil {
		t.Fatalf("first import: %v", err)
	}
	if _, err := db.CreateJob(ctx, "import", p.ID, "IO_HEAVY", `{"path":"b"}`); err != nil {
		t.Fatalf("second import must not conflict: %v", err)
	}

	// After the first render reaches a terminal state, rendering is possible
	// again.
	if err := db.FinishJob(ctx, first.ID, StatusSucceeded, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateJob(ctx, "render", p.ID, "CPU_HEAVY", ""); err != nil {
		t.Fatalf("render after terminal state: %v", err)
	}
}

func TestFindActiveJob(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	p, err := db.CreateProject(ctx, "active")
	if err != nil {
		t.Fatal(err)
	}
	if j, err := db.FindActiveJob(ctx, "render", p.ID); err != nil || j != nil {
		t.Fatalf("empty scan: job=%v err=%v", j, err)
	}

	created, err := db.CreateJob(ctx, "render", p.ID, "CPU_HEAVY", "")
	if err != nil {
		t.Fatal(err)
	}
	got, err := db.FindActiveJob(ctx, "render", p.ID)
	if err != nil || got == nil || got.ID != created.ID {
		t.Fatalf("FindActiveJob = %+v, %v; want %s", got, err, created.ID)
	}

	// Terminal job must not count as active.
	if err := db.FinishJob(ctx, created.ID, StatusFailed, "x", "x"); err != nil {
		t.Fatal(err)
	}
	if j, err := db.FindActiveJob(ctx, "render", p.ID); err != nil || j != nil {
		t.Fatalf("failed job still active: job=%v err=%v", j, err)
	}
}

func TestPruneJobHistory(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	p, err := db.CreateProject(ctx, "prune")
	if err != nil {
		t.Fatal(err)
	}

	// 4 terminal jobs with distinct finish order + 1 running (never pruned).
	// Each job is driven to a terminal state before the next is created —
	// the exclusive-active index (migration v2) would refuse overlapping
	// same-type rows.
	var ids []string
	for i := 0; i < 4; i++ {
		j, err := db.CreateJob(ctx, "analyze", p.ID, "CPU_HEAVY", "")
		if err != nil {
			t.Fatal(err)
		}
		if err := db.SetJobRunning(ctx, j.ID); err != nil {
			t.Fatal(err)
		}
		if err := db.FinishJob(ctx, j.ID, StatusSucceeded, "", ""); err != nil {
			t.Fatal(err)
		}
		// Backdate the finish order deterministically (FinishJob stamps now).
		finished := int64(1000 + i)
		if _, err := db.Exec(`UPDATE jobs SET finished_at = ? WHERE id = ?`, finished, j.ID); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, j.ID)
	}
	running, err := db.CreateJob(ctx, "render", p.ID, "CPU_HEAVY", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetJobRunning(ctx, running.ID); err != nil {
		t.Fatal(err)
	}

	removed, err := db.PruneJobHistory(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 {
		t.Fatalf("removed %d rows, want 2", removed)
	}

	// The two NEWEST terminal jobs survive, the running row is untouched.
	for _, id := range ids[2:] {
		j, _ := db.GetJob(ctx, id)
		if j == nil {
			t.Fatalf("newest terminal job %s was pruned", id)
		}
	}
	if j, _ := db.GetJob(ctx, ids[0]); j != nil {
		t.Fatal("oldest terminal job should have been pruned")
	}
	if j, _ := db.GetJob(ctx, running.ID); j == nil || j.Status != StatusRunning {
		t.Fatalf("running job must survive pruning: %+v", j)
	}

	// keep<=0 is a no-op guard.
	if _, err := db.PruneJobHistory(ctx, 0); err != nil {
		t.Fatalf("keep=0: %v", err)
	}
}
