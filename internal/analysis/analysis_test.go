package analysis

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/xiabee/XCut/internal/worker"
)

type fakeAnalyzer struct {
	name    string
	version int
	tracks  []FeatureTrack
	err     error
	calls   *int
}

func (f fakeAnalyzer) Name() string { return f.name }
func (f fakeAnalyzer) Version() int { return f.version }
func (f fakeAnalyzer) Analyze(context.Context, Options, string, bool, *slog.Logger) ([]FeatureTrack, error) {
	if f.calls != nil {
		*f.calls++
	}
	return f.tracks, f.err
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(&discardWriter{}, nil))
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

func TestFallbackAnalyzerUsesPrimary(t *testing.T) {
	primary := fakeAnalyzer{name: "primary", version: 1, tracks: []FeatureTrack{{Kind: "x"}}}
	fb := FallbackAnalyzer{Primary: primary, Fallback: fakeAnalyzer{name: "fb", tracks: []FeatureTrack{{Kind: "y"}}}}
	out, err := fb.Analyze(context.Background(), Options{}, "p", true, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Kind != "x" {
		t.Fatalf("primary output expected: %+v", out)
	}
	if fb.Name() != "primary" || fb.Version() != 1 {
		t.Fatal("name/version must reflect primary (cache-key contract)")
	}
}

func TestFallbackAnalyzerFallsBackOnError(t *testing.T) {
	calls := 0
	primary := fakeAnalyzer{name: "primary", version: 1, err: errors.New("boom"), calls: &calls}
	fallback := fakeAnalyzer{name: "fb", tracks: []FeatureTrack{{Kind: "y"}}}
	fb := FallbackAnalyzer{Primary: primary, Fallback: fallback}
	out, err := fb.Analyze(context.Background(), Options{}, "p", true, testLogger())
	if err != nil {
		t.Fatalf("fallback should have succeeded: %v", err)
	}
	if len(out) != 1 || out[0].Kind != "y" {
		t.Fatalf("fallback output expected: %+v", out)
	}
	if calls != 1 {
		t.Fatalf("primary calls = %d", calls)
	}
}

func TestResolveAnalyzersModes(t *testing.T) {
	log := testLogger()
	ctx := context.Background()

	a, err := ResolveAnalyzers(ctx, WorkerConfig{Audio: "ffmpeg"}, log)
	if err != nil || len(a) != 3 {
		t.Fatalf("ffmpeg mode: %v (%d analyzers)", err, len(a))
	}

	// Strict rust without a binary must fail loudly.
	_, err = ResolveAnalyzers(ctx, WorkerConfig{Audio: "rust"}, log)
	if !xcerrIsNotFound(err) {
		t.Fatalf("rust mode without binary should fail with NotFound, got %v", err)
	}

	// Invalid mode rejected.
	_, err = ResolveAnalyzers(ctx, WorkerConfig{Audio: "warp"}, log)
	if err == nil {
		t.Fatal("invalid mode should fail")
	}

	// auto without a binary → baseline (worker may exist on dev machines;
	// this branch is only deterministic when it is absent).
	if worker.ResolveBin("") == "" {
		a, err = ResolveAnalyzers(ctx, WorkerConfig{Audio: "auto"}, log)
		if err != nil || len(a) != 3 {
			t.Fatalf("auto mode without worker: %v", err)
		}
	}
}

func xcerrIsNotFound(err error) bool {
	return err != nil && containsStr(err.Error(), "not installed")
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
