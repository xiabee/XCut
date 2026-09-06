package timeline

import (
	"fmt"
	"math"

	"github.com/xiabee/XCut/internal/xcerr"
)

// MediaLookup resolves an asset's real duration for source-range checks.
// Implementations return ok=false for unknown assets.
type MediaLookup func(assetID string) (durationSec float64, ok bool)

// FixedLookup builds a MediaLookup from a map (tests, generation-time checks).
func FixedLookup(durations map[string]float64) MediaLookup {
	return func(id string) (float64, bool) {
		d, ok := durations[id]
		return d, ok
	}
}

const eps = 1e-6

// Validate checks the whole timeline against the schema contract. All
// violations are collected and reported together. lookup may be nil (skips
// source-vs-media duration checks).
func (t *Timeline) Validate(lookup MediaLookup) error {
	var errs []string

	if t.Version != Version {
		errs = append(errs, fmt.Sprintf("unsupported timeline version %d (want %d)", t.Version, Version))
	}
	if err := validateCanvas(t.Canvas); err != nil {
		errs = append(errs, err.Error())
	}

	seenClips := map[string]bool{}
	for ti, tr := range t.Tracks {
		if tr.ID == "" {
			errs = append(errs, fmt.Sprintf("track[%d]: empty id", ti))
		}
		if tr.Kind != "video" && tr.Kind != "audio" {
			errs = append(errs, fmt.Sprintf("track %q: invalid kind %q", tr.ID, tr.Kind))
		}
		var prevEnd float64
		for ci, c := range tr.Clips {
			ctx := fmt.Sprintf("track %q clip[%d] %q", tr.ID, ci, c.ID)
			if c.ID == "" {
				errs = append(errs, fmt.Sprintf("%s: empty id", ctx))
			} else if seenClips[c.ID] {
				errs = append(errs, fmt.Sprintf("%s: duplicate clip id", ctx))
			}
			seenClips[c.ID] = true

			if c.AssetID == "" {
				errs = append(errs, fmt.Sprintf("%s: empty asset_id", ctx))
			}

			if !finite(c.SourceStart) || !finite(c.SourceEnd) ||
				!finite(c.TimelineStart) || !finite(c.Speed) || !finite(c.Volume) {
				errs = append(errs, fmt.Sprintf("%s: non-finite numbers", ctx))
				continue
			}
			if c.SourceStart < 0 {
				errs = append(errs, fmt.Sprintf("%s: source_start %g < 0", ctx, c.SourceStart))
			}
			if c.SourceEnd <= c.SourceStart {
				errs = append(errs, fmt.Sprintf("%s: source range [%g,%g] not positive", ctx, c.SourceStart, c.SourceEnd))
				continue
			}
			if c.Speed <= 0 || c.Speed > 10 {
				errs = append(errs, fmt.Sprintf("%s: speed %g out of (0,10]", ctx, c.Speed))
			}
			if c.Volume < 0 || c.Volume > 1 {
				errs = append(errs, fmt.Sprintf("%s: volume %g out of [0,1]", ctx, c.Volume))
			}
			if c.Duration() <= 0 {
				errs = append(errs, fmt.Sprintf("%s: non-positive duration", ctx))
			}
			if c.TimelineStart < 0 {
				errs = append(errs, fmt.Sprintf("%s: timeline_start %g < 0", ctx, c.TimelineStart))
			}
			// Overlap check on the same track (clips must be ordered).
			end := c.TimelineStart + c.Duration()
			if ci > 0 && c.TimelineStart < prevEnd-eps {
				errs = append(errs, fmt.Sprintf("%s: overlaps previous clip (starts %g, previous ends %g)", ctx, c.TimelineStart, prevEnd))
			}
			if end > prevEnd {
				prevEnd = end
			}

			if c.Transition != nil {
				tt := c.Transition
				if tt.Type != "cut" && tt.Type != "fade" {
					errs = append(errs, fmt.Sprintf("%s: transition type %q unsupported", ctx, tt.Type))
				}
				if !finite(tt.Duration) || tt.Duration < 0 || tt.Duration > c.Duration() {
					errs = append(errs, fmt.Sprintf("%s: transition duration %g invalid", ctx, tt.Duration))
				}
			}
		}
	}

	if lookup != nil {
		// Source ranges must fit inside the real media; assets must exist.
		for _, tr := range t.Tracks {
			for ci, c := range tr.Clips {
				if c.AssetID == "" {
					continue
				}
				d, ok := lookup(c.AssetID)
				if !ok {
					errs = append(errs, fmt.Sprintf("track %q clip[%d] %q: unknown asset %q",
						tr.ID, ci, c.ID, c.AssetID))
					continue
				}
				if finite(d) && c.SourceEnd > d+eps {
					errs = append(errs, fmt.Sprintf("track %q clip[%d] %q: source_end %g exceeds media duration %g",
						tr.ID, ci, c.ID, c.SourceEnd, d))
				}
			}
		}
	}

	if len(errs) > 0 {
		msg := fmt.Sprintf("timeline validation failed (%d problem(s))", len(errs))
		e := xcerr.E(xcerr.CodeValidation, msg, nil)
		return &MultiError{Errors: errs, Err: e}
	}
	return nil
}

func validateCanvas(c Canvas) error {
	if !finite(float64(c.FPS)) || c.FPS <= 0 || c.FPS > 240 {
		return fmt.Errorf("canvas: invalid fps %g", c.FPS)
	}
	if c.Width < 16 || c.Width > 7680 || c.Width%2 != 0 {
		return fmt.Errorf("canvas: invalid width %d", c.Width)
	}
	if c.Height < 16 || c.Height > 4320 || c.Height%2 != 0 {
		return fmt.Errorf("canvas: invalid height %d", c.Height)
	}
	return nil
}

func finite(f float64) bool {
	return !math.IsNaN(f) && !math.IsInf(f, 0)
}

// MultiError carries all validation violations.
type MultiError struct {
	Errors []string
	Err    error
}

func (m *MultiError) Error() string {
	if m.Err != nil {
		return m.Err.Error()
	}
	return fmt.Sprintf("%d validation errors", len(m.Errors))
}

// Unwrap exposes the underlying *xcerr.Error so errors.As/xcerr.CodeOf see
// the real code (validation) instead of falling back to internal.
func (m *MultiError) Unwrap() error { return m.Err }

// Details returns the individual violation strings (for logs/UI lists).
func (m *MultiError) Details() []string { return m.Errors }
