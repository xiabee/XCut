package style

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/xiabee/XCut/internal/event"
	"github.com/xiabee/XCut/internal/timeline"
	"github.com/xiabee/XCut/internal/xcerr"
)

// AssetInfo is the asset knowledge Build needs (decoupled from storage).
type AssetInfo struct {
	ID          string
	Path        string
	DurationSec float64
	// ROI is the region the project's analysis was aimed at, when one is set.
	// The framing plan can point a window at its center; it is a position, not a
	// promise that the whole region fits in the frame — the selector does not
	// know the source's pixel aspect, and saying otherwise would be a claim the
	// renderer, not the plan, has to keep.
	ROI *MotionROI
}

// AssetEvents pairs one asset with its event segments. Events are always
// produced per source asset; Build merges them into one timeline.
type AssetEvents struct {
	Asset    AssetInfo
	Segments []event.Segment
	// Boundaries are source-time marks where a point/rally is known to have
	// ended, supplied by whoever could know that (a sidecar reading a burned-in
	// scoreboard). Optional and empty by default: the core's own signals were
	// measured and cannot infer it (docs/EVAL.md).
	Boundaries []float64
	// Beats is the source audio's beat grid (analysis.EstimateBeatGrid over the
	// onset track), which the preset's beat_snap_tolerance may pull a clip end
	// onto. It is the softer constraint: an end that Boundaries already fixed is
	// never moved, because a cut landing on the score is worth more than one
	// landing on the pulse. Empty by default, like Boundaries.
	Beats []float64
}

// selInterval is a source-time span selected so far, kept per asset for the
// diversity rules.
type selInterval struct {
	assetID    string
	start, end float64
}

// factors is one segment's normalized (0..1) score components.
type factors struct {
	motion, audio, duration float64
	hits, density           float64
}

// weighted returns the preset-weighted total.
func (f factors) weighted(p *Preset) float64 {
	return p.Scoring.Motion*f.motion + p.Scoring.Audio*f.audio +
		p.Scoring.Duration*f.duration + p.Scoring.Hits*f.hits +
		p.Scoring.Density*f.density
}

// reason names the dominant weighted factors (share of the total), e.g.
// "motion+audio". Deterministic ordering by fixed factor priority.
func (f factors) reason(p *Preset) string {
	type part struct {
		name  string
		value float64
	}
	total := f.weighted(p)
	if total <= 0 {
		return "flat"
	}
	parts := []part{
		{"motion", p.Scoring.Motion * f.motion},
		{"audio", p.Scoring.Audio * f.audio},
		{"duration", p.Scoring.Duration * f.duration},
		{"hits", p.Scoring.Hits * f.hits},
		{"density", p.Scoring.Density * f.density},
	}
	sort.SliceStable(parts, func(i, j int) bool { return parts[i].value > parts[j].value })

	var picked []string
	var share float64
	for _, pt := range parts {
		if pt.value/total >= 0.3 {
			picked = append(picked, pt.name)
			share += pt.value / total
		}
		if share >= 0.6 {
			break
		}
	}
	if len(picked) == 0 {
		return "balanced"
	}
	return strings.Join(picked, "+")
}

// breakdown renders the explainable score line stored on the clip, e.g.
// "motion 0.82x0.55=0.45; audio 0.55x0.30=0.17; duration 0.53x0.20=0.11; total 0.72".
// Factors with zero weight are omitted.
func (f factors) breakdown(p *Preset) string {
	line := func(name string, n, w float64) string {
		if w == 0 {
			return ""
		}
		return fmt.Sprintf("%s %.2fx%.2f=%.2f", name, n, w, n*w)
	}
	parts := []string{
		line("motion", f.motion, p.Scoring.Motion),
		line("audio", f.audio, p.Scoring.Audio),
		line("duration", f.duration, p.Scoring.Duration),
		line("hits", f.hits, p.Scoring.Hits),
		line("density", f.density, p.Scoring.Density),
	}
	var kept []string
	for _, s := range parts {
		if s != "" {
			kept = append(kept, s)
		}
	}
	return fmt.Sprintf("%s; total %.4f", strings.Join(kept, "; "), f.weighted(p))
}

