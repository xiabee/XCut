package subs

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// B5's other half: a cue has to fit the frame and stay long enough to read. The
// convention short-form practice reports is a few characters per line held a couple of
// seconds, and what is not a matter of taste is whether the text fits at all — a
// 40-character line at a 72-point face does not fit 1080 pixels, and leaving that to
// libass (`WrapStyle: 0`) means the frame decides where the break lands, not the reel.

func words(text string, start, end float64) []Word {
	r := []rune(text)
	span := (end - start) / float64(len(r))
	out := make([]Word, 0, len(r))
	for i, c := range r {
		s := start + float64(i)*span
		out = append(out, Word{Start: s, End: s + span, Word: string(c)})
	}
	return out
}

func cue(text string, start, end float64) Segment {
	return Segment{Start: start, End: end, Text: text, Words: words(text, start, end)}
}

func assOf(t *testing.T, tr *Transcript, style KaraokeStyle) string {
	t.Helper()
	var b strings.Builder
	if err := WriteKaraokeASS(tr, style, &b); err != nil {
		t.Fatalf("write: %v", err)
	}
	return b.String()
}

// textOf returns the event's display text: everything after the ninth comma of
// "Dialogue: Layer,Start,End,Style,Name,MarginL,MarginR,MarginV,Effect,Text".
func textOf(dialogue string) string {
	f := strings.Split(dialogue, ",")
	if len(f) <= 9 {
		return dialogue
	}
	return strings.Join(f[9:], ",")
}

// startEndOf pulls one dialogue's times, which are fields 1 and 2 of the split
// (field 0 carries the "Dialogue: Layer" prefix).
func startEndOf(dialogue string) (float64, float64) {
	f := strings.Split(dialogue, ",")
	if len(f) < 3 {
		panic("not a dialogue line: " + dialogue)
	}
	return parseASSTime(f[1]), parseASSTime(f[2])
}

// The vertical frame this package can now measure itself against: 1080 wide, a
// 72-point face, 90-pixel side margins — twelve characters to a line.
func TestCuesWrapToTheFramesWidth(t *testing.T) {
	vertical := KaraokeStyle{Width: 1080, Height: 1920}
	long := strings.Repeat("歌", 40)
	out := assOf(t, segsToTranscript([]Segment{cue(long, 0, 8)}), vertical)
	lines := dialogueLines(out)
	if len(lines) < 2 {
		t.Fatalf("a 40-character cue became %d cue(s); twelve characters do not fit a 1080-pixel line at 72 points", len(lines))
	}
	seen := 0
	for i, l := range lines {
		body := textOf(l)
		got := strings.Count(body, `\N`)
		if got > 1 {
			t.Errorf("cue %d has %d line breaks, want at most one (two lines on screen at a time)", i, got)
		}
		for _, line := range strings.Split(body, `\N`) {
			runes := visibleRunes(line)
			seen += runes
			if runes > 12 {
				t.Errorf("cue %d line %q is %d characters, want at most 12 on this frame", i, line, runes)
				// The budget the writer lays out against counts the separator between words as
				// well as the characters, because that is what the frame's line can hold — twelve units
				// here, six single-character words with their spaces. Measuring characters alone would
				// let a width computation that ignores the margins pass this test while running text
				// off the picture, which is the thing being prevented.
				if units := runes + strings.Count(line, "{\\k"); units > 12 {
					t.Errorf("cue %d line takes %d of this frame's 12 units per line", i, units)
				}
			}
			// The budget the writer lays out against counts the separator between words as
			// well as the characters, because that is what the frame's line can hold — twelve units
			// here, six single-character words with their spaces. Measuring characters alone would
			// let a width computation that ignores the margins pass this test while running text
			// off the picture, which is the thing being prevented.
			if units := runes + strings.Count(line, "{\\k"); units > 12 {
				t.Errorf("cue %d line takes %d of this frame's 12 units per line", i, units)
			}
		}
	}
	// Nothing is dropped or duplicated by the layout.
	if sweeps := strings.Count(out, "{\\kf"); sweeps != 40 {
		t.Errorf("karaoke sweeps = %d across the file, want 40 (one per character)", sweeps)
	}
	if seen != 40 {
		t.Errorf("visible characters = %d, want 40", seen)
	}
	// A line's last character is sung at its own moment, not held to the end of the
	// cue: with twelve characters every 0.2 s across a 2.4 s cue, any sweep much
	// longer than one step means the fill ran past the singer.
	for _, l := range lines {
		for _, tag := range strings.Split(textOf(l), "{\\kf") {
			i := strings.Index(tag, "}")
			if i <= 0 {
				continue
			}
			if v := atoi(tag[:i]); v > 25 {
				t.Errorf("a {\\kf%d} sweep outlasts its character (one step here is 20cs)", v)
			}
		}
	}
}

// TestShortCueIsNotSplit is the control for the case above: text that fits stays one
// cue on one line. A layout that always wraps would pass the test it is paired with
// while making every caption flicker.
func TestShortCueIsNotSplit(t *testing.T) {
	out := assOf(t, segsToTranscript([]Segment{cue("你好", 0, 2)}), KaraokeStyle{Width: 1080, Height: 1920})
	lines := dialogueLines(out)
	if len(lines) != 1 {
		t.Fatalf("a two-character cue became %d cues: %v", len(lines), lines)
	}
	if strings.Contains(textOf(lines[0]), `\N`) {
		t.Fatalf("a two-character cue was wrapped: %s", textOf(lines[0]))
	}
}

