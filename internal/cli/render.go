package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/xiabee/XCut/internal/job"
	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/render"
	"github.com/xiabee/XCut/internal/timeline"
	"github.com/xiabee/XCut/internal/xcerr"
)

func init() {
	register("render", "render a project timeline to MP4 (render <project> [--out path])", cmdRender)
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
	ctx := a.Ctx

	p, err := requireProject(db, ctx, pos[0])
	if err != nil {
		return err
	}

	// Load + re-validate the timeline against current DB state.
	tlPath, err := a.Workspace().SafeJoin(filepath.Join("projects", p.ID, "timeline.json"))
	if err != nil {
		return err
	}
	b, err := os.ReadFile(tlPath)
	if err != nil {
		if os.IsNotExist(err) {
			return xcerr.E(xcerr.CodeNotFound, "no timeline for project (run xcut timeline first)", err)
		}
		return xcerr.E(xcerr.CodeInternal, "cannot read timeline", err)
	}
	tl := &timeline.Timeline{}
	if err := json.Unmarshal(b, tl); err != nil {
		return xcerr.E(xcerr.CodeValidation, "corrupt timeline file", err)
	}
	assets, err := db.ListAssets(ctx, p.ID)
	if err != nil {
		return err
	}
	durations := make(map[string]float64, len(assets))
	for _, as := range assets {
		durations[as.ID] = as.DurationSec
	}
	if err := tl.Validate(func(id string) (float64, bool) {
		d, ok := durations[id]
		return d, ok
	}); err != nil {
		return err
	}

	// Default output lives inside the project directory.
	if outPath == "" {
		outPath, err = a.Workspace().SafeJoin(filepath.Join("projects", p.ID, "render.mp4"))
		if err != nil {
			return err
		}
	} else if abs, aerr := filepath.Abs(outPath); aerr == nil {
		outPath = abs
	}

	tools := media.ResolveTools(a.Cfg)
	q := job.NewQueue(db, a.Cfg.Resource.MaxConcurrentJobs, a.Log)

	started := time.Now()
	_, jerr := q.RunInline(ctx, "render", p.ID, job.ClassCPUHeavy,
		map[string]any{"out": outPath, "clips": len(tl.Tracks)},
		func(jctx context.Context, progress func(float64)) error {
			tempDir, err := a.Workspace().NewTempDir("render")
			if err != nil {
				return err
			}
			// Debug policy: temp is removed on success, kept on failure for
			// inspection (cleanable via `xcut cleanup`).
			defer func() {
				if jctx.Err() == nil {
					_ = os.RemoveAll(tempDir)
				}
			}()

			last := 0
			err = render.Render(jctx, tl, render.Options{
				Tools:   tools,
				TempDir: tempDir,
				OnProgress: func(done, total int) {
					if total > 0 {
						p := float64(done) / float64(total)
						if int(p*100) > last {
							last = int(p * 100)
							progress(p)
						}
					}
				},
			}, outPath)
			if err != nil {
				return err
			}
			progress(1.0)

			fi, _ := os.Stat(outPath)
			fmt.Fprintf(a.Stdout, "rendered %s (%.1f MB, %.1fs timeline) in %.1fs\n",
				outPath, float64(fi.Size())/(1<<20), tl.Duration(), time.Since(started).Seconds())
			return nil
		})
	if jerr != nil {
		return jerr
	}
	return nil
}