// Build constructs a deterministic Timeline from per-asset events and a
// preset.
//
// Selection: events are re-scored with preset weights, ranked (ties broken by
// time), then taken greedily while the target duration allows. When the
// preset enables diversity (min_gap / max_overlap_iou), candidates too close
// to — or overlapping — an already-selected clip are skipped so one match
// cannot fill the reel with near-duplicates. Every clip carries its score
// breakdown and dominant-factor reason in Metadata (explainable selection).
// Identical inputs always produce identical output (property tested).
func Build(preset *Preset, projectID string, items []AssetEvents) (*timeline.Timeline, error) {
	if len(items) == 0 {
		return nil, xcerr.E(xcerr.CodeValidation, "no assets to build timeline from", nil)
	}
	durations := map[string]float64{}

	type candidate struct {
		asset      AssetInfo
		seg        event.Segment
		boundaries []float64
		beats      []float64
		score      float64
		f          factors
	}
	var cands []candidate
	var raws []rawFactors
	for _, it := range items {
		durations[it.Asset.ID] = it.Asset.DurationSec
		for _, s := range it.Segments {
			if s.Duration() < preset.MinClipDuration {
				continue
			}
			raws = append(raws, rawSegmentFactors(s))
			cands = append(cands, candidate{
				asset:      it.Asset,
				seg:        s,
				boundaries: it.Boundaries,
				beats:      it.Beats,
			})
		}
	}
	if len(cands) == 0 {
		return nil, xcerr.E(xcerr.CodeValidation, "no events satisfy the style's clip constraints", nil)
	}
	// Factors are normalized WITHIN the candidate set: absolute caps (12
	// hits, 1.5 hits/s, ...) saturate on real sports footage where every
	// chunk far exceeds them, flattening the scores until ranking degrades
	// to "earliest first". Relative normalization restores the spread the
	// weights are meant to act on.
	rels := relativize(raws)
	for i := range cands {
		cands[i].f = rels[i]
		cands[i].score = cands[i].f.weighted(preset)
	}

	// Deterministic rank: score desc, then start asc, end asc, asset id.
	sort.Slice(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if a.score != b.score {
			return a.score > b.score
		}
		if a.seg.Start != b.seg.Start {
			return a.seg.Start < b.seg.Start
		}
		if a.seg.End != b.seg.End {
			return a.seg.End < b.seg.End
		}
		return a.asset.ID < b.asset.ID
	})

	// Greedy selection up to the target duration, honoring diversity rules.
	// budgetRanOut distinguishes the two ways the loop can end: the reel is
	// full, or there is nothing left to consider.
	total := 0.0
	budgetRanOut := false
	var chosen []selInterval
	var clips []timeline.Clip
	n := 0
	// A per-phase floor runs the same greedy pass twice: the first pass takes
	// only each window's first picks, the second fills what is left of the
	// budget in the ordinary score order. Without a floor this is one pass,
	// byte-for-byte the previous behaviour.
	passes := 1
	if preset.Diversity.MinPerWindow > 0 {
		passes = 2
	}
	used := make([]bool, len(cands))
	for pass := 0; pass < passes; pass++ {
		for i, c := range cands {
			if used[i] {
				continue
			}
			remaining := preset.TargetDuration - total
			if remaining <= 0 {
				budgetRanOut = true
				break
			}
			srcStart, srcEnd, atBoundary, anchor, ok := trimSegment(preset, c.seg, remaining, c.boundaries)
			if !ok {
				continue
			}
			// Clamp to the real media duration: events should never exceed it,
			// but a trimmed window may start late enough to poke past the end.
			if c.asset.DurationSec > 0 && srcEnd > c.asset.DurationSec {
				srcEnd = c.asset.DurationSec
			}
			if srcEnd-srcStart < preset.MinClipDuration {
				continue
			}
			// Beat snapping only applies where nothing else has fixed the end: a
			// measured point end outranks the grid, and so does a rally's own
			// landing — a clip that stops at last-hit-plus-tail keeps that
			// landing even when a beat sits nearer (the grid, built from the
			// same onsets, ends at the last hit anyway).
			atBeat := 0.0
			if atBoundary == 0 && anchor != anchorRallyEnd {
				if snapped, moved := snapEnd(preset, srcStart, srcEnd, c.seg, c.beats); moved {
					srcEnd, atBeat = snapped, snapped
				}
			}
			// Diversity operates on the trimmed window — what the reel will
			// actually show — not on the wider source segment.
			cand := selInterval{assetID: c.asset.ID, start: srcStart, end: srcEnd}
			if !diverse(preset, chosen, cand, durations[c.asset.ID]) {
				continue
			}
			if pass == 0 && preset.Diversity.MinPerWindow > 0 &&
				!withinFloor(preset, chosen, cand, durations[c.asset.ID]) {
				continue
			}
			used[i] = true
			n++
			chosen = append(chosen, cand)
			plan := framingPlan(preset, c.asset, len(clips))
			md := map[string]string{
				"score":           strconv.FormatFloat(round4(c.score), 'f', -1, 64),
				"score_breakdown": c.f.breakdown(preset),
				"reason":          c.f.reason(preset),
			}
			if plan != nil {
				// Which rule framed this clip — the geometry itself lives on the
				// clip, so this is for a reader comparing a reel against the style
				// that made it.
				md["framing"] = preset.CameraMotion.Mode
			}
			if c.seg.HitCount > 0 {
				md["hit_count"] = strconv.Itoa(c.seg.HitCount)
				md["hit_density"] = strconv.FormatFloat(c.seg.HitDensity, 'f', -1, 64)
			}
			if atBoundary > 0 {
				// Which boundary shaped this clip — so a reader can check the
				// scoreboard rule per clip instead of trusting an aggregate score.
				md["point_end"] = strconv.FormatFloat(round4(atBoundary), 'f', 2, 64)
			}
			if anchor != "" && anchor != anchorBoundary {
				// The end rule is visible per clip too: "last hit + landing
				// tail" is the answer to why a clip stops where it stops.
				md["end_anchor"] = anchor
			}
			if atBeat > 0 {
				// Same argument for the grid: the beat is visible per clip. Four
				// decimals because the estimate is not a round number — a
				// refined 0.5 s grid sits at 7.5028, and rounding the evidence to
				// two decimals would disagree with the geometry it documents.
				md["beat"] = strconv.FormatFloat(round4(atBeat), 'f', 4, 64)
			}
			clips = append(clips, timeline.Clip{
				ID:          "clip_" + strconv.Itoa(n),
				AssetID:     c.asset.ID,
				SourcePath:  c.asset.Path,
				SourceStart: srcStart,
				SourceEnd:   srcEnd,
				Speed:       1,
				Volume:      preset.Audio.Gain,
				Motion:      plan,
				Metadata:    md,
			})
			total += srcEnd - srcStart
		}
		if budgetRanOut {
			break
		}
	}
	if len(clips) == 0 {
		return nil, xcerr.E(xcerr.CodeValidation, "style constraints rejected all events", nil)
	}

	// Chronological order (by asset, then source time — stable, meaningful
	// when multiple assets are present).
	sort.Slice(clips, func(i, j int) bool {
		if clips[i].AssetID != clips[j].AssetID {
			return clips[i].AssetID < clips[j].AssetID
		}
		return clips[i].SourceStart < clips[j].SourceStart
	})
	if preset.ClipOrder == ClipOrderHookFirst {
		moveTopShotFirst(clips)
	}

	// Back-to-back placement; an xfade makes clips share the transition
	// window (timeline duration = Σ durations − Σ transitions).
	cursor := 0.0
	for i := range clips {
		clips[i].TimelineStart = round4(cursor)
		if i >= len(clips)-1 || preset.Transition.Type == "cut" {
			cursor = round4(cursor + clips[i].Duration())
			continue
		}
		// Transition between clip i and i+1 (attached to the outgoing clip,
		// same convention as "fade"). xfade consumes part of both clips, so
		// it may not exceed the shorter of the two.
		d := math.Min(preset.Transition.Duration, clips[i].Duration())
		d = math.Min(d, clips[i+1].Duration())
		clips[i].Transition = &timeline.Transition{
			Type:     preset.Transition.Type,
			Duration: round4(d),
		}
		step := clips[i].Duration()
		if preset.Transition.Type == "xfade" {
			step -= d // the next clip starts inside this one's window
		}
		cursor = round4(cursor + step)
	}

	tl := &timeline.Timeline{
		Version:   timeline.Version,
		ProjectID: projectID,
		Canvas:    preset.Canvas,
		Tracks: []timeline.Track{
			{ID: "v1", Kind: "video", Clips: clips},
		},
		Metadata: map[string]string{
			"style":   preset.Name,
			"style_v": strconv.Itoa(preset.Version),
			// What ran out first — the footage or the budget. Without these the
			// caller cannot tell "18 clips because you asked for 18" from "18
			// because this match only offered 18 candidate rallies", and the
			// second one is a fact the user has to be told out loud.
			"candidate_events": strconv.Itoa(len(cands)),
			"candidate_limit":  strconv.FormatBool(!budgetRanOut),
			// The number the reel was asked for, next to the number it got. The
			// CLI has it in scope; a client reloading a saved document does not,
			// and "58.3s" only reads as a shortfall against something.
			"target_duration": strconv.FormatFloat(round4(preset.TargetDuration), 'f', 2, 64),
		},
	}

	if err := tl.Validate(func(id string) (float64, bool) {
		d, ok := durations[id]
		return d, ok
	}); err != nil {
		return nil, err
	}
	return tl, nil
}

