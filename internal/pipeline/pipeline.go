// Package pipeline holds XCut's core operations — import, analyze, timeline,
// render — as plain functions shared by the CLI and the HTTP API. Behavior
// (job records, cache, validation) is identical no matter the entry point;
// presentation (printing) stays in the caller.
package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
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
		Queue: job.NewQueue(db, cfg.Resource.MaxConcurrentJobs, cfg.Resource.MaxRenderWorkers, cfg.Job.MaxHistory, log),
	}
}

func (d Deps) tools() media.Tools { return media.ResolveTools(d.Cfg) }

func (d Deps) analysisStore() *analysis.Store {
	s := analysis.NewStore(d.WS.CacheDir())
	s.MaxBytes = int64(d.Cfg.Resource.MaxCacheGB * (1 << 30))
	return s
}

func (d Deps) proxyStore() *analysis.ProxyStore {
	s := analysis.NewProxyStore(d.WS.CacheDir())
	s.MaxBytes = int64(d.Cfg.Resource.MaxProxyGB * (1 << 30))
	return s
}

// analysisInput resolves the file to analyze for an asset: a generated
// low-res proxy when proxy_enabled and the source is larger than the
// analysis canvas, the original otherwise. The returned Options copy pins
// UseProxy so the cache key distinguishes proxy-based results forever.
func (d Deps) analysisInput(ctx context.Context, asset *storage.Asset, baseOpts analysis.Options) (analysis.Options, string) {
	if !d.Cfg.Resource.ProxyEnabled {
		return baseOpts, asset.Path
	}
	opts := baseOpts
	proxyPath, used, err := d.proxyStore().Ensure(ctx, d.tools(), asset.Path, asset.Fingerprint,
		d.Cfg.Resource.AnalysisWidth, d.Cfg.Resource.FrameSampleFPS, d.Cfg.Resource.ProxyThreads, d.Log)
	if err != nil {
		// Proxy is an optimization, never a correctness gate.
		d.Log.Warn("proxy generation failed; analyzing original", "asset", asset.ID, "err", err)
		return baseOpts, asset.Path
	}
	if !used {
		return baseOpts, asset.Path
	}
	opts.UseProxy = true
	return opts, proxyPath
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
		CallTimeout:   d.Cfg.Resource.AnalyzerCallTimeout.Duration,
	}
}

// AnalyzedAsset is the per-asset payload of AnalyzeProject's callback.
type AnalyzedAsset struct {
	Asset    *storage.Asset
	Result   *analysis.Result
	Segments []event.Segment
}

// ImportAsset probes one media file and upserts it into the project as a
// recorded job (blocking). The file may live anywhere on disk.
func (d Deps) ImportAsset(project *storage.Project, path string) (*storage.Asset, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, xcerr.E(xcerr.CodeValidation, "cannot resolve path: "+path, err)
	}
	_, jerr := d.Queue.RunInline(d.Ctx, job.TypeImport, project.ID, job.ClassIOHeavy,
		map[string]any{"path": abs}, d.importBody(project, abs))
	if jerr != nil {
		return nil, jerr
	}
	return d.latestAssetByName(project.ID, filepath.Base(abs))
}

// ImportAssetAsync is the non-blocking variant; it returns the job id
// immediately (poll GET /api/v1/jobs/{id}).
func (d Deps) ImportAssetAsync(project *storage.Project, path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", xcerr.E(xcerr.CodeValidation, "cannot resolve path: "+path, err)
	}
	return d.Queue.RunAsync(d.Ctx, job.TypeImport, project.ID, job.ClassIOHeavy,
		map[string]any{"path": abs}, d.importBody(project, abs))
}

