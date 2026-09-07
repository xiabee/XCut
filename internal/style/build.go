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
		asset AssetInfo
		seg   event.Segment
		score float64
		f     factors
	}
	var cands []candidate
	for _, it := range items {
		durations[it.Asset.ID] = it.Asset.DurationSec
		for _, s := range it.Segments {
			if s.Duration() < preset.MinClipDuration {
				continue
			}
			f := segmentFactors(preset, s)
			cands = append(cands, candidate{
				asset: it.Asset,
				seg:   s,
				score: f.weighted(preset),
				f:     f,
			})
		}
	}
	if len(cands) == 0 {
		return nil, xcerr.E(xcerr.CodeValidation, "no events satisfy the style's clip constraints", nil)
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
		srcStart, srcEnd, ok := trimSegment(preset, c.seg, remaining)
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
		if !diverse(preset, chosen, cand) {
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

	// Back-to-back placement.
	cursor := 0.0
	for i := range clips {
		clips[i].TimelineStart = round4(cursor)
		if i < len(clips)-1 && preset.Transition.Type != "cut" {
			clips[i].Transition = &timeline.Transition{
				Type:     preset.Transition.Type,
				Duration: math.Min(preset.Transition.Duration, clips[i].Duration()),
			}
		}
		cursor = round4(cursor + clips[i].Duration())
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
func diverse(p *Preset, chosen []selInterval, cand selInterval) bool {
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

// segmentFactors normalizes a segment into 0..1 score components. The
// motion/audio/duration constants match event scoring (0.30 full-scale
// motion, 36 dB dynamic range, 15 s "long" segment) and are intentionally
// shared; hits use 12-per-segment and 1.5 hits/s as full scale.
func segmentFactors(p *Preset, s event.Segment) factors {
	return factors{
		motion:   clamp(s.MeanMotion/0.30, 0, 1),
		audio:    clamp((s.MeanAudioDB-p.EventConfig.SilenceDB)/36.0, 0, 1),
		duration: clamp(s.Duration()/15.0, 0, 1),
		hits:     clamp(float64(s.HitCount)/12.0, 0, 1),
		density:  clamp(s.HitDensity/1.5, 0, 1),
	}
}

// scoreSegment applies preset weights to normalized segment factors.
func scoreSegment(p *Preset, s event.Segment) float64 {
	return segmentFactors(p, s).weighted(p)
}

// trimSegment cuts a segment to the desired clip length: the full segment
// when short, else a centered window capped by remaining target time.
func trimSegment(p *Preset, seg event.Segment, remaining float64) (start, end float64, ok bool) {
	length := seg.Duration()
	if length > p.MaxClipDuration {
		length = p.MaxClipDuration
	}
	if length > remaining {
		length = remaining
	}
	if length < p.MinClipDuration {
		return 0, 0, false
	}
	start = round4(seg.Start + (seg.Duration()-length)/2)
	end = round4(start + length)
	return start, end, true
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
