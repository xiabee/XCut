package event

import (
	"math"
	"math/rand"
	"reflect"
	"testing"

	"github.com/xiabee/XCut/internal/analysis"
)

func motionTrack(samples ...[2]float64) analysis.FeatureTrack {
	t := analysis.FeatureTrack{Analyzer: "frame_diff", Version: 1, Kind: "frame_diff", Unit: "ratio"}
	for _, s := range samples {
		t.Samples = append(t.Samples, analysis.Sample{T: s[0], V: s[1]})
	}
	return t
}

func audioTrack(samples ...[2]float64) analysis.FeatureTrack {
	t := analysis.FeatureTrack{Analyzer: "audio_rms", Version: 1, Kind: "audio_rms_db", Unit: "dBFS"}
	for _, s := range samples {
		t.Samples = append(t.Samples, analysis.Sample{T: s[0], V: s[1]})
	}
	return t
}

// grid builds n samples at 0.5s spacing with the given values.
func grid(vals []float64) []analysis.Sample {
	out := make([]analysis.Sample, len(vals))
	for i, v := range vals {
		out[i] = analysis.Sample{T: float64(i) * 0.5, V: v}
	}
	return out
}

func TestBuildActiveSegments(t *testing.T) {
	// Quiet(0.01) → active(0.2 ×4) → quiet → active long block.
	motion := motionTrack()
	motion.Samples = grid([]float64{
		0.01, 0.01, // 0–1s inactive
		0.20, 0.25, 0.15, 0.10, // 1–3s active
		0.01, 0.01, // 3–4s inactive
		0.30, 0.30, 0.30, 0.30, // 4–6s active (would be a cut too: 0.30 > 0.28)
	})
	segs, err := Build([]analysis.FeatureTrack{motion}, 6.0, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) == 0 {
		t.Fatal("expected segments")
	}
	for _, s := range segs {
		if s.Start < 0 || s.End > 6.0 {
			t.Fatalf("segment out of range: %+v", s)
		}
		if s.End <= s.Start {
			t.Fatalf("empty segment: %+v", s)
		}
	}
	// The 0.30 values exceed CutThreshold: a cut must split, so no single
	// segment may span across the 4s boundary.
	for _, s := range segs {
		if s.Start < 3.5 && s.End > 4.5 {
			t.Fatalf("segment spans a cut: %+v", s)
		}
	}
}

func TestBuildMergesSmallGaps(t *testing.T) {
	// Two active islands separated by ONE inactive sample (0.5s gap < 0.8).
	motion := motionTrack()
	motion.Samples = grid([]float64{
		0.20, 0.20, 0.01, 0.20, 0.20,
	})
	segs, err := Build([]analysis.FeatureTrack{motion}, 2.5, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 1 {
		t.Fatalf("expected 1 merged segment, got %d: %+v", len(segs), segs)
	}
}

func TestBuildDropsShortSegments(t *testing.T) {
	motion := motionTrack()
	motion.Samples = grid([]float64{
		0.01, 0.01, 0.5, 0.01, 0.01, // single active sample → 0.5s < MinDuration
	})
	segs, err := Build([]analysis.FeatureTrack{motion}, 2.5, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 0 {
		t.Fatalf("expected 0 segments, got %d", len(segs))
	}
}

func TestBuildAudioDrivesActivity(t *testing.T) {
	// Flat video (no motion) with loud audio → active via audio.
	motion := motionTrack()
	motion.Samples = grid([]float64{0.0, 0.0, 0.0, 0.0})
	audio := audioTrack()
	audio.Samples = grid([]float64{-12, -12, -12, -12})
	segs, err := Build([]analysis.FeatureTrack{motion, audio}, 2.0, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 1 {
		t.Fatalf("expected 1 audio-driven segment, got %d", len(segs))
	}
	if segs[0].MeanAudioDB <= DefaultConfig().SilenceDB {
		t.Fatalf("audio stats not accumulated: %+v", segs[0])
	}
}

func TestBuildDigitalSilence(t *testing.T) {
	// -inf RMS values (digital silence) must not be "audible".
	motion := motionTrack()
	motion.Samples = grid([]float64{0.0, 0.0})
	audio := audioTrack([2]float64{0, math.Inf(-1)}, [2]float64{0.5, math.Inf(-1)})
	segs, err := Build([]analysis.FeatureTrack{motion, audio}, 1.0, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 0 {
		t.Fatalf("silence should not be active, got %+v", segs)
	}
}

func TestBuildDeterministic(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	motion := motionTrack()
	for i := 0; i < 200; i++ {
		motion.Samples = append(motion.Samples,
			analysis.Sample{T: float64(i) * 0.5, V: rng.Float64()})
	}
	audio := audioTrack()
	for i := 0; i < 200; i++ {
		audio.Samples = append(audio.Samples,
			analysis.Sample{T: float64(i) * 0.5, V: -60 + 50*rng.Float64()})
	}
	tracks := []analysis.FeatureTrack{motion, audio}
	a, err := Build(tracks, 100, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	b, err := Build(tracks, 100, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatal("event build is not deterministic")
	}
}

func TestBuildErrors(t *testing.T) {
	if _, err := Build(nil, 10, DefaultConfig()); err == nil {
		t.Fatal("expected error for missing tracks")
	}
	motion := motionTrack()
	motion.Samples = grid([]float64{0.1})
	if _, err := Build([]analysis.FeatureTrack{motion}, -1, DefaultConfig()); err == nil {
		t.Fatal("expected error for bad duration")
	}
	bad := DefaultConfig()
	bad.MergeGap = -5
	if _, err := Build([]analysis.FeatureTrack{motion}, 10, bad); err == nil {
		t.Fatal("expected error for bad config")
	}
}
