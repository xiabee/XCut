package analysis

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/player"
	"github.com/xiabee/XCut/internal/testmedia"
	"github.com/xiabee/XCut/internal/xcerr"
)

// TestPlayerPresenceAnalyzerMeasuresThroughTheRealPath drives the wrapper end
// to end on real media: probe, decode, patch scoring, track shape. The
// presence samples must sit on the scan clock (2 fps) and stay in [0,1], and
// the track must land under the analyzer name the pipeline's cache routing
// expects.
func TestPlayerPresenceAnalyzerMeasuresThroughTheRealPath(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	dir := t.TempDir()
	path, err := testmedia.GenerateRally(dir, "match.mp4", 320, 240, 25, 14.3,
		[]testmedia.RallySpec{{Start: 0, End: 14, HitEvery: 0.5}})
	if err != nil {
		t.Fatal(err)
	}
	tools := media.Tools{FFmpeg: "ffmpeg", FFprobe: "ffprobe"}
	probe, err := media.ProbeFile(context.Background(), tools, path)
	if err != nil {
		t.Fatal(err)
	}

	sig, _, _, err := player.MeasureSignature(context.Background(), tools, path,
		[]float64{0.3, 0.3, 0.2, 0.2}, 7, probe.DurationSec)
	if err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tracks, err := PlayerPresenceAnalyzer{Sig: sig}.Analyze(context.Background(),
		Options{Tools: tools}, path, false, logger)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 1 {
		t.Fatalf("got %d tracks, want one presence track", len(tracks))
	}
	tr := tracks[0]
	if tr.Analyzer != "player_presence" || tr.Unit != "patch-match" {
		t.Fatalf("track identity = %s/%s, want player_presence/patch-match", tr.Analyzer, tr.Unit)
	}
	if len(tr.Samples) == 0 {
		t.Fatal("the presence track is empty")
	}
	// The signature was measured from this very source, so scanning it must
	// find the person somewhere — a track of all zeros is a scan that never
	// compared anything.
	best := 0.0
	for _, s := range tr.Samples {
		if s.V > best {
			best = s.V
		}
	}
	if best <= 0 {
		t.Fatal("every sample scored 0 against a signature measured from this same source")
	}
	want := 1.0 / player.PresenceScanFPS
	for i, s := range tr.Samples {
		if s.V < 0 || s.V > 1 {
			t.Fatalf("sample %d value %.3f outside [0,1]", i, s.V)
		}
		if i > 0 {
			gap := s.T - tr.Samples[i-1].T
			if diff := gap - want; diff > 0.01 || diff < -0.01 {
				t.Fatalf("sample %d sits %.4f from its neighbour, want the %.2f s scan clock", i, gap, want)
			}
		}
	}
	if last := tr.Samples[len(tr.Samples)-1].T; last > probe.DurationSec {
		t.Fatalf("last sample at %.2f exceeds the source's %.2f s", last, probe.DurationSec)
	}
}

func TestPlayerPresenceAnalyzerRefusesAnEmptySignature(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	_, err := PlayerPresenceAnalyzer{}.Analyze(context.Background(),
		Options{Tools: media.Tools{FFmpeg: "ffmpeg", FFprobe: "ffprobe"}},
		filepath.Join(t.TempDir(), "unused.mp4"), false, logger)
	if !xcerr.IsCode(err, xcerr.CodeAnalyzerFailure) {
		t.Fatalf("an empty signature scanned instead of refusing: %v", err)
	}
}

// TestCacheKeySeparatesSignatures pins the cache-routing half: two runs that
// differ only in the seeded signature must not share an entry — re-seeding
// the spot must re-scan, which is the property the hash exists for.
func TestCacheKeySeparatesSignatures(t *testing.T) {
	base := ConfigKey{SampleFPS: 2, AnalysisWidth: 640}
	withA := ConfigKey{SampleFPS: 2, AnalysisWidth: 640, PlayerSig: "aaaa"}
	withB := ConfigKey{SampleFPS: 2, AnalysisWidth: 640, PlayerSig: "bbbb"}

	kBase := cacheKey("fp", nil, base)
	kA := cacheKey("fp", nil, withA)
	kB := cacheKey("fp", nil, withB)
	if kA == kBase || kB == kBase {
		t.Fatal("a seeded signature shares a cache entry with no signature")
	}
	if kA == kB {
		t.Fatal("two different signatures share a cache entry — re-seeding would replay the old scan")
	}
}

