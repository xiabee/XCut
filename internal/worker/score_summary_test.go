package worker

import (
	"math"
	"strings"
	"testing"
)

func TestSummarizeScoreMarks(t *testing.T) {
	for _, tc := range []struct {
		name          string
		times         []float64
		count         int
		median, tight float64
		first, last   float64
	}{
		{"empty", nil, 0, 0, 0, 0, 0},
		{"one mark has no spacing", []float64{4}, 1, 0, 0, 4, 4},
		{"two marks", []float64{4, 12}, 2, 8, 8, 4, 12},
		// Median of 8.9, 12.1, 5.0 is the middle one, not the mean.
		{"odd gaps take the middle", []float64{0, 8.9, 17.8, 22.8}, 4, 8.9, 5, 0, 22.8},
		{"even gaps average the middle two", []float64{0, 5, 15}, 3, 7.5, 5, 0, 15},
		{"input order is irrelevant", []float64{22.8, 0, 17.8, 8.9}, 4, 8.9, 5, 0, 22.8},
		{"non-finite entries are dropped", []float64{4, math.NaN(), 12, math.Inf(1)}, 2, 8, 8, 4, 12},
	} {
		got := SummarizeScoreMarks(tc.times)
		if got.Count != tc.count || got.First != tc.first || got.Last != tc.last ||
			!close(got.Median, tc.median) || !close(got.Tightest, tc.tight) {
			t.Errorf("%s: %+v, want count=%d first=%g last=%g median=%g tightest=%g",
				tc.name, got, tc.count, tc.first, tc.last, tc.median, tc.tight)
		}
	}
}

// TestSummarizeScoreMarksDetectsAFlickeringCrop is the reason the summary
// exists: a crop aimed at something that changes for a non-score reason is the
// failure a bare count cannot show. Real points are tens of seconds apart;
// a clock or a pulsing logo is not.
func TestSummarizeScoreMarksDetectsAFlickeringCrop(t *testing.T) {
	scoreboard := []float64{17.2, 28.2, 42.2, 51.2, 66.7, 80.1}
	flicker := []float64{4, 5, 6, 7, 8, 9}

	sb := SummarizeScoreMarks(scoreboard)
	fl := SummarizeScoreMarks(flicker)
	if sb.Count != fl.Count {
		t.Fatalf("the two sets must be distinguishable without counting: %d vs %d", sb.Count, fl.Count)
	}
	if sb.Median < 10 || fl.Median > 1.5 {
		t.Fatalf("median gaps do not separate them: scoreboard %g, flicker %g", sb.Median, fl.Median)
	}
	if !strings.Contains(fl.String(), "median gap 1.0s") || !strings.Contains(fl.String(), "tightest 1.0s") {
		t.Errorf("flicker summary unreadable: %q", fl.String())
	}
}

func TestScoreMarkSummaryStrings(t *testing.T) {
	if got := SummarizeScoreMarks(nil).String(); got != "no changes in that region" {
		t.Errorf("empty summary = %q", got)
	}
	one := SummarizeScoreMarks([]float64{4}).String()
	if !strings.Contains(one, "1 boundary at 4.0s") || strings.Contains(one, "median") {
		t.Errorf("single-mark summary = %q, want no spacing claim", one)
	}
	two := SummarizeScoreMarks([]float64{4, 12}).String()
	if !strings.Contains(two, "2 boundaries") || !strings.Contains(two, "median gap 8.0s") {
		t.Errorf("two-mark summary = %q", two)
	}
}

func close(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
