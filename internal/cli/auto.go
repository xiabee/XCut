package cli

import (
	"fmt"

	"github.com/xiabee/XCut/internal/xcerr"
)

func init() {
	register("auto", "one-shot: import → analyze → timeline → render (auto <file> [--style s] [--project name] [--out path])", cmdAuto)
}

// cmdAuto runs the full deterministic pipeline in one shot. It reuses the
// individual commands so behavior (job records, output, errors) matches the
// step-by-step flow exactly.
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
	tlArgs := []string{projectName, "--style", styleName}
	if err := cmdTimeline(a, tlArgs); err != nil {
		return err
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
