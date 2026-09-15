package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRemoveProjectDirs: both artifact directories die with the project,
// siblings survive, path-traversal ids are refused outright, and a second
// call is a no-op (idempotent cleanup).
func TestRemoveProjectDirs(t *testing.T) {
	w := New(t.TempDir())
	if err := w.Ensure(); err != nil {
		t.Fatal(err)
	}
	id := "prj_abc123"

	// Seed both artifact trees plus an unrelated sibling project.
	artifacts := []string{
		filepath.Join(DirProjects, id, "timeline.json"),
		filepath.Join(DirProjects, id, "render.mp4"),
		filepath.Join(DirImports, id, "clip.mp4"),
		filepath.Join(DirProjects, "prj_sibling", "timeline.json"),
	}
	for _, rel := range artifacts {
		full := filepath.Join(w.Root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := w.RemoveProjectDirs(id); err != nil {
		t.Fatal(err)
	}
	for _, rel := range artifacts[:3] {
		if _, err := os.Stat(filepath.Join(w.Root, rel)); !os.IsNotExist(err) {
			t.Fatalf("%s should be gone", rel)
		}
	}
	if _, err := os.Stat(filepath.Join(w.Root, artifacts[3])); err != nil {
		t.Fatalf("sibling project must survive: %v", err)
	}

	// Idempotent.
	if err := w.RemoveProjectDirs(id); err != nil {
		t.Fatalf("second remove: %v", err)
	}

	// Traversal-shaped ids are refused before touching the filesystem.
	for _, bad := range []string{"../escape", `a\b`, "", ".", "prj/../x"} {
		if err := w.RemoveProjectDirs(bad); err == nil {
			t.Fatalf("id %q must be refused", bad)
		}
	}
	// The sibling still survives every refused call.
	if _, err := os.Stat(filepath.Join(w.Root, artifacts[3])); err != nil {
		t.Fatalf("sibling lost after refused ids: %v", err)
	}
}
