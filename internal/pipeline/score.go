package pipeline

import (
	"context"
	"time"

	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/worker"
	"github.com/xiabee/XCut/internal/xcerr"
)

// ScoreScanTimeout bounds one scoreboard scan. The measured case is 8.6 s for a
// 10-minute 720p source; the ceiling is for a multi-hour file on slower
// hardware, and a hung sidecar must not pin an analyze job.
const ScoreScanTimeout = 10 * time.Minute

// scanScoreMarks measures point ends for every asset whose region the user set
// and whose marks are missing or were taken against a different rect.
//
// It runs here, after the parallel analysis fan-out, and serially on purpose:
// the result belongs on the asset row rather than in the analysis cache (it must
// survive eviction — D15), and each scan is a sidecar plus an ffmpeg child, so
// one at a time keeps the worker budget meaningful instead of multiplying it by
// MaxAnalysisWorkers.
func (d *Deps) scanScoreMarks(ctx context.Context, assets []storage.Asset) error {
	pending := 0
	for i := range assets {
		if needsScoreScan(&assets[i]) {
			pending++
		}
	}
	if pending == 0 {
		return nil
	}
	bin := worker.ResolveAIBin(d.Cfg.Workers.AIBin)
	if bin == "" {
		// The region was set by someone who asked for the measurement; running
		// on without it would produce a reel that silently ignores it (D13).
		return xcerr.E(xcerr.CodeNotFound,
			"a scoreboard region is set on this project, and scanning it needs an AI sidecar offering score_changes (set workers.ai_bin, e.g. scripts/xcut-ai-sidecar.py, or clear the region)", nil)
	}
	for i := range assets {
		a := &assets[i]
		if !needsScoreScan(a) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return xcerr.E(xcerr.CodeCancelled, "cancelled", err)
		}
		times, err := worker.ScoreChanges(ctx, bin, a.Path, a.ScoreCrop, ScoreScanTimeout)
		if err != nil {
			return err
		}
		if err := d.DB.SetAssetScoreMarks(ctx, a.ID, &storage.ScoreMarks{
			Crop: a.ScoreCrop, Times: times, At: time.Now().Unix(),
		}); err != nil {
			return err
		}
		summary := worker.SummarizeScoreMarks(times)
		d.Log.Info("scoreboard marks measured", "asset", a.ID, "marks", len(times),
			"crop", a.ScoreCrop, "spacing", summary.String())
	}
	return nil
}

// needsScoreScan is the whole staleness rule: a region with no measurement, or
// with one taken against a different region.
func needsScoreScan(a *storage.Asset) bool {
	if len(a.ScoreCrop) == 0 {
		return false
	}
	return a.ScoreMarks == nil || !storage.SameCrop(a.ScoreMarks.Crop, a.ScoreCrop)
}
