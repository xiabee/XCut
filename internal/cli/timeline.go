package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/xiabee/XCut/internal/analysis"
	"github.com/xiabee/XCut/internal/event"
	"github.com/xiabee/XCut/internal/job"
	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/style"
	"github.com/xiabee/XCut/internal/timeline"
	"github.com/xiabee/XCut/internal/xcerr"
)

func init() {
	register("timeline", "generate a timeline for a project (timeline <project> [--style name])", cmdTimeline)
}

func cmdTimeline(a *App, args []string) error {
	styleName := "generic_highlight"
	pos, err := parseCommandArgs(args, map[string]*string{"style": &styleName})
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return xcerr.E(xcerr.CodeValidation, "usage: xcut timeline <project> [--style name]", nil)
	}
	projectName := pos[0]

	db, err := a.OpenDB()
	if err != nil {
		return err
	}
	defer db.Close()
	ctx := a.Ctx

	p, err := requireProject(db, ctx, projectName)
	if err != nil {
		return err
	}
	assets, err := db.ListAssets(ctx, p.ID)
	if err != nil {
		return err
	}
	if len(assets) == 0 {
		return xcerr.E(xcerr.CodeValidation, "project has no assets (import first)", nil)
	}

	preset, err := style.Load(styleName, filepath.Join(a.Workspace().Root, "styles"))
	if err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "style: %s (%s, %s)\n", preset.Name, preset.Title, preset.Source)

	tools := media.ResolveTools(a.Cfg)
	store := analysis.NewStore(a.Workspace().CacheDir())
	store.MaxBytes = int64(a.Cfg.Resource.MaxCacheGB * (1 << 30))
	analyzers, err := analysis.ResolveAnalyzers(ctx, analysis.WorkerConfig{
		MediaBin: a.Cfg.Workers.MediaBin,
		Audio:    a.Cfg.Workers.Audio,
	}, a.Log)
	if err != nil {
		return err
	}
	opts := analysis.Options{
		Tools:         tools,
		SampleFPS:     a.Cfg.Resource.FrameSampleFPS,
		AnalysisWidth: a.Cfg.Resource.AnalysisWidth,
	}
	q := job.NewQueue(db, a.Cfg.Resource.MaxConcurrentJobs, a.Log)

	started := time.Now()
	var items []style.AssetEvents
	_, jerr := q.RunInline(ctx, "timeline", p.ID, job.ClassCPULight,
		map[string]any{"style": preset.Name}, func(jctx context.Context, progress func(float64)) error {
			for i, as := range assets {
				asset := as
				result, err := analysis.Run(jctx, store, opts, analyzers,
					asset.Path, asset.Fingerprint, asset.DurationSec, asset.HasAudio, a.Log)
				if err != nil {
					return err
				}
				segs, err := event.Build(result.Tracks, asset.DurationSec, preset.EventConfig)
				if err != nil {
					return err
				}
				items = append(items, style.AssetEvents{
					Asset: style.AssetInfo{
						ID:          asset.ID,
						Path:        asset.Path,
						DurationSec: asset.DurationSec,
					},
					Segments: segs,
				})
				progress(float64(i+1) / float64(len(assets)+1))
			}

			tl, err := style.Build(preset, p.ID, items)
			if err != nil {
				return err
			}
			progress(0.9)

			b, err := json.MarshalIndent(tl, "", "  ")
			if err != nil {
				return xcerr.E(xcerr.CodeInternal, "cannot serialize timeline", err)
			}
			outPath, err := a.Workspace().SafeJoin(filepath.Join("projects", p.ID, "timeline.json"))
			if err != nil {
				return err
			}
			if err := atomicWrite(outPath, b); err != nil {
				return err
			}
			progress(1.0)
			fmt.Fprintf(a.Stdout, "timeline: %d clips, %.1fs total, canvas %dx%d@%.0f\n",
				countClips(tl), tl.Duration(), tl.Canvas.Width, tl.Canvas.Height, tl.Canvas.FPS)
			fmt.Fprintf(a.Stdout, "written: %s\n", outPath)
			return nil
		})
	if jerr != nil {
		return jerr
	}
	a.Log.Debug("timeline generated", "duration_ms", time.Since(started).Milliseconds())
	return nil
}

func countClips(tl *timeline.Timeline) int {
	n := 0
	for _, tr := range tl.Tracks {
		n += len(tr.Clips)
	}
	return n
}

func atomicWrite(path string, b []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return xcerr.E(xcerr.CodeInternal, "cannot create output directory", err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-timeline-*")
	if err != nil {
		return xcerr.E(xcerr.CodeInternal, "cannot create temp file", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		_ = os.Remove(tmpName)
		return xcerr.E(xcerr.CodeInternal, "cannot write temp file", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return xcerr.E(xcerr.CodeInternal, "cannot close temp file", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return xcerr.E(xcerr.CodeInternal, "cannot finalize output file", err)
	}
	return nil
}
