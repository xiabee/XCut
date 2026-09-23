package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/xiabee/XCut/internal/analysis"
	"github.com/xiabee/XCut/internal/config"
	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/version"
	"github.com/xiabee/XCut/internal/worker"
	"github.com/xiabee/XCut/internal/workspace"
	"github.com/xiabee/XCut/internal/xcerr"
)

func init() {
	register("doctor", "check environment (ffmpeg, workspace, disk, db, optional workers)", usageSyntax("xcut doctor"), cmdDoctor)
	register("init", "create workspace layout and default config", usageSyntax("xcut init"), cmdInit)
	register("cleanup", "remove temp files and evict analysis cache to budget", usageSyntax("xcut cleanup [--dry-run]"), cmdCleanup)
}

// check is one doctor report line.
type check struct {
	Name   string `json:"name"`
	Status string `json:"status"` // OK | FAIL | WARN | OPTIONAL
	Detail string `json:"detail"`
}

func cmdInit(a *App, _ []string) error {
	ws := workspace.New(a.Cfg.Workspace)
	if err := ws.Ensure(); err != nil {
		return err
	}
	if err := ws.Writable(); err != nil {
		return err
	}
	// Write the default config file if none exists (documented starting point).
	cfgPath := filepath.Join(ws.Root, "config.json")
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		// The file records intent, not whatever secret happens to sit in the
		// environment: an XCUT_AUTH_TOKEN-provided bearer token must not be
		// baked into a 0644 config.json by an unrelated `xcut init`.
		fileCfg := *a.Cfg
		fileCfg.Server.AuthToken = ""
		b, _ := json.MarshalIndent(&fileCfg, "", "  ")
		if err := os.WriteFile(cfgPath, b, 0o644); err != nil {
			return xcerr.E(xcerr.CodeInternal, "cannot write default config", err)
		}
		fmt.Fprintf(a.Stdout, "wrote default config: %s\n", cfgPath)
	}
	// SQLite DB comes into being with its schema on first use (M3); create it
	// now so init yields a complete workspace.
	db, err := storage.Open(ws.DBPath())
	if err != nil {
		return err
	}
	defer db.Close()

	fmt.Fprintf(a.Stdout, "workspace ready: %s\n", ws.Root)
	return nil
}

func cmdCleanup(a *App, args []string) error {
	dryRun := false
	for _, arg := range args {
		switch arg {
		case "--dry-run":
			dryRun = true
		default:
			return xcerr.E(xcerr.CodeValidation, "usage: xcut cleanup [--dry-run]", nil)
		}
	}
	ws := workspace.New(a.Cfg.Workspace)
	// 1. Temp: disposable by definition, remove everything.
	removed, tempBytes, err := ws.CleanupTemp(dryRun)
	if err != nil {
		return err
	}
	verb := "removed"
	if dryRun {
		verb = "would remove"
	}
	for _, name := range removed {
		fmt.Fprintf(a.Stdout, "%s temp/%s\n", verb, name)
	}
	fmt.Fprintf(a.Stdout, "temp: %s %d entries, %.1f MB%s\n",
		verb, len(removed), float64(tempBytes)/(1024*1024), map[bool]string{true: " (dry run)", false: ""}[dryRun])

	// 2. Analysis cache: evict to the configured budget (oldest first).
	store := analysis.NewStore(ws.CacheDir())
	entries, cacheBytes, err := store.Usage()
	if err != nil {
		return err
	}
	budget := int64(a.Cfg.Resource.MaxCacheGB * (1 << 30))
	var evicted int
	var evictedBytes int64
	if dryRun {
		// Dry run must only plan: a real eviction here silently deleted
		// the cache while claiming nothing was removed.
		evicted, evictedBytes, err = store.EvictionPlan(budget)
	} else {
		evicted, evictedBytes, err = store.EvictTo(budget)
	}
	if err != nil {
		return err
	}
	if dryRun {
		fmt.Fprintf(a.Stdout, "cache: %d entries, %.1f MB; would evict %d entries (%.1f MB) to fit %.1f GB budget%s\n",
			entries, float64(cacheBytes)/(1024*1024), evicted, float64(evictedBytes)/(1024*1024),
			float64(budget)/(1<<30), " (dry run)")
	} else {
		fmt.Fprintf(a.Stdout, "cache: %d entries, %.1f MB; evicted %d entries (%.1f MB) to fit %.1f GB budget\n",
			entries, float64(cacheBytes)/(1024*1024), evicted, float64(evictedBytes)/(1024*1024), float64(budget)/(1<<30))
	}

	// 3. Analysis proxies: same eviction discipline under their own budget.
	proxies := analysis.NewProxyStore(ws.CacheDir())
	proxyEntries, proxyBytes, err := proxies.Usage()
	if err != nil {
		return err
	}
	proxyBudget := int64(a.Cfg.Resource.MaxProxyGB * (1 << 30))
	var proxyEvicted int
	var proxyEvictedBytes int64
	if dryRun {
		proxyEvicted, proxyEvictedBytes, err = proxies.EvictionPlan(proxyBudget)
	} else {
		proxyEvicted, proxyEvictedBytes, err = proxies.EvictTo(proxyBudget)
	}
	if err != nil {
		return err
	}
	if dryRun {
		fmt.Fprintf(a.Stdout, "proxies: %d entries, %.1f MB; would evict %d entries (%.1f MB) to fit %.1f GB budget%s\n",
			proxyEntries, float64(proxyBytes)/(1024*1024), proxyEvicted, float64(proxyEvictedBytes)/(1024*1024),
			float64(proxyBudget)/(1<<30), " (dry run)")
	} else {
		fmt.Fprintf(a.Stdout, "proxies: %d entries, %.1f MB; evicted %d entries (%.1f MB) to fit %.1f GB budget\n",
			proxyEntries, float64(proxyBytes)/(1024*1024), proxyEvicted, float64(proxyEvictedBytes)/(1024*1024), float64(proxyBudget)/(1<<30))
	}

	if !dryRun && (len(removed) > 0 || evicted > 0 || proxyEvicted > 0) {
		a.Log.Info("cleanup done", "temp_entries", len(removed), "temp_bytes", tempBytes,
			"cache_evicted", evicted, "cache_bytes", evictedBytes,
			"proxy_evicted", proxyEvicted, "proxy_bytes", proxyEvictedBytes)
	}

	// 4. Stale render partials: <out>.partial files left inside the
	// workspace by a crashed render are never valid output (the renderer
	// publishes atomically), so they are safe to reclaim. Custom --out
	// paths outside the workspace are never touched.
	partialRemoved, partialBytes, err := ws.CleanupPartials(dryRun)
	if err != nil {
		return err
	}
	verb = "removed"
	if dryRun {
		verb = "would remove"
	}
	fmt.Fprintf(a.Stdout, "partials: %s %d entries, %.1f MB%s\n",
		verb, partialRemoved, float64(partialBytes)/(1024*1024), map[bool]string{true: " (dry run)", false: ""}[dryRun])

	return nil
}

