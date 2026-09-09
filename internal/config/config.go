// Package config defines XCut's effective configuration: defaults, optional
// JSON file, and environment overrides.
//
// Precedence (low → high): defaults < config file < environment < CLI flags.
// Flags are applied by the caller (cli package) after Load+Resolve.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/xiabee/XCut/internal/xcerr"
)

// Server controls the future local HTTP API. Listen is forced to a loopback
// address unless ListenRemote is explicitly true (security default).
type Server struct {
	Listen       string `json:"listen"`
	ListenRemote bool   `json:"listen_remote"`
}

// Resource is the resource budget. Every concurrency knob is a hard cap; the
// scheduler must never exceed them. Zero/negative values are repaired to
// defaults by Resolve.
type Resource struct {
	MaxConcurrentJobs   int      `json:"max_concurrent_jobs"`
	MaxFFmpegProcesses  int      `json:"max_ffmpeg_processes"`
	MaxAnalysisWorkers  int      `json:"max_analysis_workers"`
	MaxRenderWorkers    int      `json:"max_render_workers"`
	FFmpegThreads       int      `json:"ffmpeg_threads"` // per ffmpeg/ffprobe process; 0 = default (2)
	ProxyThreads        int      `json:"proxy_threads"`  // one-shot proxy encode; 0 = inherit ffmpeg_threads
	MaxCacheGB          float64  `json:"max_cache_gb"`
	MaxTempGB           float64  `json:"max_temp_gb"`
	MaxProxyGB          float64  `json:"max_proxy_gb"`          // analysis-proxy disk budget
	ProxyEnabled        bool     `json:"proxy_enabled"`         // generate low-res analysis proxies
	FrameSampleFPS      float64  `json:"frame_sample_fps"`      // sampling fps for analysis
	AnalysisWidth       int      `json:"analysis_width"`        // proxy width for analysis
	AnalyzerCallTimeout Duration `json:"analyzer_call_timeout"` // per-analyzer ffmpeg budget; 0 = default (30m)
}

// FFmpeg locates external binaries. Empty means "resolve from PATH".
type FFmpeg struct {
	Bin      string `json:"bin"`
	ProbeBin string `json:"ffprobe_bin"`
}

// Job holds job-system tuning.
type Job struct {
	// StaleRunningAfter: a DB row still "running" older than this at startup is
	// reconciled as failed (crash recovery).
	StaleRunningAfter Duration `json:"stale_running_after"`
	// MaxHistory caps terminal (succeeded/failed/cancelled) job rows kept;
	// older rows are pruned as jobs finish. 0 = default (500).
	MaxHistory int `json:"max_history"`
}

// Log controls structured logging growth.
type Log struct {
	Level     string `json:"level"`       // debug|info|warn|error
	MaxSizeMB int    `json:"max_size_mb"` // rotate threshold; 0 = 50
	MaxFiles  int    `json:"max_files"`   // rotated files kept; 0 = 3
}

// Workers configures optional helper workers (never required).
type Workers struct {
	// MediaBin locates the Rust media worker; "" = PATH lookup of
	// xcut-worker-media.
	MediaBin string `json:"media_bin"`
	// Audio selects the audio analyzer: auto | ffmpeg | rust.
	Audio string `json:"audio"`
	// AIBin locates an optional AI sidecar (script or binary); "" = PATH
	// lookup of xcut-ai. Missing sidecar = AI capabilities absent.
	AIBin string `json:"ai_bin,omitempty"`
}

// Config is the full effective configuration.
type Config struct {
	Workspace string   `json:"workspace"`
	Log       Log      `json:"log"`
	Server    Server   `json:"server"`
	Resource  Resource `json:"resource"`
	FFmpeg    FFmpeg   `json:"ffmpeg"`
	Job       Job      `json:"job"`
	Workers   Workers  `json:"workers"`
}

// Duration wraps time.Duration for JSON: accepts "2h", "30m", or seconds number.
type Duration struct{ time.Duration }

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.Duration.String())
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" {
		d.Duration = 0
		return nil
	}
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
		v, err := time.ParseDuration(s)
		if err != nil {
			return fmt.Errorf("invalid duration %q", s)
		}
		d.Duration = v
		return nil
	}
	secs, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("invalid duration %s", s)
	}
	d.Duration = time.Duration(secs * float64(time.Second))
	return nil
}

// Default returns the built-in defaults.
func Default() *Config {
	return &Config{
		Workspace: "",
		Log:       Log{Level: "info", MaxSizeMB: 50, MaxFiles: 3},
		Server:    Server{Listen: "127.0.0.1:8619", ListenRemote: false},
		Resource: Resource{
			MaxConcurrentJobs:   2,
			MaxFFmpegProcesses:  2,
			MaxAnalysisWorkers:  2,
			MaxRenderWorkers:    1,
			FFmpegThreads:       2,
			MaxCacheGB:          10,
			MaxTempGB:           20,
			MaxProxyGB:          2,
			FrameSampleFPS:      2.0,
			AnalyzerCallTimeout: Duration{30 * time.Minute},
			AnalysisWidth:       640,
		},
		FFmpeg:  FFmpeg{},
		Job:     Job{StaleRunningAfter: Duration{2 * time.Hour}, MaxHistory: 500},
		Workers: Workers{MediaBin: "", Audio: "auto"},
	}
}

// maxConfigBytes caps config file size (cheap DoS guard: a config is a few
// KB by design).
const maxConfigBytes = 1 << 20 // 1 MiB

