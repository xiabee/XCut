// Package pipeline holds XCut's core operations — import, analyze, timeline,
// render — as plain functions shared by the CLI and the HTTP API. Behavior
// (job records, cache, validation) is identical no matter the entry point;
// presentation (printing) stays in the caller.
package pipeline

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/xiabee/XCut/internal/analysis"
	"github.com/xiabee/XCut/internal/config"
	"github.com/xiabee/XCut/internal/event"
	"github.com/xiabee/XCut/internal/job"
	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/render"
	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/style"
	"github.com/xiabee/XCut/internal/timeline"
	"github.com/xiabee/XCut/internal/workspace"
	"github.com/xiabee/XCut/internal/xcerr"
)

// Deps bundles everything pipeline operations need.
type Deps struct {
	Ctx   context.Context
	DB    *storage.DB
	WS    *workspace.Workspace
	Cfg   *config.Config
	Log   *slog.Logger
	Queue *job.Queue
}

// NewDeps builds Deps from an App-like configuration (used by both CLI and API).
func NewDeps(ctx context.Context, db *storage.DB, ws *workspace.Workspace, cfg *config.Config, log *slog.Logger) Deps {
	return Deps{
		Ctx:   ctx,
		DB:    db,
		WS:    ws,
		Cfg:   cfg,
		Log:   log,
		Queue: job.NewQueue(db, cfg.Resource.MaxConcurrentJobs, log),
	}
}

func (d Deps) tools() media.Tools { return media.ResolveTools(d.Cfg) }

func (d Deps) analysisStore() *analysis.Store {
	s := analysis.NewStore(d.WS.CacheDir())
	s.MaxBytes = int64(d.Cfg.Resource.MaxCacheGB * (1 << 30))
	return s
}

func (d Deps) analyzers() ([]analysis.Analyzer, error) {
	return analysis.ResolveAnalyzers(d.Ctx, analysis.WorkerConfig{
		MediaBin: d.Cfg.Workers.MediaBin,
		Audio:    d.Cfg.Workers.Audio,
	}, d.Log)
}

func (d Deps) analysisOpts() analysis.Options {
	return analysis.Options{
		Tools:         d.tools(),
		SampleFPS:     d.Cfg.Resource.FrameSampleFPS,
		AnalysisWidth: d.Cfg.Resource.AnalysisWidth,
	}
}

// AnalyzedAsset is the per-asset payload of AnalyzeProject's callback.
type AnalyzedAsset struct {
	Asset   *storage.Asset
	Result  *analysis.Result
	Segments []event.Segment
}

// ImportAsset probes one media file and upserts it into the project as a
// recorded job. The file may live anywhere on disk (user media is external).
func (d Deps) ImportAsset(project *storage.Project, path string) (*storage.Asset, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, xcerr.E(xcerr.CodeValidation, "cannot resolve path: "+path, err)
	}

	_, jerr := d.Queue.RunInline(d.Ctx, "import", project.ID, job.ClassIOHeavy,
		map[string]any{"path": abs}, func(jctx context.Context, progress func(float64)) error {
			return d.importInto(jctx, project, abs, progress)
		})
	if jerr != nil {
		return nil, jerr
	}
	// Re-read the stored row so callers see the canonical record.
	return d.latestAssetByName(project.ID, filepath.Base(abs))
}

