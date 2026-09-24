// Package subs turns sidecar speech transcripts into subtitle files: plain
// SRT for standard subtitles, and styled ASS — karaoke (word-level \kf fills)
// when the transcript times the words, captions when it only times the lines —
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
	"strconv"
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
		// Collapse all whitespace runs (inner newlines included) to single
		// spaces: SRT separates CUES with a blank line, so a segment text
		// carrying "\n\n" would end its cue early and have the rest parsed
		// as a phantom headerless cue. ASS already escapes newlines.
		s.Text = strings.Join(strings.Fields(s.Text), " ")
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

// KaraokeStyle carries the ASS style knobs. Width and Height are the reel's canvas:
// a style's pixel metrics are proportions of the frame, so a caption laid out for a
// 16:9 reel has to be re-expressed for a 9:16 one rather than left against whatever
// reference box the file happens to name. Unset means the shipped 1280×720 reference
// — which is what every subtitle file written before these fields produced, so
// nothing changes for a caller that knows nothing about them.
type KaraokeStyle struct {
	FontName string `json:"font_name,omitempty"` // default: libass fallback
	FontSize int    `json:"font_size,omitempty"` // default: 48 against a 720-tall frame
	// HighlightColor is the fill for sung text (ASS &HAABBGGRR); default
	// yellow. Unsung text stays white.
	HighlightColor string `json:"highlight_color,omitempty"`
	Width          int    `json:"width,omitempty"`
	Height         int    `json:"height,omitempty"`
}

// assFrame is the style resolved against a canvas. The starting values are the ones
// this package has always shipped — a 48-point face on a 720-tall frame, a 2-pixel
// outline, 60 and 40 pixel margins — each scaled by the axis it is measured along:
// font, stroke and side margins by the short side (they are about glyph size, and a
// vertical reel's glyphs are as tall as a horizontal one's), the bottom margin by the
// height (it is an offset from the bottom edge of this frame, whatever shape it has).
type assFrame struct {
	playResX, playResY        int
	fontSize                  int
	outline, shadow           int
	marginL, marginR, marginV int
}

func (s KaraokeStyle) frame() assFrame {
	w, h := s.Width, s.Height
	if w <= 0 || h <= 0 {
		w, h = 1280, 720
	}
	short := math.Min(float64(w), float64(h))
	scale := short / 720
	f := assFrame{
		playResX: w,
		playResY: h,
		fontSize: s.FontSize,
		outline:  int(math.Round(2 * scale)),
		shadow:   int(math.Round(1 * scale)),
		marginL:  int(math.Round(60 * scale)),
		marginR:  int(math.Round(60 * scale)),
		marginV:  int(math.Round(40 * float64(h) / 720)),
	}
	if f.fontSize <= 0 {
		f.fontSize = int(math.Round(48 * scale))
	}
	// libass draws no stroke at zero, and an outline is what keeps white text
	// readable over a bright frame: the thin end of the scale still gets one.
	if f.outline < 1 {
		f.outline = 1
	}
	return f
}

// ReadASSFrame reports the reference frame an ASS script declares: the
// PlayResX/PlayResY pair libass scales every pixel field of the script by. It is
// the reader's half of KaraokeStyle.frame — a script laid out for a 1280×720
// frame burns at the size and position meant for a shape the viewer never saw,
// so anything that wants to know whether this file belongs to *its* reel has to
// ask the file.
//
// ok=false means the script makes no claim: no [Script Info] section, a missing
// or unparsable value, or a format like SRT that has no reference frame at all.
// "No claim" is not "the shipped default" — a file that never wrote 1280×720
// must not be reported as having declared it.
func ReadASSFrame(r io.Reader) (w, h int, ok bool) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), 1<<20) // a dialogue line can outgrow the default
	section := ""
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			if section == "Script Info" {
				break // past the only section that can carry the pair
			}
			section = strings.Trim(line, "[]")
			continue
		}
		if section != "Script Info" {
			continue
		}
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || n <= 0 {
			continue
		}
		switch strings.TrimSpace(key) {
		case "PlayResX":
			w = n
		case "PlayResY":
			h = n
		}
	}
	return w, h, w > 0 && h > 0
}