func (d Deps) importBody(project *storage.Project, path string) job.Runner {
	return func(ctx context.Context, progress func(float64)) error {
		return d.importInto(ctx, project, path, progress)
	}
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

// AnalyzeProject runs the analyzer set over the project's assets as a single
// recorded job (blocking). onAsset (optional) fires after each asset. When
// onlyIDs is non-empty, only those asset IDs are analyzed.
func (d Deps) AnalyzeProject(project *storage.Project, onAsset func(AnalyzedAsset), onlyIDs ...string) error {
	_, jerr := d.Queue.RunInline(d.Ctx, job.TypeAnalyze, project.ID, job.ClassCPUHeavy,
		map[string]any{"assets": len(onlyIDs)}, d.analyzeBody(project, onAsset, onlyIDs))
	return jerr
}

// AnalyzeProjectAsync is the non-blocking variant.
func (d Deps) AnalyzeProjectAsync(project *storage.Project, onAsset func(AnalyzedAsset), onlyIDs ...string) (string, error) {
	return d.Queue.RunAsync(d.Ctx, job.TypeAnalyze, project.ID, job.ClassCPUHeavy,
		map[string]any{"assets": len(onlyIDs)}, d.analyzeBody(project, onAsset, onlyIDs))
}

func (d Deps) analyzeBody(project *storage.Project, onAsset func(AnalyzedAsset), onlyIDs []string) job.Runner {
	return func(jctx context.Context, progress func(float64)) error {
		assets, err := d.DB.ListAssets(jctx, project.ID)
		if err != nil {
			return err
		}
		if len(onlyIDs) > 0 {
			want := make(map[string]bool, len(onlyIDs))
			for _, id := range onlyIDs {
				want[id] = true
			}
			filtered := assets[:0:0]
			for _, a := range assets {
				if want[a.ID] {
					filtered = append(filtered, a)
				}
			}
			if len(filtered) == 0 {
				return xcerr.E(xcerr.CodeNotFound, "no matching assets in project", nil)
			}
			assets = filtered
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

		// Parallel across assets, bounded by max_analysis_workers: each asset
		// is independent, and the cache makes repeats cheap. Errors and
		// callbacks are marshalled back to this goroutine.
		workers := d.Cfg.Resource.MaxAnalysisWorkers
		if workers < 1 {
			workers = 1
		}
		sem := make(chan struct{}, workers)
		var wg sync.WaitGroup
		errCh := make(chan error, len(assets))
		var completed int64
		for i := range assets {
			if err := jctx.Err(); err != nil {
				break
			}
			asset := assets[i]
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				assetOpts, path := d.analysisInput(jctx, &asset, opts)
				result, err := analysis.Run(jctx, store, assetOpts, analyzers,
					path, asset.Fingerprint, asset.DurationSec, asset.HasAudio, d.Log)
				if err != nil {
					errCh <- err
					return
				}
				segs, err := event.Build(result.Tracks, asset.DurationSec, event.DefaultConfig())
				if err != nil {
					errCh <- err
					return
				}
				if onAsset != nil {
					onAsset(AnalyzedAsset{Asset: &asset, Result: result, Segments: segs})
				}
				n := atomic.AddInt64(&completed, 1)
				progress(float64(n) / float64(len(assets)))
			}()
		}
		wg.Wait()
		close(errCh)
		if err := jctx.Err(); err != nil {
			return xcerr.E(xcerr.CodeCancelled, "cancelled", err)
		}
		if first := firstErr(errCh); first != nil {
			return first
		}
		return nil
	}
}

// errChOn drains the buffered error channel and returns the first error
// (nil when empty). Errors after the first are logged by the caller's logger
// upstream — jobs record a single failure cause.
func firstErr(ch chan error) error {
	var first error
	for e := range ch {
		if first == nil {
			first = e
		}
	}
	return first
}

// BuildTimeline generates the project timeline with the given style and
// writes it to the project directory atomically (recorded job, blocking).
func (d Deps) BuildTimeline(project *storage.Project, styleName string) (*timeline.Timeline, error) {
	result := &timeline.Timeline{}
	_, jerr := d.Queue.RunInline(d.Ctx, job.TypeTimeline, project.ID, job.ClassCPULight,
		map[string]any{"style": styleName}, d.timelineBody(project, styleName, result))
	if jerr != nil {
		return nil, jerr
	}
	return result, nil
}

// BuildTimelineAsync is the non-blocking variant.
func (d Deps) BuildTimelineAsync(project *storage.Project, styleName string) (string, error) {
	return d.Queue.RunAsync(d.Ctx, job.TypeTimeline, project.ID, job.ClassCPULight,
		map[string]any{"style": styleName}, d.timelineBody(project, styleName, &timeline.Timeline{}))
}

func (d Deps) timelineBody(project *storage.Project, styleName string, result *timeline.Timeline) job.Runner {
	return func(jctx context.Context, progress func(float64)) error {
		assets, err := d.DB.ListAssets(jctx, project.ID)
		if err != nil {
			return err
		}
		if len(assets) == 0 {
			return xcerr.E(xcerr.CodeValidation, "project has no assets (import first)", nil)
		}
		preset, err := style.Load(styleName, filepath.Join(d.WS.Root, "styles"))
		if err != nil {
			return err
		}
		// Rally segmentation keys off audio transients: with no audio stream
		// anywhere the run would burn a full analysis pass only to die later
		// with an opaque "no events satisfy the style's clip constraints".
		if preset.EventConfig.Mode == event.ModeRally {
			hasAudio := false
			for i := range assets {
				if assets[i].HasAudio {
					hasAudio = true
					break
				}
			}
			if !hasAudio {
				return xcerr.E(xcerr.CodeValidation,
					fmt.Sprintf("style %q uses rally segmentation, which needs audio transients, but no imported asset has an audio stream", preset.Name), nil)
			}
		}
		analyzers, err := d.analyzers()
		if err != nil {
			return err
		}
		// Style-driven analyzers (e.g. a court-ROI motion pass) extend the
		// baseline set; the cache keys keep them separate from plain runs.
		analyzers = append(analyzers, preset.Analyzers()...)
		store := d.analysisStore()
		opts := d.analysisOpts()

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
		outPath, err := d.WS.SafeJoin(filepath.Join("projects", project.ID, "timeline.json"))
		if err != nil {
			return err
		}
		// One-level undo: regeneration is the machine overwriting whatever
		// the user last had (manual edits included), so keep the previous
		// document around before replacing it.
		var prevRevision int64
		if prev, rerr := os.ReadFile(outPath); rerr == nil {
			backupPath, err := d.TimelineBackupPath(project.ID)
			if err != nil {
				return err
			}
			if err := WriteAtomic(backupPath, prev); err != nil {
				return err
			}
			var prevDoc timeline.Timeline
			if json.Unmarshal(prev, &prevDoc) == nil {
				prevRevision = prevDoc.Revision
			}
		}
		// Regeneration advances the document revision so stale editors get
		// the same 409 protection against it that they get against PUTs.
		tl.Revision = prevRevision + 1
		b, err := json.MarshalIndent(tl, "", "  ")
		if err != nil {
			return xcerr.E(xcerr.CodeInternal, "cannot serialize timeline", err)
		}
		if err := WriteAtomic(outPath, b); err != nil {
			return err
		}
		progress(1.0)
		*result = *tl
		return nil
	}
}

// TimelinePath is where a project's timeline document lives.
func (d Deps) TimelinePath(projectID string) (string, error) {
	return d.WS.SafeJoin(filepath.Join("projects", projectID, "timeline.json"))
}

// TimelineBackupPath is the previous timeline document, kept when a style
// regeneration replaces the current one (one-level undo).
func (d Deps) TimelineBackupPath(projectID string) (string, error) {
	return d.WS.SafeJoin(filepath.Join("projects", projectID, "timeline.backup.json"))
}

// RestoreTimelineBackup swaps the backup document back in as the current
// timeline (and the current one becomes the backup, so the swap is itself
// undoable). Returns whether a backup existed.
func (d Deps) RestoreTimelineBackup(project *storage.Project) (bool, error) {
	curPath, err := d.TimelinePath(project.ID)
	if err != nil {
		return false, err
	}
	bakPath, err := d.TimelineBackupPath(project.ID)
	if err != nil {
		return false, err
	}
	bak, err := os.ReadFile(bakPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, xcerr.E(xcerr.CodeInternal, "cannot read timeline backup", err)
	}
	cur, curErr := os.ReadFile(curPath)
	if err := WriteAtomic(curPath, bak); err != nil {
		return false, err
	}
	if curErr == nil {
		if err := WriteAtomic(bakPath, cur); err != nil {
			return true, err
		}
	}
	return true, nil
}

// DefaultRenderPath is the default render output for a project.
func (d Deps) DefaultRenderPath(projectID string) (string, error) {
	return d.WS.SafeJoin(filepath.Join("projects", projectID, "render.mp4"))
}

// RenderProject renders the project's stored timeline to outPath (recorded
// job, blocking; temp scratch removed on success, kept on failure).
func (d Deps) RenderProject(project *storage.Project, outPath string, onProgress func(pct int)) error {
	if err := d.guardRenderOut(project, outPath); err != nil {
		return err
	}
	_, jerr := d.Queue.RunInline(d.Ctx, job.TypeRender, project.ID, job.ClassCPUHeavy,
		map[string]any{"out": outPath}, d.renderBody(project, outPath, onProgress))
	return jerr
}

// RenderProjectAsync is the non-blocking variant.
func (d Deps) RenderProjectAsync(project *storage.Project, outPath string, onProgress func(pct int)) (string, error) {
	if err := d.guardRenderOut(project, outPath); err != nil {
		return "", err
	}
	return d.Queue.RunAsync(d.Ctx, job.TypeRender, project.ID, job.ClassCPUHeavy,
		map[string]any{"out": outPath}, d.renderBody(project, outPath, onProgress))
}

func (d Deps) renderBody(project *storage.Project, outPath string, onProgress func(pct int)) job.Runner {
	started := time.Now()
	return func(jctx context.Context, progress func(float64)) error {
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
		assets, err := d.DB.ListAssets(jctx, project.ID)
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

		tempDir, err := d.WS.NewTempDir("render")
		if err != nil {
			return err
		}
		defer func() {
			// Scratch of a cancelled (or shutting-down) render is worthless
			// debris: the run was abandoned, and serve holds the workspace
			// lock, so "run xcut cleanup" is not actionable until restart —
			// repeated cancels would quietly exhaust the temp budget. Only
			// a timed-out run keeps its scratch (post-mortem, like failures).
			err := jctx.Err()
			if err == nil || errors.Is(err, context.Canceled) {
				_ = os.RemoveAll(tempDir)
			}
		}()

		// This render may add scratch up to the whole temp budget minus what
		// temp/ already holds (older failure debris). 0 (budget off) leaves
		// the renderer's check disabled too.
		var scratchBudget int64
		if max := d.WS.MaxTempBytes; max > 0 {
			scratchBudget = max - d.WS.TempUsage()
			if scratchBudget <= 0 {
				scratchBudget = 1 // just over the floor: fail at the first check
			}
		}

		last := 0
		err = render.Render(jctx, tl, render.Options{
			Tools:           d.tools(),
			TempDir:         tempDir,
			TempBudgetBytes: scratchBudget,
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
	}
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
