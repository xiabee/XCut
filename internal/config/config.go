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

// MinAuthTokenLen is the shortest accepted API bearer token. Below it, an
// attacker on the network can try enough candidates to get inside the remote
// bind's rate limit (api/auth.go), so the config refuses to accept one.
const MinAuthTokenLen = 24

// Redacted returns a copy with secret-valued fields replaced by a marker, for
// any path that prints or persists configuration the operator did not write by
// hand into a file (`xcut config show`, the config.json written by `xcut init`).
// A shallow copy is enough for what Config holds: the only reference-typed field
// is Resource.ProxyEnabled (*bool), and Redacted replaces values, never writes
// through a shared pointer. A second secret-bearing field would need either a deep
// copy here or a value type — the redaction is the point of the function, so a
// leaked alias is a security bug, not a style one.
func (c *Config) Redacted() *Config {
	out := *c
	if out.Server.AuthToken != "" {
		out.Server.AuthToken = "<set>"
	}
	return &out
}

// Server controls the local HTTP API. Listen is forced to a loopback address
// unless ListenRemote is explicitly true (security default), and a remote bind
// additionally requires AuthToken (D12): the API never listens off-box without
// something to authenticate the peers it then accepts.
type Server struct {
	Listen       string `json:"listen"`
	ListenRemote bool   `json:"listen_remote"`

	// AuthToken is the bearer token non-loopback peers must present. Set it
	// in the workspace config or XCUT_AUTH_TOKEN — never a CLI flag, which
	// would publish the secret into process listings and shell history.
	AuthToken string `json:"auth_token,omitempty"`
}

