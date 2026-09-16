package cli

import (
	"fmt"
	"time"

	"context"

	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/xcerr"
)

func init() {
	register("project", "manage projects", usageSyntax("xcut project create|list|show|delete <name>"), cmdProject)
	register("jobs", "list jobs of a project", usageSyntax("xcut jobs <project>"), cmdJobs)
}

func cmdProject(a *App, args []string) error {
	if len(args) < 1 {
		return xcerr.E(xcerr.CodeValidation, "usage: xcut project create|list|show|delete [args]", nil)
	}
	db, err := a.OpenDB()
	if err != nil {
		return err
	}
	defer db.Close()
	ctx := a.Ctx

	switch args[0] {
	case "create":
		if len(args) != 2 {
			return xcerr.E(xcerr.CodeValidation, "usage: xcut project create <name>", nil)
		}
		p, err := db.CreateProject(ctx, args[1])
		if err != nil {
			return err
		}
		fmt.Fprintf(a.Stdout, "created project %s (id %s)\n", p.Name, p.ID)
		return nil

	case "list":
		ps, err := db.ListProjects(ctx)
		if err != nil {
			return err
		}
		if len(ps) == 0 {
			fmt.Fprintln(a.Stdout, "no projects (create one: xcut project create <name>)")
			return nil
		}
		for _, p := range ps {
			assets, _ := db.ListAssets(ctx, p.ID)
			fmt.Fprintf(a.Stdout, "%s\t%s\t%d assets\t%s\n",
				p.Name, p.ID, len(assets), time.Unix(p.UpdatedAt, 0).Format("2006-01-02 15:04"))
		}
		return nil

	case "show":
		if len(args) != 2 {
			return xcerr.E(xcerr.CodeValidation, "usage: xcut project show <name>", nil)
		}
		p, err := requireProject(db, ctx, args[1])
		if err != nil {
			return err
		}
		fmt.Fprintf(a.Stdout, "project  %s\nid       %s\ncreated  %s\n", p.Name, p.ID, time.Unix(p.CreatedAt, 0).Format(time.RFC3339))
		assets, err := db.ListAssets(ctx, p.ID)
		if err != nil {
			return err
		}
		fmt.Fprintf(a.Stdout, "assets   %d\n", len(assets))
		for _, as := range assets {
			fmt.Fprintf(a.Stdout, "  [%s] %s  %s  %dx%d %.2ffps  %s%s\n",
				as.ID, as.Filename, media.HumanDuration(as.DurationSec),
				as.Width, as.Height, as.FPS, as.VideoCodec,
				audioSuffix(as.HasAudio, as.AudioCodec))
		}
		return nil

	case "delete":
		if len(args) != 2 {
			return xcerr.E(xcerr.CodeValidation, "usage: xcut project delete <name>", nil)
		}
		p, err := requireProject(db, ctx, args[1])
		if err != nil {
			return err
		}
		// The atomic gate lives in DeleteProject (gate + delete are one
		// statement); here the project row was just read, so 0 rows means
		// busy.
		n, err := db.DeleteProject(ctx, p.ID)
		if err != nil {
			return err
		}
		if n == 0 {
			return xcerr.E(xcerr.CodeConflict,
				"project has queued or running jobs — wait for them to finish before deleting", nil)
		}
		if err := a.Workspace().RemoveProjectDirs(p.ID); err != nil {
			fmt.Fprintf(a.Stderr, "xcut: warning: directory cleanup failed: %s\n", xcerr.UserMessage(err))
		}
		fmt.Fprintf(a.Stdout, "deleted project %s (%s)\n", p.Name, p.ID)
		return nil

	default:
		return xcerr.E(xcerr.CodeValidation, "unknown project subcommand: "+args[0], nil)
	}
}

func cmdJobs(a *App, args []string) error {
	if len(args) != 1 {
		return xcerr.E(xcerr.CodeValidation, "usage: xcut jobs <project>", nil)
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
	jobs, err := db.ListJobs(a.Ctx, p.ID)
	if err != nil {
		return err
	}
	if len(jobs) == 0 {
		fmt.Fprintln(a.Stdout, "no jobs")
		return nil
	}
	for _, j := range jobs {
		line := fmt.Sprintf("%s\t%s\t%s", j.ID, j.Type, j.Status)
		if j.Progress > 0 && j.Status == storage.StatusRunning {
			line += fmt.Sprintf(" %.0f%%", j.Progress*100)
		}
		if j.ErrorCode != "" {
			line += " [" + j.ErrorCode + "] " + j.ErrorMessage
		}
		fmt.Fprintln(a.Stdout, line)
	}
	return nil
}

func requireProject(db *storage.DB, ctx context.Context, name string) (*storage.Project, error) {
	p, err := db.GetProjectByName(ctx, name)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, xcerr.E(xcerr.CodeNotFound, "project not found: "+name, nil)
	}
	return p, nil
}

func audioSuffix(hasAudio bool, codec string) string {
	if !hasAudio {
		return " (no audio)"
	}
	return " +" + codec
}
