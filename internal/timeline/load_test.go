package timeline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFileOK(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "timeline.json")
	b, err := json.Marshal(validTimeline())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != Version || len(got.Tracks) != 1 {
		t.Fatalf("unexpected load result: %+v", got)
	}
}

func TestLoadFileCorrupt(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "timeline.json")
	// Truncated / mutated JSON must be a clean validation error, never a panic.
	for _, body := range []string{
		`{"version": 1, "tracks": [`,
		`not json at all`,
		`{"version":1,"tracks":[{"id":"v1","kind":"video","clips":[{"id":123}]}]}`,
		strings.Repeat("\x00", 100),
	} {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadFile(p); err == nil {
			t.Fatalf("corrupt body accepted: %q", body)
		}
	}
}

func TestLoadFileTooLarge(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "timeline.json")
	if err := os.WriteFile(p, make([]byte, maxTimelineBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(p); err == nil {
		t.Fatal("oversized timeline accepted")
	}
}

func TestLoadFileMissing(t *testing.T) {
	if _, err := LoadFile(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("missing file should error")
	}
}