// diverse reports whether a candidate respects the preset's diversity rules
// against everything selected so far. Disabled rules (zero values) pass.
// windowPhases divides an asset's own duration into the windows the spread rules
// work on. The ceiling and the floor both call it: two rules that disagree about
// where a window starts leave one of them silently unsatisfied.
//
// A reel asked for longer than the quota can hold needs *more* windows, not a
// looser quota per window: that keeps the spread discipline the rule exists for
// while letting the cut use the budget it was given. Measured on a 603 s match:
// with a fixed 5×2 quota a 240 s request stopped at 10 clips / 80 s (recall
// 0.140); scaling the window count instead reached 19 clips at equal precision
// (0.267).
func windowPhases(p *Preset, assetDur float64) (float64, bool) {
	if p.Diversity.Phases < 2 || assetDur <= 0 {
		return 0, false
	}
	phases := float64(p.Diversity.Phases)
	if p.Diversity.MaxPerWindow > 0 && p.MaxClipDuration > 0 && p.TargetDuration > 0 {
		room := int(math.Ceil(p.TargetDuration / p.MaxClipDuration))
		if quota := p.Diversity.Phases * p.Diversity.MaxPerWindow; room > quota {
			phases = math.Max(phases, math.Ceil(float64(room)/float64(p.Diversity.MaxPerWindow)))
		}
	}
	return phases, true
}

