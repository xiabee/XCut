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
