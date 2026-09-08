package pipeline

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/timeline"
	"github.com/xiabee/XCut/internal/xcerr"
)

// guardRenderOut refuses render outputs that would overwrite files the user
// cannot afford to lose: imported media are referenced in place (no copy —
// asset.Path IS the user's original), a stored timeline may reference paths
// beyond the asset table (manual PUT), and the timeline document itself is
// workspace state. Applies to every render entry point (CLI --out, API
// {"out": ...}, auto) so one choke point covers all of them.
func (d Deps) guardRenderOut(project *storage.Project, outPath string) error {
	protected := make([]string, 0, 8)
	assets, err := d.DB.ListAssets(d.Ctx, project.ID)
	if err != nil {
		return err
	}
	for i := range assets {
		protected = append(protected, assets[i].Path)
	}
	tlPath, err := d.TimelinePath(project.ID)
	if err != nil {
		return err
	}
	protected = append(protected, tlPath)
	if tl, err := timeline.LoadFile(tlPath); err == nil && tl != nil {
		for _, tr := range tl.Tracks {
			for _, c := range tr.Clips {
				if c.SourcePath != "" {
					protected = append(protected, c.SourcePath)
				}
			}
		}
	}
	// LoadFile NotFound (no timeline yet) is fine — nothing extra to protect.

	for _, p := range protected {
		if p == "" {
			continue
		}
		if sameFileOrPath(outPath, p) {
			return xcerr.E(xcerr.CodeValidation,
				"render output would overwrite a source file — pick a different --out path", nil)
		}
	}
	return nil
}

// sameFileOrPath reports whether a and b denote the same file. When both
// exist, the OS answers (same inode on unix, same file ID on Windows — this
// also sees through hardlinks and case differences); otherwise the fallback
// is a normalized string comparison (absolute, cleaned, case-folded on
// Windows where the filesystem is case-insensitive) so a not-yet-existing
// output cannot sneak through with different casing or separators.
func sameFileOrPath(a, b string) bool {
	if fa, err := os.Stat(a); err == nil {
		if fb, err := os.Stat(b); err == nil {
			return os.SameFile(fa, fb)
		}
	}
	return normalizePathForCompare(a) == normalizePathForCompare(b)
}

func normalizePathForCompare(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	abs = filepath.Clean(abs)
	if runtime.GOOS == "windows" {
		abs = strings.ToLower(abs)
	}
	return filepath.ToSlash(abs)
}
