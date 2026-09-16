package cli

import (
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/xcerr"
)

func init() {
	register("roi", "list, set or clear a per-asset motion ROI (court region)", usageSyntax("xcut roi <project> [--asset id] [--set x,y,w,h] [--clear]"), cmdROI)
}

// cmdROI manages per-source motion ROIs from the command line (the UI
// picker writes the same assets.motion_roi field). Scripts and batch flows
// have no browser; without this they cannot use the feature at all.
//
//	xcut roi <project>                       list assets and their ROIs
//	xcut roi <project> --set x,y,w,h         set on the (single) asset
//	xcut roi <project> --asset id --set …    set on one asset
//	xcut roi <project> --asset id --clear    clear
//
// Coordinates are normalized 0..1, matching the API/UI contract.
func cmdROI(a *App, args []string) error {
	if len(args) < 1 {
		return xcerr.E(xcerr.CodeValidation, "usage: xcut roi <project> [--asset id] [--set x,y,w,h] [--clear]", nil)
	}
	fs := flag.NewFlagSet("roi", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	assetID := fs.String("asset", "", "target asset id (optional when the project has exactly one asset)")
	setRect := fs.String("set", "", "set ROI as x,y,w,h (normalized 0..1)")
	clear := fs.Bool("clear", false, "clear the ROI")
	if err := fs.Parse(args[1:]); err != nil {
		return xcerr.E(xcerr.CodeValidation, "invalid arguments", err)
	}
	if *setRect != "" && *clear {
		return xcerr.E(xcerr.CodeValidation, "--set and --clear are mutually exclusive", nil)
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
		if err := db.SetAssetROI(a.Ctx, target, nil); err != nil {
			return err
		}
		fmt.Fprintf(a.Stdout, "cleared ROI for %s (%s)\n", chosen.Filename, target)
	case *setRect != "":
		roi, perr := parseROI(*setRect)
		if perr != nil {
			return perr
		}
		if err := db.SetAssetROI(a.Ctx, target, roi); err != nil {
			return err
		}
		fmt.Fprintf(a.Stdout, "set ROI %s for %s (%s)\n", *setRect, chosen.Filename, target)
	default:
	}

	// Always end with the listing so scripts (and humans) see the effect.
	rows, err := db.ListAssets(a.Ctx, p.ID)
	if err != nil {
		return err
	}
	for _, as := range rows {
		roi := "—"
		if as.MotionROI != nil {
			roi = fmt.Sprintf("%.3f,%.3f,%.3f,%.3f", as.MotionROI.X, as.MotionROI.Y, as.MotionROI.W, as.MotionROI.H)
		}
		fmt.Fprintf(a.Stdout, "%s  %-24s  ROI %s\n", as.ID, as.Filename, roi)
	}
	return nil
}

// parseROI parses "x,y,w,h" into a validated normalized MotionROI.
func parseROI(s string) (*storage.MotionROI, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return nil, xcerr.E(xcerr.CodeValidation,
			fmt.Sprintf("ROI %q must be x,y,w,h (normalized 0..1)", s), nil)
	}
	var v [4]float64
	for i, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return nil, xcerr.E(xcerr.CodeValidation,
				fmt.Sprintf("ROI component %q is not a number", p), err)
		}
		v[i] = f
	}
	roi := &storage.MotionROI{X: v[0], Y: v[1], W: v[2], H: v[3]}
	if !roi.Valid() {
		return nil, xcerr.E(xcerr.CodeValidation,
			fmt.Sprintf("ROI %q out of range (x,y >= 0; w,h > 0; x+w <= 1; y+h <= 1)", s), nil)
	}
	return roi, nil
}
