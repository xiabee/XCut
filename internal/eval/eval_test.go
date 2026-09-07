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
