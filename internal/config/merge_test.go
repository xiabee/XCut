package config

import "testing"

func TestMergeLayerOverridesAndKeeps(t *testing.T) {
	base := Default()
	_ = Resolve(base)

	layer := &Config{
		Resource: Resource{MaxConcurrentJobs: 5, FrameSampleFPS: 4},
		Workers:  Workers{Audio: "rust"},
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
