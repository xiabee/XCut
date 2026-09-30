package pipeline

import (
	"github.com/xiabee/XCut/internal/timeline"
)

// motionCarryIoU is how much of the old clip's source window a regenerated
// clip must reuse before the old clip's hand-picked motion may follow it. The
// plan aims a crop at specific material, so a window that moved is a window
// the pick no longer describes; 0.9 keeps a beat-snap nudge or a length-rule
// trim (both move an end by fractions of a second) while refusing a clip the
// regeneration re-cut from different material.
const motionCarryIoU = 0.9

// carryHandMotion moves hand-picked framing from the document being replaced
// onto the regeneration that replaces it — the known limitation the pane used
// to warn about ("hand-picked motion does not survive regenerating the reel"),
// now answered for the case that has an answer: same asset, near-identical
// source window, and the builder framed nothing there itself. The last
// condition is the precedence, not an oversight: regeneration re-runs the
// style, so a preset's own camera_motion outranks a pick made under the
// previous document, and a pick only fills a void the new document would
// otherwise leave. Returns how many plans were carried, for the log line.
func carryHandMotion(prev, next *timeline.Timeline) int {
	if prev == nil || next == nil {
		return 0
	}
	carried := 0
	for _, nt := range next.Tracks {
		for i := range nt.Clips {
			nc := &nt.Clips[i]
			if nc.Motion != nil {
				continue // the style framed this clip itself; the pick yields
			}
			pick := matchingPick(prev, nc)
			if pick == nil {
				continue
			}
			nc.Motion = pick.Motion
			if framing, ok := pick.Metadata["framing"]; ok {
				if nc.Metadata == nil {
					nc.Metadata = map[string]string{}
				}
				nc.Metadata["framing"] = framing
			}
			carried++
		}
	}
	return carried
}

// matchingPick finds the old clip whose hand pick best describes the new one's
// window: same asset, overlap at least motionCarryIoU of the union, highest
// IoU wins, ties to the earlier clip. A nil Motion on the old clip is not a
// pick — nothing to carry — and an old clip carrying a plan the new clip does
// not reuse (it re-cut different material) is skipped by the IoU floor.
func matchingPick(prev *timeline.Timeline, nc *timeline.Clip) *timeline.Clip {
	var best *timeline.Clip
	var bestIoU float64
	for pi := range prev.Tracks {
		for j := range prev.Tracks[pi].Clips {
			pc := &prev.Tracks[pi].Clips[j]
			if pc.AssetID != nc.AssetID || pc.Motion == nil {
				continue
			}
			iou := spanIoU(pc.SourceStart, pc.SourceEnd, nc.SourceStart, nc.SourceEnd)
			if iou < motionCarryIoU {
				continue
			}
			// Strictly greater replaces, so a tie keeps the document's earlier
			// clip — the deterministic answer, not whichever came last.
			if best == nil || iou > bestIoU {
				best, bestIoU = pc, iou
			}
		}
	}
	return best
}

// spanIoU is the intersection-over-union of two source spans; empty or
// inverted spans overlap nothing.
func spanIoU(as, ae, bs, be float64) float64 {
	lo, hi := max(as, bs), min(ae, be)
	inter := hi - lo
	if inter <= 0 {
		return 0
	}
	union := (ae - as) + (be - bs) - inter
	if union <= 0 {
		return 0
	}
	return inter / union
}
