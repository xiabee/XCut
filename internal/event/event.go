// Package event derives EventSegments from analysis feature tracks.
//
// A segment is a maximal run of "active" time: frame difference above a motion
// floor, or audible audio above a silence floor. Scene cuts always split
// segments; short gaps merge; tiny segments drop. Scores are a deterministic,
// explainable heuristic — the Style Engine re-ranks segments with
// style-specific weights on top.
//
// All functions are pure: identical tracks + config ⇒ identical segments
// (property tested).
package event

import (
	"fmt"
	"math"
	"sort"

	"github.com/xiabee/XCut/internal/analysis"
	"github.com/xiabee/XCut/internal/xcerr"
)

// Segment is a candidate highlight interval on the media timeline.
type Segment struct {
	Start       float64 `json:"start"`
	End         float64 `json:"end"`
	Score       float64 `json:"score"`         // 0..1 base quality
	MeanMotion  float64 `json:"mean_motion"`   // mean frame_diff (0..1)
	MeanAudioDB float64 `json:"mean_audio_db"` // mean RMS dBFS; silentDB when silent/no audio

	// Rally-mode extras (empty in plain activity mode): the transient count
	// inside the segment and its per-second density — the explainable hit
	// story behind the score.
	Kind       string  `json:"kind,omitempty"`      // "" (activity) | "rally"
	HitCount   int     `json:"hit_count,omitempty"` // audio transients in segment
	HitDensity float64 `json:"hit_density,omitempty"`
	// Hits carries the transient timestamps themselves so the style engine can
	// end a clip where the play actually ended (last hit + landing tail)
	// instead of at an arithmetic window edge that can cut a rally mid-air.
	Hits []float64 `json:"hits,omitempty"`

	// PlayerPresence is the mean person-signature match (0..1) inside the
	// segment, from the player_presence track an asset carries when its user
	// seeded a player spot. HasPlayerPresence distinguishes "measured 0" from
	// "no signature on this asset".
	PlayerPresence    float64 `json:"player_presence,omitempty"`
	HasPlayerPresence bool    `json:"has_player_presence,omitempty"`
	// PeakRate is the max onset rate (hits/sec) in any 1-second window — a
	// burst of smashes scores higher than uniform play at the same average.
	PeakRate float64 `json:"peak_rate,omitempty"`
}

// Duration of the segment in seconds.
func (s Segment) Duration() float64 { return s.End - s.Start }

// Config controls event extraction.
type Config struct {
	CutThreshold float64 `json:"cut_threshold"` // frame_diff above this ⇒ scene cut (0..1)
	MotionFloor  float64 `json:"motion_floor"`  // frame_diff above this ⇒ visual activity (0..1)
	SilenceDB    float64 `json:"silence_db"`    // RMS above this ⇒ audible (dBFS)
	MergeGap     float64 `json:"merge_gap"`     // inactive gaps shorter than this merge (seconds)
	MinDuration  float64 `json:"min_duration"`  // segments shorter than this drop (seconds)

	// Mode selects the segmentation strategy: "" / "activity" (default:
	// runs of motion+audio) or "rally" (cluster audio transients into
	// rally candidates — the generic hit-sport shape, not style-specific).
	Mode string `json:"mode,omitempty"`
	// MotionTrack names the feature kind used as the motion signal
	// (default "frame_diff"; a court-ROI analysis produces
	// "frame_diff_roi" and presets opt in by name).
	MotionTrack string `json:"motion_track,omitempty"`

	// Rally parameters (rally mode only; zero = documented defaults).
	// Real court audio never goes silent — footsteps, speech and ambience
	// keep firing the onset detector through every break — so rallies are
	// separated by transient *density*, not by absolute quiet: a rally opens
	// when the onset rate reaches RallyEnterRate and closes only after the
	// rate has stayed at or below RallyExitRate for RallyGap seconds. A dense span
	// longer than one rally is chunked into consecutive rally-sized pieces,
	// never truncated.
	RallyGap       float64 `json:"rally_gap,omitempty"`        // seconds below exit rate that close a rally
	RallyPad       float64 `json:"rally_pad,omitempty"`        // padding after first/last hit
	MinHits        int     `json:"min_hits,omitempty"`         // transients required per rally
	RallyEnterRate float64 `json:"rally_enter_rate,omitempty"` // hits/sec that open a rally
	RallyExitRate  float64 `json:"rally_exit_rate,omitempty"`  // hits/sec at/below which a rally is ending
	// RallyChunk is the target length of one piece when a dense span is
	// longer than a single rally. It matters more than it looks: a segment
	// yields exactly one clip candidate, so a 30-second piece can only ever
	// contribute its first ~8 seconds to the reel and the rallies after it are
	// unreachable. Measured on a real match, no threshold in its plausible
	// range moved the reel, while this length is what bounds coverage.
	// Zero = defaultMaxRally (30 s), which is the historical behavior.
	RallyChunk float64 `json:"rally_chunk,omitempty"`
}

