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

// TestAnalyzeWithProxy: proxy_enabled routes analysis through a generated
// low-res proxy; the run must complete with segments and write the proxy
// into its own cache dir (fingerprint-keyed), leaving the original untouched.
func TestAnalyzeWithProxy(t *testing.T) {
	d, p := analyzeSetup(t, 1)
	d.Cfg.Resource.ProxyEnabled = true
	d.Cfg.Resource.AnalysisWidth = 160 // fixture is 320-wide → proxy decision fires

	asset, err := d.DB.ListAssets(context.Background(), p.ID)
	if err != nil || len(asset) != 1 {
		t.Fatalf("list assets: %v", err)
	}
	before, err := os.ReadFile(asset[0].Path)
	if err != nil {
		t.Fatal(err)
	}

	var got int
	err = d.AnalyzeProject(p, func(a AnalyzedAsset) { got++ })
	if err != nil {
		t.Fatal(err)
	}
	if got != 1 {
		t.Fatalf("analyzed %d assets, want 1", got)
	}

	proxies := d.proxyStore()
	entries, bytes, err := proxies.Usage()
	if err != nil {
		t.Fatal(err)
	}
	if entries != 1 || bytes <= 0 {
		t.Fatalf("proxy cache has %d entries (%d bytes), want 1", entries, bytes)
	}
	after, err := os.ReadFile(asset[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("original media was modified by proxy-backed analysis")
	}
}

// TestTimelineBackupAndRestore: a style regeneration backs up the previous
// document, and RestoreTimelineBackup swaps it back (twice — the swap is
// its own undo).
func TestTimelineBackupAndRestore(t *testing.T) {
	d, p := analyzeSetup(t, 1)

	if _, err := d.BuildTimeline(p, "generic_highlight"); err != nil {
		t.Fatal(err)
	}
	cur, err := d.TimelinePath(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate manual edits to the stored document.
	manual := []byte(`{"version":1,"manual":true}`)
	if err := os.WriteFile(cur, manual, 0o644); err != nil {
		t.Fatal(err)
	}

	// Regeneration must push the manual version into the backup.
	if _, err := d.BuildTimeline(p, "generic_highlight"); err != nil {
		t.Fatal(err)
	}
	bak, err := d.TimelineBackupPath(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(bak)
	if err != nil {
		t.Fatalf("backup missing after regeneration: %v", err)
	}
	if string(got) != string(manual) {
		t.Fatalf("backup holds %q, want the manual document", got)
	}

	// Restore swaps: current == manual again, backup == regenerated.
	ok, err := d.RestoreTimelineBackup(p)
	if err != nil || !ok {
		t.Fatalf("restore: ok=%v err=%v", ok, err)
	}
	got, err = os.ReadFile(cur)
	if err != nil || string(got) != string(manual) {
		t.Fatalf("restored current %q err=%v", got, err)
	}
	// And the swap is undoable.
	if ok, err := d.RestoreTimelineBackup(p); err != nil || !ok {
		t.Fatalf("second restore: ok=%v err=%v", ok, err)
	}
	got, err = os.ReadFile(cur)
	if err != nil || string(got) == string(manual) {
		t.Fatalf("second restore must bring back the regenerated doc, got %q", got)
	}
}
