package pipeline

import (
	"testing"

	"github.com/xiabee/XCut/internal/analysis"
	"github.com/xiabee/XCut/internal/event"
	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/style"
)

// TestAssetAnalyzersSelection pins the per-source ROI override rule: the
// asset's own rect wins over the preset's court region, assets without one
// fall back to the preset (or the untouched baseline), and an invalid
// stored rect fails loudly instead of silently analyzing a garbage crop.
func TestAssetAnalyzersSelection(t *testing.T) {
	base := []analysis.Analyzer{analysis.FrameDiffAnalyzer{}}
	presetROI := &style.MotionROI{X: 0.0, Y: 0.0, W: 1.0, H: 1.0}
	preset := &style.Preset{Name: "p", MotionROI: presetROI}
	assetROI := &storage.MotionROI{X: 0.1, Y: 0.2, W: 0.5, H: 0.6}
	plain := &style.Preset{Name: "plain"}

	roiOf := func(ans []analysis.Analyzer) *analysis.ROI {
		for _, a := range ans {
			if r, ok := a.(analysis.FrameDiffROIAnalyzer); ok {
				return &r.ROI
			}
		}
		return nil
	}

	// Per-source override.
	run, err := assetAnalyzers(base, preset, &storage.Asset{MotionROI: assetROI})
	if err != nil {
		t.Fatal(err)
	}
	if got := roiOf(run); got == nil || *got != (analysis.ROI{X: 0.1, Y: 0.2, W: 0.5, H: 0.6}) {
		t.Fatalf("asset roi must override the preset, got %+v", got)
	}

	// Fallback to the preset's court region.
	run, err = assetAnalyzers(base, preset, &storage.Asset{})
	if err != nil {
		t.Fatal(err)
	}
	if got := roiOf(run); got == nil || *got != (analysis.ROI{X: 0, Y: 0, W: 1, H: 1}) {
		t.Fatalf("preset roi fallback broken, got %+v", got)
	}

	// Neither → exactly the baseline slice.
	run, err = assetAnalyzers(base, plain, &storage.Asset{})
	if err != nil {
		t.Fatal(err)
	}
	if len(run) != len(base) || roiOf(run) != nil {
		t.Fatalf("plain asset must run the bare baseline, got %+v", run)
	}

	// A corrupt stored rect refuses to analyze.
	if _, err := assetAnalyzers(base, plain, &storage.Asset{
		MotionROI: &storage.MotionROI{X: 0.9, Y: 0.9, W: 0.5, H: 0.5},
	}); err == nil {
		t.Fatal("invalid asset roi must error")
	}
}

// TestEventConfigFor pins the motion-source upgrade: an asset with its own
// ROI segments from the ROI track when the preset left the source on the
// default, and a preset-set motion_track is never overridden.
func TestEventConfigFor(t *testing.T) {
	assetROI := &storage.MotionROI{X: 0.1, Y: 0.1, W: 0.5, H: 0.5}
	withROI := &storage.Asset{MotionROI: assetROI}
	plain := &storage.Asset{}

	// Default source + per-source ROI → upgraded to the ROI kind.
	cfg := eventConfigFor(&style.Preset{Name: "p"}, withROI)
	if cfg.MotionTrack != "frame_diff_roi" {
		t.Fatalf("roi asset must segment from frame_diff_roi, got %q", cfg.MotionTrack)
	}
	// Default source, no asset ROI → untouched default.
	cfg = eventConfigFor(&style.Preset{Name: "p"}, plain)
	if cfg.MotionTrack != "" {
		t.Fatalf("plain asset must keep the default, got %q", cfg.MotionTrack)
	}
	// Preset explicitly set a source → precedence over the upgrade.
	cfg = eventConfigFor(&style.Preset{
		Name: "p", EventConfig: event.Config{MotionTrack: "frame_diff"},
	}, withROI)
	if cfg.MotionTrack != "frame_diff" {
		t.Fatalf("preset motion_track must win, got %q", cfg.MotionTrack)
	}
}
