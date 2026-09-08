// Package cli implements the xcut command-line interface.
//
// Commands register themselves in a table; Run parses global flags, builds the
// shared App (config, logger, cancellable context), and dispatches. Keep main.go
// thin forever — all behavior lives in internal.
package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/xiabee/XCut/internal/config"
	"github.com/xiabee/XCut/internal/job"
	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/pipeline"
	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/workspace"
	"github.com/xiabee/XCut/internal/xcerr"
)

// App carries per-invocation shared state for commands.
type App struct {
	Ctx     context.Context
	Stdout  io.Writer
	Stderr  io.Writer
	Cfg     *config.Config
	CfgPath string
	Log     *slog.Logger
	Verbose bool
}

// CommandFunc runs a subcommand. Returned errors are rendered by Run.
type CommandFunc func(a *App, args []string) error

type command struct {
	name    string
	summary string
	usage   string // one-line syntax: "xcut timeline <project> [--style name]"
	fn      CommandFunc
}

var commands []command

func register(name, summary, usage string, fn CommandFunc) {
	commands = append(commands, command{name: name, summary: summary, usage: usage, fn: fn})
}

// helpFor prints a command's syntax line. Supports `xcut <cmd> -h/--help`.
func helpFor(w io.Writer, c command) {
	if c.usage != "" {
		fmt.Fprintf(w, "usage: %s\n\n%s\n", c.usage, c.summary)
		return
	}
	fmt.Fprintf(w, "usage: xcut %s\n\n%s\n", c.name, c.summary)
}

const exitOK = 0
const exitFailure = 1
const exitUsage = 2

// Run executes the CLI and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stdout)
		return exitOK
	}

	gfs := flag.NewFlagSet("xcut", flag.ContinueOnError)
	gfs.SetOutput(io.Discard) // suppress flag's own usage print; we render errors ourselves
	cfgPath := gfs.String("config", "", "path to config file (default: <workspace>/config.json)")
	workspace := gfs.String("workspace", "", "workspace directory (default: ~/.xcut, env XCUT_WORKSPACE)")
	verbose := gfs.Bool("v", false, "verbose (debug) logging")
	quiet := gfs.Bool("q", false, "quiet (warn) logging")
	if err := gfs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			usage(stdout)
			return exitOK
		}
		fmt.Fprintf(stderr, "xcut: %v\n", err)
		return exitUsage
	}
	rest := gfs.Args()
	if len(rest) == 0 {
		usage(stdout)
		return exitOK
	}

	name, cmdArgs := rest[0], rest[1:]
	cmd, ok := lookup(name)
	if !ok {
		fmt.Fprintf(stderr, "xcut: unknown command %q\n\n", name)
		usage(stderr)
		return exitUsage
	}

	// Per-command help: `xcut <cmd> -h|--help|help` never executes the
	// command (side-effect-free by construction).
	if len(cmdArgs) == 1 {
		switch cmdArgs[0] {
		case "-h", "--help", "help":
			helpFor(stdout, cmd)
			return exitOK
		}
	}

	// Context: cancelled on first Ctrl+C / SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, cfgFile, err := loadConfig(*cfgPath, *workspace)
	if err != nil {
		fmt.Fprintf(stderr, "xcut: %s\n", xcerr.UserMessage(err))
		return exitFailure
	}

	level := slog.LevelInfo
	switch {
	case *verbose:
		level = slog.LevelDebug
	case *quiet:
		level = slog.LevelWarn
	}
	log := newLogger(stderr, level)
	media.SetProcessLimit(cfg.Resource.MaxFFmpegProcesses)

	a := &App{
		Ctx:     ctx,
		Stdout:  stdout,
		Stderr:  stderr,
		Cfg:     cfg,
		CfgPath: cfgFile,
		Log:     log,
		Verbose: *verbose,
	}

	// Writers take the exclusive workspace lock so two XCut processes can
	// never mutate one workspace concurrently (DECISIONS.md). Readers never
	// block, so `xcut jobs` works while serve runs. Crash safety: a lock
	// whose owner process is gone is removed automatically.
	if writesWorkspace(name, cmdArgs) {
		ws := a.Workspace()
		release, lerr := ws.Acquire(name)
		if lerr != nil {
			fmt.Fprintf(stderr, "xcut %s: %s\n", name, xcerr.UserMessage(lerr))
			return exitFailure
		}
		defer release()
	}

	if err := cmd.fn(a, cmdArgs); err != nil {
		if xcerr.IsCode(err, xcerr.CodeCancelled) || ctx.Err() != nil {
			fmt.Fprintf(stderr, "xcut %s: cancelled\n", name)
			return 130
		}
		fmt.Fprintf(stderr, "xcut %s: %s\n", name, err)
		log.Debug("command failed", "cmd", name, "err", err)
		return exitFailure
	}
	return exitOK
}

func lookup(name string) (command, bool) {
	for _, c := range commands {
		if c.name == name {
			return c, true
		}
	}
	return command{}, false
}

// writesWorkspace classifies commands by whether they mutate workspace state
// (DB, projects, cache, temp). Anything not listed is treated read-only —
// reads stay lock-free, so they work while another process holds the lock.
// `eval` writes only to a throwaway temp workspace and never touches this
// one; `serve` is a writer (it serves write API calls) and holds the lock
// for its lifetime.
func writesWorkspace(name string, args []string) bool {
	switch name {
	case "init", "import", "analyze", "timeline", "render", "auto",
		"cleanup", "serve":
		return true
	case "project":
		// list/show read; create/delete write.
		return len(args) > 0 && (args[0] == "create" || args[0] == "delete")
	case "cache":
		// stats reads; clear writes.
		return len(args) > 0 && args[0] == "clear"
	}
	return false
}

