package analysis

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/xcerr"
)

// FrameDiffAnalyzer measures per-sample-frame luma difference (signalstats
// YDIF, normalized to 0..1). It doubles as:
//   - a motion intensity signal (within-scene activity), and
//   - a cut detector (spikes at scene changes),
// which is why it emits one track consumed by both roles (DECISIONS log:
// single cheap pass instead of two overlapping filters).
type FrameDiffAnalyzer struct{}

func (FrameDiffAnalyzer) Name() string    { return "frame_diff" }
func (FrameDiffAnalyzer) Version() int    { return 1 }

func (a FrameDiffAnalyzer) Analyze(ctx context.Context, opts Options, path string, _ bool, log *slog.Logger) ([]FeatureTrack, error) {
	filter := fmt.Sprintf("fps=%s,scale=%d:-2,signalstats,metadata=print:key=lavfi.signalstats.YDIF:file=-",
		formatFPS(opts.SampleFPS), opts.AnalysisWidth)

	out, errOut, err := media.Run(ctx, opts.Tools.FFmpeg,
		"-hide_banner", "-nostdin", "-v", "error",
		"-threads", strconv.Itoa(maxThreads(opts.Tools.Threads)),
		"-i", path,
		"-an", "-vf", filter,
		"-f", "null", "-",
	)
	if err != nil {
		if ctx.Err() != nil {
			return nil, xcerr.E(xcerr.CodeAnalyzerFailure, "video analysis timed out", ctx.Err())
		}
		return nil, xcerr.E(xcerr.CodeAnalyzerFailure, "video analysis failed", fmt.Errorf("%v: %s", err, tailBytes(errOut)))
	}

	samples, err := parseMetadataPrint(out, "lavfi.signalstats.YDIF")
	if err != nil {
		return nil, xcerr.E(xcerr.CodeAnalyzerFailure, "cannot parse video analysis output", err)
	}
	// Normalize YDIF (0..255 for 8-bit luma) to 0..1.
	for i := range samples {
		samples[i].V /= 255.0
	}
	track := FeatureTrack{
		Analyzer: a.Name(),
		Version:  a.Version(),
		Kind:     "frame_diff",
		Unit:     "ratio",
		Samples:  samples,
	}
	log.Debug("frame_diff analyzed", "samples", len(samples))
	return []FeatureTrack{track}, nil
}

// parseMetadataPrint parses ffmpeg `metadata=print` output pairs:
//
//	frame:12   pts:12288      pts_time:3.078
//	lavfi.signalstats.YDIF=4.718750
//
// Only entries for the requested key are returned, sorted by time.
func parseMetadataPrint(out []byte, key string) ([]Sample, error) {
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	var samples []Sample
	pendingT := -1.0
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "frame:") {
			t, ok := extractPtsTime(line)
			if !ok {
				pendingT = -1
				continue
			}
			pendingT = t
			continue
		}
		if pendingT < 0 {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok && strings.TrimSpace(k) == key {
			f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err != nil {
				continue // tolerate single malformed lines
			}
			samples = append(samples, Sample{T: pendingT, V: f})
			pendingT = -1
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(samples) == 0 {
		return nil, fmt.Errorf("no %s samples in analyzer output (%d bytes)", key, len(out))
	}
	return samples, nil
}

// extractPtsTime pulls the trailing "pts_time:<value>" from a frame line.
func extractPtsTime(line string) (float64, bool) {
	const tag = "pts_time:"
	i := strings.LastIndex(line, tag)
	if i < 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(line[i+len(tag):]), 64)
	if err != nil || v < 0 {
		return 0, false
	}
	return v, true
}

func formatFPS(fps float64) string {
	return strconv.FormatFloat(fps, 'f', -1, 64)
}

func maxThreads(n int) int {
	if n <= 0 {
		return 2
	}
	return n
}
