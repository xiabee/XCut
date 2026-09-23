package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDurationJSON(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{`"2h"`, 2 * time.Hour},
		{`"90s"`, 90 * time.Second},
		{`45`, 45 * time.Second},
	}
	for _, c := range cases {
		var d Duration
		if err := d.UnmarshalJSON([]byte(c.in)); err != nil {
			t.Fatalf("unmarshal %s: %v", c.in, err)
		}
		if d.Duration != c.want {
			t.Fatalf("unmarshal %s = %v, want %v", c.in, d.Duration, c.want)
		}
	}
	var d Duration
	if err := d.UnmarshalJSON([]byte(`"bogus"`)); err == nil {
		t.Fatal("expected error for bogus duration")
	}
}

func TestResolveForcesLoopback(t *testing.T) {
	cfg := Default()
	cfg.Server.Listen = "0.0.0.0:9999"
	if err := Resolve(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Listen != "127.0.0.1:9999" {
		t.Fatalf("listen = %q, want loopback", cfg.Server.Listen)
	}

	cfg2 := Default()
	cfg2.Server.ListenRemote = true
	cfg2.Server.AuthToken = strings.Repeat("x", 32) // fixture, not a credential
	cfg2.Server.Listen = "0.0.0.0:9999"
	if err := Resolve(cfg2); err != nil {
		t.Fatal(err)
	}
	if cfg2.Server.Listen != "0.0.0.0:9999" {
		t.Fatalf("listen_remote=true should keep explicit listen, got %q", cfg2.Server.Listen)
	}
}

func TestResolveRepairsBadResource(t *testing.T) {
	cfg := Default()
	cfg.Resource.MaxConcurrentJobs = -3
	cfg.Resource.FrameSampleFPS = 0
	cfg.Resource.AnalysisWidth = 999999
	if err := Resolve(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Resource.MaxConcurrentJobs != 2 {
		t.Fatalf("MaxConcurrentJobs = %d, want repaired default 2", cfg.Resource.MaxConcurrentJobs)
	}
	if cfg.Resource.FrameSampleFPS != 2.0 {
		t.Fatalf("FrameSampleFPS = %v, want 2.0", cfg.Resource.FrameSampleFPS)
	}
	if cfg.Resource.AnalysisWidth != 640 {
		t.Fatalf("AnalysisWidth = %d, want 640", cfg.Resource.AnalysisWidth)
	}
}

// The shipped memory cap is 1536 MB per ffmpeg child (owner decision 2026-09-23):
// the largest legitimate child measured on this machine is the xfade render at
// 566 MB, and the analysis fan-out costs ~54 MB per 720p child, so a default cap
// sits well above any real workload while a runaway encoder is now bounded out of
// the box. 0 still means "uncapped" for anyone who asks for that; a negative value
// is a typo and is repaired to the shipped cap rather than to uncapped, because
// silently restoring the old posture is the worse mistake of the two.
func TestResolveFFmpegMemoryCap(t *testing.T) {
	ship := Default().Resource.FFmpegMaxMemoryMB
	if ship != 1536 {
		t.Fatalf("default FFmpegMaxMemoryMB = %d, want the shipped cap 1536", ship)
	}
	cfg := Default()
	cfg.Resource.FFmpegMaxMemoryMB = 4096
	if err := Resolve(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Resource.FFmpegMaxMemoryMB != 4096 {
		t.Fatalf("FFmpegMaxMemoryMB = %d, want 4096 preserved", cfg.Resource.FFmpegMaxMemoryMB)
	}
	cfg.Resource.FFmpegMaxMemoryMB = 0
	if err := Resolve(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Resource.FFmpegMaxMemoryMB != 0 {
		t.Fatalf("an explicit 0 (uncapped) = %d, want it honoured", cfg.Resource.FFmpegMaxMemoryMB)
	}
	cfg.Resource.FFmpegMaxMemoryMB = -1
	if err := Resolve(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Resource.FFmpegMaxMemoryMB != ship {
		t.Fatalf("negative cap = %d, want the shipped cap %d (a typo must not disable the bound)",
			cfg.Resource.FFmpegMaxMemoryMB, ship)
	}
}

func TestLoadInvalidFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	if err := os.WriteFile(p, []byte(`{"resource": {"max_concurrent_jobs": "x"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("expected validation error for invalid JSON")
	}
}

// Load is sparse by contract: a missing file says nothing, so nothing is set. The
// defaults come from the layer below it at merge time — which is the whole point,
// and the thing a prefill here used to defeat (a workspace config.json that
// mentioned one knob arrived carrying every default and reset a bootstrap file's
// settings). This test pins both halves: sparse in, defaults out after the merge.
func TestLoadMissingFileIsSparseAndMergesToDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Resource.MaxConcurrentJobs != 0 || cfg.Log.Level != "" || cfg.Server.Listen != "" {
		t.Fatalf("a missing file must load as nothing said, got %+v / %q / %q",
			cfg.Resource, cfg.Log.Level, cfg.Server.Listen)
	}
	merged := MergeLayer(Default(), cfg)
	if merged.Resource.MaxConcurrentJobs != 2 || merged.Log.Level != "info" ||
		merged.Server.Listen != "127.0.0.1:8619" {
		t.Fatalf("defaults not applied through the merge: %+v", merged.Resource)
	}
}

// The reset this replaced: the upper layer may only change what it mentions.
func TestLoadPresentFileLeavesUnmentionedFieldsZero(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(`{"resource":{"max_cache_gb":7}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Resource.MaxCacheGB != 7 {
		t.Fatalf("the mentioned field was lost: %+v", cfg.Resource)
	}
	if cfg.Resource.MaxConcurrentJobs != 0 || cfg.Resource.AnalysisWidth != 0 || cfg.Job.MaxHistory != 0 {
		t.Fatalf("unmentioned fields must stay zero for the merge to see them as unsaid: %+v", cfg.Resource)
	}
}

// Proxies are on by default as of the owner decision on 2026-09-23: repeated
// analysis measured 10-20x cheaper with them (docs/PERFORMANCE.md) and
// resource.max_proxy_gb was a ceiling over an empty directory. It has to be
// switchable off by name, which is why the field is a pointer — a bool cannot
// tell "the file said false" from "the file said nothing", and the old one-way
// merge let a workspace opt in but never out.
func TestProxyKnobs(t *testing.T) {
	cfg := Default()
	if !cfg.ProxyOn() {
		t.Error("proxy_enabled must default to true (the 2 GiB budget governs something real now)")
	}
	if cfg.Resource.MaxProxyGB != 2 {
		t.Errorf("max_proxy_gb default %v, want 2", cfg.Resource.MaxProxyGB)
	}
	cfg.Resource.MaxProxyGB = 0
	if err := Resolve(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Resource.MaxProxyGB != 2 {
		t.Errorf("Resolve must repair max_proxy_gb to 2, got %v", cfg.Resource.MaxProxyGB)
	}

	t.Setenv("XCUT_PROXY_ENABLED", "1")
	cfg2 := Default()
	cfg2.Resource.ProxyEnabled = nil // pretend nothing else said anything
	Env(cfg2)
	if cfg2.ProxyOn() != true {
		t.Error("XCUT_PROXY_ENABLED=1 must enable proxies")
	}
	t.Setenv("XCUT_PROXY_ENABLED", "off")
	cfg3 := Default()
	Env(cfg3)
	if cfg3.ProxyOn() {
		t.Error("XCUT_PROXY_ENABLED=off must disable proxies")
	}
}

// An unresolved Config is the case ProxyOn's nil branch exists for: Resolve
// normalises the pointer, so the only way to reach a nil is a hand-built one (a
// test, a tool, a future seam) — and such a config must read as the shipped
// posture, not as "off", because turning proxies silently off would be the exact
// surprise the default flip was decided against.
func TestProxyOnDefaultsInAnUnresolvedConfig(t *testing.T) {
	if !(&Config{}).ProxyOn() {
		t.Error("an all-zero Config read as proxies off; the nil arm must mean the shipped default")
	}
	var none *Config
	if !none.ProxyOn() {
		t.Error("a nil Config read as off where the accessor promises the default")
	}
	off := false
	if (&Config{Resource: Resource{ProxyEnabled: &off}}).ProxyOn() {
		t.Error("an explicit false must read as false")
	}
}

// TestProxyOffIsExpressibleThroughAFile is the capability the pointer bought: an
// operator with a small disk says no once, and the layering carries it.
func TestProxyOffIsExpressibleThroughAFile(t *testing.T) {
	off := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(off, []byte(`{"resource":{"proxy_enabled":false}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(off)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Resource.ProxyEnabled == nil {
		t.Fatal("an explicit false loaded as nothing said — the merge cannot tell it from an absent key")
	}
	merged := MergeLayer(Default(), cfg)
	if merged.ProxyOn() {
		t.Error("proxy_enabled: false in a file must survive the merge over the default")
	}
	// And a file that never mentions it must not turn the shipped default off.
	quiet := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(quiet, []byte(`{"resource":{"analysis_width":320}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	said, err := Load(quiet)
	if err != nil {
		t.Fatal(err)
	}
	if said.Resource.ProxyEnabled != nil {
		t.Error("an unmentioned proxy_enabled must load as nil, not as a choice")
	}
	if !MergeLayer(Default(), said).ProxyOn() {
		t.Error("the shipped default was silenced by a file that never mentioned it")
	}
}

func TestProxyThreadsKnob(t *testing.T) {
	cfg := Default()
	if cfg.Resource.ProxyThreads != 0 {
		t.Errorf("proxy_threads default %d, want 0 (inherit ffmpeg_threads)", cfg.Resource.ProxyThreads)
	}
	cfg.Resource.ProxyThreads = -3
	if err := Resolve(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Resource.ProxyThreads != 0 {
		t.Errorf("Resolve must repair negative proxy_threads to 0, got %d", cfg.Resource.ProxyThreads)
	}
	cfg.Resource.ProxyThreads = 8
	if err := Resolve(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Resource.ProxyThreads != 8 {
		t.Errorf("explicit proxy_threads must survive Resolve, got %d", cfg.Resource.ProxyThreads)
	}
}

func TestMaxHistoryKnob(t *testing.T) {
	cfg := Default()
	if cfg.Job.MaxHistory != 500 {
		t.Fatalf("default max_history = %d, want 500", cfg.Job.MaxHistory)
	}
	if err := Resolve(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Job.MaxHistory != 500 {
		t.Fatalf("resolved max_history = %d, want 500", cfg.Job.MaxHistory)
	}

	// Explicit values survive; invalid ones are repaired to the default.
	cfg2 := Default()
	cfg2.Job.MaxHistory = 42
	if err := Resolve(cfg2); err != nil {
		t.Fatal(err)
	}
	if cfg2.Job.MaxHistory != 42 {
		t.Fatalf("explicit max_history lost: %d", cfg2.Job.MaxHistory)
	}
	cfg3 := Default()
	cfg3.Job.MaxHistory = -1
	if err := Resolve(cfg3); err != nil {
		t.Fatal(err)
	}
	if cfg3.Job.MaxHistory != 500 {
		t.Fatalf("negative max_history must repair to 500, got %d", cfg3.Job.MaxHistory)
	}
}

// TestResolveClampsAbsurdDiskBudgets: a huge float budget (1e18 GB) used to
// overflow the GB→bytes int64 conversion downstream, silently disabling the
// budget; Resolve must clamp to an honest ceiling.
func TestResolveClampsAbsurdDiskBudgets(t *testing.T) {
	cfg := Default()
	cfg.Resource.MaxCacheGB = 1e18
	cfg.Resource.MaxTempGB = 1e18
	cfg.Resource.MaxProxyGB = 1e18
	if err := Resolve(cfg); err != nil {
		t.Fatal(err)
	}
	const maxDiskGB = 1e6
	if cfg.Resource.MaxCacheGB > maxDiskGB || cfg.Resource.MaxTempGB > maxDiskGB || cfg.Resource.MaxProxyGB > maxDiskGB {
		t.Fatalf("absurd budgets not clamped: %+v", cfg.Resource)
	}
	// The downstream conversion must stay positive.
	if b := int64(cfg.Resource.MaxTempGB * (1 << 30)); b <= 0 {
		t.Fatalf("temp budget bytes overflow: %d", b)
	}
}
