package pipeline

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/config"
	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/testmedia"
	"github.com/xiabee/XCut/internal/timeline"
	"github.com/xiabee/XCut/internal/workspace"
)

// scoreboardDeps imports one synthetic rally fixture (motion + hit bursts, so
// rally mode has audio to work with) into a real workspace.
func scoreboardDeps(t *testing.T) (Deps, *storage.Project) {
	t.Helper()
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
	d := NewDeps(context.Background(), db, ws, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	p, err := db.CreateProject(context.Background(), "marks")
	if err != nil {
		t.Fatal(err)
	}
	path, err := testmedia.GenerateRally(root, "rallies.mp4", 320, 240, 10, 47, []testmedia.RallySpec{
		{Start: 6, End: 16, HitEvery: 0.7},
		{Start: 22, End: 32, HitEvery: 0.7},
		{Start: 38, End: 46, HitEvery: 0.7},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.ImportAsset(p, path); err != nil {
		t.Fatal(err)
	}
	return d, p
}

// TestStoredScoreMarksEndTheClip: the marks on the asset row must reach the
// selection, or the whole scoreboard feature is dead weight — style has its own
// unit tests, storage has its round trip, and this is the one join between
// them. Ground truth is taken from a marks-free run of the same fixture, so the
// assertion cannot be satisfied by segmentation drifting.
func TestStoredScoreMarksEndTheClip(t *testing.T) {
	d, p := scoreboardDeps(t)
	ctx := context.Background()

	unmarked, err := d.BuildTimeline(p, Style("badminton_highlight"))
	if err != nil {
		t.Fatal(err)
	}
	firstClips := clipsOf(unmarked)
	if len(firstClips) < 1 {
		t.Fatalf("fixture produced no clips: %+v", firstClips)
	}
	first := firstClips[0]
	for _, c := range firstClips {
		if c.SourceStart < first.SourceStart {
			first = c
		}
	}
	if first.SourceEnd-first.SourceStart < 4 {
		t.Fatalf("expected a full-length first clip, got %.2f..%.2f", first.SourceStart, first.SourceEnd)
	}
	// One second before that clip's end: inside the same rally, and past the
	// style's 1.5 s minimum, so the only thing that can move the end is a mark.
	// Rounded the way the style rounds, to compare against it exactly.
	mark := math.Round((first.SourceEnd-1.0)*1e4) / 1e4

	assets, err := d.DB.ListAssets(ctx, p.ID)
	if err != nil || len(assets) != 1 {
		t.Fatalf("assets: %+v, %v", assets, err)
	}
	if err := d.DB.SetAssetScoreMarks(ctx, assets[0].ID, &storage.ScoreMarks{
		Crop: []float64{0.7, 0, 0.25, 0.15}, Times: []float64{mark}, At: 1,
	}); err != nil {
		t.Fatal(err)
	}

	marked, err := d.BuildTimeline(p, Style("badminton_highlight"))
	if err != nil {
		t.Fatal(err)
	}
	if endsAt(marked, mark) != 1 {
		t.Fatalf("no clip ends at the scoreboard mark %.2f; ends are %s", mark, ends(marked))
	}
	if endsAt(unmarked, mark) != 0 {
		t.Fatalf("the marks-free run already ended at %.2f, so the assertion proves nothing", mark)
	}
	if endsAt(marked, first.SourceEnd) != 0 {
		t.Fatalf("a clip still ends at the untrimmed %.2f: %s", first.SourceEnd, ends(marked))
	}
	// The start is untouched: the mark shortens the clip, it does not move it.
	if startsAt(marked, first.SourceStart) != 1 {
		t.Fatalf("the trimmed clip lost its start at %.2f: %s", first.SourceStart, ends(marked))
	}
}

func clipsOf(tl *timeline.Timeline) []timeline.Clip {
	var out []timeline.Clip
	for _, tr := range tl.Tracks {
		out = append(out, tr.Clips...)
	}
	return out
}

func endsAt(tl *timeline.Timeline, at float64) int {
	return matches(tl, func(c timeline.Clip) bool { return c.SourceEnd == at })
}

func startsAt(tl *timeline.Timeline, at float64) int {
	return matches(tl, func(c timeline.Clip) bool { return c.SourceStart == at })
}

func matches(tl *timeline.Timeline, pred func(timeline.Clip) bool) int {
	n := 0
	for _, c := range clipsOf(tl) {
		if pred(c) {
			n++
		}
	}
	return n
}

func ends(tl *timeline.Timeline) string {
	parts := make([]string, 0, len(tl.Tracks))
	for _, c := range clipsOf(tl) {
		parts = append(parts, fmt.Sprintf("%.2f..%.2f", c.SourceStart, c.SourceEnd))
	}
	return "[" + strings.Join(parts, " ") + "]"
}
