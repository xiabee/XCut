package analysis

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/xiabee/XCut/internal/testmedia"
)

// benchAudioFixture builds (once) a 60 s audio fixture and returns its path.
func benchAudioFixture(b *testing.B) string {
	if !testmedia.HasFFmpeg() {
		b.Skip("ffmpeg not available")
	}
	root := b.TempDir()
	path, err := testmedia.GenerateAudio(root, "bench60s.m4a",
		"sine=frequency=440:duration=60,volume=volume='if(lt(mod(t\\,1)\\,0.05)\\,1\\,0.02)':eval=frame")
	if err != nil {
		b.Fatal(err)
	}
	return path
}

func benchmarkAnalyzer(b *testing.B, a Analyzer) time.Duration {
	path := benchAudioFixture(b)
	log := slog.New(slog.NewTextHandler(&discardWriter{}, nil))
	ctx := context.Background()

	b.ResetTimer()
	var total time.Duration
	for i := 0; i < b.N; i++ {
		start := time.Now()
		if _, err := a.Analyze(ctx, testOnsetOptions(), path, true, log); err != nil {
			b.Fatal(err)
		}
		total += time.Since(start)
	}
	b.StopTimer()
	return total / time.Duration(b.N)
}

func BenchmarkAudioRMS60s(b *testing.B)   { benchmarkAnalyzer(b, AudioAnalyzer{}) }
func BenchmarkAudioOnset60s(b *testing.B) { benchmarkAnalyzer(b, AudioOnsetAnalyzer{}) }
