package storage

import (
	"context"
	"github.com/xiabee/XCut/internal/xcerr"
	"path/filepath"
	"slices"
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
	if n, err := db.DeleteProject(ctx, p.ID); err != nil || n != 1 {
		t.Fatalf("delete = %d rows, %v", n, err)
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
	origID, origCreated := a1.ID, a1.CreatedAt

	// Same path → update, not duplicate. The asset ID must stay stable:
	// stored timelines reference clips by asset ID, so re-importing the
	// same file must never re-key them (a new ID bricks every clip).
	a1b := &Asset{ProjectID: p.ID, Path: `D:\vids\match.mp4`, Filename: "match.mp4", Fingerprint: "fp2", DurationSec: 11, Width: 1920, Height: 1080, FPS: 30, VideoCodec: "h264"}
	if err := db.UpsertAsset(ctx, a1b); err != nil {
		t.Fatal(err)
	}
	if a1b.ID != origID {
		t.Fatalf("re-import re-keyed the asset: %s -> %s", origID, a1b.ID)
	}
	if a1b.CreatedAt != origCreated {
		t.Fatalf("re-import changed created_at: %d -> %d", origCreated, a1b.CreatedAt)
	}
	got, err := db.GetAsset(ctx, origID)
	if err != nil || got == nil {
		t.Fatalf("GetAsset: %+v, %v", got, err)
	}
	if got.Fingerprint != "fp2" || got.DurationSec != 11 {
		t.Fatalf("upsert did not update probe data: %+v", got)
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

	// Every type the schema's index declares is driven off that declaration rather
	// than a hand-copied list: a type added to the migration with no test here (or
	// the reverse) is the same drift the index exists to make impossible.
	exclusive := ExclusiveJobTypes()
	if len(exclusive) == 0 {
		t.Fatal("the schema names no exclusive job types at all")
	}
	t.Logf("exclusive types enforced by the index: %v", exclusive)
	for _, typ := range exclusive {
		first, err := db.CreateJob(ctx, typ, p.ID, "CPU_HEAVY", "")
		if err != nil {
			t.Fatalf("first %s job: %v", typ, err)
		}
		if _, err := db.CreateJob(ctx, typ, p.ID, "CPU_HEAVY", ""); !xcerr.IsCode(err, xcerr.CodeConflict) {
			t.Fatalf("duplicate %s err = %v, want conflict", typ, err)
		}
		// Once the first reaches a terminal state the type is available again — and
		// that second row has to be finished too, or it is still active and the
		// assertions below are testing a leftover.
		if err := db.FinishJob(ctx, first.ID, StatusSucceeded, "", ""); err != nil {
			t.Fatal(err)
		}
		second, err := db.CreateJob(ctx, typ, p.ID, "CPU_HEAVY", "")
		if err != nil {
			t.Fatalf("%s after its job finished: %v", typ, err)
		}
		if err := db.FinishJob(ctx, second.ID, StatusSucceeded, "", ""); err != nil {
			t.Fatal(err)
		}
	}

	// A different exclusive type is fine while one runs.
	active, err := db.CreateJob(ctx, "render", p.ID, "CPU_HEAVY", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateJob(ctx, "analyze", p.ID, "CPU_HEAVY", ""); err != nil {
		t.Fatalf("analyze while render active: %v", err)
	}
	if err := db.FinishJob(ctx, active.ID, StatusSucceeded, "", ""); err != nil {
		t.Fatal(err)
	}

	// Import is not exclusive: concurrent imports must stay legitimate.
	if _, err := db.CreateJob(ctx, "import", p.ID, "IO_HEAVY", `{"path":"a"}`); err != nil {
		t.Fatalf("first import: %v", err)
	}
	if _, err := db.CreateJob(ctx, "import", p.ID, "IO_HEAVY", `{"path":"b"}`); err != nil {
		t.Fatalf("second import must not conflict: %v", err)
	}
	if slices.Contains(ExclusiveJobTypes(), "import") {
		t.Error("import became exclusive, which would refuse legitimate parallel imports")
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

func TestHasActiveJobs(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	p, err := db.CreateProject(ctx, "guard")
	if err != nil {
		t.Fatal(err)
	}
	if active, err := db.HasActiveJobs(ctx, p.ID); err != nil || active {
		t.Fatalf("empty project: active=%v err=%v", active, err)
	}

	j, err := db.CreateJob(ctx, "render", p.ID, "CPU_HEAVY", "")
	if err != nil {
		t.Fatal(err)
	}
	if active, err := db.HasActiveJobs(ctx, p.ID); err != nil || !active {
		t.Fatalf("queued render: active=%v err=%v", active, err)
	}
	if err := db.SetJobRunning(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	if active, err := db.HasActiveJobs(ctx, p.ID); err != nil || !active {
		t.Fatalf("running render: active=%v err=%v", active, err)
	}
	if err := db.FinishJob(ctx, j.ID, StatusFailed, "x", "x"); err != nil {
		t.Fatal(err)
	}
	if active, err := db.HasActiveJobs(ctx, p.ID); err != nil || active {
		t.Fatalf("terminal render: active=%v err=%v", active, err)
	}
}

// TestDeleteProjectGateIsAtomic: the active-job gate and the delete are one
// statement. A check-then-act pair left a window where a trigger enqueueing
// a job during the delete had its row cascade-deleted under a live runner.
func TestDeleteProjectGateIsAtomic(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	p, err := db.CreateProject(ctx, "busy")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateJob(ctx, "render", p.ID, "CPU_HEAVY", "{}"); err != nil {
		t.Fatal(err)
	}

	// A queued job blocks the delete (0 rows, no error).
	if n, err := db.DeleteProject(ctx, p.ID); err != nil || n != 0 {
		t.Fatalf("delete with queued job = %d rows, %v — must be refused", n, err)
	}
	if got, err := db.GetProject(ctx, p.ID); err != nil || got == nil {
		t.Fatalf("project must survive a refused delete: %+v, %v", got, err)
	}

	// Finishing the job lifts the gate.
	jobs, err := db.ListJobs(ctx, p.ID)
	if err != nil || len(jobs) == 0 {
		t.Fatalf("list jobs: %d, %v", len(jobs), err)
	}
	for _, j := range jobs {
		if err := db.FinishJob(ctx, j.ID, StatusSucceeded, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := db.DeleteProject(ctx, p.ID); err != nil || n != 1 {
		t.Fatalf("delete after terminal jobs = %d rows, %v — must succeed", n, err)
	}
}

// TestExclusiveIndexMigrationAppliesToAnOlderDatabase: a schema change is only real
// when it applies to a file that predates it. v7 replaces the exclusive-jobs index
// while active rows written under the old one are still in the table, so the test
// rolls a database back to its v6 shape (drop the index, forget the migration),
// leaves an active job behind, and upgrades again.
func TestExclusiveIndexMigrationAppliesToAnOlderDatabase(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	p, err := db.CreateProject(ctx, "upgrader")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateJob(ctx, "render", p.ID, "CPU_HEAVY", ""); err != nil {
		t.Fatal(err) // one still-active row from the old world
	}
	path := db.path
	if _, err := db.ExecContext(ctx, `DROP INDEX IF EXISTS idx_jobs_active_exclusive`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE id >= 7`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(path)
	if err != nil {
		t.Fatalf("the v7 upgrade refused a database that predates it: %v", err)
	}
	t.Cleanup(func() { upgraded.Close() })
	var applied int
	if err := upgraded.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE id = 7`).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied != 1 {
		t.Fatalf("schema_migrations records v7 %d times, want 1", applied)
	}
	// The rebuilt index must cover the widened list — and it applies retroactively:
	// the render left running above is refused a partner even though it was written
	// before the upgrade.
	if _, err := upgraded.CreateJob(ctx, "render", p.ID, "CPU_HEAVY", ""); !xcerr.IsCode(err, xcerr.CodeConflict) {
		t.Fatalf("the index does not reach a row written before the upgrade (err = %v)", err)
	}
	for _, typ := range ExclusiveJobTypes() {
		if typ == "render" {
			continue
		}
		if _, err := upgraded.CreateJob(ctx, typ, p.ID, "CPU_HEAVY", ""); err != nil {
			t.Fatalf("first %s after the upgrade: %v", typ, err)
		}
		if _, err := upgraded.CreateJob(ctx, typ, p.ID, "CPU_HEAVY", ""); !xcerr.IsCode(err, xcerr.CodeConflict) {
			t.Fatalf("%s is not exclusive after the upgrade (err = %v)", typ, err)
		}
	}
}
