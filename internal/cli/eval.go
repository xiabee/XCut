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
	"github.com/xiabee/XCut/internal/worker"
	"github.com/xiabee/XCut/internal/xcerr"
)

func init() {
	register("eval", "score pipeline selection quality against an annotated manifest",
		usageSyntax("xcut eval <manifest.json> [--check] [--style name] [--duration seconds] [--out results.json] [--iou 0.3] [--baseline results.json]"), cmdEval)
}

// evalCaseResult is one case's outcome in the results document.
type evalCaseResult struct {
	Name  string `json:"name"`
	Style string `json:"style"`
	Media string `json:"media"`
	Error string `json:"error,omitempty"`
	// Selected carries each clip's source interval plus the style engine's
	// explanation (score, dominant factors) — an eval run is self-diagnosing:
	// WHY each moment was picked matters for tuning as much as the metrics.
	Selected []selectedClipJSON `json:"selected,omitempty"`
	Metrics  *eval.CaseMetrics  `json:"metrics,omitempty"`
	// ScoreMarks records how many scoreboard boundaries this case's clips were
	// allowed to stop at. Without it a "score_roi" run that scanned nothing
	// would read exactly like one that scanned everything.
	ScoreMarks int `json:"score_marks,omitempty"`
}

// scanScoreMarks measures point ends off the burned-in scoreboard and stores
// them on the case's asset through the production write (`pipeline.ScoreScan`,
// the same one the analyze fan-out uses), so the A/B measures the feature
// rather than a harness stand-in. It returns the count it stored. Refusing
// loudly when no sidecar exists is the point: score_roi is a request for a
// measurement, not a hint — and the refusal names the case, because a manifest
// can hold several.
func scanScoreMarks(ea *App, db *storage.DB, c eval.Case, assetID string) (int, error) {
	crop := []float64{c.ScoreROI.X, c.ScoreROI.Y, c.ScoreROI.W, c.ScoreROI.H}
	bin := worker.ResolveAIBin(ea.Cfg.Workers.AIBin)
	if bin == "" {
		return 0, xcerr.E(xcerr.CodeNotFound,
			fmt.Sprintf("case %q asks for score_roi, which needs a scoreboard-capable AI sidecar (set workers.ai_bin)", c.Name), nil)
	}
	times, err := pipeline.ScoreScan(ea.Ctx, db, bin, assetID, c.Media, crop)
	if err != nil {
		return 0, err
	}
	return len(times), nil
}

// selectedClipJSON is one selected source interval in the results document.
// Score/hit fields mirror the clip Metadata the style engine writes; they
// are pointers so a missing/parse-failing metadata key is omitted rather
// than reported as a false zero.
type selectedClipJSON struct {
	Start      float64  `json:"start"`
	End        float64  `json:"end"`
	Score      *float64 `json:"score,omitempty"`
	Reason     string   `json:"reason,omitempty"`
	Breakdown  string   `json:"score_breakdown,omitempty"`
	HitCount   *int     `json:"hit_count,omitempty"`
	HitDensity *float64 `json:"hit_density,omitempty"`
	PointEnd   *float64 `json:"point_end,omitempty"`
}

type evalResults struct {
	Version      int     `json:"version"`
	GeneratedAt  string  `json:"generated_at"`
	HitIoU       float64 `json:"hit_iou"`
	DuplicateIoU float64 `json:"duplicate_iou"`
	// Duration is the reel-length override this run used, 0 when each style's
	// own target applied. A baseline and a run at different lengths are not
	// comparable as algorithms, so the number has to survive into the file.
	Duration float64          `json:"duration,omitempty"`
	Cases    []evalCaseResult `json:"cases"`
	Macro    eval.Macro       `json:"macro"`
}