// WriteKaraokeASS renders the transcript as ASS with word-level \kf sweeps
// (the KTV sing-along fill). Requires word timings on every segment.
func WriteKaraokeASS(t *Transcript, style KaraokeStyle, w io.Writer) error {
	if !t.HasWordTimings() {
		return xcerr.E(xcerr.CodeValidation,
			"transcript has no word timings — karaoke output needs them (plain SRT still works)", nil)
	}
	if style.HighlightColor == "" {
		style.HighlightColor = "&H0000FFFF" // ASS colors are &HAABBGGRR: yellow
	}
	font := style.FontName
	if font == "" {
		font = "sans-serif"
	}
	f := style.frame()

	bw := bufio.NewWriter(w)
	// Karaoke: the primary colour is the sung fill, the secondary is what unsung
	// text is drawn in — white.
	fmt.Fprintf(bw, assHeader, f.playResX, f.playResY, "Karaoke", font, f.fontSize, style.HighlightColor, "&H00FFFFFF",
		f.outline, f.shadow, f.marginL, f.marginR, f.marginV)
	cues := layoutTranscript(t, f)
	for _, c := range cues {
		fmt.Fprintf(bw, "Dialogue: 0,%s,%s,Karaoke,,0,0,0,,%s\n",
			assTime(c.start), assTime(c.end), karaokeCue(c))
	}
	return bw.Flush()
}

// lineRunes is how many characters fit on one caption line of this frame: the width
// between the side margins, divided by the em. A CJK glyph is about one em wide and a
// Latin one less, so this over-wraps English slightly and never under-wraps Chinese —
// the failure worth avoiding is text running off the frame, and a line that is a
// little short is only ever that. Clamped because a frame narrow enough to fit three
// characters is a sign, not a caption, and a very wide one should not run a full
// sentence on one line either.
func (f assFrame) lineRunes() int {
	usable := float64(f.playResX - 2*f.marginL)
	n := int(math.Floor(usable / float64(f.fontSize)))
	switch {
	case n < 4:
		return 4
	case n > 42:
		return 42
	}
	return n
}

// layoutTranscript wraps and holds the whole transcript at once: a cue may only
// borrow the silence up to the next one, and after wrapping a long segment there may
// be no gap at all where the speech had one.
func layoutTranscript(t *Transcript, f assFrame) []laidCue {
	var cues []laidCue
	perLine := f.lineRunes()
	for _, s := range t.Segments {
		cues = append(cues, layoutCues(s, perLine)...)
	}
	holdCues(cues)
	return cues
}

// layoutTextCues lays out a segment that has no word timings, where the unit of
// layout is the character rather than the word. The budget, the lines-per-cue cap and
// the time tiling are the same functions the timed path uses — only the unit differs,
// because nothing told us where one word ends and the next begins.
func layoutTextCues(s Segment, perLine int) []laidCue {
	r := []rune(s.Text)
	if len(r) == 0 {
		return nil
	}
	if perLine < 1 {
		perLine = 1
	}
	var lines [][]Word
	for i := 0; i < len(r); {
		end := i + perLine
		if end > len(r) {
			end = len(r)
		}
		// Break at the last space inside the budget when the text has one: a space is
		// the sidecar telling us where a word ends, and cutting through it puts
		// "o" on one line and "f" on the next. With no space in reach — CJK, or a word
		// wider than the frame — the character rule stands, because dropping the rest
		// of a long word to keep the line tidy would lose text nobody asked to lose.
		if end < len(r) {
			for j := end - 1; j > i; j-- {
				if r[j] == ' ' {
					end = j
					break
				}
			}
		}
		// One Word per line, not per character: the plain renderer prints the
		// text and the timings only ever name the cue's window.
		lines = append(lines, []Word{{Start: 0, End: 0, Word: string(r[i:end])}})
		i = end
		// The space that was chosen as the break is not printed on either line.
		for i < len(r) && r[i] == ' ' {
			i++
		}
	}
	var cues []laidCue
	// share is each cue's character count: the span is divided by it, because with
	// no word timings nothing here can claim one line was spoken faster than another.
	var share []int
	total := 0
	for i := 0; i < len(lines); i += maxLinesPerCue {
		last := i + maxLinesPerCue
		if last > len(lines) {
			last = len(lines)
		}
		group := lines[i:last]
		n := 0
		for _, l := range group {
			n += len([]rune(l[0].Word))
		}
		cues = append(cues, laidCue{lines: group})
		share = append(share, n)
		total += n
	}
	cursor := s.Start
	span := s.End - s.Start
	acc := 0
	for i := range cues {
		finish := s.End
		if i+1 < len(cues) && total > 0 {
			// Cumulative, not per cue: each share is of the whole span, so the
			// boundary is where this much of the text ends — a per-cue addition
			// from the segment start would hand every cue the same finish.
			acc += share[i]
			finish = s.Start + span*float64(acc)/float64(total)
		}
		cues[i].start, cues[i].end, cues[i].speechEnd = cursor, finish, finish
		cursor = finish
	}
	return cues
}

