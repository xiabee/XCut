package pipeline

import (
	"context"
	"io"
	"log/slog"
	"math"
	"strconv"
	"testing"

	"github.com/xiabee/XCut/internal/config"
	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/testmedia"
	"github.com/xiabee/XCut/internal/timeline"
	"github.com/xiabee/XCut/internal/workspace"
)

// TestDocumentStatesTheGridItWasCutAgainst follows the real path — fixture,
// import, analyze, build — and reads the stamp the build left on the document:
// the tempo and phase timeline.BeatTicks maps for the ruler. Snapping on, the
// document states the file's own grid; snapping off, no grid served the cut and
// the document must state neither key — BeatTicks renders that as "no ticks",
// not as ticks at an invented phase.
func TestDocumentStatesTheGridItWasCutAgainst(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	const period = 0.5
	dir := t.TempDir()
	path, err := testmedia.GenerateRally(dir, "clicks.mp4", 320, 240, 25, 14.3,
		[]testmedia.RallySpec{{Start: 0, End: 14, HitEvery: period}})
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
	p, err := db.CreateProject(context.Background(), "beatstamp")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.ImportAsset(p, path); err != nil {
		t.Fatal(err)
	}
	if err := d.AnalyzeProject(p, nil); err != nil {
		t.Fatal(err)
	}

	off, err := d.BuildTimeline(p, snapReq(0))
	if err != nil {
		t.Fatal(err)
	}
	if _, has := off.Metadata[timeline.MetaBeatBPM]; has {
		t.Errorf("a snap-off run stamped beat_bpm %q; no grid served that cut", off.Metadata[timeline.MetaBeatBPM])
	}
	if _, has := off.Metadata[timeline.MetaBeatPhase]; has {
		t.Error("a snap-off run stamped beat_phase; no grid served that cut")
	}
	if bt := off.BeatTicks(); bt != nil {
		t.Errorf("the snap-off document derived %+v; a document that states no grid must render null", bt)
	}

	on, err := d.BuildTimeline(p, snapReq(period/2))
	if err != nil {
		t.Fatal(err)
	}
	bpm, perr := strconv.ParseFloat(on.Metadata[timeline.MetaBeatBPM], 64)
	if perr != nil {
		t.Fatalf("the snap-on document states beat_bpm %q, which does not parse: %v", on.Metadata[timeline.MetaBeatBPM], perr)
	}
	if math.Abs(bpm-120) > 2 {
		t.Errorf("beat_bpm = %v, want ~120 (the fixture clicks every %.1f s)", bpm, period)
	}
	phase, qerr := strconv.ParseFloat(on.Metadata[timeline.MetaBeatPhase], 64)
	if qerr != nil {
		t.Fatalf("the snap-on document states beat_phase %q, which does not parse: %v", on.Metadata[timeline.MetaBeatPhase], qerr)
	}
	if phase < 0 || phase >= 1.0/120*60 {
		t.Errorf("beat_phase = %v, want a first-beat position inside one period", phase)
	}
	bt := on.BeatTicks()
	if bt == nil || bt.BPM != bpm || len(bt.Ticks) == 0 {
		t.Fatalf("the stated grid derived %+v; the ruler would draw nothing from a real stamped document", bt)
	}
	// The ticks sit inside the reel the document plays, and the first one lands
	// on the grid: phase + k·period mapped through the first clip.
	first := on.Tracks[0].Clips[0]
	at := bt.Ticks[0]
	sourceAt := first.SourceStart + (at-first.TimelineStart)*first.Speed
	resid := math.Mod(sourceAt-phase, 60/bpm)
	resid = math.Min(resid, 60/bpm-resid)
	if math.Abs(resid) > 1e-6 && math.Abs(resid-period) > 1e-6 {
		t.Errorf("first tick %.4f maps to source %.4f, which is %.4f off the stated grid", at, sourceAt, resid)
	}
}
