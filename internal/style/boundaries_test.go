package style

import (
	"testing"

	"github.com/xiabee/XCut/internal/event"
)

// Point boundaries arrive from outside (a sidecar that can read a scoreboard —
// docs/EVAL.md measured that knowing where a rally ended is worth ~14 points of
// precision, and that no signal the core owns can infer it). The rule the style
// engine applies is deliberately narrow: end the clip at the boundary when the
// boundary is reachable inside the same segment, and never invent material
// outside it.

func boundaryPreset() *Preset {
	p := testPreset()
	p.TargetDuration = 60
	p.MinClipDuration = 2
	p.MaxClipDuration = 8
	p.Diversity = Diversity{}
	return p
}

func TestBoundaryInsideWindowTrimsTheDeadTail(t *testing.T) {
	p := boundaryPreset()
	// segment 10..30, window would be 10..18, but the point ended at 14
	items := []AssetEvents{{
		Asset:      AssetInfo{ID: "a1", Path: "a.mp4", DurationSec: 600},
		Segments:   []event.Segment{seg(10, 30, 0.5, -8)},
		Boundaries: []float64{14},
	}}
	tl, err := Build(p, "prj", items)
	if err != nil {
		t.Fatal(err)
	}
	c := tl.Tracks[0].Clips[0]
	if c.SourceStart != 10 || c.SourceEnd != 14 {
		t.Fatalf("boundary inside the window must trim the tail: got %v..%v, want 10..14",
			c.SourceStart, c.SourceEnd)
	}
	// The clip says so: a reader checking one clip in the UI must be able to see
	// which boundary shaped it, not only that the aggregate got better.
	if got := c.Metadata["point_end"]; got != "14.00" {
		t.Fatalf("metadata point_end = %q, want \"14.00\" (metadata: %v)", got, c.Metadata)
	}
}

func TestBoundaryBeyondWindowShiftsToEndThere(t *testing.T) {
	p := boundaryPreset()
	// segment 0..30, window would be 0..8; the point actually ran to 17.2,
	// which the segment contains — so show the finish, not the first 8 seconds.
	items := []AssetEvents{{
		Asset:      AssetInfo{ID: "a1", Path: "a.mp4", DurationSec: 600},
		Segments:   []event.Segment{seg(0, 30, 0.5, -8)},
		Boundaries: []float64{17.2},
	}}
	tl, err := Build(p, "prj", items)
	if err != nil {
		t.Fatal(err)
	}
	c := tl.Tracks[0].Clips[0]
	if c.SourceEnd != 17.2 {
		t.Fatalf("clip must end at the point's end: got %v, want 17.2", c.SourceEnd)
	}
	if got := c.Metadata["point_end"]; got != "17.20" {
		t.Fatalf("metadata point_end = %q, want \"17.20\"", got)
	}
	if c.SourceEnd-c.SourceStart < p.MinClipDuration {
		t.Fatalf("shifted clip is below the minimum length: %v s", c.SourceEnd-c.SourceStart)
	}
	if c.SourceStart < 0 {
		t.Fatalf("shifted before the media start: %v", c.SourceStart)
	}
}

func TestBoundaryOutsideSegmentIsIgnored(t *testing.T) {
	p := boundaryPreset()
	// the only boundary is past the segment end: the engine may not reach
	// outside the segment it was given, so the clip stays start-anchored.
	items := []AssetEvents{{
		Asset:      AssetInfo{ID: "a1", Path: "a.mp4", DurationSec: 600},
		Segments:   []event.Segment{seg(10, 20, 0.5, -8)},
		Boundaries: []float64{400},
	}}
	tl, err := Build(p, "prj", items)
	if err != nil {
		t.Fatal(err)
	}
	c := tl.Tracks[0].Clips[0]
	if c.SourceStart != 10 || c.SourceEnd != 18 {
		t.Fatalf("unreachable boundary must change nothing: got %v..%v, want 10..18",
			c.SourceStart, c.SourceEnd)
	}
	if _, present := c.Metadata["point_end"]; present {
		t.Fatalf("a clip that was not shaped by a boundary must not claim one: %v", c.Metadata)
	}
}

