package pipeline

import (
	"context"
	"io"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/config"
	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/testmedia"
	"github.com/xiabee/XCut/internal/timeline"
	"github.com/xiabee/XCut/internal/workspace"
)

// The unit tests in internal/style hand Build a beat grid. This one is the
// difference: the grid has to come out of a real media file's own audio, through
// the analyzer and the analysis cache, or the feature is a test fixture.
//
// The claim is the one docs/ROADMAP.md set for 卡点: snapping moves a clip end and
// nothing else — not the selection, not the start, not past the tolerance. The
// fixture is 120 BPM clicks, so its grid is the half second, and rally ends sit
// 0.2 s off it (the pad is 1.2 s on a 0.5 s lattice). With the window opened to
// half a beat period, every free end must therefore land on a half second, and the
// control asserts the ends start off it — a snap that reached no grid at all would
// otherwise pass every assertion below unnoticed.
func TestBeatSnappingThroughTheRealAnalysisPath(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	const (
		period = 0.5  // 120 BPM clicks, placed by the fixture itself
		tol    = 0.25 // half a beat: every end is then within reach of one
		slack  = 0.06 // onset detection is sampled, not exact
	)
	dir := t.TempDir()
	// The file runs 0.3 s past a lattice line (14.3): a whole-file rally now
	// clamps its end there — OFF the half-second grid — so the control below
	// still has something to prove, and the snap can reach the 14.5 beat.
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
	p, err := db.CreateProject(context.Background(), "beats")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.ImportAsset(p, path); err != nil {
		t.Fatal(err)
	}
	if err := d.AnalyzeProject(p, nil); err != nil {
		t.Fatal(err)
	}

	// The preset carries no tolerance, so BeatSnap 0 means the rule is off.
	off := buildReel(t, d, p, snapReq(0))
	on := buildReel(t, d, p, snapReq(tol))

	if len(off) != len(on) {
		t.Fatalf("snapping changed the clip count (%d → %d); it may move an end, not select differently",
			len(off), len(on))
	}
	offGrid := 0
	for _, c := range off {
		if !nearBeat(c.SourceEnd, period, slack) {
			offGrid++
		}
	}
	if offGrid == 0 {
		t.Fatalf("control broken: every end already sits on the half second (%v), so the snap has nothing to prove",
			clipEnds(off))
	}

	moved := 0
	for i := range off {
		a, b := off[i], on[i]
		if math.Abs(a.SourceStart-b.SourceStart) > 1e-6 {
			t.Fatalf("clip %d: start moved %v → %v; only the end is the snap's to move", i, a.SourceStart, b.SourceStart)
		}
		delta := math.Abs(b.SourceEnd - a.SourceEnd)
		if delta > tol+1e-6 {
			t.Fatalf("clip %d moved %.4f s, over the %g tolerance", i, delta, tol)
		}
		if !nearBeat(b.SourceEnd, period, slack) {
			t.Fatalf("clip %d ends at %.4f, which is no beat of the file's own %.1f s grid: ends now %v",
				i, b.SourceEnd, period, clipEnds(on))
		}
		if delta <= 1e-6 {
			if _, has := b.Metadata["beat"]; has {
				t.Fatalf("clip %d claims a beat it did not move", i)
			}
			continue
		}
		moved++
		// The metadata is the per-clip evidence, and it has to agree with geometry.
		reported, perr := strconv.ParseFloat(b.Metadata["beat"], 64)
		if perr != nil {
			t.Fatalf("clip %d moved to %v without a parseable beat metadata value (%q)",
				i, b.SourceEnd, b.Metadata["beat"])
		}
		if math.Abs(reported-b.SourceEnd) > 1e-6 {
			t.Fatalf("clip %d: beat metadata %v disagrees with its end %v", i, reported, b.SourceEnd)
		}
	}
	if moved == 0 {
		t.Fatalf("no end moved with a %g s window on a %.1f s grid, though %d ends sat off it (%v): the rule never ran",
			tol, period, offGrid, clipEnds(off))
	}
	t.Logf("clicks every %.1f s: %d of %d ends moved onto the grid, %d off-grid before",
		period, moved, len(on), offGrid)
}

