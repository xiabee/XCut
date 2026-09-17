package cli

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/xiabee/XCut/internal/eval"
	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/pipeline"
	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/style"
	"github.com/xiabee/XCut/internal/timeline"
	"github.com/xiabee/XCut/internal/xcerr"
)

func init() {
	register("eval", "score pipeline selection quality against an annotated manifest",
		usageSyntax("xcut eval <manifest.json> [--check] [--style name] [--out results.json] [--iou 0.3]"), cmdEval)
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
	// --check is a boolean-style flag; pull it out before parseCommandArgs
	// (which requires values for its flags).
	check := false
	rest := args[:0]
	for _, arg := range args {
		if arg == "--check" {
			check = true
			continue
		}
		rest = append(rest, arg)
	}
	pos, err := parseCommandArgs(rest, map[string]*string{
		"style": &styleFlag,
		"out":   &outPath,
		"iou":   &iouFlag,
	})
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return xcerr.E(xcerr.CodeValidation,
			"usage: xcut eval <manifest.json> [--check] [--style name] [--out results.json] [--iou 0.3]", nil)
	}
	if check {
		if outPath != "" {
			return xcerr.E(xcerr.CodeValidation, "--out has no effect with --check", nil)
		}
		if iouFlag != "0.3" {
			return xcerr.E(xcerr.CodeValidation, "--iou has no effect with --check", nil)
		}
		return evalCheck(a, pos[0], styleFlag)
	}
	hitIoU, err := strconv.ParseFloat(iouFlag, 64)
	if err != nil || math.IsNaN(hitIoU) || hitIoU <= 0 || hitIoU > 1 {
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
	// Sanitized names can collide ("a b" and "a/b" both → "a_b") and the
	// projects table enforces unique names; disambiguate with a counter
	// instead of failing the case with a raw storage error.
	base := "eval_" + sanitizeProjectName(c.Name)
	projectName := base
	for i := 2; ; i++ {
		existing, err := db.GetProjectByName(ea.Ctx, projectName)
		if err != nil {
			return nil, err
		}
		if existing == nil {
			break
		}
		projectName = fmt.Sprintf("%s_%d", base, i)
	}
	if _, err := db.CreateProject(ea.Ctx, projectName); err != nil {
		return nil, err
	}
	p, err := db.GetProjectByName(ea.Ctx, projectName)
	if err != nil {
		return nil, err
	}
	d := ea.Pipeline(db)
	asset, err := d.ImportAsset(p, c.Media)
	if err != nil {
		return nil, err
	}
	// Per-case court ROI: applied to the imported asset so the timeline
	// segments THIS case from its own region (manifest A/B support).
	if c.AssetROI != nil {
		if !c.AssetROI.Valid() {
			return nil, xcerr.E(xcerr.CodeValidation,
				fmt.Sprintf("case %s: asset_roi must satisfy 0<=x,y and 0<w,h and x+w,y+h<=1", c.Name), nil)
		}
		if err := db.SetAssetROI(ea.Ctx, asset.ID, &storage.MotionROI{
			X: c.AssetROI.X, Y: c.AssetROI.Y, W: c.AssetROI.W, H: c.AssetROI.H,
		}); err != nil {
			return nil, err
		}
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

// durationSlack tolerates annotation rounding (a range noted as 603.0s
// against a 602.98s file is a rounding artifact, not an annotation error).
const durationSlack = 0.05

// evalCheck validates a manifest and its referenced media WITHOUT running
// the pipeline: no workspace, no imports, no encodes — seconds instead of
// minutes. The /eval/ annotation workflow iterates on hand-written
// manifests; a full eval run only to learn that a range overshoots the
// media (or a style name is mistyped) wastes both time and compute.
func evalCheck(a *App, manifestPath, styleFlag string) error {
	manifest, err := eval.LoadManifest(manifestPath)
	if err != nil {
		return err
	}

	// Duration checks need ffprobe. Without it, existence and style checks
	// still run — skipped loudly, never silently.
	tools := media.ResolveTools(a.Cfg)
	if _, lerr := exec.LookPath(tools.FFprobe); lerr != nil {
		tools.FFprobe = ""
		fmt.Fprintf(a.Stdout, "check: ffprobe not found — media existence checked, durations NOT\n")
	}
	// Workspace style overrides are honored when the directory already
	// exists; check mode never creates workspace state.
	var styleDirs []string
	if dir := filepath.Join(a.Workspace().Root, "styles"); dirExists(dir) {
		styleDirs = append(styleDirs, dir)
	}

	problems := 0
	fmt.Fprintf(a.Stdout, "check: %d case(s) (%s)\n", len(manifest.Cases), manifestPath)
	for _, c := range manifest.Cases {
		issues, dur, probed := checkCase(a, tools, c, styleFlag, styleDirs)
		if len(issues) == 0 {
			durNote := ""
			if probed {
				durNote = "  " + media.HumanDuration(dur)
			}
			fmt.Fprintf(a.Stdout, "  %-24s ok%s  ranges %d\n", c.Name, durNote, len(c.Expected))
			continue
		}
		problems++
		fmt.Fprintf(a.Stdout, "  %-24s FAIL %s\n", c.Name, issues[0])
		for _, extra := range issues[1:] {
			fmt.Fprintf(a.Stdout, "  %-24s      %s\n", "", extra)
		}
	}
	if problems > 0 {
		return xcerr.E(xcerr.CodeValidation,
			fmt.Sprintf("manifest check found problems in %d case(s)", problems), nil)
	}
	fmt.Fprintf(a.Stdout, "check: OK — media present, annotations within duration, styles resolve\n")
	return nil
}

// checkCase validates one case: media existence, ffprobe duration against
// the annotated ranges, and style resolution. Returns the human-readable
// issues (empty = pass) plus the probed duration.
func checkCase(a *App, tools media.Tools, c eval.Case, styleFlag string, styleDirs []string) ([]string, float64, bool) {
	var issues []string
	var dur float64
	probed := false
	if _, err := os.Stat(c.Media); err != nil {
		issues = append(issues, "media missing: "+filepath.Base(c.Media))
	} else if tools.FFprobe != "" {
		p, perr := media.ProbeFile(a.Ctx, tools, c.Media)
		if perr != nil {
			issues = append(issues, "media unreadable: "+userSafeMessage(perr))
		} else {
			dur = p.DurationSec
			probed = true
			for j, r := range c.Expected {
				if r.End > dur+durationSlack {
					issues = append(issues, fmt.Sprintf(
						"range %d (%g..%gs) exceeds media duration %s",
						j, r.Start, r.End, media.HumanDuration(dur)))
				}
			}
		}
	}
	styleName := c.Style
	if styleFlag != "" {
		styleName = styleFlag
	}
	if styleName == "" {
		styleName = "generic_highlight"
	}
	if _, serr := style.Load(styleName, styleDirs...); serr != nil {
		issues = append(issues, "style: "+userSafeMessage(serr))
	}
	return issues, dur, probed
}

// userSafeMessage extracts the display message from an xcerr error without
// the wrapped cause (causes carry internal paths; output stays user-safe).
func userSafeMessage(err error) string {
	if xe, ok := err.(*xcerr.Error); ok {
		return xe.Message
	}
	return err.Error()
}

func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}
