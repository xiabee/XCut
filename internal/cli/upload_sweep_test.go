package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/xiabee/XCut/internal/workspace"
)

// TestSweepUploadStagingPerProject: upload staging lives at
// imports/<project>/.upload-* (upload.go stages inside the per-project
// directory), so a serve killed mid-upload strands multi-GiB debris there.
// The startup sweep must reclaim it — plus root-level debris from the old
// layout — while landing files and project directories stay untouched.
func TestSweepUploadStagingPerProject(t *testing.T) {
	root := t.TempDir()
	ws := workspace.New(root)
	imports := ws.ImportsDir()

	projA := filepath.Join(imports, "projA")
	projB := filepath.Join(imports, "projB")
	for _, d := range []string{projA, projB} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	seeds := map[string]string{
		filepath.Join(imports, ".upload-root"):  "old layout debris",
		filepath.Join(projA, ".upload-123456"):  "crashed staging",
		filepath.Join(projA, "landed.mp4"):      "a landed upload",
		filepath.Join(projB, "staging-not.mp4"): "landed, unfortunate name",
	}
	for p, content := range seeds {
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := sweepUploadStaging(ws)
	if err != nil {
		t.Fatalf("sweep failed: %v", err)
	}
	if removed != 2 {
		t.Fatalf("sweep removed %d files, want 2 (both staging files)", removed)
	}

	for _, p := range []string{filepath.Join(imports, ".upload-root"), filepath.Join(projA, ".upload-123456")} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("staging file %s must be swept", p)
		}
	}
	for _, p := range []string{filepath.Join(projA, "landed.mp4"), filepath.Join(projB, "staging-not.mp4")} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("landed file %s must survive the sweep: %v", p, err)
		}
	}
	// Unrelated project directories survive (their land files live there).
	if fi, err := os.Stat(projB); err != nil || !fi.IsDir() {
		t.Errorf("project dir projB must survive: %v", err)
	}
}
