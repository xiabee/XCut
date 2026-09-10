package storage

import (
	"context"
	"database/sql"
	"time"

	"github.com/xiabee/XCut/internal/xcerr"
)

// Job statuses (canonical lifecycle).
const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

// Job is a recorded unit of work (import, analyze, render, …).
type Job struct {
	ID            string  `json:"id"`
	Type          string  `json:"type"`
	ProjectID     string  `json:"project_id,omitempty"`
	Status        string  `json:"status"`
	Progress      float64 `json:"progress"`
	ErrorCode     string  `json:"error_code,omitempty"`
	ErrorMessage  string  `json:"error_message,omitempty"`
	Attempt       int     `json:"attempt"`
	ResourceClass string  `json:"resource_class"`
	PayloadJSON   string  `json:"payload_json,omitempty"`
	CreatedAt     int64   `json:"created_at"`
	StartedAt     *int64  `json:"started_at,omitempty"`
	FinishedAt    *int64  `json:"finished_at,omitempty"`
}

const jobCols = `id, type, project_id, status, progress, error_code, error_message,
attempt, resource_class, payload_json, created_at, started_at, finished_at`

func scanJob(row interface{ Scan(...any) error }) (*Job, error) {
	var j Job
	var projectID sql.NullString
	err := row.Scan(&j.ID, &j.Type, &projectID, &j.Status, &j.Progress,
		&j.ErrorCode, &j.ErrorMessage, &j.Attempt, &j.ResourceClass,
		&j.PayloadJSON, &j.CreatedAt, &j.StartedAt, &j.FinishedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	j.ProjectID = projectID.String
	return &j, nil
}

// CreateJob inserts a job row in queued state. An empty projectID is stored
// as NULL (global job, FK-safe). A duplicate active exclusive job (see
// migration v2) is reported as Conflict, not a storage failure.
func (d *DB) CreateJob(ctx context.Context, typ, projectID, resourceClass, payloadJSON string) (*Job, error) {
	j := &Job{
		ID:            NewID("job"),
		Type:          typ,
		ProjectID:     projectID,
		Status:        StatusQueued,
		ResourceClass: resourceClass,
		PayloadJSON:   payloadJSON,
		CreatedAt:     time.Now().Unix(),
	}
	var fkProject any
	if projectID != "" {
		fkProject = projectID
	}
	_, err := d.ExecContext(ctx, `
INSERT INTO jobs (id, type, project_id, status, progress, error_code, error_message,
	attempt, resource_class, payload_json, created_at, started_at, finished_at)
VALUES (?, ?, ?, ?, 0, '', '', 0, ?, ?, ?, NULL, NULL)`,
		j.ID, j.Type, fkProject, j.Status, j.ResourceClass, j.PayloadJSON, j.CreatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, xcerr.E(xcerr.CodeConflict,
				"an identical job is already queued or running for this project", err)
		}
		return nil, xcerr.E(xcerr.CodeStorageFailure, "cannot create job", err)
	}
	return j, nil
}

// FindActiveJob returns the newest queued/running job of the given type for
// the project (nil when none). Empty projectID matches global jobs.
func (d *DB) FindActiveJob(ctx context.Context, typ, projectID string) (*Job, error) {
	q := `SELECT ` + jobCols + ` FROM jobs WHERE type = ? AND status IN (?, ?)`
	args := []any{typ, StatusQueued, StatusRunning}
	if projectID != "" {
		q += ` AND project_id = ?`
		args = append(args, projectID)
	} else {
		q += ` AND project_id IS NULL`
	}
	q += ` ORDER BY created_at DESC, id DESC LIMIT 1`
	j, err := scanJob(d.QueryRowContext(ctx, q, args...))
	if err != nil {
		return nil, xcerr.E(xcerr.CodeStorageFailure, "cannot scan active job", err)
	}
	return j, nil
}