// loadConfig resolves effective configuration in two layers: a bootstrap
// config (explicit path, XCUT_CONFIG, or ~/.xcut/config.json) determines the
// workspace; when the effective workspace has its own config.json and no
// explicit config path was given, that file is layered on top (workspace
// config wins for keys it sets). Env vars apply to both layers; the resolved
// path returned is the most specific file used ("" when defaults only).
func loadConfig(flagPath, flagWorkspace string) (*config.Config, string, error) {
	explicit := flagPath != "" || os.Getenv("XCUT_CONFIG") != ""
	path := flagPath
	if path == "" {
		path = os.Getenv("XCUT_CONFIG")
	}
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, "", xcerr.E(xcerr.CodeInternal, "cannot locate home directory", err)
		}
		path = filepath.Join(home, ".xcut", "config.json")
	}

	cfg, err := config.Load(path)
	if err != nil {
		return nil, "", err
	}
	config.Env(cfg)
	if ws := firstNonEmpty(flagWorkspace, os.Getenv("XCUT_WORKSPACE")); ws != "" {
		cfg.Workspace = ws
	}
	if err := config.Resolve(cfg); err != nil {
		return nil, "", err
	}

	// Layer 2: the effective workspace's own config (when it differs from the
	// bootstrap file's location).
	if !explicit {
		wsCfgPath := filepath.Join(cfg.Workspace, "config.json")
		if sameFile(wsCfgPath, path) {
			return cfg, path, nil
		}
		if _, err := os.Stat(wsCfgPath); err == nil {
			wsCfg, err := config.Load(wsCfgPath)
			if err != nil {
				return nil, "", err
			}
			merged := config.MergeLayer(cfg, wsCfg)
			config.Env(merged)
			if ws := firstNonEmpty(flagWorkspace, os.Getenv("XCUT_WORKSPACE")); ws != "" {
				merged.Workspace = ws
			}
			if err := config.Resolve(merged); err != nil {
				return nil, "", err
			}
			return merged, wsCfgPath, nil
		}
	}
	return cfg, path, nil
}

func sameFile(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	fa, err1 := os.Stat(a)
	fb, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(fa, fb)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// Workspace builds the workspace handle from effective config.
func (a *App) Workspace() *workspace.Workspace { return workspace.New(a.Cfg.Workspace) }

// Pipeline builds the shared pipeline operations bound to an open DB.
func (a *App) Pipeline(db *storage.DB) pipeline.Deps {
	return pipeline.NewDeps(a.Ctx, db, a.Workspace(), a.Cfg, a.Log)
}

// parseCommandArgs splits command args into string flags and positionals.
// Flags may appear anywhere: `xcut timeline proj --style x` and
// `xcut timeline --style x proj` both work (Go's flag package stops at the
// first positional, which would make the first form fail).
// flags maps long names (--name) to destinations; unknown flags error.
func parseCommandArgs(args []string, flags map[string]*string) ([]string, error) {
	var pos []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-" || !strings.HasPrefix(arg, "--") {
			pos = append(pos, arg)
			continue
		}
		body := strings.TrimPrefix(arg, "--")
		name, value, hasValue := strings.Cut(body, "=")
		dst, ok := flags[name]
		if !ok {
			return nil, xcerr.E(xcerr.CodeValidation, "unknown flag --"+name, nil)
		}
		if !hasValue {
			if i+1 >= len(args) {
				return nil, xcerr.E(xcerr.CodeValidation, "flag --"+name+" needs a value", nil)
			}
			i++
			value = args[i]
		}
		*dst = value
	}
	return pos, nil
}

// OpenDB ensures the workspace exists, opens the database, and reconciles
// orphaned jobs left by previous dead processes.
func (a *App) OpenDB() (*storage.DB, error) {
	ws := a.Workspace()
	if err := ws.Ensure(); err != nil {
		return nil, err
	}
	db, err := storage.Open(ws.DBPath())
	if err != nil {
		return nil, err
	}
	q := job.NewQueue(db, a.Cfg.Resource.MaxConcurrentJobs, a.Log)
	ctx, cancel := context.WithTimeout(a.Ctx, 15*time.Second)
	defer cancel()
	if _, err := q.ReconcileOrphans(ctx, a.Cfg.Job.StaleRunningAfter.Duration); err != nil {
		a.Log.Warn("job reconciliation failed", "err", err)
	}
	return db, nil
}

func newLogger(w io.Writer, level slog.Level) *slog.Logger {
	h := slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})
	return slog.New(h)
}

func usage(w io.Writer) {
	fmt.Fprintf(w, "xcut — local-first automatic video editing\n\n")
	fmt.Fprintf(w, "Usage: xcut [global flags] <command> [args]\n\n")
	fmt.Fprintf(w, "Global flags:\n")
	fmt.Fprintf(w, "  --config <path>    config file (default <workspace>/config.json, env XCUT_CONFIG)\n")
	fmt.Fprintf(w, "  --workspace <dir>  workspace directory (default ~/.xcut, env XCUT_WORKSPACE)\n")
	fmt.Fprintf(w, "  -v / -q            verbose / quiet logging\n")
	fmt.Fprintf(w, "\nCommands:\n")
	for _, c := range commands {
		fmt.Fprintf(w, "  %-10s %s\n", c.name, c.summary)
	}
	fmt.Fprintf(w, "\nxcut %s on %s/%s\n", versionShort(), runtime.GOOS, runtime.GOARCH)
}

// usageSyntax marks a command's syntax line (identity helper; keeps call
// sites readable: register(name, summary, usageSyntax("xcut …"), fn)).
func usageSyntax(s string) string { return s }