func (d Deps) importInto(ctx context.Context, project *storage.Project, path string, progress func(float64)) error {
	fp, err := media.Fingerprint(path)
	if err != nil {
		return err
	}
	progress(0.3)
	probe, err := media.ProbeFile(ctx, d.tools(), path)
	if err != nil {
		return err
	}
	progress(0.8)
	asset := &storage.Asset{
		ProjectID:   project.ID,
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
	if err := d.DB.UpsertAsset(ctx, asset); err != nil {
		return err
	}
	progress(1.0)
	return nil
}

func (d Deps) latestAssetByName(projectID, filename string) (*storage.Asset, error) {
	assets, err := d.DB.ListAssets(d.Ctx, projectID)
	if err != nil {
		return nil, err
	}
	var latest *storage.Asset
	for i := range assets {
		if assets[i].Filename == filename && (latest == nil || assets[i].CreatedAt >= latest.CreatedAt) {
			latest = &assets[i]
		}
	}
	if latest == nil {
		return nil, xcerr.E(xcerr.CodeNotFound, "asset not found after import", nil)
	}
	return latest, nil
}

// AnalyzeProject runs the analyzer set over every asset of the project as a
// single recorded job. onAsset (optional) fires after each asset.
func (d Deps) AnalyzeProject(project *storage.Project, onAsset func(AnalyzedAsset)) error {
	assets, err := d.DB.ListAssets(d.Ctx, project.ID)
	if err != nil {
		return err
	}
	if len(assets) == 0 {
		return xcerr.E(xcerr.CodeValidation, "project has no assets (import first)", nil)
	}
	analyzers, err := d.analyzers()
	if err != nil {
		return err
	}
	store := d.analysisStore()
	opts := d.analysisOpts()

	_, jerr := d.Queue.RunInline(d.Ctx, "analyze", project.ID, job.ClassCPUHeavy,
		map[string]any{"assets": len(assets)}, func(jctx context.Context, progress func(float64)) error {
			for i := range assets {
				if err := jctx.Err(); err != nil {
					return xcerr.E(xcerr.CodeCancelled, "cancelled", err)
				}
				asset := assets[i]
				result, err := analysis.Run(jctx, store, opts, analyzers,
					asset.Path, asset.Fingerprint, asset.DurationSec, asset.HasAudio, d.Log)
				if err != nil {
					return err
				}
				segs, err := event.Build(result.Tracks, asset.DurationSec, event.DefaultConfig())
				if err != nil {
					return err
				}
				if onAsset != nil {
					onAsset(AnalyzedAsset{Asset: &asset, Result: result, Segments: segs})
				}
				progress(float64(i+1) / float64(len(assets)))
			}
			return nil
		})
	return jerr
}

// BuildTimeline generates the project timeline with the given style and
// writes it to the project directory atomically (recorded job).
func (d Deps) BuildTimeline(project *storage.Project, styleName string) (*timeline.Timeline, error) {
	assets, err := d.DB.ListAssets(d.Ctx, project.ID)
	if err != nil {
		return nil, err
	}
	if len(assets) == 0 {
		return nil, xcerr.E(xcerr.CodeValidation, "project has no assets (import first)", nil)
	}
	preset, err := style.Load(styleName, filepath.Join(d.WS.Root, "styles"))
	if err != nil {
		return nil, err
	}
	analyzers, err := d.analyzers()
	if err != nil {
		return nil, err
	}
	store := d.analysisStore()
	opts := d.analysisOpts()

	var result *timeline.Timeline
	_, jerr := d.Queue.RunInline(d.Ctx, "timeline", project.ID, job.ClassCPULight,
		map[string]any{"style": preset.Name}, func(jctx context.Context, progress func(float64)) error {
			var items []style.AssetEvents
			for i := range assets {
				asset := assets[i]
				res, err := analysis.Run(jctx, store, opts, analyzers,
					asset.Path, asset.Fingerprint, asset.DurationSec, asset.HasAudio, d.Log)
				if err != nil {
					return err
				}
				segs, err := event.Build(res.Tracks, asset.DurationSec, preset.EventConfig)
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
			tl, err := style.Build(preset, project.ID, items)
			if err != nil {
				return err
			}
			b, err := json.MarshalIndent(tl, "", "  ")
			if err != nil {
				return xcerr.E(xcerr.CodeInternal, "cannot serialize timeline", err)
			}
			outPath, err := d.WS.SafeJoin(filepath.Join("projects", project.ID, "timeline.json"))
			if err != nil {
				return err
			}
			if err := WriteAtomic(outPath, b); err != nil {
				return err
			}
			progress(1.0)
			result = tl
			return nil
		})
	if jerr != nil {
		return nil, jerr
	}
	return result, nil
}

// TimelinePath is where a project's timeline document lives.
func (d Deps) TimelinePath(projectID string) (string, error) {
	return d.WS.SafeJoin(filepath.Join("projects", projectID, "timeline.json"))
}

// DefaultRenderPath is the default render output for a project.
func (d Deps) DefaultRenderPath(projectID string) (string, error) {
	return d.WS.SafeJoin(filepath.Join("projects", projectID, "render.mp4"))
}

// RenderProject renders the project's stored timeline to outPath (recorded
// job; temp scratch removed on success, kept on failure for debugging).
func (d Deps) RenderProject(project *storage.Project, outPath string, onProgress func(pct int)) error {
	tlPath, err := d.TimelinePath(project.ID)
	if err != nil {
		return err
	}
	tl, err := timeline.LoadFile(tlPath)
	if err != nil {
		if xcerr.IsCode(err, xcerr.CodeNotFound) {
			return xcerr.E(xcerr.CodeNotFound, "no timeline for project (generate one first)", err)
		}
		return err
	}
	assets, err := d.DB.ListAssets(d.Ctx, project.ID)
	if err != nil {
		return err
	}
	durations := make(map[string]float64, len(assets))
	for i := range assets {
		durations[assets[i].ID] = assets[i].DurationSec
	}
	if err := tl.Validate(func(id string) (float64, bool) {
		v, ok := durations[id]
		return v, ok
	}); err != nil {
		return err
	}

	started := time.Now()
	_, jerr := d.Queue.RunInline(d.Ctx, "render", project.ID, job.ClassCPUHeavy,
		map[string]any{"out": outPath, "clips": countClips(tl)}, func(jctx context.Context, progress func(float64)) error {
			tempDir, err := d.WS.NewTempDir("render")
			if err != nil {
				return err
			}
			defer func() {
				if jctx.Err() == nil {
					_ = os.RemoveAll(tempDir)
				}
			}()

			last := 0
			err = render.Render(jctx, tl, render.Options{
				Tools:   d.tools(),
				TempDir: tempDir,
				OnProgress: func(done, total int) {
					if total > 0 && onProgress != nil {
						pct := done * 100 / total
						if pct > last {
							last = pct
							onProgress(pct)
							progress(float64(pct) / 100)
						}
					}
				},
			}, outPath)
			if err != nil {
				return err
			}
			progress(1.0)
			fi, _ := os.Stat(outPath)
			d.Log.Debug("render done", "out", outPath, "bytes", fileSize(fi), "duration_ms", time.Since(started).Milliseconds())
			return nil
		})
	return jerr
}

func countClips(tl *timeline.Timeline) int {
	n := 0
	for _, tr := range tl.Tracks {
		n += len(tr.Clips)
	}
	return n
}

func fileSize(fi os.FileInfo) int64 {
	if fi == nil {
		return 0
	}
	return fi.Size()
}

// WriteAtomic writes b to path via temp file + rename (crash safety).
func WriteAtomic(path string, b []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return xcerr.E(xcerr.CodeInternal, "cannot create output directory", err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
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
