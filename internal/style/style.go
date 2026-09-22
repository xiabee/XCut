// Package style implements XCut's Style Engine: user-selectable presets that
// turn EventSegments into a validated Timeline. Styles are DATA, not code —
// no `if style == "ktv"` anywhere (D7).
//
// Presets are JSON with schema validation (unknown fields rejected). Built-ins
// are embedded in the binary; users may override or add presets in
// <workspace>/styles/<name>.json.
package style

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/xiabee/XCut/internal/analysis"
	"github.com/xiabee/XCut/internal/event"
	"github.com/xiabee/XCut/internal/timeline"
	"github.com/xiabee/XCut/internal/xcerr"
)

// roiRect converts the preset's ROI block to the analysis type.
func roiRect(r *MotionROI) analysis.ROI {
	return analysis.ROI{X: r.X, Y: r.Y, W: r.W, H: r.H}
}

// Analyzers returns the extra analyzers this preset requires (a cropped
// motion pass when a motion ROI is configured). Append them to the baseline
// set before analysis; empty when the preset is plain.
func (p *Preset) Analyzers() []analysis.Analyzer {
	if p.MotionROI == nil {
		return nil
	}
	return []analysis.Analyzer{analysis.FrameDiffROIAnalyzer{ROI: roiRect(p.MotionROI)}}
}

// Scoring weights (each applied to a 0..1 normalized factor). Hits and
// Density apply to rally-mode segments (hit_count / hit_density); zero
// weights keep plain activity presets behaving exactly as before.
type Scoring struct {
	Motion   float64 `json:"motion"`
	Audio    float64 `json:"audio"`
	Duration float64 `json:"duration"`
	Hits     float64 `json:"hits,omitempty"`
	Density  float64 `json:"density,omitempty"`
}

// AudioGain linear loudness multiplier applied to clip volume.
type Audio struct {
	Gain float64 `json:"gain"` // 0..1
	// The mix used when a run lays a music bed under the reel: the bed plays at
	// MusicGain, the clips' own audio at SourceGain. Unset means the product's own
	// starting mix (pipeline's defaults), not silence — a preset that never
	// mentions music parses and renders exactly as it did before these fields.
	MusicGain  *float64 `json:"music_gain,omitempty"`
	SourceGain *float64 `json:"source_gain,omitempty"`
}

// Diversity suppresses near-duplicate picks. Zero-valued = disabled (older
// presets parse unchanged and keep the plain top-N behavior).
type Diversity struct {
	// MinGap is the minimum source-time distance (seconds) between selected
	// clips of the same asset; a candidate closer than this to any selected
	// clip is rejected.
	MinGap float64 `json:"min_gap,omitempty"`
	// MaxOverlapIoU rejects a candidate whose temporal IoU with an
	// already-selected clip exceeds this (0..1). 0 = rule disabled.
	MaxOverlapIoU float64 `json:"max_overlap_iou,omitempty"`
	// MaxPerWindow caps how many clips may come from any one time region of the
	// source. MinGap and MaxOverlapIoU only push picks apart locally, so a loud
	// stretch can still take the whole reel and leave the closing phase
	// unrepresented; this rule is what makes a highlight cover the match.
	// Phases says how many such regions the source is divided into (window
	// length is derived from the asset's own duration, so the rule behaves the
	// same on a 47-second clip and a 10-minute match). Both must be set
	// (Phases >= 2) to enable it; MaxPerWindow 0 disables the rule.
	//
	// Phases is a *floor*, not a ceiling on the reel: when target_duration
	// could hold more clips than Phases x MaxPerWindow, the source is divided
	// into more, finer windows rather than allowing more picks per window. The
	// rule keeps doing its job (spread) instead of quietly deciding how long
	// the reel may be — see docs/EVAL.md for the measurement that found this.
	MaxPerWindow int `json:"max_per_window,omitempty"`
	// MinPerWindow is the other half of MaxPerWindow: before any window may take
	// a second clip, each window that still has none takes its first one. A
	// ceiling alone is satisfied by two clips in the opening minute and nothing
	// in the closing one — which is the reel the owner's match produced (8 clips,
	// ten consecutive rallies unrepresented, docs/EVAL.md). 0 = rule disabled,
	// so an unset preset keeps today's ranking exactly.
	MinPerWindow int `json:"min_per_window,omitempty"`
	Phases       int `json:"phases,omitempty"`
}