// DefaultConfig is a conservative generic baseline.
func DefaultConfig() Config {
	return Config{
		CutThreshold: 0.28,
		MotionFloor:  0.06,
		SilenceDB:    -42,
		MergeGap:     0.8,
		MinDuration:  1.0,
	}
}

// Validate checks the config is internally consistent.
func (c Config) Validate() error {
	sane := c.CutThreshold > 0 && c.CutThreshold <= 1 &&
		c.MotionFloor >= 0 && c.MotionFloor < c.CutThreshold &&
		c.MergeGap >= 0 && c.MinDuration > 0 &&
		!math.IsNaN(c.CutThreshold) && !math.IsNaN(c.MotionFloor) &&
		!math.IsNaN(c.MergeGap) && !math.IsNaN(c.MinDuration) &&
		finiteSilenceDB(c.SilenceDB)
	if !sane {
		return xcerr.E(xcerr.CodeValidation, "invalid event config", nil)
	}
	switch c.Mode {
	case "", ModeActivity, ModeRally:
	default:
		return xcerr.E(xcerr.CodeValidation,
			"invalid event mode "+c.Mode+" (activity|rally)", nil)
	}
	if c.RallyGap < 0 || c.RallyPad < 0 || c.MinHits < 0 ||
		math.IsNaN(c.RallyGap) || math.IsNaN(c.RallyPad) {
		return xcerr.E(xcerr.CodeValidation, "invalid rally parameters", nil)
	}
	// A chunk shorter than the smallest usable clip would turn every dense
	// span into a flood of near-identical candidates: the ceiling on candidate
	// count is this floor, so it is enforced rather than left to the preset.
	if c.RallyChunk < 0 || math.IsNaN(c.RallyChunk) || (c.RallyChunk > 0 && c.RallyChunk < minRallyChunk) {
		return xcerr.E(xcerr.CodeValidation,
			fmt.Sprintf("invalid rally_chunk: 0 (default) or >= %gs", minRallyChunk), nil)
	}
	if (c.RallyEnterRate < 0 || c.RallyExitRate < 0 ||
		math.IsNaN(c.RallyEnterRate) || math.IsNaN(c.RallyExitRate)) ||
		(c.RallyEnterRate > 0 && c.RallyExitRate > 0 && c.RallyExitRate >= c.RallyEnterRate) {
		return xcerr.E(xcerr.CodeValidation,
			"invalid rally rates: rally_exit_rate must be below rally_enter_rate", nil)
	}
	return nil
}

// silentDB stands in for -inf (digital silence) and no-audio assets.
const silentDB = -120.0

// cell is one position on the motion-sample time grid.
type cell struct {
	t       float64
	motion  float64 // frame_diff, 0..1
	db      float64 // audio RMS at this time (silentDB when absent)
	audible bool    // db > cfg.SilenceDB
	cut     bool    // motion > cfg.CutThreshold
}

// span accumulates a candidate interval and its statistics.
type span struct {
	start, end float64
	motionSum  float64
	motionN    int
	audioSum   float64
	audioN     int
}

// BuildStats explains how the gates disposed of candidate material. A
// style that rejects every candidate must be distinguishable from a video
// with nothing in it — "nothing found" and "found but refused" have
// different fixes (lower the floor vs. check the footage), and without
// counters the segmentation looks identical in both cases.
type BuildStats struct {
	// Rally mode: onset samples seen and density-walk spans opened.
	Onsets              int
	SpansOpened         int
	SpansDroppedMinHits int
	// Both modes: chunks/runs that reached the scoring gates.
	ChunksConsidered         int
	ChunksDroppedMinHits     int
	ChunksDroppedMinDuration int
	ChunksDroppedMotionFloor int
	// Segments that survived every gate.
	Segments int
}

