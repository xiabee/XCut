package job

import (
	"context"
	"log/slog"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/xcerr"
)

func testQueue(t *testing.T, maxConcurrent int) (*Queue, *storage.DB) {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return NewQueue(db, maxConcurrent, 1, 0, slog.New(slog.NewTextHandler(&testWriter{t}, nil))), db
}

type testWriter struct{ t *testing.T }

func (w *testWriter) Write(p []byte) (int, error) { return len(p), nil }

func TestRunInlineSuccess(t *testing.T) {
	q, db := testQueue(t, 2)
	ctx := context.Background()

	if _, err := db.CreateProject(ctx, "p"); err != nil {
		t.Fatal(err)
	}
	p, _ := db.GetProjectByName(ctx, "p")

	var progressed atomic.Bool
	id, err := q.RunInline(ctx, "analyze", p.ID, ClassCPUHeavy, map[string]int{"k": 1},
		func(ctx context.Context, progress func(float64)) error {
			progress(0.5)
			progressed.Store(true)
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	j, err := db.GetJob(ctx, id)
	if err != nil || j == nil {
		t.Fatalf("GetJob: %+v, %v", j, err)
	}
	if j.Status != storage.StatusSucceeded || j.Progress != 1 {
		t.Fatalf("job: %+v", j)
	}
	if !progressed.Load() {
		t.Fatal("progress callback never invoked")
	}
	if j.ResourceClass != string(ClassCPUHeavy) {
		t.Fatalf("resource class: %s", j.ResourceClass)
	}
}

func TestRunInlineFailureRecordsCode(t *testing.T) {
	q, db := testQueue(t, 1)
	ctx := context.Background()

	if _, err := db.CreateProject(ctx, "p"); err != nil {
		t.Fatal(err)
	}
	p, _ := db.GetProjectByName(ctx, "p")

	id, err := q.RunInline(ctx, "render", p.ID, ClassCPUHeavy, nil,
		func(ctx context.Context, progress func(float64)) error {
			return xcerr.E(xcerr.CodeFFmpegFailure, "encode exploded", nil)
		})
	if err == nil {
		t.Fatal("expected error")
	}
	j, _ := db.GetJob(ctx, id)
	if j == nil {
		t.Fatal("job row missing")
	}
	if j.Status != storage.StatusFailed || j.ErrorCode != "ffmpeg_failure" || j.ErrorMessage != "encode exploded" {
		t.Fatalf("failed job: %+v", j)
	}
}

func TestQueueConcurrencyBound(t *testing.T) {
	q, _ := testQueue(t, 2)
	ctx := context.Background()

	var running, maxRunning int64
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = q.RunInline(ctx, "analyze", "", ClassCPULight, nil,
				func(ctx context.Context, progress func(float64)) error {
					cur := atomic.AddInt64(&running, 1)
					for {
						old := atomic.LoadInt64(&maxRunning)
						if cur <= old || atomic.CompareAndSwapInt64(&maxRunning, old, cur) {
							break
						}
					}
					time.Sleep(30 * time.Millisecond)
					atomic.AddInt64(&running, -1)
					return nil
				})
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt64(&maxRunning); got > 2 {
		t.Fatalf("max concurrent = %d, want <= 2", got)
	}
}

func TestReconcileOrphans(t *testing.T) {
	q, db := testQueue(t, 1)
	ctx := context.Background()

	// Simulate a dead process: a running job created 3h ago.
	id, err := q.RunInline(context.Background(), "render", "", ClassCPUHeavy, nil,
		func(ctx context.Context, progress func(float64)) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-3 * time.Hour).Unix()
	if _, err := db.Exec(`UPDATE jobs SET created_at = ?, status = 'running', started_at = ?, finished_at = NULL WHERE id = ?`,
		past, past, id); err != nil {
		t.Fatal(err)
	}

	n, err := q.ReconcileOrphans(ctx, 2*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("reconciled %d, want 1", n)
	}
	j, jerr := db.GetJob(ctx, id)
	if jerr != nil {
		t.Fatalf("GetJob: %v", jerr)
	}
	if j == nil {
		var cnt int
		_ = db.QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&cnt)
		var gotID string
		_ = db.QueryRow(`SELECT id FROM jobs LIMIT 1`).Scan(&gotID)
		t.Fatalf("job row missing after reconcile: count=%d first_id=%q want=%q", cnt, gotID, id)
	}
}

func TestRenderWorkerBound(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	// Generic pool admits 3; renders are capped at 1.
	q := NewQueue(db, 3, 1, 0, slog.New(slog.NewTextHandler(&testWriter{t}, nil)))
	ctx := context.Background()

	var running, maxRunning atomic.Int64
	runner := func(ctx context.Context, progress func(float64)) error {
		cur := running.Add(1)
		for {
			old := maxRunning.Load()
			if cur <= old || maxRunning.CompareAndSwap(old, cur) {
				break
			}
		}
		time.Sleep(60 * time.Millisecond)
		running.Add(-1)
		return nil
	}

	for i := 0; i < 4; i++ {
		if _, err := q.RunAsync(ctx, TypeRender, "", ClassCPUHeavy, nil, runner); err != nil {
			t.Fatal(err)
		}
	}
	q.Wait()
	if got := maxRunning.Load(); got != 1 {
		t.Fatalf("max concurrent renders = %d, want 1", got)
	}
}

func TestRenderBoundLeavesOtherJobsFree(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	// Pool of 4: the parked render holds one generic slot, leaving exactly
	// three for the analyze jobs below.
	q := NewQueue(db, 4, 1, 0, slog.New(slog.NewTextHandler(&testWriter{t}, nil)))
	ctx := context.Background()

	// A render parked in the generic pool must not stop non-render jobs:
	// if the render bound were a shared class semaphore, analyze jobs would
	// queue behind it forever.
	release := make(chan struct{})
	if _, err := q.RunAsync(ctx, TypeRender, "", ClassCPUHeavy, nil,
		func(ctx context.Context, progress func(float64)) error {
			<-release
			return nil
		}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond) // let the render job start

	var running, maxRunning atomic.Int64
	var analyzed sync.WaitGroup
	for i := 0; i < 3; i++ {
		analyzed.Add(1)
		if _, err := q.RunAsync(ctx, TypeAnalyze, "", ClassCPUHeavy, nil,
			func(ctx context.Context, progress func(float64)) error {
				defer analyzed.Done()
				cur := running.Add(1)
				for {
					old := maxRunning.Load()
					if cur <= old || maxRunning.CompareAndSwap(old, cur) {
						break
					}
				}
				time.Sleep(40 * time.Millisecond)
				running.Add(-1)
				return nil
			}); err != nil {
			t.Fatal(err)
		}
	}
	analyzed.Wait() // would hang if analyze jobs were blocked behind the render slot
	close(release)
	q.Wait()
	if got := maxRunning.Load(); got != 3 {
		t.Fatalf("max concurrent analyze jobs = %d, want 3", got)
	}
}

func TestRunAsyncDuplicateConflict(t *testing.T) {
	q, db := testQueue(t, 2)
	ctx := context.Background()

	if _, err := db.CreateProject(ctx, "p"); err != nil {
		t.Fatal(err)
	}
	p, _ := db.GetProjectByName(ctx, "p")

	release := make(chan struct{})
	id1, err := q.RunAsync(ctx, TypeRender, p.ID, ClassCPUHeavy, nil,
		func(ctx context.Context, progress func(float64)) error {
			<-release
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}

	_, err = q.RunAsync(ctx, TypeRender, p.ID, ClassCPUHeavy, nil, noopRunner)
	if !xcerr.IsCode(err, xcerr.CodeConflict) {
		t.Fatalf("duplicate err = %v, want conflict", err)
	}

	// Non-exclusive types are never deduped.
	for i := 0; i < 2; i++ {
		if _, err := q.RunAsync(ctx, TypeImport, p.ID, ClassIOHeavy, nil, noopRunner); err != nil {
			t.Fatalf("import #%d must not conflict: %v", i+1, err)
		}
	}

	close(release)
	q.Wait()

	// Terminal state frees the slot: enqueueing works again.
	id2, err := q.RunAsync(ctx, TypeRender, p.ID, ClassCPUHeavy, nil, noopRunner)
	if err != nil {
		t.Fatalf("render after completion: %v", err)
	}
	if id2 == id1 {
		t.Fatal("job ids must differ")
	}
	q.Wait()
}

func noopRunner(ctx context.Context, progress func(float64)) error { return nil }

func TestQueuePrunesHistory(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	q := NewQueue(db, 2, 1, 2, slog.New(slog.NewTextHandler(&testWriter{t}, nil)))
	ctx := context.Background()

	if _, err := db.CreateProject(ctx, "p"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := q.RunInline(ctx, TypeImport, "", ClassIOHeavy, nil, noopRunner); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE status = ?`, storage.StatusSucceeded).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("terminal job rows = %d, want 2 (max_history)", n)
	}
}
