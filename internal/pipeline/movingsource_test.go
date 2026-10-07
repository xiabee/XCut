package pipeline

import (
	"context"
	"io"
	"log/slog"
	"math"
	"testing"

	"github.com/xiabee/XCut/internal/config"
	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/testmedia"
	"github.com/xiabee/XCut/internal/workspace"
)

// The color-card fixtures are temporally flat between cuts, so the activity
// segmentation has nothing to chunk on: one span, one clip, and every
// activity-mode eval row on them reads degenerate — the gap docs/EVAL.md and
// the roadmap's B4c note call "the multi-cut moving source this project does
// not have". GeneratePanBursts is that input in its smallest form. These two
// tests pin the contrast through the real chain (fixture → analyze → activity
// segmentation → beat_shortform build): on the moving source the selection is
// non-degenerate and every clip sits on a pan burst; on the color card it
// stays the one-clip span the content dictates. No claim about real footage
// moves — that input remains owner-supplied.

// panBurstsFixture layout: [pan 0..3][still 3..7][pan 7..10][still 10..14][pan 14..17][still 17..21].
const (
	msPan    = 3.0
	msStill  = 4.0
	msBursts = 3
)

func msInPanBurst(t float64) bool {
	period := msPan + msStill
	pos := math.Mod(t, period)
	return pos < msPan
}

func TestMovingSourceYieldsNonDegenerateActivitySelection(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
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
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	d := NewDeps(context.Background(), db, ws, cfg, logger)

	p, err := db.CreateProject(context.Background(), "moving-source")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path, err := testmedia.GeneratePanBursts(dir, "pan.mp4", 640, 360, 15, msPan, msStill, msBursts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.ImportAsset(p, path); err != nil {
		t.Fatal(err)
	}

	tl, err := d.BuildTimeline(p, Style("beat_shortform"))
	if err != nil {
		t.Fatalf("build on a moving source: %v", err)
	}
	clips := tl.Tracks[0].Clips
	t.Logf("moving source yielded %d clips", len(clips))
	if len(clips) < 2 {
		t.Fatalf("%d clips from a 3-burst moving source — the activity segmentation found no chunks", len(clips))
	}
	// Every clip must sit on a pan burst: a shot chosen entirely from a still
	// scene means the motion floor let flat content through.
	for i, c := range clips {
		dur := c.SourceEnd - c.SourceStart
		if dur < 1.0-1e-6 || dur > 2.8+1e-6 {
			t.Errorf("clip %d source window %.3f..%.3f is outside the preset's 1.0..2.8 clip span", i, c.SourceStart, c.SourceEnd)
		}
		if !msInPanBurst(c.SourceStart) && !msInPanBurst(c.SourceStart+dur/2) && !msInPanBurst(c.SourceEnd) {
			t.Errorf("clip %d window %.3f..%.3f lies entirely in a still scene", i, c.SourceStart, c.SourceEnd)
		}
	}
}

func TestStaticNoCutSourceStaysOneSpanOneClip(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
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
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	d := NewDeps(context.Background(), db, ws, cfg, logger)

	p, err := db.CreateProject(context.Background(), "static-no-cut")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	// ONE scene, no cuts, no in-scene motion: the fixed-camera shape the eval
	// notes describe. (A color card WITH cuts is not this control — the cut
	// spikes alone segment it: the DefaultFixture yields 4 clips here, one per
	// 2 s scene, which is why the degenerate row needed a cut-less source.)
	path, err := testmedia.Generate(dir, "static.mp4",
		[]testmedia.Scene{{Seconds: 8, Color: "teal", Frequency: 330}}, 320, 240, 15)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.ImportAsset(p, path); err != nil {
		t.Fatal(err)
	}

	tl, err := d.BuildTimeline(p, Style("beat_shortform"))
	if err != nil {
		t.Fatalf("build on a static cut-less source: %v", err)
	}
	// The documented degenerate shape: no cuts and no in-scene motion, one
	// activity span, one clip — the content talking, not the selection.
	if n := len(tl.Tracks[0].Clips); n != 1 {
		t.Fatalf("%d clips on a static cut-less source, want exactly the one-span selection", n)
	}
}