// The request bound is checked per knob, independent of the others: with the
// first cut of this code an absent duration returned early and skipped the snap
// check, so {"beat_snap": 2} queued a job that the preset would have refused.
func TestBeatSnapRequestValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  TimelineRequest
		ok   bool
	}{
		{"unset keeps the style's own", TimelineRequest{Style: "generic_highlight", BeatSnap: 0}, true},
		{"off is a legal value", TimelineRequest{Style: "generic_highlight", BeatSnap: BeatSnapOff}, true},
		{"a quarter second is accepted", TimelineRequest{Style: "generic_highlight", BeatSnap: 0.25}, true},
		{"the ceiling itself is accepted", TimelineRequest{Style: "generic_highlight", BeatSnap: 0.5}, true},
		{"past the ceiling refused", TimelineRequest{Style: "generic_highlight", BeatSnap: 0.51}, false},
		{"two seconds refused with no duration", TimelineRequest{Style: "generic_highlight", BeatSnap: 2}, false},
		{"a negative other than off refused", TimelineRequest{Style: "generic_highlight", BeatSnap: -2}, false},
		{"NaN refused", TimelineRequest{Style: "generic_highlight", BeatSnap: math.NaN()}, false},
	} {
		err := tc.req.validate()
		if tc.ok && err != nil {
			t.Errorf("%s: got %v, want accepted", tc.name, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("%s: got nil, want rejection", tc.name)
		}
	}
	// The combined case: with both knobs out of range the message is about the
	// duration (the first check), and the snap is still refused once fixed.
	both := TimelineRequest{Style: "generic_highlight", Duration: 0.5, BeatSnap: 9}
	if err := both.validate(); err == nil || !strings.Contains(err.Error(), "duration") {
		t.Fatalf("combined bad request: %v, want the duration named first", err)
	}
	both.Duration = 0
	if err := both.validate(); err == nil || !strings.Contains(err.Error(), "beat snap") {
		t.Fatalf("after fixing the duration the snap must still be refused, got %v", err)
	}
}

// nearBeat reports whether t sits within slack of a multiple of the beat period.
func nearBeat(ts, period, slack float64) bool {
	r := math.Mod(ts, period)
	return r <= slack || period-r <= slack
}

// snapReq asks for a 7.7 s rally reel — an odd ask on purpose. A round one (8 s)
// from a 0.5 s lattice ends exactly on the lattice (max_clip_duration carries it
// there), and then "did the snap move anything?" could only answer no. The reel is
// one clip because the fixture is one rally; the multi-clip geometry — a pinned
// end that must not move beside a free one that must — is internal/style's
// TestBeatSnap* family, which needs no ffmpeg to check it.
func snapReq(snap float64) TimelineRequest {
	// generic_highlight: activity segments carry no hit times, so their ends
	// stay head-anchored and free for the snap to move. A rally-style reel
	// would end at last-hit-plus-tail, which the grid (built from the same
	// onsets) never reaches — and the landing, not the beat, owns that edge.
	return TimelineRequest{Style: "generic_highlight", Duration: 7.7, BeatSnap: snap}
}

func clipEnds(clips []timeline.Clip) []float64 {
	out := make([]float64, 0, len(clips))
	for _, c := range clips {
		out = append(out, c.SourceEnd)
	}
	return out
}

func buildReel(t *testing.T, d Deps, p *storage.Project, req TimelineRequest) []timeline.Clip {
	t.Helper()
	tl, err := d.BuildTimeline(p, req)
	if err != nil {
		t.Fatalf("build reel for %+v: %v", req, err)
	}
	clips := clipsOf(tl)
	if len(clips) == 0 {
		t.Fatalf("style %q produced no clips from the click fixture", req.Style)
	}
	return clips
}
