// Package storage provides SQLite persistence: connection management (WAL,
// busy timeout, FK enforcement), a code-driven migration mechanism, and typed
// stores for projects, assets, and jobs.
//
// modernc.org/sqlite is used deliberately (pure Go, no CGO) so xcut
// cross-compiles to windows/amd64, linux/amd64, linux/arm64 without a C
// toolchain (DECISIONS D4).
package storage

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/xiabee/XCut/internal/xcerr"
)

// DB wraps sql.DB with SQLite-specific configuration.
type DB struct {
	*sql.DB
	path string
}

// dsn builds a modernc/sqlite URI with pragmas applied per connection.
func dsn(path string) string {
	p := filepath.ToSlash(path)
	// Percent-encode characters that would break URI parsing. Slashes stay.
	repl := strings.NewReplacer(
		"%", "%25",
		"?", "%3F",
		"#", "%23",
		" ", "%20",
	)
	p = repl.Replace(p)
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(10000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "synchronous(NORMAL)")
	return "file:" + p + "?" + q.Encode()
}

// Open opens (creating if needed) the database and applies pending migrations.
func Open(path string) (*DB, error) {
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, xcerr.E(xcerr.CodeStorageFailure, "cannot open database", err)
	}
	// SQLite serializes writes; a small pool is plenty for a single-user tool.
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(0)

	d := &DB{DB: db, path: path}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := d.PingContext(ctx); err != nil {
		db.Close()
		return nil, xcerr.E(xcerr.CodeStorageFailure, "database unreachable", err)
	}
	if err := d.Migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return d, nil
}

// Ping verifies connectivity.
func (d *DB) Ping() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return d.PingContext(ctx)
}

// migration is one schema version step.
type migration struct {
	id   int
	name string
	stmt string
}