// Framing modes for CameraMotion (运镜). Each is one sentence about where the
// window sits; the renderer turns it into pixels and nothing else reads them.
const (
	FramingNone    = "none"
	FramingPunchIn = "punch_in"
	FramingDrift   = "drift"
	FramingROI     = "roi"
)

// Clip orders (Preset.ClipOrder): how the selected shots are arranged in the
// reel. The name exists because a short-form cut and a match recap want
// different answers to the same set of picks.
const (
	// ClipOrderChronological plays the picks in the order they happened. It is
	// also the empty value, because that is what every preset written before
	// this knob exists says, and it must keep meaning the same thing.
	ClipOrderChronological = "chronological"
	// ClipOrderHookFirst leads with the shot the style itself scored highest and
	// keeps the rest in match order. One clip moves — sorting the whole reel by
	// score would scatter the rally chronology, which is a different claim.
	ClipOrderHookFirst = "hook_first"
)

// CameraMotion assigns a framing plan to every clip the style selects.
//
// The default is no motion, and not for lack of a use case: cropping a broadcast
// can cut the score bug out of the shot, and no aesthetic claim about pans has
// been measured on this project's footage, so a style has to ask for it by name.
// Zoom is the window's height as a fraction of the source's, so 0.8 magnifies by
// 1.25x; it is required when a mode is set rather than defaulted, because a
// preset that says "punch_in" and a preset that says nothing should not render
// the same.
type CameraMotion struct {
	Mode string  `json:"mode,omitempty"` // none | punch_in | drift | roi
	Zoom float64 `json:"zoom,omitempty"` // (0,1]
}

// MotionROI is a normalized region of interest (0..1) for motion analysis
// (a court area). When set and event_config.motion_track is empty, the
// builder analyzes "frame_diff_roi" instead of full-frame motion — a
// court-confined signal that crowd movement cannot dominate. nil = full
// frame (default).
type MotionROI struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

// Transition applied between selected clips.
type Transition struct {
	Type     string  `json:"type"` // cut | fade
	Duration float64 `json:"duration"`
}

// Preset is one style definition.
type Preset struct {
	Name            string          `json:"name"`
	Title           string          `json:"title"`
	Version         int             `json:"version"`
	Canvas          timeline.Canvas `json:"canvas"`
	TargetDuration  float64         `json:"target_duration"`
	MinClipDuration float64         `json:"min_clip_duration"`
	MaxClipDuration float64         `json:"max_clip_duration"`
	// BeatSnapTolerance lets a clip end that nothing else has fixed move to the
	// nearest beat of the source's own grid, so cuts land where the audio's
	// pulse is (卡点). 0 = off. Capped well below a clip's length on purpose:
	// past that it is re-timing the shot, not snapping the cut.
	BeatSnapTolerance float64      `json:"beat_snap_tolerance,omitempty"`
	Scoring           Scoring      `json:"scoring"`
	EventConfig       event.Config `json:"event_config"`
	Transition        Transition   `json:"transition"`
	Audio             Audio        `json:"audio"`
	Diversity         Diversity    `json:"diversity,omitempty"`
	CameraMotion      CameraMotion `json:"camera_motion,omitempty"`
	// ClipOrder arranges the shots the selector chose. Unset means
	// chronological, which is what every existing preset means today.
	ClipOrder string     `json:"clip_order,omitempty"`
	MotionROI *MotionROI `json:"motion_roi,omitempty"`

	// Source records where the preset was loaded from (not serialized).
	Source string `json:"-"`
}

