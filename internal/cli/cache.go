package cli

import (
	"encoding/json"
	"fmt"

	"github.com/xiabee/XCut/internal/analysis"
	"github.com/xiabee/XCut/internal/xcerr"
)

func init() {
	register("cache", "inspect or clear the analysis cache",
		usageSyntax("xcut cache stats [--json] | clear [--dry-run]"), cmdCache)
}

func cmdCache(a *App, args []string) error {
	if len(args) == 0 {
		return xcerr.E(xcerr.CodeValidation, "usage: xcut cache stats [--json] | clear [--dry-run]", nil)
	}
	sub, rest := args[0], args[1:]
	ws := a.Workspace()
	store := analysis.NewStore(ws.CacheDir())
	budget := int64(a.Cfg.Resource.MaxCacheGB * (1 << 30))

	switch sub {
	case "stats":
		jsonOut := false
		for _, arg := range rest {
			switch arg {
			case "--json":
				jsonOut = true
			default:
				return xcerr.E(xcerr.CodeValidation, "usage: xcut cache stats [--json]", nil)
			}
		}
		entries, bytes, err := store.Usage()
		if err != nil {
			return err
		}
		if jsonOut {
			b, err := json.Marshal(map[string]any{
				"dir":          store.Dir(),
				"entries":      entries,
				"bytes":        bytes,
				"budget_bytes": budget,
			})
			if err != nil {
				return xcerr.E(xcerr.CodeInternal, "cannot encode cache stats", err)
			}
			fmt.Fprintf(a.Stdout, "%s\n", b)
			return nil
		}
		pct := 0.0
		if budget > 0 {
			pct = float64(bytes) / float64(budget) * 100
		}
		fmt.Fprintf(a.Stdout, "cache dir: %s\nentries:   %d\nsize:      %.1f MB of %.1f GB budget (%.1f%%)\n",
			store.Dir(), entries, float64(bytes)/(1<<20), float64(budget)/(1<<30), pct)
		return nil

	case "clear":
		dryRun := false
		for _, arg := range rest {
			switch arg {
			case "--dry-run":
				dryRun = true
			default:
				return xcerr.E(xcerr.CodeValidation, "usage: xcut cache clear [--dry-run]", nil)
			}
		}
		entries, bytes, err := store.Usage()
		if err != nil {
			return err
		}
		if entries == 0 {
			fmt.Fprintln(a.Stdout, "cache: already empty")
			return nil
		}
		if dryRun {
			fmt.Fprintf(a.Stdout, "would remove %d entries (%.1f MB) (dry run)\n",
				entries, float64(bytes)/(1<<20))
			return nil
		}
		removed, freed, err := store.EvictTo(0)
		if err != nil {
			return err
		}
		fmt.Fprintf(a.Stdout, "removed %d entries (%.1f MB)\n", removed, float64(freed)/(1<<20))
		a.Log.Info("cache cleared", "entries", removed, "bytes", freed)
		return nil

	default:
		return xcerr.E(xcerr.CodeValidation,
			"unknown subcommand — usage: xcut cache stats [--json] | clear [--dry-run]", nil)
	}
}
