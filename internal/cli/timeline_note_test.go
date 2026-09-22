package cli

import (
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/timeline"
)

// TestFootageLimitNote: the note fires only when the footage ran out while the
// budget had room. Every other combination must stay silent — a reel that is
// short because the selector stopped early is not a footage fact, and a note
// printed for a full reel is noise that trains people to ignore the true ones.
func TestFootageLimitNote(t *testing.T) {
	reel := func(seconds float64, meta map[string]string) *timeline.Timeline {
		return &timeline.Timeline{
			Tracks: []timeline.Track{{Clips: []timeline.Clip{
				{SourceStart: 0, SourceEnd: seconds, TimelineStart: 0, Speed: 1},
			}}},
			Metadata: meta,
		}
	}
	exhausted := map[string]string{"candidate_limit": "true", "candidate_events": "18"}
	budget := map[string]string{"candidate_limit": "false", "candidate_events": "400"}

	for _, tc := range []struct {
		name  string
		tl    *timeline.Timeline
		asked float64
		want  bool
	}{
		{"footage ran out, 174s short", reel(126.4, exhausted), 300, true},
		{"footage ran out, asked exactly", reel(126.4, exhausted), 126.4, false},
		{"footage ran out, within a second", reel(126.4, exhausted), 127.0, false},
		{"budget stopped it, still short", reel(126.4, budget), 300, false},
		{"no request (style default)", reel(126.4, exhausted), 0, false},
		{"no metadata at all", reel(126.4, nil), 300, false},
	} {
		got := footageLimitNote(tc.tl, tc.asked)
		if tc.want && got == "" {
			t.Errorf("%s: wanted a note, got none", tc.name)
		}
		if !tc.want && got != "" {
			t.Errorf("%s: wanted silence, got %q", tc.name, got)
		}
		if tc.want {
			for _, needle := range []string{"offered 18 candidate rallies", "the cut took 1 of them", "126.4s", "300s"} {
				if !strings.Contains(got, needle) {
					t.Errorf("%s: note %q lacks %q", tc.name, got, needle)
				}
			}
		}
	}
}

// TestFootageLimitNoteIsSingularForOneRally: the walk over a real match with the
// vertical style produced exactly one candidate, and the note read "offered 1
// candidate rallies" at the user. Singular is not vanity — with one candidate the
// second half of the plural sentence ("worked through every candidate") says
// something different from the truth, which is that there was nothing else to cut.
func TestFootageLimitNoteIsSingularForOneRally(t *testing.T) {
	tl := &timeline.Timeline{
		Tracks: []timeline.Track{{Clips: []timeline.Clip{
			{SourceStart: 0, SourceEnd: 2.8, TimelineStart: 0, Speed: 1},
		}}},
		Metadata: map[string]string{"candidate_limit": "true", "candidate_events": "1"},
	}
	got := footageLimitNote(tl, 30)
	for _, needle := range []string{"one candidate rally", "the cut took it", "2.8s of the 30s", "nothing else to cut"} {
		if !strings.Contains(got, needle) {
			t.Errorf("the note for a single candidate lacks %q:\n%s", needle, got)
		}
	}
	if strings.Contains(got, "1 candidate rallies") || strings.Contains(got, "took 1 of them") {
		t.Errorf("the singular case still reads as the plural one:\n%s", got)
	}
}
