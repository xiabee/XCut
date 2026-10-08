package event

import (
	"math"
	"testing"

	"github.com/xiabee/XCut/internal/analysis"
)

func presenceTrack(kind string, v float64) analysis.FeatureTrack {
	tr := analysis.FeatureTrack{Kind: kind}
	for t := 0.0; t <= 15.0; t += 0.5 {
		tr.Samples = append(tr.Samples, analysis.Sample{T: t, V: v})
	}
	return tr
}

// seamRallyTracks assembles the one-rally recipe the rally tests use, plus
// whatever presence tracks the caller wants beside it.
func seamRallyTracks(presence ...analysis.FeatureTrack) []analysis.FeatureTrack {
	tracks := []analysis.FeatureTrack{
		*track("frame_diff", [][2]float64{{0, 0.2}, {5, 0.2}, {10, 0.2}}),
		*hitsAt(5, 5.5, 6, 6.5, 7),
	}
	return append(tracks, presence...)
}

// TestSegmentGateReadsThePhase1PresenceTrack: the gate's threshold is
// calibrated on the phase-1 patch-match scale, so the phase-1 track is what
// segments carry — a band-model track beside it (a different scale with its
// own Kind) must neither feed the gate nor shadow it.
func TestSegmentGateReadsThePhase1PresenceTrack(t *testing.T) {
	tracks := seamRallyTracks(
		presenceTrack("player_presence", 0.75),
		presenceTrack("player_presence_mr", 0.05),
	)
	segs, _, err := Build(tracks, 15, rallyConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) == 0 {
		t.Fatal("expected a rally from the seam recipe")
	}
	withPresence := 0
	for _, s := range segs {
		if !s.HasPlayerPresence {
			continue
		}
		withPresence++
		if math.Abs(s.PlayerPresence-0.75) > 0.01 {
			t.Fatalf("rally carries presence %.3f, want the phase-1 track's 0.75", s.PlayerPresence)
		}
	}
	if withPresence == 0 {
		t.Fatal("no rally carried presence — the gate never saw the phase-1 track")
	}
}

// TestBandModelTrackDoesNotFeedTheGate: with only the band-model track
// present, rallies carry no presence at all — an uncalibrated scale must not
// silently become the gate's input.
func TestBandModelTrackDoesNotFeedTheGate(t *testing.T) {
	tracks := seamRallyTracks(presenceTrack("player_presence_mr", 0.9))
	segs, _, err := Build(tracks, 15, rallyConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) == 0 {
		t.Fatal("expected a rally from the seam recipe")
	}
	for _, s := range segs {
		if s.HasPlayerPresence {
			t.Fatalf("the band track leaked into the phase-1-calibrated gate: %+v", s)
		}
	}
}
