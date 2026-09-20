package pipeline

import (
	"math"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/timeline"
	"github.com/xiabee/XCut/internal/xcerr"
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
		// Literal hours, not just the constant: a ceiling that quietly drops
		// below "a long event reel" is a user-visible regression, while raising
		// it stays a free decision. Asserting the constant itself would fire on
		// either and catch neither.
		{"a three hour reel is accepted", TimelineRequest{Style: "generic_highlight", Duration: 3 * 3600}, true},
		{"a five hour reel is refused", TimelineRequest{Style: "generic_highlight", Duration: 5 * 3600}, false},
		{"sub-second rejected", TimelineRequest{Style: "generic_highlight", Duration: 0.5}, false},
		{"negative rejected", TimelineRequest{Style: "generic_highlight", Duration: -30}, false},
		{"past the ceiling rejected", TimelineRequest{Style: "generic_highlight", Duration: MaxRequestDuration + 1}, false},
		{"NaN rejected", TimelineRequest{Style: "generic_highlight", Duration: math.NaN()}, false},
		{"Inf rejected", TimelineRequest{Style: "generic_highlight", Duration: math.Inf(1)}, false},
	} {
		err := tc.req.Validate()
		if tc.ok && err != nil {
			t.Errorf("%s: got %v, want accepted", tc.name, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("%s: got nil, want rejection", tc.name)
		}
	}
}

// A reel shorter than the style's minimum clip cannot hold a single candidate;
// the run must refuse with both numbers instead of burning a full analysis pass
// and failing with the generic "rejected all events". ktv_mv's floor is 2 s,
// generic_highlight's is 1 s.
func TestDurationShorterThanMinClipRefusedWithReason(t *testing.T) {
	d, p := analyzeSetup(t, 2) // ~20 s of generated material

	_, err := d.BuildTimeline(p, TimelineRequest{Style: "ktv_mv", Duration: 1})
	if err == nil {
		t.Fatal("1s reel on a 2s-floor style: want rejection, got a timeline")
	}
	if xcerr.CodeOf(err) != xcerr.CodeValidation {
		t.Errorf("error code %v, want validation", xcerr.CodeOf(err))
	}
	for _, want := range []string{"1s", "ktv_mv", "2s"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q lost %q", err.Error(), want)
		}
	}

	// The boundary itself still works: a reel exactly at the floor fits one
	// minimum clip, and a style with a 1 s floor accepts a 1 s reel.
	tl, err := d.BuildTimeline(p, TimelineRequest{Style: "generic_highlight", Duration: 1})
	if err != nil {
		t.Fatalf("1s reel on a 1s-floor style: %v", err)
	}
	if clipCount(tl) == 0 {
		t.Error("boundary reel produced no clips")
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
