package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/xiabee/XCut/internal/analysis"
	"github.com/xiabee/XCut/internal/event"
	"github.com/xiabee/XCut/internal/job"
	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/xcerr"
)

func init() {
	register("analyze", "run baseline analyzers over project assets (analyze <project> [assetID...])", cmdAnalyze)
}

func cmdAnalyze(a *App, args []string) error {
	if len(args) < 1 {
		return xcerr.E(xcerr.CodeValidation, "usage: xcut analyze <project> [assetID...]", nil)
	}
	db, err := a.OpenDB()
	if err != nil {
		return err
	}
	defer db.Close()
	ctx := a.Ctx

	p, err := requireProject(db, ctx, args[0])
	if err != nil {
		return err
	}
	assets, err := db.ListAssets(ctx, p.ID)
	if err != nil {
		return err
	}
	if len(args) > 1 {
		want := make(map[string]bool)
		for _, id := range args[1:] {
			want[id] = true
		}
		kept := make([]storage.Asset, 0, len(assets))
		for _, as := range assets {
			if want[as.ID] {
				kept = append(kept, as)
			}
		}
		if len(kept) == 0 {
			return xcerr.E(xcerr.CodeNotFound, "no matching assets in project", nil)
		}
		assets = kept
	}
	if len(assets) == 0 {
		return xcerr.E(xcerr.CodeValidation, "project has no assets (import first)", nil)
	}

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
	q := job.NewQueue(db, a.Cfg.Resource.MaxConcurrentJobs, a.Log)
	opts := analysis.Options{
		Tools:         tools,
		SampleFPS:     a.Cfg.Resource.FrameSampleFPS,
		AnalysisWidth: a.Cfg.Resource.AnalysisWidth,
	}

	failed := false
	for _, as := range assets {
		asset := as
		started := time.Now()
		_, jerr := q.RunInline(ctx, "analyze", p.ID, job.ClassCPUHeavy,
			map[string]any{"asset_id": asset.ID, "path": asset.Path},
			func(jctx context.Context, progress func(float64)) error {
				progress(0.2)
				result, err := analysis.Run(jctx, store, opts, analyzers,
					asset.Path, asset.Fingerprint, asset.DurationSec, asset.HasAudio, a.Log)
				if err != nil {
					return err
				}
				progress(0.8)
				// Duration comes from the asset row (source of truth), not the
				// analysis artifact: analyzers never own media metadata.
				segs, err := event.Build(result.Tracks, asset.DurationSec, event.DefaultConfig())
				if err != nil {
					return err
				}
				progress(1.0)
				fmt.Fprintf(a.Stdout, "analyzed %s [%s]: %d tracks, %d samples, %d events (cache %s)\n",
					asset.Filename, asset.ID, len(result.Tracks), countSamples(result), len(segs),
					cacheVerb(started))
				for i, s := range segs {
					if i >= 8 {
						fmt.Fprintf(a.Stdout, "  … and %d more events\n", len(segs)-8)
						break
					}
					fmt.Fprintf(a.Stdout, "  event %6.2fs–%6.2fs  score %.2f  (motion %.2f, audio %.1f dB)\n",
						s.Start, s.End, s.Score, s.MeanMotion, s.MeanAudioDB)
				}
				return nil
			})
		if jerr != nil {
			failed = true
			fmt.Fprintf(a.Stderr, "analyze failed for %s: %s\n", asset.Filename, xcerr.UserMessage(jerr))
		}
	}
	if failed {
		return xcerr.E(xcerr.CodeInternal, "one or more analyses failed (see above)", nil)
	}
	return nil
}

func countSamples(r *analysis.Result) int {
	n := 0
	for _, t := range r.Tracks {
		n += len(t.Samples)
	}
	return n
}

func cacheVerb(started time.Time) string {
	if time.Since(started) < 30*time.Millisecond {
		return "hit"
	}
	return "computed"
}
