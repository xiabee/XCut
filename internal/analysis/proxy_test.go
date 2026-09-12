package analysis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xiabee/XCut/internal/config"
	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/testmedia"
)

func proxyTools() media.Tools { return media.ResolveTools(config.Default()) }

func proxyLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestProxyEnsureGeneratesOnce: a 640-wide fixture with a 320 analysis
// canvas generates exactly one proxy at the analysis geometry; a second
// Ensure reuses it (no re-encode).
func TestProxyEnsureGeneratesOnce(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	dir := t.TempDir()
	src, err := testmedia.Generate(dir, "src.mp4", testmedia.DefaultFixture(), 640, 480, 10)
	if err != nil {
		t.Fatal(err)
	}
	fp, err := media.Fingerprint(src)
	if err != nil {
		t.Fatal(err)
	}

	store := NewProxyStore(dir)
	p, used, err := store.Ensure(context.Background(), proxyTools(), src, fp, 320, 2.0, 0, proxyLogger())
	if err != nil {
		t.Fatal(err)
	}
	if !used || p == "" {
		t.Fatal("proxy must be generated for a source wider than the analysis canvas")
	}
	probe, err := media.ProbeFile(context.Background(), proxyTools(), p)
	if err != nil {
		t.Fatal(err)
	}
	if probe.Width != 320 {
		t.Errorf("proxy width %d, want 320", probe.Width)
	}
	if probe.FPS < 1.9 || probe.FPS > 2.1 {
		t.Errorf("proxy fps %.2f, want ~2", probe.FPS)
	}
	if !probe.HasAudio {
		t.Error("proxy must keep the audio track (RMS/onset analyzers read it)")
	}
	if filepath.Dir(p) != filepath.Join(dir, "proxy") {
		t.Errorf("proxy path %q outside the proxy dir", p)
	}

	// Second call must reuse the existing file byte-for-byte. mtime is no
	// longer a reuse signal (hits refresh it for LRU), so compare content.
	proxyHash := func(path string) string {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(b)
		return hex.EncodeToString(sum[:])
	}
	h1 := proxyHash(p)
	p2, used2, err := store.Ensure(context.Background(), proxyTools(), src, fp, 320, 2.0, 0, proxyLogger())
	if err != nil || !used2 || p2 != p {
		t.Fatalf("second Ensure: path=%q used=%v err=%v", p2, used2, err)
	}
	if proxyHash(p2) != h1 {
		t.Error("second Ensure re-encoded an existing proxy")
	}
}

