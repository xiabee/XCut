package analysis

import (
	"context"
	"encoding/binary"
	"log/slog"
	"math"
	"testing"

	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/testmedia"
)

func testOnsetOptions() Options {
	return Options{Tools: media.Tools{FFmpeg: "ffmpeg", FFprobe: "ffprobe", Threads: 1}}
}

func hopEnvelope(onsetHops map[int]float64, n int) []float64 {
	// Flat quiet noise floor with rises at the given hops.
	db := make([]float64, n)
	for i := range db {
		db[i] = -40
	}
	for hop, rise := range onsetHops {
		if hop >= 1 && hop < n {
			db[hop] += rise
		}
	}
	return db
}

func TestDetectOnsetsImpulseTrain(t *testing.T) {
	// A 12 dB hit every 10 hops over 100 hops of quiet floor.
	onsets := map[int]float64{10: 12, 20: 12, 30: 12, 40: 12, 50: 12, 60: 12, 70: 12, 80: 12}
	got := detectOnsets(hopEnvelope(onsets, 100), 50)
	if len(got) != len(onsets) {
		t.Fatalf("got %d onsets (%v), want %d", len(got), got, len(onsets))
	}
	for i, s := range got {
		want := float64(10*(i+1)) / 50.0
		if math.Abs(s.T-want) > 1e-9 {
			t.Errorf("onset %d at %g want %g", i, s.T, want)
		}
		if s.V <= 0 || s.V > 1 {
			t.Errorf("onset %d strength %g out of (0,1]", i, s.V)
		}
	}
}

func TestDetectOnsetsSilenceAndConstantTone(t *testing.T) {
	if got := detectOnsets(nil, 50); got != nil {
		t.Errorf("empty envelope produced %v", got)
	}
	// Digital silence: no flux, no onsets.
	flat := make([]float64, 200)
	for i := range flat {
		flat[i] = -90
	}
	if got := detectOnsets(flat, 50); len(got) != 0 {
		t.Errorf("silence produced %d onsets", len(got))
	}
	// Constant loud tone: level steps once at decode start, then no flux.
	loud := make([]float64, 200)
	for i := range loud {
		loud[i] = -10
	}
	if got := detectOnsets(loud, 50); len(got) != 0 {
		t.Errorf("constant tone produced %d onsets", len(got))
	}
}

func TestDetectOnsetsDeterministic(t *testing.T) {
	env := hopEnvelope(map[int]float64{7: 15, 33: 8, 51: 22, 90: 12}, 120)
	a := detectOnsets(env, 50)
	for i := 0; i < 10; i++ {
		b := detectOnsets(env, 50)
		if len(a) != len(b) {
			t.Fatalf("nondeterministic onset count: %d vs %d", len(a), len(b))
		}
		for j := range a {
			if a[j] != b[j] {
				t.Fatalf("nondeterministic onset %d: %v vs %v", j, a[j], b[j])
			}
		}
	}
}

func TestOnsetProcessorChunkAlignment(t *testing.T) {
	// Chunks must be reassembled across arbitrary byte boundaries.
	pcm := make([]byte, 0, 2*onsetHopSamples*3)
	appendHop := func(amp int16) {
		for i := 0; i < onsetHopSamples; i++ {
			b := make([]byte, 2)
			binary.LittleEndian.PutUint16(b, uint16(amp))
			pcm = append(pcm, b...)
		}
	}
	appendHop(1000)  // quiet
	appendHop(30000) // loud hop → onset
	appendHop(1000)

	p := newOnsetProcessor()
	// Feed one byte at a time: worst-case chunking.
	for i := 0; i < len(pcm); i++ {
		if err := p.consume(pcm[i : i+1]); err != nil {
			t.Fatal(err)
		}
	}
	got := p.finish()
	if len(got) != 1 {
		t.Fatalf("got %d onsets, want 1: %v", len(got), got)
	}
	if got[0].T != 0.02 {
		t.Errorf("onset t=%g want 0.02", got[0].T)
	}

	// Whole-buffer feed must give the identical result.
	p2 := newOnsetProcessor()
	if err := p2.consume(pcm); err != nil {
		t.Fatal(err)
	}
	if a, b := p.finish(), p2.finish(); len(a) != len(b) || a[0] != b[0] {
		t.Fatalf("chunking changed the result: %v vs %v", a, b)
	}
}

// TestAudioOnsetAnalyzerBursts verifies the full analyzer against a real
// AAC fixture with periodic full-scale bursts every second.
func TestAudioOnsetAnalyzerBursts(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	root := t.TempDir()
	// 50 ms full-scale burst every 1 s over a −34 dB bed, 10 s total.
	path, err := testmedia.GenerateAudio(root, "bursts.m4a",
		"sine=frequency=440:duration=10,volume=volume='if(lt(mod(t,1),0.05),1,0.02)':eval=frame")
	if err != nil {
		t.Fatal(err)
	}

	a := AudioOnsetAnalyzer{}
	tracks, err := a.Analyze(context.Background(), testOnsetOptions(), path, true, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 1 || tracks[0].Kind != "audio_onset" {
		t.Fatalf("unexpected tracks: %+v", tracks)
	}
	got := tracks[0].Samples
	if len(got) < 8 {
		t.Fatalf("only %d onsets for 10 bursts: %v", len(got), got)
	}
	if len(got) > 20 {
		t.Fatalf("too many onsets (%d): detector is firing on noise", len(got))
	}
	// Each onset should sit near a whole second (±0.15 s).
	for _, s := range got {
		frac := math.Abs(s.T - math.Round(s.T))
		if frac > 0.15 {
			t.Errorf("onset at %.3fs is not near a burst boundary", s.T)
		}
	}
	// Strength ordering sanity: at least one near-certain hit.
	maxStrength := 0.0
	for _, s := range got {
		if s.V > maxStrength {
			maxStrength = s.V
		}
	}
	if maxStrength < 0.5 {
		t.Errorf("no confident onset (max strength %g)", maxStrength)
	}
}

func TestAudioOnsetAnalyzerNoAudio(t *testing.T) {
	a := AudioOnsetAnalyzer{}
	tracks, err := a.Analyze(context.Background(), Options{}, "irrelevant.mp4", false, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if tracks != nil {
		t.Fatalf("no-audio asset must yield no track, got %+v", tracks)
	}
}

// TestOnsetAnalyzerTimeout verifies the analyzer respects its wall-clock
// budget via an already-cancelled context.
func TestOnsetAnalyzerTimeout(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	root := t.TempDir()
	path, err := testmedia.GenerateAudio(root, "tone.m4a", "sine=frequency=440:duration=1")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()
	if _, err := (AudioOnsetAnalyzer{}).Analyze(ctx, testOnsetOptions(), path, true, slog.Default()); err == nil {
		t.Fatal("expected timeout error")
	}
}
