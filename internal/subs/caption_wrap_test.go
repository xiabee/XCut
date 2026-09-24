package subs

import (
	"strings"
	"testing"
)

// A real transcript of Latin text was seen leaving the caption writer as
// "first line o\Nf dialogue": the plain path divided the segment into perLine
// runes without ever looking for a space, so the break landed inside a word.
// For CJK that character rule is the whole design (nothing in the text says
// where a word ends); for text that does say it, cutting the word is a defect
// the sidecar never authorised. These cases hold the two apart.

func plainLatinSegment() Segment {
	return Segment{Start: 0, End: 8, Text: "first line of dialogue over the reel today"}
}

func TestPlainWrapKeepsLatinWordsWhole(t *testing.T) {
	var b strings.Builder
	if err := WriteCaptionASS(plainTranscript(plainLatinSegment()), KaraokeStyle{Width: 1080, Height: 1920}, &b); err != nil {
		t.Fatal(err)
	}
	words := strings.Fields(plainLatinSegment().Text)
	var got []string
	lines := 0
	for _, cue := range dialogueLines(b.String()) {
		for _, line := range strings.Split(textOf(cue), `\N`) {
			lines++
			got = append(got, strings.Fields(line)...)
		}
	}
	if lines < 2 {
		t.Fatalf("the sentence became %d line(s); it is 40 characters on a 12-unit frame", lines)
	}
	if strings.Join(got, " ") != strings.Join(words, " ") {
		t.Errorf("the wrap cut or reordered words:\n got: %q\nwant: %q", strings.Join(got, " "), strings.Join(words, " "))
	}
}

// A word longer than the frame cannot be kept whole, and the documented rule is
// that it is not silently dropped: it still has to reach the screen, broken.
func TestPlainWrapStillBreaksAWordWiderThanTheFrame(t *testing.T) {
	one := "supercalifragilistic"
	var b strings.Builder
	tr := plainTranscript(Segment{Start: 0, End: 4, Text: one})
	if err := WriteCaptionASS(tr, KaraokeStyle{Width: 320, Height: 240}, &b); err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, cue := range dialogueLines(b.String()) {
		for _, line := range strings.Split(textOf(cue), `\N`) {
			if strings.TrimSpace(line) == "" {
				t.Errorf("an empty line was laid out of a %d-character word: %q", len(one), line)
			}
			seen += visibleRunes(line)
		}
	}
	if seen != len(one) {
		t.Errorf("%d characters reached the screen, want all %d of %q", seen, len(one), one)
	}
}

// The character rule stays for text with no break opportunities: this is the
// promise the CJK layout was built on, and a word-boundary rule that silently
// swallowed a long unspaced run would be worse than the mid-word break it fixes.
func TestPlainWrapStillBreaksUnspacedTextByCharacter(t *testing.T) {
	text := strings.Repeat("歌", 30)
	var b strings.Builder
	if err := WriteCaptionASS(plainTranscript(Segment{Start: 0, End: 6, Text: text}), KaraokeStyle{Width: 1080, Height: 1920}, &b); err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, cue := range dialogueLines(b.String()) {
		for _, line := range strings.Split(textOf(cue), `\N`) {
			if got := visibleRunes(line); got > 12 {
				t.Errorf("a line carries %d characters, want at most the frame's 12", got)
			}
			seen += visibleRunes(line)
		}
	}
	if seen != 30 {
		t.Errorf("%d characters reached the screen, want all 30", seen)
	}
}