// Validate enforces the preset schema contract.
func (p *Preset) Validate() error {
	var errs []string
	add := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }

	if strings.TrimSpace(p.Name) == "" {
		add("name must not be empty")
	}
	if p.Version != 1 {
		add("unsupported preset version %d", p.Version)
	}
	if p.Canvas.FPS <= 0 || p.Canvas.FPS > 240 {
		add("canvas.fps %g invalid", p.Canvas.FPS)
	}
	if p.Canvas.Width < 16 || p.Canvas.Width > 7680 || p.Canvas.Width%2 != 0 {
		add("canvas.width %d invalid", p.Canvas.Width)
	}
	if p.Canvas.Height < 16 || p.Canvas.Height > 4320 || p.Canvas.Height%2 != 0 {
		add("canvas.height %d invalid", p.Canvas.Height)
	}
	if p.TargetDuration <= 0 || p.TargetDuration > 3600 {
		add("target_duration %g out of (0,3600]", p.TargetDuration)
	}
	if p.MinClipDuration <= 0 || p.MinClipDuration > p.MaxClipDuration {
		add("min_clip_duration must be in (0, max_clip_duration]")
	}
	if p.MaxClipDuration > p.TargetDuration {
		add("max_clip_duration %g exceeds target_duration %g", p.MaxClipDuration, p.TargetDuration)
	}
	if p.BeatSnapTolerance < 0 {
		add("beat_snap_tolerance must be >= 0")
	}
	// A snap is a trim adjustment, not a re-timing: beyond half a second (a
	// third of the shortest clip this project ships) it would move a cut further
	// than the cut's own precision, and beyond that the grid stops being a hint.
	if p.BeatSnapTolerance > 0.5 {
		add("beat_snap_tolerance %g out of [0,0.5]", p.BeatSnapTolerance)
	}
	if p.BeatSnapTolerance > 0 && p.MinClipDuration > 0 && p.BeatSnapTolerance >= p.MinClipDuration {
		add("beat_snap_tolerance %g is at least min_clip_duration %g, which would let a snap empty a clip",
			p.BeatSnapTolerance, p.MinClipDuration)
	}
	w := p.Scoring
	if w.Motion < 0 || w.Audio < 0 || w.Duration < 0 || w.Hits < 0 || w.Density < 0 {
		add("scoring weights must be >= 0")
	}
	if w.Motion+w.Audio+w.Duration+w.Hits+w.Density <= 0 {
		add("scoring weights must not all be zero")
	}
	if p.Transition.Type != "cut" && p.Transition.Type != "fade" && p.Transition.Type != "xfade" {
		add("transition.type %q unsupported (cut|fade|xfade)", p.Transition.Type)
	}
	if p.Transition.Type == "xfade" && p.Transition.Duration <= 0 {
		add("transition.duration must be > 0 for xfade")
	}
	if p.Transition.Duration < 0 {
		add("transition.duration must be >= 0")
	}
	if p.Audio.Gain < 0 || p.Audio.Gain > 1 {
		add("audio.gain must be in [0,1]")
	}
	// A mix level is only meaningful inside that range; 0 is refused rather than
	// silently reading as "unset", because a preset that wants silence says so by
	// leaving the bed out of the run.
	for _, g := range []struct {
		name string
		v    *float64
	}{{"audio.music_gain", p.Audio.MusicGain}, {"audio.source_gain", p.Audio.SourceGain}} {
		if g.v == nil {
			continue
		}
		if math.IsNaN(*g.v) || *g.v <= 0 || *g.v > 1 {
			add("%s %g out of (0,1]", g.name, *g.v)
		}
	}
	if math.IsNaN(p.Diversity.MinGap) || p.Diversity.MinGap < 0 {
		add("diversity.min_gap must be >= 0")
	}
	if math.IsNaN(p.Diversity.MaxOverlapIoU) ||
		p.Diversity.MaxOverlapIoU < 0 || p.Diversity.MaxOverlapIoU > 1 {
		add("diversity.max_overlap_iou must be in [0,1]")
	}
	if p.Diversity.MaxPerWindow < 0 {
		add("diversity.max_per_window must be >= 0")
	}
	if p.Diversity.MaxPerWindow > 0 && p.Diversity.Phases < 2 {
		add("diversity.phases must be >= 2 when max_per_window is set")
	}
	if p.Diversity.MaxPerWindow == 0 && p.Diversity.Phases < 0 {
		add("diversity.phases must be >= 0")
	}
	if p.Diversity.MinPerWindow < 0 {
		add("diversity.min_per_window must be >= 0")
	}
	if p.Diversity.MinPerWindow > 0 && p.Diversity.Phases < 2 {
		add("diversity.phases must be >= 2 when min_per_window is set")
	}
	if p.Diversity.MinPerWindow > 0 && p.Diversity.MaxPerWindow > 0 &&
		p.Diversity.MinPerWindow > p.Diversity.MaxPerWindow {
		add("diversity.min_per_window %d exceeds max_per_window %d, which no candidate could satisfy",
			p.Diversity.MinPerWindow, p.Diversity.MaxPerWindow)
	}
	switch p.CameraMotion.Mode {
	case "", FramingNone, FramingPunchIn, FramingDrift, FramingROI:
	default:
		add("camera_motion.mode %q is not one of none/punch_in/drift/roi", p.CameraMotion.Mode)
	}
	switch p.ClipOrder {
	case "", ClipOrderChronological, ClipOrderHookFirst:
	default:
		// Refused rather than read as the default: a preset that misspells its
		// order would keep playing the clips in match order while promising a
		// hook, and the pacing readout would then be describing a rule nobody
		// wrote.
		add("clip_order %q is not one of chronological/hook_first", p.ClipOrder)
	}
	if p.CameraMotion.Mode != "" && p.CameraMotion.Mode != FramingNone &&
		(p.CameraMotion.Zoom <= 0 || p.CameraMotion.Zoom > 1) {
		add("camera_motion.zoom %g out of (0,1] — a mode without a window is not a plan",
			p.CameraMotion.Zoom)
	}
	if err := p.EventConfig.Validate(); err != nil {
		add("event_config: %v", err)
	}
	if p.MotionROI != nil {
		r := roiRect(p.MotionROI)
		if !r.Valid() {
			add("motion_roi must satisfy 0<=x,y and w,h>0 and x+w,y+h<=1")
		}
		// The ROI is meaningless unless analysis consumes the ROI motion
		// track; auto-wire when the preset did not pick a track itself.
		if p.EventConfig.MotionTrack == "" {
			p.EventConfig.MotionTrack = "frame_diff_roi"
		}
	}
	if len(errs) > 0 {
		return xcerr.E(xcerr.CodeValidation,
			fmt.Sprintf("invalid style preset %q: %s", p.Name, strings.Join(errs, "; ")), nil)
	}
	return nil
}