// Resource is the resource budget. Every concurrency knob is a hard cap; the
// scheduler must never exceed them. Zero/negative values are repaired to
// defaults by Resolve.
type Resource struct {
	MaxConcurrentJobs  int `json:"max_concurrent_jobs"`
	MaxFFmpegProcesses int `json:"max_ffmpeg_processes"`
	// MaxAnalysisWorkers is how many assets one analyze run keeps in flight. It is
	// not the ceiling on processes: every ffmpeg/ffprobe child still asks
	// MaxFFmpegProcesses, so raising this past that changes nothing measurable
	// (docs/PERFORMANCE.md, 2026-09-23).
	MaxAnalysisWorkers int     `json:"max_analysis_workers"`
	MaxRenderWorkers   int     `json:"max_render_workers"`
	FFmpegThreads      int     `json:"ffmpeg_threads"`       // per ffmpeg/ffprobe process; 0 = default (2)
	FFmpegMaxMemoryMB  int     `json:"ffmpeg_max_memory_mb"` // per-process cap via the Windows job object; 0 = uncapped, default 1536
	ProxyThreads       int     `json:"proxy_threads"`        // one-shot proxy encode; 0 = inherit ffmpeg_threads
	MaxCacheGB         float64 `json:"max_cache_gb"`
	MaxTempGB          float64 `json:"max_temp_gb"`
	MaxProxyGB         float64 `json:"max_proxy_gb"` // analysis-proxy disk budget
	// ProxyEnabled is a pointer because a bool cannot tell "the file said false"
	// from "the file said nothing", and with the shipped default on (owner decision
	// 2026-09-23) the second reading has to mean "keep the default" while the first
	// has to win. Resolve leaves it non-nil; read it through Config.ProxyOn.
	ProxyEnabled        *bool    `json:"proxy_enabled,omitempty"` // generate low-res analysis proxies
	FrameSampleFPS      float64  `json:"frame_sample_fps"`        // sampling fps for analysis
	AnalysisWidth       int      `json:"analysis_width"`          // proxy width for analysis
	AnalyzerCallTimeout Duration `json:"analyzer_call_timeout"`   // per-analyzer ffmpeg budget; 0 = default (30m)
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

// Render tunes video encoding for renders (and the subtitle burn that can
// ride one). Encoder picks the H.264 encoder: "auto" probes the machine's
// hardware encoders (NVENC/AMF/QSV/VAAPI, platform order) and falls back to
// libx264; a named encoder its probe refuses degrades to libx264 too —
// wanting speed must never cost the render. Validated at load against
// render.EncoderNames.
type Render struct {
	Encoder string `json:"encoder"` // auto | libx264 | h264_nvenc | hevc_nvenc | h264_qsv | h264_amf | h264_vaapi
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
	Render    Render   `json:"render"`
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
			FFmpegMaxMemoryMB:   defaultFFmpegMemoryMB,
			MaxCacheGB:          10,
			MaxTempGB:           20,
			MaxProxyGB:          2,
			ProxyEnabled:        boolPtr(true),
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

func boolPtr(b bool) *bool { return &b }

// defaultFFmpegMemoryMB is the shipped per-child memory bound, named so Default()
// and Resolve()'s repair of a negative value agree by construction: the measured
// worst legitimate child on this machine is the xfade render at 566 MB, and a
// typo'd negative must not silently restore the uncapped posture the default
// replaced.
const defaultFFmpegMemoryMB = 1536

// ProxyOn is the only way to ask whether analysis should build proxies: the field
// is a pointer so a config layer can distinguish "false" from "unsaid", and every
// reader wants the answer after that question is resolved. A nil pointer reads as
// the shipped default rather than as off — Resolve normalises it, but a hand-built
// Config (a test, a tool) must not flip behavior by forgetting to.
func (c *Config) ProxyOn() bool {
	return c == nil || c.Resource.ProxyEnabled == nil || *c.Resource.ProxyEnabled
}

// Load reads one config file and returns it **sparse**: only the fields the file
// mentions come back set, everything else is the zero value. That is what
// MergeLayer's rule ("a zero keeps the base value") needs, and it is why the
// prefill that used to happen here was a defect: a workspace config.json that
// mentioned one knob arrived carrying *every* default, so merging it over a
// bootstrap config reset settings the operator had deliberately put in
// ~/.xcut/config.json. Filling gaps with Default() is the caller's job
// (cli.loadConfig layers: defaults ← bootstrap ← workspace, then env, then flags).
// A missing file is not an error; it loads as all-zero, i.e. "nothing said".
func Load(path string) (*Config, error) {
	cfg := &Config{}
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
		// defaults only — every field stays zero, and the caller's merge keeps
		// whatever is below it
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
	if v := os.Getenv("XCUT_AUTH_TOKEN"); v != "" {
		cfg.Server.AuthToken = v
	}
	if v := os.Getenv("XCUT_AI_BIN"); v != "" {
		cfg.Workers.AIBin = v
	}
	if v := os.Getenv("XCUT_PROXY_ENABLED"); v != "" {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on":
			cfg.Resource.ProxyEnabled = boolPtr(true)
		case "0", "false", "no", "off":
			cfg.Resource.ProxyEnabled = boolPtr(false)
		}
	}
}

// Resolve validates and repairs the config after defaults+file+env+flags merge.
func Resolve(cfg *Config) error {
	// Proximity lookup first: a double-clicked release exe has no PATH
	// setup, so tools placed next to the binary win before PATH does.
	neighborTools(cfg)

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
	// A negative memory cap is a typo, not a wish: 0 remains the documented way to
	// ask for uncapped, so a value below zero is repaired to the shipped default
	// rather than to the posture the default replaced.
	if r.FFmpegMaxMemoryMB < 0 {
		r.FFmpegMaxMemoryMB = defaultFFmpegMemoryMB
	}
	// Normalise the pointer so every consumer (and `xcut config show`) sees a
	// decided bool; the "unsaid" reading only exists between layers.
	if r.ProxyEnabled == nil {
		r.ProxyEnabled = boolPtr(true)
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
	// Absurd disk budgets (e.g. 1e18) overflow the GB→bytes int64
	// conversion downstream and silently disable the budget; 1e6 GB (1 PB)
	// is far past any honest local disk.
	const maxDiskGB = 1e6
	if r.MaxCacheGB > maxDiskGB {
		r.MaxCacheGB = maxDiskGB
	}
	if r.MaxTempGB > maxDiskGB {
		r.MaxTempGB = maxDiskGB
	}
	if r.MaxProxyGB > maxDiskGB {
		r.MaxProxyGB = maxDiskGB
	}
	if r.FrameSampleFPS < 0.1 || r.FrameSampleFPS > 30 {
		r.FrameSampleFPS = 2.0
	}
	if r.AnalysisWidth < 160 || r.AnalysisWidth > 3840 {
		r.AnalysisWidth = 640
	}

	// Security default: unless remote listening is explicitly enabled, pin to
	// the loopback interface regardless of the configured address.
	cfg.Server.AuthToken = strings.TrimSpace(cfg.Server.AuthToken)
	if !cfg.Server.ListenRemote {
		cfg.Server.Listen = forceLoopback(cfg.Server.Listen)
	} else if cfg.Server.AuthToken == "" {
		return xcerr.E(xcerr.CodeValidation,
			"server.listen_remote needs server.auth_token (or XCUT_AUTH_TOKEN): "+
				"a token-less remote bind would expose the whole workspace to the network; "+
				"generate one with e.g. `openssl rand -hex 24`", nil)
	}
	if cfg.Server.AuthToken != "" && len(cfg.Server.AuthToken) < MinAuthTokenLen {
		return xcerr.E(xcerr.CodeValidation,
			fmt.Sprintf("server.auth_token is too short (%d chars, need %d): a short bearer token is brute-forceable",
				len(cfg.Server.AuthToken), MinAuthTokenLen), nil)
	}
	if strings.ContainsAny(cfg.Server.AuthToken, " \t\r\n") {
		return xcerr.E(xcerr.CodeValidation,
			"server.auth_token must not contain whitespace", nil)
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
	// The encoder knob is case-folded so "NVENC" from a hand-edited config is
	// not a silent software render; unknown names are refused at load, because
	// a typo here would otherwise be discovered as "it still works, only slow".
	cfg.Render.Encoder = strings.ToLower(strings.TrimSpace(cfg.Render.Encoder))
	if cfg.Render.Encoder == "" {
		cfg.Render.Encoder = EncoderAuto
	}
	if !ValidEncoderName(cfg.Render.Encoder) {
		return xcerr.E(xcerr.CodeValidation,
			fmt.Sprintf("invalid render.encoder %q (want one of: %s)", cfg.Render.Encoder, strings.Join(EncoderNames, ", ")), nil)
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