// captionLine is one laid-out cue with no karaoke: words joined by the space the
// renderer expects, lines joined by the break libass understands, every character
// escaped because sidecar text is not trusted.
func captionLine(c laidCue) string {
	var b strings.Builder
	for li, line := range c.lines {
		if li > 0 {
			b.WriteString("\\N")
		}
		for i, wd := range line {
			if i > 0 {
				b.WriteString(" ")
			}
			b.WriteString(assEscape(wd.Word))
		}
	}
	return b.String()
}

// WriteCaptionASS renders a transcript as ASS with a plain Caption style: the same
// frame-derived metrics, the same wrapping and dwell, and no {\kf} fill. A transcript
// without word timings used to get an SRT and nothing else, so the burn styled it
// with libass defaults while the karaoke path got a designed frame — same words, two
// different captions, depending on whether the sidecar happened to know when each
// syllable fell.
func WriteCaptionASS(t *Transcript, style KaraokeStyle, w io.Writer) error {
	if len(t.Segments) == 0 {
		return xcerr.E(xcerr.CodeValidation, "transcript has no segments to write", nil)
	}
	font := style.FontName
	if font == "" {
		font = "sans-serif"
	}
	f := style.frame()
	bw := bufio.NewWriter(w)
	// Both colours white: there is no sung/unsung distinction without word timings,
	// and a leftover secondary would invite one.
	fmt.Fprintf(bw, assHeader, f.playResX, f.playResY, "Caption", font, f.fontSize, "&H00FFFFFF", "&H00FFFFFF",
		f.outline, f.shadow, f.marginL, f.marginR, f.marginV)
	for _, c := range layoutTranscript(t, f) {
		fmt.Fprintf(bw, "Dialogue: 0,%s,%s,Caption,,0,0,0,,%s\n",
			assTime(c.start), assTime(c.end), captionLine(c))
	}
	return bw.Flush()
}

// maxLinesPerCue and minCueDwell are the other half of the convention: two lines on
// screen at a time, and a cue held long enough to be read even when the speech that
// produced it was very short. The hold borrows silence after the words, never the
// next cue's time.
const (
	maxLinesPerCue = 2
	minCueDwell    = 1.2
)

// laidCue is one caption on the screen: the display lines it shows — each a run of
// words, because words carry the timings the karaoke sweeps are built from — and the
// window it shows them in. A segment longer than maxLinesPerCue lines becomes several
// cues, because the alternative (one cue running past the frame) is the thing being
// fixed.
type laidCue struct {
	lines      [][]Word
	start, end float64
	// speechEnd is where the words stop; `end` is where the caption leaves the
	// screen. They differ by the dwell hold, and the karaoke fill must follow the
	// words: stretching a cue to keep it readable should not stretch its last
	// character's sweep too, or the highlight finishes after the singing did.
	speechEnd float64
}

func (c laidCue) firstWord() (Word, bool) {
	for _, l := range c.lines {
		if len(l) > 0 {
			return l[0], true
		}
	}
	return Word{}, false
}

func (c laidCue) lastWord() (Word, bool) {
	for i := len(c.lines) - 1; i >= 0; i-- {
		if len(c.lines[i]) > 0 {
			return c.lines[i][len(c.lines[i])-1], true
		}
	}
	return Word{}, false
}

// layoutCues wraps one segment's words into lines that fit the frame and groups those
// lines into cues. A word wider than a line stays on it alone: breaking it would put
// characters on screen at times the sidecar never claimed they were spoken.
func layoutCues(s Segment, perLine int) []laidCue {
	if len(s.Words) == 0 {
		return layoutTextCues(s, perLine)
	}
	var lines [][]Word
	var line []Word
	width := 0
	for _, w := range s.Words {
		n := len([]rune(w.Word)) + 1 // the space the renderer puts between words
		if len(line) > 0 && width+n > perLine {
			lines = append(lines, line)
			line, width = nil, 0
		}
		line = append(line, w)
		width += n
	}
	if len(line) > 0 {
		lines = append(lines, line)
	}
	var cues []laidCue
	for i := 0; i < len(lines); i += maxLinesPerCue {
		last := i + maxLinesPerCue
		if last > len(lines) {
			last = len(lines)
		}
		c := laidCue{lines: lines[i:last]}
		if w, ok := c.firstWord(); ok {
			// The first cue starts where the segment does, not where its first word
			// does: the gap between the two is real (the sidecar heard the line a
			// moment after it began) and the leading {\k} span below is what keeps
			// each word's fill landing on the word it belongs to.
			c.start = s.Start
			if i > 0 {
				c.start = w.Start
			}
		}
		if w, ok := c.lastWord(); ok {
			c.end = w.End
		}
		cues = append(cues, c)
	}
	if len(cues) == 0 {
		return nil
	}
	// The segment's own tail belongs to its last cue — a word's fill runs to the end
	// of what it is part of, which is what this did before any layout existed — and
	// successive cues meet at the next cue's start, so one line of speech does not
	// leave a hole or double-book a moment on screen.
	cues[len(cues)-1].end = s.End
	for i := 0; i+1 < len(cues); i++ {
		cues[i].end = cues[i+1].start
	}
	for i := range cues {
		cues[i].speechEnd = cues[i].end
	}
	return cues
}

