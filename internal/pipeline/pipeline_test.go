package pipeline

import (
	"context"
	"io"
	"log/slog"
	"os"

	"path/filepath"
	"sync"
	"testing"

	"github.com/xiabee/XCut/internal/config"
	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/testmedia"
	"github.com/xiabee/XCut/internal/workspace"
)

// analyzeSetup builds a Deps with a real workspace/DB and imports n fixtures.
func analyzeSetup(t *testing.T, n int) (Deps, *storage.Project) {
	t.Helper()
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	root := t.TempDir()
	cfg := config.Default()
	cfg.Workspace = root
	cfg.Resource.MaxAnalysisWorkers = 4
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

	p, err := db.CreateProject(context.Background(), "multi")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		dir := t.TempDir() // auto-cleaned after the test; analysis reads later
		path, err := testmedia.Generate(dir, "fx.mp4", testmedia.DefaultFixture(), 320, 240, 10)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.ImportAsset(p, path); err != nil {
			t.Fatal(err)
		}
	}
	return d, p
}

func TestAnalyzeProjectParallelMultiAsset(t *testing.T) {
	d, p := analyzeSetup(t, 3)

	var mu sync.Mutex
	seen := map[string]bool{}
	err := d.AnalyzeProject(p, func(res AnalyzedAsset) {
		mu.Lock()
		seen[res.Asset.ID] = true
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 3 {
		t.Fatalf("callback fired for %d assets, want 3", len(seen))
	}

	// Timeline build over all three assets must succeed and stay valid.
	tl, err := d.BuildTimeline(p, "generic_highlight")
	if err != nil {
		t.Fatal(err)
	}
	if len(tl.Tracks[0].Clips) == 0 {
		t.Fatal("no clips produced")
	}
}

func TestAnalyzeProjectUnknownAssetID(t *testing.T) {
	d, p := analyzeSetup(t, 1)
	err := d.AnalyzeProject(p, nil, "asst_nope")
	if err == nil {
		t.Fatal("expected error for unknown asset filter")
	}
}

func TestWriteAtomicRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "f.json")
	if err := WriteAtomic(p, []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil || string(b) != `{"a":1}` {
		t.Fatalf("read back: %s %v", b, err)
	}
	// Overwrite works.
	if err := WriteAtomic(p, []byte(`{"a":2}`)); err != nil {
		t.Fatal(err)
	}
}
