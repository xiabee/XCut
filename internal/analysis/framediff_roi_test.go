package analysis

import (
	"context"
	"log/slog"
	"math"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/testmedia"
)

func TestROIValid(t *testing.T) {
	good := ROI{X: 0.1, Y: 0.1, W: 0.5, H: 0.6}
	if !good.Valid() {
		t.Error("valid ROI rejected")
	}
	bad := []ROI{
		{X: -0.1, Y: 0, W: 0.5, H: 0.5},
		{X: 0, Y: 0, W: 0, H: 0.5},
		{X: 0.6, Y: 0, W: 0.5, H: 0.5}, // x+w > 1
		{X: 0, Y: 0.6, W: 0.5, H: 0.5}, // y+h > 1
		{X: 0, Y: 0, W: 0.5, H: 1.5},
		{X: math.NaN(), Y: 0, W: 0.5, H: 0.5},
	}
	for i, r := range bad {
		if r.Valid() {
			t.Errorf("bad ROI %d accepted: %+v", i, r)
		}
	}
}

func TestROIKeyStable(t *testing.T) {
	a := ROI{X: 0.1, Y: 0.2, W: 0.5, H: 0.4}
	b := ROI{X: 0.1, Y: 0.2, W: 0.5, H: 0.4}
	c := ROI{X: 0.1, Y: 0.2, W: 0.6, H: 0.4} // 4-decimal key precision by design
	if a.key() != b.key() {
		t.Error("identical ROIs must produce identical keys")
	}
	if a.key() == c.key() {
		t.Error("different ROIs must produce different keys")
	}
	an := FrameDiffROIAnalyzer{ROI: a}
	if an.Name() != "frame_diff_roi[x0.1000_y0.2000_w0.5000_h0.4000]" {
		t.Errorf("analyzer name %q", an.Name())
	}
}

func TestROIFilterExprEvenAlignment(t *testing.T) {
	r := ROI{X: 0.25, Y: 0.25, W: 0.5, H: 0.5}
	got := r.filterExpr()
	for _, want := range []string{"trunc(iw*0.5/2)*2", "trunc(ih*0.5/2)*2", "trunc(iw*0.25/2)*2"} {
		if !strings.Contains(got, want) {
			t.Errorf("filter expr %q missing %q", got, want)
		}
	}
}

// TestFrameDiffROIAnalyzerIsolatesMotion verifies against a real fixture
// where motion lives only in the top-left quadrant: the ROI track must see
// substantially more motion than the full-frame track.
func TestFrameDiffROIAnalyzerIsolatesMotion(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	root := t.TempDir()
	path, err := testmedia.GenerateMotionCorner(root, "corner.mp4", 320, 240, 10, 6)
	if err != nil {
		t.Fatal(err)
	}

	opts := Options{
		Tools:         testOnsetOptions().Tools,
		SampleFPS:     2,
		AnalysisWidth: 320,
	}
	full, err := (FrameDiffAnalyzer{}).Analyze(context.Background(), opts, path, false, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	roi := ROI{X: 0, Y: 0, W: 0.5, H: 0.5}
	roiTrack, err := (FrameDiffROIAnalyzer{ROI: roi}).Analyze(context.Background(), opts, path, false, slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	fullMean := trackMean(full[0].Samples)
	roiMean := trackMean(roiTrack[0].Samples)
	if roiMean <= 0 {
		t.Fatal("ROI over moving corner sees no motion")
	}
	// The moving corner fills a quarter of the frame; full-frame motion is
	// diluted roughly by area. The chroma-aware full-frame metric (max of
	// Y/U/V) also sees encoder chroma noise on the static background and the
	// corner's chroma footprint bleeding past the ROI through 420
	// subsampling, so the clean 4x area dilution lands near 2x in practice.
	// Require a clear 1.5x margin over that noise.
	if fullMean <= 0 || roiMean/fullMean < 1.5 {
		t.Errorf("ROI isolation weak: roi=%g full=%g ratio=%g",
			roiMean, fullMean, roiMean/math.Max(fullMean, 1e-9))
	}
}

func trackMean(samples []Sample) float64 {
	if len(samples) == 0 {
		return 0
	}
	sum := 0.0
	for _, s := range samples {
		sum += s.V
	}
	return sum / float64(len(samples))
}
