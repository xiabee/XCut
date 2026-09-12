package analysis

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/xcerr"
)

// signalstats diff keys printed by the frame_diff analyzer. Y alone misses
// chroma-only scene switches (red→green differs by ~0.16 in luma after
// scaling, ~0.7 in V), so the analyzer takes the max across all three
// planes. Y spans 0..255; U/V span 16..240 (224 wide).
const (
	yDifKey = "lavfi.signalstats.YDIF"
	uDifKey = "lavfi.signalstats.UDIF"
	vDifKey = "lavfi.signalstats.VDIF"

	yDifSpan  = 255.0
	uvDifSpan = 224.0
)

// FrameDiffAnalyzer measures per-sample-frame difference (signalstats
// YDIF/UDIF/VDIF, each normalized to 0..1, combined by max). It doubles as:
//   - a motion intensity signal (within-scene activity), and
//   - a cut detector (spikes at scene changes),
//
// which is why it emits one track consumed by both roles (DECISIONS log:
// single cheap pass instead of two overlapping filters).
type FrameDiffAnalyzer struct{}

func (FrameDiffAnalyzer) Name() string { return "frame_diff" }
func (FrameDiffAnalyzer) Version() int { return 2 }

func (a FrameDiffAnalyzer) Analyze(ctx context.Context, opts Options, path string, _ bool, log *slog.Logger) ([]FeatureTrack, error) {
	filter := fmt.Sprintf("fps=%s,scale=%d:-2,signalstats,"+
		"metadata=print:key=%s:file=-,metadata=print:key=%s:file=-,metadata=print:key=%s:file=-",
		formatFPS(opts.SampleFPS), opts.AnalysisWidth, yDifKey, uDifKey, vDifKey)

	out := newMetadataCollector(yDifKey, uDifKey, vDifKey)
	err := media.StreamStdout(ctx, opts.Tools.FFmpeg, out.sink,
		"-hide_banner", "-nostdin", "-v", "error",
		"-threads", strconv.Itoa(maxThreads(opts.Tools.Threads)),
		"-i", path,
		"-an", "-vf", filter,
		"-f", "null", "-",
	)
	if err != nil {
		if ctx.Err() != nil {
			return nil, xcerr.E(xcerr.CodeAnalyzerFailure, "video analysis timed out or was cancelled", ctx.Err())
		}
		return nil, xcerr.E(xcerr.CodeAnalyzerFailure, "video analysis failed", err)
	}

	perKey, err := out.finish()
	if err != nil {
		return nil, xcerr.E(xcerr.CodeAnalyzerFailure, "cannot parse video analysis output", err)
	}
	if len(perKey[yDifKey]) == 0 {
		return nil, xcerr.E(xcerr.CodeAnalyzerFailure,
			fmt.Sprintf("no %s samples in analyzer output (%d bytes streamed)", yDifKey, out.bytesSeen), nil)
	}
	samples := combineChannelDiffs(perKey[yDifKey], perKey[uDifKey], perKey[vDifKey])
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

// combineChannelDiffs merges the per-plane diff samples into one track:
// each sample time gets max(YDIF/255, UDIF/224, VDIF/224), clamped to [0,1].
// The Y list is the backbone (its times define the output); U/V contribute
// when a sample with the same time exists.
func combineChannelDiffs(y, u, v []Sample) []Sample {
	index := func(list []Sample) map[float64]float64 {
		m := make(map[float64]float64, len(list))
		for _, s := range list {
			m[s.T] = s.V
		}
		return m
	}
	um, vm := index(u), index(v)

	out := make([]Sample, 0, len(y))
	for _, s := range y {
		d := s.V / yDifSpan
		if uv, ok := um[s.T]; ok {
			d = max(d, uv/uvDifSpan)
		}
		if vv, ok := vm[s.T]; ok {
			d = max(d, vv/uvDifSpan)
		}
		if d > 1 {
			d = 1
		}
		out = append(out, Sample{T: s.T, V: d})
	}
	return out
}

// parseMetadataPrint parses a single key out of ffmpeg `metadata=print`
// output (used by the ROI analyzer, whose filter prints one key).
func parseMetadataPrint(out []byte, key string) ([]Sample, error) {
	perKey, err := parseMetadataPrintKeys(out, key)
	if err != nil {
		return nil, err
	}
	if len(perKey[key]) == 0 {
		return nil, fmt.Errorf("no %s samples in analyzer output (%d bytes)", key, len(out))
	}
	return perKey[key], nil
}

// parseMetadataPrintKeys parses ffmpeg `metadata=print` blocks for several
// keys at once:
//
//	frame:12   pts:12288      pts_time:3.078
//	lavfi.signalstats.YDIF=4.718750
//
// Each requested key gets its own sample list, sorted by time.
func parseMetadataPrintKeys(out []byte, keys ...string) (map[string][]Sample, error) {
	c := newMetadataCollector(keys...)
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		c.line(sc.Text())
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return c.samples, nil
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
