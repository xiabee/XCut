package cli

import (
	"fmt"

	"github.com/xiabee/XCut/internal/timeline"
	"github.com/xiabee/XCut/internal/xcerr"
)

func init() {
	register("timeline", "generate a timeline for a project", usageSyntax("xcut timeline <project> [--style name] | xcut timeline <project> --restore-backup"), cmdTimeline)
}

func cmdTimeline(a *App, args []string) error {
	styleName := "generic_highlight"
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
	pos, err := parseCommandArgs(rest, map[string]*string{"style": &styleName})
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return xcerr.E(xcerr.CodeValidation,
			"usage: xcut timeline <project> [--style name] | xcut timeline <project> --restore-backup", nil)
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

	tl, err := d.BuildTimeline(p, styleName)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "style: %s\n", styleName)
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
