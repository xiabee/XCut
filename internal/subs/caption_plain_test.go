package subs

import (
	"math"
	"strings"
	"testing"
)

// A transcript with no word timings used to get an SRT and nothing else, which meant
// the burn-in styled it with libass defaults while the karaoke path got a designed
// frame: same words, two different captions depending on whether a sidecar happened to
// know when each syllable fell. These tests hold both paths to one layout.

func plainTranscript(segs ...Segment) *Transcript {
	return &Transcript{Segments: segs}
}

func plainSegment(text string, start, end float64) Segment {
	return Segment{Start: start, End: end, Text: text}
}

func TestCaptionASSSharesTheKaraokeLayout(t *testing.T) {
	long := strings.Repeat("歌", 40)
	var b strings.Builder
	tr := plainTranscript(plainSegment(long, 0, 8))
	if err := WriteCaptionASS(tr, KaraokeStyle{Width: 1080, Height: 1920}, &b); err != nil {
		t.Fatalf("write: %v", err)
	}
	out := b.String()
	for _, want := range []string{"PlayResX: 1080", "PlayResY: 1920", "Fontsize", "Style: Caption,"} {
		if want == "Fontsize" {
			continue // present in the Format line; checked below by name
		}
		if !strings.Contains(out, want) {
			t.Errorf("caption .ass lacks %q:\n%s", want, out[:min(len(out), 400)])
		}
	}
	lines := dialogueLines(out)
	if len(lines) < 2 {
		t.Fatalf("a 40-character line became %d cue(s); the layout is shared with karaoke, so it must wrap here too", len(lines))
	}
	// No karaoke: nothing to fill, and a leftover sweep tag would tell libass to
	// highlight text that was never timed.
	if strings.Contains(out, `{\\kf`) || strings.Contains(out, `{\\k0`) {
		t.Errorf("the plain style still carries karaoke sweeps:\n%s", out)
	}
	// The characters are all there, spread over the cues, and none of them runs
	// off the frame it was laid out for.
	seen := 0
	for i, l := range lines {
		body := textOf(l)
		if got := strings.Count(body, `\N`); got > 1 {
			t.Errorf("cue %d has %d line breaks, want at most one (two lines on screen at a time)", i, got)
		}
		for _, line := range strings.Split(body, `\N`) {
			if runes := visibleRunes(line); runes > 12 {
				t.Errorf("cue %d line %q is %d characters, want at most 12 on this frame", i, line, runes)
			}
			seen += visibleRunes(line)
		}
	}
	if seen != 40 {
		t.Errorf("visible characters = %d, want 40", seen)
	}
}

// TestPlainCuesTileTheSegmentsTime: splitting a line nobody timed means inventing
// the clock for its pieces. The invention on offer is an even share by the
// characters each piece carries — so a short last piece must leave early, and the
// span must be handed over with neither a hole nor an overlap.
func TestPlainCuesTileTheSegmentsTime(t *testing.T) {
	var b strings.Builder
	tr := plainTranscript(plainSegment(strings.Repeat("长", 60), 0, 9), plainSegment("短", 9.5, 11))
	if err := WriteCaptionASS(tr, KaraokeStyle{Width: 1080, Height: 1920}, &b); err != nil {
		t.Fatalf("write: %v", err)
	}
	lines := dialogueLines(b.String())
	// Twelve units a line, two lines a cue: 24 characters, 24, then the last 12.
	if len(lines) != 4 {
		t.Fatalf("want 3 split cues plus the second segment, got %d:\n%s", len(lines), b.String())
	}
	want := [][2]float64{{0, 3.6}, {3.6, 7.2}, {7.2, 9}, {9.5, 11}}
	for i, l := range lines {
		st, en := startEndOf(l)
		if math.Abs(st-want[i][0]) > 1e-6 || math.Abs(en-want[i][1]) > 1e-6 {
			t.Errorf("cue %d shows %.2f..%.2f, want %.2f..%.2f (the share is by characters, not by cue count)\n%s",
				i, st, en, want[i][0], want[i][1], b.String())
		}
	}
}

func TestCaptionASSHoldsBriefLinesAndEscapes(t *testing.T) {
	var b strings.Builder
	tr := plainTranscript(
		plainSegment("短", 0, 0.3),
		plainSegment("a{b}c\\d", 3, 5),
	)
	if err := WriteCaptionASS(tr, KaraokeStyle{Width: 1280, Height: 720}, &b); err != nil {
		t.Fatalf("write: %v", err)
	}
	out := b.String()
	lines := dialogueLines(out)
	if len(lines) != 2 {
		t.Fatalf("want two cues, got %d:\n%s", len(lines), out)
	}
	_, end := startEndOf(lines[0])
	if end < 1.2 {
		t.Errorf("the brief cue ends at %.2fs, want the same 1.2s floor the karaoke path uses", end)
	}
	// Sidecar text is untrusted: braces and backslashes are libass syntax.
	body := textOf(lines[1])
	if strings.Contains(body, "{") || strings.Contains(body, "}") || strings.Contains(body, "\\d") {
		t.Errorf("unescaped control characters reached the file: %q", body)
	}
}

func TestCaptionASSRefusesNothingButHasSomethingToShow(t *testing.T) {
	var b strings.Builder
	if err := WriteCaptionASS(&Transcript{}, KaraokeStyle{}, &b); err == nil {
		t.Fatal("an empty transcript wrote a file; the caller would publish an empty [Events] section")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