// Parse decodes + validates a preset from JSON bytes. Unknown fields are
// rejected so typos (e.g. "traget_duration") fail loudly instead of silently
// using defaults.
func Parse(b []byte) (*Preset, error) {
	p := &Preset{}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(p); err != nil {
		return nil, xcerr.E(xcerr.CodeValidation, "invalid style preset JSON", err)
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return p, nil
}

// maxPresetBytes caps preset file size (a preset is <10 KB by design).
const maxPresetBytes = 256 << 10

// Load reads a preset by name from search paths (dir first, then embedded).
func Load(name string, extraDirs ...string) (*Preset, error) {
	if !validName(name) {
		return nil, xcerr.E(xcerr.CodeValidation, "invalid style name: "+name, nil)
	}
	fname := name + ".json"
	for _, dir := range extraDirs {
		p := filepath.Join(dir, fname)
		if fi, err := os.Stat(p); err == nil && fi.Size() > maxPresetBytes {
			return nil, xcerr.E(xcerr.CodeValidation,
				fmt.Sprintf("style preset too large (%d bytes)", fi.Size()), nil)
		}
		b, err := os.ReadFile(p)
		if err == nil {
			preset, perr := Parse(b)
			if perr != nil {
				return nil, perr
			}
			preset.Source = "file:" + p
			return preset, nil
		}
		if !os.IsNotExist(err) {
			return nil, xcerr.E(xcerr.CodeInternal, "cannot read style preset "+p, err)
		}
	}
	b, ok := embedded[name]
	if !ok {
		return nil, xcerr.E(xcerr.CodeNotFound, "unknown style: "+name, nil)
	}
	preset, err := Parse(b)
	if err != nil {
		return nil, err
	}
	preset.Source = "embedded"
	return preset, nil
}

func validName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
		default:
			return false
		}
	}
	return true
}

// Names returns available style names (embedded + files in extraDirs),
// sorted: the map iteration order is randomized per process, and consumers
// build UI pickers from this list — an unsorted list would flip the
// default-selected style across restarts. Names that Load would reject
// (e.g. "My Style" — see validName) are not listed.
func Names(extraDirs ...string) []string {
	seen := map[string]bool{}
	var out []string
	for _, dir := range extraDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			n := strings.TrimSuffix(e.Name(), ".json")
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	for n := range embedded {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	filtered := out[:0]
	for _, n := range out {
		if validName(n) {
			filtered = append(filtered, n)
		}
	}
	sort.Strings(filtered)
	return filtered
}
