package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/xiabee/XCut/internal/pipeline"
	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/worker"
	"github.com/xiabee/XCut/internal/xcerr"
)

func init() {
	register("boundaries", "read point ends off a burned-in scoreboard, so clips can stop there",
		usageSyntax("xcut boundaries <project> [--asset id] [--crop x,y,w,h] [--clear]"), cmdBoundaries)
}

// cmdBoundaries stores per-asset point-boundary marks, measured from the score
// overlay by the optional AI sidecar. The core's own signals were measured and
// cannot tell where a rally ended (docs/EVAL.md); the scoreboard can, because
// one change is exactly one finished point.
//
//	xcut boundaries <project>                    list assets and their marks
//	xcut boundaries <project> --crop x,y,w,h     scan the (single) asset
//	xcut boundaries <project> --asset id --crop …  scan one asset
//	xcut boundaries <project> --asset id --clear   forget its marks
//
// Nothing is scanned, or required, until a crop is supplied: without marks the
// pipeline selects exactly as it did before this existed.
func cmdBoundaries(a *App, args []string) error {
	if len(args) < 1 {
		return xcerr.E(xcerr.CodeValidation,
			"usage: xcut boundaries <project> [--asset id] [--crop x,y,w,h] [--clear]", nil)
	}
	fs := flag.NewFlagSet("boundaries", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	assetID := fs.String("asset", "", "target asset id (optional when the project has exactly one asset)")
	cropRect := fs.String("crop", "", "scoreboard region as x,y,w,h (normalized 0..1)")
	clear := fs.Bool("clear", false, "forget the stored marks")
	if err := fs.Parse(args[1:]); err != nil {
		return xcerr.E(xcerr.CodeValidation, "invalid arguments", err)
	}
	if *cropRect != "" && *clear {
		return xcerr.E(xcerr.CodeValidation, "--crop and --clear are mutually exclusive", nil)
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
	assets, err := db.ListAssets(a.Ctx, p.ID)
	if err != nil {
		return err
	}
	if len(assets) == 0 {
		return xcerr.E(xcerr.CodeNotFound, "project has no assets (import first)", nil)
	}

	target := *assetID
	if target == "" {
		if len(assets) != 1 {
			return xcerr.E(xcerr.CodeValidation,
				"project has multiple assets — pass --asset <id> (see the listing)", nil)
		}
		target = assets[0].ID
	}
	var chosen *storage.Asset
	for i := range assets {
		if assets[i].ID == target {
			chosen = &assets[i]
			break
		}
	}
	if chosen == nil {
		return xcerr.E(xcerr.CodeNotFound, "unknown asset for this project: "+target, nil)
	}

	switch {
	case *clear:
		// The crop carries the intent, so clearing it is what removes both the
		// request and the measurement (SetAssetScoreCrop drops stale marks).
		if err := db.SetAssetScoreCrop(a.Ctx, target, nil); err != nil {
			return err
		}
		fmt.Fprintf(a.Stdout, "cleared score region and marks for %s (%s)\n", chosen.Filename, target)
	case *cropRect != "":
		roi, perr := parseROI(*cropRect)
		if perr != nil {
			return perr
		}
		crop := []float64{roi.X, roi.Y, roi.W, roi.H}
		if _, err := os.Stat(chosen.Path); err != nil {
			return xcerr.E(xcerr.CodeNotFound, "asset media is missing: "+chosen.Path, err)
		}
		bin := worker.ResolveAIBin(a.Cfg.Workers.AIBin)
		if bin == "" {
			return xcerr.E(xcerr.CodeNotFound,
				"no AI sidecar available — the scoreboard scan runs there (set workers.ai_bin, e.g. scripts/xcut-ai-sidecar.py)", nil)
		}
		caps, err := worker.Capabilities(a.Ctx, bin)
		if err != nil {
			return err
		}
		if !sidecarOffersOp(caps, "score_changes") {
			return xcerr.E(xcerr.CodeNotFound,
				"the AI sidecar does not offer score_changes — this build needs the scoreboard op (see xcut doctor)", nil)
		}
		started := time.Now()
		// The region first, then the measurement: score_crop is the source of
		// truth for what the user asked to be scanned, and writing it through
		// the same setter the UI uses keeps the two paths identical.
		if err := db.SetAssetScoreCrop(a.Ctx, target, crop); err != nil {
			return err
		}
		times, err := worker.ScoreChanges(a.Ctx, bin, chosen.Path, crop, pipeline.ScoreScanTimeout)
		if err != nil {
			return err
		}
		marks := &storage.ScoreMarks{Crop: crop, Times: times, At: time.Now().Unix()}
		if err := db.SetAssetScoreMarks(a.Ctx, target, marks); err != nil {
			return err
		}
		fmt.Fprintf(a.Stdout, "found %d point boundaries in %s (%s, %.1fs, crop %s)\n",
			len(times), chosen.Filename, target, time.Since(started).Seconds(), *cropRect)
		// Spacing is what makes the number checkable: 44 boundaries spread over
		// ten minutes look like a match, 44 spaced one second apart look like a
		// crop that followed something other than the score.
		fmt.Fprintf(a.Stdout, "  %s\n", worker.SummarizeScoreMarks(times).String())
		switch {
		case len(times) == 0:
			// A region that never changed is almost always a misaimed crop, and
			// silence here would read as "this source has no scoreboard".
			fmt.Fprintln(a.Stdout, "  note: nothing changed in that region — check the crop against a frame (the scoreboard digits, not its background)")
		case times[len(times)-1] > chosen.DurationSec:
			fmt.Fprintf(a.Stdout, "  note: the last mark is past this asset's %.1fs duration — was it re-imported from a shorter file?\n",
				chosen.DurationSec)
		}
	}

	rows, err := db.ListAssets(a.Ctx, p.ID)
	if err != nil {
		return err
	}
	for _, as := range rows {
		shown := "—"
		switch {
		case as.ScoreMarks != nil:
			m := as.ScoreMarks
			shown = fmt.Sprintf("%d marks at %s", len(m.Times), formatMarksCrop(m.Crop))
			if m.At > 0 {
				shown += " (" + time.Unix(m.At, 0).Format("2006-01-02 15:04") + ")"
			}
			if !sameCrop(as.ScoreCrop, m.Crop) {
				// The region moved since the scan: these boundaries describe a
				// different part of the frame, and the next analyze re-measures.
				shown += "  [STALE: region now " + formatMarksCrop(as.ScoreCrop) + "]"
			}
		case as.ScoreCrop != nil:
			shown = "region " + formatMarksCrop(as.ScoreCrop) + ", not scanned yet (run `xcut analyze`)"
		}
		fmt.Fprintf(a.Stdout, "%s  %-24s  %s\n", as.ID, as.Filename, shown)
	}
	return nil
}

// sameCrop compares two normalized regions by value; nil means "unset".
func sameCrop(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func formatMarksCrop(crop []float64) string {
	if len(crop) != 4 {
		return "unknown crop"
	}
	return fmt.Sprintf("%.3f,%.3f,%.3f,%.3f", crop[0], crop[1], crop[2], crop[3])
}

// sidecarOffersOp reports whether the sidecar advertises one op by name.
func sidecarOffersOp(c *worker.AICapabilities, op string) bool {
	for _, o := range c.Ops {
		if o.Op == op {
			return true
		}
	}
	return false
}