// windowCount counts the already-chosen clips that sit in the same window as the
// candidate, on this candidate's asset.
func windowCount(chosen []selInterval, cand selInterval, win float64) int {
	phase := int(cand.start / win)
	n := 0
	for _, c := range chosen {
		if c.assetID == cand.assetID && int(c.start/win) == phase {
			n++
		}
	}
	return n
}

// withinFloor reports whether the candidate's window may still receive a clip in
// the seeding pass. Ranking alone spends the whole budget on whichever phase is
// richest — the ceiling bounds a window but never asks an empty one to be
// filled — so the owner's match produced 8 clips and left ten consecutive rallies
// unrepresented (docs/EVAL.md). Without windows to divide there is nothing to
// floor, and the fill pass decides.
func withinFloor(p *Preset, chosen []selInterval, cand selInterval, assetDur float64) bool {
	phases, ok := windowPhases(p, assetDur)
	if !ok {
		return true
	}
	return windowCount(chosen, cand, assetDur/phases) < p.Diversity.MinPerWindow
}

func diverse(p *Preset, chosen []selInterval, cand selInterval, assetDur float64) bool {
	if p.Diversity.MaxPerWindow > 0 {
		// The window is a slice of *this* asset's own duration, so the rule
		// means "at most N clips per phase" on a 47-second drill clip just as
		// it does on a 10-minute match. An absolute window length would
		// silently truncate short sources into a single phase and drop the
		// reel's tail — which is exactly what this rule exists to prevent.
		if phases, ok := windowPhases(p, assetDur); ok {
			if windowCount(chosen, cand, assetDur/phases) >= p.Diversity.MaxPerWindow {
				return false
			}
		}
	}
	for _, c := range chosen {
		if c.assetID != cand.assetID {
			continue
		}
		if p.Diversity.MinGap > 0 {
			gap := cand.start - c.end
			if gap < 0 {
				gap = c.start - cand.end
			}
			// Overlapping candidates have a negative "gap": always too close.
			if gap < p.Diversity.MinGap {
				return false
			}
		}
		if p.Diversity.MaxOverlapIoU > 0 {
			inter := math.Min(cand.end, c.end) - math.Max(cand.start, c.start)
			if inter > 0 {
				interLen := inter
				union := (cand.end - cand.start) + (c.end - c.start) - interLen
				if union > 0 && interLen/union > p.Diversity.MaxOverlapIoU {
					return false
				}
			}
		}
	}
	return true
}

