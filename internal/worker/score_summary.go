package worker

import (
	"fmt"
	"math"
	"sort"
)

// ScoreMarkSummary is what a boundary set looks like from the outside: how many,
// how they are spaced, and where the first and last sit. It exists because a
// scan that returns a number and nothing else cannot be checked by the person
// who chose the region — they have to be able to see whether the spacing looks
// like finished points before rendering a reel around it.
type ScoreMarkSummary struct {
	Count    int
	First    float64
	Last     float64
	Median   float64
	Tightest float64 // smallest gap between two consecutive marks
}

// SummarizeScoreMarks ignores non-finite entries (a tool that invents an Inf
// boundary is broken upstream, and the summary must not inherit that into a
// median of NaN).
func SummarizeScoreMarks(times []float64) ScoreMarkSummary {
	ts := make([]float64, 0, len(times))
	for _, t := range times {
		if math.IsNaN(t) || math.IsInf(t, 0) {
			continue
		}
		ts = append(ts, t)
	}
	sort.Float64s(ts)
	s := ScoreMarkSummary{Count: len(ts)}
	if len(ts) == 0 {
		return s
	}
	s.First, s.Last = ts[0], ts[len(ts)-1]
	if len(ts) == 1 {
		return s
	}
	gaps := make([]float64, 0, len(ts)-1)
	s.Tightest = math.Inf(1)
	for i := 1; i < len(ts); i++ {
		g := ts[i] - ts[i-1]
		gaps = append(gaps, g)
		if g < s.Tightest {
			s.Tightest = g
		}
	}
	sort.Float64s(gaps)
	n := len(gaps)
	if n%2 == 1 {
		s.Median = gaps[n/2]
	} else {
		s.Median = (gaps[n/2-1] + gaps[n/2]) / 2
	}
	return s
}

// String is the one-line form both the CLI and the analyze log show. A single
// mark has no spacing to report, so the row says just that rather than
// printing a median of 0 that reads like a measurement.
func (s ScoreMarkSummary) String() string {
	switch {
	case s.Count == 0:
		return "no changes in that region"
	case s.Count == 1:
		return "1 boundary at " + sec(s.First)
	default:
		return fmt.Sprintf("%d boundaries, %s..%s, median gap %s, tightest %s",
			s.Count, sec(s.First), sec(s.Last), sec(s.Median), sec(s.Tightest))
	}
}

// sec keeps one decimal: a gap reported as "10.4s" is checkable against a
// scoreboard, "10s" is not.
func sec(v float64) string {
	return fmt.Sprintf("%.1fs", v)
}
