package cli

import (
	"fmt"
	"time"

	"github.com/xiabee/XCut/internal/analysis"
	"github.com/xiabee/XCut/internal/pipeline"
	"github.com/xiabee/XCut/internal/xcerr"
)

func init() {
	register("analyze", "run baseline analyzers over project assets", usageSyntax("xcut analyze <project> [assetID...]"), cmdAnalyze)
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

	p, err := requireProject(db, a.Ctx, args[0])
	if err != nil {
		return err
	}
	// Positional asset IDs (optional) restrict the run to those assets.
	d := a.Pipeline(db)

	started := time.Now()
	err = d.AnalyzeProject(p, func(res pipeline.AnalyzedAsset) {
		fmt.Fprintf(a.Stdout, "analyzed %s [%s]: %d tracks, %d samples, %d events\n",
			res.Asset.Filename, res.Asset.ID, len(res.Result.Tracks), countSamples(res.Result), len(res.Segments))
		for i, s := range res.Segments {
			if i >= 8 {
				fmt.Fprintf(a.Stdout, "  … and %d more events\n", len(res.Segments)-8)
				return
			}
			fmt.Fprintf(a.Stdout, "  event %6.2fs–%6.2fs  score %.2f  (motion %.2f, audio %.1f dB)\n",
				s.Start, s.End, s.Score, s.MeanMotion, s.MeanAudioDB)
		}
	}, args[1:]...)
	if err != nil {
		return err
	}
	a.Log.Debug("analyze done", "duration_ms", time.Since(started).Milliseconds())
	return nil
}

func countSamples(r *analysis.Result) int {
	n := 0
	for _, t := range r.Tracks {
		n += len(t.Samples)
	}
	return n
}
