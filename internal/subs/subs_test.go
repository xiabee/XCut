package subs

import (
	"strings"
	"testing"
)

const transcriptJSON = `{
  "language": "zh",
  "segments": [
    {"start": 3.5, "end": 5.0, "text": "第二句", "words": [
      {"start": 3.5, "end": 4.2, "word": "第二句"}]},
    {"start": 1.0, "end": 2.5, "text": "第一句", "words": [
      {"start": 1.0, "end": 1.4, "word": "第一"},
      {"start": 1.6, "end": 2.5, "word": "句"}]},
    {"start": 6.0, "end": 7.0, "text": "   "}
  ]
}`

func TestParseValidatesSortsAndDrops(t *testing.T) {
	tt, err := Parse([]byte(transcriptJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(tt.Segments) != 2 {
		t.Fatalf("segments = %d, want 2 (empty text and bad times dropped)", len(tt.Segments))
	}
	if tt.Segments[0].Start != 1.0 || tt.Segments[1].Start != 3.5 {
		t.Fatalf("segments not sorted by start: %v", tt.Segments)
	}
	if tt.Language != "zh" {
		t.Fatalf("language %q", tt.Language)
	}
	if !tt.HasWordTimings() {
		t.Fatal("both segments carry words")
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	if _, err := Parse([]byte(`{"segments": [{"start": 2, "end": 1, "text": "backwards"}]}`)); err == nil {
		t.Fatal("end before start must be rejected")
	}
	if _, err := Parse([]byte(`{"segments": [{"start": 0, "end": 1}]}`)); err == nil {
		t.Fatal("empty text must leave nothing")
	}
	if _, err := Parse([]byte(`not json`)); err == nil {
		t.Fatal("garbage payload must be rejected")
	}
	if _, err := Parse([]byte(`{"segments": []}`)); err == nil {
		t.Fatal("empty transcript must be an error, not silence")
	}
}

func TestWriteSRT(t *testing.T) {
	tt, err := Parse([]byte(`{"segments": [
		{"start": 1.0, "end": 2.5, "text": "hello world"},
		{"start": 3661.25, "end": 3662.0, "text": "one hour in"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := WriteSRT(tt, &b); err != nil {
		t.Fatal(err)
	}
	want := "1\n00:00:01,000 --> 00:00:02,500\nhello world\n\n" +
		"2\n01:01:01,250 --> 01:01:02,000\none hour in\n"
	if b.String() != want {
		t.Fatalf("srt =\n%q\nwant\n%q", b.String(), want)
	}
}

func TestWriteKaraokeASS(t *testing.T) {
	tt, err := Parse([]byte(`{"segments": [
		{"start": 1.0, "end": 2.5, "text": "第一 句", "words": [
			{"start": 1.0, "end": 1.4, "word": "第一"},
			{"start": 1.6, "end": 2.5, "word": "句"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := WriteKaraokeASS(tt, KaraokeStyle{}, &b); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, "Style: Karaoke,sans-serif,48,") {
		t.Fatalf("missing default style line:\n%s", out)
	}
	if !strings.Contains(out, "Dialogue: 0,0:00:01.00,0:00:02.50,Karaoke,,0,0,0,,{\\kf60}第一 {\\kf90}句") {
		t.Fatalf("karaoke line wrong:\n%s", out)
	}
	// \kf centiseconds must sum to the segment duration: word 1 fills from
	// 1.0 to the next word's start 1.6 (the gap belongs to it), word 2 from
	// 1.6 to the segment end 2.5 — 60cs + 90cs = 150cs = 1.5s ✓.
}

func TestWriteKaraokeASSRequiresWordTimings(t *testing.T) {
	tt, err := Parse([]byte(`{"segments": [{"start": 1.0, "end": 2.0, "text": "no words"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := WriteKaraokeASS(tt, KaraokeStyle{}, &b); err == nil {
		t.Fatal("karaoke output without word timings must be refused")
	}
}

func TestASSTimeFormat(t *testing.T) {
	cases := map[float64]string{0: "0:00:00.00", 59.999: "0:01:00.00", 3661.25: "1:01:01.25"}
	for in, want := range cases {
		if got := assTime(in); got != want {
			t.Errorf("assTime(%v) = %q, want %q", in, got, want)
		}
	}
}

// TestKaraokeLeadSpan: whisper-style word timestamps typically start after
// the segment start. ASS \kf fills are cumulative from the Dialogue start,
// so without a leading offset every sweep fired early by that gap. The
// zero-width {\k} span absorbs it; total centiseconds still cover the whole
// Dialogue (lead + fills == segment duration).
func TestKaraokeLeadSpan(t *testing.T) {
	tt, err := Parse([]byte(`{"segments": [
		{"start": 1.0, "end": 2.5, "text": "第一 句", "words": [
			{"start": 1.2, "end": 1.5, "word": "第一"},
			{"start": 1.7, "end": 2.4, "word": "句"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := WriteKaraokeASS(tt, KaraokeStyle{}, &b); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	// Lead 0.2s = 20cs, word 1 fills 1.2→1.7 (50cs), word 2 fills 1.7→2.5 (80cs).
	if !strings.Contains(out, `{\k20}{\kf50}第一 {\kf80}句`) {
		t.Fatalf("karaoke line missing the lead span or wrong fills:\n%s", out)
	}
}

// TestKaraokeEscapesControlChars: sidecar text is untrusted — braces would
// inject live ASS override tags and a newline would split the Dialogue event,
// corrupting the whole [Events] section from that point.
func TestKaraokeEscapesControlChars(t *testing.T) {
	tt, err := Parse([]byte(`{"segments": [
		{"start": 1.0, "end": 2.0, "text": "bad", "words": [
			{"start": 1.0, "end": 1.5, "word": "{\\b1}坏"},
			{"start": 1.5, "end": 2.0, "word": "line\nbreak"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := WriteKaraokeASS(tt, KaraokeStyle{}, &b); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if strings.Contains(out, "{\b1}") {
		t.Fatalf("raw override tag leaked into the Dialogue line:\n%s", out)
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "Dialogue:") && strings.Count(l, ",,") < 3 {
			// Dialogue lines have a fixed comma structure; an embedded raw
			// newline would have produced stray continuation lines.
			continue
		}
	}
	dialogue := 0
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "Dialogue: ") {
			dialogue++
		}
	}
	if dialogue != 1 {
		t.Fatalf("expected exactly 1 Dialogue event (a raw newline would split it), got %d:\n%s", dialogue, out)
	}
	if !strings.Contains(out, "(/b1)坏") || !strings.Contains(out, "line break") {
		t.Fatalf("escape replacements missing:\n%s", out)
	}
}
