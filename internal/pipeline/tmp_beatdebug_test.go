package pipeline

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"

	"github.com/xiabee/XCut/internal/analysis"
	"github.com/xiabee/XCut/internal/config"
	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/style"
	"github.com/xiabee/XCut/internal/testmedia"
	"github.com/xiabee/XCut/internal/workspace"
)

func TestTmpDebugBeatGrid(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("no ffmpeg")
	}
	dir := t.TempDir()
	path, err := testmedia.GenerateRally(dir, "clicks.mp4", 320, 240, 25, 14.3,
		[]testmedia.RallySpec{{Start: 0, End: 14, HitEvery: 0.5}})
	if err != nil {
		t.Fatal(err)
	}
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
	defer db.Close()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	d := NewDeps(context.Background(), db, ws, cfg, logger)
	p, err := db.CreateProject(context.Background(), "dbg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.ImportAsset(p, path); err != nil {
		t.Fatal(err)
	}
	if err := d.AnalyzeProject(p, nil); err != nil {
		t.Fatal(err)
	}
	assets, _ := db.ListAssets(context.Background(), p.ID)
	asset := assets[0]
	res, err := analysis.Run(context.Background(), analysis.NewStore(ws.CacheDir()),
		analysis.Options{Tools: media.Tools{FFmpeg: "ffmpeg", FFprobe: "ffprobe"}, SampleFPS: cfg.Resource.FrameSampleFPS, AnalysisWidth: cfg.Resource.AnalysisWidth},
		analysis.Baseline(), asset.Path, asset.Fingerprint, asset.DurationSec, asset.HasAudio, logger)
	if err != nil {
		t.Fatal(err)
	}
	preset, err := style.Load("badminton_highlight")
	if err != nil {
		t.Fatal(err)
	}
	preset.BeatSnapTolerance = 0.25
	beats := beatGridFor(logger, res, &asset, preset)
	fmt.Printf("BEATS(%d): %v\n", len(beats), beats)
	var near []float64
	for _, b := range beats {
		if b > 13.0 {
			near = append(near, b)
		}
	}
	fmt.Printf("NEAR-END: %v\n", near)
}
