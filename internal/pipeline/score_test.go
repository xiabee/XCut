package pipeline

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/config"
	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/testmedia"
	"github.com/xiabee/XCut/internal/workspace"
	"github.com/xiabee/XCut/internal/xcerr"
)

// TestNeedsScoreScan is the whole staleness rule, and the rule is the feature:
// measure when asked, and re-measure when the region moved.
func TestNeedsScoreScan(t *testing.T) {
	crop := []float64{0.4, 0.7, 0.2, 0.15}
	other := []float64{0.1, 0.1, 0.2, 0.2}
	for _, tc := range []struct {
		name  string
		asset storage.Asset
		want  bool
	}{
		{"no region, no marks", storage.Asset{}, false},
		{"marks without a region are not a request", storage.Asset{
			ScoreMarks: &storage.ScoreMarks{Crop: crop, Times: []float64{4}},
		}, false},
		{"region, never scanned", storage.Asset{ScoreCrop: crop}, true},
		{"region, scanned against itself", storage.Asset{ScoreCrop: crop,
			ScoreMarks: &storage.ScoreMarks{Crop: crop, Times: []float64{4}}}, false},
		{"region moved since the scan", storage.Asset{ScoreCrop: crop,
			ScoreMarks: &storage.ScoreMarks{Crop: other, Times: []float64{4}}}, true},
		{"region, scanned and found nothing", storage.Asset{ScoreCrop: crop,
			ScoreMarks: &storage.ScoreMarks{Crop: crop}}, false},
	} {
		if got := needsScoreScan(&tc.asset); got != tc.want {
			t.Errorf("%s: needsScoreScan = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func bareDeps(t *testing.T) (Deps, *storage.Project) {
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
	d := NewDeps(context.Background(), db, ws, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	p, err := db.CreateProject(context.Background(), "score")
	if err != nil {
		t.Fatal(err)
	}
	return d, p
}

// TestScoreScanRefusesWithoutSidecar: a region is a request for a measurement.
// Building the reel as if nobody had asked — or building it silently without
// boundaries — is the failure D13 rules out, so the refusal names the remedy.
// Runs on any host: the lookup is forced empty by PATH, and the refusal happens
// before a single media byte is touched.
func TestScoreScanRefusesWithoutSidecar(t *testing.T) {
	d, _ := bareDeps(t)
	t.Setenv("PATH", string(os.PathListSeparator))
	d.Cfg.Workers.AIBin = ""

	err := d.scanScoreMarks(context.Background(), []storage.Asset{
		{ID: "asst_1", ScoreCrop: []float64{0.4, 0.7, 0.2, 0.15}},
	})
	if !xcerr.IsCode(err, xcerr.CodeNotFound) {
		t.Fatalf("err = %v, want not_found", err)
	}
	msg := xcerr.UserMessage(err)
	for _, want := range []string{"scoreboard region", "workers.ai_bin", "score_changes"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal %q does not mention %q", msg, want)
		}
	}
}

// TestAnalyzeMeasuresScoreboardRegion is the web picker's path end to end: a
// region set on the asset row (all the picker can write), then one analyze run,
// and the marks sit on that row with the times the fixture was built from.
// Skipped without python or ffmpeg — and the gate names that skip rather than
// hiding it inside a package-level "ok".
func TestAnalyzeMeasuresScoreboardRegion(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		if _, err := exec.LookPath("python"); err != nil {
			t.Skip("no python interpreter on PATH")
		}
	}
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Skip("cannot locate repo root")
	}
	script := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(thisFile))),
		"scripts", "xcut-ai-sidecar.py")
	if _, err := os.Stat(script); err != nil {
		t.Skipf("reference sidecar missing: %v", err)
	}

	root := t.TempDir()
	d, p := bareDeps(t)
	d.Cfg.Workers.AIBin = script
	ctx := context.Background()

	mediaPath, err := testmedia.GenerateScoreboard(root, "board.mp4", 320, 240, 10, 16, []float64{4, 12})
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	asset, err := d.ImportAsset(p, mediaPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.DB.SetAssetScoreCrop(ctx, asset.ID, []float64{0, 0, 0.4, 0.3}); err != nil {
		t.Fatal(err)
	}
	if err := d.AnalyzeProject(p, nil); err != nil {
		t.Fatalf("analyze: %v", err)
	}
	got, err := d.DB.GetAsset(ctx, asset.ID)
	if err != nil || got == nil {
		t.Fatalf("GetAsset: %+v, %v", got, err)
	}
	if got.ScoreMarks == nil {
		t.Fatal("analyze stored no marks for a region that was set")
	}
	if len(got.ScoreMarks.Times) != 2 {
		t.Fatalf("want the fixture's two corner changes, got %v", got.ScoreMarks.Times)
	}
	for i, want := range []float64{4, 12} {
		if diff := got.ScoreMarks.Times[i] - want; diff < -0.75 || diff > 0.75 {
			t.Errorf("mark %d = %.2f, want within 0.75s of %.1f", i, got.ScoreMarks.Times[i], want)
		}
	}
}
