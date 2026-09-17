package cli

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/xiabee/XCut/internal/workspace"
)

// sweepUploadStaging removes .upload-* staging files left by a serve that
// died mid-upload. Staging lives at imports/<project>/.upload-* (upload.go
// stages inside the per-project directory); the imports root is swept too
// for debris from the pre-project-scoped layout. Land files (no prefix) and
// the per-project directories are untouched.
func sweepUploadStaging(ws *workspace.Workspace) (int, error) {
	root := ws.ImportsDir()
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	removed := 0
	sweepDir := func(dir string, entries []os.DirEntry) {
		for _, e := range entries {
			if !e.IsDir() && strings.HasPrefix(e.Name(), ".upload-") {
				if os.Remove(filepath.Join(dir, e.Name())) == nil {
					removed++
				}
			}
		}
	}
	for _, e := range entries {
		if e.IsDir() {
			// Per-project directory: sweep its staging files. An unreadable
			// entry holds nothing we can name, let alone remove — skip it.
			if sub, err := os.ReadDir(filepath.Join(root, e.Name())); err == nil {
				sweepDir(filepath.Join(root, e.Name()), sub)
			}
			continue
		}
		if strings.HasPrefix(e.Name(), ".upload-") {
			if os.Remove(filepath.Join(root, e.Name())) == nil {
				removed++
			}
		}
	}
	return removed, nil
}
