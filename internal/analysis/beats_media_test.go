package analysis

import (
	"context"
	"io"
	"log/slog"
	"math"
	"testing"

	"github.com/xiabee/XCut/internal/testmedia"
)

// TestBeatGridFromRealClickAudio closes the gap between "the estimator works on a
// list of numbers" and "the onset times the analyzer actually reports from a
// media file carry the grid": a click fixture whose truth is known by
// construction, the shipped onset analyzer, then the estimator.
func TestBeatGridFromRealClickAudio(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	const period = 0.5 // 120 BPM, placed by the fixture itself
	path, err := testmedia.GenerateRally(t.TempDir(), "clicks.mp4", 320, 240, 25, 12,
		[]testmedia.RallySpec{{Start: 0, End: 12, HitEvery: period}})
	if err != nil {
		t.Fatal(err)
	}

	res, err := AudioOnsetAnalyzer{}.Analyze(context.Background(), testOnsetOptions(), path, true,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	var track *FeatureTrack
	for i := range res {
		if res[i].Kind == "audio_onset" {
			track = &res[i]
		}
	}
	if track == nil {
		t.Fatalf("no audio_onset track in the result: %+v", res)
	}
	onsets := make([]float64, 0, len(track.Samples))
	for _, s := range track.Samples {
		onsets = append(onsets, s.T)
	}
	if len(onsets) < 8 {
		t.Fatalf("a 0.5 s click train yielded %d onsets (%v) — the fixture or the analyzer changed", len(onsets), onsets)
	}

	grid, ok := EstimateBeatGrid(onsets, 12)
	if !ok {
		t.Fatalf("no grid from %d real onsets: %v", len(onsets), onsets)
	}
	if math.Abs(grid.Period-period)/period > 0.1 {
		t.Fatalf("period %.4f from real audio, want %.4f within 10%% (onsets %v)", grid.Period, period, onsets)
	}
	for _, b := range grid.Beats {
		off := math.Mod(b, period)
		if off > period/2 {
			off = period - off
		}
		if off > 0.15*period {
			t.Fatalf("beat %.3f sits %.3f s off the constructed %.1f s grid", b, off, period)
		}
	}
	t.Logf("real-audio grid: period=%.4f bpm=%.1f coverage=%.2f beats=%d onsets=%d",
		grid.Period, grid.BPM, grid.Coverage, len(grid.Beats), len(onsets))
}
