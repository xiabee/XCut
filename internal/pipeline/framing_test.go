package pipeline

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/xiabee/XCut/internal/config"
	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/style"
	"github.com/xiabee/XCut/internal/testmedia"
	"github.com/xiabee/XCut/internal/workspace"
)

// The style package proves what a framing mode means. This proves the other half:
// the region a project actually drew reaches the plan, through storage, the
// workspace style file and the timeline fan-out — because a policy that only works
// when the test hands it a struct has never met a user's court.
func TestFramingPlanUsesTheStoredRegion(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	// The default fixture (distinct colour scenes) is what generic_highlight
	// segments; a still canvas with one moving patch gives it no cuts to choose.
	src, err := testmedia.Generate(t.TempDir(), "fx.mp4", testmedia.DefaultFixture(), 320, 240, 10)
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
	t.Cleanup(func() { db.Close() })

	ctx := context.Background()
	d := NewDeps(ctx, db, ws, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	p, err := db.CreateProject(ctx, "framing")
	if err != nil {
		t.Fatal(err)
	}
	asset, err := d.ImportAsset(p, src)
	if err != nil {
		t.Fatal(err)
	}
	const cx, cy = 0.55, 0.45 // the center of the region written below
	if err := db.SetAssetROI(ctx, asset.ID, &storage.MotionROI{X: 0.3, Y: 0.2, W: 0.5, H: 0.5}); err != nil {
		t.Fatal(err)
	}

	// A workspace copy of the style is how a user turns motion on today (the same
	// path the style editor writes), so this also proves the knob survives JSON.
	preset, err := style.Load("generic_highlight")
	if err != nil {
		t.Fatal(err)
	}
	preset.CameraMotion = style.CameraMotion{Mode: style.FramingROI, Zoom: 0.6}
	raw, err := json.Marshal(preset)
	if err != nil {
		t.Fatal(err)
	}
	stylesDir := filepath.Join(ws.Root, "styles")
	if err := os.MkdirAll(stylesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stylesDir, "generic_highlight.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := d.AnalyzeProject(p, nil); err != nil {
		t.Fatal(err)
	}
	tl, err := d.BuildTimeline(p, Style("generic_highlight"))
	if err != nil {
		t.Fatal(err)
	}
	framed := 0
	for _, tr := range tl.Tracks {
		for _, c := range tr.Clips {
			if c.Motion == nil {
				continue
			}
			framed++
			if math.Abs(c.Motion.From[0]-cx) > 1e-9 || math.Abs(c.Motion.From[1]-cy) > 1e-9 {
				t.Fatalf("clip %s framed at %v, want the drawn region's center (%g,%g)",
					c.ID, c.Motion.From, cx, cy)
			}
			if c.Motion.Zoom != 0.6 {
				t.Fatalf("clip %s zoom %v, want the style's 0.6", c.ID, c.Motion.Zoom)
			}
			if c.Metadata["framing"] != style.FramingROI {
				t.Fatalf("clip %s metadata framing = %q", c.ID, c.Metadata["framing"])
			}
		}
	}
	if framed == 0 {
		t.Fatal("no clip carries a plan: the region, the style file or the wiring is not reaching selection")
	}
	t.Logf("%d clips framed on the drawn region's center", framed)
}
