// Package analysis runs baseline media analyzers (FFmpeg-based) and manages
// analysis result caching.
//
// Design notes:
//   - Coarse-to-fine: analyzers sample at config frame_sample_fps (default 2)
//     on a downscaled stream (config analysis_width, default 640). No full-fps
//     or full-resolution decode ever happens here (PERFORMANCE.md).
//   - Determinism: identical input + config ⇒ byte-identical result JSON.
//   - Analyzers are separate ffmpeg passes per stream type (video/audio) and
//     are the future insertion point for Rust/AI workers (ARCHITECTURE.md).
package analysis

import (
	"context"
	"log/slog"

	"github.com/xiabee/XCut/internal/media"
)

// Sample is one measurement at media time T seconds.
type Sample struct {
	T float64 `json:"t"`
	V float64 `json:"v"`
}

// FeatureTrack is a named time series over media time.
type FeatureTrack struct {
	Analyzer string  `json:"analyzer"`
	Version  int     `json:"version"`
	Kind     string  `json:"kind"` // e.g. frame_diff, audio_rms_db
	Unit     string  `json:"unit"`
	Samples  []Sample `json:"samples"`
}

// Result is the full analysis artifact for one asset (cached on disk).
type Result struct {
	Fingerprint string         `json:"fingerprint"`
	DurationSec float64        `json:"duration_s"`
	Tracks      []FeatureTrack `json:"tracks"`
}

// FindTrack returns the track with the given kind, or nil.
func (r *Result) FindTrack(kind string) *FeatureTrack {
	for i := range r.Tracks {
		if r.Tracks[i].Kind == kind {
			return &r.Tracks[i]
		}
	}
	return nil
}

// Options controls analyzer execution.
type Options struct {
	Tools         media.Tools
	SampleFPS     float64 // analysis sampling rate
	AnalysisWidth int     // downscaled analysis width
}

// Analyzer is the pluggable analysis unit. Future Rust/AI workers implement
// the same shape (Name+Version+Analyze) over the process boundary.
type Analyzer interface {
	Name() string
	Version() int
	// Analyze produces zero or more feature tracks.
	Analyze(ctx context.Context, opts Options, path string, hasAudio bool, log *slog.Logger) ([]FeatureTrack, error)
}