// rawFactors is one segment's un-normalized score component values (they
// live on incomparable scales: motion ratios, dB, seconds, counts).
type rawFactors struct {
	motion, audio, duration, hits, density float64
}

func rawSegmentFactors(s event.Segment) rawFactors {
	return rawFactors{
		motion:   s.MeanMotion,
		audio:    s.MeanAudioDB,
		duration: s.Duration(),
		hits:     float64(s.HitCount),
		density:  s.HitDensity,
	}
}

// component is one accessor of rawFactors plus its observed min and span
// across the candidate set.
type component struct {
	get       func(rawFactors) float64
	set       func(f *factors, v float64)
	min, span float64
}

// components lists the scored components. Higher is better for every one
// of them (louder, more motion, longer, denser).
func components() []component {
	return []component{
		{func(r rawFactors) float64 { return r.motion }, func(f *factors, v float64) { f.motion = v }, 0, 0},
		{func(r rawFactors) float64 { return r.audio }, func(f *factors, v float64) { f.audio = v }, 0, 0},
		{func(r rawFactors) float64 { return r.duration }, func(f *factors, v float64) { f.duration = v }, 0, 0},
		{func(r rawFactors) float64 { return r.hits }, func(f *factors, v float64) { f.hits = v }, 0, 0},
		{func(r rawFactors) float64 { return r.density }, func(f *factors, v float64) { f.density = v }, 0, 0},
	}
}

// relativize maps each component to (v-min)/(max-min) across the candidate
// set. A component identical across all candidates maps to 1.0 everywhere
// (0/0): it carries no discriminating information, and neutrality keeps
// the weights' relative meaning.
func relativize(all []rawFactors) []factors {
	comps := components()
	out := make([]factors, len(all))
	for _, c := range comps {
		minv, maxv := c.get(all[0]), c.get(all[0])
		for _, r := range all {
			v := c.get(r)
			if v < minv {
				minv = v
			}
			if v > maxv {
				maxv = v
			}
		}
		c.span = maxv - minv
		c.min = minv
		for i, r := range all {
			v := 1.0
			if c.span != 0 {
				v = clamp((c.get(r)-c.min)/c.span, 0, 1)
			}
			c.set(&out[i], v)
		}
	}
	return out
}

