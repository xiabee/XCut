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
	pos, err := parseCommandArgs(rest, map[string]*string{"style": &styleName, "duration": &durationFlag})
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return xcerr.E(xcerr.CodeValidation,
			"usage: xcut timeline <project> [--style name] [--duration seconds] | xcut timeline <project> --restore-backup", nil)
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
	req := pipeline.TimelineRequest{Style: styleName, Duration: duration}
	tl, err := d.BuildTimeline(p, req)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "style: %s\n", styleName)
	if duration > 0 {
		fmt.Fprintf(a.Stdout, "target duration: %.0fs (overriding the style's own)\n", duration)
	}
	fmt.Fprintf(a.Stdout, "timeline: %d clips, %.1fs total, canvas %dx%d@%.0f\n",
		countTimelineClips(tl), tl.Duration(), tl.Canvas.Width, tl.Canvas.Height, tl.Canvas.FPS)
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
