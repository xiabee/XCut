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
	"strings"

	"github.com/xiabee/XCut/internal/event"
	"github.com/xiabee/XCut/internal/timeline"
	"github.com/xiabee/XCut/internal/xcerr"
)

// Scoring weights (each applied to a 0..1 normalized factor).
type Scoring struct {
	Motion   float64 `json:"motion"`
	Audio    float64 `json:"audio"`
	Duration float64 `json:"duration"`
}

// AudioGain linear loudness multiplier applied to clip volume.
type Audio struct {
	Gain float64 `json:"gain"` // 0..1
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
	Scoring         Scoring         `json:"scoring"`
	EventConfig     event.Config    `json:"event_config"`
	Transition      Transition      `json:"transition"`
	Audio           Audio           `json:"audio"`
	Diversity       Diversity       `json:"diversity,omitempty"`

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
	w := p.Scoring
	if w.Motion < 0 || w.Audio < 0 || w.Duration < 0 {
		add("scoring weights must be >= 0")
	}
	if w.Motion+w.Audio+w.Duration <= 0 {
		add("scoring weights must not all be zero")
	}
	if p.Transition.Type != "cut" && p.Transition.Type != "fade" {
		add("transition.type %q unsupported (cut|fade)", p.Transition.Type)
	}
	if p.Transition.Duration < 0 {
		add("transition.duration must be >= 0")
	}
	if p.Audio.Gain < 0 || p.Audio.Gain > 1 {
		add("audio.gain must be in [0,1]")
	}
	if math.IsNaN(p.Diversity.MinGap) || p.Diversity.MinGap < 0 {
		add("diversity.min_gap must be >= 0")
	}
	if math.IsNaN(p.Diversity.MaxOverlapIoU) ||
		p.Diversity.MaxOverlapIoU < 0 || p.Diversity.MaxOverlapIoU > 1 {
		add("diversity.max_overlap_iou must be in [0,1]")
	}
	if err := p.EventConfig.Validate(); err != nil {
		add("event_config: %v", err)
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

// Names returns available style names (embedded + files in extraDirs).
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
	return out
}
