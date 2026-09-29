package pipeline

import (
	"io"
	"log/slog"
	"math/rand"
	"testing"

	"github.com/xiabee/XCut/internal/analysis"
	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/style"
)

func beatGridLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func onsetTrack(times []float64) analysis.FeatureTrack {
	tr := analysis.FeatureTrack{Kind: "audio_onset"}
	for _, t := range times {
		tr.Samples = append(tr.Samples, analysis.Sample{T: t, V: 1})
	}
	return tr
}

// TestBeatGridForStaysNilWhenTheStyleDoesNotSnap pins the first gate: a preset
// with no snap tolerance must come back nil even when the audio carries a
// perfect grid, because the caller treats nil as "select exactly as before".
func TestBeatGridForStaysNilWhenTheStyleDoesNotSnap(t *testing.T) {
	var onsets []float64
	for t0 := 0.5; t0 <= 14.0; t0 += 0.5 {
		onsets = append(onsets, t0)
	}
	res := &analysis.Result{Tracks: []analysis.FeatureTrack{onsetTrack(onsets)}}
	asset := &storage.Asset{DurationSec: 14.3}

	beats := beatGridFor(beatGridLogger(), res, asset, &style.Preset{})
	if beats != nil {
		t.Fatalf("a snapping-less style produced %d beats; want nil so selection is untouched", len(beats))
	}
}

// TestBeatGridForReadsOnlyTheOnsetTrack pins the track filter: any timed track
// on a fixture looks like onsets if the filter is dropped, and a regular frame
// diff would happily found a grid. A fixture whose only track is frame_diff
// must therefore produce nothing.
func TestBeatGridForReadsOnlyTheOnsetTrack(t *testing.T) {
	tr := analysis.FeatureTrack{Kind: "frame_diff"}
	for t0 := 0.5; t0 <= 14.0; t0 += 0.5 {
		tr.Samples = append(tr.Samples, analysis.Sample{T: t0, V: 3})
	}
	res := &analysis.Result{Tracks: []analysis.FeatureTrack{tr}}
	asset := &storage.Asset{DurationSec: 14.3}
	preset := &style.Preset{BeatSnapTolerance: 0.25}

	beats := beatGridFor(beatGridLogger(), res, asset, preset)
	if beats != nil {
		t.Fatalf("a frame_diff track founded a %d-beat grid; only audio_onset may", len(beats))
	}
}

// TestBeatGridForReturnsTheFittedLattice is the positive arm: a jittered click
// train goes in, an even 0.5 s lattice comes out. The spacing bound is what
// separates grid beats from the onsets they were fitted to — the onsets carry
// ±30 ms of jitter, the projection none — so returning the raw onsets cannot
// pass it, and neither can a fit that landed on the wrong period.
func TestBeatGridForReturnsTheFittedLattice(t *testing.T) {
	rnd := rand.New(rand.NewSource(11))
	var onsets []float64
	for t0 := 0.5; t0 <= 14.0; t0 += 0.5 {
		j := (rnd.Float64() - 0.5) * 0.06 // ±30 ms, the estimator's own jitter case
		onsets = append(onsets, t0+j)
	}
	res := &analysis.Result{Tracks: []analysis.FeatureTrack{onsetTrack(onsets)}}
	asset := &storage.Asset{DurationSec: 14.3}
	preset := &style.Preset{BeatSnapTolerance: 0.25}

	beats := beatGridFor(beatGridLogger(), res, asset, preset)
	if len(beats) < 20 {
		t.Fatalf("a 28-onset click train produced only %d beats", len(beats))
	}
	for i := 1; i < len(beats); i++ {
		gap := beats[i] - beats[i-1]
		if diff := gap - 0.5; diff > 0.005 || diff < -0.005 {
			t.Fatalf("beat %d sits %.4f from its neighbour; the projected lattice must be even, not the jittered onsets", i, gap)
		}
		if beats[i] > asset.DurationSec {
			t.Fatalf("beat %.4f exceeds the asset's %.1f s", beats[i], asset.DurationSec)
		}
	}
}

// TestBeatGridForRefusesAnIrregularTrain holds the analysis-level refusal at
// the pipeline seam: crowd noise with no grid must reach the caller as nil,
// not as a confident wrong lattice.
func TestBeatGridForRefusesAnIrregularTrain(t *testing.T) {
	rnd := rand.New(rand.NewSource(7))
	var onsets []float64
	t0 := 0.0
	for i := 0; i < 300; i++ {
		t0 += 0.05 + rnd.Float64()*1.4
		if t0 > 60 {
			break
		}
		onsets = append(onsets, t0)
	}
	res := &analysis.Result{Tracks: []analysis.FeatureTrack{onsetTrack(onsets)}}
	asset := &storage.Asset{DurationSec: 60}
	preset := &style.Preset{BeatSnapTolerance: 0.25}

	beats := beatGridFor(beatGridLogger(), res, asset, preset)
	if beats != nil {
		t.Fatalf("%d irregular onsets produced %d beats; an ungridded train must stay nil", len(onsets), len(beats))
	}
}
