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

// AudioAnalyzer produces per-window RMS loudness (dBFS) via astats.
// Silence/no-signal handling is left to consumers (event builder); assets
// carrying no audio stream simply yield no track.
type AudioAnalyzer struct{}

func (AudioAnalyzer) Name() string { return "audio_rms" }
func (AudioAnalyzer) Version() int { return 1 }

// windowSamples: 22050 samples @ 44100 Hz ⇒ 0.5 s RMS windows regardless of
// the container's native sample rate (we resample first).
const audioWindowSamples = 22050

func (a AudioAnalyzer) Analyze(ctx context.Context, opts Options, path string, hasAudio bool, log *slog.Logger) ([]FeatureTrack, error) {
	if !hasAudio {
		return nil, nil
	}
	filter := fmt.Sprintf(
		"aresample=44100,asetnsamples=n=%d:p=0,astats=metadata=1:reset=1,ametadata=print:key=lavfi.astats.Overall.RMS_level:file=-",
		audioWindowSamples)

	out := newMetadataCollector("lavfi.astats.Overall.RMS_level")
	err := media.StreamStdout(ctx, opts.Tools.FFmpeg, out.sink,
		"-hide_banner", "-nostdin", "-v", "error",
		"-threads", strconv.Itoa(maxThreads(opts.Tools.Threads)),
		"-i", path,
		"-vn", "-af", filter,
		"-f", "null", "-",
	)
	if err != nil {
		if ctx.Err() != nil {
			return nil, xcerr.E(xcerr.CodeAnalyzerFailure, "audio analysis timed out or was cancelled", ctx.Err())
		}
		return nil, xcerr.E(xcerr.CodeAnalyzerFailure, "audio analysis failed", err)
	}

	perKey, err := out.finish()
	if err != nil {
		return nil, xcerr.E(xcerr.CodeAnalyzerFailure, "cannot parse audio analysis output", err)
	}
	samples := perKey["lavfi.astats.Overall.RMS_level"]
	if len(samples) == 0 {
		return nil, xcerr.E(xcerr.CodeAnalyzerFailure, "no RMS samples in audio analyzer output", nil)
	}
	// astats reports -inf for digital silence; JSON cannot carry it (cache
	// serialization) and -120 dBFS is the documented stand-in.
	for i := range samples {
		if math.IsInf(samples[i].V, -1) || math.IsNaN(samples[i].V) {
			samples[i].V = -120
		}
	}
	track := FeatureTrack{
		Analyzer: a.Name(),
		Version:  a.Version(),
		Kind:     "audio_rms_db",
		Unit:     "dBFS",
		Samples:  samples,
	}
	log.Debug("audio_rms analyzed", "samples", len(samples))
	return []FeatureTrack{track}, nil
}
