package config

import (
	"testing"
	"time"
)

func TestMergeLayerOverridesAndKeeps(t *testing.T) {
	base := Default()
	_ = Resolve(base)

	layer := &Config{
		Resource: Resource{MaxConcurrentJobs: 5, FrameSampleFPS: 4},
		Workers:  Workers{Audio: "rust", AIBin: "C:/sidecars/fake-ai.py"},
		Log:      Log{Level: "debug"},
	}
	got := MergeLayer(base, layer)

	if got.Resource.MaxConcurrentJobs != 5 {
		t.Fatalf("layered jobs = %d", got.Resource.MaxConcurrentJobs)
	}
	if got.Resource.FrameSampleFPS != 4 {
		t.Fatalf("layered fps = %v", got.Resource.FrameSampleFPS)
	}
	if got.Workers.Audio != "rust" {
		t.Fatalf("layered audio = %q", got.Workers.Audio)
	}
	if got.Workers.AIBin != "C:/sidecars/fake-ai.py" {
		t.Fatalf("layered ai_bin = %q (workspace workers.ai_bin must not be dropped)", got.Workers.AIBin)
	}
	if got.Log.Level != "debug" {
		t.Fatalf("layered log level = %q", got.Log.Level)
	}
	// Untouched fields keep base values.
	if got.Resource.MaxCacheGB != base.Resource.MaxCacheGB {
		t.Fatalf("cache budget changed: %v", got.Resource.MaxCacheGB)
	}
	if got.Server.Listen != base.Server.Listen {
		t.Fatalf("listen changed: %v", got.Server.Listen)
	}
	// Base must not be mutated.
	if base.Resource.MaxConcurrentJobs == 5 {
		t.Fatal("base config mutated by merge")
	}
}

// TestMergeLayerCarriesNewerResourceKnobs: the proxy/timeout knobs postdate
// the merge function and were silently dropped from the workspace layer —
// config show shows the merged result, so nothing looked wrong.
func TestMergeLayerCarriesNewerResourceKnobs(t *testing.T) {
	base := Default()
	_ = Resolve(base)

	d := Duration{5 * time.Minute}
	layer := &Config{
		Resource: Resource{
			ProxyThreads:        3,
			MaxProxyGB:          4,
			AnalyzerCallTimeout: d,
			ProxyEnabled:        true,
		},
	}
	got := MergeLayer(base, layer)
	if got.Resource.ProxyThreads != 3 {
		t.Fatalf("layered proxy_threads = %d", got.Resource.ProxyThreads)
	}
	if got.Resource.MaxProxyGB != 4 {
		t.Fatalf("layered max_proxy_gb = %v", got.Resource.MaxProxyGB)
	}
	if got.Resource.AnalyzerCallTimeout.Duration != 5*time.Minute {
		t.Fatalf("layered analyzer_call_timeout = %v", got.Resource.AnalyzerCallTimeout.Duration)
	}
	if !got.Resource.ProxyEnabled {
		t.Fatal("workspace proxy_enabled=true must opt in")
	}
	// The bool cannot express "unset": a false layer keeps the base value
	// (turning it off is the bootstrap/env/flag layer's job).
	base2 := Default()
	base2.Resource.ProxyEnabled = true
	got2 := MergeLayer(base2, &Config{})
	if !got2.Resource.ProxyEnabled {
		t.Fatal("false in the layer must keep the base value")
	}
}