// The whole feature is optional: with no boundaries supplied the output must be
// byte-identical to before, which is what makes shipping it safe.
func TestNoBoundariesKeepsStartAnchoredWindows(t *testing.T) {
	p := boundaryPreset()
	asset := AssetInfo{ID: "a1", Path: "a.mp4", DurationSec: 600}
	segs := []event.Segment{seg(10, 30, 0.5, -8), seg(100, 130, 0.4, -9)}
	withEmpty, err := Build(p, "prj", []AssetEvents{{Asset: asset, Segments: segs, Boundaries: []float64{}}})
	if err != nil {
		t.Fatal(err)
	}
	plain, err := Build(p, "prj", []AssetEvents{{Asset: asset, Segments: segs}})
	if err != nil {
		t.Fatal(err)
	}
	a, b := withEmpty.Tracks[0].Clips, plain.Tracks[0].Clips
	if len(a) != len(b) {
		t.Fatalf("clip counts differ: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].SourceStart != b[i].SourceStart || a[i].SourceEnd != b[i].SourceEnd {
			t.Fatalf("clip %d differs: %v..%v vs %v..%v", i,
				a[i].SourceStart, a[i].SourceEnd, b[i].SourceStart, b[i].SourceEnd)
		}
		// The equality claim covers what the UI shows, not just the cut: a
		// boundary key appearing on an unmarked project would be a lie.
		if _, present := a[i].Metadata["point_end"]; present {
			t.Fatalf("clip %d carries point_end without any boundaries: %v", i, a[i].Metadata)
		}
		if a[i].Metadata["reason"] != b[i].Metadata["reason"] {
			t.Fatalf("clip %d reason differs: %q vs %q", i, a[i].Metadata["reason"], b[i].Metadata["reason"])
		}
	}
}

// TestCandidateLimitMetadata: the flag the CLI prints to explain a short reel
// must distinguish "this footage offered no more" from "your budget stopped
// early". Getting it backwards would blame the footage for the user's setting,
// which is worse than saying nothing.
func TestCandidateLimitMetadata(t *testing.T) {
	items := func() []AssetEvents {
		return []AssetEvents{{
			Asset: AssetInfo{ID: "a1", Path: "a.mp4", DurationSec: 600},
			Segments: []event.Segment{
				seg(10, 30, 0.5, -8), seg(100, 130, 0.4, -9), seg(200, 225, 0.6, -7),
			},
		}}
	}

	full := boundaryPreset() // target 60s: every segment fits
	tl, err := Build(full, "prj", items())
	if err != nil {
		t.Fatal(err)
	}
	if tl.Metadata["candidate_limit"] != "true" {
		t.Fatalf("candidates ran out, metadata says %q", tl.Metadata["candidate_limit"])
	}
	if tl.Metadata["candidate_events"] != "3" {
		t.Fatalf("candidate_events = %q, want 3", tl.Metadata["candidate_events"])
	}
	// The asked-for length travels with the document, and it follows the preset
	// rather than the style file: a client reopening a saved timeline cannot
	// otherwise tell "60s of material" from "60s was asked, the footage gave 50".
	if tl.Metadata["target_duration"] != "60.00" {
		t.Fatalf("target_duration = %q, want 60.00", tl.Metadata["target_duration"])
	}

	tight := boundaryPreset()
	tight.TargetDuration = 10 // only the first clip can fit
	tl2, err := Build(tight, "prj", items())
	if err != nil {
		t.Fatal(err)
	}
	if tl2.Metadata["candidate_limit"] != "false" {
		t.Fatalf("a 10s budget stopped the loop early; metadata claims %q", tl2.Metadata["candidate_limit"])
	}
	if tl2.Metadata["target_duration"] != "10.00" {
		t.Fatalf("target_duration = %q, want 10.00", tl2.Metadata["target_duration"])
	}
}