// TestHoldStopsAtTheNextCue: the dwell is a floor, not a target. A 0.3-second cue
// followed by speech at 0.5 s must be held to 0.5, not to 1.2 — a caption that
// steps on the next one blinks, which is the thing the hold exists to prevent.
func TestHoldStopsAtTheNextCue(t *testing.T) {
	tr := segsToTranscript([]Segment{
		cue("快", 0, 0.3),
		cue("下一句", 0.5, 3),
	})
	lines := dialogueLines(assOf(t, tr, KaraokeStyle{Width: 1080, Height: 1920}))
	if len(lines) != 2 {
		t.Fatalf("want two cues, got %d", len(lines))
	}
	_, firstEnd := startEndOf(lines[0])
	start2, _ := startEndOf(lines[1])
	if firstEnd > start2+1e-6 {
		t.Errorf("the hold ran to %.2fs and stepped on the next cue at %.2fs", firstEnd, start2)
	}
	if firstEnd > 0.5+1e-6 {
		t.Errorf("first cue held to %.2fs, want it to stop at the next cue's start (0.50s)", firstEnd)
	}
}

func TestCueTimesAreMonotonicAndNeverOverlap(t *testing.T) {
	tr := segsToTranscript([]Segment{
		cue(strings.Repeat("长", 60), 0, 9),
		cue("短", 9.5, 11),
	})
	out := assOf(t, tr, KaraokeStyle{Width: 1080, Height: 1920})
	lines := dialogueLines(out)
	if len(lines) < 3 {
		t.Fatalf("expected the long cue to be split across cues, got %d dialogue line(s)", len(lines))
	}
	var starts, ends []float64
	for _, l := range lines {
		st, en := startEndOf(l)
		starts, ends = append(starts, st), append(ends, en)
	}
	for i := range starts {
		if !(ends[i] > starts[i]) {
			t.Errorf("cue %d does not end after it starts (%.2f..%.2f)", i, starts[i], ends[i])
		}
		if i > 0 && starts[i] < starts[i-1] {
			t.Errorf("cue %d goes backwards in time (%.2f after %.2f)", i, starts[i], starts[i-1])
		}
		if i > 0 && starts[i]+1e-6 < ends[i-1] {
			t.Errorf("cue %d starts at %.2f, before cue %d ends at %.2f — overlapping captions blink", i, starts[i], i-1, ends[i-1])
		}
	}
}

// TestBriefCueIsHeldLongEnoughToRead: a 0.3-second utterance is still words on a
// screen, and the second half of the convention is dwell, not size. The hold may use
// the silence after the speech — never the next cue's time.
func TestBriefCueIsHeldLongEnoughToRead(t *testing.T) {
	tr := segsToTranscript([]Segment{
		cue("一句", 0, 0.3),
		cue("下一句", 3, 5),
	})
	out := assOf(t, tr, KaraokeStyle{Width: 1080, Height: 1920})
	lines := dialogueLines(out)
	if len(lines) != 2 {
		t.Fatalf("want two cues, got %d", len(lines))
	}
	st, en := startEndOf(lines[0])
	held := en - st
	if held < 1.2 {
		t.Errorf("the brief cue is shown for %.2fs, want at least 1.2s of dwell", held)
	}
	if held > 3+1e-6 {
		t.Errorf("the hold ran to %.2fs and reached the next cue's start at 3.00s", held)
	}
}

// visibleRunes counts the characters of the lyric on one line: {\kfNN} / {\kNN} tags
// are timing, not text, and the spaces between words are the renderer's own, so what
// is left should be exactly the characters the transcript carried.
func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return -1
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func visibleRunes(assText string) int {
	out := 0
	for i := 0; i < len(assText); {
		if assText[i] == '{' {
			j := strings.IndexByte(assText[i:], '}')
			if j < 0 {
				i++
				continue
			}
			i += j + 1
			continue
		}
		r, size := utf8.DecodeRuneInString(assText[i:])
		if r != ' ' {
			out++
		}
		i += size
	}
	return out
}

// TestHoldDoesNotStretchTheFill is the same claim on a fixture where the two are
// easy to tell apart: two characters spoken across 0.3 s, held for a second more.
// Each sweep is 15 centiseconds; anything larger means the dwell leaked into the
// karaoke timing.
func TestHoldDoesNotStretchTheFill(t *testing.T) {
	tr := segsToTranscript([]Segment{
		cue("一句", 0, 0.3),
		cue("下一句", 3, 5),
	})
	lines := dialogueLines(assOf(t, tr, KaraokeStyle{Width: 1080, Height: 1920}))
	if len(lines) != 2 {
		t.Fatalf("want two cues, got %d", len(lines))
	}
	_, heldEnd := startEndOf(lines[0])
	if heldEnd-0.3 < 0.8 {
		t.Fatalf("the cue was not held (end %.2fs)", heldEnd)
	}
	for _, tag := range strings.Split(textOf(lines[0]), "{\\"+"kf") {
		i := strings.Index(tag, "}")
		if i <= 0 {
			continue
		}
		if v := atoi(tag[:i]); v > 16 {
			t.Errorf("a {\\"+"kf%d} sweep outlasts its 0.15s of singing — the dwell leaked into the fill", v)
		}
	}
}