func cmdDoctor(a *App, args []string) error {
	if len(args) != 0 {
		return xcerr.E(xcerr.CodeValidation, "usage: xcut doctor", nil)
	}

	var checks []check
	requiredOK := true
	add := func(name, status, detail string) { checks = append(checks, check{name, status, detail}) }

	add("Go core", "OK", version.Version)

	ctx, cancel := context.WithTimeout(a.Ctx, 15*time.Second)
	defer cancel()

	tools := media.ResolveTools(a.Cfg)
	if ver, err := media.Version(ctx, tools.FFmpeg); err != nil {
		requiredOK = false
		add("FFmpeg", "FAIL", "not runnable — install ffmpeg or set XCUT_FFMPEG")
	} else {
		add("FFmpeg", "OK", ver)
	}
	if ver, err := media.Version(ctx, tools.FFprobe); err != nil {
		requiredOK = false
		add("FFprobe", "FAIL", "not runnable — install ffprobe or set XCUT_FFPROBE")
	} else {
		add("FFprobe", "OK", ver)
	}

	ws := workspace.New(a.Cfg.Workspace)
	if err := ws.Ensure(); err != nil {
		requiredOK = false
		add("Workspace", "FAIL", err.Error())
	} else if err := ws.Writable(); err != nil {
		requiredOK = false
		add("Workspace", "FAIL", "not writable: "+ws.Root)
	} else {
		add("Workspace", "OK", ws.Root)
	}

	if free, err := workspace.DiskFree(ws.Root); err != nil {
		add("Disk", "WARN", "cannot query free space")
	} else {
		gb := float64(free) / (1 << 30)
		switch {
		case gb < 1:
			add("Disk", "FAIL", fmt.Sprintf("%.1f GB free", gb))
			requiredOK = false
		case gb < 5:
			add("Disk", "WARN", fmt.Sprintf("%.1f GB free", gb))
		default:
			add("Disk", "OK", fmt.Sprintf("%.1f GB free", gb))
		}
	}

	db, err := storage.Open(ws.DBPath())
	if err != nil {
		requiredOK = false
		add("SQLite", "FAIL", xcerr.UserMessage(err))
	} else {
		if err := db.Ping(); err != nil {
			requiredOK = false
			add("SQLite", "FAIL", err.Error())
		} else {
			add("SQLite", "OK", filepath.Base(ws.DBPath()))
		}
		db.Close()
	}

	// API posture, reported without the token itself: an operator deciding
	// whether to expose serve needs to see which of the three states they are in.
	switch {
	case a.Cfg.Server.ListenRemote:
		add("API auth", "OK", fmt.Sprintf("remote bind enabled; non-local peers must present a bearer token (min %d chars)", config.MinAuthTokenLen))
	case a.Cfg.Server.AuthToken != "":
		add("API auth", "OPTIONAL", "token configured but listen is loopback; local clients stay unauthenticated by design")
	default:
		add("API auth", "OPTIONAL", "no token; loopback only, remote bind refused")
	}

	// Process sandbox posture: what bounds a runaway ffmpeg child. The
	// kill-on-close job object is Windows; the memory cap has a second rung on
	// Linux through a per-child systemd scope (D18), and media.SandboxPosture
	// reports which of the two — or which refusal — is in play here.
	if runtime.GOOS == "windows" {
		if mb := a.Cfg.Resource.FFmpegMaxMemoryMB; mb > 0 {
			add("Process sandbox", "OK", fmt.Sprintf("ffmpeg children join a kill-on-close job with a %d MB per-process memory cap (resource.ffmpeg_max_memory_mb)", mb))
		} else {
			add("Process sandbox", "OPTIONAL", "ffmpeg children join a kill-on-close job; memory uncapped — set resource.ffmpeg_max_memory_mb to bound runaway encoders")
		}
	} else if posture := media.SandboxPosture(); posture != "" {
		// Linux reports what it measured at the first child, not what the config
		// asked for: a scope systemd refused is a WARN with the refusal in it,
		// because the caller set a cap that is not happening.
		switch {
		case media.SandboxArmed():
			add("Process sandbox", "OK", posture)
		case a.Cfg.Resource.FFmpegMaxMemoryMB > 0:
			add("Process sandbox", "WARN", posture)
		default:
			add("Process sandbox", "OPTIONAL", posture)
		}
	} else {
		add("Process sandbox", "OPTIONAL", "no per-child memory cap on this platform; cleanup relies on the context-kill path")
	}

	// Cache usage: informational (OPTIONAL) — eviction is enforced elsewhere.
	{
		store := analysis.NewStore(ws.CacheDir())
		proxies := analysis.NewProxyStore(ws.CacheDir())
		entries, cacheBytes, err := store.Usage()
		if err != nil {
			add("Cache", "WARN", xcerr.UserMessage(err))
		} else {
			proxyEntries, proxyBytes, perr := proxies.Usage()
			if perr != nil {
				add("Cache", "WARN", xcerr.UserMessage(perr))
			} else {
				add("Cache", "OPTIONAL", fmt.Sprintf("analysis %d entries (%.1f MB of %.1f GB), proxies %d (%.1f MB of %.1f GB)",
					entries, float64(cacheBytes)/(1<<20), a.Cfg.Resource.MaxCacheGB,
					proxyEntries, float64(proxyBytes)/(1<<20), a.Cfg.Resource.MaxProxyGB))
			}
		}
	}

	if bin := worker.ResolveBin(a.Cfg.Workers.MediaBin); bin == "" {
		add("Rust worker", "OPTIONAL", "not installed (analysis falls back to Go/FFmpeg)")
	} else if d, err := worker.Probe(ctx, bin); err != nil {
		add("Rust worker", "WARN", "present but unusable: "+xcerr.UserMessage(err))
	} else {
		add("Rust worker", "OPTIONAL", fmt.Sprintf("%s v%s (protocol %d, ops: %s)",
			d.Name, d.Version, d.Protocol, strings.Join(d.Ops, ",")))
	}
	if aibin := worker.ResolveAIBin(a.Cfg.Workers.AIBin); aibin == "" {
		add("AI sidecar", "OPTIONAL", "not installed (optional; set workers.ai_bin, e.g. scripts/xcut-ai-sidecar.py)")
	} else {
		ad, err := worker.Probe(ctx, aibin)
		if err != nil {
			add("AI sidecar", "WARN", "present but unusable: "+xcerr.UserMessage(err))
		} else {
			caps, cerr := worker.Capabilities(ctx, aibin)
			health, herr := worker.Health(ctx, aibin)
			detail := fmt.Sprintf("%s v%s (ops: %s)", ad.Name, ad.Version, strings.Join(ad.Ops, ","))
			if cerr == nil && len(caps.Models) > 0 {
				detail += fmt.Sprintf(", models: %d advertised", len(caps.Models))
			}
			if herr == nil && health.Ready {
				detail += ", ready"
			}
			add("AI sidecar", "OPTIONAL", detail)
		}
	}

	if gpu := detectGPU(ctx); gpu != "" {
		add("GPU", "OPTIONAL", gpu)
	} else {
		add("GPU", "OPTIONAL", "not detected (CPU-only mode)")
	}

	if v, detail := clientShellInfo(); v != "" {
		add("Client shell", "OPTIONAL", "WebView2 "+v)
	} else {
		add("Client shell", "OPTIONAL", detail)
	}

	for _, c := range checks {
		mark := map[string]string{"OK": "✓", "FAIL": "✗", "WARN": "!", "OPTIONAL": "·"}[c.Status]
		fmt.Fprintf(a.Stdout, "%-14s %s %-9s %s\n", c.Name, mark, c.Status, c.Detail)
	}
	if requiredOK {
		fmt.Fprintln(a.Stdout, "\ndoctor: required components OK")
		return nil
	}
	fmt.Fprintln(a.Stdout, "\ndoctor: FAILURES present")
	return xcerr.E(xcerr.CodeInternal, "environment check failed", nil)
}

func detectGPU(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, _, err := media.Run(ctx, "nvidia-smi", "-L")
	if err == nil && len(out) > 0 {
		line := out
		if i := bytes.IndexByte(line, '\n'); i >= 0 {
			line = line[:i]
		}
		return strings.TrimSpace(string(line))
	}
	return ""
}
