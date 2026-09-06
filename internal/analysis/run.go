package analysis

import (
	"context"
	"log/slog"

	"github.com/xiabee/XCut/internal/worker"
	"github.com/xiabee/XCut/internal/xcerr"
)

// Baseline returns the phase-1 analyzer set (deterministic, FFmpeg-only).
func Baseline() []Analyzer {
	return []Analyzer{
		FrameDiffAnalyzer{},
		AudioAnalyzer{},
	}
}

// WorkerConfig controls optional worker-based analyzers (decoupled from the
// config package: plain values only).
type WorkerConfig struct {
	MediaBin string // worker binary path; "" = PATH lookup
	Audio    string // "auto" | "ffmpeg" | "rust"
}

// ResolveAnalyzers builds the analyzer set honoring the audio preference.
// auto: prefer the Rust worker with an FFmpeg fallback (codec gaps in the
// pure-Rust decoder never break analysis); rust: strict worker; ffmpeg: never
// probe for the worker.
func ResolveAnalyzers(ctx context.Context, wc WorkerConfig, log *slog.Logger) ([]Analyzer, error) {
	mode := wc.Audio
	if mode == "" {
		mode = "auto"
	}
	switch mode {
	case "ffmpeg":
		return Baseline(), nil
	case "rust":
		bin := worker.ResolveBin(wc.MediaBin)
		if bin == "" {
			return nil, xcerr.E(xcerr.CodeNotFound, "audio=rust but xcut-worker-media is not installed", nil)
		}
		return []Analyzer{FrameDiffAnalyzer{}, RustAudioAnalyzer{Bin: bin}}, nil
	case "auto":
		bin := worker.ResolveBin(wc.MediaBin)
		if bin == "" {
			log.Debug("media worker not found; audio analyzer = ffmpeg")
			return Baseline(), nil
		}
		if _, err := worker.Probe(ctx, bin); err != nil {
			log.Warn("media worker found but unusable; falling back to ffmpeg", "err", err)
			return Baseline(), nil
		}
		return []Analyzer{
			FrameDiffAnalyzer{},
			FallbackAnalyzer{
				Primary:  RustAudioAnalyzer{Bin: bin},
				Fallback: AudioAnalyzer{},
			},
		}, nil
	default:
		return nil, xcerr.E(xcerr.CodeValidation,
			"workers.audio must be auto|ffmpeg|rust (got "+mode+")", nil)
	}
}

// FallbackAnalyzer wraps a primary (worker-based) analyzer with the built-in
// FFmpeg implementation as a safety net. The cache key reflects the primary:
// a run that silently used the fallback is cached as if the primary produced
// it — acceptable because both emit the same track kind deterministically.
type FallbackAnalyzer struct {
	Primary  Analyzer
	Fallback Analyzer
}

func (f FallbackAnalyzer) Name() string { return f.Primary.Name() }
func (f FallbackAnalyzer) Version() int { return f.Primary.Version() }

func (f FallbackAnalyzer) Analyze(ctx context.Context, opts Options, path string, hasAudio bool, log *slog.Logger) ([]FeatureTrack, error) {
	tracks, err := f.Primary.Analyze(ctx, opts, path, hasAudio, log)
	if err == nil {
		return tracks, nil
	}
	log.Warn("analyzer falling back to ffmpeg", "primary", f.Primary.Name(), "err", err)
	return f.Fallback.Analyze(ctx, opts, path, hasAudio, log)
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
		// Enforce the disk budget after growing the cache (config
		// resource.max_cache_gb). Errors are non-fatal: eviction is
		// housekeeping, not correctness.
		if store.MaxBytes > 0 {
			if _, _, err := store.EvictTo(store.MaxBytes); err != nil {
				log.Warn("analysis cache eviction failed", "err", err)
			}
		}
	}
	return result, nil
}
