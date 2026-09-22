package timeline

import (
	"encoding/json"
	"strings"
	"testing"
)

// reel builds the document a style run would write: one video track, clips laid
// back to back. TimelineStart is set separately from SourceStart on purpose —
// every number Pacing reports about *position* has to come from the output
// timeline, and a fixture where the two coincide cannot tell them apart.
func reel(clips ...Clip) *Timeline {
	for i := range clips {
		clips[i].TimelineStart = float64(i) * 100
	}
	return &Timeline{Tracks: []Track{{ID: "v1", Kind: "video", Clips: clips}}}
}

func shot(srcStart, srcEnd, speed float64, score string) Clip {
	c := Clip{SourceStart: srcStart, SourceEnd: srcEnd, Speed: speed}
	if score != "" {
		c.Metadata = map[string]string{"score": score}
	}
	return c
}

// TestPacingEqualShots is the control: with one shot length there is exactly one
// honest answer, and mean, median, longest and shortest must all say it. A median
// taken from an unsorted list, or a mean divided by the wrong count, still looks
// plausible on uneven input — never on this.
func TestPacingEqualShots(t *testing.T) {
	tl := reel(shot(3, 6, 1, ""), shot(10, 13, 1, ""), shot(20, 23, 1, ""), shot(30, 33, 1, ""))
	p := tl.Pacing()
	if p.Shots != 4 {
		t.Fatalf("Shots = %d, want 4", p.Shots)
	}
	for _, v := range []float64{p.MeanSeconds, p.MedianSeconds, p.LongestSeconds, p.ShortestSeconds} {
		if v != 3 {
			t.Fatalf("every shot is 3s, so mean/median/longest/shortest must all be 3; got %v %v %v %v",
				p.MeanSeconds, p.MedianSeconds, p.LongestSeconds, p.ShortestSeconds)
		}
	}
}

// TestPacingUnevenShots pins the four statistics against values that differ from
// each other, on a clip list deliberately out of length order, with one clip at
// half speed so a measurement that used the source span instead of the played
// span lands on a number this fixture does not contain.
func TestPacingUnevenShots(t *testing.T) {
	// Played lengths: 2, 9, 3 (a 6 s source span at 2x), 4.
	tl := reel(shot(0, 2, 1, ""), shot(100, 109, 1, ""), shot(200, 206, 2, ""), shot(300, 304, 1, ""))
	p := tl.Pacing()
	if p.ShortestSeconds != 2 || p.LongestSeconds != 9 {
		t.Fatalf("shortest/longest = %v/%v, want 2/9", p.ShortestSeconds, p.LongestSeconds)
	}
	if p.MeanSeconds != 4.5 { // (2+9+3+4)/4; a source-span mean would say 5.25
		t.Fatalf("mean = %v, want 4.5", p.MeanSeconds)
	}
	if p.MedianSeconds != 3.5 { // sorted 2,3,4,9 -> (3+4)/2
		t.Fatalf("median = %v, want 3.5 (an unsorted median would say 5.5)", p.MedianSeconds)
	}
}

// TestPacingOddCountMedian takes the middle value rather than averaging two.
func TestPacingOddCountMedian(t *testing.T) {
	tl := reel(shot(0, 5, 1, ""), shot(50, 51, 1, ""), shot(100, 103, 1, ""))
	if p := tl.Pacing(); p.MedianSeconds != 3 {
		t.Fatalf("median = %v, want 3 (the middle of 1, 3, 5)", p.MedianSeconds)
	}
}

// TestPacingLocatesTheTopShotOnTheOutputTimeline: the hook is a statement about
// when the viewer meets the best moment, so it is read off the output position of
// the highest-scored clip — not its source position, not the first clip.
func TestPacingLocatesTheTopShotOnTheOutputTimeline(t *testing.T) {
	tl := reel(
		shot(500, 502, 1, "0.31"),
		shot(10, 12, 1, "0.44"),
		shot(900, 903, 1, "0.82"), // the best shot, third in the output
		shot(40, 41, 1, "0.20"),
	)
	p := tl.Pacing()
	if p.ScoredShots != 4 {
		t.Fatalf("ScoredShots = %d, want 4", p.ScoredShots)
	}
	if p.HookSeconds != 200 {
		t.Fatalf("HookSeconds = %v, want 200 (the third clip's output start); its source time is 900 and clip order would also be a bug", p.HookSeconds)
	}
	if p.HookScore != 0.82 {
		t.Fatalf("HookScore = %v, want 0.82", p.HookScore)
	}
}

