package cli

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/config"
	"github.com/xiabee/XCut/internal/eval"
	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/testmedia"
	"github.com/xiabee/XCut/internal/workspace"
)

// TestEvalPlayerSpotMeasuresThroughTheProductionWrite: a case that seeds
// player_spot must leave the asset row carrying a measured color signature —
// the same rows the analyze pass builds for a UI-drawn spot — so
// min_player_presence styles become measurable on labeled footage. The case
// runs through evalRunCase against a workspace the test owns, because eval's
// own throwaway workspace is deleted before anyone could read its rows.
func TestEvalPlayerSpotMeasuresThroughTheProductionWrite(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	root := t.TempDir()
	if _, err := testmedia.GenerateRally(root, "match.mp4", 320, 240, 25, 14.3,
		[]testmedia.RallySpec{{Start: 0, End: 14, HitEvery: 0.5}}); err != nil {
		t.Fatalf("fixture: %v", err)
	}

	cfg := config.Default()
	cfg.Workspace = root
	if err := config.Resolve(cfg); err != nil {
		t.Fatal(err)
	}
	if err := workspace.New(root).Ensure(); err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(filepath.Join(root, "xcut.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ea := &App{
		Ctx:    context.Background(),
		Stdout: io.Discard,
		Stderr: io.Discard,
		Cfg:    cfg,
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	caseName := "spot_case"
	_, _, err = evalRunCase(ea, db, eval.Case{
		Name:       caseName,
		Media:      filepath.Join(root, "match.mp4"),
		Style:      "generic_highlight",
		Expected:   []eval.Range{{Start: 1, End: 5}},
		PlayerSpot: &eval.Spot{X: 0.3, Y: 0.3, W: 0.2, H: 0.2, At: 7},
	}, "generic_highlight", 0, 0)
	if err != nil {
		t.Fatalf("evalRunCase: %v", err)
	}

	p, err := db.GetProjectByName(context.Background(), "eval_"+caseName)
	if err != nil || p == nil {
		t.Fatalf("eval project missing: %+v (%v)", p, err)
	}
	assets, err := db.ListAssets(context.Background(), p.ID)
	if err != nil || len(assets) != 1 {
		t.Fatalf("ListAssets: %d assets (%v)", len(assets), err)
	}
	spot := assets[0].PlayerSpot
	if spot == nil {
		t.Fatal("the manifest's player_spot never reached the asset row")
	}
	if len(spot.Rect) != 4 || spot.Rect[0] != 0.3 || spot.Rect[2] != 0.2 || spot.At != 7 {
		t.Fatalf("spot rect/at = %v (at %.2f), want the case's values", spot.Rect, spot.At)
	}
	if len(spot.Bins) == 0 {
		t.Fatal("the spot is on the row but no signature was measured from it")
	}
	// The measure pass builds both models from the one decode: the band
	// model rides the same row (this square spot yields three usable bands),
	// and each band carries the full histogram geometry.
	if len(spot.Bands) != 3 {
		t.Fatalf("spot carries %d band models, want the 3 the multi-region measure builds", len(spot.Bands))
	}
	for i, b := range spot.Bands {
		if len(b) == 0 {
			t.Fatalf("band %d measured empty", i)
		}
	}
	if spot.SampledAt == 0 {
		t.Fatal("a measured signature must carry its sampled-at moment")
	}
}

// TestEvalPlayerSpotRefusesAnInvalidRect holds the row-write boundary: with
// real media, a spot the storage layer would reject is refused with the case
// named and nothing written — the same rule the manifest layer enforces at
// --check time, checked again where the write happens.
func TestEvalPlayerSpotRefusesAnInvalidRect(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	root := t.TempDir()
	if _, err := testmedia.GenerateRally(root, "match.mp4", 320, 240, 25, 14.3,
		[]testmedia.RallySpec{{Start: 0, End: 14, HitEvery: 0.5}}); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	cfg := config.Default()
	cfg.Workspace = root
	if err := config.Resolve(cfg); err != nil {
		t.Fatal(err)
	}
	if err := workspace.New(root).Ensure(); err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(filepath.Join(root, "xcut.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ea := &App{
		Ctx:    context.Background(),
		Stdout: io.Discard,
		Stderr: io.Discard,
		Cfg:    cfg,
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	_, _, err = evalRunCase(ea, db, eval.Case{
		Name:       "bad_spot",
		Media:      filepath.Join(root, "match.mp4"),
		Expected:   []eval.Range{{Start: 1, End: 5}},
		PlayerSpot: &eval.Spot{X: 0.9, Y: 0.9, W: 0.5, H: 0.5, At: 7},
	}, "generic_highlight", 0, 0)
	if err == nil || !strings.Contains(err.Error(), "bad_spot") ||
		!strings.Contains(err.Error(), "player_spot") {
		t.Fatalf("expected the named spot refusal, got %v", err)
	}
}
