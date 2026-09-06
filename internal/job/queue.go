// Package job implements XCut's job queue: DB-backed job rows, bounded
// concurrency, progress reporting, and startup reconciliation of orphaned jobs.
//
// Resource policy lives here: the queue never exceeds MaxConcurrentJobs, and
// each job declares a ResourceClass the future scheduler can weight (phase 1:
// one semaphore for everything).
package job

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/xcerr"
)

// ResourceClass is a coarse scheduling weight for future resource-aware
// scheduling. Phase 1 records it; enforcement is a single global semaphore.
type ResourceClass string

const (
	ClassCPULight  ResourceClass = "CPU_LIGHT"
	ClassCPUHeavy  ResourceClass = "CPU_HEAVY"
	ClassIOHeavy   ResourceClass = "IO_HEAVY"
	ClassGPUMedium ResourceClass = "GPU_MEDIUM"
	ClassGPUHeavy  ResourceClass = "GPU_HEAVY"
)

// Queue runs jobs with bounded concurrency.
type Queue struct {
	db    *storage.DB
	log   *slog.Logger
	slots chan struct{}
}

// NewQueue builds a queue allowing at most maxConcurrent simultaneously
// running jobs (>=1 enforced).
func NewQueue(db *storage.DB, maxConcurrent int, log *slog.Logger) *Queue {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	if log == nil {
		log = slog.Default()
	}
	return &Queue{db: db, log: log, slots: make(chan struct{}, maxConcurrent)}
}

// Runner executes the job body. progress reports 0..1 (calls are throttled).
type Runner func(ctx context.Context, progress func(p float64)) error

// RunInline records a job row and executes fn to completion, blocking the
// caller (phase 1 has no background workers yet; `serve` will add them).
// Terminal state is always persisted, even on panic.
func (q *Queue) RunInline(ctx context.Context, typ, projectID string, class ResourceClass, payload any, fn Runner) (string, error) {
	var payloadJSON string
	if payload != nil {
		if b, err := json.Marshal(payload); err == nil {
			payloadJSON = string(b)
		}
	}
	rec, err := q.db.CreateJob(ctx, typ, projectID, string(class), payloadJSON)
	if err != nil {
		return "", err
	}
	id := rec.ID

	// Acquire a concurrency slot (cancellation-aware).
	select {
	case q.slots <- struct{}{}:
		defer func() { <-q.slots }()
	case <-ctx.Done():
		_ = q.db.FinishJob(context.WithoutCancel(ctx), id, storage.StatusCancelled, "cancelled", "cancelled before start")
		return id, xcerr.E(xcerr.CodeCancelled, "job cancelled before start", ctx.Err())
	}

	if err := q.db.SetJobRunning(ctx, id); err != nil {
		return id, err
	}
	q.log.Info("job started", "job_id", id, "type", typ, "project_id", projectID, "class", class)

	lastWrite := time.Now()
	progress := func(p float64) {
		now := time.Now()
		if now.Sub(lastWrite) < 250*time.Millisecond && p < 1 {
			return // throttle DB writes
		}
		lastWrite = now
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = q.db.SetJobProgress(cctx, id, p)
	}

	runErr := q.safeRun(ctx, id, fn, progress)

	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	switch {
	case runErr == nil:
		if err := q.db.FinishJob(cctx, id, storage.StatusSucceeded, "", ""); err != nil {
			q.log.Error("job finish bookkeeping failed", "job_id", id, "err", err)
		}
		q.log.Info("job succeeded", "job_id", id, "type", typ, "duration_ms", time.Since(time.Unix(rec.CreatedAt, 0)).Milliseconds())
		return id, nil
	case ctx.Err() != nil:
		_ = q.db.FinishJob(cctx, id, storage.StatusCancelled, string(xcerr.CodeCancelled), "cancelled")
		return id, xcerr.E(xcerr.CodeCancelled, "job cancelled", runErr)
	default:
		code := xcerr.CodeOf(runErr)
		_ = q.db.FinishJob(cctx, id, storage.StatusFailed, string(code), xcerr.UserMessage(runErr))
		q.log.Error("job failed", "job_id", id, "type", typ, "error_code", code, "err", runErr)
		return id, runErr
	}
}

// safeRun runs fn and converts a panic into a failed job rather than crashing
// the process; the panic is re-thrown only if it cannot be attributed.
func (q *Queue) safeRun(ctx context.Context, id string, fn Runner, progress func(float64)) (err error) {
	defer func() {
		if r := recover(); r != nil {
			q.log.Error("job panicked", "job_id", id, "panic", r)
			err = xcerr.E(xcerr.CodeInternal, "internal error while running job", nil)
		}
	}()
	return fn(ctx, progress)
}

// ReconcileOrphans marks jobs left queued/running by a previous dead process
// as failed. Must be called once at startup before running new jobs.
// Note: concurrent xcut processes on one workspace are not supported in
// phase 1; this reconciliation assumes it runs from the only live process.
func (q *Queue) ReconcileOrphans(ctx context.Context, olderThan time.Duration) (int, error) {
	ids, err := q.db.ReconcileStale(ctx, olderThan)
	if err != nil {
		return 0, err
	}
	if len(ids) > 0 {
		q.log.Warn("reconciled orphaned jobs", "count", len(ids), "job_ids", ids)
	}
	return len(ids), nil
}
