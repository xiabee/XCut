package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/xiabee/XCut/internal/xcerr"
)

// NewID returns a prefixed random identifier, e.g. "prj_ab12cd34...". Random
// IDs avoid enumeration and collide with probability ~2^-64 per 8 bytes.
func NewID(prefix string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Fall back to time-based entropy; never fail on ID generation.
		return fmt.Sprintf("%s_%x%d", prefix, time.Now().UnixNano(), time.Now().Nanosecond())
	}
	return prefix + "_" + hex.EncodeToString(b)
}

// Project is a user workspace unit grouping assets, analyses, and timelines.
type Project struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

func scanProject(row interface{ Scan(...any) error }) (*Project, error) {
	var p Project
	err := row.Scan(&p.ID, &p.Name, &p.CreatedAt, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

const projectCols = `id, name, created_at, updated_at`

// CreateProject inserts a new project. Names are unique per workspace.
func (d *DB) CreateProject(ctx context.Context, name string) (*Project, error) {
	now := time.Now().Unix()
	p := &Project{ID: NewID("prj"), Name: name, CreatedAt: now, UpdatedAt: now}
	_, err := d.ExecContext(ctx,
		`INSERT INTO projects (id, name, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		p.ID, p.Name, p.CreatedAt, p.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, xcerr.E(xcerr.CodeValidation, "project name already exists: "+name, err)
		}
		return nil, xcerr.E(xcerr.CodeStorageFailure, "cannot create project", err)
	}
	return p, nil
}

// GetProject by id; nil when missing.
func (d *DB) GetProject(ctx context.Context, id string) (*Project, error) {
	p, err := scanProject(d.QueryRowContext(ctx,
		`SELECT `+projectCols+` FROM projects WHERE id = ?`, id))
	if err != nil {
		return nil, xcerr.E(xcerr.CodeStorageFailure, "cannot read project", err)
	}
	return p, nil
}

// GetProjectByName; nil when missing.
func (d *DB) GetProjectByName(ctx context.Context, name string) (*Project, error) {
	p, err := scanProject(d.QueryRowContext(ctx,
		`SELECT `+projectCols+` FROM projects WHERE name = ?`, name))
	if err != nil {
		return nil, xcerr.E(xcerr.CodeStorageFailure, "cannot read project", err)
	}
	return p, nil
}

// ListProjects ordered by creation.
func (d *DB) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT `+projectCols+` FROM projects ORDER BY created_at, id`)
	if err != nil {
		return nil, xcerr.E(xcerr.CodeStorageFailure, "cannot list projects", err)
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, xcerr.E(xcerr.CodeStorageFailure, "cannot read project row", err)
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// TouchProject bumps updated_at.
func (d *DB) TouchProject(ctx context.Context, id string) error {
	_, err := d.ExecContext(ctx, `UPDATE projects SET updated_at = ? WHERE id = ?`, time.Now().Unix(), id)
	if err != nil {
		return xcerr.E(xcerr.CodeStorageFailure, "cannot update project", err)
	}
	return nil
}

// DeleteProject removes the project row (assets/jobs cascade) only when no
// queued or running job references it. The gate and the delete are ONE
// statement: a check-then-act pair here would let a trigger enqueue a job
// between them and have that job's row cascade-deleted under a live runner
// — the runner would then work against a deleted project. Returns the rows
// deleted; 0 means the project is missing (caller distinguishes 404) or
// still busy (caller reports 409).
func (d *DB) DeleteProject(ctx context.Context, id string) (int64, error) {
	res, err := d.ExecContext(ctx, `DELETE FROM projects WHERE id = ? AND NOT EXISTS (
		SELECT 1 FROM jobs
		WHERE jobs.project_id = projects.id AND jobs.status IN (?, ?))`,
		id, StatusQueued, StatusRunning)
	if err != nil {
		return 0, xcerr.E(xcerr.CodeStorageFailure, "cannot delete project", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, xcerr.E(xcerr.CodeStorageFailure, "cannot confirm project deletion", err)
	}
	return n, nil
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	// modernc/sqlite surfaces SQLite extended result codes in the message.
	s := err.Error()
	return contains(s, "UNIQUE constraint failed") || contains(s, "2067") || contains(s, "1555")
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
