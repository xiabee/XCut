// Package eval implements XCut's evaluation harness: quantifiable,
// repeatable comparison of selection quality against human annotations.
//
// A manifest describes labeled media cases (expected highlight ranges);
// running the pipeline produces selected ranges; this package computes
// deterministic metrics — temporal IoU, precision/recall/F1, per-range
// coverage, and near-duplicate rate — so algorithm changes are measured
// old-vs-new instead of eyeballed (docs/EVAL.md).
//
// All functions are pure and media-agnostic: callers map timelines to
// []Interval, annotations are []Range.
package eval

import "math"

// Interval is a half-open [Start, End) time range in seconds on one media
// source (the selected clip's source span).
type Interval struct {
	Start float64
	End   float64
}

// Range is one annotated expected highlight interval, optionally labeled.
type Range struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Label string  `json:"label,omitempty"`
}

func (r Range) interval() Interval { return Interval{r.Start, r.End} }

func (i Interval) length() float64 { return i.End - i.Start }

func (i Interval) intersect(o Interval) (Interval, bool) {
	s, e := math.Max(i.Start, o.Start), math.Min(i.End, o.End)
	if e <= s {
		return Interval{}, false
	}
	return Interval{s, e}, true
}

// IoU is the temporal intersection-over-union of two intervals (0 when they
// do not overlap).
func IoU(a, b Interval) float64 {
	inter, ok := a.intersect(b)
	if !ok {
		return 0
	}
	union := a.length() + b.length() - inter.length()
	if union <= 0 {
		return 0
	}
	return inter.length() / union
}

// mergeIntervals returns the union of possibly overlapping intervals as a
// sorted, disjoint list (touching intervals are not merged: the measure is
// unaffected).
func mergeIntervals(ivs []Interval) []Interval {
	if len(ivs) == 0 {
		return nil
	}
	sorted := make([]Interval, len(ivs))
	copy(sorted, ivs)
	sortByStart(sorted)
	out := []Interval{sorted[0]}
	for _, iv := range sorted[1:] {
		cur := &out[len(out)-1]
		switch {
		case iv.End <= cur.End:
			// contained
		case iv.Start <= cur.End:
			cur.End = iv.End
		default:
			out = append(out, iv)
		}
	}
	return out
}

// unionLength is the total covered time of possibly overlapping intervals.
func unionLength(ivs []Interval) float64 {
	total := 0.0
	for _, iv := range mergeIntervals(ivs) {
		total += iv.length()
	}
	return total
}

func intersectionLength(a, b []Interval) float64 {
	sa := mergeIntervals(a)
	sb := mergeIntervals(b)
	total := 0.0
	i, j := 0, 0
	for i < len(sa) && j < len(sb) {
		if in, ok := sa[i].intersect(sb[j]); ok {
			total += in.length()
		}
		if sa[i].End <= sb[j].End {
			i++
		} else {
			j++
		}
	}
	return total
}

func sortByStart(ivs []Interval) {
	// Small n everywhere (clips and annotations); insertion sort keeps this
	// dependency-free and deterministic.
	for i := 1; i < len(ivs); i++ {
		for j := i; j > 0 && ivs[j].Start < ivs[j-1].Start; j-- {
			ivs[j], ivs[j-1] = ivs[j-1], ivs[j]
		}
	}
}

// CaseMetrics is the quality of one selection against one annotation set.
type CaseMetrics struct {
	// Time-based scores over the union of ranges.
	Precision float64 `json:"precision"` // selected time inside expected / selected time
	Recall    float64 `json:"recall"`    // expected time covered / expected time
	F1        float64 `json:"f1"`

	// Range-based scores: an expected range counts as hit when its best
	// selected-interval IoU reaches Threshold.
	RangesHit    int             `json:"ranges_hit"`
	RangesTotal  int             `json:"ranges_total"`
	RangeResults []RangeMatch    `json:"range_results"`
	Duplicates   []DuplicatePair `json:"duplicates,omitempty"`

	Clips           int     `json:"clips"`
	SelectedSeconds float64 `json:"selected_seconds"`
	ExpectedSeconds float64 `json:"expected_seconds"`

	// Duplicate rate: fraction of selected intervals involved in a
	// near-duplicate pair (IoU > DuplicateIoU). 0 = no redundant picks.
	DuplicateRate float64 `json:"duplicate_rate"`
}

// RangeMatch is one expected range against the best-matching selection.
type RangeMatch struct {
	Start    float64 `json:"start"`
	End      float64 `json:"end"`
	Label    string  `json:"label,omitempty"`
	BestIoU  float64 `json:"best_iou"`
	Coverage float64 `json:"coverage"` // fraction of the range covered by any selection
	Hit      bool    `json:"hit"`
}

// DuplicatePair records two selected intervals that overlap heavily.
type DuplicatePair struct {
	A   int     `json:"a"`
	B   int     `json:"b"`
	IoU float64 `json:"iou"`
}

