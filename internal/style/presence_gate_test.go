package style

import (
	"testing"

	"github.com/xiabee/XCut/internal/event"
)

func withPresence(s event.Segment, v float64) event.Segment {
	s.HasPlayerPresence = true
	s.PlayerPresence = v
	return s
}

// TestMinPlayerPresenceDropsOnlyMeasuredWeakSegments is the gate the whole
// person-filter chain exists for: a style threshold keeps the strong and the
// weak apart where presence was measured — and leaves segments without a
// measurement alone, because a knob their data cannot answer must not veto
// them.
func TestMinPlayerPresenceDropsOnlyMeasuredWeakSegments(t *testing.T) {
	p := testPreset()
	p.MaxClipDuration = 8
	p.MinClipDuration = 2
	p.TargetDuration = 60
	p.MinPlayerPresence = 0.5

	strong := withPresence(seg(10, 20, 0.25, -8), 0.9)
	weak := withPresence(seg(110, 120, 0.25, -8), 0.3)
	unmeasured := seg(210, 220, 0.25, -8)

	items := []AssetEvents{
		{Asset: AssetInfo{ID: "a1", Path: "a.mp4", DurationSec: 600},
			Segments: []event.Segment{strong, weak, unmeasured}},
	}
	built, err := Build(p, "prj", items)
	if err != nil {
		t.Fatal(err)
	}

	var starts []float64
	for _, tr := range built.Tracks {
		for _, c := range tr.Clips {
			starts = append(starts, c.SourceStart)
		}
	}
	if len(starts) != 2 {
		t.Fatalf("reel has %d clips (%v), want exactly the strong and the unmeasured segment", len(starts), starts)
	}
	for _, c := range built.Tracks[0].Clips {
		if c.SourceStart >= 105 && c.SourceStart <= 125 {
			t.Fatalf("the weak-measured segment (presence 0.3 < 0.5) survived at %.1f", c.SourceStart)
		}
	}
	strongIn, unmeasuredIn := false, false
	for _, s := range starts {
		if s >= 5 && s <= 25 {
			strongIn = true
		}
		if s >= 205 && s <= 225 {
			unmeasuredIn = true
		}
	}
	if !strongIn || !unmeasuredIn {
		t.Fatalf("strong (%v) or unmeasured (%v) segment missing — the gate dropped the wrong things", strongIn, unmeasuredIn)
	}
}

// TestMinPlayerPresenceInertWithoutAKnob pins the off state: with no threshold
// the measured-weak segment is just a segment — the filter must not bite when
// the style never asked for it.
func TestMinPlayerPresenceInertWithoutAKnob(t *testing.T) {
	p := testPreset()
	p.MaxClipDuration = 8
	p.MinClipDuration = 2
	p.TargetDuration = 60

	weak := withPresence(seg(10, 20, 0.25, -8), 0.3)
	items := []AssetEvents{
		{Asset: AssetInfo{ID: "a1", Path: "a.mp4", DurationSec: 600},
			Segments: []event.Segment{weak}},
	}
	built, err := Build(p, "prj", items)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(built.Tracks[0].Clips); n != 1 {
		t.Fatalf("an unset threshold dropped the segment anyway (%d clips)", n)
	}
}
