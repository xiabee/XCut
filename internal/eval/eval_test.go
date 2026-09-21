package eval

import (
	"encoding/json"
	"math"
	"testing"
)

func fsec(a, b float64) Interval { return Interval{Start: a, End: b} }

func TestIoU(t *testing.T) {
	cases := []struct {
		name string
		a, b Interval
		want float64
	}{
		{"disjoint", fsec(0, 1), fsec(2, 3), 0},
		{"touching", fsec(0, 1), fsec(1, 2), 0},
		{"identical", fsec(1, 3), fsec(1, 3), 1},
		{"contained", fsec(0, 10), fsec(2, 4), 0.2},
		{"half overlap", fsec(0, 2), fsec(1, 3), 1.0 / 3.0},
		{"zero length", fsec(1, 1), fsec(1, 2), 0},
	}
	for _, tc := range cases {
		if got := IoU(tc.a, tc.b); math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("%s: IoU=%g want %g", tc.name, got, tc.want)
		}
	}
}

func TestUnionAndIntersection(t *testing.T) {
	sel := []Interval{fsec(0, 2), fsec(1, 3), fsec(5, 6)}
	if got := unionLength(sel); got != 4 {
		t.Errorf("union=%g want 4", got)
	}
	exp := []Interval{fsec(1, 2), fsec(5.5, 7)}
	// [1,2] ⊂ sel union → 1s; [5.5,7] ∩ [5,6] = 0.5s.
	if got := intersectionLength(sel, exp); math.Abs(got-1.5) > 1e-9 {
		t.Errorf("intersection=%g want 1.5", got)
	}
	if got := unionLength(nil); got != 0 {
		t.Errorf("empty union=%g want 0", got)
	}
}

func TestScorePerfectSelection(t *testing.T) {
	expected := []Range{{Start: 10, End: 20, Label: "rally"}, {Start: 30, End: 40}}
	selected := []Interval{fsec(10, 20), fsec(30, 40)}
	m := Score(selected, expected, DefaultMetricsConfig())
	if m.Precision != 1 || m.Recall != 1 || m.F1 != 1 {
		t.Errorf("perfect selection: P=%g R=%g F1=%g want 1", m.Precision, m.Recall, m.F1)
	}
	if m.RangesHit != 2 || m.RangesTotal != 2 {
		t.Errorf("ranges %d/%d want 2/2", m.RangesHit, m.RangesTotal)
	}
	if m.DuplicateRate != 0 {
		t.Errorf("duplicate rate %g want 0", m.DuplicateRate)
	}
}

func TestScoreEmptySelection(t *testing.T) {
	expected := []Range{{Start: 10, End: 20}}
	m := Score(nil, expected, DefaultMetricsConfig())
	if m.Recall != 0 || m.Precision != 0 || m.RangesHit != 0 {
		t.Errorf("empty selection should score 0: %+v", m)
	}
	if m.ExpectedSeconds != 10 {
		t.Errorf("expected seconds %g want 10", m.ExpectedSeconds)
	}
}

// TestScoreLongestSkipSeparatesWhatPRTakesAsEqual: the point of the metric is
// that two reels can agree on every existing number and differ on the one that
// says whether the match was actually watched end to end. Both selections below
// score P 1, R 1/3, F1 0.5 and hit two of three ranges — by hand, over expected
// [0,10] [30,40] [60,70]:
//
//	end to end: [0,5] + [60,65]  → steps over 30..60, i.e. 55 s
//	first half: [0,5] + [30,35]  → steps over 35..70, i.e. 35 s
func TestScoreLongestSkipSeparatesWhatPRTakesAsEqual(t *testing.T) {
	expected := []Range{{Start: 0, End: 10}, {Start: 30, End: 40}, {Start: 60, End: 70}}
	spread := Score([]Interval{fsec(0, 5), fsec(60, 65)}, expected, DefaultMetricsConfig())
	clumped := Score([]Interval{fsec(0, 5), fsec(30, 35)}, expected, DefaultMetricsConfig())

	for _, pair := range []struct{ a, b CaseMetrics }{{spread, clumped}, {clumped, spread}} {
		if pair.a.Precision != pair.b.Precision || pair.a.Recall != pair.b.Recall ||
			pair.a.F1 != pair.b.F1 || pair.a.RangesHit != pair.b.RangesHit {
			t.Fatalf("the two selections were supposed to tie on the existing metrics: %+v vs %+v", pair.a, pair.b)
		}
	}
	if spread.LongestSkip != 55 {
		t.Errorf("end-to-end reel: longest skip %g, want 55 (the 30..60 gap to the last range's start)", spread.LongestSkip)
	}
	if clumped.LongestSkip != 35 {
		t.Errorf("first-half reel: longest skip %g, want 35 (nothing picked after 35)", clumped.LongestSkip)
	}
}

// TestScoreLongestSkipMergesBeforeMeasuring: the gap has to be computed over the
// union of picks. [0,60]+[10,70] covers 0..70, so a 0..100 span leaves 30 s over
// — and [0,100]+[10,20] covers the whole span, so it leaves nothing, where
// subtracting per-pick ends would invent an 80 s hole out of a contained clip.
// The two orders of the first pair are the same case seen from the other side.
func TestScoreLongestSkipMergesBeforeMeasuring(t *testing.T) {
	expected := []Range{{Start: 0, End: 100}}
	cases := []struct {
		name  string
		picks []Interval
		want  float64
	}{
		{"overlapping pair", []Interval{fsec(0, 60), fsec(10, 70)}, 30},
		{"same pair, reversed order", []Interval{fsec(10, 70), fsec(0, 60)}, 30},
		{"contained clip", []Interval{fsec(0, 100), fsec(10, 20)}, 0},
	}
	for _, c := range cases {
		if got := Score(c.picks, expected, DefaultMetricsConfig()).LongestSkip; got != c.want {
			t.Errorf("%s: longest skip %g, want %g", c.name, got, c.want)
		}
	}
	if s := Score(nil, expected, DefaultMetricsConfig()); s.LongestSkip != 100 {
		t.Errorf("no picks: longest skip %g, want the whole 100 s span", s.LongestSkip)
	}
}

