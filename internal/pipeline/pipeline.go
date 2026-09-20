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
	"math"
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

	// TimelineWriteLock, when set, serializes the timeline document's
	// check-and-write sections (API PUTs, backup restores, regeneration
	// writes) inside one process — the API server injects its own mutex so
	// a PUT that races a regeneration cannot collide revisions with it.
	// CLI commands leave it nil: the workspace writer lock already
	// excludes concurrent writers across processes.
	TimelineWriteLock sync.Locker
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

// assetAnalyzers appends the style-driven analyzers for ONE asset to the
// baseline set: a per-source motion ROI (assets.motion_roi) overrides the
// preset's own court region; with neither, the baseline runs unchanged.
// The ROI is part of the analyzer name, so distinct regions produce
// distinct cache keys and can never cross-contaminate.
func assetAnalyzers(base []analysis.Analyzer, preset *style.Preset, a *storage.Asset) ([]analysis.Analyzer, error) {
	extras := preset.Analyzers()
	if a.MotionROI != nil {
		if !a.MotionROI.Valid() {
			return nil, xcerr.E(xcerr.CodeValidation,
				"asset has an invalid motion_roi", nil)
		}
		extras = []analysis.Analyzer{analysis.FrameDiffROIAnalyzer{ROI: analysis.ROI{
			X: a.MotionROI.X, Y: a.MotionROI.Y, W: a.MotionROI.W, H: a.MotionROI.H,
		}}}
	}
	if len(extras) == 0 {
		return base, nil
	}
	run := make([]analysis.Analyzer, 0, len(base)+len(extras))
	run = append(run, base...)
	return append(run, extras...), nil
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
	asset := &storage.Asset{}
	_, jerr := d.Queue.RunInline(d.Ctx, job.TypeImport, project.ID, job.ClassIOHeavy,
		map[string]any{"path": abs}, d.importBody(project, abs, asset))
	if jerr != nil {
		return nil, jerr
	}
	return asset, nil
}

// ImportAssetAsync is the non-blocking variant; it returns the job id
// immediately (poll GET /api/v1/jobs/{id}).
func (d Deps) ImportAssetAsync(project *storage.Project, path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", xcerr.E(xcerr.CodeValidation, "cannot resolve path: "+path, err)
	}
	return d.Queue.RunAsync(d.Ctx, job.TypeImport, project.ID, job.ClassIOHeavy,
		map[string]any{"path": abs}, d.importBody(project, abs, nil))
}

func (d Deps) importBody(project *storage.Project, path string, out *storage.Asset) job.Runner {
	return func(ctx context.Context, progress func(float64)) error {
		return d.importInto(ctx, project, path, out, progress)
	}
}

// importInto probes and upserts one file; out receives the stored asset row
// (identity is reconciled by UpsertAsset, so re-importing the same path
// yields the same asset with refreshed probe data).
func (d Deps) importInto(ctx context.Context, project *storage.Project, path string, out *storage.Asset, progress func(float64)) error {
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
	if out != nil {
		*out = *asset
	}
	progress(1.0)
	return nil
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
			// Cancellation-aware slot acquire: a worker waiting for a free
			// slot must not outlive the job context.
			var acquired bool
			select {
			case sem <- struct{}{}:
				acquired = true
			case <-jctx.Done():
			}
			if jctx.Err() != nil {
				if acquired {
					<-sem
				}
				break
			}
			asset := assets[i]
			wg.Add(1)
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
				segs, estat, err := event.Build(result.Tracks, asset.DurationSec, event.DefaultConfig())
				if err != nil {
					errCh <- err
					return
				}
				logSegmentation(d.Log, asset.ID, "default", estat, len(segs))
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

// TimelineRequest carries the knobs of one timeline generation. Style is
// required; Duration overrides the preset's target_duration for this run only
// (the preset file on disk is never rewritten), and 0 keeps the preset's own.
//
// Reel length is the lever that bounds highlight coverage — docs/EVAL.md
// measures how far it bounds it — so it is settable per run rather than only
// by editing JSON.
type TimelineRequest struct {
	Style    string
	Duration float64
}

// Style returns a request that keeps the preset's own target duration.
func Style(name string) TimelineRequest { return TimelineRequest{Style: name} }

// MaxRequestDuration bounds an explicit override: a reel longer than this from
// one command is a mistake, not a workflow (AGENTS.md rule 4).
const MaxRequestDuration = 4 * 3600

func (r TimelineRequest) validate() error {
	if r.Duration == 0 {
		return nil
	}
	if math.IsNaN(r.Duration) || math.IsInf(r.Duration, 0) ||
		r.Duration < 1 || r.Duration > MaxRequestDuration {
		return xcerr.E(xcerr.CodeValidation, fmt.Sprintf(
			"timeline duration must be between 1 and %d seconds (got %g), or 0 to keep the style's own target",
			MaxRequestDuration, r.Duration), nil)
	}
	return nil
}

// BuildTimeline generates the project timeline with the given style and
// writes it to the project directory atomically (recorded job, blocking).
// onlyIDs scopes generation to those assets — auto passes the assets it
// imported so a shared/default project's earlier imports never leak clips
// into this run's cut.
func (d Deps) BuildTimeline(project *storage.Project, req TimelineRequest, onlyIDs ...string) (*timeline.Timeline, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}
	result := &timeline.Timeline{}
	_, jerr := d.Queue.RunInline(d.Ctx, job.TypeTimeline, project.ID, job.ClassCPULight,
		map[string]any{"style": req.Style}, d.timelineBody(project, req, onlyIDs, result))
	if jerr != nil {
		return nil, jerr
	}
	return result, nil
}