// trimSegment cuts a segment to the desired clip length: the full segment
// when short, else a window capped by remaining target time.
//
// When the caller knows where the point actually ended (boundaries), the
// window is placed to END there instead of starting at the segment head —
// provided that end is reachable without leaving the segment. One rule covers
// both jobs: a boundary inside the window trims the dead tail after the point,
// a boundary beyond it slides the window forward so the clip contains the
// rally's finish rather than its first eight seconds.
// trimSegment shapes one clip out of a segment. The third result is the point
// boundary the window was ended at — 0 when the clip is start-anchored — so the
// caller can record *why* this clip stops where it does instead of leaving the
// reader to infer it from a duration.
func trimSegment(p *Preset, seg event.Segment, remaining float64, boundaries []float64) (start, end, atBoundary float64, anchor string, ok bool) {
	length := seg.Duration()
	if length > p.MaxClipDuration {
		length = p.MaxClipDuration
	}
	if length > remaining {
		length = remaining
	}
	if length < p.MinClipDuration {
		return 0, 0, 0, "", false
	}

	// Default anchor, unchanged for segments that carry no hit times: the
	// window starts at the segment head.
	anchor = anchorHead
	start = round4(seg.Start)
	end = round4(start + length)

	// Rally mode: the window ENDS where the play ended — the last hit plus
	// the landing tail — so a dive at the buzzer and the shuttle coming down
	// stay inside the clip instead of an arithmetic edge cutting the point
	// mid-air. The window then reaches back for its length.
	if len(seg.Hits) > 0 {
		lastHit := seg.Hits[len(seg.Hits)-1]
		naturalEnd := lastHit + rallyTailSeconds(p)
		if naturalEnd <= seg.End+hitTailEps {
			end = naturalEnd
		} else {
			end = seg.End // the segment's own pad already holds the tail
		}
		start = end - length
		if start < seg.Start {
			start = seg.Start
			end = start + length
		}
		anchor = anchorRallyEnd
	}

	// Scoreboard boundaries (sidecar-measured point ends) outrank the hit
	// heuristic and keep the semantics they always had.
	if b, found := reachableBoundary(boundaries, seg, p.MinClipDuration); found {
		newStart := b - length
		if newStart < seg.Start {
			newStart = seg.Start
		}
		start, end = round4(newStart), round4(b)
		atBoundary, anchor = b, anchorBoundary
	}
	return round4(start), round4(end), atBoundary, anchor, true
}

// defaultRallyTail mirrors internal/event's default rally_pad: the landing
// tail a clip keeps after the last hit when the style says nothing.
const defaultRallyTail = 1.2

// rallyTailSeconds is the landing tail a clip keeps after the last hit: the
// style's own rally_pad (the seconds that already pad segment bounds), so the
// space between "last hit" and "players walk away" stays one number.
func rallyTailSeconds(p *Preset) float64 {
	if p.EventConfig.RallyPad > 0 {
		return p.EventConfig.RallyPad
	}
	return defaultRallyTail
}

// hitTailEps absorbs round4 rounding on hit timestamps.
const hitTailEps = 0.01

// Clip-end anchors, recorded into clip metadata so a reel explains not only
// why a clip was picked but also why it stops where it stops.
const (
	anchorBoundary = "scoreboard point end"
	anchorRallyEnd = "last hit + landing tail"
	anchorHead     = "segment head"
)

// reachableBoundary picks the first boundary that can serve as a clip end:
// at least one minimum-clip inside the segment, and not past its end (the
// engine may not show material the detector did not attribute to this event).
// Boundaries arrive unsorted from external tools, so they are scanned, not
// indexed; the count is points in a match, not frames.
func reachableBoundary(boundaries []float64, seg event.Segment, minClip float64) (float64, bool) {
	best := math.Inf(1)
	for _, b := range boundaries {
		if math.IsNaN(b) || b < seg.Start+minClip || b > seg.End {
			continue
		}
		if b < best {
			best = b
		}
	}
	if math.IsInf(best, 1) {
		return 0, false
	}
	return best, true
}

// moveTopShotFirst brings the shot the style itself scored highest to the front
// of the reel and leaves the rest exactly where match order put them. A hook is
// one clip moved — re-sorting the whole reel by score would scatter the rally
// chronology the tail exists to show. A clip whose score does not parse is not a
// candidate, so a document with nothing scored keeps the order it had.
func moveTopShotFirst(clips []timeline.Clip) {
	best := -1
	var bestScore float64
	for i := range clips {
		v, err := strconv.ParseFloat(clips[i].Metadata["score"], 64)
		if err != nil {
			continue
		}
		if best < 0 || v > bestScore {
			best, bestScore = i, v
		}
	}
	if best <= 0 {
		return
	}
	top := clips[best]
	copy(clips[1:best+1], clips[:best])
	clips[0] = top
}

