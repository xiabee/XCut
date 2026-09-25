package style

import (
	"testing"

	"github.com/xiabee/XCut/internal/event"
)

// rallyPreset is a badminton-shaped preset for the trim tests.
func rallyPreset() *Preset {
	return &Preset{
		Name:            "rally-test",
		Version:         1,
		MinClipDuration: 1.5,
		MaxClipDuration: 8.0,
		EventConfig:     event.DefaultConfig(),
		Transition:      Transition{Type: "cut"},
	}
}

func segWithHits(start, end float64, hits ...float64) event.Segment {
	return event.Segment{
		Start: start, End: end, Score: 0.5, Kind: event.ModeRally,
		HitCount: len(hits), Hits: hits,
	}
}

// The complaint that drove the rule: a reel cut the dive before the shuttle
// landed, because the window started at the segment head and ran 8 s. The
// window must instead END at the last hit plus the landing tail, reaching
// back for its length.
func TestTrimEndsAtLastHitPlusTail(t *testing.T) {
	p := rallyPreset()
	seg := segWithHits(10, 30, 11.0, 12.0, 14.5, 18.0, 25.0, 28.5)

	start, end, atBoundary, anchor, ok := trimSegment(p, seg, 120, nil)
	if !ok {
		t.Fatal("expected a clip")
	}
	if atBoundary != 0 {
		t.Fatalf("no boundary given, got %v", atBoundary)
	}
	if anchor != anchorRallyEnd {
		t.Fatalf("anchor = %q, want %q", anchor, anchorRallyEnd)
	}
	wantEnd := 28.5 + 1.2 // last hit + rally_pad
	if end != 29.7 {
		t.Fatalf("end = %v, want 29.7 (last hit + tail)", end)
	}
	if start != 21.7 {
		t.Fatalf("start = %v, want 21.7 (end - 8 s)", start)
	}
	_ = wantEnd
}

// The natural end can sit past the segment's own edge when the segment pad
// already grew: end at the segment edge instead of inventing time.
func TestTrimClampsToSegmentEnd(t *testing.T) {
	p := rallyPreset()
	seg := segWithHits(10, 30, 10.5, 29.5) // last hit + 1.2 > 30

	start, end, _, _, ok := trimSegment(p, seg, 120, nil)
	if !ok {
		t.Fatal("expected a clip")
	}
	if end != 30 {
		t.Fatalf("end = %v, want the segment end 30", end)
	}
	if start != 22 {
		t.Fatalf("start = %v, want 22", start)
	}
}

// The remaining reel budget still caps a clip: a long rally yields its TAIL
// (the climax), not its head.
func TestTrimRemainingBudgetKeepsTail(t *testing.T) {
	p := rallyPreset()
	seg := segWithHits(10, 30, 12.0, 14.0, 28.0, 29.5)

	start, end, _, _, ok := trimSegment(p, seg, 3.0, nil) // only 3 s of budget left
	if !ok {
		t.Fatal("expected a clip")
	}
	if end != 30 {
		t.Fatalf("end = %v, want the segment end 30 (its pad holds the tail)", end)
	}
	if end-start != 3.0 {
		t.Fatalf("length = %v, want the remaining 3 s with the tail kept", end-start)
	}
}

// Scoreboard boundaries outrank the hit heuristic, exactly as before.
func TestTrimBoundaryStillWins(t *testing.T) {
	p := rallyPreset()
	seg := segWithHits(10, 30, 11.0, 28.5)

	start, end, atBoundary, _, ok := trimSegment(p, seg, 120, []float64{20.0})
	if !ok {
		t.Fatal("expected a clip")
	}
	if atBoundary != 20.0 || end != 20.0 || start != 12.0 {
		t.Fatalf("boundary trim: start=%v end=%v at=%v", start, end, atBoundary)
	}
}

// Segments without hit times keep the head-anchored fallback (activity mode).
func TestTrimHeadAnchoredWithoutHits(t *testing.T) {
	p := rallyPreset()
	seg := event.Segment{Start: 10, End: 30}

	start, end, _, _, ok := trimSegment(p, seg, 120, nil)
	if !ok {
		t.Fatal("expected a clip")
	}
	if start != 10 || end != 18 {
		t.Fatalf("head-anchored: start=%v end=%v", start, end)
	}
}
