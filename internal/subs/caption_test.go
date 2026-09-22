package subs

import (
	"fmt"
	"strings"
	"testing"
)

// Caption styling is the part of a reel a viewer reads on a phone, and the
// convention short-form practice reports is concrete: a few characters per line,
// held a couple of seconds, white with a thin outline. What is *not* a matter of
// taste is geometry: a file whose PlayRes says 1280×720 gets its font and margins
// scaled against that box even when the reel is 1080×1920, so the same style lands
// in different places on two canvases. These tests pin the geometry first, because
// that part has a right answer.

func assFor(t *testing.T, tr *Transcript, style KaraokeStyle) string {
	t.Helper()
	var b strings.Builder
	if err := WriteKaraokeASS(tr, style, &b); err != nil {
		t.Fatalf("write: %v", err)
	}
	return b.String()
}

func segsToTranscript(segs []Segment) *Transcript {
	for i := range segs {
		if segs[i].Words == nil {
			segs[i].Words = wordsFor(segs[i].Text, segs[i].Start, segs[i].End)
		}
	}
	return &Transcript{Segments: segs}
}

// wordsFor splits a line into one timed token per rune — the shape a CJK
// transcript arrives with.
func wordsFor(text string, start, end float64) []Word {
	r := []rune(text)
	if len(r) == 0 {
		return nil
	}
	span := (end - start) / float64(len(r))
	out := make([]Word, 0, len(r))
	for i, c := range r {
		s := start + float64(i)*span
		out = append(out, Word{Start: s, End: s + span, Word: string(c)})
	}
	return out
}

func TestASSHeaderMatchesTheReelsCanvas(t *testing.T) {
	tr := segsToTranscript([]Segment{{Start: 0, End: 2, Text: "你好"}})
	plain := assFor(t, tr, KaraokeStyle{})
	vertical := assFor(t, tr, KaraokeStyle{Width: 1080, Height: 1920})

	for _, want := range []string{"PlayResX: 1280", "PlayResY: 720"} {
		if !strings.Contains(plain, want) {
			t.Errorf("a style with no canvas says nothing about the frame; the header should keep the shipped reference (%s): %s", want, firstLines(plain, 6))
		}
	}
	for _, want := range []string{"PlayResX: 1080", "PlayResY: 1920"} {
		if !strings.Contains(vertical, want) {
			t.Errorf("a 1080x1920 reel got no matching PlayRes: %s", firstLines(vertical, 6))
		}
	}
	// The font is the same proportion of the frame on either canvas, so a
	// caption does not shrink just because the reel turned sideways.
	if fs := field(vertical, "Style:", 3); fs != "72" {
		t.Errorf("vertical Fontsize = %s, want 72 (48 against a 720 reference, scaled to a 1080 short side)", fs)
	}
	if fs := field(plain, "Style:", 3); fs != "48" {
		t.Errorf("720p Fontsize = %s, want 48 — the shipped default, unchanged", fs)
	}
	// A small frame still gets a stroke: at a scale where 2 pixels rounds to 1 and
	// below, an outline of 0 would put white text directly on a bright frame and
	// make it unreadable, which is the one thing the outline is for.
	small := assFor(t, tr, KaraokeStyle{Width: 256, Height: 144})
	if o := field(small, "Style:", 17); o != "1" {
		t.Errorf("256x144 Outline = %s, want 1 — the floor, not the rounding", o)
	}
	// Margins and outline scale with the axis they are measured along, or a
	// bottom-centred caption drifts into the frame on a taller canvas.
	if mv := field(vertical, "Style:", 22); mv != "107" {
		t.Errorf("vertical MarginV = %s, want 107 (40 against 720, scaled by 1920)", mv)
	}
	if o := field(vertical, "Style:", 17); o != "3" {
		t.Errorf("vertical Outline = %s, want 3 (a stroke is measured against the glyphs it outlines: 2 scaled by the short side)", o)
	}
}

func firstLines(s string, n int) string {
	parts := strings.Split(s, "\n")
	if len(parts) > n {
		parts = parts[:n]
	}
	return strings.Join(parts, " | ")
}

// field returns the n-th comma-separated field of the first line starting with
// prefix (1-based, as ASS documents its own columns).
func field(ass, prefix string, n int) string {
	for _, line := range strings.Split(ass, "\n") {
		if strings.HasPrefix(line, prefix) {
			fs := strings.Split(line, ",")
			if n-1 < len(fs) {
				return strings.TrimSpace(fs[n-1])
			}
		}
	}
	return ""
}

func dialogueLines(ass string) []string {
	var out []string
	for _, line := range strings.Split(ass, "\n") {
		if strings.HasPrefix(line, "Dialogue:") {
			out = append(out, line)
		}
	}
	return out
}

// parseASSTime reads the 0:00:02.50 form the writer emits.
func parseASSTime(s string) float64 {
	s = strings.TrimSpace(s)
	var h, m, sec, cs int
	if _, err := fmt.Sscanf(s, "%d:%d:%d.%d", &h, &m, &sec, &cs); err != nil {
		panic(err)
	}
	return float64(h*3600+m*60+sec) + float64(cs)/100
}

func clip(s string) string {
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}