// TestProxyHitRefreshesRecency: reusing an existing proxy promotes it in
// the LRU order, so a hot source's proxy survives budget pressure even
// when it was generated long ago.
func TestProxyHitRefreshesRecency(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	dir := t.TempDir()
	src, err := testmedia.Generate(dir, "src.mp4", testmedia.DefaultFixture(), 640, 480, 10)
	if err != nil {
		t.Fatal(err)
	}
	fp, err := media.Fingerprint(src)
	if err != nil {
		t.Fatal(err)
	}
	store := NewProxyStore(dir)
	p, used, err := store.Ensure(context.Background(), proxyTools(), src, fp, 320, 2.0, 0, proxyLogger())
	if err != nil || !used {
		t.Fatalf("Ensure: used=%v err=%v", used, err)
	}
	backdated := time.Unix(1000, 0)
	if err := os.Chtimes(p, backdated, backdated); err != nil {
		t.Fatal(err)
	}
	p2, used2, err := store.Ensure(context.Background(), proxyTools(), src, fp, 320, 2.0, 0, proxyLogger())
	if err != nil || !used2 || p2 != p {
		t.Fatalf("second Ensure: path=%q used=%v err=%v", p2, used2, err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.ModTime().After(backdated) {
		t.Error("proxy hit did not refresh recency (mtime still backdated)")
	}
}

// TestProxyDeclinedWhenSourceFits: no decode savings below the analysis
// width — Ensure must decline without writing anything.
func TestProxyDeclinedWhenSourceFits(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	dir := t.TempDir()
	src, err := testmedia.Generate(dir, "src.mp4", testmedia.DefaultFixture(), 320, 240, 10)
	if err != nil {
		t.Fatal(err)
	}
	fp, err := media.Fingerprint(src)
	if err != nil {
		t.Fatal(err)
	}

	store := NewProxyStore(dir)
	p, used, err := store.Ensure(context.Background(), proxyTools(), src, fp, 640, 2.0, 0, proxyLogger())
	if err != nil {
		t.Fatal(err)
	}
	if used || p != "" {
		t.Fatalf("proxy must be declined for a small source (used=%v path=%q)", used, p)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "proxy")); len(entries) != 0 {
		t.Error("declined Ensure must not write files")
	}
}

// TestProxyEviction: growth over MaxBytes prunes the oldest proxy.
func TestProxyEviction(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	dir := t.TempDir()
	store := NewProxyStore(dir)

	// Two proxies from distinct sources.
	var fps []string
	for i, name := range []string{"a.mp4", "b.mp4"} {
		src, err := testmedia.Generate(dir, name, testmedia.DefaultFixture(), 640, 480, 10)
		if err != nil {
			t.Fatal(err)
		}
		fp, err := media.Fingerprint(src)
		if err != nil {
			t.Fatal(err)
		}
		if _, used, err := store.Ensure(context.Background(), proxyTools(), src, fp, 320, 2.0, 0, proxyLogger()); err != nil || !used {
			t.Fatalf("proxy %d: used=%v err=%v", i, used, err)
		}
		fps = append(fps, fp)
	}
	if _, err := os.Stat(store.path(fps[0], 320, 2.0)); err != nil {
		t.Fatal(err)
	}

	// Budget equal to the newer proxy's size: only the oldest (a) must go.
	fiB, err := os.Stat(store.path(fps[1], 320, 2.0))
	if err != nil {
		t.Fatal(err)
	}
	store.MaxBytes = fiB.Size()
	removed, freed, err := store.EvictTo(store.MaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("eviction removed %d, want exactly the oldest proxy", removed)
	}
	if freed <= 0 {
		t.Errorf("freed %d bytes, want >0", freed)
	}
	if _, err := os.Stat(store.path(fps[1], 320, 2.0)); err != nil {
		t.Errorf("newer proxy must survive: %v", err)
	}
}

// TestProxyBitChangesCacheKey: proxy and original analysis of the same
// source under the same sampling config must land in different cache
// entries — a proxy result can never be served for original analysis.
func TestProxyBitChangesCacheKey(t *testing.T) {
	cfgOff := ConfigKey{SampleFPS: 2, AnalysisWidth: 640}
	cfgOn := ConfigKey{SampleFPS: 2, AnalysisWidth: 640, Proxy: true}
	if cacheKey("fp", []Analyzer{stubAnalyzer{}}, cfgOff) == cacheKey("fp", []Analyzer{stubAnalyzer{}}, cfgOn) {
		t.Fatal("proxy flag must change the cache key")
	}
}

// stubAnalyzer satisfies Analyzer for key-computation tests.
type stubAnalyzer struct{}

func (stubAnalyzer) Name() string { return "stub" }
func (stubAnalyzer) Version() int { return 1 }
func (stubAnalyzer) Kind() string { return "stub" }
func (stubAnalyzer) Analyze(context.Context, Options, string, bool, *slog.Logger) ([]FeatureTrack, error) {
	return nil, nil
}

// hangingAnalyzer blocks until its context is done — simulates a hung
// ffmpeg that must not pin a worker slot forever.
type hangingAnalyzer struct{ called *bool }

func (h hangingAnalyzer) Name() string { return "hanging" }
func (h hangingAnalyzer) Version() int { return 1 }
func (h hangingAnalyzer) Kind() string { return "hanging" }
func (h hangingAnalyzer) Analyze(ctx context.Context, _ Options, _ string, _ bool, _ *slog.Logger) ([]FeatureTrack, error) {
	*h.called = true
	<-ctx.Done()
	return nil, ctx.Err()
}

// instantAnalyzer emits one track immediately.
type instantAnalyzer struct{}

func (instantAnalyzer) Name() string { return "instant" }
func (instantAnalyzer) Version() int { return 1 }
func (instantAnalyzer) Kind() string { return "instant" }
func (instantAnalyzer) Analyze(context.Context, Options, string, bool, *slog.Logger) ([]FeatureTrack, error) {
	return []FeatureTrack{{Kind: "instant", Samples: []Sample{{T: 0, V: 1}}}}, nil
}

// TestRunPerCallTimeout: a hung analyzer is cut off by CallTimeout with an
// error naming it; a following analyzer on a healthy call is unaffected.
func TestRunPerCallTimeout(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	called := false
	log := proxyLogger()

	// Timeout fires for the hanging analyzer.
	_, err := Run(context.Background(), store, Options{CallTimeout: 50 * time.Millisecond},
		[]Analyzer{hangingAnalyzer{called: &called}}, "no-file.mp4", "fp", 1, false, log)
	if err == nil {
		t.Fatal("hanging analyzer must fail on CallTimeout")
	}
	if !strings.Contains(err.Error(), "hanging") {
		t.Errorf("timeout error must name the analyzer: %v", err)
	}
	if !called {
		t.Error("hanging analyzer was never invoked")
	}

	// A generous timeout lets a fast analyzer succeed.
	res, err := Run(context.Background(), store, Options{CallTimeout: 30 * time.Second},
		[]Analyzer{instantAnalyzer{}}, "no-file.mp4", "fp", 1, false, log)
	if err != nil {
		t.Fatalf("fast analyzer must succeed: %v", err)
	}
	if len(res.Tracks) != 1 || res.Tracks[0].Kind != "instant" {
		t.Errorf("unexpected result: %+v", res.Tracks)
	}

	// CallTimeout=0 disables the cap (ctx still cancels).
	_, err = Run(context.Background(), store, Options{},
		[]Analyzer{instantAnalyzer{}}, "no-file.mp4", "fp", 1, false, log)
	if err != nil {
		t.Fatalf("CallTimeout=0 must not interfere: %v", err)
	}
}

// TestProxyGeometryChangeRegenerates: raising analysis_width or changing
// frame_sample_fps must generate a NEW proxy at the new geometry — the old
// file stays untouched on disk (stale, ages out via eviction) and is never
// silently reused for a different canvas.
func TestProxyGeometryChangeRegenerates(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	dir := t.TempDir()
	src, err := testmedia.Generate(dir, "src.mp4", testmedia.DefaultFixture(), 960, 540, 10)
	if err != nil {
		t.Fatal(err)
	}
	fp, err := media.Fingerprint(src)
	if err != nil {
		t.Fatal(err)
	}
	store := NewProxyStore(dir)
	ctx := context.Background()

	p320, used, err := store.Ensure(ctx, proxyTools(), src, fp, 320, 2.0, 0, proxyLogger())
	if err != nil || !used {
		t.Fatalf("first proxy: used=%v err=%v", used, err)
	}
	fi320, err := os.Stat(p320)
	if err != nil {
		t.Fatal(err)
	}

	// Wider analysis canvas → new proxy at 480, old one untouched.
	p480, used, err := store.Ensure(ctx, proxyTools(), src, fp, 480, 2.0, 0, proxyLogger())
	if err != nil || !used {
		t.Fatalf("wider proxy: used=%v err=%v", used, err)
	}
	if p480 == p320 {
		t.Fatal("width change must not reuse the 320px proxy")
	}
	probe, err := media.ProbeFile(ctx, proxyTools(), p480)
	if err != nil {
		t.Fatal(err)
	}
	if probe.Width != 480 {
		t.Errorf("new proxy width %d, want 480", probe.Width)
	}
	if fi, err := os.Stat(p320); err != nil || !fi.ModTime().Equal(fi320.ModTime()) {
		t.Errorf("old proxy must stay untouched: %v", err)
	}

	// Different sampling fps → another new proxy, ~5 fps.
	p5, used, err := store.Ensure(ctx, proxyTools(), src, fp, 320, 5.0, 0, proxyLogger())
	if err != nil || !used {
		t.Fatalf("fps-changed proxy: used=%v err=%v", used, err)
	}
	if p5 == p320 {
		t.Fatal("fps change must not reuse the 2fps proxy")
	}
	probe, err = media.ProbeFile(ctx, proxyTools(), p5)
	if err != nil {
		t.Fatal(err)
	}
	if probe.FPS < 4.8 || probe.FPS > 5.2 {
		t.Errorf("new proxy fps %.2f, want ~5", probe.FPS)
	}

	// Same geometry again → reuse (no re-encode).
	p480again, used, err := store.Ensure(ctx, proxyTools(), src, fp, 480, 2.0, 0, proxyLogger())
	if err != nil || !used || p480again != p480 {
		t.Fatalf("reuse after geometry cycle: path=%q used=%v err=%v", p480again, used, err)
	}
}
