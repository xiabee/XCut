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
	for _, needle := range []string{"one candidate rally", "the cut took it", "2.8s of the 30s", "nothing else to cut", "A different --style reads the same footage for different events"} {
		if !strings.Contains(got, needle) {
			t.Errorf("the note for a single candidate lacks %q:\n%s", needle, got)
		}
	}
	if strings.Contains(got, "1 candidate rallies") || strings.Contains(got, "took 1 of them") {
		t.Errorf("the singular case still reads as the plural one:\n%s", got)
	}
}

// TestDegenerateReelNote: a single-clip reel with no explicit --duration is the
// shape the fixed-camera collapse takes, and before this note it printed nothing
// past the clip count — which reads as success. The note fires exactly where the
// shortfall note stays silent, and stays silent itself wherever that note is
// already on the screen: two notes about the same span is noise, and the reader
// who learns to skip noise skips the true ones too.
func TestDegenerateReelNote(t *testing.T) {
	reel := func(clips int, seconds float64, meta map[string]string) *timeline.Timeline {
		tr := timeline.Track{}
		for i := 0; i < clips; i++ {
			tr.Clips = append(tr.Clips, timeline.Clip{
				SourceStart: float64(i) * seconds, SourceEnd: float64(i+1) * seconds,
				TimelineStart: float64(i) * seconds, Speed: 1,
			})
		}
		return &timeline.Timeline{Tracks: []timeline.Track{tr}, Metadata: meta}
	}
	collapse := map[string]string{
		"style": "generic_highlight", "candidate_events": "1", "candidate_limit": "true",
	}

	for _, tc := range []struct {
		name  string
		tl    *timeline.Timeline
		asked float64
		want  bool
	}{
		{"one-clip collapse, style's own target", reel(1, 2.8, collapse), 0, true},
		{"one-clip collapse, ask already answered", reel(1, 2.8, collapse), 3.0, true},
		{"one-clip collapse the shortfall note covers", reel(1, 2.8, collapse), 30, false},
		{"healthy reel", reel(3, 2.8, collapse), 0, false},
		{"no document", nil, 0, false},
	} {
		got := degenerateReelNote(tc.tl, tc.asked)
		if tc.want && got == "" {
			t.Errorf("%s: wanted a note, got none", tc.name)
		}
		if !tc.want && got != "" {
			t.Errorf("%s: wanted silence, got %q", tc.name, got)
		}
		if tc.want {
			for _, needle := range []string{"generic_highlight", "2.8s", "--style"} {
				if !strings.Contains(got, needle) {
					t.Errorf("%s: note %q lacks %q", tc.name, got, needle)
				}
			}
		}
	}
}

// TestDegenerateReelNoteReadsOnlyTheDocument: the shortfall clause appears only
// when an ask went unanswered, the candidate count follows the singular/plural
// rule the shortfall note is held to, and a document from before the provenance
// stamp still produces English rather than "the  style cut".
func TestDegenerateReelNoteReadsOnlyTheDocument(t *testing.T) {
	one := func(meta map[string]string) *timeline.Timeline {
		return &timeline.Timeline{
			Tracks: []timeline.Track{{Clips: []timeline.Clip{
				{SourceStart: 0, SourceEnd: 2.8, TimelineStart: 0, Speed: 1},
			}}},
			Metadata: meta,
		}
	}

	got := degenerateReelNote(one(map[string]string{"style": "generic_highlight", "candidate_events": "1"}), 0)
	for _, needle := range []string{"one candidate region", "the reel is a single span (2.8s)"} {
		if !strings.Contains(got, needle) {
			t.Errorf("the no-ask note lacks %q:\n%s", needle, got)
		}
	}
	if strings.Contains(got, "asked for") {
		t.Errorf("the no-ask note claims a shortfall nobody asked about:\n%s", got)
	}

	got = degenerateReelNote(one(map[string]string{"style": "generic_highlight", "candidate_events": "1"}), 30)
	if !strings.Contains(got, "of the 30s asked for") {
		t.Errorf("the unanswered-ask note lacks the shortfall:\n%s", got)
	}

	got = degenerateReelNote(one(map[string]string{"style": "generic_highlight", "candidate_events": "4"}), 0)
	if !strings.Contains(got, "4 candidate regions") || strings.Contains(got, "one candidate region") {
		t.Errorf("the plural count reads as the singular one:\n%s", got)
	}

	got = degenerateReelNote(one(map[string]string{"candidate_events": "1"}), 0)
	if !strings.Contains(got, "the selected style found one candidate region") || strings.Contains(got, "the  style") {
		t.Errorf("a document without a style key does not read as English:\n%s", got)
	}

	got = degenerateReelNote(one(nil), 0)
	if !strings.Contains(got, "the selected style built a single-span reel (2.8s)") {
		t.Errorf("a document with no facts at all still needs a grammatical sentence:\n%s", got)
	}
}

// TestPacingLineSingularOnACollapseReel: the note family already learned that
// "1 candidate rallies" reads as a different claim than the truth; the pacing
// line sat one line above it saying "1 shots". A singular reel gets singular
// nouns everywhere it is described.
func TestPacingLineSingularOnACollapseReel(t *testing.T) {
	tl := &timeline.Timeline{Tracks: []timeline.Track{{Clips: []timeline.Clip{
		{SourceStart: 0, SourceEnd: 2.8, TimelineStart: 0, Speed: 1},
	}}}}
	got := pacingLine(tl)
	if !strings.Contains(got, "1 shot,") {
		t.Errorf("the pacing line for a single shot reads %q", got)
	}
	if strings.Contains(got, "1 shots") {
		t.Errorf("the pacing line still pluralizes a single shot: %q", got)
	}
}

// TestCountNoun: the shared helper behind every count line — zero, one and
// many are the three cases, and English has only two forms for them.
func TestCountNoun(t *testing.T) {
	for _, tc := range []struct {
		n    int
		want string
	}{
		{0, "0 clips"}, {1, "1 clip"}, {2, "2 clips"}, {16, "16 clips"},
	} {
		if got := countNoun(tc.n, "clip", "clips"); got != tc.want {
			t.Errorf("countNoun(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}
