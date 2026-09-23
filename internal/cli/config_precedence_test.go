package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/config"
)

// The documented precedence is defaults < <workspace>/config.json < env < flags
// (AGENTS.md, config.go's own header). The two file layers are combined by
// config.MergeLayer, whose contract — pinned by TestMergeLayerKeepsBaseWhenLayerZero
// in its own package — is "a zero in the upper layer means the lower one keeps".
// What the composition did not honour is the word *mentioned*: config.Load prefills
// with Default() before decoding, so every field a workspace file never heard of
// arrives at the merge already set to its default, and the merge cannot tell that
// from a choice. The observable effect is that writing anything at all into
// <workspace>/config.json resets everything the operator had set in the bootstrap
// config — measured through the shipped binary before this test existed:
// ~/.xcut/config.json with log.level=debug, resource.max_cache_gb=2,
// resource.max_temp_gb=3, plus a workspace file that mentions only
// resource.frame_sample_fps, came back as info / 10 / 20 / 3.

func writeRawConfig(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, "config.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// fakeHome puts a bootstrap config where loadConfig looks when no --config or
// XCUT_CONFIG is given (the home dir + /.xcut/config.json), so the two-file
// arrangement the layering branch reads is the one under test: an explicit
// --config deliberately stops at the bootstrap file. The home dir is pointed at a
// temp dir rather than at this machine's real ~/.xcut.
func fakeHome(t *testing.T, body string) {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, ".xcut")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// Windows resolves the home dir from USERPROFILE, POSIX from HOME.
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
}

func TestWorkspaceConfigOverridesOnlyWhatItMentions(t *testing.T) {
	ws := t.TempDir()
	fakeHome(t, `{"resource":{"max_cache_gb":2,"max_temp_gb":3},"log":{"level":"debug"}}`)
	writeRawConfig(t, ws, `{"resource":{"frame_sample_fps":3.0}}`)

	cfg, path, err := loadConfig("", ws)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	// Without this, the test could pass while never exercising the layering at all
	// (an explicit --config short-circuits it, which is how the first version of
	// this case was green on a broken premise).
	if !strings.Contains(path, ws) {
		t.Fatalf("the resolved config path is %q, want the workspace file under %s", path, ws)
	}
	if cfg.Resource.MaxCacheGB != 2 || cfg.Resource.MaxTempGB != 3 {
		t.Errorf("the workspace file reset what it never mentioned: cache %v temp %v, want 2 and 3",
			cfg.Resource.MaxCacheGB, cfg.Resource.MaxTempGB)
	}
	if cfg.Log.Level != "debug" {
		t.Errorf("log.level came back %q, want the bootstrap file's debug", cfg.Log.Level)
	}
	// The field the workspace file *did* mention must still win.
	if cfg.Resource.FrameSampleFPS != 3.0 {
		t.Errorf("frame_sample_fps = %v, want the workspace value 3", cfg.Resource.FrameSampleFPS)
	}
	// And a key neither file mentions must still carry the shipped default, which is
	// what makes Resolve's post-merge zero-filling load-bearing rather than accidental.
	if cfg.Resource.AnalysisWidth != config.Default().Resource.AnalysisWidth {
		t.Errorf("analysis_width = %d, want the default %d",
			cfg.Resource.AnalysisWidth, config.Default().Resource.AnalysisWidth)
	}
}

// The env layer sits above both files and must keep doing so after the fix — and
// above the *workspace* file specifically, which needs the home-based arrangement
// to be reached at all.
func TestEnvStillBeatsBothFileLayers(t *testing.T) {
	ws := t.TempDir()
	fakeHome(t, `{"log":{"level":"debug"}}`)
	writeRawConfig(t, ws, `{"log":{"level":"error"}}`)
	t.Setenv("XCUT_LOG_LEVEL", "warn")

	cfg, _, err := loadConfig("", ws)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.Log.Level != "warn" {
		t.Errorf("log.level = %q, want XCUT_LOG_LEVEL's warn", cfg.Log.Level)
	}
}

// A bootstrap file alone must keep everything it set — the case that worked by
// accident before (nothing above it to overwrite), and the one that would break if
// the fix turned sparse loading into "lose the defaults".
func TestBootstrapAloneKeepsItsValuesAndTheDefaults(t *testing.T) {
	bootstrap := writeRawConfig(t, t.TempDir(), `{"resource":{"max_cache_gb":7},"log":{"level":"error"}}`)

	cfg, _, err := loadConfig(bootstrap, t.TempDir() /* no workspace config at all */)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.Resource.MaxCacheGB != 7 {
		t.Errorf("max_cache_gb = %v, want 7", cfg.Resource.MaxCacheGB)
	}
	if cfg.Log.Level != "error" {
		t.Errorf("log.level = %q, want error", cfg.Log.Level)
	}
	if cfg.Server.Listen != "127.0.0.1:8619" {
		t.Errorf("an unmentioned server.listen = %q, want the shipped loopback default", cfg.Server.Listen)
	}
	if cfg.Job.MaxHistory != config.Default().Job.MaxHistory {
		t.Errorf("job.max_history = %d, want the default %d", cfg.Job.MaxHistory, config.Default().Job.MaxHistory)
	}
}