// Build extracts segments from feature tracks over media of given duration.
// The returned stats explain any empty result (see BuildStats).
func Build(tracks []analysis.FeatureTrack, duration float64, cfg Config) ([]Segment, BuildStats, error) {
	var stats BuildStats
	if err := cfg.Validate(); err != nil {
		return nil, stats, err
	}
	if duration <= 0 || math.IsNaN(duration) || math.IsInf(duration, 0) {
		return nil, stats, xcerr.E(xcerr.CodeValidation, "media duration must be positive", nil)
	}

	var motion, audio, onsets, playerPresence *analysis.FeatureTrack
	wantMotion := cfg.MotionTrack
	if wantMotion == "" {
		wantMotion = "frame_diff"
	}
	for i := range tracks {
		switch tracks[i].Kind {
		case wantMotion:
			motion = &tracks[i]
		case "frame_diff":
			if motion == nil {
				motion = &tracks[i] // graceful fallback when the preferred kind is absent
			}
		case "audio_rms_db":
			audio = &tracks[i]
		case "audio_onset":
			onsets = &tracks[i]
		case "player_presence":
			playerPresence = &tracks[i]
		}
	}
	if playerPresence != nil {
		sortSamples(playerPresence.Samples)
	}
	if motion == nil || len(motion.Samples) == 0 {
		return nil, stats, xcerr.E(xcerr.CodeValidation, "frame_diff feature track missing", nil)
	}
	sortSamples(motion.Samples)
	if audio != nil {
		sortSamples(audio.Samples)
	}
	if onsets != nil {
		sortSamples(onsets.Samples)
	}

	if cfg.Mode == ModeRally {
		ssegs, serr := buildRallies(motion, audio, onsets, playerPresence, duration, cfg, &stats)
		stats.Segments = len(ssegs)
		return ssegs, stats, serr
	}
	window := estimateWindow(motion.Samples)

	cells := buildCells(motion, audio, cfg, window)
	runs := splitAtCuts(cells)

	var segments []Segment
	for _, r := range runs {
		for _, s := range mergeSpans(activeSpans(r, cfg.MotionFloor, window), cfg.MergeGap) {
			if s.end > duration {
				s.end = duration
			}
			if s.start >= duration {
				continue
			}
			if s.end-s.start < cfg.MinDuration {
				continue
			}
			seg := scoreSpan(s, cfg)
			// Transient density inside activity segments: a generic music /
			// percussive-energy signal (styles score it via hits/density
			// weights; it is never surfaced as a "chorus" or other claim we
			// cannot support).
			if onsets != nil {
				seg.HitCount = countOnsetsIn(onsets, s.start, s.end)
				if dur := seg.End - seg.Start; dur > 0 {
					seg.HitDensity = round4(float64(seg.HitCount) / dur)
				}
			}
			segments = append(segments, seg)
		}
	}
	stats.Segments = len(segments)
	return segments, stats, nil
}

// countOnsetsIn counts onset samples within [start, end].
func countOnsetsIn(onsets *analysis.FeatureTrack, start, end float64) int {
	lo := sort.Search(len(onsets.Samples), func(i int) bool { return onsets.Samples[i].T >= start })
	hi := sort.Search(len(onsets.Samples), func(i int) bool { return onsets.Samples[i].T > end })
	return hi - lo
}

// buildCells maps the motion grid to activity cells with nearest-window audio.
func buildCells(motion, audio *analysis.FeatureTrack, cfg Config, window float64) []cell {
	cells := make([]cell, 0, len(motion.Samples))
	for _, smp := range motion.Samples {
		c := cell{
			t:      smp.T,
			motion: smp.V,
			db:     silentDB,
			cut:    smp.V > cfg.CutThreshold,
		}
		if s, ok := nearestAudio(audio, smp.T, window); ok {
			if math.IsNaN(s.V) {
				s.V = silentDB
			}
			if math.IsInf(s.V, -1) {
				s.V = silentDB
			}
			c.db = s.V
			c.audible = s.V > cfg.SilenceDB
		}
		cells = append(cells, c)
	}
	return cells
}

// splitAtCuts breaks the cell grid at scene cuts; a cut cell starts new content.
func splitAtCuts(cells []cell) [][]cell {
	var runs [][]cell
	var run []cell
	for _, c := range cells {
		if c.cut && len(run) > 0 {
			runs = append(runs, run)
			run = nil
		}
		run = append(run, c)
	}
	if len(run) > 0 {
		runs = append(runs, run)
	}
	return runs
}

