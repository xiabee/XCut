package cli

import (
	"fmt"
	"path/filepath"

	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/xcerr"
)

func init() {
	register("auto", "one-shot: import → analyze → timeline → render", usageSyntax("xcut auto <file...> [--style name] [--project name] [--out path]"), cmdAuto)
}

// cmdAuto runs the full deterministic pipeline in one shot. It reuses the
// individual commands so behavior (job records, output, errors) matches the
// step-by-step flow exactly — with one deliberate deviation: the timeline is
// scoped to the assets THIS run imported. Without that, two runs sharing the
// default project name would silently compose a single cut from both files.
func cmdAuto(a *App, args []string) error {
	styleName := "generic_highlight"
	projectName := "auto"
	outPath := ""
	pos, err := parseCommandArgs(args, map[string]*string{
		"style":   &styleName,
		"project": &projectName,
		"out":     &outPath,
	})
	if err != nil {
		return err
	}
	if len(pos) < 1 {
		return xcerr.E(xcerr.CodeValidation,
			"usage: xcut auto <file...> [--style name] [--project name] [--out path]", nil)
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
	fmt.Fprintf(a.Stdout, "==> analyze\n")
	if err := cmdAnalyze(a, []string{projectName}); err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "==> timeline (%s)\n", styleName)
	// Scope the cut to this run's imports: resolve the assets for the input
	// paths (identity is path-stable, so a resume re-run of the same file
	// still maps to its asset). Two runs sharing the default project name
	// used to compose one cut from BOTH files.
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
		db.Close()
		ids, err := matchAssetIDs(assets, inputs)
		if err != nil {
			return err
		}
		assetIDs = ids
	}
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
		tl, err := d.BuildTimeline(p, styleName, assetIDs...)
		if err != nil {
			return err
		}
		fmt.Fprintf(a.Stdout, "timeline: %d clips, %.1fs total, canvas %dx%d@%.0f\n",
			countTimelineClips(tl), tl.Duration(), tl.Canvas.Width, tl.Canvas.Height, tl.Canvas.FPS)
	}
	fmt.Fprintf(a.Stdout, "==> render\n")
	renderArgs := []string{projectName}
	if outPath != "" {
		renderArgs = append(renderArgs, "--out", outPath)
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
