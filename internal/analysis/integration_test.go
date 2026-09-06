package analysis

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/testmedia"
)

func requireTools(t *testing.T) Options {
	t.Helper()
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	return Options{
		Tools:         media.Tools{FFmpeg: "ffmpeg", FFprobe: "ffprobe", Threads: 2},
		SampleFPS:     2,
		AnalysisWidth: 320,
	}
}

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func fixturePath(t *testing.T) string {
	t.Helper()
	path, err := testmedia.Generate(t.TempDir(), "fx.mp4", testmedia.DefaultFixture(), 320, 240, 10)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFrameDiffAnalyzerFixture(t *testing.T) {
	opts := requireTools(t)
	path := fixturePath(t)
	logger := newTestLogger()
	a := FrameDiffAnalyzer{}

	tracks, err := a.Analyze(context.Background(), opts, path, true, logger)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 1 || tracks[0].Kind != "frame_diff" {
		t.Fatalf("tracks: %+v", tracks)
	}
	// 8s fixture @ 2fps ⇒ ~16 samples.
	if len(tracks[0].Samples) < 12 || len(tracks[0].Samples) > 20 {
		t.Fatalf("sample count %d out of range", len(tracks[0].Samples))
	}
	for _, s := range tracks[0].Samples {
		if s.V < 0 || s.V > 1 {
			t.Fatalf("sample out of [0,1]: %+v", s)
		}
	}
	// Determinism: two runs produce identical JSON.
	b1, _ := json.Marshal(tracks)
	tracks2, err := a.Analyze(context.Background(), opts, path, true, logger)
	if err != nil {
		t.Fatal(err)
	}
	b2, _ := json.Marshal(tracks2)
	if string(b1) != string(b2) {
		t.Fatal("frame_diff not deterministic")
	}
}

func TestAudioAnalyzerFixture(t *testing.T) {
	opts := requireTools(t)
	path := fixturePath(t)
	logger := newTestLogger()
	a := AudioAnalyzer{}

	tracks, err := a.Analyze(context.Background(), opts, path, true, logger)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 1 || tracks[0].Kind != "audio_rms_db" {
		t.Fatalf("tracks: %+v", tracks)
	}
	if len(tracks[0].Samples) < 10 {
		t.Fatalf("too few RMS windows: %d", len(tracks[0].Samples))
	}
	for _, s := range tracks[0].Samples {
		if s.V > 0 {
			t.Fatalf("dBFS should be <= 0: %+v", s)
		}
	}

	// No-audio input yields no track, no error.
	silent := filepath.Join(t.TempDir(), "silent.mp4")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, _, err := media.Run(ctx, opts.Tools.FFmpeg,
		"-f", "lavfi", "-i", "color=c=red:s=160x120:r=10:d=1",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-y", silent); err != nil {
		t.Fatal(err)
	}
	tracks2, err := a.Analyze(ctx, opts, silent, false, logger)
	if err != nil || len(tracks2) != 0 {
		t.Fatalf("no-audio: tracks=%v err=%v", tracks2, err)
	}
}

func TestRunCacheRoundTrip(t *testing.T) {
	opts := requireTools(t)
	path := fixturePath(t)
	logger := newTestLogger()
	store := NewStore(t.TempDir())
	analyzers := Baseline()

	fp, err := media.Fingerprint(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	r1, err := Run(ctx, store, opts, analyzers, path, fp, 8, true, logger)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := Run(ctx, store, opts, analyzers, path, fp, 8, true, logger)
	if err != nil {
		t.Fatal(err)
	}
	b1, _ := json.Marshal(r1)
	b2, _ := json.Marshal(r2)
	if string(b1) != string(b2) {
		t.Fatal("cached result differs from first run")
	}
}
