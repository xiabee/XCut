package analysis

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/testmedia"
)

// The Rust worker is optional by design, and the optionality is the contract: no
// binary → ffmpeg analysis; a binary that works → the worker with an ffmpeg net
// underneath it; a binary that does not work → ffmpeg again, loudly, never a failed
// analysis. The mode matrix is covered in analysis_test.go; what was never run here
// is the branch where the worker is actually there — which, until the gate learned
// to build it, was a branch no leg exercised anywhere.

// mediaWorkerBin mirrors the lookup internal/worker's tests use: the built crate
// first, then PATH. Empty means "not built here", and the cases that need it say
// so rather than passing quietly.
func mediaWorkerBin() string {
	exe := "xcut-worker-media"
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	if _, thisFile, _, ok := runtime.Caller(0); ok {
		root := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
		for _, profile := range []string{"debug", "release"} {
			p := filepath.Join(root, "crates", "xcut-worker-media", "target", profile, exe)
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return strings.TrimSpace(os.Getenv("XCUT_MEDIA_WORKER_BIN"))
}

func analyzerNames(as []Analyzer) []string {
	out := make([]string, 0, len(as))
	for _, a := range as {
		out = append(out, a.Name())
	}
	return out
}

func hasNamed(as []Analyzer, name string) bool {
	for _, a := range as {
		if a.Name() == name {
			return true
		}
	}
	return false
}

// TestWorkerPresentGivesTheRustAnalyzerTheCacheKey: the name an analyzer reports is
// part of the cache key, so "prefer the worker" must not be indistinguishable from
// "used ffmpeg" in the store — the two produce different numbers from different
// decoders (docs/PROJECT_STATE.md, the proxy/cache separation note).
func TestWorkerPresentGivesTheRustAnalyzerTheCacheKey(t *testing.T) {
	bin := mediaWorkerBin()
	if bin == "" {
		t.Skip("xcut-worker-media not built (cargo build in crates/xcut-worker-media)")
	}
	log := testLogger()

	withWorker, err := ResolveAnalyzers(context.Background(), WorkerConfig{MediaBin: bin, Audio: "auto"}, log)
	if err != nil {
		t.Fatalf("auto mode with a working worker: %v", err)
	}
	if len(withWorker) != 3 {
		t.Fatalf("analyzers = %v, want the three-piece set", analyzerNames(withWorker))
	}
	if !hasNamed(withWorker, "audio_rms_rust") {
		t.Fatalf("the worker was present and unused: %v", analyzerNames(withWorker))
	}
	// The wrapper must carry the primary's name, not invent one: that name is what
	// already sits in users' cache entries.
	var wrapped Analyzer
	for _, a := range withWorker {
		if fb, ok := a.(FallbackAnalyzer); ok {
			if _, isRust := fb.Primary.(RustAudioAnalyzer); !isRust {
				t.Fatalf("fallback primary is %T, want the rust analyzer", fb.Primary)
			}
			if _, isFFmpeg := fb.Fallback.(AudioAnalyzer); !isFFmpeg {
				t.Fatalf("fallback is %T, want the ffmpeg analyzer underneath", fb.Fallback)
			}
			wrapped = a
		}
	}
	if wrapped == nil {
		t.Fatal("no FallbackAnalyzer in the set — the ffmpeg net is missing")
	}

	rustKey := cacheKey("fp", []Analyzer{wrapped}, ConfigKey{})
	ffmpegKey := cacheKey("fp", []Analyzer{AudioAnalyzer{}}, ConfigKey{})
	if rustKey == ffmpegKey {
		t.Fatal("rust and ffmpeg analyses share a cache key")
	}
	// And the separation must survive a rename that makes them the same thing.
	if hasNamed(withWorker, "audio_rms") {
		t.Error("the worker set also carries the plain ffmpeg audio analyzer — one asset would analyze twice")
	}
}

// TestFFmpegModeIgnoresAWorkerThatIsThere: "ffmpeg" is the opt-out an operator uses
// when the worker is the problem, so it must hold even when a working binary is
// configured — this is the arm that would pass vacuously if no worker existed.
func TestFFmpegModeIgnoresAWorkerThatIsThere(t *testing.T) {
	bin := mediaWorkerBin()
	if bin == "" {
		t.Skip("xcut-worker-media not built (cargo build in crates/xcut-worker-media)")
	}
	as, err := ResolveAnalyzers(context.Background(), WorkerConfig{MediaBin: bin, Audio: "ffmpeg"}, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	if hasNamed(as, "audio_rms_rust") {
		t.Errorf("audio=ffmpeg still used the worker: %v", analyzerNames(as))
	}
	if !hasNamed(as, "audio_rms") {
		t.Errorf("audio=ffmpeg lost the ffmpeg analyzer it is supposed to keep: %v", analyzerNames(as))
	}
}

// TestUnusableWorkerDegradesToFFmpeg: a file that cannot answer `describe` is found
// by the PATH-less lookup and must cost a warning, not an analysis. Needs no Rust
// toolchain, so it runs on every leg.
func TestUnusableWorkerDegradesToFFmpeg(t *testing.T) {
	notAWorker := filepath.Join(t.TempDir(), "xcut-worker-media.txt")
	if err := os.WriteFile(notAWorker, []byte("this is not a worker\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	as, err := ResolveAnalyzers(context.Background(), WorkerConfig{MediaBin: notAWorker, Audio: "auto"}, testLogger())
	if err != nil {
		t.Fatalf("an unusable worker failed the analysis instead of stepping aside: %v", err)
	}
	if hasNamed(as, "audio_rms_rust") {
		t.Errorf("a worker that cannot describe itself was still used: %v", analyzerNames(as))
	}
	if !hasNamed(as, "audio_rms") {
		t.Errorf("the ffmpeg analyzer vanished with the worker: %v", analyzerNames(as))
	}
	// Strict mode is the opposite answer, and the one the operator asked for: it
	// does not probe, so an unusable path is still handed to the caller — with no
	// ffmpeg net underneath it. If this ever grows a fallback, "rust" stops meaning
	// "tell me loudly when the worker is the problem".
	strict, err := ResolveAnalyzers(context.Background(), WorkerConfig{MediaBin: notAWorker, Audio: "rust"}, testLogger())
	if err != nil {
		t.Fatalf("audio=rust refused without asking the worker anything: %v", err)
	}
	if !hasNamed(strict, "audio_rms_rust") {
		t.Errorf("audio=rust did not use the worker it was told to require: %v", analyzerNames(strict))
	}
	for _, a := range strict {
		if _, wrapped := a.(FallbackAnalyzer); wrapped {
			t.Error("audio=rust grew an ffmpeg net underneath; strict mode must fail loudly, not degrade")
		}
	}
}

// bareAudioFile writes an audio-only file: the shape the worker's decoder does
// take. XCut's own fixtures encode AAC at 96k, which is what the crate's feature
// list (isomp4 + aac) is there for.
func bareAudioFile(t *testing.T) string {
	t.Helper()
	out, err := testmedia.GenerateAudio(t.TempDir(), "tone.m4a", "sine=frequency=440:duration=3")
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestRustAudioAnalyzerMeasuresBareAudio is the Go side of the audio_rms call: the
// request goes out as the protocol, the answer comes back as the same track kind the
// ffmpeg analyzer emits, and the numbers mean something.
func TestRustAudioAnalyzerMeasuresBareAudio(t *testing.T) {
	bin := mediaWorkerBin()
	if bin == "" {
		t.Skip("xcut-worker-media not built (cargo build in crates/xcut-worker-media)")
	}
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	clip := bareAudioFile(t)
	tracks, err := (RustAudioAnalyzer{Bin: bin}).Analyze(context.Background(), Options{}, clip, true, testLogger())
	if err != nil {
		t.Fatalf("the worker refused the audio-only file it exists for: %v", err)
	}
	if len(tracks) != 1 {
		t.Fatalf("tracks = %d, want one", len(tracks))
	}
	tr := tracks[0]
	if tr.Kind != "audio_rms_db" || tr.Unit != "dBFS" {
		t.Errorf("emitted %s/%s, want audio_rms_db/dBFS — the kind consumers read", tr.Kind, tr.Unit)
	}
	if tr.Analyzer != "audio_rms_rust" || tr.Version != 1 {
		t.Errorf("provenance came back as %s/v%d", tr.Analyzer, tr.Version)
	}
	if len(tr.Samples) == 0 {
		t.Fatal("no samples for a 3-second tone")
	}
	// A 440 Hz tone at unity is loud: the windowed RMS must be finite and below
	// digital full scale, and it must be *varied* — an empty decode answers with
	// one block of zeros, which is not a measurement.
	min, max := tr.Samples[0].V, tr.Samples[0].V
	varied := false
	for _, s := range tr.Samples {
		if s.T < 0 {
			t.Fatalf("negative sample time: %v", s.T)
		}
		if s.V > 0 {
			t.Fatalf("RMS above full scale at t=%.2f: %v dBFS", s.T, s.V)
		}
		if s.V < min {
			min = s.V
		}
		if s.V > max {
			max = s.V
		}
		if s.V != tr.Samples[0].V {
			varied = true
		}
	}
	if !varied {
		t.Fatalf("every one of %d samples is %v dBFS — the decode produced no signal shape", len(tr.Samples), min)
	}
	// The contract with the rest of the pipeline: no audio stream, no track.
	silent, err := (RustAudioAnalyzer{Bin: bin}).Analyze(context.Background(), Options{}, clip, false, testLogger())
	if err != nil || silent != nil {
		t.Errorf("hasAudio=false answered %d tracks, %v — the worker should not be asked at all", len(silent), err)
	}
}

// TestAutoModeCoversTheWorkerCodecGap is the half of the contract that has always
// been asserted in prose: on XCut's own media shape — H.264 video with a 96k AAC
// track in an MP4 — the worker's decoder fails ("aac: predictor data", measured
// 2026-09-23), and `auto` mode's ffmpeg net is what makes that a shrug instead of a
// failed analysis. The gap is provoked on purpose here, and the premise is checked
// first: without that check the fallback could be passing because nothing failed.
func TestAutoModeCoversTheWorkerCodecGap(t *testing.T) {
	bin := mediaWorkerBin()
	if bin == "" {
		t.Skip("xcut-worker-media not built (cargo build in crates/xcut-worker-media)")
	}
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	video, err := testmedia.GenerateRally(t.TempDir(), "hall.mp4", 320, 240, 10, 6,
		[]testmedia.RallySpec{{Start: 0, End: 6, HitEvery: 1.2}})
	if err != nil {
		t.Fatal(err)
	}
	if _, rerr := (RustAudioAnalyzer{Bin: bin}).Analyze(context.Background(), Options{}, video, true, testLogger()); rerr == nil {
		t.Skip("the worker now decodes XCut's own media shape — this case's premise is gone; re-point it at a stream the worker still refuses rather than letting it pass on nothing")
	}

	as, err := ResolveAnalyzers(context.Background(), WorkerConfig{MediaBin: bin, Audio: "auto"}, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	var audio Analyzer
	for _, a := range as {
		if a.Name() == "audio_rms_rust" {
			audio = a
		}
	}
	if audio == nil {
		t.Fatal("no worker-backed audio analyzer in the auto set")
	}
	tracks, err := audio.Analyze(context.Background(), testOnsetOptions(), video, true, testLogger())
	if err != nil {
		t.Fatalf("the codec gap reached the caller: %v", err)
	}
	if len(tracks) != 1 || tracks[0].Analyzer != "audio_rms" {
		t.Fatalf("tracks = %+v, want the ffmpeg analyzer's track after the fallback", tracks)
	}
	if len(tracks[0].Samples) == 0 {
		t.Fatal("the fallback produced an empty track")
	}
}
