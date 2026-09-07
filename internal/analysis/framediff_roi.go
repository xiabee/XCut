package analysis

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strconv"

	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/xcerr"
)

// ROI is a normalized rectangular region of interest (0..1 each).
// It lets a preset restrict motion analysis to e.g. the court area so
// crowd movement behind the baseline cannot dominate the signal.
type ROI struct {
	X, Y, W, H float64
}

// Valid reports whether the rect is inside the unit square and non-empty.
func (r ROI) Valid() bool {
	for _, v := range []float64{r.X, r.Y, r.W, r.H} {
		if math.IsNaN(v) || v < 0 {
			return false
		}
	}
	return r.W > 0 && r.H > 0 && r.X+r.W <= 1+1e-9 && r.Y+r.H <= 1+1e-9
}

// key renders the ROI as a stable, cache-safe string.
func (r ROI) key() string {
	return fmt.Sprintf("x%.4f_y%.4f_w%.4f_h%.4f", r.X, r.Y, r.W, r.H)
}

// filterExpr renders the ffmpeg crop parameters for the ROI at any input
// size. Dimensions round to even (yuv420p chroma alignment); the ≤1px loss
// is noise for analysis.
func (r ROI) filterExpr() string {
	return fmt.Sprintf(
		"w='trunc(iw*%s/2)*2':h='trunc(ih*%s/2)*2':x='trunc(iw*%s/2)*2':y='trunc(ih*%s/2)*2'",
		strconv.FormatFloat(r.W, 'f', -1, 64),
		strconv.FormatFloat(r.H, 'f', -1, 64),
		strconv.FormatFloat(r.X, 'f', -1, 64),
		strconv.FormatFloat(r.Y, 'f', -1, 64))
}

// FrameDiffROIAnalyzer measures luma difference inside a normalized region
// of interest (crop → signalstats YDIF). Emits the "frame_diff_roi" kind.
// The ROI is part of the analyzer *name*, so different regions produce
// different cache keys and can never cross-contaminate (cacheKey hashes
// analyzer names).
type FrameDiffROIAnalyzer struct {
	ROI ROI
}

func (a FrameDiffROIAnalyzer) Name() string {
	return "frame_diff_roi[" + a.ROI.key() + "]"
}
func (a FrameDiffROIAnalyzer) Version() int { return 1 }
func (FrameDiffROIAnalyzer) Kind() string   { return "frame_diff_roi" }

func (a FrameDiffROIAnalyzer) Analyze(ctx context.Context, opts Options, path string, _ bool, log *slog.Logger) ([]FeatureTrack, error) {
	if !a.ROI.Valid() {
		return nil, xcerr.E(xcerr.CodeValidation, "invalid motion ROI", nil)
	}
	filter := fmt.Sprintf("fps=%s,scale=%d:-2,crop=%s,signalstats,metadata=print:key=lavfi.signalstats.YDIF:file=-",
		formatFPS(opts.SampleFPS), opts.AnalysisWidth, a.ROI.filterExpr())

	out, errOut, err := media.Run(ctx, opts.Tools.FFmpeg,
		"-hide_banner", "-nostdin", "-v", "error",
		"-threads", strconv.Itoa(maxThreads(opts.Tools.Threads)),
		"-i", path,
		"-an", "-vf", filter,
		"-f", "null", "-",
	)
	if err != nil {
		if ctx.Err() != nil {
			return nil, xcerr.E(xcerr.CodeAnalyzerFailure, "ROI motion analysis timed out", ctx.Err())
		}
		return nil, xcerr.E(xcerr.CodeAnalyzerFailure, "ROI motion analysis failed",
			fmt.Errorf("%v: %s", err, tailBytes(errOut)))
	}

	samples, err := parseMetadataPrint(out, "lavfi.signalstats.YDIF")
	if err != nil {
		return nil, xcerr.E(xcerr.CodeAnalyzerFailure, "cannot parse ROI motion analysis output", err)
	}
	for i := range samples {
		samples[i].V /= 255.0
	}
	track := FeatureTrack{
		Analyzer: a.Name(),
		Version:  a.Version(),
		Kind:     "frame_diff_roi",
		Unit:     "ratio",
		Samples:  samples,
	}
	log.Debug("frame_diff_roi analyzed", "roi", a.ROI.key(), "samples", len(samples))
	return []FeatureTrack{track}, nil
}
