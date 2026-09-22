package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/xiabee/XCut/internal/pipeline"

	"github.com/xiabee/XCut/internal/timeline"
	"github.com/xiabee/XCut/internal/xcerr"
)

func init() {
	register("timeline", "generate a timeline for a project", usageSyntax("xcut timeline <project> [--style name] [--duration seconds] | xcut timeline <project> --restore-backup"), cmdTimeline)
}

func cmdTimeline(a *App, args []string) error {
	styleName := "generic_highlight"
	durationFlag := ""
	beatFlag := ""
	musicFlag := "" // a track to lay under the reel and cut to
	restore := false
	// --restore-backup is a boolean-style flag; pull it out before
	// parseCommandArgs (which requires values for its flags).
	rest := args[:0]
	for _, arg := range args {
		if arg == "--restore-backup" {
			restore = true
			continue
		}
		rest = append(rest, arg)
	}
	pos, err := parseCommandArgs(rest, map[string]*string{
		"style": &styleName, "duration": &durationFlag, "beat-snap": &beatFlag, "music": &musicFlag})
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return xcerr.E(xcerr.CodeValidation,
			"usage: xcut timeline <project> [--style name] [--duration seconds] [--beat-snap seconds|off] [--music file] | xcut timeline <project> --restore-backup", nil)
	}

	db, err := a.OpenDB()
	if err != nil {
		return err
	}
	defer db.Close()

	p, err := requireProject(db, a.Ctx, pos[0])
	if err != nil {
		return err
	}
	d := a.Pipeline(db)

	if restore {
		ok, err := d.RestoreTimelineBackup(p)
		if err != nil {
			return err
		}
		if !ok {
			return xcerr.E(xcerr.CodeNotFound, "no timeline backup for this project (nothing to restore)", nil)
		}
		fmt.Fprintf(a.Stdout, "restored timeline backup for %s (the previous document is now the backup)\n", p.Name)
		return nil
	}

	duration, err := parseDurationFlag(durationFlag)
	if err != nil {
		return err
	}
	beatSnap, err := parseBeatSnapFlag(beatFlag)
	if err != nil {
		return err
	}
	req := pipeline.TimelineRequest{Style: styleName, Duration: duration, BeatSnap: beatSnap, Music: musicFlag}
	tl, err := d.BuildTimeline(p, req)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "style: %s\n", styleName)
	if duration > 0 {
		fmt.Fprintf(a.Stdout, "target duration: %.0fs (overriding the style's own)\n", duration)
	}
	switch {
	case beatSnap == pipeline.BeatSnapOff:
		fmt.Fprintln(a.Stdout, "beat snap: off (overriding the style's own)")
	case beatSnap > 0:
		fmt.Fprintf(a.Stdout, "beat snap: ±%gs (overriding the style's own)\n", beatSnap)
	}
	if musicFlag != "" {
		// The path is the user's own argument echoed back; the bed's grid and the
		// snap it implies are reported by the lines above and the clip metadata.
		fmt.Fprintf(a.Stdout, "music bed: %s\n", musicFlag)
	}
	fmt.Fprintf(a.Stdout, "timeline: %d clips, %.1fs total, canvas %dx%d@%.0f\n",
		countTimelineClips(tl), tl.Duration(), tl.Canvas.Width, tl.Canvas.Height, tl.Canvas.FPS)
	if line := pacingLine(tl); line != "" {
		fmt.Fprintln(a.Stdout, line)
	}
	if snapped := snappedClipCount(tl); snapped > 0 {
		fmt.Fprintf(a.Stdout, "cuts on the beat: %d of %d clips\n", snapped, countTimelineClips(tl))
	}
	if note := footageLimitNote(tl, req.Duration); note != "" {
		fmt.Fprintln(a.Stdout, note)
	}
	outPath, err := d.TimelinePath(p.ID)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "written: %s\n", outPath)
	return nil
}

func countTimelineClips(tl *timeline.Timeline) int {
	n := 0
	for _, tr := range tl.Tracks {
		n += len(tr.Clips)
	}
	return n
}

// parseDurationFlag reads --duration: empty (or an explicit 0) means "use
// the style's own target", and anything else must be a sane number of seconds. The bound is
// the same one pipeline enforces, so the CLI never accepts a value the
// pipeline will refuse.
func parseDurationFlag(v string) (float64, error) {
	if strings.TrimSpace(v) == "" {
		return 0, nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || (f != 0 && (f < 1 || f > pipeline.MaxRequestDuration)) {
		return 0, xcerr.E(xcerr.CodeValidation,
			"--duration must be between 1 and "+strconv.Itoa(pipeline.MaxRequestDuration)+
				" seconds (got "+v+")", nil)
	}
	return f, nil
}

// parseBeatSnapFlag reads --beat-snap: "" keeps the style's own tolerance,
// "off" forces snapping off, and a number in (0, 0.5] sets it for this run.
func parseBeatSnapFlag(v string) (float64, error) {
	switch strings.TrimSpace(v) {
	case "":
		return 0, nil
	case "off", "none":
		return pipeline.BeatSnapOff, nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || !(f > 0 && f <= 0.5) {
		return 0, xcerr.E(xcerr.CodeValidation,
			"--beat-snap must be off, or a tolerance in (0, 0.5] seconds (got "+v+")", nil)
	}
	return f, nil
}

// snappedClipCount counts the clips whose end was moved onto a beat. The CLI
// prints it because a flag line alone would read as "it snapped" for a run where
// the audio carried no grid — which is a different answer with a different fix.
func snappedClipCount(tl *timeline.Timeline) int {
	if tl == nil {
		return 0
	}
	n := 0
	for _, tr := range tl.Tracks {
		for _, c := range tr.Clips {
			if _, ok := c.Metadata["beat"]; ok {
				n++
			}
		}
	}
	return n
}

// pacingLine renders the shape of the reel the style actually produced: selection
// metrics score a 15 s stretch and five 3 s cuts identically, so without this line
// "the new style has the same F1" would read as "the new style is the same edit".
// The shortest shot is left out on purpose — a preset's floor guarantees it, so
// printing it would measure the constraint rather than the cut.
func pacingLine(tl *timeline.Timeline) string {
	p := tl.Pacing()
	if p.Shots == 0 {
		return ""
	}
	line := fmt.Sprintf("pacing: %d shots, mean %.1fs, median %.1fs, longest %.1fs",
		p.Shots, p.MeanSeconds, p.MedianSeconds, p.LongestSeconds)
	if p.ScoredShots > 0 {
		// Where the style's own best moment lands in the reel. Short-form practice
		// puts the decision point inside the first seconds; this says what this cut
		// did about it, in either direction.
		line += fmt.Sprintf(", top shot starts at %.1fs", p.HookSeconds)
	}
	return line
}

// footageLimitNote explains a reel that came out shorter than asked when the
// reason is the footage, not the budget: the selector ran out of candidate
// events while seconds were still allotted. Silence here reads as "it chose not
// to fill the reel", which is a different problem with a different fix.
func footageLimitNote(tl *timeline.Timeline, asked float64) string {
	if tl == nil || asked <= 0 {
		return ""
	}
	if tl.Metadata["candidate_limit"] != "true" {
		return ""
	}
	short := asked - tl.Duration()
	if short < 1.0 {
		return ""
	}
	return fmt.Sprintf("  note: this footage offered %s candidate rallies and the cut took %d of them — %.1fs of the %.0fs asked for. Filling the rest needs more sources: the selector has worked through every candidate it found.",
		tl.Metadata["candidate_events"], countTimelineClips(tl), tl.Duration(), asked)
}
