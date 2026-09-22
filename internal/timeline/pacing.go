package timeline

import (
	"math"
	"sort"
	"strconv"
)

// Pacing is what a finished reel does with time, read off the document rather
// than off the style that asked for it. Selection metrics (precision, recall,
// range hits) are set-based: they score identically whether a reel's five picks
// land as one 15 s stretch or five 3 s cuts. Shape is a separate fact, and short
// forms live or die by it — so it gets measured instead of eyeballed.
//
// The shot set is every clip on every track, the same set `xcut timeline` counts
// in its summary line; a reel whose "clips" and "shots" disagree would be a
// readout nobody could reconcile.
type Pacing struct {
	Shots           int     `json:"shots"`
	MeanSeconds     float64 `json:"mean_seconds"`
	MedianSeconds   float64 `json:"median_seconds"`
	LongestSeconds  float64 `json:"longest_seconds"`
	ShortestSeconds float64 `json:"shortest_seconds"`

	// ScoredShots counts the clips carrying the style engine's "score". A
	// hand-edited document has none, and then the hook below is not zero — it is
	// absent, which is a different answer.
	ScoredShots int `json:"scored_shots"`
	// HookSeconds is where the top-scored shot *begins on the output timeline*.
	// Source position answers "when was it filmed", which is not the question a
	// hook is. Equal scores resolve to the earlier output position: the viewer
	// meets that one first whatever the document's clip order says.
	HookSeconds float64 `json:"hook_seconds"`
	HookScore   float64 `json:"hook_score"`
}

// Pacing measures this document. A nil or empty timeline reports the zero value:
// no shots, and therefore no lengths and no hook.
func (t *Timeline) Pacing() Pacing {
	if t == nil {
		return Pacing{}
	}
	var p Pacing
	var (
		lengths  []float64
		total    float64
		haveHook bool
	)
	for _, tr := range t.Tracks {
		for _, c := range tr.Clips {
			d := c.Duration()
			lengths = append(lengths, d)
			total += d
			if d > p.LongestSeconds {
				p.LongestSeconds = d
			}
			if p.Shots == 0 || d < p.ShortestSeconds {
				p.ShortestSeconds = d
			}
			p.Shots++
			// Only a score that parses is a score: metadata is strings, so a
			// hand-edited or half-written document can hold anything here, and
			// reading it as 0 would let a real 0.4 lose to a typo.
			v, err := strconv.ParseFloat(c.Metadata["score"], 64)
			if err != nil {
				continue
			}
			p.ScoredShots++
			if !haveHook || v > p.HookScore || (v == p.HookScore && c.TimelineStart < p.HookSeconds) {
				p.HookScore, p.HookSeconds, haveHook = v, c.TimelineStart, true
			}
		}
	}
	if p.Shots == 0 {
		return p
	}
	p.MeanSeconds = total / float64(p.Shots)
	sort.Float64s(lengths)
	mid := p.Shots / 2
	if p.Shots%2 == 1 {
		p.MedianSeconds = lengths[mid]
	} else {
		p.MedianSeconds = (lengths[mid-1] + lengths[mid]) / 2
	}
	// A millisecond is all a readout can claim. These numbers come out of sums over
	// float durations, and shipping 7.50000000000001 in an API response asks the
	// reader to wonder what the extra digits mean. They do not.
	p.MeanSeconds = roundMillis(p.MeanSeconds)
	p.MedianSeconds = roundMillis(p.MedianSeconds)
	p.LongestSeconds = roundMillis(p.LongestSeconds)
	p.ShortestSeconds = roundMillis(p.ShortestSeconds)
	p.HookSeconds = roundMillis(p.HookSeconds)
	return p
}

func roundMillis(v float64) float64 {
	return math.Round(v*1000) / 1000
}