// Load reads the config file if present. Missing file is not an error.
func Load(path string) (*Config, error) {
	cfg := Default()
	if fi, err := os.Stat(path); err == nil && fi.Size() > maxConfigBytes {
		return nil, xcerr.E(xcerr.CodeValidation,
			fmt.Sprintf("config file too large (%d bytes)", fi.Size()), nil)
	}
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		dec := json.NewDecoder(strings.NewReader(string(b)))
		if err := dec.Decode(cfg); err != nil {
			return nil, xcerr.E(xcerr.CodeValidation,
				fmt.Sprintf("invalid config file %s: %v", filepath.Base(path), err), err)
		}
	case os.IsNotExist(err):
		// defaults only
	default:
		return nil, xcerr.E(xcerr.CodeInternal, "cannot read config file", err)
	}
	return cfg, nil
}

// Env applies environment variable overrides.
func Env(cfg *Config) {
	if v := os.Getenv("XCUT_WORKSPACE"); v != "" {
		cfg.Workspace = v
	}
	if v := os.Getenv("XCUT_FFMPEG"); v != "" {
		cfg.FFmpeg.Bin = v
	}
	if v := os.Getenv("XCUT_FFPROBE"); v != "" {
		cfg.FFmpeg.ProbeBin = v
	}
	if v := os.Getenv("XCUT_LOG_LEVEL"); v != "" {
		cfg.Log.Level = v
	}
	if v := os.Getenv("XCUT_LISTEN"); v != "" {
		cfg.Server.Listen = v
	}
	if v := os.Getenv("XCUT_AI_BIN"); v != "" {
		cfg.Workers.AIBin = v
	}
	if v := os.Getenv("XCUT_PROXY_ENABLED"); v != "" {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on":
			cfg.Resource.ProxyEnabled = true
		case "0", "false", "no", "off":
			cfg.Resource.ProxyEnabled = false
		}
	}
}

// Resolve validates and repairs the config after defaults+file+env+flags merge.
func Resolve(cfg *Config) error {
	if ws := strings.TrimSpace(cfg.Workspace); ws == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return xcerr.E(xcerr.CodeInternal, "cannot locate home directory; set workspace explicitly", err)
		}
		cfg.Workspace = filepath.Join(home, ".xcut")
	}

	cfg.Log.Level = strings.ToLower(strings.TrimSpace(cfg.Log.Level))
	switch cfg.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		return xcerr.E(xcerr.CodeValidation,
			fmt.Sprintf("invalid log.level %q (want debug|info|warn|error)", cfg.Log.Level), nil)
	}
	if cfg.Log.MaxSizeMB <= 0 {
		cfg.Log.MaxSizeMB = 50
	}
	if cfg.Log.MaxFiles <= 0 {
		cfg.Log.MaxFiles = 3
	}

	r := &cfg.Resource
	floor := func(v, min int, def int) int {
		if v < min {
			return def
		}
		return v
	}
	r.MaxConcurrentJobs = floor(r.MaxConcurrentJobs, 1, 2)
	r.MaxFFmpegProcesses = floor(r.MaxFFmpegProcesses, 1, 2)
	r.MaxAnalysisWorkers = floor(r.MaxAnalysisWorkers, 1, 2)
	r.MaxRenderWorkers = floor(r.MaxRenderWorkers, 1, 1)
	if r.FFmpegThreads < 0 {
		r.FFmpegThreads = 2
	}
	if r.ProxyThreads < 0 {
		r.ProxyThreads = 0 // 0 = inherit ffmpeg_threads
	}
	if r.AnalyzerCallTimeout.Duration <= 0 {
		r.AnalyzerCallTimeout = Duration{30 * time.Minute}
	}
	if r.MaxCacheGB <= 0 {
		r.MaxCacheGB = 10
	}
	if r.MaxTempGB <= 0 {
		r.MaxTempGB = 20
	}
	if r.MaxProxyGB <= 0 {
		r.MaxProxyGB = 2
	}
	if r.FrameSampleFPS < 0.1 || r.FrameSampleFPS > 30 {
		r.FrameSampleFPS = 2.0
	}
	if r.AnalysisWidth < 160 || r.AnalysisWidth > 3840 {
		r.AnalysisWidth = 640
	}

	// Security default: unless remote listening is explicitly enabled, pin to
	// the loopback interface regardless of the configured address.
	if !cfg.Server.ListenRemote {
		cfg.Server.Listen = forceLoopback(cfg.Server.Listen)
	}
	if cfg.Server.Listen == "" {
		cfg.Server.Listen = "127.0.0.1:8619"
	}
	if cfg.Job.StaleRunningAfter.Duration <= 0 {
		cfg.Job.StaleRunningAfter = Duration{2 * time.Hour}
	}
	if cfg.Job.MaxHistory <= 0 {
		cfg.Job.MaxHistory = 500
	}
	switch cfg.Workers.Audio {
	case "", "auto", "ffmpeg", "rust":
	default:
		return xcerr.E(xcerr.CodeValidation,
			fmt.Sprintf("invalid workers.audio %q (want auto|ffmpeg|rust)", cfg.Workers.Audio), nil)
	}
	return nil
}

func forceLoopback(addr string) string {
	a := strings.TrimSpace(addr)
	if a == "" {
		return "127.0.0.1:8619"
	}
	_, port, err := splitHostPort(a)
	if err != nil || port == "" {
		return "127.0.0.1:8619"
	}
	return "127.0.0.1:" + port
}

func splitHostPort(addr string) (string, string, error) {
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return addr, "", fmt.Errorf("missing port in %q", addr)
	}
	return addr[:i], addr[i+1:], nil
}
