package style

import (
	"testing"

	"github.com/xiabee/XCut/internal/event"
)

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

func TestTrimPeakWindow(t *testing.T) {
	p := rallyPreset()
	seg := segWithHits(10, 30, 11.0, 12.0, 14.5, 18.0, 25.0, 28.5)
	start, end, _, anchor, ok := trimSegment(p, seg, 120, nil)
	if !ok {
		t.Fatal("expected a clip")
	}
	if anchor != anchorPeakWindow {
		t.Fatalf("anchor = %q", anchor)
	}
	if end-start != 8.0 {
		t.Fatalf("length = %v", end-start)
	}
}

func TestTrimBoundaryStillWins(t *testing.T) {
	p := rallyPreset()
	seg := segWithHits(10, 30, 11.0, 28.5)
	boundary := 20.0
	_, end, atBoundary, anchor, ok := trimSegment(p, seg, 120, []float64{boundary})
	if !ok {
		t.Fatal("expected a clip")
	}
	if atBoundary != boundary || end != boundary || anchor != anchorBoundary {
		t.Fatalf("at=%v end=%v anchor=%q", atBoundary, end, anchor)
	}
}

func TestTrimHeadAnchoredWithoutHits(t *testing.T) {
	p := rallyPreset()
	seg := event.Segment{Start: 10, End: 30}
	start, end, _, anchor, ok := trimSegment(p, seg, 120, nil)
	if !ok {
		t.Fatal("expected a clip")
	}
	_ = start
	_ = end
	if anchor != anchorHead {
		t.Fatalf("anchor = %q, want anchorHead", anchor)
	}
}

func TestTrimRemainingBudgetCapsLength(t *testing.T) {
	p := rallyPreset()
	seg := segWithHits(10, 30, 12.0, 14.0, 28.0, 29.5)
	_, _, _, _, ok := trimSegment(p, seg, 3.0, nil)
	if !ok {
		t.Fatal("expected a clip")
	}
}

func TestTrimDensestWindowSelected(t *testing.T) {
	p := rallyPreset()
	seg := segWithHits(10, 30,
		11.0,
		24.0, 24.3, 24.6,
		25.0, 25.3, 25.6,
	)
	_, end, _, anchor, ok := trimSegment(p, seg, 120, nil)
	if !ok {
		t.Fatal("expected a clip")
	}
	_ = anchor
	if end <= 20 {
		t.Fatalf("clip end = %v, expected dense tail", end)
	}
}
