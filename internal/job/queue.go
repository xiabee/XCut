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
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/xcerr"
)

// ResourceClass is a coarse scheduling weight for future resource-aware
// scheduling. Phase 1 records it; enforcement is a single global semaphore
// plus a render-specific bound (MaxRenderWorkers).
type ResourceClass string

const (
	ClassCPULight  ResourceClass = "CPU_LIGHT"
	ClassCPUHeavy  ResourceClass = "CPU_HEAVY"
	ClassIOHeavy   ResourceClass = "IO_HEAVY"
	ClassGPUMedium ResourceClass = "GPU_MEDIUM"
	ClassGPUHeavy  ResourceClass = "GPU_HEAVY"
)

// Job type names (stored in the jobs table, matched by callers).
const (
	TypeImport    = "import"
	TypeAnalyze   = "analyze"
	TypeTimeline  = "timeline"
	TypeRender    = "render"
	TypeSubtitles = "subtitles"
)

// Queue runs jobs with bounded concurrency.
type Queue struct {
	db    *storage.DB
	log   *slog.Logger
	slots chan struct{}
	// renders bounds concurrent TypeRender jobs independently of slots
	// (resource.max_render_workers): renders hold an ffmpeg process nearly
	// continuously, so one crowded generic slot pool must not mean four
	// simultaneous encodes. nil when the bound is >= the generic pool.
	renders chan struct{}
	// maxHistory caps terminal job rows (jobs.max_history); the queue
	// prunes as jobs finish. <=0 disables pruning (tests).
	maxHistory int
	wg         sync.WaitGroup

	// cancels holds one CancelFunc per active async job (queued or running),
	// registered synchronously in RunAsync before the runner goroutine starts
	// and removed at terminal state. Cancel(id) cancels the job's derived
	// context; the standard terminal bookkeeping in runJob persists
	// StatusCancelled. Inline jobs (CLI) are not registered — their owner
	// process cancels them with Ctrl+C.
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

// NewQueue builds a queue allowing at most maxConcurrent simultaneously
// running jobs (>=1 enforced), of which at most maxRenderWorkers are render
// jobs (>=1 enforced), keeping at most maxHistory terminal job rows
// (<=0 disables pruning).
func NewQueue(db *storage.DB, maxConcurrent, maxRenderWorkers, maxHistory int, log *slog.Logger) *Queue {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	if maxRenderWorkers < 1 {
		maxRenderWorkers = 1
	}
	if log == nil {
		log = slog.Default()
	}
	q := &Queue{db: db, log: log, slots: make(chan struct{}, maxConcurrent), maxHistory: maxHistory,
		cancels: make(map[string]context.CancelFunc)}
	if maxRenderWorkers < maxConcurrent {
		q.renders = make(chan struct{}, maxRenderWorkers)
	}
	return q
}

// Runner executes the job body. progress reports 0..1 (calls are throttled).
type Runner func(ctx context.Context, progress func(p float64)) error

// RunInline records a job row and executes fn to completion, blocking the
// caller. Terminal state is always persisted, even on panic.
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
	runErr := q.runJob(ctx, id, typ, projectID, fn)
	switch {
	case runErr == nil:
		return id, nil
	case ctx.Err() != nil:
		return id, xcerr.E(xcerr.CodeCancelled, "job cancelled", runErr)
	default:
		return id, runErr
	}
}

// safeRun runs fn and converts a panic into a failed job rather than crashing
// the process.
func (q *Queue) safeRun(ctx context.Context, id string, fn Runner, progress func(float64)) (err error) {
	defer func() {
		if r := recover(); r != nil {
			q.log.Error("job panicked", "job_id", id, "panic", r)
			err = xcerr.E(xcerr.CodeInternal, "internal error while running job", nil)
		}
	}()
	return fn(ctx, progress)
}

// Wait blocks until all async jobs have reached a terminal state. Call at
// graceful shutdown (bounded by the caller's context/timeout).
func (q *Queue) Wait() { q.wg.Wait() }

