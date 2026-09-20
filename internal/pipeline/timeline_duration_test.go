package pipeline

import (
	"math"
	"testing"

	"github.com/xiabee/XCut/internal/timeline"
)

// The reel-length override is bounded by the pipeline, not by the caller that
// happens to be in front of it (CLI, web UI, and any future client all funnel
// through here).
func TestTimelineDurationValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  TimelineRequest
		ok   bool
	}{
		{"preset default passes", Style("generic_highlight"), true},
		{"zero means the style's own", TimelineRequest{Style: "generic_highlight", Duration: 0}, true},
		{"one second is the floor", TimelineRequest{Style: "generic_highlight", Duration: 1}, true},
		{"four hours is the ceiling", TimelineRequest{Style: "generic_highlight", Duration: MaxRequestDuration}, true},
		{"sub-second rejected", TimelineRequest{Style: "generic_highlight", Duration: 0.5}, false},
		{"negative rejected", TimelineRequest{Style: "generic_highlight", Duration: -30}, false},
		{"past the ceiling rejected", TimelineRequest{Style: "generic_highlight", Duration: MaxRequestDuration + 1}, false},
		{"NaN rejected", TimelineRequest{Style: "generic_highlight", Duration: math.NaN()}, false},
		{"Inf rejected", TimelineRequest{Style: "generic_highlight", Duration: math.Inf(1)}, false},
	} {
		err := tc.req.validate()
		if tc.ok && err != nil {
			t.Errorf("%s: got %v, want accepted", tc.name, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("%s: got nil, want rejection", tc.name)
		}
	}
}

// The bound has to be reachable from the CLI's own message, otherwise the two
// copies drift and the CLI promises a range the pipeline refuses.
func TestMaxRequestDurationIsOneHourMultiples(t *testing.T) {
	if MaxRequestDuration != 4*3600 {
		t.Fatalf("MaxRequestDuration = %d; the docs and USAGE quote 4 hours", MaxRequestDuration)
	}
}

func TestDurationOverrideChangesTheReel(t *testing.T) {
	d, p := analyzeSetup(t, 3) // ~30 s of generated material

	short, err := d.BuildTimeline(p, TimelineRequest{Style: "generic_highlight", Duration: 6})
	if err != nil {
		t.Fatal(err)
	}
	long, err := d.BuildTimeline(p, TimelineRequest{Style: "generic_highlight", Duration: 25})
	if err != nil {
		t.Fatal(err)
	}
	if cl, ct := clipCount(short), short.Duration(); cl == 0 || ct > 8 {
		t.Fatalf("6s target produced %d clips / %.1fs; want a short reel", cl, ct)
	}
	if cl, ct := clipCount(long), long.Duration(); ct <= short.Duration() {
		t.Fatalf("25s target gave %.1fs vs 6s target %.1fs: the override did not reach the style engine",
			ct, short.Duration())
	} else if cl < clipCount(short) {
		t.Fatalf("longer target yielded fewer clips (%d < %d) with the same budget of material",
			cl, clipCount(short))
	}
}

func clipCount(tl *timeline.Timeline) int {
	n := 0
	for _, tr := range tl.Tracks {
		n += len(tr.Clips)
	}
	return n
}
