package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/xiabee/XCut/internal/job"
	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/xcerr"
)

func init() {
	register("import", "probe media files into a project (import <project> <file...>)", cmdImport)
}

func cmdImport(a *App, args []string) error {
	if len(args) < 2 {
		return xcerr.E(xcerr.CodeValidation, "usage: xcut import <project> <file...>", nil)
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

	tools := media.ResolveTools(a.Cfg)
	q := job.NewQueue(db, a.Cfg.Resource.MaxConcurrentJobs, a.Log)

	failed := false
	for _, file := range args[1:] {
		path, err := filepath.Abs(file)
		if err != nil {
			return xcerr.E(xcerr.CodeValidation, "cannot resolve path: "+file, err)
		}

		started := time.Now()
		_, jerr := q.RunInline(ctx, "import", p.ID, job.ClassIOHeavy,
			map[string]any{"path": path},
			func(jctx context.Context, progress func(float64)) error {
				progress(0.1)
				fp, err := media.Fingerprint(path)
				if err != nil {
					return err
				}
				progress(0.3)
				probe, err := media.ProbeFile(jctx, tools, path)
				if err != nil {
					return err
				}
				progress(0.8)
				asset := &storage.Asset{
					ProjectID:   p.ID,
					Path:        path,
					Filename:    filepath.Base(path),
					Fingerprint: fp,
					DurationSec: probe.DurationSec,
					Width:       probe.Width,
					Height:      probe.Height,
					FPS:         probe.FPS,
					VideoCodec:  probe.VideoCodec,
					AudioCodec:  probe.AudioCodec,
					HasAudio:    probe.HasAudio,
					Bitrate:     probe.Bitrate,
					SizeBytes:   probe.SizeBytes,
					ProbeJSON:   string(probe.Raw),
				}
				if err := db.UpsertAsset(jctx, asset); err != nil {
					return err
				}
				progress(1.0)
				fmt.Fprintf(a.Stdout, "imported %s -> [%s] %s %dx%d %.2ffps %s%s\n",
					filepath.Base(path), asset.ID, media.HumanDuration(asset.DurationSec),
					asset.Width, asset.Height, asset.FPS, asset.VideoCodec,
					audioSuffix(asset.HasAudio, asset.AudioCodec))
				return nil
			})
		if jerr != nil {
			failed = true
			fmt.Fprintf(a.Stderr, "import failed for %s: %s\n", filepath.Base(path), xcerr.UserMessage(jerr))
			continue
		}
		a.Log.Debug("import done", "path", path, "duration_ms", time.Since(started).Milliseconds())
	}
	if failed {
		return xcerr.E(xcerr.CodeInternal, "one or more imports failed (see above)", nil)
	}
	return nil
}
