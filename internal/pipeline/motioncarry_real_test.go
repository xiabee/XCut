package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/xiabee/XCut/internal/config"
	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/testmedia"
	"github.com/xiabee/XCut/internal/timeline"
	"github.com/xiabee/XCut/internal/workspace"
)

// TestHandMotionSurvivesRegeneration walks the real path: build a reel whose
// style frames nothing, pick motion on its lead clip the way the inspector's
// Apply does, publish that document (what a manual save does), regenerate the
// same style — and read the pick back off the new document. The builder is
// deterministic, so the regenerated windows are the ones the pick was drawn
// for; the carry is what stands between the user's pick and the bit bucket.
func TestHandMotionSurvivesRegeneration(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	dir := t.TempDir()
	path, err := testmedia.GenerateRally(dir, "match.mp4", 320, 240, 25, 14.3,
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
	t.Cleanup(func() { db.Close() })

	d := NewDeps(context.Background(), db, ws, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	p, err := db.CreateProject(context.Background(), "motioncarry")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.ImportAsset(p, path); err != nil {
		t.Fatal(err)
	}
	if err := d.AnalyzeProject(p, nil); err != nil {
		t.Fatal(err)
	}

	req := TimelineRequest{Style: "generic_highlight", Duration: 7.7}
	first, err := d.BuildTimeline(p, req)
	if err != nil {
		t.Fatal(err)
	}
	lead := &first.Tracks[0].Clips[0]
	if lead.Motion != nil {
		t.Fatalf("the style framed the lead clip itself (%+v); this fixture needs an unframed reel", lead.Motion)
	}
	lead.Motion = &timeline.Motion{Zoom: 1.5, From: []float64{0.4, 0.5}}
	if lead.Metadata == nil {
		lead.Metadata = map[string]string{}
	}
	lead.Metadata["framing"] = "roi"
	pickedAt := [2]float64{lead.SourceStart, lead.SourceEnd}

	b, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	docPath, err := d.TimelinePath(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(docPath, b); err != nil {
		t.Fatal(err)
	}

	second, err := d.BuildTimeline(p, req)
	if err != nil {
		t.Fatal(err)
	}
	var carried *timeline.Clip
	for i := range second.Tracks[0].Clips {
		c := &second.Tracks[0].Clips[i]
		if c.AssetID == first.Tracks[0].Clips[0].AssetID &&
			c.SourceStart == pickedAt[0] && c.SourceEnd == pickedAt[1] {
			carried = c
			break
		}
	}
	if carried == nil {
		t.Fatal("the regenerated reel has no clip on the picked window; the carry had nothing to reach")
	}
	if carried.Motion == nil || carried.Motion.Zoom != 1.5 {
		t.Fatalf("the hand pick did not survive the regeneration: motion=%+v", carried.Motion)
	}
	if carried.Metadata["framing"] != "roi" {
		t.Fatalf("framing claim = %q, want the pick's claim carried beside it", carried.Metadata["framing"])
	}
	// And the published document is the carried one, not just the return value:
	// the file on disk is what the next reader loads.
	raw, rerr := os.ReadFile(docPath)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if !bytes.Contains(raw, []byte(`"zoom": 1.5`)) {
		t.Fatal("the published document does not carry the pick; only the in-memory copy did")
	}
}
