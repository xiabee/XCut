package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/xcerr"
)

func init() {
	register("player", "person filter: mark where you are in a source", usageSyntax("xcut player <project> --asset <id> [--set x,y,w,h [--at seconds]]"), cmdPlayer)
}

func cmdPlayer(a *App, args []string) error {
	if len(args) < 1 {
		return xcerr.E(xcerr.CodeValidation,
			"usage: xcut player <project> --asset <id> [--set x,y,w,h [--at seconds]]", nil)
	}
	projectName := args[0]

	assetID := ""
	set := ""
	atStr := ""
	if _, err := parseCommandArgs(args[1:], map[string]*string{
		"asset": &assetID,
		"set":   &set,
		"at":    &atStr,
	}); err != nil {
		return err
	}
	if assetID == "" {
		return xcerr.E(xcerr.CodeValidation, "--asset <id> is required", nil)
	}

	db, err := a.OpenDB()
	if err != nil {
		return err
	}
	defer db.Close()
	ctx := a.Ctx

	p, err := requireProject(db, ctx, projectName)
	if err != nil {
		return err
	}
	assets, err := db.ListAssets(ctx, p.ID)
	if err != nil {
		return err
	}
	var asset *storage.Asset
	for i := range assets {
		if assets[i].ID == assetID {
			asset = &assets[i]
			break
		}
	}
	if asset == nil {
		return xcerr.E(xcerr.CodeNotFound, "asset not found: "+assetID, nil)
	}

	// Show mode: no --set prints the current state.
	if set == "" {
		if asset.PlayerSpot == nil {
			fmt.Fprintf(a.Stdout, "asset %s has no player spot\n", asset.ID)
			return nil
		}
		r := asset.PlayerSpot.Rect
		fmt.Fprintf(a.Stdout, "asset %s\n  spot   %.4f,%.4f %.4fx%.4f at %.1fs\n",
			asset.ID, r[0], r[1], r[2], r[3], asset.PlayerSpot.At)
		if len(asset.PlayerSpot.Bins) > 0 {
			fmt.Fprintf(a.Stdout, "  signature measured (%d bins)\n", len(asset.PlayerSpot.Bins))
		} else {
			fmt.Fprintf(a.Stdout, "  signature not measured yet — run: xcut analyze %s\n", p.Name)
		}
		return nil
	}

	rect, err := parseSpotRect(set)
	if err != nil {
		return err
	}
	at := 0.0
	if atStr != "" {
		at, err = strconv.ParseFloat(atStr, 64)
		if err != nil {
			return xcerr.E(xcerr.CodeValidation, "--at must be seconds", err)
		}
	}
	spot := &storage.PlayerSpot{Rect: rect, At: at}
	if err := db.SetAssetPlayerSpot(ctx, asset.ID, spot); err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "player spot set on %s: %s at %.1fs\n", asset.ID, set, at)
	fmt.Fprintf(a.Stdout, "run: xcut analyze %s — to measure the signature\n", p.Name)
	return nil
}

// parseSpotRect parses "x,y,w,h" (normalized 0..1 fractions).
func parseSpotRect(s string) ([]float64, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return nil, xcerr.E(xcerr.CodeValidation,
			"spot must be x,y,w,h (normalized 0..1 fractions, comma-separated)", nil)
	}
	rect := make([]float64, 4)
	for i, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return nil, xcerr.E(xcerr.CodeValidation, fmt.Sprintf("spot component %q is not a number", p), err)
		}
		rect[i] = v
	}
	if !storage.ValidSpotRect(rect) {
		return nil, xcerr.E(xcerr.CodeValidation,
			"spot must satisfy 0<=x,y and x+w<=1, y+h<=1 with positive w,h", nil)
	}
	return rect, nil
}

// parsePlayerSpotFlag parses the auto --player-spot value "x,y,w,h[,at]".
func parsePlayerSpotFlag(v string) ([]float64, float64, error) {
	parts := strings.Split(v, ",")
	if len(parts) != 4 && len(parts) != 5 {
		return nil, 0, xcerr.E(xcerr.CodeValidation,
			"--player-spot must be x,y,w,h or x,y,w,h,at (normalized 0..1 fractions)", nil)
	}
	nums := make([]float64, len(parts))
	for i, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return nil, 0, xcerr.E(xcerr.CodeValidation,
				fmt.Sprintf("--player-spot component %q is not a number", p), err)
		}
		nums[i] = f
	}
	rect := nums[:4]
	if !storage.ValidSpotRect(rect) {
		return nil, 0, xcerr.E(xcerr.CodeValidation,
			"--player-spot must satisfy 0<=x,y and x+w<=1, y+h<=1 with positive w,h", nil)
	}
	at := 0.0
	if len(parts) == 5 {
		at = nums[4]
	}
	return rect, at, nil
}
