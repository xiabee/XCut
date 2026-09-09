package config

import (
	"os"
	"path/filepath"
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

func TestLoadMissingFileUsesDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Resource.MaxConcurrentJobs != 2 {
		t.Fatalf("defaults not applied: %+v", cfg.Resource)
	}
}

func TestProxyKnobs(t *testing.T) {
	cfg := Default()
	if cfg.Resource.ProxyEnabled {
		t.Error("proxy_enabled must default to false (no silent behavior change)")
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
	Env(cfg2)
	if !cfg2.Resource.ProxyEnabled {
		t.Error("XCUT_PROXY_ENABLED=1 must enable proxies")
	}
	t.Setenv("XCUT_PROXY_ENABLED", "off")
	cfg3 := Default()
	cfg3.Resource.ProxyEnabled = true
	Env(cfg3)
	if cfg3.Resource.ProxyEnabled {
		t.Error("XCUT_PROXY_ENABLED=off must disable proxies")
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
