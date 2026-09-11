// Package subs turns sidecar speech transcripts into subtitle files: plain
// SRT for standard subtitles, and karaoke ASS (word-level \kf fills) for
// KTV-style sing-alongs. The transcript itself always comes from an AI
// sidecar — the core never runs models and never downloads them (D3); this
// package is the deterministic formatting half of that split.
package subs

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"

	"github.com/xiabee/XCut/internal/xcerr"
)

// Word is one timed token inside a transcript segment (optional: plain SRT
// needs only segment times).
type Word struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Word  string  `json:"word"`
}

// Segment is one subtitle cue: a spoken span and its text, optionally with
// word-level timings for karaoke rendering.
type Segment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
	Words []Word  `json:"words,omitempty"`
}

// Transcript is the sidecar analyze result for the "transcript" analyzer.
type Transcript struct {
	Language string    `json:"language,omitempty"`
	Segments []Segment `json:"segments"`
}

// Parse validates a sidecar transcript payload: times must be finite and
// ordered (unordered input is sorted), empty cues are dropped, and every
// remaining segment must end after it starts.
func Parse(raw []byte) (*Transcript, error) {
	var t Transcript
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, xcerr.E(xcerr.CodeValidation, "transcript payload is not valid JSON", err)
	}
	out := t.Segments[:0]
	for _, s := range t.Segments {
		s.Text = strings.TrimSpace(s.Text)
		if s.Text == "" {
			continue
		}
		if !finite(s.Start) || !finite(s.End) || s.Start < 0 || s.End <= s.Start {
			return nil, xcerr.E(xcerr.CodeValidation,
				fmt.Sprintf("transcript segment has invalid times (%g..%g)", s.Start, s.End), nil)
		}
		for _, w := range s.Words {
			if !finite(w.Start) || !finite(w.End) || w.End < w.Start || w.Start < 0 {
				return nil, xcerr.E(xcerr.CodeValidation,
					"transcript word timing is invalid", nil)
			}
		}
		sort.Slice(s.Words, func(i, j int) bool { return s.Words[i].Start < s.Words[j].Start })
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	if len(out) == 0 {
		return nil, xcerr.E(xcerr.CodeValidation,
			"transcript is empty (no speech recognized, or word timings unavailable)", nil)
	}
	t.Segments = out
	return &t, nil
}

// HasWordTimings reports whether every segment carries word timings —
// karaoke output needs them all (a partially timed transcript would flash
// untimed lines).
func (t *Transcript) HasWordTimings() bool {
	for _, s := range t.Segments {
		if len(s.Words) == 0 {
			return false
		}
	}
	return true
}

// WriteSRT renders the transcript as SubRip (.srt).
func WriteSRT(t *Transcript, w io.Writer) error {
	bw := bufio.NewWriter(w)
	for i, s := range t.Segments {
		if i > 0 {
			bw.WriteString("\n")
		}
		fmt.Fprintf(bw, "%d\n%s --> %s\n%s\n",
			i+1, srtTime(s.Start), srtTime(s.End), s.Text)
	}
	return bw.Flush()
}

// KaraokeStyle carries the ASS style knobs (sane defaults for 16:9 canvases).
type KaraokeStyle struct {
	FontName string `json:"font_name,omitempty"` // default: libass fallback
	FontSize int    `json:"font_size,omitempty"` // default 48
	// HighlightColor is the fill for sung text (ASS &HAABBGGRR); default
	// yellow. Unsung text stays white.
	HighlightColor string `json:"highlight_color,omitempty"`
}

// WriteKaraokeASS renders the transcript as ASS with word-level \kf sweeps
// (the KTV sing-along fill). Requires word timings on every segment.
func WriteKaraokeASS(t *Transcript, style KaraokeStyle, w io.Writer) error {
	if !t.HasWordTimings() {
		return xcerr.E(xcerr.CodeValidation,
			"transcript has no word timings — karaoke output needs them (plain SRT still works)", nil)
	}
	if style.FontSize <= 0 {
		style.FontSize = 48
	}
	if style.HighlightColor == "" {
		style.HighlightColor = "&H0000FFFF" // ASS colors are &HAABBGGRR: yellow
	}
	font := style.FontName
	if font == "" {
		font = "sans-serif"
	}

	bw := bufio.NewWriter(w)
	fmt.Fprintf(bw, assHeader, font, style.FontSize, style.HighlightColor)
	for _, s := range t.Segments {
		line, end := karaokeLine(s)
		fmt.Fprintf(bw, "Dialogue: 0,%s,%s,Karaoke,,0,0,0,,%s\n",
			assTime(s.Start), assTime(end), line)
	}
	return bw.Flush()
}

// karaokeLine builds the {\kf} tag stream for one segment. Each word's fill
// runs until the next word starts (gaps belong to the previous word, the
// classic KTV feel); the last word fills to the segment end.
func karaokeLine(s Segment) (string, float64) {
	end := s.End
	var b strings.Builder
	for i, wd := range s.Words {
		next := s.End
		if i+1 < len(s.Words) && s.Words[i+1].Start > wd.Start {
			next = s.Words[i+1].Start
		}
		if next < wd.Start {
			next = wd.Start
		}
		cs := int(math.Round((next - wd.Start) * 100))
		if cs < 0 {
			cs = 0
		}
		fmt.Fprintf(&b, "{\\kf%d}%s", cs, wd.Word)
		if i+1 < len(s.Words) {
			b.WriteString(" ")
		}
	}
	if b.Len() == 0 { // words empty — HasWordTimings guards, belt and braces
		b.WriteString(s.Text)
	}
	return b.String(), end
}

// assHeader is the ASS preamble: one Karaoke style (secondary color white =
// unsung, primary overridden per-style = sung fill).
const assHeader = `[Script Info]
ScriptType: v4.00+
PlayResX: 1280
PlayResY: 720
WrapStyle: 0

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Karaoke,%s,%d,%s,&H00FFFFFF,&H00101010,&H96000000,0,0,0,0,100,100,0,0,1,2,1,2,60,60,40,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
`

// srtTime renders 00:00:00,000 (SubRip).
func srtTime(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	ms := int(math.Round(sec * 1000))
	return fmt.Sprintf("%02d:%02d:%02d,%03d", ms/3600000, (ms/60000)%60, (ms/1000)%60, ms%1000)
}

// assTime renders 0:00:00.00 (ASS centiseconds).
func assTime(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	cs := int(math.Round(sec * 100))
	return fmt.Sprintf("%d:%02d:%02d.%02d", cs/360000, (cs/6000)%60, (cs/100)%60, cs%100)
}

func finite(f float64) bool {
	return !math.IsNaN(f) && !math.IsInf(f, 0)
}
