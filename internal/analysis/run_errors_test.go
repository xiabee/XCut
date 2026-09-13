package analysis

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

type runProbeAnalyzer struct {
	name string
	run  func(ctx context.Context) ([]FeatureTrack, error)
}

func (f runProbeAnalyzer) Name() string { return f.name }
func (f runProbeAnalyzer) Version() int { return 1 }
func (f runProbeAnalyzer) Kind() string { return f.name }
func (f runProbeAnalyzer) Analyze(ctx context.Context, _ Options, _ string, _ bool, _ *slog.Logger) ([]FeatureTrack, error) {
	return f.run(ctx)
}

func runOne(t *testing.T, a Analyzer, timeout time.Duration, parent context.Context) (*Result, error) {
	t.Helper()
	if parent == nil {
		parent = context.Background()
	}
	return Run(parent, nil, Options{CallTimeout: timeout}, []Analyzer{a},
		"whatever.mp4", "fp", 1.0, false, slog.Default())
}

// TestRunReportsPlainAnalyzerFailure: a failing analyzer with a live call
// context must surface the analyzer's own error — it used to be branded a
// per-call-budget timeout because run.go cancelled the context before
// classifying the failure (a missing ffmpeg reported "exceeded its 30m0s
// time budget", sending users hunting for a hang that never happened).
func TestRunReportsPlainAnalyzerFailure(t *testing.T) {
	boom := errors.New("cannot start ffmpeg: executable file not found")
	a := runProbeAnalyzer{name: "boom", run: func(context.Context) ([]FeatureTrack, error) {
		return nil, boom
	}}
	_, err := runOne(t, a, 30*time.Minute, nil)
	if err == nil {
		t.Fatal("expected the analyzer error")
	}
	if !errors.Is(err, boom) {
		t.Fatalf("plain failure must be returned verbatim, got: %v", err)
	}
	if strings.Contains(err.Error(), "time budget") {
		t.Fatalf("plain failure must not be branded a timeout: %v", err)
	}
}

// TestRunDetectsGenuineTimeout: an analyzer that hangs past its per-call
// budget is still classified as a timeout (the budget's whole purpose).
func TestRunDetectsGenuineTimeout(t *testing.T) {
	a := runProbeAnalyzer{name: "hang", run: func(ctx context.Context) ([]FeatureTrack, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	_, err := runOne(t, a, 10*time.Millisecond, nil)
	if err == nil || !strings.Contains(err.Error(), "time budget") {
		t.Fatalf("hung analyzer must report the budget, got: %v", err)
	}
}

// TestRunDistinguishesParentCancellation: when the JOB context dies the
// failure belongs to the cancellation, not the analyzer's budget.
func TestRunDistinguishesParentCancellation(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	a := runProbeAnalyzer{name: "slow", run: func(ctx context.Context) ([]FeatureTrack, error) {
		cancel() // job dies mid-run; the analyzer then errors too
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	_, err := runOne(t, a, 30*time.Minute, parent)
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "time budget") {
		t.Fatalf("parent cancellation must not be branded a budget timeout: %v", err)
	}
}