// holdCues lengthens any cue that would flash by, borrowing the silence until the next
// cue starts. The last cue is left alone: nothing here knows how long the media is,
// and a hold that outruns the picture is worse than a short caption.
func holdCues(cues []laidCue) {
	for i := range cues {
		if cues[i].end-cues[i].start >= minCueDwell {
			continue
		}
		want := cues[i].start + minCueDwell
		if i+1 < len(cues) && want > cues[i+1].start {
			want = cues[i+1].start
		} else if i+1 == len(cues) {
			continue
		}
		if want > cues[i].end {
			cues[i].end = want // the hold is display time, not singing time
		}
	}
}

// karaokeCue builds the {\kf} tag stream for one cue: each word fills until the next
// word starts (gaps belong to the previous word, the classic KTV feel) and the last
// fills to the cue's end; the cue's lines are joined by the break libass understands.
//
// ASS sweeps are cumulative from the Dialogue start, which is the cue's start, while
// whisper-style word timestamps typically begin slightly after it — without a leading
// offset every sweep fires early by that gap. A zero-width {\k} span absorbs it.
func karaokeCue(c laidCue) string {
	var b strings.Builder
	lead := ""
	if w, ok := c.firstWord(); ok {
		if n := int(math.Round((w.Start - c.start) * 100)); n > 0 {
			lead = fmt.Sprintf("{\\k%d}", n)
		}
	}
	for li, line := range c.lines {
		if li > 0 {
			b.WriteString(`\N`)
		}
		b.WriteString(lead)
		lead = ""
		for i, wd := range line {
			// The sweep runs to the next word *anywhere in the cue*, not to the next
			// word on this line: a line's last character is sung at its own moment,
			// and letting it fill to the cue's end would have the first line finish
			// its highlight long after the singer did. And the cue's end for this
			// purpose is where the words stop, not where the caption leaves — the
			// dwell hold belongs to reading, not to singing.
			next := c.speechEnd
			if next <= 0 {
				next = c.end
			}
			if li+1 < len(c.lines) && i+1 == len(line) {
				next = c.lines[li+1][0].Start
			} else if i+1 < len(line) && line[i+1].Start > wd.Start {
				next = line[i+1].Start
			}
			if next < wd.Start {
				next = wd.Start
			}
			cs := int(math.Round((next - wd.Start) * 100))
			if cs < 0 {
				cs = 0
			}
			if i > 0 {
				b.WriteString(" ")
			}
			fmt.Fprintf(&b, "{\\kf%d}%s", cs, assEscape(wd.Word))
		}
	}
	return b.String()
}

// assEscape neutralizes ASS control characters in sidecar-produced text.
// Sidecar output is untrusted: `{`/`}` inject live override tags (corrupting
// the karaoke fill and colors), a backslash starts a tag, and a newline
// would split the Dialogue event and corrupt the whole [Events] section.
// The replacements are cosmetic (lyrics never legitimately contain them).
func assEscape(s string) string {
	r := strings.NewReplacer(
		"{", "(",
		"}", ")",
		"\\", "/",
		"\r", " ",
		"\n", " ",
	)
	return r.Replace(s)
}

// assHeader is the ASS preamble: one Karaoke style (secondary color white =
// unsung, primary overridden per-style = sung fill). Its pixel fields arrive
// resolved against the reel's canvas by assFrame, not as constants, because
// libass scales the whole script by PlayRes: name a 1280×720 box and render onto
// a 1080×1920 reel and the caption is sized and placed for a frame the viewer
// never sees.
const assHeader = `[Script Info]
ScriptType: v4.00+
PlayResX: %d
PlayResY: %d
WrapStyle: 0

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: %s,%s,%d,%s,%s,&H00101010,&H96000000,0,0,0,0,100,100,0,0,1,%d,%d,2,%d,%d,%d,1

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
