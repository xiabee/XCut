package timeline

import (
	"fmt"
	"math"
	"strconv"
	"strings"

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

// placementEps is the tolerance for timeline-start placement checks (gap,
// overlap, xfade join). The document itself is rounded to 4 decimals, so
// consecutive back-to-back placements legitimately differ from their exact
// sum by up to 5e-5 — the schema's own rounding granularity, not a defect.
const placementEps = 1e-4

// maxTimelineSeconds caps the total timeline length (24h). Anything beyond
// is a hand-editing accident, not a highlight cut: it would pin a render
// job for many hours and fill the temp budget before failing.
const maxTimelineSeconds = 24.0 * 60 * 60

// secs writes a number of seconds the way a person reads it. `%g` prints the
// shortest *exact* form, so a time produced by float arithmetic arrives with
// its residue attached — "starts 10.500100000000003" names a moment nobody can
// point at, and the walk over the manual-editing surface found exactly that
// inside a validator sentence. A tenth of a millisecond is finer than the
// fastest canvas frame (240 fps is 4.17 ms), so anything past it is residue and
// gets dropped rather than shown.
func secs(v float64) string {
	return strconv.FormatFloat(math.Round(v*10000)/10000, 'f', -1, 64)
}

// Validate checks the whole timeline against the schema contract. All
// violations are collected and reported together. lookup may be nil (skips
// source-vs-media duration checks).
// maxNamedProblems bounds how many violations go into the message itself: enough
// to fix the first mistake without re-PUTing to discover the next, short enough
// that a document with forty problems still fits on a terminal line.
const maxNamedProblems = 3

func (t *Timeline) Validate(lookup MediaLookup) error {
	var errs []string

	if t.Version != Version {
		errs = append(errs, fmt.Sprintf("unsupported timeline version %d (want %d)", t.Version, Version))
	}
	if err := validateCanvas(t.Canvas); err != nil {
		errs = append(errs, err.Error())
	}

	seenClips := map[string]bool{}
	var maxEnd float64
	for ti, tr := range t.Tracks {
		if tr.ID == "" {
			errs = append(errs, fmt.Sprintf("track[%d]: empty id", ti))
		}
		if tr.Kind != "video" && tr.Kind != "audio" {
			errs = append(errs, fmt.Sprintf("track %q: invalid kind %q", tr.ID, tr.Kind))
		}
		var prevEnd float64
		var prev *Clip
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
				errs = append(errs, fmt.Sprintf("%s: source_start %s < 0", ctx, secs(c.SourceStart)))
			}
			if c.SourceEnd <= c.SourceStart {
				errs = append(errs, fmt.Sprintf("%s: source range [%s,%s] not positive", ctx, secs(c.SourceStart), secs(c.SourceEnd)))
				continue
			}
			// 0.1 is the practical floor: the atempo chain handles it, and
			// anything smaller turns a typo into a many-hour render (a 60s
			// source at 0.001 is a 16-hour clip). Values near the underflow
			// boundary make Duration() overflow to +Inf, which then died in
			// JSON serialization with a 500 instead of this validation error.
			if c.Speed < 0.1 || c.Speed > 10 {
				errs = append(errs, fmt.Sprintf("%s: speed %g out of [0.1,10]", ctx, c.Speed))
			}
			if c.Volume < 0 || c.Volume > 1 {
				errs = append(errs, fmt.Sprintf("%s: volume %g out of [0,1]", ctx, c.Volume))
			}
			if c.Motion != nil {
				errs = append(errs, motionErrors(ctx, c.Motion)...)
			}
			if c.Duration() <= 0 {
				errs = append(errs, fmt.Sprintf("%s: non-positive duration", ctx))
			}
			if d := c.Duration(); d > maxTimelineSeconds {
				errs = append(errs, fmt.Sprintf("%s: clip duration %ss exceeds the %ss render cap — raise the clip speed or trim the source range", ctx, secs(d), secs(maxTimelineSeconds)))
			}
			if c.TimelineStart < 0 {
				errs = append(errs, fmt.Sprintf("%s: timeline_start %s < 0", ctx, secs(c.TimelineStart)))
			}
			// Overlap check on the same track (clips must be ordered). An
			// xfade transition on the *previous* clip means the two clips
			// intentionally share the transition window: the overlap must
			// match the transition duration exactly (within eps). Any other
			// join must be flush — the renderer joins clips back-to-back,
			// so a placement gap could never be honored and would only
			// surface as a confusing duration mismatch after rendering.
			end := c.TimelineStart + c.Duration()
			if ci > 0 && c.TimelineStart < prevEnd-placementEps {
				xfade := prev != nil && prev.Transition != nil &&
					prev.Transition.Type == "xfade" && prev.Transition.Duration > 0
				overlap := prevEnd - c.TimelineStart
				if !xfade {
					errs = append(errs, fmt.Sprintf("%s: overlaps previous clip (starts %s, previous ends %s)", ctx, secs(c.TimelineStart), secs(prevEnd)))
				} else if diff := overlap - prev.Transition.Duration; diff > placementEps || diff < -placementEps {
					errs = append(errs, fmt.Sprintf("%s: xfade overlap %s does not match transition duration %s", ctx, secs(overlap), secs(prev.Transition.Duration)))
				} else if xfade && prev.Transition.Duration > c.Duration()+eps {
					errs = append(errs, fmt.Sprintf("%s: xfade duration %s exceeds this clip's length %s", ctx, secs(prev.Transition.Duration), secs(c.Duration())))
				}
			}
			if ci > 0 && c.TimelineStart > prevEnd+placementEps {
				errs = append(errs, fmt.Sprintf("%s: leaves a %ss gap after the previous clip (ends %s, starts %s) — the renderer joins clips back-to-back, so gaps cannot be honored", ctx, secs(c.TimelineStart-prevEnd), secs(prevEnd), secs(c.TimelineStart)))
			}
			// A flush join carrying an xfade is the mirror of the gap rule:
			// the renderer would blend across the previous clip's tail and
			// produce output shorter than the document claims. Require the
			// overlap the transition declares.
			if ci > 0 && prev != nil && prev.Transition != nil &&
				prev.Transition.Type == "xfade" && prev.Transition.Duration > 0 &&
				c.TimelineStart >= prevEnd-placementEps && c.TimelineStart <= prevEnd+placementEps {
				errs = append(errs, fmt.Sprintf("%s: xfade on a flush join would blend into the previous clip's tail and shorten the output — overlap this clip's start by the transition duration (%ss), or use cut/fade", ctx, secs(prev.Transition.Duration)))
			}
			if end > prevEnd {
				prevEnd = end
			}
			if end > maxEnd {
				maxEnd = end
			}
			prev = &tr.Clips[ci]

			if c.Transition != nil {
				tt := c.Transition
				if tt.Type != "cut" && tt.Type != "fade" && tt.Type != "xfade" {
					errs = append(errs, fmt.Sprintf("%s: transition type %q unsupported", ctx, tt.Type))
				}
				// xfade blends into the NEXT clip; on the last clip there is
				// nothing to blend with and the renderer would silently drop
				// the declared transition — refuse instead (a fade-out wants
				// type "fade", which IS honored on the last clip).
				if tt.Type == "xfade" && ci == len(tr.Clips)-1 {
					errs = append(errs, fmt.Sprintf("%s: trailing xfade has no following clip to blend with — use fade for a fade-out", ctx))
				}
				// A zero-duration fade/xfade would be silently dropped by
				// the renderer (which only engages on Duration > 0) while
				// the document still claims the join is a transition — the
				// same silent-degrade the trailing-xfade rule refuses. Say
				// so at validation time; "cut" (or omitting the transition)
				// is the honest form.
				if tt.Type != "cut" && tt.Duration <= 0 {
					errs = append(errs, fmt.Sprintf("%s: transition type %q with zero duration renders as a plain cut — use cut", ctx, tt.Type))
				}
				if !finite(tt.Duration) || tt.Duration < 0 || tt.Duration > c.Duration() {
					errs = append(errs, fmt.Sprintf("%s: transition duration %s invalid", ctx, secs(tt.Duration)))
				}
			}
		}
	}

	if maxEnd > maxTimelineSeconds {
		errs = append(errs, fmt.Sprintf("timeline duration %ss exceeds the %ss render cap — select fewer or shorter clips", secs(maxEnd), secs(maxTimelineSeconds)))
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
					errs = append(errs, fmt.Sprintf("track %q clip[%d] %q: source_end %s exceeds media duration %s",
						tr.ID, ci, c.ID, secs(c.SourceEnd), secs(d)))
				}
			}
		}
	}

	if len(errs) > 0 {
		// Name what was rejected, up to a screenful. A count is not something a
		// person can act on — the list exists already (Details), and the walk over
		// the manual-editing surface found clients receiving "(2 problem(s))" for a
		// zoom of 3.0, an unknown asset and an empty document alike. The rest are
		// counted rather than hidden.
		shown, tail := errs, ""
		if len(errs) > maxNamedProblems {
			shown, tail = errs[:maxNamedProblems], fmt.Sprintf(" (and %d more)", len(errs)-maxNamedProblems)
		}
		msg := fmt.Sprintf("timeline validation failed: %s%s", strings.Join(shown, "; "), tail)
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

// motionErrors bounds a framing plan. The zoom ceiling is 1 because a window
// larger than the source would be a crop of nothing; the coordinates are
// normalized to the frame so a plan survives a re-export at another resolution
// and a hand-edited document cannot ask the renderer to sample outside it.
func motionErrors(ctx string, mv *Motion) []string {
	var errs []string
	if !finite(mv.Zoom) || mv.Zoom <= 0 || mv.Zoom > 1 {
		errs = append(errs, fmt.Sprintf("%s: motion.zoom %g out of (0,1]", ctx, mv.Zoom))
	}
	for _, p := range []struct {
		name string
		pt   []float64
	}{{"motion.from", mv.From}, {"motion.to", mv.To}} {
		if p.pt == nil {
			continue
		}
		if len(p.pt) != 2 {
			errs = append(errs, fmt.Sprintf("%s: %s needs [x,y], got %d number(s)", ctx, p.name, len(p.pt)))
			continue
		}
		for _, c := range p.pt {
			if !finite(c) || c < 0 || c > 1 {
				errs = append(errs, fmt.Sprintf("%s: %s coordinate %g out of [0,1]", ctx, p.name, c))
			}
		}
	}
	return errs
}
