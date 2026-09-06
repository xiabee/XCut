package analysis

import (
	"context"
	"log/slog"

	"github.com/xiabee/XCut/internal/xcerr"
)

// Baseline returns the phase-1 analyzer set (deterministic, FFmpeg-only).
func Baseline() []Analyzer {
	return []Analyzer{
		FrameDiffAnalyzer{},
		AudioAnalyzer{},
	}
}

// Run executes the given analyzers over one asset and returns the combined
// result. Cached results are served when the key matches.
func Run(ctx context.Context, store *Store, opts Options, analyzers []Analyzer, path, fingerprint string, durationSec float64, hasAudio bool, log *slog.Logger) (*Result, error) {
	cfg := ConfigKey{SampleFPS: opts.SampleFPS, AnalysisWidth: opts.AnalysisWidth}
	key := cacheKey(fingerprint, analyzers, cfg)

	if store != nil {
		if hit, err := store.Load(key); err == nil && hit != nil {
			log.Debug("analysis cache hit", "key", key[:12])
			return hit, nil
		}
	}

	result := &Result{Fingerprint: fingerprint, DurationSec: durationSec}
	for _, a := range analyzers {
		tracks, err := a.Analyze(ctx, opts, path, hasAudio, log)
		if err != nil {
			return nil, err
		}
		result.Tracks = append(result.Tracks, tracks...)
	}
	if len(result.Tracks) == 0 {
		return nil, xcerr.E(xcerr.CodeAnalyzerFailure, "no analyzers produced output", nil)
	}

	if store != nil {
		if err := store.Save(key, result); err != nil {
			log.Warn("analysis cache save failed", "err", err) // non-fatal
		}
	}
	return result, nil
}