// Config tunes metric thresholds.
type Config struct {
	// HitIoU is the IoU a selected interval needs for the expected range to
	// count as hit. 0.3 ≈ "the highlight is mostly the same moment".
	HitIoU float64
	// DuplicateIoU marks a near-duplicate pair above this overlap.
	DuplicateIoU float64
}

// DefaultMetricsConfig is the documented baseline (docs/EVAL.md).
func DefaultMetricsConfig() Config { return Config{HitIoU: 0.3, DuplicateIoU: 0.5} }

func (c Config) validate() Config {
	out := c
	if !(out.HitIoU > 0 && out.HitIoU <= 1) || math.IsNaN(out.HitIoU) {
		out.HitIoU = 0.3
	}
	if !(out.DuplicateIoU > 0 && out.DuplicateIoU <= 1) || math.IsNaN(out.DuplicateIoU) {
		out.DuplicateIoU = 0.5
	}
	return out
}

// Score computes metrics for one case. selected are the pipeline's clip
// source ranges; expected are the annotations. Both may be empty/unsorted;
// neither is modified.
func Score(selected []Interval, expected []Range, cfg Config) CaseMetrics {
	cfg = cfg.validate()
	m := CaseMetrics{RangesTotal: len(expected)}

	selSecs := unionLength(selected)
	expIntervals := make([]Interval, len(expected))
	for i, r := range expected {
		expIntervals[i] = r.interval()
	}
	expSecs := unionLength(expIntervals)
	m.Clips = len(selected)
	m.SelectedSeconds = round4(selSecs)
	m.ExpectedSeconds = round4(expSecs)
	if selSecs > 0 && expSecs > 0 {
		inter := intersectionLength(selected, expIntervals)
		m.Precision = round4(inter / selSecs)
		m.Recall = round4(inter / expSecs)
		if m.Precision+m.Recall > 0 {
			m.F1 = round4(2 * m.Precision * m.Recall / (m.Precision + m.Recall))
		}
	}

	for _, e := range expected {
		ei := e.interval()
		rm := RangeMatch{Start: e.Start, End: e.End, Label: e.Label}
		for _, s := range selected {
			if v := IoU(ei, s); v > rm.BestIoU {
				rm.BestIoU = v
			}
			if in, ok := ei.intersect(s); ok {
				rm.Coverage += in.length()
			}
		}
		if e.End > e.Start {
			rm.Coverage = round4(rm.Coverage / (e.End - e.Start))
		}
		rm.Hit = rm.BestIoU >= cfg.HitIoU
		if rm.Hit {
			m.RangesHit++
		}
		m.RangeResults = append(m.RangeResults, rm)
	}

	flagged := map[int]bool{}
	for i := 0; i < len(selected); i++ {
		for j := i + 1; j < len(selected); j++ {
			if v := IoU(selected[i], selected[j]); v > cfg.DuplicateIoU {
				m.Duplicates = append(m.Duplicates, DuplicatePair{A: i, B: j, IoU: round4(v)})
				flagged[i], flagged[j] = true, true
			}
		}
	}
	if len(selected) > 0 {
		m.DuplicateRate = round4(float64(len(flagged)) / float64(len(selected)))
	}
	return m
}

func round4(v float64) float64 {
	return math.Round(v*10000) / 10000
}

// IntervalJSON is the JSON form of Interval (results documents, CLI output).
type IntervalJSON struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// Macro aggregates case metrics into a run-level summary (unweighted mean
// over scored cases; range totals summed).
type Macro struct {
	Precision     float64 `json:"precision"`
	Recall        float64 `json:"recall"`
	F1            float64 `json:"f1"`
	DuplicateRate float64 `json:"duplicate_rate"`
	RangesHit     int     `json:"ranges_hit"`
	RangesTotal   int     `json:"ranges_total"`
	Cases         int     `json:"cases"`
	CasesFailed   int     `json:"cases_failed"`
}

// MacroScore averages the given case metrics (nil entries are counted as
// failed cases); rangesTotal is the manifest-wide annotated range count.
func MacroScore(ms []*CaseMetrics, rangesTotal int) Macro {
	var m Macro
	m.RangesTotal = rangesTotal
	var n int
	for _, cm := range ms {
		m.Cases++
		if cm == nil {
			m.CasesFailed++
			continue
		}
		n++
		m.Precision += cm.Precision
		m.Recall += cm.Recall
		m.F1 += cm.F1
		m.DuplicateRate += cm.DuplicateRate
		m.RangesHit += cm.RangesHit
	}
	if n > 0 {
		m.Precision = round4(m.Precision / float64(n))
		m.Recall = round4(m.Recall / float64(n))
		m.F1 = round4(m.F1 / float64(n))
		m.DuplicateRate = round4(m.DuplicateRate / float64(n))
	}
	return m
}
