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
//
// When MR is set the scan scores each candidate window as a hypothesized
// person box against the band model (the multi-region filter), and the spot's
// sampled aspect is what the window keeps; the single-histogram path is
// unchanged. The two models live in separate cache namespaces (the sig hash
// carries the model kind), so neither scan ever satisfies the other's key.
type PlayerPresenceAnalyzer struct {
	Sig player.Signature
	MR  *player.MultiRegionSignature
	// SpotHPerW is the spot rect's sampled height-per-width — the patch
	// aspect the multi-region scan keeps. Meaningless when MR is nil.
	SpotHPerW float64
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
	unit := "patch-match"
	var samples []player.Sample
	if a.MR != nil {
		unit = "mr-patch-match"
		samples, err = player.ScanPresenceMR(ctx, opts.Tools, path,
			probe.Width, probe.Height, *a.MR, a.SpotHPerW, 0, probe.DurationSec)
	} else {
		samples, err = player.ScanPresence(ctx, opts.Tools, path,
			probe.Width, probe.Height, a.Sig, 0, probe.DurationSec)
	}
	if err != nil {
		return nil, xcerr.E(xcerr.CodeAnalyzerFailure, "player presence scan failed", err)
	}
	track := FeatureTrack{
		Analyzer: a.Name(),
		Unit:     unit,
		Samples:  make([]Sample, 0, len(samples)),
	}
	for _, s := range samples {
		track.Samples = append(track.Samples, Sample{T: s.T, V: s.V})
	}
	log.Debug("player presence scanned", "samples", len(track.Samples), "model", unit)
	return []FeatureTrack{track}, nil
}
