package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xiabee/XCut/internal/analysis"
	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/version"
	"github.com/xiabee/XCut/internal/worker"
	"github.com/xiabee/XCut/internal/workspace"
	"github.com/xiabee/XCut/internal/xcerr"
)

func init() {
	register("doctor", "check environment (ffmpeg, workspace, disk, db, optional workers)", cmdDoctor)
	register("init", "create workspace layout and default config", cmdInit)
	register("cleanup", "remove temp files and evict analysis cache to budget (cleanup [--dry-run])", cmdCleanup)
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
		b, _ := json.MarshalIndent(a.Cfg, "", "  ")
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
	evicted, evictedBytes, err := store.EvictTo(budget)
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

	if !dryRun && (len(removed) > 0 || evicted > 0) {
		a.Log.Info("cleanup done", "temp_entries", len(removed), "temp_bytes", tempBytes,
			"cache_evicted", evicted, "cache_bytes", evictedBytes)
	}
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

	if bin := worker.ResolveBin(a.Cfg.Workers.MediaBin); bin == "" {
		add("Rust worker", "OPTIONAL", "not installed (analysis falls back to Go/FFmpeg)")
	} else if d, err := worker.Probe(ctx, bin); err != nil {
		add("Rust worker", "WARN", "present but unusable: "+xcerr.UserMessage(err))
	} else {
		add("Rust worker", "OPTIONAL", fmt.Sprintf("%s v%s (protocol %d, ops: %s)",
			d.Name, d.Version, d.Protocol, strings.Join(d.Ops, ",")))
	}
	add("AI worker", "OPTIONAL", "not installed (not required)")

	if gpu := detectGPU(ctx); gpu != "" {
		add("GPU", "OPTIONAL", gpu)
	} else {
		add("GPU", "OPTIONAL", "not detected (CPU-only mode)")
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
