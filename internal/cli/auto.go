package cli

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/xiabee/XCut/internal/config"
	"github.com/xiabee/XCut/internal/pipeline"
	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/xcerr"
)

func init() {
	register("auto", "one-shot: import → analyze → timeline → (captions) → render", usageSyntax("xcut auto <file...> [--style name] [--duration seconds] [--beat-snap seconds|off] [--music file] [--subs on|off|auto|file] [--project name] [--out path] [--score-crop x,y,w,h] [--encoder name]"), cmdAuto)
}

// cmdAuto runs the full deterministic pipeline in one shot. It reuses the
// individual commands so behavior (job records, output, errors) matches the
// step-by-step flow exactly — with one deliberate deviation: the timeline is
// scoped to the assets THIS run imported. Without that, two runs sharing the
// default project name would silently compose a single cut from both files.
func cmdAuto(a *App, args []string) error {
	styleName := "generic_highlight"
	durationFlag := ""
	beatFlag := ""  // "" = the style's own tolerance, "off" = never
	musicFlag := "" // a track to lay under the reel and cut to
	projectName := "auto"
	outPath := ""
	subsFlag := ""      // "" or "off" = no captions; "on" = transcribe this run; a path = burn that file
	scoreCropFlag := "" // normalized x,y,w,h of a burned-in scoreboard; "" = none
	encoderFlag := ""   // hardware/software encoder override for this run's render
	pos, err := parseCommandArgs(args, map[string]*string{
		"style":      &styleName,
		"duration":   &durationFlag,
		"beat-snap":  &beatFlag,
		"music":      &musicFlag,
		"project":    &projectName,
		"out":        &outPath,
		"subs":       &subsFlag,
		"score-crop": &scoreCropFlag,
		"encoder":    &encoderFlag,
	})
	if err != nil {
		return err
	}
	if encoderFlag != "" {
		enc := strings.ToLower(strings.TrimSpace(encoderFlag))
		if !config.ValidEncoderName(enc) {
			return xcerr.E(xcerr.CodeValidation,
				fmt.Sprintf("invalid --encoder %q (want one of: %s)", enc, strings.Join(config.EncoderNames, ", ")), nil)
		}
		a.Cfg.Render.Encoder = enc
	}
	duration, err := parseDurationFlag(durationFlag)
	if err != nil {
		return err
	}
	beatSnap, err := parseBeatSnapFlag(beatFlag)
	if err != nil {
		return err
	}
	if len(pos) < 1 {
		return xcerr.E(xcerr.CodeValidation,
			"usage: xcut auto <file...> [--style name] [--duration seconds] [--beat-snap seconds|off] [--music file] [--subs on|off|auto|file] [--project name] [--out path] [--score-crop x,y,w,h]", nil)
	}
	inputs := pos

	// Project: reuse when it exists, else create.
	{
		db, err := a.OpenDB()
		if err != nil {
			return err
		}
		existing, err := db.GetProjectByName(a.Ctx, projectName)
		if err != nil {
			db.Close()
			return err
		}
		if existing == nil {
			if _, err := db.CreateProject(a.Ctx, projectName); err != nil {
				db.Close()
				return err
			}
		}
		db.Close()
	}
	fmt.Fprintf(a.Stdout, "==> project %s\n", projectName)

	if err := cmdImport(a, append([]string{projectName}, inputs...)); err != nil {
		return err
	}
	// Scope the cut to this run's imports: resolve the assets for the input
	// paths (identity is path-stable, so a resume re-run of the same file
	// still maps to its asset). Two runs sharing the default project name
	// used to compose one cut from BOTH files. This happens before analyze
	// because a --score-crop belongs to exactly these assets, and the analyze
	// stage is what measures the region into point boundaries.
	assetIDs := []string{}
	{
		db, err := a.OpenDB()
		if err != nil {
			return err
		}
		p, err := requireProject(db, a.Ctx, projectName)
		if err != nil {
			db.Close()
			return err
		}
		assets, err := db.ListAssets(a.Ctx, p.ID)
		if err != nil {
			db.Close()
			return err
		}
		ids, err := matchAssetIDs(assets, inputs)
		if err != nil {
			db.Close()
			return err
		}
		assetIDs = ids
		if scoreCropFlag != "" {
			roi, perr := parseROI(scoreCropFlag)
			if perr != nil {
				db.Close()
				return perr
			}
			crop := []float64{roi.X, roi.Y, roi.W, roi.H}
			for _, id := range assetIDs {
				if err := db.SetAssetScoreCrop(a.Ctx, id, crop); err != nil {
					db.Close()
					return err
				}
			}
			fmt.Fprintf(a.Stdout, "==> scoreboard region %s on %d asset(s), measured during analyze\n",
				scoreCropFlag, len(assetIDs))
		}
		db.Close()
	}
	fmt.Fprintf(a.Stdout, "==> analyze\n")
	if err := cmdAnalyze(a, []string{projectName}); err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "==> timeline (%s)\n", styleName)
	{
		db, err := a.OpenDB()
		if err != nil {
			return err
		}
		defer db.Close()
		p, err := requireProject(db, a.Ctx, projectName)
		if err != nil {
			return err
		}
		d := a.Pipeline(db)
		tl, err := d.BuildTimeline(p, pipeline.TimelineRequest{Style: styleName, Duration: duration, BeatSnap: beatSnap, Music: musicFlag}, assetIDs...)
		if err != nil {
			return err
		}
		fmt.Fprintf(a.Stdout, "timeline: %d clips, %.1fs total, canvas %dx%d@%.0f\n",
			countTimelineClips(tl), tl.Duration(), tl.Canvas.Width, tl.Canvas.Height, tl.Canvas.FPS)
		if line := pacingLine(tl); line != "" {
			fmt.Fprintln(a.Stdout, line)
		}
		// The one-shot is where an ambitious --duration is most likely to be
		// answered by a shorter reel, so it has to carry the same explanation
		// `xcut timeline` gives — silence here reads as "it chose not to fill".
		if note := footageLimitNote(tl, duration); note != "" {
			fmt.Fprintln(a.Stdout, note)
		}
	}
	// --subs is the step the one-shot never had: a reel a platform takes has
	// captions on it, and making the user run `xcut subtitles` between two halves of
	// the same command is how a one-shot stops being one. It runs AFTER the timeline on
	// purpose — the caption box is styled against the canvas the reel just declared, so
	// the two agree without anyone ordering them to.
	subsPath := ""
	switch subsFlag {
	case "", "off":
	case "on":
		fmt.Fprintf(a.Stdout, "==> subtitles (transcribed from this run's input)\n")
		db, err := a.OpenDB()
		if err != nil {
			return err
		}
		p, err := requireProject(db, a.Ctx, projectName)
		if err != nil {
			db.Close()
			return err
		}
		d := a.Pipeline(db)
		if err := d.TranscribeProject(p, assetIDs[0]); err != nil {
			db.Close()
			return err
		}
		// Resolve rather than assume the extension: whether the sidecar timed the
		// words decides if the burn gets karaoke, captions or plain SRT.
		subsPath, err = d.ResolveSubtitlesPath(p.ID)
		db.Close()
		if err != nil {
			return err
		}
	case "auto":
		// The render's question, asked here: what should burn for this project, after
		// anything that can be fixed from disk is fixed. It does not transcribe —
		// that is `--subs on` just below, and a flag named for using what is already
		// there would be a lie if it spent minutes of Whisper instead.
		fmt.Fprintf(a.Stdout, "==> subtitles (resolved from this project)\n")
		db, err := a.OpenDB()
		if err != nil {
			return err
		}
		p, err := requireProject(db, a.Ctx, projectName)
		if err != nil {
			db.Close()
			return err
		}
		d := a.Pipeline(db)
		note := ""
		subsPath, note, err = d.ReelSubtitles(p.ID)
		db.Close()
		if err != nil {
			if xcerr.IsCode(err, xcerr.CodeNotFound) {
				return xcerr.E(xcerr.CodeNotFound,
					"this project has no subtitles to burn — re-run with --subs on to transcribe, or pass --subs <file>", err)
			}
			return err
		}
		if note != "" {
			fmt.Fprintf(a.Stdout, "subtitles: %s\n", note)
		}
	default:
		subsPath = subsFlag // a file the caller already has, burned as it stands
	}
	fmt.Fprintf(a.Stdout, "==> render\n")
	renderArgs := []string{projectName}
	if outPath != "" {
		renderArgs = append(renderArgs, "--out", outPath)
	}
	if subsPath != "" {
		renderArgs = append(renderArgs, "--subs", subsPath)
	}
	if err := cmdRender(a, renderArgs); err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "==> done\n")
	return nil
}

// matchAssetIDs resolves the run's inputs to stored asset IDs so the
// timeline is scoped to THIS run's imports (two runs sharing the default
// project name used to compose one cut from BOTH files). Every input must
// resolve — an empty or partial match (path spelled differently than the
// import: Windows case, a moved file) must fail loudly rather than fall
// through to BuildTimeline's unscoped default, which silently composes the
// cut from the whole project again.
func matchAssetIDs(assets []storage.Asset, inputs []string) ([]string, error) {
	want := map[string]bool{}
	for _, in := range inputs {
		if abs, aerr := filepath.Abs(in); aerr == nil {
			want[abs] = true
		}
	}
	ids := []string{}
	for i := range assets {
		if want[assets[i].Path] {
			ids = append(ids, assets[i].ID)
		}
	}
	if len(ids) != len(want) {
		return nil, xcerr.E(xcerr.CodeValidation,
			"not every input resolved to an imported asset — re-import the files into this project, or run auto with the exact paths that were imported", nil)
	}
	return ids, nil
}
