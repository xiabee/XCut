package event

import (
	"math"
	"testing"

	"github.com/xiabee/XCut/internal/analysis"
)

// track builds a feature track kind with samples at even spacing.
func track(kind string, xs [][2]float64) *analysis.FeatureTrack {
	t := &analysis.FeatureTrack{Kind: kind}
	for _, x := range xs {
		t.Samples = append(t.Samples, analysis.Sample{T: x[0], V: x[1]})
	}
	return t
}

func rallyConfig() Config {
	return Config{
		CutThreshold: 0.25, MotionFloor: 0.05, SilenceDB: -45,
		MergeGap: 1.2, MinDuration: 2.0,
		Mode: ModeRally, RallyGap: 2.0, RallyPad: 1.0, MinHits: 3,
	}
}

// motionTrack returns constant motion across [0, duration].
func flatMotion(duration, v float64) *analysis.FeatureTrack {
	var xs [][2]float64
	for t := 0.0; t <= duration; t += 0.5 {
		xs = append(xs, [2]float64{t, v})
	}
	return track("frame_diff", xs)
}

func hitsAt(times ...float64) *analysis.FeatureTrack {
	var xs [][2]float64
	for _, t := range times {
		xs = append(xs, [2]float64{t, 0.8})
	}
	return track("audio_onset", xs)
}

