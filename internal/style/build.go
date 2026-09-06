package style

import (
	"math"
	"sort"
	"strconv"

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

// Build constructs a deterministic Timeline from per-asset events and a
// preset.
//
// Selection: events are re-scored with preset weights, ranked (ties broken by
// time), greedily taken while the target duration allows, trimmed to a
// centered window, then emitted chronologically. Identical inputs always
// produce identical output (property tested).
func Build(preset *Preset, projectID string, items []AssetEvents) (*timeline.Timeline, error) {
	if len(items) == 0 {
		return nil, xcerr.E(xcerr.CodeValidation, "no assets to build timeline from", nil)
	}
	durations := map[string]float64{}

	type candidate struct {
		asset   AssetInfo
		seg     event.Segment
		score   float64
	}
	var cands []candidate
	for _, it := range items {
		durations[it.Asset.ID] = it.Asset.DurationSec
		for _, s := range it.Segments {
			if s.Duration() < preset.MinClipDuration {
				continue
			}
			cands = append(cands, candidate{asset: it.Asset, seg: s, score: scoreSegment(preset, s)})
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

	// Greedy selection up to the target duration.
	total := 0.0
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
		n++
		clips = append(clips, timeline.Clip{
			ID:          "clip_" + strconv.Itoa(n),
			AssetID:     c.asset.ID,
			SourcePath:  c.asset.Path,
			SourceStart: srcStart,
			SourceEnd:   srcEnd,
			Speed:       1,
			Volume:      preset.Audio.Gain,
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

// scoreSegment applies preset weights to normalized segment factors. The
// normalization constants match event scoring (0.30 full-scale motion,
// 36 dB dynamic range, 15 s "long" segment) and are intentionally shared.
func scoreSegment(p *Preset, s event.Segment) float64 {
	motionN := clamp(s.MeanMotion/0.30, 0, 1)
	audioN := clamp((s.MeanAudioDB-p.EventConfig.SilenceDB)/36.0, 0, 1)
	durN := clamp(s.Duration()/15.0, 0, 1)
	return p.Scoring.Motion*motionN + p.Scoring.Audio*audioN + p.Scoring.Duration*durN
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
