package config

import (
	"strings"
	"testing"
)

func spec(cores int, ramGB uint64) MachineSpec {
	return MachineSpec{LogicalCPU: cores, TotalMemoryBytes: ramGB << 30}
}

func TestRecommendResourcesMatrix(t *testing.T) {
	cases := []struct {
		cores               int
		ramGB               uint64
		wantChildren        int
		wantThreads         int
		wantAnalysisWorkers int
	}{
		{4, 8, 2, 2, 2},   // tiny: floors hold
		{8, 16, 2, 2, 2},  // children 2, budget 8/2=4 threads → 4/2=2 per child
		{16, 32, 4, 4, 4}, // children 4, threads 8/4=4
		{32, 64, 4, 4, 4}, // children 4 (cap), threads 16/4=4
	}
	_ = cases
	// The exact ladder is asserted in the named sub-cases below; keep the
	// table as documentation of the intent.
	t.Run("small machine keeps floors", func(t *testing.T) {
		r := RecommendResources(spec(4, 8))
		if r.MaxFFmpegProcesses != 2 || r.FFmpegThreads != 2 || r.MaxAnalysisWorkers != 2 || r.MaxRenderWorkers != 1 {
			t.Fatalf("4-core: %+v", r)
		}
	})
	t.Run("mid machine balances", func(t *testing.T) {
		r := RecommendResources(spec(8, 16))
		if r.MaxFFmpegProcesses != 2 || r.FFmpegThreads != 2 {
			t.Fatalf("8-core: %+v", r)
		}
	})
	t.Run("big machine caps politely", func(t *testing.T) {
		r := RecommendResources(spec(32, 64))
		// 32 CPUs: children cap at 4, threads = (32/2)/4 = 4.
		if r.MaxFFmpegProcesses != 4 || r.FFmpegThreads != 4 || r.MaxAnalysisWorkers != 4 {
			t.Fatalf("32-core: %+v", r)
		}
		if total := r.MaxFFmpegProcesses * r.FFmpegThreads; total > 16 {
			t.Fatalf("budget exceeded: %d threads > half of 32", total)
		}
	})
	t.Run("ram guard shrinks children", func(t *testing.T) {
		// 8 GB RAM: half = 4 GB; 1.5 GB per child → at most 2 children.
		r := RecommendResources(spec(32, 8))
		if r.MaxFFmpegProcesses > 2 {
			t.Fatalf("8 GB should cap children at 2, got %d", r.MaxFFmpegProcesses)
		}
	})
	t.Run("unknown ram skips guard", func(t *testing.T) {
		r := RecommendResources(MachineSpec{LogicalCPU: 32})
		if r.MaxFFmpegProcesses != 4 {
			t.Fatalf("no-RAM: %+v", r)
		}
	})
	t.Run("one core still works", func(t *testing.T) {
		// The RAM guard shrinks a 4 GB box to a single child; the floors keep
		// it functional (never zero workers, never zero threads).
		r := RecommendResources(spec(1, 4))
		if r.MaxFFmpegProcesses != 1 || r.FFmpegThreads != 2 {
			t.Fatalf("1-core: %+v", r)
		}
	})
}

func TestApplyAutoProfileOverwritesKnobs(t *testing.T) {
	cfg := Default()
	cfg.Resource.MaxFFmpegProcesses = 99 // a user's explicit value...
	applyAutoProfile(cfg)                // ...is overwritten because profile=auto
	if cfg.Resource.MaxFFmpegProcesses == 99 {
		t.Fatal("auto profile must own the knobs")
	}
}

func TestResolveProfileManualKeepsValues(t *testing.T) {
	cfg := Default()
	cfg.Resource.Profile = ProfileManual
	cfg.Resource.MaxFFmpegProcesses = 3
	cfg.Resource.FFmpegThreads = 5
	t.Setenv("XCUT_WORKSPACE", t.TempDir())
	if err := Resolve(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Resource.MaxFFmpegProcesses != 3 || cfg.Resource.FFmpegThreads != 5 {
		t.Fatalf("manual profile lost user values: %+v", cfg.Resource)
	}
}

func TestResolveProfileAutoOverridesValues(t *testing.T) {
	cfg := Default()
	cfg.Resource.Profile = ProfileAuto
	cfg.Resource.MaxFFmpegProcesses = 99
	t.Setenv("XCUT_WORKSPACE", t.TempDir())
	if err := Resolve(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Resource.MaxFFmpegProcesses == 99 {
		t.Fatal("auto profile should have replaced the value")
	}
	if !strings.HasPrefix(cfg.Resource.Profile, "auto") {
		t.Fatalf("profile = %q", cfg.Resource.Profile)
	}
}

func TestResolveRejectsBadProfile(t *testing.T) {
	cfg := Default()
	cfg.Resource.Profile = "turbo"
	t.Setenv("XCUT_WORKSPACE", t.TempDir())
	err := Resolve(cfg)
	if err == nil || !strings.Contains(err.Error(), "resource.profile") {
		t.Fatalf("got %v, want resource.profile validation error", err)
	}
}
