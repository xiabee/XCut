package subs

import (
	"strings"
	"testing"
)

// The frame a caption file declares is the only thing a consumer can compare
// against its own reel: the transcript that produced the file is not kept, so
// the PlayRes pair is the whole memory of what shape the captions were laid out
// for. Reading it wrong is worse than not reading it — a false "1280×720" turns
// a reel into one that believes it owns horizontal captions.

func captionTranscript() *Transcript {
	return &Transcript{Segments: []Segment{
		{Start: 0, End: 2, Text: "hello there"},
	}}
}

func TestReadASSFrameRoundTripsTheWriters(t *testing.T) {
	caption := func(style KaraokeStyle) string {
		var b strings.Builder
		if err := WriteCaptionASS(captionTranscript(), style, &b); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	karaoke := func(style KaraokeStyle) string {
		tr := captionTranscript()
		tr.Segments[0].Words = []Word{
			{Start: 0, End: 1, Word: "hello"},
			{Start: 1, End: 2, Word: "there"},
		}
		var b strings.Builder
		if err := WriteKaraokeASS(tr, style, &b); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}

	cases := []struct {
		name   string
		file   string
		w, h   int
		assume string
	}{
		{
			name: "captions on a vertical reel",
			file: caption(KaraokeStyle{Width: 1080, Height: 1920}),
			w:    1080, h: 1920,
			assume: "a 9:16 layout must report the frame it was resolved against",
		},
		{
			name: "karaoke on a vertical reel",
			file: karaoke(KaraokeStyle{Width: 1080, Height: 1920}),
			w:    1080, h: 1920,
			assume: "both writers share the header, so both must be readable",
		},
		{
			name: "no canvas named at all",
			file: caption(KaraokeStyle{}),
			w:    1280, h: 720,
			assume: "the shipped reference is written into the file, so the file declares it",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, h, ok := ReadASSFrame(strings.NewReader(tc.file))
			if !ok {
				t.Fatalf("%s: the generated file declares no frame:\n%s", tc.assume, tc.file)
			}
			if w != tc.w || h != tc.h {
				t.Errorf("%s: read %dx%d, want %dx%d", tc.assume, w, h, tc.w, tc.h)
			}
		})
	}
}

func TestReadASSFrameRefusesToInventAClaim(t *testing.T) {
	cases := map[string]string{
		"no script info": `
[V4+ Styles]
Format: Name, Fontname, Fontsize
Style: Caption,sans-serif,48
`,
		"only the horizontal axis": `
[Script Info]
PlayResX: 1080
`,
		"only the vertical axis": `
[Script Info]
PlayResY: 1920
`,
		"a commented-out default, as hand-edited files carry": `
[Script Info]
;PlayResX: 384
;PlayResY: 288
`,
		"a zero, which is no frame": `
[Script Info]
PlayResX: 0
PlayResY: 0
`,
		"not a number": `
[Script Info]
PlayResX: wide
PlayResY: tall
`,
		"a negative": `
[Script Info]
PlayResX: -1080
PlayResY: -1920
`,
		"srt has no reference frame at all": `1
00:00:00,000 --> 00:00:02,000
hello there
`,
		"the pair buried in dialogue, where a viewer cannot set a frame": `
[Script Info]
ScriptType: v4.00+

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 0,0:00:01.00,0:00:02.00,Caption,,0,0,0,,the subtitle says PlayResX: 1080 and PlayResY: 1920
`,
		// The shape that separates "only Script Info may declare the frame" from
		// "read the first pair you see": a file with no Script Info section at all,
		// so nothing triggers the early exit, and the pair parked in a style block
		// where libass would ignore it.
		"a pair outside the section that owns it": `
[V4+ Styles]
Format: Name, Fontname, Fontsize
Style: Caption,sans-serif,48
PlayResX: 1080
PlayResY: 1920
`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if w, h, ok := ReadASSFrame(strings.NewReader(body)); ok {
				t.Errorf("no frame was declared, but the reader reported %dx%d\n%s", w, h, body)
			}
		})
	}
}

// The section guard has to survive the shape a real Aegisub file has: keys
// before the pair, and the pair in the middle of them.
func TestReadASSFrameSkipsUnrelatedScriptInfoKeys(t *testing.T) {
	body := `[Script Info]
ScriptType: v4.00+
Title: my reel
Collisions: Normal
LineSize: 32
PlayResX: 720
PlayResY: 1280
Timer: 100.0000
ScaledBorderAndShadow: yes

[V4+ Styles]
Format: Name, Fontname, Fontsize
Style: Caption,sans-serif,48
`
	w, h, ok := ReadASSFrame(strings.NewReader(body))
	if !ok || w != 720 || h != 1280 {
		t.Fatalf("read %dx%d ok=%v, want 720x1280 true", w, h, ok)
	}
}