// TestCacheKeySeparatesModelKinds pins the multi-region namespace: a
// band-model run and a single-histogram run of the same media must never
// share an entry — the mr: hash lands in its own ConfigKey field, so neither
// scan can satisfy the other's key.
func TestCacheKeySeparatesModelKinds(t *testing.T) {
	single := ConfigKey{SampleFPS: 2, AnalysisWidth: 640, PlayerSig: "162:..."}
	band := ConfigKey{SampleFPS: 2, AnalysisWidth: 640, PlayerSig: "162:...", PlayerSigMR: "mr3:..."}
	band2 := ConfigKey{SampleFPS: 2, AnalysisWidth: 640, PlayerSig: "162:...", PlayerSigMR: "mr3:...other"}

	if cacheKey("fp", nil, single) == cacheKey("fp", nil, band) {
		t.Fatal("a band-model scan shares a cache entry with a single-histogram scan of the same signature")
	}
	if cacheKey("fp", nil, band) == cacheKey("fp", nil, band2) {
		t.Fatal("two different band models share a cache entry — re-seeding would replay the old scan")
	}
}

// TestPlayerPresenceAnalyzerMRScansTheBandModel drives the multi-region
// wrapper end to end on real media: the band model measured from the same
// source must produce a scan whose track says which model it is and whose
// samples find the person — a track of all zeros would be a band scan that
// never compared anything.
func TestPlayerPresenceAnalyzerMRScansTheBandModel(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	dir := t.TempDir()
	path, err := testmedia.GenerateRally(dir, "match.mp4", 320, 240, 25, 14.3,
		[]testmedia.RallySpec{{Start: 0, End: 14, HitEvery: 0.5}})
	if err != nil {
		t.Fatal(err)
	}
	tools := media.Tools{FFmpeg: "ffmpeg", FFprobe: "ffprobe"}
	probe, err := media.ProbeFile(context.Background(), tools, path)
	if err != nil {
		t.Fatal(err)
	}

	sig, mr, _, err := player.MeasureSignature(context.Background(), tools, path,
		[]float64{0.3, 0.3, 0.2, 0.2}, 7, probe.DurationSec)
	if err != nil {
		t.Fatal(err)
	}
	if mr == nil {
		t.Fatal("the fixture's square spot yielded no band model — the measure pass is single-histogram only")
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tracks, err := PlayerPresenceAnalyzer{Sig: sig, MR: mr, SpotHPerW: 1.0}.Analyze(context.Background(),
		Options{Tools: tools}, path, false, logger)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 1 {
		t.Fatalf("got %d tracks, want one presence track", len(tracks))
	}
	tr := tracks[0]
	if tr.Analyzer != "player_presence" || tr.Unit != "mr-patch-match" {
		t.Fatalf("track identity = %s/%s, want player_presence/mr-patch-match", tr.Analyzer, tr.Unit)
	}
	if len(tr.Samples) == 0 {
		t.Fatal("the band-model presence track is empty")
	}
	best := 0.0
	for _, s := range tr.Samples {
		if s.V > best {
			best = s.V
		}
	}
	if best <= 0 {
		t.Fatal("every sample scored 0 against a band model measured from this same source")
	}
}

// spyAnalyzer records whether it ran: the cache question is "did the scan
// execute", not "what did it produce".
type spyAnalyzer struct {
	calls int
}

func (s *spyAnalyzer) Name() string { return "player_presence" }
func (s *spyAnalyzer) Version() int { return 1 }
func (s *spyAnalyzer) Analyze(_ context.Context, _ Options, _ string, _ bool, _ *slog.Logger) ([]FeatureTrack, error) {
	s.calls++
	return []FeatureTrack{{Analyzer: s.Name()}}, nil
}

// TestRunReSeedsThePresenceScanWhenTheSignatureChanges drives the wiring the
// unit test on cacheKey cannot see: Options.PlayerSig must reach the cache
// key Run builds. Same signature twice hits the cache once; a re-seeded
// signature re-scans — which is the whole property the hash exists for.
func TestRunReSeedsThePresenceScanWhenTheSignatureChanges(t *testing.T) {
	store := NewStore(t.TempDir())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	spy := &spyAnalyzer{}
	opts := Options{PlayerSig: "aaaa"}

	if _, err := Run(context.Background(), store, opts, []Analyzer{spy}, "unused.mp4", "fp1", 10, false, logger); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), store, opts, []Analyzer{spy}, "unused.mp4", "fp1", 10, false, logger); err != nil {
		t.Fatal(err)
	}
	if spy.calls != 1 {
		t.Fatalf("the same signature ran the scan %d times; the second run must hit the cache", spy.calls)
	}

	opts.PlayerSig = "bbbb"
	if _, err := Run(context.Background(), store, opts, []Analyzer{spy}, "unused.mp4", "fp1", 10, false, logger); err != nil {
		t.Fatal(err)
	}
	if spy.calls != 2 {
		t.Fatal("a re-seeded signature replayed the old scan instead of running again")
	}
}