// BuildTimelineAsync is the non-blocking variant.
func (d Deps) BuildTimelineAsync(project *storage.Project, req TimelineRequest, onlyIDs ...string) (string, error) {
	if err := req.validate(); err != nil {
		return "", err
	}
	return d.Queue.RunAsync(d.Ctx, job.TypeTimeline, project.ID, job.ClassCPULight,
		map[string]any{"style": req.Style}, d.timelineBody(project, req, onlyIDs, &timeline.Timeline{}))
}

func (d Deps) timelineBody(project *storage.Project, req TimelineRequest, onlyIDs []string, result *timeline.Timeline) job.Runner {
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
		preset, err := style.Load(req.Style, filepath.Join(d.WS.Root, "styles"))
		if err != nil {
			return err
		}
		if req.Duration > 0 {
			preset.TargetDuration = req.Duration
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
		store := d.analysisStore()
		opts := d.analysisOpts()

		var items []style.AssetEvents
		for i := range assets {
			asset := assets[i]
			// Same input resolution as the analyze stage: with proxy_enabled
			// the timeline must decode the proxy and use the SAME cache key
			// (UseProxy bit), not the original at a never-hit key — the old
			// form re-decoded full originals on every regeneration and
			// duplicated the analysis cache entries.
			assetOpts, path := d.analysisInput(jctx, &asset, opts)
			run, err := assetAnalyzers(analyzers, preset, &asset)
			if err != nil {
				return err
			}
			res, err := analysis.Run(jctx, store, assetOpts, run,
				path, asset.Fingerprint, asset.DurationSec, asset.HasAudio, d.Log)
			if err != nil {
				return err
			}
			// A per-source ROI produced a frame_diff_roi track; when the
			// preset leaves the motion source on its default, segment THIS
			// asset from its own region instead of the full-frame signal.
			segs, estat, err := event.Build(res.Tracks, asset.DurationSec, eventConfigFor(preset, &asset))
			if err != nil {
				return err
			}
			logSegmentation(d.Log, asset.ID, preset.Name, estat, len(segs))
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
		if err := d.WriteRegeneratedTimeline(project, tl); err != nil {
			return err
		}
		progress(1.0)
		*result = *tl
		return nil
	}
}

// eventConfigFor returns the event config for ONE asset: an asset-scoped
// ROI upgrades the default motion source to the ROI track, so the cut is
// driven by what happened on the court instead of the full-frame signal; a
// preset-set motion_track keeps precedence.
func eventConfigFor(preset *style.Preset, a *storage.Asset) event.Config {
	cfg := preset.EventConfig
	if a.MotionROI != nil && cfg.MotionTrack == "" {
		cfg.MotionTrack = "frame_diff_roi"
	}
	return cfg
}

// logSegmentation makes gate rejections visible: "no events satisfy the
// style's clip constraints" is unactionable without knowing WHICH gate
// refused how much (a too-strict motion floor vs. footage with no onsets
// have different fixes). nil logger (tests) stays silent.
func logSegmentation(log *slog.Logger, assetID, source string, st event.BuildStats, segments int) {
	if log == nil {
		return
	}
	log.Info("event segmentation",
		"asset_id", assetID, "config", source,
		"onsets", st.Onsets,
		"spans_opened", st.SpansOpened, "spans_dropped_min_hits", st.SpansDroppedMinHits,
		"chunks_considered", st.ChunksConsidered,
		"dropped_min_hits", st.ChunksDroppedMinHits,
		"dropped_min_duration", st.ChunksDroppedMinDuration,
		"dropped_motion_floor", st.ChunksDroppedMotionFloor,
		"segments", segments)
}

// WriteRegeneratedTimeline publishes a style-regenerated document as the
// project's timeline: the previous document becomes the one-level backup and
// the revision is bumped so stale editors keep their 409 protection. When
// Deps.TimelineWriteLock is set (serve), the whole read-backup-write section
// runs under it — a manual PUT that lands mid-regeneration becomes the
// backup instead of being silently clobbered at the same revision.
func (d Deps) WriteRegeneratedTimeline(project *storage.Project, tl *timeline.Timeline) error {
	if d.TimelineWriteLock != nil {
		d.TimelineWriteLock.Lock()
		defer d.TimelineWriteLock.Unlock()
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
	return WriteAtomic(outPath, b)
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
			// The swap-back failed: undo the first write so the restore is a
			// clean no-op — otherwise current and backup hold the same
			// document and the advertised one-level undo is silently gone.
			if rbErr := WriteAtomic(curPath, cur); rbErr != nil {
				return true, xcerr.E(xcerr.CodeInternal,
					"restore swap failed and the rollback failed too — current and backup now hold the same document",
					errors.Join(err, rbErr))
			}
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
// job, blocking; temp scratch removed on success, kept on failure). A
// non-empty subsPath burns subtitles over the finished video as a final
// pass (libass; .ass or .srt) — audio is copied, the video is re-encoded
// once. A failed burn removes the freshly rendered output rather than
// publishing an un-burned result the caller asked to subtitle.
func (d Deps) RenderProject(project *storage.Project, outPath, subsPath string, onProgress func(pct int)) error {
	if err := d.guardRenderOut(project, outPath); err != nil {
		return err
	}
	_, jerr := d.Queue.RunInline(d.Ctx, job.TypeRender, project.ID, job.ClassCPUHeavy,
		map[string]any{"out": outPath, "subs": subsPath}, d.renderBody(project, outPath, subsPath, onProgress))
	return jerr
}

// RenderProjectAsync is the non-blocking variant.
func (d Deps) RenderProjectAsync(project *storage.Project, outPath, subsPath string, onProgress func(pct int)) (string, error) {
	if err := d.guardRenderOut(project, outPath); err != nil {
		return "", err
	}
	return d.Queue.RunAsync(d.Ctx, job.TypeRender, project.ID, job.ClassCPUHeavy,
		map[string]any{"out": outPath, "subs": subsPath}, d.renderBody(project, outPath, subsPath, onProgress))
}

func (d Deps) renderBody(project *storage.Project, outPath, subsPath string, onProgress func(pct int)) job.Runner {
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
		// renderErr carries the render's own outcome into the cleanup: the
		// job context stays live across a plain ffmpeg failure, so jctx.Err()
		// alone cannot tell "abandoned debris" from "failed run". Failed (and
		// timed-out) runs keep their scratch for post-mortem; a plain
		// cancellation means the run was abandoned, and serve holds the
		// workspace lock so "run xcut cleanup" is not actionable until
		// restart — repeated cancels would quietly exhaust the temp budget.
		var renderErr error
		defer func() {
			if renderErr == nil || errors.Is(jctx.Err(), context.Canceled) {
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
		renderErr = render.Render(jctx, tl, render.Options{
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
		if renderErr != nil {
			return renderErr
		}
		if subsPath != "" {
			// The burn is part of the render the caller asked for: on failure
			// the just-rendered base file is removed, not published half-done.
			if renderErr = render.BurnSubtitles(jctx, d.tools(), outPath, subsPath, outPath); renderErr != nil {
				_ = os.Remove(outPath)
				return renderErr
			}
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

// testHookWriteFail, when set (tests only), makes WriteAtomic fail for
// paths it reports — the injection point that lets the backup-restore
// rollback test force a mid-swap failure deterministically. nil in
// production.
var testHookWriteFail func(path string) bool

// WriteAtomic writes b to path via temp file + rename (crash safety).
func WriteAtomic(path string, b []byte) error {
	if testHookWriteFail != nil && testHookWriteFail(path) {
		return xcerr.E(xcerr.CodeInternal, "injected write failure", nil)
	}
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
	if err := workspace.RetryableRename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return xcerr.E(xcerr.CodeInternal, "cannot finalize output file", err)
	}
	return nil
}