// WaitContext is Wait with a deadline: it returns ctx.Err() when jobs are
// still draining as the context expires (a wedged job that ignored its own
// cancellation must not own the shutdown path — the next startup sweeps
// whatever rows it leaves behind).
func (q *Queue) WaitContext(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		q.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// exclusiveTypes may have at most one queued/running job per project.
// Import is deliberately excluded — concurrent imports of different files
// are legitimate. Must stay in sync with migration v2's partial unique
// index, which enforces the same rule at the storage layer.
var exclusiveTypes = map[string]bool{
	TypeAnalyze:  true,
	TypeTimeline: true,
	TypeRender:   true,
}

// RunAsync records a job row and executes fn in a background goroutine,
// returning the job id immediately. Concurrency is still bounded by the
// queue's slots; poll the DB for status. Used by the HTTP API. Duplicate
// active exclusive jobs (same project+type) are refused with Conflict —
// e.g. a second render would drive a second encode into the same output.
// Global jobs (empty projectID) are never deduped, matching the migration
// v2 index where NULL project rows are distinct.
func (q *Queue) RunAsync(ctx context.Context, typ, projectID string, class ResourceClass, payload any, fn Runner) (string, error) {
	if projectID != "" && exclusiveTypes[typ] {
		if active, err := q.db.FindActiveJob(ctx, typ, projectID); err != nil {
			return "", err
		} else if active != nil {
			return "", xcerr.E(xcerr.CodeConflict,
				fmt.Sprintf("a %s job for this project is already %s — wait for it or poll its status instead of queueing a duplicate", typ, active.Status), nil)
		}
	}
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
	// Register the cancel handle synchronously so a Cancel arriving between
	// row creation and goroutine start still lands (the runner goroutine
	// removes it at terminal state).
	jobCtx, cancel := context.WithCancel(ctx)
	q.mu.Lock()
	q.cancels[rec.ID] = cancel
	q.mu.Unlock()
	q.wg.Add(1)
	go func() {
		defer q.wg.Done()
		defer q.unregister(rec.ID)
		q.runJob(jobCtx, rec.ID, typ, projectID, fn)
	}()
	return rec.ID, nil
}

// Cancel requests cancellation of an active async job. It reports whether
// the job was found among this process's active (queued or running) jobs;
// false also covers jobs that already finished (their handle is gone).
// Cancellation is cooperative: the runner observes context cancellation
// (ffmpeg children die with it) and the queue persists StatusCancelled on
// the way out, so the caller should poll the job row for the terminal state.
func (q *Queue) Cancel(id string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	cancel, ok := q.cancels[id]
	if !ok {
		return false
	}
	cancel()
	return true
}

func (q *Queue) unregister(id string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.cancels, id)
}

