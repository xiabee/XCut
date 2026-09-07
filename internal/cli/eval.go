package cli

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/xiabee/XCut/internal/eval"
	"github.com/xiabee/XCut/internal/pipeline"
	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/timeline"
	"github.com/xiabee/XCut/internal/xcerr"
)

func init() {
	register("eval", "score pipeline selection quality against an annotated manifest",
		usageSyntax("xcut eval <manifest.json> [--style name] [--out results.json] [--iou 0.3]"), cmdEval)
}

// evalCaseResult is one case's outcome in the results document.
type evalCaseResult struct {
	Name     string              `json:"name"`
	Style    string              `json:"style"`
	Media    string              `json:"media"`
	Error    string              `json:"error,omitempty"`
	Selected []eval.IntervalJSON `json:"selected,omitempty"`
	Metrics  *eval.CaseMetrics   `json:"metrics,omitempty"`
}

type evalResults struct {
	Version      int              `json:"version"`
	GeneratedAt  string           `json:"generated_at"`
	HitIoU       float64          `json:"hit_iou"`
	DuplicateIoU float64          `json:"duplicate_iou"`
	Cases        []evalCaseResult `json:"cases"`
	Macro        eval.Macro       `json:"macro"`
}

func cmdEval(a *App, args []string) error {
	styleFlag := "" // "" = per-case style, else "generic_highlight"
	outPath := ""
	iouFlag := "0.3"
	pos, err := parseCommandArgs(args, map[string]*string{
		"style": &styleFlag,
		"out":   &outPath,
		"iou":   &iouFlag,
	})
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return xcerr.E(xcerr.CodeValidation,
			"usage: xcut eval <manifest.json> [--style name] [--out results.json] [--iou 0.3]", nil)
	}
	hitIoU, err := strconv.ParseFloat(iouFlag, 64)
	if err != nil || hitIoU <= 0 || hitIoU > 1 {
		return xcerr.E(xcerr.CodeValidation,
			"--iou must be a number in (0,1] (got "+iouFlag+")", nil)
	}

	manifest, err := eval.LoadManifest(pos[0])
	if err != nil {
		return err
	}

	// Run in an isolated throwaway workspace: eval never touches the user's
	// projects, and cases share one analysis cache.
	wsDir, err := os.MkdirTemp("", "xcut-eval-*")
	if err != nil {
		return xcerr.E(xcerr.CodeInternal, "cannot create eval workspace", err)
	}
	defer os.RemoveAll(wsDir)

	ea := *a
	cfg := *a.Cfg
	cfg.Workspace = wsDir
	ea.Cfg = &cfg
	// Job/analysis logs are noise for eval output; keep them behind -v.
	if !a.Verbose {
		ea.Log = slog.New(slog.NewTextHandler(a.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	}

	fmt.Fprintf(a.Stdout, "eval: %d case(s), hit_iou %.2f (workspace %s)\n",
		len(manifest.Cases), hitIoU, wsDir)

	run := evalResults{
		Version:      1,
		GeneratedAt:  time.Now().UTC().Format(time.RFC3339),
		HitIoU:       hitIoU,
		DuplicateIoU: eval.DefaultMetricsConfig().DuplicateIoU,
		Cases:        make([]evalCaseResult, 0, len(manifest.Cases)),
	}
	var runMetrics []*eval.CaseMetrics

	db, err := ea.OpenDB()
	if err != nil {
		return err
	}
	defer db.Close()

	for i := range manifest.Cases {
		c := manifest.Cases[i]
		styleName := c.Style
		if styleFlag != "" {
			styleName = styleFlag
		}
		if styleName == "" {
			styleName = "generic_highlight"
		}
		res := evalCaseResult{Name: c.Name, Style: styleName, Media: c.Media}

		selected, terr := evalRunCase(&ea, db, c, styleName)
		var cm *eval.CaseMetrics
		if terr != nil {
			res.Error = terr.Error()
			ea.Log.Debug("eval case failed", "case", c.Name, "err", terr)
		} else {
			res.Selected = toJsonIntervals(selected)
			m := eval.Score(selected, c.Expected, eval.Config{HitIoU: hitIoU})
			res.Metrics = &m
			cm = &m
		}
		run.Cases = append(run.Cases, res)
		runMetrics = append(runMetrics, cm)

		// Presentation: one line per case, detail only when asked.
		if res.Error != "" {
			fmt.Fprintf(a.Stdout, "  %-24s ERROR %s\n", c.Name, res.Error)
			continue
		}
		m := res.Metrics
		fmt.Fprintf(a.Stdout, "  %-24s P %.3f  R %.3f  F1 %.3f  ranges %d/%d  dup %.2f  clips %d\n",
			c.Name, m.Precision, m.Recall, m.F1, m.RangesHit, m.RangesTotal, m.DuplicateRate, m.Clips)
	}

	totalRanges := 0
	for _, c := range manifest.Cases {
		totalRanges += len(c.Expected)
	}
	run.Macro = eval.MacroScore(runMetrics, totalRanges)
	fmt.Fprintf(a.Stdout, "macro: P %.3f  R %.3f  F1 %.3f  ranges %d/%d  dup %.3f\n",
		run.Macro.Precision, run.Macro.Recall, run.Macro.F1,
		run.Macro.RangesHit, run.Macro.RangesTotal, run.Macro.DuplicateRate)

	if outPath != "" {
		b, jerr := json.MarshalIndent(run, "", "  ")
		if jerr != nil {
			return xcerr.E(xcerr.CodeInternal, "cannot serialize eval results", jerr)
		}
		if werr := pipeline.WriteAtomic(outPath, b); werr != nil {
			return werr
		}
		fmt.Fprintf(a.Stdout, "results: %s\n", outPath)
	}

	for _, c := range run.Cases {
		if c.Error != "" {
			return xcerr.E(xcerr.CodeInternal, "eval had failing cases (see output above)", nil)
		}
	}
	return nil
}

// evalRunCase executes import → timeline for one manifest case in the shared
// eval workspace and returns the selected source intervals.
func evalRunCase(ea *App, db *storage.DB, c eval.Case, styleName string) ([]eval.Interval, error) {
	if _, err := os.Stat(c.Media); err != nil {
		return nil, xcerr.E(xcerr.CodeNotFound, "media file missing: "+filepath.Base(c.Media), err)
	}
	projectName := "eval_" + sanitizeProjectName(c.Name)
	if _, err := db.CreateProject(ea.Ctx, projectName); err != nil {
		return nil, err
	}
	p, err := db.GetProjectByName(ea.Ctx, projectName)
	if err != nil {
		return nil, err
	}
	d := ea.Pipeline(db)
	if _, err := d.ImportAsset(p, c.Media); err != nil {
		return nil, err
	}
	tl, err := d.BuildTimeline(p, styleName)
	if err != nil {
		return nil, err
	}
	return selectedIntervals(tl), nil
}

// selectedIntervals maps timeline clips to source-time intervals. All clips
// reference the single imported asset, so source times are comparable.
func selectedIntervals(tl *timeline.Timeline) []eval.Interval {
	var out []eval.Interval
	for _, tr := range tl.Tracks {
		for _, c := range tr.Clips {
			if c.SourceEnd > c.SourceStart {
				out = append(out, eval.Interval{Start: c.SourceStart, End: c.SourceEnd})
			}
		}
	}
	return out
}

func toJsonIntervals(ivs []eval.Interval) []eval.IntervalJSON {
	out := make([]eval.IntervalJSON, 0, len(ivs))
	for _, iv := range ivs {
		out = append(out, eval.IntervalJSON{Start: iv.Start, End: iv.End})
	}
	return out
}

// sanitizeProjectName keeps project names filesystem-safe.
func sanitizeProjectName(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return "case"
	}
	return string(out)
}