func TestScoreDuplicates(t *testing.T) {
	// Two heavily overlapping picks of the same moment.
	selected := []Interval{fsec(10, 18), fsec(11, 19), fsec(30, 32)}
	expected := []Range{{Start: 10, End: 20}}
	m := Score(selected, expected, DefaultMetricsConfig())
	if len(m.Duplicates) != 1 {
		t.Fatalf("duplicates=%d want 1", len(m.Duplicates))
	}
	if math.Abs(m.DuplicateRate-2.0/3.0) > 1e-4 {
		t.Errorf("duplicate rate %g want 0.6667", m.DuplicateRate)
	}
	// Precision: selected union = [10,19] ∪ [30,32] = 11s, of which 9s
	// (inside the 10s range) is annotated → 9/11.
	if math.Abs(m.Precision-9.0/11.0) > 1e-4 {
		t.Errorf("precision %g want ~0.818", m.Precision)
	}
}

func TestScorePartialMatch(t *testing.T) {
	// Selection clips mostly outside the range plus a small overlap.
	// selected union = [15,17] ∪ [30,32] = 4s; annotated intersection = 2s.
	selected := []Interval{fsec(30, 32), fsec(15, 17)}
	expected := []Range{{Start: 10, End: 20}}
	m := Score(selected, expected, DefaultMetricsConfig())
	if m.Precision != 0.5 {
		t.Errorf("precision %g want 0.5", m.Precision)
	}
	if m.Recall != 0.2 {
		t.Errorf("recall %g want 0.2", m.Recall)
	}
	if math.Abs(m.F1-2*0.5*0.2/0.7) > 1e-4 {
		t.Errorf("f1 %g want 0.286", m.F1)
	}
	// Best IoU = 2/10 = 0.2 < 0.3 → not a hit (clip contained in the range
	// gives IoU = inter/union = inter/range-length).
	if m.RangeResults[0].Hit {
		t.Errorf("IoU %g < 0.3 must not hit", m.RangeResults[0].BestIoU)
	}
}

func TestScoreDeterministic(t *testing.T) {
	selected := []Interval{fsec(3, 9), fsec(22, 31), fsec(40, 52), fsec(60, 61)}
	expected := []Range{{Start: 2, End: 10}, {Start: 20, End: 30}, {Start: 41, End: 51}}
	a := Score(selected, expected, DefaultMetricsConfig())
	aj, _ := json.Marshal(a)
	for i := 0; i < 20; i++ {
		b := Score(selected, expected, DefaultMetricsConfig())
		bj, _ := json.Marshal(b)
		if string(aj) != string(bj) {
			t.Fatalf("score not deterministic: %s vs %s", aj, bj)
		}
	}
}

func TestMetricsConfigFallsBackOnBadValues(t *testing.T) {
	cfg := Config{HitIoU: 0, DuplicateIoU: -1}.validate()
	if cfg.HitIoU != 0.3 || cfg.DuplicateIoU != 0.5 {
		t.Errorf("bad config not defaulted: %+v", cfg)
	}
	cfg = Config{HitIoU: math.NaN(), DuplicateIoU: 2}.validate()
	if cfg.HitIoU != 0.3 || cfg.DuplicateIoU != 0.5 {
		t.Errorf("nan/overflow config not defaulted: %+v", cfg)
	}
}

func TestParseManifest(t *testing.T) {
	m, err := ParseManifest([]byte(`{
		"version": 1,
		"cases": [
			{"name": "c1", "media": "a.mp4", "style": "badminton_highlight",
			 "expected": [{"start": 1, "end": 2, "label": "rally"}]}
		]
	}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(m.Cases) != 1 || m.Cases[0].Expected[0].Label != "rally" {
		t.Fatalf("unexpected manifest: %+v", m)
	}
	b, _ := json.Marshal(m)
	m2, err := ParseManifest(b)
	if err != nil || m2.Cases[0].Name != "c1" {
		t.Fatalf("roundtrip failed: %v %+v", err, m2)
	}
}

func TestParseManifestRejects(t *testing.T) {
	bad := map[string]string{
		"unknown field": `{"version":1,"cases":[{"name":"c","media":"m","foo":1}]}`,
		"bad version":   `{"version":2,"cases":[{"name":"c","media":"m"}]}`,
		"no cases":      `{"version":1,"cases":[]}`,
		"empty name":    `{"version":1,"cases":[{"name":" ","media":"m"}]}`,
		"empty media":   `{"version":1,"cases":[{"name":"c","media":""}]}`,
		"dup name":      `{"version":1,"cases":[{"name":"c","media":"a"},{"name":"c","media":"b"}]}`,
		"bad range":     `{"version":1,"cases":[{"name":"c","media":"m","expected":[{"start":5,"end":5}]}]}`,
		"neg range":     `{"version":1,"cases":[{"name":"c","media":"m","expected":[{"start":-1,"end":3}]}]}`,
	}
	for name, in := range bad {
		if _, err := ParseManifest([]byte(in)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
