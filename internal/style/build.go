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
	total := 0.0
	var chosen []selInterval
	var clips []timeline.Clip
	n := 0
	for _, c := range cands {
		remaining := preset.TargetDuration - total
		if remaining <= 0 {
			break
		}
		srcStart, srcEnd, atBoundary, ok := trimSegment(preset, c.seg, remaining, c.boundaries)
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
		// Diversity operates on the trimmed window — what the reel will
		// actually show — not on the wider source segment.
		cand := selInterval{assetID: c.asset.ID, start: srcStart, end: srcEnd}
		if !diverse(preset, chosen, cand, durations[c.asset.ID]) {
			continue
		}
		n++
		chosen = append(chosen, cand)
		md := map[string]string{
			"score":           strconv.FormatFloat(round4(c.score), 'f', -1, 64),
			"score_breakdown": c.f.breakdown(preset),
			"reason":          c.f.reason(preset),
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
		clips = append(clips, timeline.Clip{
			ID:          "clip_" + strconv.Itoa(n),
			AssetID:     c.asset.ID,
			SourcePath:  c.asset.Path,
			SourceStart: srcStart,
			SourceEnd:   srcEnd,
			Speed:       1,
			Volume:      preset.Audio.Gain,
			Metadata:    md,
		})
		total += srcEnd - srcStart
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
func diverse(p *Preset, chosen []selInterval, cand selInterval, assetDur float64) bool {
	if p.Diversity.MaxPerWindow > 0 && p.Diversity.Phases >= 2 && assetDur > 0 {
		// The window is a slice of *this* asset's own duration, so the rule
		// means "at most N clips per phase" on a 47-second drill clip just as
		// it does on a 10-minute match. An absolute window length would
		// silently truncate short sources into a single phase and drop the
		// reel's tail — which is exactly what this rule exists to prevent.
		phases := float64(p.Diversity.Phases)
		// A reel asked for longer than the quota can hold needs *more* windows,
		// not a looser quota per window: that keeps the spread discipline that
		// the rule exists for while letting the cut use the budget it was
		// given. Measured on a 603 s match: with a fixed 5×2 quota a 240 s
		// request stopped at 10 clips / 80 s (recall 0.140); scaling the
		// window count instead reached 19 clips at equal precision (0.267).
		if p.MaxClipDuration > 0 && p.TargetDuration > 0 {
			room := int(math.Ceil(p.TargetDuration / p.MaxClipDuration))
			if quota := p.Diversity.Phases * p.Diversity.MaxPerWindow; room > quota {
				phases = math.Max(phases, math.Ceil(float64(room)/float64(p.Diversity.MaxPerWindow)))
			}
		}
		win := assetDur / phases
		phase := int(cand.start / win)
		inPhase := 0
		for _, c := range chosen {
			if c.assetID == cand.assetID && int(c.start/win) == phase {
				inPhase++
			}
		}
		if inPhase >= p.Diversity.MaxPerWindow {
			return false
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
func trimSegment(p *Preset, seg event.Segment, remaining float64, boundaries []float64) (start, end, atBoundary float64, ok bool) {
	length := seg.Duration()
	if length > p.MaxClipDuration {
		length = p.MaxClipDuration
	}
	if length > remaining {
		length = remaining
	}
	if length < p.MinClipDuration {
		return 0, 0, 0, false
	}
	start = round4(seg.Start)
	end = round4(start + length)
	if b, found := reachableBoundary(boundaries, seg, p.MinClipDuration); found {
		newStart := b - length
		if newStart < seg.Start {
			newStart = seg.Start
		}
		start, end = round4(newStart), round4(b)
		atBoundary = b
	}
	return start, end, atBoundary, true
}

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