// runJob executes one recorded job to a terminal state (shared by inline and
// async paths). It returns the job's terminal error (nil on success) so the
// inline path can propagate it; async callers only persist it.
func (q *Queue) runJob(ctx context.Context, id, typ, projectID string, fn Runner) error {
	// Render jobs take the render-worker slot first so a queued render never
	// holds a generic slot while waiting for its class bound.
	if typ == TypeRender && q.renders != nil {
		select {
		case q.renders <- struct{}{}:
			defer func() { <-q.renders }()
		case <-ctx.Done():
			q.finishBeforeStart(ctx, id, "cancelled waiting for render slot")
			return xcerr.E(xcerr.CodeCancelled, "job cancelled before start", ctx.Err())
		}
	}

	// Acquire a concurrency slot (cancellation-aware).
	select {
	case q.slots <- struct{}{}:
		defer func() { <-q.slots }()
	case <-ctx.Done():
		q.finishBeforeStart(ctx, id, "cancelled before start")
		return xcerr.E(xcerr.CodeCancelled, "job cancelled before start", ctx.Err())
	}

	// The select above may legally pick the slot case even when Done is
	// already closed (both ready → runtime chooses freely). A job that
	// arrives here cancelled must still reach a terminal state — without
	// this guard it would die in SetJobRunning below and leave a phantom
	// queued row that blocks the project's exclusive-job slot until restart.
	if ctx.Err() != nil {
		q.finishBeforeStart(ctx, id, "cancelled before start")
		return xcerr.E(xcerr.CodeCancelled, "job cancelled before start", ctx.Err())
	}

	if err := q.db.SetJobRunning(ctx, id); err != nil {
		q.log.Error("job start bookkeeping failed", "job_id", id, "err", err)
		// Best-effort terminal write: without it the row stays queued
		// forever (blocking exclusive types), with no cancel handle left.
		fctx, fcancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer fcancel()
		if ferr := q.db.FinishJob(fctx, id, storage.StatusFailed, string(xcerr.CodeStorageFailure), "job start could not be recorded"); ferr != nil {
			q.log.Error("job failure bookkeeping failed", "job_id", id, "err", ferr)
		}
		return xcerr.E(xcerr.CodeStorageFailure, "cannot record job start", err)
	}
	q.log.Info("job started", "job_id", id, "type", typ, "project_id", projectID)

	var progressMu sync.Mutex
	lastWrite := time.Now()
	progress := func(p float64) {
		// Job bodies may report progress from multiple goroutines (parallel
		// per-asset analysis); the throttle state must be synchronized.
		progressMu.Lock()
		defer progressMu.Unlock()
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
	defer q.pruneHistory()

	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	switch {
	case runErr == nil:
		if err := q.db.FinishJob(cctx, id, storage.StatusSucceeded, "", ""); err != nil {
			q.log.Error("job finish bookkeeping failed", "job_id", id, "err", err)
		}
		q.log.Info("job succeeded", "job_id", id, "type", typ)
		return nil
	case ctx.Err() != nil:
		if err := q.db.FinishJob(cctx, id, storage.StatusCancelled, string(xcerr.CodeCancelled), "cancelled"); err != nil {
			// Same diagnosability rule as finishBeforeStart: a job stuck
			// "running" after its body returned must leave a trace.
			q.log.Error("job cancel bookkeeping failed", "job_id", id, "err", err)
		}
		return xcerr.E(xcerr.CodeCancelled, "job cancelled", runErr)
	default:
		code := xcerr.CodeOf(runErr)
		_ = q.db.FinishJob(cctx, id, storage.StatusFailed, string(code), xcerr.UserMessage(runErr))
		q.log.Error("job failed", "job_id", id, "type", typ, "error_code", code, "err", runErr)
		return runErr
	}
}

// pruneHistory trims terminal job rows to the configured cap (best-effort:
// retention is housekeeping and must never fail the job that triggered it).
func (q *Queue) pruneHistory() {
	if q.maxHistory <= 0 {
		return
	}
	cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	n, err := q.db.PruneJobHistory(cctx, q.maxHistory)
	if err != nil {
		q.log.Warn("job history prune failed", "err", err)
	} else if n > 0 {
		q.log.Info("pruned job history", "removed", n, "kept", q.maxHistory)
	}
}

// finishBeforeStart persists a "cancelled before the job body ran" outcome
// with its own fresh timeout (the caller's ctx is already done). Failure is
// logged, never silent: a row stuck in queued would keep blocking the
// project's exclusive-job slot with no visible cause.
func (q *Queue) finishBeforeStart(ctx context.Context, id, msg string) {
	cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := q.db.FinishJob(cctx, id, storage.StatusCancelled, string(xcerr.CodeCancelled), msg); err != nil {
		q.log.Error("job cancel bookkeeping failed", "job_id", id, "err", err)
	}
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
	// Startup also prunes: covers rows accumulated before this knob existed
	// or while history grew under a crashed writer.
	q.pruneHistory()
	return len(ids), nil
}