// migrations append-only. Never edit an applied migration; add a new one.
var migrations = []migration{
	{id: 1, name: "init", stmt: `
CREATE TABLE projects (
	id          TEXT PRIMARY KEY,
	name        TEXT NOT NULL UNIQUE,
	created_at  INTEGER NOT NULL,
	updated_at  INTEGER NOT NULL
);

CREATE TABLE assets (
	id           TEXT PRIMARY KEY,
	project_id   TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	path         TEXT NOT NULL,
	filename     TEXT NOT NULL,
	fingerprint  TEXT NOT NULL,
	duration_s   REAL NOT NULL DEFAULT 0,
	width        INTEGER NOT NULL DEFAULT 0,
	height       INTEGER NOT NULL DEFAULT 0,
	fps          REAL NOT NULL DEFAULT 0,
	video_codec  TEXT NOT NULL DEFAULT '',
	audio_codec  TEXT NOT NULL DEFAULT '',
	has_audio    INTEGER NOT NULL DEFAULT 0,
	bitrate      INTEGER NOT NULL DEFAULT 0,
	size_bytes   INTEGER NOT NULL DEFAULT 0,
	probe_json   TEXT NOT NULL DEFAULT '',
	created_at   INTEGER NOT NULL,
	UNIQUE(project_id, path)
);

CREATE TABLE jobs (
	id             TEXT PRIMARY KEY,
	type           TEXT NOT NULL,
	project_id     TEXT REFERENCES projects(id) ON DELETE CASCADE,
	status         TEXT NOT NULL,
	progress       REAL NOT NULL DEFAULT 0,
	error_code     TEXT NOT NULL DEFAULT '',
	error_message  TEXT NOT NULL DEFAULT '',
	attempt        INTEGER NOT NULL DEFAULT 0,
	resource_class TEXT NOT NULL DEFAULT 'CPU_LIGHT',
	payload_json   TEXT NOT NULL DEFAULT '',
	created_at     INTEGER NOT NULL,
	started_at     INTEGER,
	finished_at    INTEGER
);

CREATE INDEX idx_assets_project ON assets(project_id);
CREATE INDEX idx_jobs_project   ON jobs(project_id);
CREATE INDEX idx_jobs_status    ON jobs(status);
`},
	// v2: at most one queued/running job per (project, exclusive type).
	// Two concurrent renders for one project would drive two ffmpeg encodes
	// into the same output scratch; this makes the guard race-proof at the
	// storage layer, not just a pre-check. Import is excluded — concurrent
	// imports of different files are legitimate. NULL project_id (global
	// jobs) is distinct in SQLite unique indexes, so globals are unaffected.
	{id: 2, name: "exclusive-active-jobs", stmt: `
CREATE UNIQUE INDEX idx_jobs_active_exclusive
ON jobs(project_id, type)
WHERE status IN ('queued', 'running')
  AND type IN ('analyze', 'timeline', 'render');
`},
	// v3: transcription joins the exclusive set. Two concurrent transcribes
	// write the same subtitles.{srt,ass} pair — interleaved runs could pair
	// one run's .srt with another's .ass (and the loser's stale-cleanup
	// could delete the winner's karaoke file). Append-only: v2's index
	// stays as applied; this replaces it with the wider type list.
	{id: 3, name: "exclusive-active-subtitles", stmt: `
DROP INDEX IF EXISTS idx_jobs_active_exclusive;
CREATE UNIQUE INDEX idx_jobs_active_exclusive
ON jobs(project_id, type)
WHERE status IN ('queued', 'running')
  AND type IN ('analyze', 'timeline', 'render', 'subtitles');
`},
	// v4: per-source motion ROI. A fixed camera per file means the court
	// sits at a different spot in each source; the per-preset rect cannot
	// express that. The rect is JSON (normalized 0..1) or '' when unset —
	// the per-preset motion_roi stays as the fallback for assets without
	// their own region.
	{id: 4, name: "asset-motion-roi", stmt: `
ALTER TABLE assets ADD COLUMN motion_roi TEXT NOT NULL DEFAULT '';
`},
	// v5: scoreboard marks. The core's own signals were measured and cannot
	// tell where a point ended (docs/EVAL.md), so the one source that can —
	// a burned-in scoreboard, read by the optional AI sidecar — is stored per
	// asset next to the crop it was read from. JSON or '' when unset; the
	// timeline then ends clips at a mark instead of running past the point.
	{id: 5, name: "asset-score-marks", stmt: `
ALTER TABLE assets ADD COLUMN score_marks TEXT NOT NULL DEFAULT '';
`},
	// v6: the scoreboard region, split from the marks it produced. The rect is
	// what a user (or the UI picker) asks for; the times are what the scan
	// measured. Keeping them apart lets the analyze stage see "crop set, never
	// scanned" and "scanned against an older crop" as different states, which
	// one coupled column cannot express.
	{id: 6, name: "asset-score-crop", stmt: `
ALTER TABLE assets ADD COLUMN score_crop TEXT NOT NULL DEFAULT '';
`},
}

// Migrate applies pending schema migrations. Each runs in a transaction and is
// recorded in schema_migrations; applied ones never re-run.
func (d *DB) Migrate(ctx context.Context) error {
	if _, err := d.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
	id         INTEGER PRIMARY KEY,
	name       TEXT NOT NULL,
	applied_at INTEGER NOT NULL
)`); err != nil {
		return xcerr.E(xcerr.CodeStorageFailure, "cannot create schema_migrations", err)
	}

	var current int
	if err := d.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM schema_migrations`).Scan(&current); err != nil {
		return xcerr.E(xcerr.CodeStorageFailure, "cannot read schema version", err)
	}

	for _, m := range migrations {
		if m.id <= current {
			continue
		}
		tx, err := d.BeginTx(ctx, nil)
		if err != nil {
			return xcerr.E(xcerr.CodeStorageFailure, fmt.Sprintf("migration %d: cannot begin", m.id), err)
		}
		if _, err := tx.ExecContext(ctx, m.stmt); err != nil {
			tx.Rollback()
			return xcerr.E(xcerr.CodeStorageFailure, fmt.Sprintf("migration %d (%s) failed", m.id, m.name), err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (id, name, applied_at) VALUES (?, ?, ?)`,
			m.id, m.name, time.Now().Unix()); err != nil {
			tx.Rollback()
			return xcerr.E(xcerr.CodeStorageFailure, fmt.Sprintf("migration %d: record failed", m.id), err)
		}
		if err := tx.Commit(); err != nil {
			return xcerr.E(xcerr.CodeStorageFailure, fmt.Sprintf("migration %d: commit failed", m.id), err)
		}
	}
	return nil
}
