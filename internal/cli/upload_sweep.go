package cli

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/xiabee/XCut/internal/workspace"
)

// sweepUploadStaging removes .upload-* staging files left in imports/ by a
// serve that died mid-upload. Land files (renamed, no prefix) and the
// per-project directories are untouched.
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
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), ".upload-") {
			if err := os.Remove(filepath.Join(root, e.Name())); err == nil {
				removed++
			}
		}
		continue
	}
	return removed, nil
}