func cmdEval(a *App, args []string) error {
	styleFlag := "" // "" = per-case style, else "generic_highlight"
	outPath := ""
	iouFlag := "0.3"
	durationFlag := "" // "" = each style's own target_duration
	baselinePath := "" // results.json from a previous run, to diff against
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
		"style":    &styleFlag,
		"out":      &outPath,
		"iou":      &iouFlag,
		"duration": &durationFlag,
		"baseline": &baselinePath,
	})
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return xcerr.E(xcerr.CodeValidation,
			"usage: xcut eval <manifest.json> [--check] [--style name] [--duration seconds] [--out results.json] [--iou 0.3] [--baseline results.json]", nil)
	}
	if check {
		if baselinePath != "" {
			return xcerr.E(xcerr.CodeValidation, "--baseline has no effect with --check", nil)
		}
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

	duration, err := parseDurationFlag(durationFlag)
	if err != nil {
		return err
	}

	manifest, err := eval.LoadManifest(pos[0])
	if err != nil {
		return err
	}

	// Results are derived data; the manifest (hand-written annotations) and
	// the case media are the irreplaceable inputs — refuse an --out that
	// would clobber either, before any ffmpeg work starts.
	if outPath != "" {
		if pipeline.SameFileOrPath(outPath, pos[0]) {
			return xcerr.E(xcerr.CodeValidation,
				"eval results would overwrite the input manifest — pick a different --out path", nil)
		}
		for _, c := range manifest.Cases {
			if pipeline.SameFileOrPath(outPath, c.Media) {
				return xcerr.E(xcerr.CodeValidation,
					"eval results would overwrite a case's source media — pick a different --out path", nil)
			}
		}
	}

	// Comparing against the file this run is about to overwrite would produce
	// a diff against itself on the next invocation — a quiet way to make every
	// change look like no change at all.
	if baselinePath != "" && outPath != "" && pipeline.SameFileOrPath(baselinePath, outPath) {
		return xcerr.E(xcerr.CodeValidation,
			"--baseline and --out are the same file; write this run elsewhere to compare", nil)
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
		Duration:     duration,
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
		// Liveness: a real-media case runs minutes of ffmpeg; without a
		// line per started case the run looks hung until the case finishes.
		fmt.Fprintf(a.Stdout, "  [%d/%d] %s  (%s)\n", i+1, len(manifest.Cases), c.Name, styleName)
		res := evalCaseResult{Name: c.Name, Style: styleName, Media: c.Media}

		clips, marks, terr := evalRunCase(&ea, db, c, styleName, duration)
		res.ScoreMarks = marks
		var cm *eval.CaseMetrics
		if terr != nil {
			res.Error = terr.Error()
			ea.Log.Debug("eval case failed", "case", c.Name, "err", terr)
		} else {
			res.Selected = toJsonSelectedClips(clips)
			m := eval.Score(clipsToIntervals(clips), c.Expected, eval.Config{HitIoU: hitIoU})
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
		fmt.Fprintf(a.Stdout, "  %-24s P %.3f  R %.3f  F1 %.3f  ranges %d/%d  dup %.2f  clips %d  missed run %d%s\n",
			c.Name, m.Precision, m.Recall, m.F1, m.RangesHit, m.RangesTotal, m.DuplicateRate, m.Clips,
			m.LongestMissedRun, boundaryShaped(res))
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

	// After the write, deliberately: a bad --baseline must not throw away the
	// minutes of ffmpeg work this run already produced.
	if baselinePath != "" {
		if err := reportEvalBaseline(a, &run, baselinePath); err != nil {
			return err
		}
	}

	for _, c := range run.Cases {
		if c.Error != "" {
			return xcerr.E(xcerr.CodeInternal, "eval had failing cases (see output above)", nil)
		}
	}
	return nil
}

// evalRunCase executes import → timeline for one manifest case in the shared
// eval workspace and returns the style-selected clips (source intervals plus
// the per-clip score metadata). All clips reference the single imported asset,
// so source times are comparable.
// duration overrides each case's reel length for this run (0 = the style's own
// target), which is what lets the harness measure the length/coverage trade-off
// instead of arguing about it. The second result is how many scoreboard marks
// the case ran with (0 when the manifest asked for none).
func evalRunCase(ea *App, db *storage.DB, c eval.Case, styleName string, duration float64) ([]timeline.Clip, int, error) {
	if _, err := os.Stat(c.Media); err != nil {
		return nil, 0, xcerr.E(xcerr.CodeNotFound, "media file missing: "+filepath.Base(c.Media), err)
	}
	// Sanitized names can collide ("a b" and "a/b" both → "a_b") and the
	// projects table enforces unique names; disambiguate with a counter
	// instead of failing the case with a raw storage error.
	base := "eval_" + sanitizeProjectName(c.Name)
	projectName := base
	for i := 2; ; i++ {
		existing, err := db.GetProjectByName(ea.Ctx, projectName)
		if err != nil {
			return nil, 0, err
		}
		if existing == nil {
			break
		}
		projectName = fmt.Sprintf("%s_%d", base, i)
	}
	if _, err := db.CreateProject(ea.Ctx, projectName); err != nil {
		return nil, 0, err
	}
	p, err := db.GetProjectByName(ea.Ctx, projectName)
	if err != nil {
		return nil, 0, err
	}
	d := ea.Pipeline(db)
	asset, err := d.ImportAsset(p, c.Media)
	if err != nil {
		return nil, 0, err
	}
	// Per-case court ROI: applied to the imported asset so the timeline
	// segments THIS case from its own region (manifest A/B support).
	if c.AssetROI != nil {
		if !c.AssetROI.Valid() {
			return nil, 0, xcerr.E(xcerr.CodeValidation,
				fmt.Sprintf("case %s: asset_roi must satisfy 0<=x,y and 0<w,h and x+w,y+h<=1", c.Name), nil)
		}
		if err := db.SetAssetROI(ea.Ctx, asset.ID, &storage.MotionROI{
			X: c.AssetROI.X, Y: c.AssetROI.Y, W: c.AssetROI.W, H: c.AssetROI.H,
		}); err != nil {
			return nil, 0, err
		}
	}
	marks := 0
	if c.ScoreROI != nil {
		var serr error
		if marks, serr = scanScoreMarks(ea, db, c, asset.ID); serr != nil {
			return nil, 0, serr
		}
	}
	tl, err := d.BuildTimeline(p, pipeline.TimelineRequest{Style: styleName, Duration: duration})
	if err != nil {
		// The marks survive the failure: they were measured and stored before
		// the reel was attempted, and a results document that reports 0 for a
		// case that scanned two is the exact confusion this field exists to
		// avoid.
		return nil, marks, err
	}
	var clips []timeline.Clip
	for _, tr := range tl.Tracks {
		clips = append(clips, tr.Clips...)
	}
	return clips, marks, nil
}

// clipsToIntervals maps clips to source-time intervals for scoring. A valid
// timeline never carries empty clips, but the filter keeps the contract
// explicit.
func clipsToIntervals(clips []timeline.Clip) []eval.Interval {
	var out []eval.Interval
	for _, c := range clips {
		if c.SourceEnd > c.SourceStart {
			out = append(out, eval.Interval{Start: c.SourceStart, End: c.SourceEnd})
		}
	}
	return out
}

// toJsonSelectedClips maps selected clips to their results-document form,
// lifting the style engine's explanation out of the clip metadata. Metadata
// values are strings (the timeline IR stores metadata as map[string]string);
// unparseable values are omitted rather than reported as false zeros.
// boundaryShaped renders "  boundaries 8/8" for a case that scanned a
// scoreboard: how many of its clips were actually ended by a mark. It prints
// nothing when no scan ran, so an unmarked case's line is exactly what it was
// before — and a marked run that used nothing says so out loud (0/8) rather
// than letting the improved-looking numbers stand unexplained.
func boundaryShaped(res evalCaseResult) string {
	if res.ScoreMarks == 0 || len(res.Selected) == 0 {
		return ""
	}
	used := 0
	for _, s := range res.Selected {
		if s.PointEnd != nil {
			used++
		}
	}
	return fmt.Sprintf("  boundaries %d/%d", used, len(res.Selected))
}

func toJsonSelectedClips(clips []timeline.Clip) []selectedClipJSON {
	out := make([]selectedClipJSON, 0, len(clips))
	for _, c := range clips {
		if c.SourceEnd <= c.SourceStart {
			continue
		}
		sel := selectedClipJSON{Start: c.SourceStart, End: c.SourceEnd}
		if v, err := strconv.ParseFloat(c.Metadata["score"], 64); err == nil {
			sel.Score = &v
		}
		sel.Reason = c.Metadata["reason"]
		sel.Breakdown = c.Metadata["score_breakdown"]
		if v, err := strconv.Atoi(c.Metadata["hit_count"]); err == nil {
			sel.HitCount = &v
		}
		if v, err := strconv.ParseFloat(c.Metadata["hit_density"], 64); err == nil {
			sel.HitDensity = &v
		}
		// Which point boundary ended this clip, if one did. `score_marks` says
		// how many boundaries were available; this says how many were used —
		// without it a scan that measured everything and shaped nothing would
		// still print a marked-looking row.
		if v, err := strconv.ParseFloat(c.Metadata["point_end"], 64); err == nil {
			sel.PointEnd = &v
		}
		out = append(out, sel)
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

	problems := 0
	fmt.Fprintf(a.Stdout, "check: %d case(s) (%s)\n", len(manifest.Cases), manifestPath)
	for _, c := range manifest.Cases {
		issues, dur, probed := checkCase(a, tools, c, styleFlag)
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
//
// Styles resolve against the embedded presets only, on purpose: a real eval
// run builds its timeline in a throwaway workspace whose styles/ directory
// is always empty, so workspace overrides never apply there. Checking the
// user's overrides too would report PASS for styles the run will reject.
func checkCase(a *App, tools media.Tools, c eval.Case, styleFlag string) ([]string, float64, bool) {
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
	if _, serr := style.Load(styleName); serr != nil {
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

// reportEvalBaseline prints this run against a previous results document.
//
// The point is honesty about comparability, not decoration: deltas between two
// runs scored with different hit-IoU thresholds (or where a case errored, or
// where the annotations changed underneath) are not measurements, so each of
// those says so instead of printing a confident number.
func reportEvalBaseline(a *App, run *evalResults, path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return xcerr.E(xcerr.CodeNotFound, "cannot read --baseline "+path, err)
	}
	var base evalResults
	if err := json.Unmarshal(b, &base); err != nil || base.Version != 1 {
		return xcerr.E(xcerr.CodeValidation,
			"--baseline must be a results.json produced by xcut eval --out", nil)
	}
	// A manifest is also {"version":1,"cases":[...]}, so shape alone does not
	// tell the two apart. Only a results document records when it was scored
	// and against which IoU; require both rather than silently diffing against
	// annotations.
	if base.GeneratedAt == "" || base.HitIoU <= 0 {
		return xcerr.E(xcerr.CodeValidation,
			"--baseline is not a results document (no generated_at/hit_iou) — "+
				"pass the file written by `xcut eval --out`, not the manifest", nil)
	}
	byName := map[string]evalCaseResult{}
	for _, c := range base.Cases {
		byName[c.Name] = c
	}

	fmt.Fprintf(a.Stdout, "\nvs baseline %s (%s)\n", path, base.GeneratedAt)
	if base.HitIoU != run.HitIoU {
		fmt.Fprintf(a.Stdout, "  WARN hit_iou differs (baseline %.2f, this run %.2f): "+
			"the deltas below compare different yardsticks, not two algorithms\n",
			base.HitIoU, run.HitIoU)
	}
	if base.Duration != run.Duration {
		fmt.Fprintf(a.Stdout, "  WARN reel length differs (baseline %s, this run %s): "+
			"the deltas below compare different yardsticks, not two algorithms\n",
			baselineLength(base.Duration), baselineLength(run.Duration))
	}
	d := func(v float64) string { return fmt.Sprintf("%+.3f", v) }

	seen := map[string]bool{}
	for _, c := range run.Cases {
		prev, ok := byName[c.Name]
		if !ok {
			fmt.Fprintf(a.Stdout, "  %-24s NEW (not in baseline)\n", c.Name)
			continue
		}
		seen[c.Name] = true
		switch {
		case c.Error != "" || prev.Error != "":
			fmt.Fprintf(a.Stdout, "  %-24s NOT COMPARABLE (a side errored)\n", c.Name)
		case prev.Metrics == nil || c.Metrics == nil:
			fmt.Fprintf(a.Stdout, "  %-24s NOT COMPARABLE (no metrics)\n", c.Name)
		default:
			p, m := prev.Metrics, c.Metrics
			fmt.Fprintf(a.Stdout, "  %-24s P %s  R %s  F1 %s  ranges %+d  dup %s  missed run %+d\n",
				c.Name, d(m.Precision-p.Precision), d(m.Recall-p.Recall), d(m.F1-p.F1),
				m.RangesHit-p.RangesHit, d(m.DuplicateRate-p.DuplicateRate),
				m.LongestMissedRun-p.LongestMissedRun)
		}
	}
	for _, c := range base.Cases {
		if !seen[c.Name] {
			fmt.Fprintf(a.Stdout, "  %-24s DROPPED (no longer run)\n", c.Name)
		}
	}
	if base.Macro.RangesTotal > 0 || base.Macro.Precision > 0 {
		m, p := run.Macro, base.Macro
		fmt.Fprintf(a.Stdout, "  %-24s P %s  R %s  F1 %s  ranges %+d\n",
			"macro", d(m.Precision-p.Precision), d(m.Recall-p.Recall), d(m.F1-p.F1),
			m.RangesHit-p.RangesHit)
	}
	return nil
}

// baselineLength names a run's reel budget for the comparison line: an explicit
// override in seconds, or the style's own target when none was given.
func baselineLength(d float64) string {
	if d <= 0 {
		return "each style's own target"
	}
	return fmt.Sprintf("%.0fs", d)
}
