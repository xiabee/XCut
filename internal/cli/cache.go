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
	proxies := analysis.NewProxyStore(ws.CacheDir())
	budget := int64(a.Cfg.Resource.MaxCacheGB * (1 << 30))
	proxyBudget := int64(a.Cfg.Resource.MaxProxyGB * (1 << 30))

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
		proxyEntries, proxyBytes, err := proxies.Usage()
		if err != nil {
			return err
		}
		if jsonOut {
			b, err := json.Marshal(map[string]any{
				"analysis": cacheUsageJSON{store.Dir(), entries, bytes, budget},
				"proxy":    cacheUsageJSON{proxies.Dir(), proxyEntries, proxyBytes, proxyBudget},
			})
			if err != nil {
				return xcerr.E(xcerr.CodeInternal, "cannot encode cache stats", err)
			}
			fmt.Fprintf(a.Stdout, "%s\n", b)
			return nil
		}
		fmt.Fprintf(a.Stdout, "cache dir: %s\nentries:   %d\nsize:      %.1f MB of %.1f GB budget (%.1f%%)\n\n",
			store.Dir(), entries, float64(bytes)/(1<<20), float64(budget)/(1<<30), pctOf(bytes, budget))
		fmt.Fprintf(a.Stdout, "proxy dir: %s\nentries:   %d\nsize:      %.1f MB of %.1f GB budget (%.1f%%)\n",
			proxies.Dir(), proxyEntries, float64(proxyBytes)/(1<<20), float64(proxyBudget)/(1<<30), pctOf(proxyBytes, proxyBudget))
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
		proxyEntries, proxyBytes, err := proxies.Usage()
		if err != nil {
			return err
		}
		if entries == 0 && proxyEntries == 0 {
			fmt.Fprintln(a.Stdout, "cache: already empty")
			return nil
		}
		if dryRun {
			fmt.Fprintf(a.Stdout, "would remove %d analysis entries (%.1f MB) and %d proxies (%.1f MB) (dry run)\n",
				entries, float64(bytes)/(1<<20), proxyEntries, float64(proxyBytes)/(1<<20))
			return nil
		}
		removed, freed, err := store.EvictTo(0)
		if err != nil {
			return err
		}
		proxyRemoved, proxyFreed, err := proxies.EvictTo(0)
		if err != nil {
			return err
		}
		fmt.Fprintf(a.Stdout, "removed %d analysis entries (%.1f MB) and %d proxies (%.1f MB)\n",
			removed, float64(freed)/(1<<20), proxyRemoved, float64(proxyFreed)/(1<<20))
		a.Log.Info("cache cleared", "analysis_entries", removed, "analysis_bytes", freed,
			"proxy_entries", proxyRemoved, "proxy_bytes", proxyFreed)
		return nil

	default:
		return xcerr.E(xcerr.CodeValidation,
			"unknown subcommand — usage: xcut cache stats [--json] | clear [--dry-run]", nil)
	}
}

type cacheUsageJSON struct {
	Dir         string `json:"dir"`
	Entries     int    `json:"entries"`
	Bytes       int64  `json:"bytes"`
	BudgetBytes int64  `json:"budget_bytes"`
}

func pctOf(v, budget int64) float64 {
	if budget <= 0 {
		return 0
	}
	return float64(v) / float64(budget) * 100
}