func TestBuildRallyClustersHitsAndSplitsOnGaps(t *testing.T) {
	cfg := rallyConfig()
	// Rally 1: hits every 0.5s from 5..8 (7 hits). Gap, then rally 2 at
	// 30..32 (5 hits). The 24s gap splits them.
	onsets := hitsAt(5, 5.5, 6, 6.5, 7, 7.5, 8, 30, 30.5, 31, 31.5, 32)
	tracks := []analysis.FeatureTrack{
		*flatMotion(40, 0.15),
		*onsets,
	}
	segs, _, err := Build(tracks, 40, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 2 {
		t.Fatalf("got %d rallies, want 2: %+v", len(segs), segs)
	}
	// Padded windows: [4, 9] and [29, 33].
	if segs[0].Start != 4 || segs[0].End != 9 {
		t.Errorf("rally 1 = [%g,%g], want [4,9]", segs[0].Start, segs[0].End)
	}
	if segs[1].Start != 29 || segs[1].End != 33 {
		t.Errorf("rally 2 = [%g,%g], want [29,33]", segs[1].Start, segs[1].End)
	}
	if segs[0].HitCount != 7 || segs[1].HitCount != 5 {
		t.Errorf("hit counts %d/%d, want 7/5", segs[0].HitCount, segs[1].HitCount)
	}
	density := segs[0].HitDensity
	if math.Abs(density-7.0/5.0) > 1e-6 {
		t.Errorf("density %g, want 1.4", density)
	}
	if segs[0].Kind != ModeRally {
		t.Errorf("kind %q, want rally", segs[0].Kind)
	}
}

func TestBuildRallyDropsThinAndQuiet(t *testing.T) {
	cfg := rallyConfig()
	// 2 hits only (below min_hits=3), then 6 hits over static (motion 0.01
	// < motion_floor): both must be suppressed.
	onsets := hitsAt(5, 5.5, 20, 20.5, 21, 21.5, 22, 22.5)
	motion := track("frame_diff", [][2]float64{{0, 0.01}, {5, 0.01}, {10, 0.01},
		{15, 0.01}, {20, 0.01}, {25, 0.01}, {30, 0.01}})
	tracks := []analysis.FeatureTrack{*motion, *onsets}
	segs, _, err := Build(tracks, 35, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 0 {
		t.Fatalf("expected no rallies, got %+v", segs)
	}
}

func TestBuildRallyWithoutOnsetsYieldsNothing(t *testing.T) {
	cfg := rallyConfig()
	tracks := []analysis.FeatureTrack{*flatMotion(20, 0.2)}
	segs, _, err := Build(tracks, 20, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 0 {
		t.Fatalf("no onsets must mean no rallies, got %+v", segs)
	}
}

func TestBuildRallyRespectsMotionTrackPreference(t *testing.T) {
	cfg := rallyConfig()
	cfg.MotionTrack = "frame_diff_roi"
	// Full-frame motion is strong (would pass), ROI motion is weak: with the
	// preference the ROI track must gate the rally out.
	onsets := hitsAt(5, 5.5, 6, 6.5, 7)
	full := track("frame_diff", [][2]float64{{0, 0.2}, {5, 0.2}, {10, 0.2}})
	roi := track("frame_diff_roi", [][2]float64{{0, 0.01}, {5, 0.01}, {10, 0.01}})
	tracks := []analysis.FeatureTrack{*full, *roi, *onsets}
	segs, _, err := Build(tracks, 15, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 0 {
		t.Fatalf("ROI-gated rally should be suppressed, got %+v", segs)
	}

	cfg.MotionTrack = ""
	segs, _, err = Build(tracks, 15, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 1 {
		t.Fatalf("default motion track should pass, got %d rallies", len(segs))
	}
}

func TestBuildActivityModeAnnotatesOnsetDensity(t *testing.T) {
	// Activity mode stays activity (Kind "") but, when an onset track is
	// present, segments gain an honest transient count/density annotation.
	cfg := Config{CutThreshold: 0.25, MotionFloor: 0.05, SilenceDB: -45,
		MergeGap: 1.2, MinDuration: 2.0}
	onsets := hitsAt(5, 5.5, 6, 6.5, 7)
	tracks := []analysis.FeatureTrack{*flatMotion(15, 0.2), *onsets}
	segs, _, err := Build(tracks, 15, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) == 0 {
		t.Fatal("activity segmentation produced nothing")
	}
	var covered int
	for _, s := range segs {
		if s.Kind != "" {
			t.Fatalf("activity segment must not claim rally kind: %+v", s)
		}
		covered += s.HitCount
	}
	if covered != 5 {
		t.Fatalf("onset annotations across segments = %d, want 5", covered)
	}
}

func TestConfigRejectsBadModeAndRallyParams(t *testing.T) {
	cfg := rallyConfig()
	cfg.Mode = "magic"
	if err := cfg.Validate(); err == nil {
		t.Error("bad mode must be rejected")
	}
	cfg = rallyConfig()
	cfg.RallyGap = -1
	if err := cfg.Validate(); err == nil {
		t.Error("negative rally_gap must be rejected")
	}
	cfg = rallyConfig()
	if err := cfg.Validate(); err != nil {
		t.Errorf("valid rally config rejected: %v", err)
	}
}

func TestOnsetSamplesDedupAndSort(t *testing.T) {
	in := track("audio_onset", [][2]float64{{3, 0.5}, {1, 0.4}, {3, 0.5}, {2, 0.6}})
	got := onsetSamples(in)
	wantT := []float64{1, 2, 3}
	if len(got) != 3 {
		t.Fatalf("got %d samples, want 3", len(got))
	}
	for i, s := range got {
		if s.T != wantT[i] {
			t.Errorf("sample %d at %g, want %g", i, s.T, wantT[i])
		}
	}
	if onsetSamples(nil) != nil {
		t.Error("nil track must give nil samples")
	}
}

// TestBuildRallyChunksContinuousPlay: real court audio fires on ambience
// through every break, so a long recording can be one dense onset stream.
// The detector must chunk it into consecutive rally-sized pieces — the old
// cap truncated the span and silently discarded everything past 30s.
func TestBuildRallyChunksContinuousPlay(t *testing.T) {
	cfg := rallyConfig()
	cfg.RallyGap = 2.5
	var times []float64
	for t := 0.5; t < 130.0; t += 0.4 {
		times = append(times, t)
	}
	tracks := []analysis.FeatureTrack{*flatMotion(130, 0.15), *hitsAt(times...)}
	segs, _, err := Build(tracks, 130, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) < 4 {
		t.Fatalf("got %d rallies, want chunked coverage of a 130s dense span", len(segs))
	}
	covered := 0.0
	for _, s := range segs {
		if s.Duration() > 30.0+1e-9 {
			t.Fatalf("rally %v exceeds the 30s rally size", s)
		}
		if s.HitCount < 3 {
			t.Fatalf("rally %v below min hits", s)
		}
		covered += s.Duration()
	}
	if covered < 120 {
		t.Fatalf("chunks cover only %gs of a 130s dense span", covered)
	}
}

// TestBuildRallySplitsOnLowDensityBreaks: a dip in onset density sustained
// past rally_gap closes the rally even though isolated noise hits keep
// arriving — absolute-quiet splits never fire on real court audio.
func TestBuildRallySplitsOnLowDensityBreaks(t *testing.T) {
	cfg := rallyConfig()
	cfg.RallyGap = 2.5
	var times []float64
	// Rally 1: dense 4..14 (0.4s spacing). Break: one stray hit every ~2s
	// (below exit rate) from 14..30. Rally 2: dense again 30..40.
	for t := 4.0; t <= 14.0; t += 0.4 {
		times = append(times, t)
	}
	for t := 15.0; t < 30.0; t += 2.0 {
		times = append(times, t)
	}
	for t := 30.0; t <= 40.0; t += 0.4 {
		times = append(times, t)
	}
	tracks := []analysis.FeatureTrack{*flatMotion(45, 0.15), *hitsAt(times...)}
	segs, _, err := Build(tracks, 45, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) < 2 {
		t.Fatalf("got %d rallies, want the two dense regions separated", len(segs))
	}
	if segs[0].End > 17 || segs[len(segs)-1].Start < 28 {
		t.Fatalf("rallies must hug the dense regions: first end %g, last start %g",
			segs[0].End, segs[len(segs)-1].Start)
	}
}

func TestValidateRejectsInvertedRallyRates(t *testing.T) {
	cfg := rallyConfig()
	cfg.RallyEnterRate = 0.5
	cfg.RallyExitRate = 1.0
	if err := cfg.Validate(); err == nil {
		t.Fatal("exit rate above enter rate must be rejected")
	}
	cfg = rallyConfig()
	cfg.RallyEnterRate, cfg.RallyExitRate = 1.0, 0.5
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid rates rejected: %v", err)
	}
}

// TestChunkBoundsExactMultiple: a span exactly 2x the max chunk size must
// split into 2 full chunks, not 3 ragged ones (ceil, not int+1).
func TestChunkBoundsExactMultiple(t *testing.T) {
	got := chunkBounds(10, 70, 30)
	if len(got) != 2 {
		t.Fatalf("chunks = %d (%v), want 2", len(got), got)
	}
	for _, ch := range got {
		if ch[1]-ch[0] > 30+1e-9 {
			t.Fatalf("chunk %v exceeds max 30", ch)
		}
	}
}

// TestBuildStatsNamesTheGate: the stats must say WHICH gate refused how
// much — "no events satisfy the style's clip constraints" is unactionable
// when motion-floor and min-hits rejections look identical.
func TestBuildStatsNamesTheGate(t *testing.T) {
	// Motion everywhere below the floor: every chunk reaches the scorer and
	// is refused by the motion gate, not by hit counting.
	cfg := rallyConfig()
	cfg.Mode = ModeRally
	quiet := []analysis.FeatureTrack{*flatMotion(40, 0.01), *hitsAt(5, 5.5, 6, 6.5, 7, 7.5, 8)}
	_, st, err := Build(quiet, 40, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if st.Segments != 0 {
		t.Fatalf("segments = %d, want 0 (all below the motion floor)", st.Segments)
	}
	if st.Onsets != 7 || st.SpansOpened != 1 {
		t.Fatalf("onsets/spans = %d/%d, want 7/1", st.Onsets, st.SpansOpened)
	}
	if st.ChunksConsidered == 0 || st.ChunksConsidered != st.ChunksDroppedMotionFloor {
		t.Fatalf("chunks %d considered, %d dropped by motion floor — counters disagree",
			st.ChunksConsidered, st.ChunksDroppedMotionFloor)
	}
	if st.ChunksDroppedMinHits != 0 || st.ChunksDroppedMinDuration != 0 {
		t.Fatalf("min_hits/min_duration drops = %d/%d, want 0/0",
			st.ChunksDroppedMinHits, st.ChunksDroppedMinDuration)
	}

	// Two hits only: the window opens (2 hits in one 2s window) but the span
	// holds fewer than minHits hits, so the span gate is what refused.
	sparse := []analysis.FeatureTrack{*flatMotion(40, 0.15), *hitsAt(5, 5.5)}
	_, st, err = Build(sparse, 40, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if st.Segments != 0 {
		t.Fatalf("segments = %d, want 0 (span below min hits)", st.Segments)
	}
	if st.SpansDroppedMinHits == 0 {
		t.Fatalf("spans_dropped_min_hits = 0, want >= 1")
	}
}
