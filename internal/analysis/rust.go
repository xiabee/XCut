package analysis

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/xiabee/XCut/internal/worker"
	"github.com/xiabee/XCut/internal/xcerr"
)

// RustAudioAnalyzer computes audio RMS through the optional Rust worker
// (xcut-worker-media, symphonia-based). Same emitted track kind as the
// FFmpeg analyzer, so downstream consumers cannot tell the difference —
// only the cache key and provenance differ.
type RustAudioAnalyzer struct {
	Bin string
}

func (a RustAudioAnalyzer) Name() string { return "audio_rms_rust" }
func (a RustAudioAnalyzer) Version() int { return 1 }

func (a RustAudioAnalyzer) Analyze(ctx context.Context, opts Options, path string, hasAudio bool, log *slog.Logger) ([]FeatureTrack, error) {
	if !hasAudio {
		return nil, nil
	}
	// The worker call must honor the configured per-call budget; the
	// client's built-in default would silently cap every call at 10 minutes
	// no matter what resource.analyzer_call_timeout says.
	timeout := opts.CallTimeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	raw, err := worker.CallWithTimeout(ctx, a.Bin, worker.Request{
		Protocol: worker.Protocol,
		Op:       "audio_rms",
		Input:    path,
		Params:   map[string]any{"window_sec": 0.5},
	}, timeout)
	if err != nil {
		return nil, xcerr.E(xcerr.CodeAnalyzerFailure, "rust audio worker failed", err)
	}
	var res struct {
		Kind       string  `json:"kind"`
		Unit       string  `json:"unit"`
		SampleRate uint32  `json:"sample_rate"`
		WindowSec  float64 `json:"window_sec"`
		Samples    []struct {
			T float64 `json:"t"`
			V float64 `json:"v"`
		} `json:"samples"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, xcerr.E(xcerr.CodeAnalyzerFailure, "rust audio worker response unparseable", err)
	}
	track := FeatureTrack{
		Analyzer: a.Name(),
		Version:  a.Version(),
		Kind:     "audio_rms_db",
		Unit:     "dBFS",
		Samples:  make([]Sample, 0, len(res.Samples)),
	}
	for _, s := range res.Samples {
		track.Samples = append(track.Samples, Sample{T: s.T, V: s.V})
	}
	log.Debug("rust audio_rms analyzed", "samples", len(track.Samples))
	return []FeatureTrack{track}, nil
}
