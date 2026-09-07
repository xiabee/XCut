package event

import (
	"math"
	"sort"

	"github.com/xiabee/XCut/internal/analysis"
)

// Rally segmentation: group audio transients (hit-like onsets) into
// candidate rallies — the generic shape of any hit-driven sport rally
// (badminton, tennis, volleyball): dense transients + visual motion, with
// quiet gaps between. It is a data-selected mode of the event builder
// (Config.Mode == ModeRally), never a style-specific code path.
//
// Pipeline: hits (onset samples) → cluster by gap → pad with lead/tail →
// filter by hit count and motion support → scored, explainable segments.
const (
	ModeActivity = "activity"
	ModeRally    = "rally"
)

// Rally defaults applied when Config selects rally mode without parameters
// (presets may set them explicitly; zero values never disable a rally run).
const (
	defaultRallyGap  = 2.5  // s of quiet between hits that splits rallies
	defaultRallyPad  = 1.2  // s padded before/after the first/last hit
	defaultMinHits   = 4    // transients required inside one rally
	defaultMaxRally  = 30.0 // s — longer clusters are degenerate
	rallySplitMotion = 0.28 // motion spike that hard-splits a rally (scene cut)
)

// buildRallies clusters onsets into rally segments.
func buildRallies(motion, audio, onsets *analysis.FeatureTrack, duration float64, cfg Config) ([]Segment, error) {
	hits := onsetSamples(onsets)
	if len(hits) == 0 {
		return nil, nil
	}

	gap := firstNonZero(cfg.RallyGap, defaultRallyGap)
	pad := firstNonZero(cfg.RallyPad, defaultRallyPad)
	minHits := cfg.MinHits
	if minHits <= 0 {
		minHits = defaultMinHits
	}

	// Cluster: consecutive hits closer than gap belong to the same rally.
	type cluster struct {
		hits []analysis.Sample
	}
	var clusters []cluster
	cur := cluster{hits: []analysis.Sample{hits[0]}}
	flush := func() {
		if len(cur.hits) >= minHits {
			clusters = append(clusters, cur)
		}
	}
	for _, h := range hits[1:] {
		if h.T-cur.hits[len(cur.hits)-1].T > gap {
			flush()
			cur = cluster{hits: []analysis.Sample{h}}
			continue
		}
		cur.hits = append(cur.hits, h)
	}
	flush()

	var segments []Segment
	for _, c := range clusters {
		start := c.hits[0].T - pad
		end := c.hits[len(c.hits)-1].T + pad
		if start < 0 {
			start = 0
		}
		if end > duration {
			end = duration
		}
		if end-start > defaultMaxRally {
			end = start + defaultMaxRally
		}
		if end-start < cfg.MinDuration {
			continue
		}
		// Motion support: a rally with no on-screen motion is not watchable.
		meanMotion := intervalMean(motion, start, end)
		if meanMotion < cfg.MotionFloor {
			continue
		}
		if hasCutWithin(motion, start, end) {
			// A scene cut inside the padded window suggests mixed content;
			// keep it only if hits dominate (>= min hits after the cut).
			continue
		}
		if math.IsNaN(meanMotion) || math.IsInf(meanMotion, 0) {
			continue
		}
		meanDB := silentDB
		if audio != nil {
			meanDB = intervalMeanDB(audio, start, end)
		}
		count := len(c.hits)
		dur := end - start
		density := float64(count) / dur
		hitsN := clamp(float64(count)/12.0, 0, 1)
		densN := clamp(density/1.5, 0, 1)
		motionN := clamp(meanMotion/0.30, 0, 1)
		durN := clamp(dur/12.0, 0, 1)
		sc := 0.40*hitsN + 0.30*densN + 0.20*motionN + 0.10*durN
		if math.IsNaN(sc) || math.IsInf(sc, 0) {
			sc = 0
		}
		segments = append(segments, Segment{
			Start:       round4(start),
			End:         round4(end),
			Score:       round4(sc),
			MeanMotion:  round4(meanMotion),
			MeanAudioDB: round4(meanDB),
			Kind:        ModeRally,
			HitCount:    count,
			HitDensity:  round4(density),
		})
	}
	sort.Slice(segments, func(i, j int) bool { return segments[i].Start < segments[j].Start })
	return segments, nil
}

// onsetSamples extracts sorted, deduplicated onset samples.
func onsetSamples(onsets *analysis.FeatureTrack) []analysis.Sample {
	if onsets == nil || len(onsets.Samples) == 0 {
		return nil
	}
	out := make([]analysis.Sample, len(onsets.Samples))
	copy(out, onsets.Samples)
	sortSamples(out)
	// Collapse duplicates at the same time (should not happen; cheap guard).
	dedup := out[:0]
	for i, s := range out {
		if i == 0 || s.T != out[i-1].T {
			dedup = append(dedup, s)
		}
	}
	return dedup
}

// intervalMean is the mean frame_diff value within [start, end].
func intervalMean(t *analysis.FeatureTrack, start, end float64) float64 {
	if t == nil || len(t.Samples) == 0 {
		return 0
	}
	sum, n := 0.0, 0
	for _, s := range t.Samples {
		if s.T >= start && s.T <= end {
			sum += s.V
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

// intervalMeanDB is the mean RMS dBFS within [start, end] (silentDB when the
// track has no samples there).
func intervalMeanDB(t *analysis.FeatureTrack, start, end float64) float64 {
	if t == nil || len(t.Samples) == 0 {
		return silentDB
	}
	sum, n := 0.0, 0
	for _, s := range t.Samples {
		if s.T >= start && s.T <= end {
			sum += s.V
			n++
		}
	}
	if n == 0 {
		return silentDB
	}
	return sum / float64(n)
}

// hasCutWithin reports whether the motion track spikes above the cut
// threshold inside [start, end].
func hasCutWithin(t *analysis.FeatureTrack, start, end float64) bool {
	if t == nil {
		return false
	}
	for _, s := range t.Samples {
		if s.T >= start && s.T <= end && s.V > rallySplitMotion {
			return true
		}
	}
	return false
}

func firstNonZero(v, def float64) float64 {
	if v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0) {
		return v
	}
	return def
}
