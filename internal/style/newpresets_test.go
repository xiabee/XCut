package style

import (
	"math"
	"testing"

	"github.com/xiabee/XCut/internal/event"
	"github.com/xiabee/XCut/internal/timeline"
)

// The two Phase 5 presets exist to move the numbers the pacing readout reports:
// B4b measured the shipped one at mean = median = longest = its own
// max_clip_duration, with the top-scored shot a quarter of the reel in. So each
// preset below is tested against that shape, and against the one thing it must
// not do — claim a crop it cannot back up when the project has no region.

func rallyItems(roi *MotionROI) []AssetEvents {
	segs := []event.Segment{
		rallySeg(4, 14, 0.30, -14, 12),
		rallySeg(24, 34, 0.35, -13, 15),
		rallySeg(44, 54, 0.90, -6, 28), // the best rally, and not the first
		rallySeg(64, 74, 0.25, -15, 10),
		rallySeg(84, 94, 0.40, -12, 16),
	}
	return []AssetEvents{{
		Asset:    AssetInfo{ID: "a1", Path: "match.mp4", DurationSec: 120, ROI: roi},
		Segments: segs,
	}}
}

func rallySeg(start, end, motion, db float64, hits int) event.Segment {
	return event.Segment{
		Start: start, End: end, Score: 0.5, MeanMotion: motion, MeanAudioDB: db,
		Kind: "rally", HitCount: hits, HitDensity: float64(hits) / (end - start),
	}
}

func buildWith(t *testing.T, name string, items []AssetEvents) *timeline.Timeline {
	t.Helper()
	p, err := Load(name)
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	tl, err := Build(p, "prj", items)
	if err != nil {
		t.Fatalf("build %s: %v", name, err)
	}
	return tl
}

func TestSportsVerticalIsTallerShorterAndRegionAimed(t *testing.T) {
	tl := buildWith(t, "sports_vertical", rallyItems(&MotionROI{X: 0.3, Y: 0.3, W: 0.5, H: 0.5}))
	if tl.Canvas.Width >= tl.Canvas.Height {
		t.Fatalf("canvas %dx%d is not vertical", tl.Canvas.Width, tl.Canvas.Height)
	}
	clips := tl.Tracks[0].Clips
	if len(clips) < 3 {
		t.Fatalf("%d clips selected, want at least 3 to shape a reel", len(clips))
	}
	p := tl.Pacing()
	// The B4b finding was that every shot sat on the 8 s ceiling. This preset's
	// whole pacing claim is a lower ceiling, so the readout has to show it.
	if p.LongestSeconds > 4.0+1e-9 {
		t.Fatalf("longest shot is %.3fs; the preset promises a %.1fs ceiling", p.LongestSeconds, 4.0)
	}
	if p.MeanSeconds >= 8.0 {
		t.Fatalf("mean shot %.3fs — no shorter than the style this preset replaces", p.MeanSeconds)
	}
	// A recap keeps match order: only beat_shortform asks for a hook.
	for i := 1; i < len(clips); i++ {
		if clips[i].SourceStart < clips[i-1].SourceStart {
			t.Fatalf("clip %d jumps backwards in source time (%.1f after %.1f); a recap stays chronological",
				i, clips[i].SourceStart, clips[i-1].SourceStart)
		}
	}
	// The reframe is aimed at the region the project was analyzed with, and it is
	// the same centre for every shot — that is what "roi" promises.
	cx, cy := 0.3+0.5/2, 0.3+0.5/2
	for i, c := range clips {
		if c.Motion == nil {
			t.Fatalf("clip %d has no framing plan; the preset asked for roi and the asset has a region", i)
		}
		if len(c.Motion.From) != 2 || math.Abs(c.Motion.From[0]-cx) > 1e-9 || math.Abs(c.Motion.From[1]-cy) > 1e-9 {
			t.Fatalf("clip %d plans from %v, want the region's centre (%g, %g)", i, c.Motion.From, cx, cy)
		}
		if c.Metadata["framing"] != FramingROI {
			t.Fatalf("clip %d records framing %q, want %q", i, c.Metadata["framing"], FramingROI)
		}
	}
}

// TestSportsVerticalWithoutARegionCropsNothing: the preset asks for a crop, but a
// project analyzed full-frame has nothing to aim at. Falling back to no plan is
// the honest answer — inventing a centre crop would cut the scoreboard out of the
// shot on the strength of a guess, which is the exact reason no shipped preset
// enabled motion before this.
func TestSportsVerticalWithoutARegionCropsNothing(t *testing.T) {
	tl := buildWith(t, "sports_vertical", rallyItems(nil))
	clips := tl.Tracks[0].Clips
	if len(clips) == 0 {
		t.Fatal("no clips at all")
	}
	for i, c := range clips {
		if c.Motion != nil {
			t.Fatalf("clip %d got a plan (%+v) with no analyzed region to aim it at", i, c.Motion)
		}
		if _, ok := c.Metadata["framing"]; ok {
			t.Fatalf("clip %d claims framing it does not have", i)
		}
	}
}

func TestBeatShortformIsShortAndLeadsWithItsBestShot(t *testing.T) {
	tl := buildWith(t, "beat_shortform", rallyItems(nil))
	if tl.Canvas.Width >= tl.Canvas.Height {
		t.Fatalf("canvas %dx%d is not vertical", tl.Canvas.Width, tl.Canvas.Height)
	}
	p := tl.Pacing()
	if p.Shots < 3 {
		t.Fatalf("%d shots — one clip is trivially 'first', so the hook claim would prove nothing", p.Shots)
	}
	if p.LongestSeconds > 2.8+1e-9 {
		t.Fatalf("longest shot %.3fs against a promised 2-3 s short form (max_clip_duration 2.8)", p.LongestSeconds)
	}
	if p.HookSeconds != 0 {
		t.Fatalf("HookSeconds = %v, want the top shot to lead the reel: %+v", p.HookSeconds, p)
	}

	// The same footage and the same picks under the default order must *not* open
	// on that shot, or the ordering rule is being credited for nothing.
	plain := rallyItems(nil)
	cp, err := Load("beat_shortform")
	if err != nil {
		t.Fatal(err)
	}
	cp.ClipOrder = ""
	ctl, err := Build(cp, "prj", plain)
	if err != nil {
		t.Fatal(err)
	}
	if q := ctl.Pacing(); q.HookSeconds == 0 {
		t.Fatalf("the control reel already opens on its top shot (%+v); the fixture cannot show a hook moving", q)
	}
	if a, b := ctl.Pacing(), p; a.Shots != b.Shots || math.Abs(a.MeanSeconds-b.MeanSeconds) > 1e-9 {
		t.Fatalf("ordering changed the selection or the lengths: %+v vs %+v", a, b)
	}
}
