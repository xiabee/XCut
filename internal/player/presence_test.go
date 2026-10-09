package player

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/testmedia"
)

// The presence path talks to ffmpeg twice — MeasureSignature probes first,
// then decodes; ScanPresence only decodes. The child's argv decides which
// behaviour a test needs, so the test binary plays both roles: probe answers
// the one JSON ProbeFile reads, frames emits raw RGB24 with a deterministic
// content pattern. It has to be TestMain because the real caller decides the
// argument list, which the testing flag parser would reject.
func TestMain(m *testing.M) {
	// The probe role is picked by argv, not by a separate env var: one test
	// (MeasureSignature) plays both roles in a row — the child is asked to
	// probe first and decode second.
	if os.Getenv("XCUT_FAKE_PLAYER_PROBE") == "1" && hasArg("-show_format") {
		fmt.Println(`{"format":{"duration":"10","format_name":"mov,mp4"},"streams":[` +
			`{"codec_type":"video","codec_name":"h264","width":640,"height":360,"duration":"10"}]}`)
		os.Exit(0)
	}
	if mode := os.Getenv("XCUT_FAKE_PLAYER_FRAMES"); mode != "" {
		fakeFrames(mode)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func hasArg(want string) bool {
	for _, a := range os.Args[1:] {
		if a == want {
			return true
		}
	}
	return false
}

// fakeFrames parses its own -vf for the scale geometry and writes four frames
// in odd-sized chunks, so the reader's frame reassembly is exercised across
// write boundaries rather than fed neat frame-sized records. "scan" ends with
// a red frame (the scan test needs the curve to move); "measure" is green
// throughout (the model should absorb what it is fed).
func fakeFrames(mode string) {
	outW, outH := 320, 180
	cropped := false
	for i, a := range os.Args {
		if a == "-vf" && i+1 < len(os.Args) {
			for _, f := range strings.Split(os.Args[i+1], ",") {
				if w, h, ok := parseScale(f); ok {
					outW, outH = w, h
				}
				if strings.HasPrefix(f, "crop=") {
					cropped = true
				}
			}
		}
	}
	green := greenFrame(outW, outH)
	red := make([]byte, len(green))
	for i := 0; i < len(red); i += 3 {
		red[i] = 255
	}
	// "measure" answers in the color the crop asked for: a run that lost its
	// crop filter must come back with a model built from the wrong pixels,
	// not with one that looks fine.
	var frame []byte
	switch mode {
	case "measure":
		fill := green
		if !cropped {
			fill = red
		}
		frame = append(frame, fill...)
		frame = append(frame, fill...)
		frame = append(frame, fill...)
	case "scan":
		frame = append(frame, green...)
		frame = append(frame, green...)
		frame = append(frame, green...)
		frame = append(frame, red...)
	}
	const chunk = 997 // deliberately not a multiple of the frame size
	for off := 0; off < len(frame); off += chunk {
		end := off + chunk
		if end > len(frame) {
			end = len(frame)
		}
		if _, err := os.Stdout.Write(frame[off:end]); err != nil {
			os.Exit(1)
		}
	}
}

func parseScale(f string) (w, h int, ok bool) {
	if !strings.HasPrefix(f, "scale=") {
		return 0, 0, false
	}
	parts := strings.SplitN(strings.TrimPrefix(f, "scale="), ":", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	w, err1 := strconv.Atoi(parts[0])
	h, err2 := strconv.Atoi(parts[1])
	return w, h, err1 == nil && err2 == nil
}

func greenFrame(w, h int) []byte {
	b := make([]byte, w*h*3)
	for i := 1; i < len(b); i += 3 {
		b[i] = 255 // RGB green
	}
	return b
}

func greenSignature(t *testing.T) Signature {
	t.Helper()
	sig, err := BuildSignature(2, 2, [][]byte{{0, 255, 0, 0, 255, 0, 0, 255, 0, 0, 255, 0}})
	if err != nil {
		t.Fatal(err)
	}
	return sig
}

// TestScanPresenceReassemblesWhatTheChildStreams drives the whole decode:
// four frames arriving in 997-byte chunks must come out as four samples with
// the frame clock's spacing and the frames' own values. A splitter that drops
// or carries a stray byte turns the red final frame into a mixture, so the
// exact value sequence is the assertion.
func TestScanPresenceReassemblesWhatTheChildStreams(t *testing.T) {
	t.Setenv("XCUT_FAKE_PLAYER_FRAMES", "scan")
	tools := media.Tools{FFmpeg: os.Args[0]}

	samples, err := ScanPresence(context.Background(), tools, "unused.mp4",
		640, 360, greenSignature(t), 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 4 {
		t.Fatalf("got %d samples, want the 4 frames the child wrote", len(samples))
	}
	want := []float64{1, 1, 1, 0}
	for i, s := range samples {
		if wantT := float64(i) / PresenceScanFPS; s.T < wantT-1e-9 || s.T > wantT+1e-9 {
			t.Fatalf("sample %d is at %.3f, want the frame clock's %.3f", i, s.T, wantT)
		}
		if s.V < float64(want[i])-1e-9 || s.V > float64(want[i])+1e-9 {
			t.Fatalf("sample %d scores %.3f, want %.0f — the frames did not survive reassembly", i, s.V, want[i])
		}
	}
}

func TestScanPresenceRefusesAnEmptySignature(t *testing.T) {
	if _, err := ScanPresence(context.Background(), media.Tools{FFmpeg: os.Args[0]},
		"x.mp4", 640, 360, Signature{}, 0, 2); err == nil {
		t.Fatal("an empty signature scanned instead of refusing")
	}
}

func TestScanPresenceRefusesAnEmptyRange(t *testing.T) {
	if _, err := ScanPresence(context.Background(), media.Tools{FFmpeg: os.Args[0]},
		"x.mp4", 640, 360, greenSignature(t), 2, 2); err == nil {
		t.Fatal("an empty range scanned instead of refusing")
	}
}

// greenMultiRegion builds a band model whose every band is the green the fake
// scan writes, so a green frame's best window must score 1 and the red final
// frame 0 — the same value sequence the single-histogram scan pins, through
// the band path.
func greenMultiRegion(t *testing.T) MultiRegionSignature {
	t.Helper()
	mb := NewMultiRegionBuilder()
	for band := 0; band < RegionCount; band++ {
		row := []byte{0, 255, 0, 0, 255, 0, 0, 255, 0, 0, 255, 0}
		if err := mb.bands[band].Add(4, 1, row); err != nil {
			t.Fatal(err)
		}
	}
	mr, err := mb.Build()
	if err != nil {
		t.Fatal(err)
	}
	return mr
}

// TestScanPresenceMRReassemblesWhatTheChildStreams drives the band-model scan
// end to end on the fake decoder: the same four frames in odd chunks, scored
// as person-shaped windows. The value sequence is the assertion — the red
// final frame matches no band, and a splitter bug would smear it.
func TestScanPresenceMRReassemblesWhatTheChildStreams(t *testing.T) {
	t.Setenv("XCUT_FAKE_PLAYER_FRAMES", "scan")
	tools := media.Tools{FFmpeg: os.Args[0]}

	samples, err := ScanPresenceMR(context.Background(), tools, "unused.mp4",
		640, 360, greenMultiRegion(t), 1.0, Signature{}, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 4 {
		t.Fatalf("got %d samples, want the 4 frames the child wrote", len(samples))
	}
	want := []float64{1, 1, 1, 0}
	for i, s := range samples {
		if s.V < float64(want[i])-1e-9 || s.V > float64(want[i])+1e-9 {
			t.Fatalf("sample %d scores %.3f, want %.0f — the band scan did not survive reassembly", i, s.V, want[i])
		}
	}
}

func TestScanPresenceMRRefuses(t *testing.T) {
	tools := media.Tools{FFmpeg: os.Args[0]}
	if _, err := ScanPresenceMR(context.Background(), tools, "x.mp4", 640, 360,
		MultiRegionSignature{}, 1.0, Signature{}, 0, 2); err == nil {
		t.Fatal("an empty band model scanned instead of refusing")
	}
	if _, err := ScanPresenceMR(context.Background(), tools, "x.mp4", 640, 360,
		greenMultiRegion(t), 1.0, Signature{}, 2, 2); err == nil {
		t.Fatal("an empty range scanned instead of refusing")
	}
	if _, err := ScanPresenceMR(context.Background(), tools, "x.mp4", 640, 360,
		greenMultiRegion(t), 0, Signature{}, 0, 2); err == nil {
		t.Fatal("a zero spot aspect scanned instead of refusing")
	}
}

// TestPatchMaxMRKeepsScanningWhenTheSpotIsTallerThanTheFrame pins the clamp:
// a portrait spot aspect on landscape video must not produce a window that
// never fits — the scan runs, the best window is the whole frame height, and
// the score stays a real number in [0,1].
func TestPatchMaxMRKeepsScanningWhenTheSpotIsTallerThanTheFrame(t *testing.T) {
	mr := greenMultiRegion(t)
	w, h := 320, 180
	frame := greenFrame(w, h)
	v := s_patchMaxMR(mr, 3.0, w, h, frame)
	if v < 0.999 || v > 1.001 {
		t.Fatalf("all-green frame with a too-tall spot scored %.3f, want ~1 — the clamp broke the scan", v)
	}
}

// TestFrameGeomCapsTheLongerSide pins the sampling geometry: landscape keeps
// the width rule it always had, portrait media and tall crops cap their
// HEIGHT instead — and the capped shape must keep a full-asset scan under the
// streaming budget, which the width rule broke for portrait sources.
func TestFrameGeomCapsTheLongerSide(t *testing.T) {
	cases := []struct {
		w, h, ew, eh int
	}{
		{1280, 720, 320, 180}, // landscape: unchanged
		{1920, 1080, 320, 180},
		{720, 1280, 180, 320}, // portrait: height capped
		{1080, 1920, 180, 320},
		{1000, 1000, 320, 320}, // square
		{100, 4000, 8, 320},    // extreme: the short side shrinks with it
		{4000, 100, 320, 8},
	}
	for _, c := range cases {
		w, h := frameGeom(c.w, c.h)
		if w != c.ew || h != c.eh {
			t.Fatalf("frameGeom(%d,%d) = %dx%d, want %dx%d", c.w, c.h, w, h, c.ew, c.eh)
		}
	}
	// The property the cap exists for: the per-frame sample cost stops
	// depending on the source's shape. (Budget fit is then a duration
	// property — ~25 minutes of 2fps scan per 512 MB — identical for every
	// aspect, not a portrait penalty.)
	lw, lh := frameGeom(1280, 720)
	pw, ph := frameGeom(720, 1280)
	if lw*lh != pw*ph {
		t.Fatalf("portrait sample %dx%d costs a different per-frame area than landscape %dx%d",
			pw, ph, lw, lh)
	}
}

// TestScanPresenceOnPortraitMedia drives a portrait fixture end to end: the
// shape the width rule used to starve scans, measures and scores like any
// other source.
func TestScanPresenceOnPortraitMedia(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	dir := t.TempDir()
	path, err := testmedia.GenerateRally(dir, "vertical.mp4", 240, 426, 25, 6.0,
		[]testmedia.RallySpec{{Start: 0, End: 5, HitEvery: 0.5}})
	if err != nil {
		t.Fatal(err)
	}
	tools := media.Tools{FFmpeg: "ffmpeg", FFprobe: "ffprobe"}

	sig, mr, _, err := MeasureSignature(context.Background(), tools, path,
		[]float64{0.3, 0.3, 0.3, 0.3}, 2, 6.0)
	if err != nil {
		t.Fatalf("portrait measure failed: %v", err)
	}
	if mr == nil {
		t.Fatal("portrait spot measured no band model")
	}
	samples, err := ScanPresence(context.Background(), tools, path, 240, 426, sig, 0, 6)
	if err != nil {
		t.Fatalf("portrait scan failed: %v", err)
	}
	if len(samples) == 0 {
		t.Fatal("portrait scan produced no samples")
	}
	mrSamples, err := ScanPresenceMR(context.Background(), tools, path, 240, 426, *mr, 1.0, Signature{}, 0, 6)
	if err != nil {
		t.Fatalf("portrait band scan failed: %v", err)
	}
	if len(mrSamples) == 0 {
		t.Fatal("portrait band scan produced no samples")
	}
	// The shortlisted form on the same media: every sample within the sweep's
	// value (the subset-max property) and non-empty.
	slSamples, err := ScanPresenceMR(context.Background(), tools, path, 240, 426, *mr, 1.0, sig, 0, 6)
	if err != nil {
		t.Fatalf("portrait shortlisted scan failed: %v", err)
	}
	if len(slSamples) != len(mrSamples) {
		t.Fatalf("shortlisted scan produced %d samples, want the sweep's %d", len(slSamples), len(mrSamples))
	}
	for i := range slSamples {
		if slSamples[i].V > mrSamples[i].V+1e-9 {
			t.Fatalf("sample %d: shortlisted %.4f exceeds the sweep's %.4f", i, slSamples[i].V, mrSamples[i].V)
		}
	}
}

// TestMeasureSignatureCropsAndBuildsTheModel is the measure arm: the spot
// rect must reach the child as a crop filter, the frames it answers must all
// be absorbed, and the model that comes back must be a normalized histogram
// of exactly that content.
func TestMeasureSignatureCropsAndBuildsTheModel(t *testing.T) {
	t.Setenv("XCUT_FAKE_PLAYER_PROBE", "1")
	t.Setenv("XCUT_FAKE_PLAYER_FRAMES", "measure")
	tools := media.Tools{FFmpeg: os.Args[0], FFprobe: os.Args[0]}
	path := filepath.Join(t.TempDir(), "source.mp4")
	if err := os.WriteFile(path, []byte("not media — the fake never reads it"), 0o644); err != nil {
		t.Fatal(err)
	}

	sig, _, frames, err := MeasureSignature(context.Background(), tools, path,
		[]float64{0, 0, 0.5, 1}, 5, 10)
	if err != nil {
		t.Fatal(err)
	}
	if frames != 3 {
		t.Fatalf("measured from %d frames, want the 3 the child wrote", frames)
	}
	sum := 0.0
	for _, b := range sig.Bins {
		sum += b
	}
	if sum < 0.999 || sum > 1.001 {
		t.Fatalf("histogram sums to %.4f, want an L1-normalized 1.0", sum)
	}
	if v, err := sig.ScoreFrame(2, 2, []byte{0, 255, 0, 0, 255, 0, 0, 255, 0, 0, 255, 0}); err != nil || v != 1 {
		t.Fatalf("green frame scores %.3f (err %v) against a green-built model", v, err)
	}
	if v, err := sig.ScoreFrame(2, 2, []byte{255, 0, 0, 255, 0, 0, 255, 0, 0, 255, 0, 0}); err != nil || v != 0 {
		t.Fatalf("red frame scores %.3f (err %v) against a green-built model", v, err)
	}
}

func TestMeasureSignatureRefusesABadRect(t *testing.T) {
	t.Setenv("XCUT_FAKE_PLAYER_PROBE", "1")
	tools := media.Tools{FFmpeg: os.Args[0], FFprobe: os.Args[0]}
	path := filepath.Join(t.TempDir(), "source.mp4")
	if err := os.WriteFile(path, []byte("placeholder"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := MeasureSignature(context.Background(), tools, path,
		[]float64{0.5, 0.5, 0.5}, 5, 10); err == nil {
		t.Fatal("a three-component rect was accepted")
	}
}

// TestPatchMaxPrefersTheConcentratedMatch is the property PatchFrac exists
// for: the same amount of green scores ~1.0 when it fills one person-sized
// window and only its density (~0.2) when scattered as single pixels — a
// whole-frame average could not tell those apart.
func TestPatchMaxPrefersTheConcentratedMatch(t *testing.T) {
	sig := greenSignature(t)
	const w, h = 320, 180

	concentrated := make([]byte, w*h*3)
	for y := 0; y < 36; y++ {
		for x := 256; x < 320; x++ {
			px := (y*w + x) * 3
			concentrated[px+1] = 255
		}
	}
	if got := s_patchMax(sig, w, h, concentrated); got < 0.99 {
		t.Fatalf("a window-filling green block scores %.3f, want ~1.0", got)
	}

	dispersed := make([]byte, w*h*3)
	for px := 0; px+2 < len(dispersed); px += 3 {
		if (px/3)%5 == 0 {
			dispersed[px+1] = 255
		}
	}
	if got := s_patchMax(sig, w, h, dispersed); got >= 0.5 {
		t.Fatalf("a 20%%-density scatter scores %.3f; the best window must not inflate a scatter", got)
	}
}

func TestRectPixelsValidatesAndClamps(t *testing.T) {
	x, y, w, h, err := rectPixels([]float64{0.1, 0.2, 0.3, 0.4}, 100, 50)
	if err != nil || x != 10 || y != 10 || w != 30 || h != 20 {
		t.Fatalf("rectPixels = %d,%d %dx%d (err %v), want 10,10 30x20", x, y, w, h, err)
	}
	x, y, w, h, err = rectPixels([]float64{0.9, 0, 0.5, 1}, 100, 50)
	if err != nil || x != 90 || w != 10 || y != 0 || h != 50 {
		t.Fatalf("overflow rect = %d,%d %dx%d (err %v), want the right edge clamped to 90,0 10x50", x, y, w, h, err)
	}
	for _, bad := range [][]float64{{0.5, 0.5, 0.5}, {1.5, 0, 0.1, 0.1}, {-0.1, 0, 0.1, 0.1}, {0, 0, 0.001, 0.001}} {
		if _, _, _, _, err := rectPixels(bad, 100, 50); err == nil {
			t.Fatalf("rect %v was accepted", bad)
		}
	}
}

func TestFrameGeomScalesToFloorsTheHeight(t *testing.T) {
	if w, h := frameGeom(640, 360); w != 320 || h != 180 {
		t.Fatalf("frameGeom(640,360) = %dx%d, want 320x180", w, h)
	}
	if w, h := frameGeom(1000, 2); w != 320 || h != 2 {
		t.Fatalf("frameGeom(1000,2) = %dx%d, want the height floored at 2", w, h)
	}
}

// TestSignatureContract pins the model's edges: the zero-value Builder really
// is zero-value ready, near-black pixels are noise rather than signal, an
// unfed model refuses to build, and the hash that keys the analysis cache
// distinguishes models while staying stable for equal ones.
func TestSignatureContract(t *testing.T) {
	var b Builder
	green12 := []byte{0, 255, 0, 0, 255, 0, 0, 255, 0, 0, 255, 0}
	if err := b.Add(2, 2, green12); err != nil {
		t.Fatalf("the zero-value Builder refused a frame: %v", err)
	}
	zero, err := b.Build()
	if err != nil || len(zero.Bins) != HueBins*SatBins*ValBins {
		t.Fatalf("the zero-value Builder did not become a full model (err %v)", err)
	}
	b = NewBuilder()
	if err := b.Add(2, 2, make([]byte, 12)); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Build(); err == nil {
		t.Fatal("an all-black (counted=0) model built instead of refusing")
	}
	b = NewBuilder()
	if err := b.Add(1, 2, make([]byte, 12)); err == nil {
		t.Fatal("a 4-byte stride mismatch was accepted as a 6-byte frame")
	}

	sig := greenSignature(t)
	other, err := BuildSignature(2, 2, [][]byte{{255, 0, 0, 255, 0, 0, 255, 0, 0, 255, 0, 0}})
	if err != nil {
		t.Fatal(err)
	}
	if SigHash(sig.Bins) == "" || SigHash(sig.Bins) != SigHash(greenSignature(t).Bins) {
		t.Fatal("the signature hash is not stable for equal models")
	}
	if SigHash(sig.Bins) == SigHash(other.Bins) {
		t.Fatal("green and red models hash identically")
	}
	if SigHash(nil) != "" {
		t.Fatal("an empty model hashes to something rather than nothing")
	}
}

// noiseFrame fills w*h RGB with a deterministic LCG pattern — no ffmpeg, no
// fixture file: the shortlist properties need adversarial pixel soup, not
// real media.
func noiseFrame(seed, w, h int) []byte {
	frame := make([]byte, w*h*3)
	s := uint32(seed)
	for i := 0; i < len(frame); i++ {
		s = s*1664525 + 1013904223
		frame[i] = byte(s >> 24)
	}
	return frame
}

// plantRect overwrites a rect with the pure green the green* helpers train
// on, at frame coordinates (a planted "person" the prefilter must rank).
func plantRect(frame []byte, w, x, y, rw, rh int) {
	for yy := y; yy < y+rh; yy++ {
		row := yy * w * 3
		for xx := x; xx < x+rw; xx++ {
			frame[row+xx*3+1] = 255
		}
	}
}

// TestMRShortlistFindsThePlantedPerson: a green rect planted on noise makes
// the winning window obvious — the shortlist must report the sweep's exact
// best, or the prefilter is not a prefilter but a different statistic.
func TestMRShortlistFindsThePlantedPerson(t *testing.T) {
	mr := greenMultiRegion(t)
	sig := greenSignature(t)
	const w, h = 320, 240
	aspect := 1.0

	for _, seed := range []int{1, 7, 42, 2026} {
		frame := noiseFrame(seed, w, h)
		plantRect(frame, w, 120, 80, 64, 64)
		full := s_patchMaxMR(mr, aspect, w, h, frame)
		short := s_patchMaxMRShortlist(mr, sig, aspect, w, h, frame)
		if math.Abs(full-short) > 1e-9 {
			t.Fatalf("seed %d: shortlisted %.6f != sweep %.6f — the prefilter missed the planted window", seed, short, full)
		}
	}
}

// TestMRShortlistNeverExceedsTheSweep: the prefilter scores a subset of the
// grid, so its max can only be lower or equal — on noise where nothing
// matches, on planted scenes, everywhere.
func TestMRShortlistNeverExceedsTheSweep(t *testing.T) {
	mr := greenMultiRegion(t)
	sig := greenSignature(t)
	const w, h = 320, 240
	for _, seed := range []int{2, 3, 5, 8, 13, 21} {
		frame := noiseFrame(seed, w, h)
		full := s_patchMaxMR(mr, 1.0, w, h, frame)
		short := s_patchMaxMRShortlist(mr, sig, 1.0, w, h, frame)
		if short > full+1e-9 {
			t.Fatalf("seed %d: shortlisted %.6f > sweep %.6f — the subset-max property broke", seed, short, full)
		}
		plantRect(frame, w, 40, 30, 96, 96)
		full = s_patchMaxMR(mr, 1.0, w, h, frame)
		short = s_patchMaxMRShortlist(mr, sig, 1.0, w, h, frame)
		if short > full+1e-9 {
			t.Fatalf("seed %d planted: shortlisted %.6f > sweep %.6f", seed, short, full)
		}
	}
}

// TestMRShortlistEmptySignatureIsTheFullSweep: an empty phase-1 signature
// must reproduce the un-prefiltered scan exactly — the analyzer's degrade
// path is the old statistic, byte for byte.
func TestMRShortlistEmptySignatureIsTheFullSweep(t *testing.T) {
	mr := greenMultiRegion(t)
	const w, h = 320, 240
	for _, seed := range []int{4, 9} {
		frame := noiseFrame(seed, w, h)
		full := s_patchMaxMR(mr, 1.0, w, h, frame)
		short := s_patchMaxMRShortlist(mr, Signature{}, 1.0, w, h, frame)
		if full != short {
			t.Fatalf("seed %d: empty-shortlist run scored %.6f, want the sweep's %.6f", seed, short, full)
		}
	}
}