// TestPacingTopShotTieIsFirstSeen: two clips can score identically. The answer
// must not depend on document order — "the one the viewer meets first" is the
// only reading that means anything for a hook, so the fixture lists the clips
// with the later-starting one first.
func TestPacingTopShotTieIsFirstSeen(t *testing.T) {
	tl := &Timeline{Tracks: []Track{{ID: "v1", Kind: "video", Clips: []Clip{
		{SourceStart: 200, SourceEnd: 202, TimelineStart: 200, Speed: 1, Metadata: map[string]string{"score": "0.9"}},
		{SourceStart: 100, SourceEnd: 102, TimelineStart: 100, Speed: 1, Metadata: map[string]string{"score": "0.9"}},
	}}}}
	if p := tl.Pacing(); p.HookSeconds != 100 {
		t.Fatalf("HookSeconds = %v, want 100 (the earlier of the two equal tops, whichever order the document lists)", p.HookSeconds)
	}
}

// TestPacingOnDocumentsThatCannotAnswer: an empty reel, and a hand-edited one with
// no scores in it. Both must report the shot stats they can and say nothing about
// a hook — a zero here is a measurement, not a claim that the reel opens strong.
func TestPacingOnDocumentsThatCannotAnswer(t *testing.T) {
	var none Timeline
	if p := none.Pacing(); p.Shots != 0 || p.MeanSeconds != 0 || p.LongestSeconds != 0 || p.ScoredShots != 0 || p.HookSeconds != 0 {
		t.Fatalf("empty document: %+v, want the zero value", p)
	}
	edited := reel(shot(0, 4, 1, ""), shot(100, 106, 1, ""))
	p := edited.Pacing()
	if p.Shots != 2 || p.MeanSeconds != 5 {
		t.Fatalf("unscored document: %+v, want two 5s shots measured", p)
	}
	if p.ScoredShots != 0 || p.HookSeconds != 0 || p.HookScore != 0 {
		t.Fatalf("unscored document claims a hook: %+v", p)
	}
}

// TestPacingIgnoresUnreadableScores: metadata is strings, and a hand-edited or
// half-written document can carry anything in the score key. An unparsable score
// is not a zero score — it must not outrank a real one, nor be counted.
func TestPacingIgnoresUnreadableScores(t *testing.T) {
	tl := reel(shot(0, 2, 1, "not-a-number"), shot(100, 102, 1, "0.5"), shot(200, 202, 1, ""))
	p := tl.Pacing()
	if p.ScoredShots != 1 {
		t.Fatalf("ScoredShots = %d, want 1 (the unparsable and absent scores are not scores)", p.ScoredShots)
	}
	if p.HookSeconds != 100 || p.HookScore != 0.5 {
		t.Fatalf("hook = %v/%v, want the one readable score at 100", p.HookSeconds, p.HookScore)
	}
}

// TestPacingNumbersFitOnTheWire: these numbers go out over HTTP and into a browser.
// Durations sum in float64, so three shots of 7.1, 7.3 and 8.1 seconds average to
// 7.500000000000001 — a figure that invites a reader to wonder what the trailing
// digits mean. They don't: a millisecond is the finest claim a shot list can make,
// and the walk over a real match is what put the long form in front of a person.
func TestPacingNumbersFitOnTheWire(t *testing.T) {
	// One second played at 3× is 1/3, which has no finite decimal form: the first
	// version of this case used durations a human would write (7.1, 7.3, 8.1) and
	// the mutation that deleted the rounding sailed through it, because those
	// thirds of a second are what actually fail to fit on the wire.
	tl := reel(shot(0, 1, 3, "0.5"), shot(10, 11, 3, "0.9"), shot(20, 21, 3, "0.4"))
	raw, err := json.Marshal(tl.Pacing())
	if err != nil {
		t.Fatal(err)
	}
	doc := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"mean_seconds", "median_seconds", "longest_seconds", "shortest_seconds", "hook_seconds"} {
		v := string(doc[key])
		if i := strings.Index(v, "."); i >= 0 && len(v)-i-1 > 3 {
			t.Errorf("%s = %s on the wire, want at most millisecond digits", key, v)
		}
	}
	for key, want := range map[string]string{"mean_seconds": "0.333", "shortest_seconds": "0.333", "longest_seconds": "0.333"} {
		if got := string(doc[key]); got != want {
			t.Errorf("%s = %s, want %s (1/3 of a second rounded to the millisecond)", key, got, want)
		}
	}
}
