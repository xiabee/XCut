package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xiabee/XCut/internal/xcerr"
)

func init() {
	register("render", "render a project timeline to MP4", usageSyntax("xcut render <project> [--out path] [--subs file|auto]"), cmdRender)
}

func cmdRender(a *App, args []string) error {
	outPath := ""
	subsPath := ""
	pos, err := parseCommandArgs(args, map[string]*string{"out": &outPath, "subs": &subsPath})
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return xcerr.E(xcerr.CodeValidation, "usage: xcut render <project> [--out path] [--subs file|auto]", nil)
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

	if outPath == "" {
		outPath, err = d.DefaultRenderPath(p.ID)
		if err != nil {
			return err
		}
	} else if abs, aerr := filepath.Abs(outPath); aerr == nil {
		outPath = abs
	}
	subsNote := ""
	if strings.EqualFold(subsPath, "auto") {
		// "auto" is the render asking the same question the export tap answers
		// internally: what should burn for this reel, after anything that can be
		// fixed from what is on disk is fixed. A named file stays a named file — it
		// burns as it stands, because the caller pointed at it.
		subsPath, subsNote, err = d.ReelSubtitles(p.ID)
		if err != nil {
			return err
		}
		if subsPath == "" {
			return xcerr.E(xcerr.CodeNotFound,
				"this project has no subtitles to burn (transcribe them, or pass --subs <file>)", nil)
		}
	} else if subsPath != "" {
		if abs, aerr := filepath.Abs(subsPath); aerr == nil {
			subsPath = abs
		}
		if _, serr := os.Stat(subsPath); serr != nil {
			return xcerr.E(xcerr.CodeNotFound, "subtitle file does not exist", serr)
		}
	}
	if subsNote != "" {
		fmt.Fprintf(a.Stdout, "subtitles: %s\n", subsNote)
	}

	started := time.Now()
	err = d.RenderProject(p, outPath, subsPath, func(pct int) {})
	if err != nil {
		return err
	}
	fi, _ := os.Stat(outPath)
	size := int64(0)
	if fi != nil {
		size = fi.Size()
	}
	fmt.Fprintf(a.Stdout, "rendered %s (%.1f MB) in %.1fs\n",
		outPath, float64(size)/(1<<20), time.Since(started).Seconds())
	return nil
}
