package pipeline

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/xiabee/XCut/internal/analysis"
	"github.com/xiabee/XCut/internal/event"
	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/player"
	"github.com/xiabee/XCut/internal/testmedia"
)

func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestPresenceTrackKindReachesTheSegmentGate is the witness for a seam that
// ran dark since the person filter shipped: the analyzer never set the
// track's Kind and the event builder's switch matches on Kind, so the
// measured presence never reached a segment and min_player_presence never
// bit on real data. The analyzer's actual output — both models, Kind set —
// fed straight into event.Build must land on segments.
func TestPresenceTrackKindReachesTheSegmentGate(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	dir := t.TempDir()
	path, err := testmedia.GenerateRally(dir, "match.mp4", 320, 240, 25, 14.3,
		[]testmedia.RallySpec{{Start: 0, End: 14, HitEvery: 0.5}})
	if err != nil {
		t.Fatal(err)
	}
	tools := media.Tools{FFmpeg: "ffmpeg", FFprobe: "ffprobe"}
	probe, err := media.ProbeFile(context.Background(), tools, path)
	if err != nil {
		t.Fatal(err)
	}
	sig, mr, _, err := player.MeasureSignature(context.Background(), tools, path,
		[]float64{0.3, 0.3, 0.2, 0.2}, 2, probe.DurationSec)
	if err != nil {
		t.Fatal(err)
	}
	if mr == nil {
		t.Fatal("the fixture's spot yielded no band model")
	}
	// The pipeline wires two analyzer instances — one per model — each
	// returning its own track; reproduce exactly that feed.
	phase1, err := analysis.PlayerPresenceAnalyzer{Sig: sig}.Analyze(
		context.Background(), analysis.Options{Tools: tools}, path, false, discardLog())
	if err != nil {
		t.Fatal(err)
	}
	band, err := analysis.PlayerPresenceAnalyzer{Sig: sig, MR: mr, SpotHPerW: 1.0}.Analyze(
		context.Background(), analysis.Options{Tools: tools}, path, false, discardLog())
	if err != nil {
		t.Fatal(err)
	}
	tracks := append(append([]analysis.FeatureTrack{}, phase1...), band...)
	if len(tracks) != 2 {
		t.Fatalf("got %d tracks, want the phase-1 and band tracks", len(tracks))
	}
	kinds := map[string]bool{}
	for _, tr := range tracks {
		if tr.Kind == "" {
			t.Fatalf("track %s/%s carries no Kind — the event switch cannot see it", tr.Analyzer, tr.Unit)
		}
		kinds[tr.Kind] = true
	}
	if !kinds["player_presence"] || !kinds["player_presence_mr"] {
		t.Fatalf("track kinds = %v, want player_presence and player_presence_mr", kinds)
	}

	// The gate's real path is rally mode (badminton_highlight is the style
	// that carries min_player_presence), so the feed is the rally recipe:
	// flat motion, hit onsets, then the analyzer's own presence tracks.
	motion := analysis.FeatureTrack{Kind: "frame_diff"}
	for t := 0.0; t <= probe.DurationSec; t += 0.5 {
		motion.Samples = append(motion.Samples, analysis.Sample{T: t, V: 0.2})
	}
	var onsets analysis.FeatureTrack
	onsets.Kind = "audio_onset"
	for t := 5.0; t <= 10.0; t += 0.5 {
		onsets.Samples = append(onsets.Samples, analysis.Sample{T: t, V: 0.8})
	}
	feeds := append([]analysis.FeatureTrack{motion, onsets}, tracks...)
	cfg := event.Config{
		CutThreshold: 0.25, MotionFloor: 0.05, SilenceDB: -45,
		MergeGap: 1.2, MinDuration: 2.0,
		Mode: event.ModeRally, RallyGap: 2.0, RallyPad: 1.0, MinHits: 3,
	}
	segs, _, err := event.Build(feeds, probe.DurationSec, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) == 0 {
		t.Fatal("expected a rally from the seam feed")
	}
	withPresence := 0
	for _, s := range segs {
		if s.HasPlayerPresence {
			withPresence++
		}
	}
	if withPresence == 0 {
		t.Fatal("no rally carried presence — the analyzer's track still does not reach the gate")
	}

	// The same analyzer feed through the ACTIVITY builder: presence is
	// mode-independent data, and the gate must see it in vlog/KTV reels too
	// (the attachment lived rally-only until 2026-10-09).
	asegs, _, err := event.Build(feeds, probe.DurationSec, event.Config{
		CutThreshold: 0.25, MotionFloor: 0.05, SilenceDB: -45,
		MergeGap: 1.2, MinDuration: 2.0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(asegs) == 0 {
		t.Fatal("expected activity segments from the seam feed")
	}
	aWithPresence := 0
	for _, s := range asegs {
		if s.HasPlayerPresence {
			aWithPresence++
		}
	}
	if aWithPresence == 0 {
		t.Fatal("no activity segment carried presence — the analyzer's track reaches rallies only")
	}
}
