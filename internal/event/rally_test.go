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
	segs, err := Build(tracks, 40, cfg)
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
	segs, err := Build(tracks, 35, cfg)
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
	segs, err := Build(tracks, 20, cfg)
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
	segs, err := Build(tracks, 15, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 0 {
		t.Fatalf("ROI-gated rally should be suppressed, got %+v", segs)
	}

	cfg.MotionTrack = ""
	segs, err = Build(tracks, 15, cfg)
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
	segs, err := Build(tracks, 15, cfg)
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