func isCellActive(c cell, motionFloor float64) bool {
	return c.motion > motionFloor || c.audible
}

// activeSpans collects maximal runs of active cells within one cut-run.
func activeSpans(run []cell, motionFloor, window float64) []span {
	var out []span
	var cur *span
	closeSpan := func() {
		if cur != nil {
			out = append(out, *cur)
			cur = nil
		}
	}
	for _, c := range run {
		if !isCellActive(c, motionFloor) {
			closeSpan()
			continue
		}
		if cur == nil {
			s := span{start: c.t, end: c.t + window}
			cur = &s
		} else {
			cur.end = c.t + window
		}
		cur.motionSum += c.motion
		cur.motionN++
		cur.audioSum += c.db
		cur.audioN++
	}
	closeSpan()
	return out
}

// mergeSpans merges adjacent spans separated by less than maxGap.
func mergeSpans(spans []span, maxGap float64) []span {
	if len(spans) == 0 {
		return nil
	}
	out := []span{spans[0]}
	for _, s := range spans[1:] {
		prev := &out[len(out)-1]
		if s.start-prev.end < maxGap {
			prev.end = s.end
			prev.motionSum += s.motionSum
			prev.motionN += s.motionN
			prev.audioSum += s.audioSum
			prev.audioN += s.audioN
			continue
		}
		out = append(out, s)
	}
	return out
}

// scoreSpan converts accumulated statistics into a deterministic 0..1 score.
func scoreSpan(s span, cfg Config) Segment {
	meanMotion := 0.0
	if s.motionN > 0 {
		meanMotion = s.motionSum / float64(s.motionN)
	}
	meanDB := silentDB
	if s.audioN > 0 {
		meanDB = s.audioSum / float64(s.audioN)
	}
	dur := s.end - s.start

	motionN := clamp(meanMotion/0.30, 0, 1)
	audioN := clamp((meanDB-cfg.SilenceDB)/36.0, 0, 1)
	durN := clamp(dur/15.0, 0, 1)
	sc := 0.45*motionN + 0.35*audioN + 0.20*durN
	if math.IsNaN(sc) || math.IsInf(sc, 0) {
		sc = 0
	}
	return Segment{
		Start:       round4(s.start),
		End:         round4(s.end),
		Score:       round4(sc),
		MeanMotion:  round4(meanMotion),
		MeanAudioDB: round4(meanDB),
	}
}

func sortSamples(s []analysis.Sample) {
	sort.Slice(s, func(i, j int) bool { return s[i].T < s[j].T })
}

func estimateWindow(samples []analysis.Sample) float64 {
	if len(samples) < 2 {
		return 0.5
	}
	gaps, n := 0.0, 0
	for i := 1; i < len(samples) && n < 32; i++ {
		d := samples[i].T - samples[i-1].T
		if d > 0 {
			gaps += d
			n++
		}
	}
	if n == 0 {
		return 0.5
	}
	if w := gaps / float64(n); w > 0 && !math.IsNaN(w) {
		return w
	}
	return 0.5
}

func nearestAudio(t *analysis.FeatureTrack, at, tol float64) (analysis.Sample, bool) {
	if t == nil || len(t.Samples) == 0 {
		return analysis.Sample{}, false
	}
	i := sort.Search(len(t.Samples), func(i int) bool { return t.Samples[i].T >= at-tol/2 })
	if i >= len(t.Samples) {
		i = len(t.Samples) - 1
	}
	best := t.Samples[i]
	if j := i - 1; j >= 0 && abs(t.Samples[j].T-at) < abs(best.T-at) {
		best = t.Samples[j]
	}
	if abs(best.T-at) > tol {
		return analysis.Sample{}, false
	}
	return best, true
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
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

// finiteSilenceDB gates the audio threshold: NaN/±Inf poisons both the
// audible test and the score normalization (every segment silently scored
// 0), and anything above 0 or below the −120 dBFS floor is nonsense for a
// dBFS threshold.
func finiteSilenceDB(db float64) bool {
	return !math.IsNaN(db) && !math.IsInf(db, 0) && db <= 0 && db >= -120
}

func round4(v float64) float64 {
	return math.Round(v*10000) / 10000
}
