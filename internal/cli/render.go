package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/xiabee/XCut/internal/xcerr"
)

func init() {
	register("render", "render a project timeline to MP4", usageSyntax("xcut render <project> [--out path]"), cmdRender)
}

func cmdRender(a *App, args []string) error {
	outPath := ""
	pos, err := parseCommandArgs(args, map[string]*string{"out": &outPath})
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return xcerr.E(xcerr.CodeValidation, "usage: xcut render <project> [--out path]", nil)
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

	started := time.Now()
	err = d.RenderProject(p, outPath, func(pct int) {})
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
