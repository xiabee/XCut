package style

import (
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/event"
	"github.com/xiabee/XCut/internal/timeline"
)

// The ordering policy is a claim about the *reel*, not about the footage: the
// same three picks can be shown in the order they were played or with the
// moment the style scored best up front. Everything else about the cut — which
// spans were chosen, how long each runs, what framing they carry — must be
// identical, or "a new order" would be an excuse to change the selection.

func orderItems() []AssetEvents {
	return []AssetEvents{{
		Asset: AssetInfo{ID: "a1", Path: "a.mp4", DurationSec: 60},
		Segments: []event.Segment{
			seg(2, 6, 0.10, -30), // weakest
			seg(14, 18, 0.20, -20),
			seg(26, 30, 0.90, -5), // strongest, and last in source time
		},
	}}
}

func orderReel(t *testing.T, order string, items []AssetEvents) *timeline.Timeline {
	t.Helper()
	p := testPreset()
	p.TargetDuration = 30
	p.ClipOrder = order
	if err := p.Validate(); err != nil {
		t.Fatalf("preset with clip_order %q rejected: %v", order, err)
	}
	tl, err := Build(p, "prj", items)
	if err != nil {
		t.Fatal(err)
	}
	return tl
}

func sourceStarts(clips []timeline.Clip) []float64 {
	out := make([]float64, 0, len(clips))
	for _, c := range clips {
		out = append(out, c.SourceStart)
	}
	return out
}

func TestHookFirstPutsTheTopShotFirst(t *testing.T) {
	chrono := orderReel(t, "", orderItems()).Tracks[0].Clips
	if len(chrono) != 3 {
		t.Fatalf("the control reel has %d clips, want all 3 segments used", len(chrono))
	}
	// The control has to hold first: if the default order were not chronological,
	// "hook_first changed the order" would be measuring nothing.
	want := []float64{2, 14, 26}
	got := sourceStarts(chrono)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("default order gave source starts %v, want %v — the control is broken", got, want)
		}
	}

	hooked := orderReel(t, ClipOrderHookFirst, orderItems()).Tracks[0].Clips
	got = sourceStarts(hooked)
	if len(got) != 3 {
		t.Fatalf("hook_first reel has %d clips, want the same 3", len(got))
	}
	if got[0] != 26 {
		t.Fatalf("hook_first started at %v, want the top-scored segment (source 26) first: %v", got[0], got)
	}
	// Everything after the hook keeps match order — a hook is one clip moved, not
	// a full re-sort by score, which would scatter the rally chronology.
	if got[1] != 2 || got[2] != 14 {
		t.Fatalf("the tail of a hook_first reel must stay chronological, got %v", got)
	}
}

// TestHookFirstChangesOnlyTheOrder: same selection, same lengths, same framing,
// same total. Anything else would let a preset quietly rewrite the edit while
// claiming to have reordered it.
func TestHookFirstChangesOnlyTheOrder(t *testing.T) {
	chrono := orderReel(t, ClipOrderChronological, orderItems()).Tracks[0].Clips
	hooked := orderReel(t, ClipOrderHookFirst, orderItems()).Tracks[0].Clips
	bySource := func(clips []timeline.Clip) map[float64]timeline.Clip {
		m := map[float64]timeline.Clip{}
		for _, c := range clips {
			m[c.SourceStart] = c
		}
		return m
	}
	a, b := bySource(chrono), bySource(hooked)
	if len(a) != len(b) {
		t.Fatalf("%d clips vs %d", len(a), len(b))
	}
	for s, ca := range a {
		cb, ok := b[s]
		if !ok {
			t.Fatalf("clip from source %g is missing from the hook_first reel", s)
		}
		if ca.Duration() != cb.Duration() {
			t.Errorf("clip at source %g is %vs in one order and %vs in the other", s, ca.Duration(), cb.Duration())
		}
		if ca.Metadata["score"] != cb.Metadata["score"] {
			t.Errorf("clip at source %g scored %q in one order and %q in the other", s, ca.Metadata["score"], cb.Metadata["score"])
		}
		if ca.Speed != cb.Speed || ca.SourceEnd != cb.SourceEnd {
			t.Errorf("clip at source %g differs beyond its position: %+v vs %+v", s, ca, cb)
		}
	}
}

// TestPacingAgreesWithTheOrderItIsAskedToProduce: the readout B4b added is what
// the ordering rule claims to move, so the two must be checked against each other
// rather than each against its own fixture.
func TestPacingAgreesWithTheOrderItIsAskedToProduce(t *testing.T) {
	chrono := orderReel(t, "", orderItems())
	hooked := orderReel(t, ClipOrderHookFirst, orderItems())
	if p := chrono.Pacing(); p.HookSeconds == 0 {
		t.Fatalf("the chronological reel already opens on its top shot (%+v) — the control proves nothing", p)
	}
	if p := hooked.Pacing(); p.HookSeconds != 0 {
		t.Fatalf("hook_first reported HookSeconds = %v, want 0 (the top shot leads the reel): %+v", p.HookSeconds, p)
	}
	if a, b := chrono.Pacing(), hooked.Pacing(); a.MeanSeconds != b.MeanSeconds || a.Shots != b.Shots {
		t.Fatalf("reordering changed the shape: %+v vs %+v", a, b)
	}
}

// TestClipOrderRejectedAtValidation: an unknown order name must not be read as
// "the default". A typo in a shipped preset would otherwise silently keep
// chronological order while the preset's own text promised a hook.
func TestClipOrderRejectedAtValidation(t *testing.T) {
	for _, bad := range []string{"hooks_first", "Hook_First", "random", "score"} {
		p := testPreset()
		p.ClipOrder = bad
		err := p.Validate()
		if err == nil {
			t.Fatalf("clip_order %q accepted; a typo would silently keep the old order", bad)
		}
		if !strings.Contains(err.Error(), "clip_order") {
			t.Fatalf("clip_order %q refused for the wrong reason: %v", bad, err)
		}
	}
	for _, ok := range []string{"", ClipOrderChronological, ClipOrderHookFirst} {
		p := testPreset()
		p.ClipOrder = ok
		if err := p.Validate(); err != nil {
			t.Fatalf("clip_order %q rejected: %v", ok, err)
		}
	}
}
