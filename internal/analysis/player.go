package analysis

import (
	"context"
	"log/slog"

	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/player"
	"github.com/xiabee/XCut/internal/xcerr"
)

// PlayerPresenceAnalyzer produces the person-presence feature track: for every
// sampled frame, how strongly the best-matching patch looks like the color
// signature measured from the user's player spot. It is a data-driven
// analyzer like frame_diff — enabled by passing a signature in Options, not by
// a style branch — and its track lands in the same cache under a key that
// includes the signature hash.
type PlayerPresenceAnalyzer struct {
	Sig player.Signature
}

func (PlayerPresenceAnalyzer) Name() string { return "player_presence" }
func (PlayerPresenceAnalyzer) Version() int { return 1 }

func (a PlayerPresenceAnalyzer) Analyze(ctx context.Context, opts Options, path string, _ bool, log *slog.Logger) ([]FeatureTrack, error) {
	// The full source range is scanned; the caller bounds it with the same
	// call timeout every analyzer gets.
	probe, err := media.ProbeFile(ctx, opts.Tools, path)
	if err != nil {
		return nil, xcerr.E(xcerr.CodeAnalyzerFailure, "cannot probe source for presence scan", err)
	}
	samples, err := player.ScanPresence(ctx, opts.Tools, path,
		probe.Width, probe.Height, a.Sig, 0, probe.DurationSec)
	if err != nil {
		return nil, xcerr.E(xcerr.CodeAnalyzerFailure, "player presence scan failed", err)
	}
	track := FeatureTrack{
		Analyzer: a.Name(),
		Unit:     "patch-match",
		Samples:  make([]Sample, 0, len(samples)),
	}
	for _, s := range samples {
		track.Samples = append(track.Samples, Sample{T: s.T, V: s.V})
	}
	log.Debug("player presence scanned", "samples", len(track.Samples))
	return []FeatureTrack{track}, nil
}