// DefaultMotionZoom is the window the per-clip picker opens a clip with when nobody
// names one: 15% off the frame edge, which reads as a deliberate move at phone size
// without cropping a subject out of the shot.
const DefaultMotionZoom = 0.85

// MotionFor is the one rule that turns a camera-motion name into one clip's plan
// (运镜). framingPlan applies it across a reel and the per-clip endpoint applies it to
// a single clip, so the geometry a user chooses cannot disagree with the geometry a
// style would have produced. ordinal only decides which way a drift runs, so a reel
// alternates its pans instead of repeating one metronome.
func MotionFor(mode string, zoom float64, roi *MotionROI, ordinal int) (*timeline.Motion, error) {
	if mode == "" || mode == FramingNone {
		return nil, nil // no window: the clip shows the whole frame
	}
	if zoom <= 0 || zoom > 1 {
		return nil, xcerr.E(xcerr.CodeValidation, fmt.Sprintf(
			"camera motion zoom %g out of (0,1] — a mode without a window is not a plan", zoom), nil)
	}
	switch mode {
	case FramingPunchIn:
		return &timeline.Motion{Zoom: zoom}, nil
	case FramingDrift:
		from, to := 0.35, 0.65
		if ordinal%2 == 1 {
			from, to = to, from
		}
		return &timeline.Motion{Zoom: zoom, From: []float64{from, 0.5}, To: []float64{to, 0.5}}, nil
	case FramingROI:
		if roi == nil {
			return nil, xcerr.E(xcerr.CodeValidation,
				"this asset has no region to aim at — draw one in the Regions panel first", nil)
		}
		cx, cy := clamp(roi.X+roi.W/2, 0, 1), clamp(roi.Y+roi.H/2, 0, 1)
		return &timeline.Motion{Zoom: zoom, From: []float64{cx, cy}, To: []float64{cx, cy}}, nil
	}
	return nil, xcerr.E(xcerr.CodeValidation,
		fmt.Sprintf("camera motion %q is not one of none/punch_in/drift/roi", mode), nil)
}

// framingPlan turns the style's camera-motion policy into one clip's plan (运镜).
// The ordinal only decides which way a drift runs, so a reel alternates its pans
// instead of repeating one metronome, while the same inputs still produce the
// same document.
func framingPlan(p *Preset, a AssetInfo, ordinal int) *timeline.Motion {
	plan, err := MotionFor(p.CameraMotion.Mode, p.CameraMotion.Zoom, a.ROI, ordinal)
	if err != nil {
		// A preset's mode and zoom are both validated in Resolve, so in practice
		// this is "roi on an asset with no region": the clip frames the whole
		// picture, which is what it did before this function had a second caller.
		return nil
	}
	return plan
}

// snapEnd moves an otherwise-unfixed clip end onto the nearest beat of the
// source's grid, within the preset's tolerance. It is deliberately narrower than
// "cut on the beat": the end may not pass the material the detector attributed
// to this event, may not lengthen the clip past max_clip_duration, and may not
// eat the clip's own minimum. Nearest wins; the scan order breaks a tie, so an
// unsorted grid is still deterministic.
func snapEnd(p *Preset, start, end float64, seg event.Segment, beats []float64) (float64, bool) {
	if p.BeatSnapTolerance <= 0 || len(beats) == 0 {
		return end, false
	}
	lo := start + p.MinClipDuration
	hi := math.Min(seg.End, start+p.MaxClipDuration)
	best, bestDist := end, p.BeatSnapTolerance+1
	for _, b := range beats {
		d := math.Abs(b - end)
		if d > p.BeatSnapTolerance || d >= bestDist || b < lo || b > hi {
			continue
		}
		best, bestDist = b, d
	}
	if bestDist > p.BeatSnapTolerance {
		return end, false
	}
	return round4(best), true
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func round4(v float64) float64 {
	return math.Round(v*10000) / 10000
}