// HasActiveJobs reports whether the project has any queued/running job of
// any type. Deleting a project cascades its job rows, which would silently
// kill in-flight work (a running encode would only fail at publish time),
// so callers refuse deletion while this is true. Not atomic with
// DeleteProject — a job queued in the same instant can slip through; the
// guard covers the realistic delete-while-rendering case.
func (d *DB) HasActiveJobs(ctx context.Context, projectID string) (bool, error) {
	var n int
	err := d.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM jobs WHERE project_id = ? AND status IN (?, ?)`,
		projectID, StatusQueued, StatusRunning).Scan(&n)
	if err != nil {
		return false, xcerr.E(xcerr.CodeStorageFailure, "cannot scan active jobs", err)
	}
	return n > 0, nil
}

// SetJobRunning marks a job running.
func (d *DB) SetJobRunning(ctx context.Context, id string) error {
	now := time.Now().Unix()
	_, err := d.ExecContext(ctx,
		`UPDATE jobs SET status = ?, started_at = ?, attempt = attempt + 1 WHERE id = ?`,
		StatusRunning, now, id)
	if err != nil {
		return xcerr.E(xcerr.CodeStorageFailure, "cannot update job", err)
	}
	return nil
}

// SetJobProgress stores job progress in [0,1].
func (d *DB) SetJobProgress(ctx context.Context, id string, progress float64) error {
	if progress < 0 {
		progress = 0
	}
	if progress > 1 {
		progress = 1
	}
	_, err := d.ExecContext(ctx, `UPDATE jobs SET progress = ? WHERE id = ?`, progress, id)
	if err != nil {
		return xcerr.E(xcerr.CodeStorageFailure, "cannot update job progress", err)
	}
	return nil
}

// FinishJob records a terminal state.
func (d *DB) FinishJob(ctx context.Context, id, status, errCode, errMsg string) error {
	if status != StatusSucceeded && status != StatusFailed && status != StatusCancelled {
		return xcerr.E(xcerr.CodeValidation, "invalid terminal job status: "+status, nil)
	}
	now := time.Now().Unix()
	_, err := d.ExecContext(ctx,
		`UPDATE jobs SET status = ?, error_code = ?, error_message = ?, finished_at = ?,
		progress = CASE WHEN ? = ? THEN 1 ELSE progress END WHERE id = ?`,
		status, errCode, errMsg, now, status, StatusSucceeded, id)
	if err != nil {
		return xcerr.E(xcerr.CodeStorageFailure, "cannot finish job", err)
	}
	return nil
}

// ReconcileStale fails queued/running jobs older than the cutoff. Called at
// startup: a CLI process that died leaves rows behind that will never run.
// olderThan <= 0 disables the age gate and sweeps every queued/running row —
// only safe for a caller that provably outlives all previous writers (serve
// holds the exclusive workspace lock, so any row it sees at startup was left
// by a dead process). Returns the reconciled job ids.
func (d *DB) ReconcileStale(ctx context.Context, olderThan time.Duration) ([]string, error) {
	q := `SELECT id FROM jobs WHERE status IN (?, ?)`
	args := []any{StatusQueued, StatusRunning}
	if olderThan > 0 {
		q += ` AND created_at <= ?`
		args = append(args, time.Now().Add(-olderThan).Unix())
	}
	rows, err := d.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, xcerr.E(xcerr.CodeStorageFailure, "cannot scan stale jobs", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, xcerr.E(xcerr.CodeStorageFailure, "cannot scan stale jobs", err)
	}
	for _, id := range ids {
		if err := d.FinishJob(ctx, id, StatusFailed, "orphaned",
			"job left unfinished by a previous process; marked failed on startup"); err != nil {
			return ids, err
		}
	}
	return ids, nil
}

// PruneJobHistory keeps at most keep terminal jobs (succeeded/failed/
// cancelled, newest first by finished_at) and deletes older ones. Queued/
// running rows are never touched. Returns the number of rows removed.
// The jobs table is the one growth axis with no natural bound — every
// import/analyze/timeline/render adds a row — so the queue prunes it as
// jobs finish (resource.max history knob, jobs.max_history).
func (d *DB) PruneJobHistory(ctx context.Context, keep int) (int64, error) {
	if keep <= 0 {
		return 0, nil
	}
	res, err := d.ExecContext(ctx, `
DELETE FROM jobs WHERE status IN (?, ?, ?) AND id NOT IN (
	SELECT id FROM jobs WHERE status IN (?, ?, ?)
	ORDER BY finished_at DESC, created_at DESC, id DESC LIMIT ?
)`,
		StatusSucceeded, StatusFailed, StatusCancelled,
		StatusSucceeded, StatusFailed, StatusCancelled, keep)
	if err != nil {
		return 0, xcerr.E(xcerr.CodeStorageFailure, "cannot prune job history", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// GetJob by id; nil when missing.
func (d *DB) GetJob(ctx context.Context, id string) (*Job, error) {
	j, err := scanJob(d.QueryRowContext(ctx, `SELECT `+jobCols+` FROM jobs WHERE id = ?`, id))
	if err != nil {
		return nil, xcerr.E(xcerr.CodeStorageFailure, "cannot read job", err)
	}
	return j, nil
}

// ListJobs of a project (or all when projectID is empty), newest first.
func (d *DB) ListJobs(ctx context.Context, projectID string) ([]Job, error) {
	q := `SELECT ` + jobCols + ` FROM jobs`
	args := []any{}
	if projectID != "" {
		q += ` WHERE project_id = ?`
		args = append(args, projectID)
	}
	q += ` ORDER BY created_at DESC, id DESC`
	rows, err := d.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, xcerr.E(xcerr.CodeStorageFailure, "cannot list jobs", err)
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, xcerr.E(xcerr.CodeStorageFailure, "cannot read job row", err)
		}
		out = append(out, *j)
	}
	return out, rows.Err()
}
