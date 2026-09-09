package storage

import (
	"path/filepath"
	"testing"
)

func TestOpenMigratesIdempotently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")

	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var v int
	if err := db.QueryRow(`SELECT COALESCE(MAX(id),0) FROM schema_migrations`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != len(migrations) {
		t.Fatalf("schema version = %d, want %d", v, len(migrations))
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen: no errors, no duplicate application.
	db2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	if err := db2.QueryRow(`SELECT COALESCE(MAX(id),0) FROM schema_migrations`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != len(migrations) {
		t.Fatalf("schema version after reopen = %d, want %d", v, len(migrations))
	}

	// Required tables exist.
	for _, table := range []string{"projects", "assets", "jobs"} {
		row := db2.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table)
		var name string
		if err := row.Scan(&name); err != nil {
			t.Fatalf("table %s missing: %v", table, err)
		}
	}
}

func TestForeignKeysEnforced(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// assets references projects; inserting with a bogus project id must fail.
	_, err = db.Exec(`INSERT INTO assets (id, project_id, path, filename, fingerprint, created_at)
	                  VALUES ('a1', 'nope', 'p', 'f', 'fp', 0)`)
	if err == nil {
		t.Fatal("expected FK violation")
	}
}
