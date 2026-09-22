package style

import (
	"math"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/event"
	"github.com/xiabee/XCut/internal/timeline"
)

// Beat snapping (卡点) is the one style rule that deliberately makes a cut *not*
// where the detector said the action ends, so these cases fix what it may never
// break: a boundary the scoreboard measured outranks a beat, the clip stays
// inside the segment it was detected in, and a preset that never asks for it
// selects exactly as it did before.

func beatSnapItems() []AssetEvents {
	// Three events on one asset. The first is longer than max_clip_duration, so its
	// end is set by the length rule alone — and 0.05 s from a beat the snap may
	// reach, because the event's own footage runs past that end. The second stops
	// on a measured mark with a beat 0.05 s further, which must not move. The third
	// has no beat anywhere near its end.
	return []AssetEvents{{
		Asset:      AssetInfo{ID: "a1", Path: "a.mp4", DurationSec: 60},
		Segments:   []event.Segment{seg(2, 7.5, 0.5, -12), seg(12, 18, 0.5, -12), seg(20, 26, 0.5, -12)},
		Beats:      []float64{6.95, 13.45, 25.5},
		Boundaries: []float64{13.4},
	}}
}

func clipByStart(clips []timeline.Clip, start float64) *timeline.Clip {
	for i := range clips {
		if math.Abs(clips[i].SourceStart-start) < 0.01 {
			return &clips[i]
		}
	}
	return nil
}

func mustClipByStart(t *testing.T, clips []timeline.Clip, start float64) timeline.Clip {
	t.Helper()
	c := clipByStart(clips, start)
	if c == nil {
		t.Fatalf("no clip starts at %g; reel: %v", start, clips)
	}
	return *c
}

func TestBeatSnapMovesAnUnpinnedEnd(t *testing.T) {
	p := testPreset()
	p.TargetDuration = 30
	p.BeatSnapTolerance = 0.12
	tl, err := Build(p, "prj", beatSnapItems())
	if err != nil {
		t.Fatal(err)
	}
	clips := tl.Tracks[0].Clips

	// The free end follows the beat.
	if c := mustClipByStart(t, clips, 2); math.Abs(c.SourceEnd-6.95) > 1e-6 {
		t.Fatalf("snapped end = %v, want the beat at 6.95", c.SourceEnd)
	} else if c.Metadata["beat"] != "6.9500" {
		t.Fatalf("beat metadata = %q, want the exact 6.9500 so a reader can check the snap per clip", c.Metadata["beat"])
	}
	// A mark-pinned end does not move, even though a beat sits 0.05 s away: the
	// scoreboard rule is the harder constraint by design.
	pinned := mustClipByStart(t, clips, 12)
	if math.Abs(pinned.SourceEnd-13.4) > 1e-6 {
		t.Fatalf("boundary-pinned end moved to %v; a beat may not override a measured point end", pinned.SourceEnd)
	}
	if pinned.Metadata["point_end"] != "13.40" {
		t.Fatalf("point_end = %q, want the mark recorded", pinned.Metadata["point_end"])
	}
	if _, ok := pinned.Metadata["beat"]; ok {
		t.Fatalf("a pinned clip must not claim a beat snap: %v", pinned.Metadata)
	}
	// Nothing within tolerance: the end stays where the length rules put it.
	if c := mustClipByStart(t, clips, 20); math.Abs(c.SourceEnd-25) > 1e-6 {
		t.Fatalf("end = %v, want 25 (nearest beat 25.5 is 0.5 s away, over the 0.12 tolerance)", c.SourceEnd)
	}
}

// Without the knob the selection must be the one the preset has always produced;
// this is also the control that proves the assertion above is about the snap.
func TestBeatSnapOffLeavesEndsAlone(t *testing.T) {
	p := testPreset()
	p.TargetDuration = 30
	tl, err := Build(p, "prj", beatSnapItems())
	if err != nil {
		t.Fatal(err)
	}
	clips := tl.Tracks[0].Clips
	if c := mustClipByStart(t, clips, 2); math.Abs(c.SourceEnd-7) > 1e-6 {
		t.Fatalf("with no tolerance set the end must stay at the length rule's 7.0, got %v", c.SourceEnd)
	}
	for i, c := range clips {
		if _, ok := c.Metadata["beat"]; ok {
			t.Fatalf("clip %d carries beat metadata with the feature off", i)
		}
	}
}

// A snap may not reach footage the detector did not attribute to the event, nor
// lengthen the clip past max_clip_duration — both rules already bind the plain
// trim, and a snap that silently steps outside them would be a new way to break
// the reel's contract with the source.
func TestBeatSnapStaysInsideTheEventAndTheLength(t *testing.T) {
	p := testPreset()
	p.TargetDuration = 30
	p.MaxClipDuration = 5
	p.BeatSnapTolerance = 0.2
	items := []AssetEvents{{
		Asset:    AssetInfo{ID: "a1", Path: "a.mp4", DurationSec: 60},
		Segments: []event.Segment{seg(2, 6.6, 0.5, -12), seg(20, 40, 0.45, -12)},
		// 6.7 is past the first event's own end; 25.15 would make a 5.15 s clip.
		Beats: []float64{6.7, 25.15},
	}}
	tl, err := Build(p, "prj", items)
	if err != nil {
		t.Fatal(err)
	}
	clips := tl.Tracks[0].Clips
	if c := mustClipByStart(t, clips, 2); math.Abs(c.SourceEnd-6.6) > 1e-6 {
		t.Fatalf("end = %v, want 6.6: the beat at 6.7 sits past the event's own end", c.SourceEnd)
	}
	if c := mustClipByStart(t, clips, 20); math.Abs(c.SourceEnd-25) > 1e-6 {
		t.Fatalf("end = %v, want 25: snapping to 25.15 would exceed max_clip_duration 5", c.SourceEnd)
	}
	// Positive control for the test itself: with the same beats and a segment that
	// can host them, the snap does happen — otherwise "stays at 6.6" would pass on
	// a rule that never ran.
	items[0].Segments[0] = seg(2, 6.8, 0.5, -12)
	tl, err = Build(p, "prj", items)
	if err != nil {
		t.Fatal(err)
	}
	if c := mustClipByStart(t, tl.Tracks[0].Clips, 2); math.Abs(c.SourceEnd-6.7) > 1e-6 {
		t.Fatalf("end = %v, want the beat at 6.7 once the event reaches that far", c.SourceEnd)
	}
}

func TestBeatSnapToleranceValidated(t *testing.T) {
	p := testPreset()
	p.BeatSnapTolerance = 0.9
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "beat_snap_tolerance") {
		t.Fatalf("a 0.9 s snap must be refused by name, got %v", err)
	}
	p = testPreset()
	p.BeatSnapTolerance = -0.1
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "beat_snap_tolerance") {
		t.Fatalf("a negative snap must be refused by name, got %v", err)
	}
	// min_clip_duration is 1 here, so a snap of 1 s or more could empty a clip.
	p = testPreset()
	p.BeatSnapTolerance = 1.0
	p.MaxClipDuration = 5
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "min_clip_duration") {
		t.Fatalf("a snap as long as the minimum clip must be refused, got %v", err)
	}
	p = testPreset()
	p.BeatSnapTolerance = 0.12
	if err := p.Validate(); err != nil {
		t.Fatalf("a normal tolerance rejected: %v", err)
	}
}
