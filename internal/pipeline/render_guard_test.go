package pipeline

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/config"
	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/timeline"
	"github.com/xiabee/XCut/internal/workspace"
	"github.com/xiabee/XCut/internal/xcerr"
)

// guardSetup builds a Deps with a real workspace/DB, one project, and one
// registered asset whose media file really exists on disk (the guard stats
// both sides when present). No ffmpeg needed — asset rows are inserted
// directly.
func guardSetup(t *testing.T) (Deps, *storage.Project, string) {
	t.Helper()
	root := t.TempDir()
	cfg := config.Default()
	cfg.Workspace = root
	if err := config.Resolve(cfg); err != nil {
		t.Fatal(err)
	}
	ws := workspace.New(root)
	if err := ws.Ensure(); err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(ws.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	d := NewDeps(context.Background(), db, ws, cfg, logger)

	p, err := db.CreateProject(context.Background(), "guard")
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(root, "user-original.mp4")
	if err := os.WriteFile(src, []byte("ORIGINAL MEDIA BYTES"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = db.UpsertAsset(context.Background(), &storage.Asset{
		ID: "a1", ProjectID: p.ID, Path: src, Filename: "user-original.mp4",
		DurationSec: 4, Fingerprint: "fp-guard",
	})
	if err != nil {
		t.Fatal(err)
	}
	return d, p, src
}

// TestGuardRenderOutRejectsSource: --out equal to an imported asset's file
// must be refused with a validation error, in every spelling of the path
// (exact, different case on Windows, through-separator, relative).
func TestGuardRenderOutRejectsSource(t *testing.T) {
	d, p, src := guardSetup(t)

	abs, err := filepath.Abs(src)
	if err != nil {
		t.Fatal(err)
	}
	variants := []string{abs}
	if runtime.GOOS == "windows" {
		variants = append(variants, strings.ToUpper(abs))
	}
	variants = append(variants, filepath.Join(filepath.Dir(abs), ".", "user-original.mp4"))
	// Relative spelling resolved from the workspace root.
	wd, err := os.Getwd()
	if err == nil {
		if rel, rerr := filepath.Rel(wd, abs); rerr == nil {
			variants = append(variants, rel)
		}
	}

	for _, out := range variants {
		err := d.guardRenderOut(p, out)
		if err == nil {
			t.Fatalf("out %q must be refused", out)
		}
		if !xcerr.IsCode(err, xcerr.CodeValidation) {
			t.Fatalf("out %q: want validation code, got %v", out, err)
		}
	}
}

// TestGuardRenderOutRejectsTimelineDoc: the project's timeline.json is
// workspace state, never a valid render target.
func TestGuardRenderOutRejectsTimelineDoc(t *testing.T) {
	d, p, _ := guardSetup(t)
	tlPath := filepath.Join(d.WS.Root, "projects", p.ID, "timeline.json")
	if err := d.guardRenderOut(p, tlPath); err == nil {
		t.Fatal("render onto timeline.json must be refused")
	}
}

// TestGuardRenderOutRejectsClipSourcePath: a manually PUT timeline can carry
// source paths beyond the asset table; those are protected too.
func TestGuardRenderOutRejectsClipSourcePath(t *testing.T) {
	d, p, _ := guardSetup(t)
	extra := filepath.Join(t.TempDir(), "manual-edit-source.mp4")
	tl := &timeline.Timeline{
		Version: timeline.Version,
		Canvas:  timeline.Canvas{Width: 320, Height: 240, FPS: 10},
		Tracks: []timeline.Track{{ID: "v1", Kind: "video", Clips: []timeline.Clip{{
			ID: "c1", AssetID: "a1", SourcePath: extra,
			SourceStart: 0, SourceEnd: 1, Speed: 1, Volume: 1,
		}}}},
	}
	tlPath := filepath.Join(d.WS.Root, "projects", p.ID, "timeline.json")
	if err := os.MkdirAll(filepath.Dir(tlPath), 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(tl)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tlPath, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := d.guardRenderOut(p, extra); err == nil {
		t.Fatal("render onto a clip source path from the stored timeline must be refused")
	}
}

// TestGuardRenderOutAllowsFreshPath: the default workspace render path and
// an unrelated outside path stay allowed; the untouched source file keeps
// its exact bytes in all refused cases.
func TestGuardRenderOutAllowsFreshPath(t *testing.T) {
	d, p, src := guardSetup(t)
	before, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}

	def, err := d.DefaultRenderPath(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.guardRenderOut(p, def); err != nil {
		t.Fatalf("default render path must stay allowed: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "elsewhere", "out.mp4")
	if err := d.guardRenderOut(p, outside); err != nil {
		t.Fatalf("unrelated output path must stay allowed: %v", err)
	}

	after, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("source file was modified by a refused render guard check")
	}
}

// TestSameFileOrPathExists: when both files exist, os.SameFile decides —
// a hardlink/alias of the source is caught even under a different name.
func TestSameFileOrPathExists(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.mp4")
	if err := os.WriteFile(a, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := filepath.Join(dir, "alias.mp4")
	if err := os.Link(a, b); err != nil {
		t.Skip("hardlinks not supported on this filesystem")
	}
	if !sameFileOrPath(a, b) {
		t.Fatal("hardlinked paths must compare equal")
	}
	if sameFileOrPath(a, filepath.Join(dir, "other.mp4")) {
		t.Fatal("distinct files must not compare equal")
	}
}
