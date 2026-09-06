package cli

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/xcerr"
)

func init() {
	register("import", "probe media files into a project", usageSyntax("xcut import <project> <file...>"), cmdImport)
}

func cmdImport(a *App, args []string) error {
	if len(args) < 2 {
		return xcerr.E(xcerr.CodeValidation, "usage: xcut import <project> <file...>", nil)
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
	d := a.Pipeline(db)

	failed := false
	for _, file := range args[1:] {
		started := time.Now()
		asset, err := d.ImportAsset(p, file)
		if err != nil {
			failed = true
			fmt.Fprintf(a.Stderr, "import failed for %s: %s\n", filepath.Base(file), xcerr.UserMessage(err))
			continue
		}
		fmt.Fprintf(a.Stdout, "imported %s -> [%s] %s %dx%d %.2ffps %s%s\n",
			asset.Filename, asset.ID, media.HumanDuration(asset.DurationSec),
			asset.Width, asset.Height, asset.FPS, asset.VideoCodec,
			audioSuffix(asset.HasAudio, asset.AudioCodec))
		a.Log.Debug("import done", "path", asset.Path, "duration_ms", time.Since(started).Milliseconds())
	}
	if failed {
		return xcerr.E(xcerr.CodeInternal, "one or more imports failed (see above)", nil)
	}
	return nil
}
