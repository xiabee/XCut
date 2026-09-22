package pipeline

import (
	"context"
	"io"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xiabee/XCut/internal/analysis"
	"github.com/xiabee/XCut/internal/config"
	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/render"
	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/style"
	"github.com/xiabee/XCut/internal/testmedia"
	"github.com/xiabee/XCut/internal/timeline"
	"github.com/xiabee/XCut/internal/workspace"
)

// The reel is cut to the music laid under it, not to its own location audio — that
// is the measured finding behind this feature (docs/EVAL.md: a match's crowd
// carries no grid worth believing). So the tests below are built to tell those
// two apart: the video pulses at 150 BPM, the bed at 120, and an end that moved
// onto 0.5 s while 0.4 s was also reachable proves which one won.

const (
	videoHitEvery = 0.4  // the footage's own pulse (150 BPM)
	bedHitEvery   = 0.5  // the music bed's pulse (120 BPM)
	bedSnap       = 0.25 // half a beat: every free end can then reach one
)

type bedFixture struct {
	d     Deps
	p     *storage.Project
	bed   string
	video string
}

func newBedFixture(t *testing.T) bedFixture {
	t.Helper()
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	dir := t.TempDir()
	video, err := testmedia.GenerateRally(dir, "video.mp4", 320, 240, 25, 14,
		[]testmedia.RallySpec{{Start: 0, End: 13, HitEvery: videoHitEvery}})
	if err != nil {
		t.Fatal(err)
	}
	// The bed is audio-only and arrives through the same door a user's file does:
	// a real file on disk, not a list of numbers handed to the estimator.
	clicks, err := testmedia.GenerateRally(dir, "clicks.mp4", 320, 240, 25, 14,
		[]testmedia.RallySpec{{Start: 0, End: 13, HitEvery: bedHitEvery}})
	if err != nil {
		t.Fatal(err)
	}
	bed := filepath.Join(dir, "bed.m4a")
	// The context outlives this helper: the Deps it returns keeps using it, so
	// the cancel belongs to the test's cleanup and not to a defer here (a defer
	// cancelled every job the test then tried to run).
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	t.Cleanup(cancel)
	if out, err := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-v", "error", "-y",
		"-i", clicks, "-vn", "-c:a", "aac", "-b:a", "96k", bed).CombinedOutput(); err != nil {
		t.Fatalf("extract the bed: %v (%s)", err, out)
	}

	root := t.TempDir()
	cfg := config.Default()
	cfg.Workspace = root
	if err := config.Resolve(cfg); err != nil {
		t.Fatal(err)
	}
	ws := workspace.New(root)
	if err := ws.Ensure(); err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(ws.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	d := NewDeps(ctx, db, ws, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	p, err := db.CreateProject(ctx, "bed")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.ImportAsset(p, video); err != nil {
		t.Fatal(err)
	}
	if err := d.AnalyzeProject(p, nil); err != nil {
		t.Fatal(err)
	}
	return bedFixture{d: d, p: p, bed: bed, video: video}
}

func reqFor(f bedFixture, music string, snap float64) TimelineRequest {
	// 7.7 s, odd on purpose: a round ask is answered by max_clip_duration, which
	// lands on the half-second lattice by construction and leaves the snap with
	// nothing to prove.
	return TimelineRequest{Style: "badminton_highlight", Duration: 7.7, BeatSnap: snap, Music: music}
}

// TestBedGridOutranksTheSourceAudio is the precedence claim end to end.
func TestBedGridOutranksTheSourceAudio(t *testing.T) {
	f := newBedFixture(t)

	noBed, err := f.d.BuildTimeline(f.p, reqFor(f, "", bedSnap))
	if err != nil {
		t.Fatal(err)
	}
	withBed, err := f.d.BuildTimeline(f.p, reqFor(f, f.bed, bedSnap))
	if err != nil {
		t.Fatal(err)
	}

	if withBed.Metadata[timeline.MetaMusic] != f.bed {
		t.Fatalf("the document does not name its bed: %q", withBed.Metadata[timeline.MetaMusic])
	}
	bpm, err := strconv.ParseFloat(withBed.Metadata[timeline.MetaMusicBPM], 64)
	if err != nil || math.Abs(bpm-120) > 1 {
		t.Fatalf("music_bpm = %q, want the bed's 120 (the footage itself is 150): %v",
			withBed.Metadata[timeline.MetaMusicBPM], err)
	}

	a, b := clipsOf(noBed), clipsOf(withBed)
	if len(a) != len(b) || len(a) == 0 {
		t.Fatalf("the bed changed the selection (%d vs %d clips); it may only move ends", len(a), len(b))
	}
	moved, onBed, onVideo := 0, 0, 0
	for i := range a {
		if math.Abs(a[i].SourceStart-b[i].SourceStart) > 1e-6 {
			t.Fatalf("clip %d: start moved %v → %v", i, a[i].SourceStart, b[i].SourceStart)
		}
		if math.Abs(b[i].SourceEnd-a[i].SourceEnd) > bedSnap+1e-6 {
			t.Fatalf("clip %d moved %.4f s, past the %g tolerance", i, b[i].SourceEnd-a[i].SourceEnd, bedSnap)
		}
		if nearPulse(b[i].SourceEnd, bedHitEvery) {
			onBed++
		}
		if nearPulse(b[i].SourceEnd, videoHitEvery) {
			onVideo++
		}
		if math.Abs(b[i].SourceEnd-a[i].SourceEnd) > 1e-6 {
			moved++
			if _, ok := b[i].Metadata["beat"]; !ok {
				t.Fatalf("clip %d moved without recording the beat it moved to", i)
			}
		}
	}
	if moved == 0 {
		t.Fatalf("the bed moved nothing (ends %v) — the grid is not reaching selection", endsOf(b))
	}
	if onBed == 0 {
		t.Fatalf("no end landed on the bed's %.1f s grid (%v): the snap followed the footage, not the music",
			bedHitEvery, endsOf(b))
	}
	if onVideo > onBed {
		t.Fatalf("%d ends sit on the footage's own %.1f s pulse against %d on the bed's %.1f s: precedence is backwards",
			onVideo, videoHitEvery, onBed, bedHitEvery)
	}
	t.Logf("%d/%d ends moved, %d on the bed grid, %d on the footage's, bpm %s",
		moved, len(b), onBed, onVideo, withBed.Metadata[timeline.MetaMusicBPM])
}

// TestBedImpliesTheDefaultSnap checks the policy that a bed without an explicit
// tolerance still cuts on the beat, and that "off" really means off. The
// tolerance the caller asked for is already on the preset by the time prepareBed
// runs (timelineBody applies it), so each case states both sides of that.
func TestBedImpliesTheDefaultSnap(t *testing.T) {
	f := newBedFixture(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name          string
		reqSnap       float64
		presetSnap    float64
		wantTolerance float64
	}{
		{"neither side named one: the bed's default", 0, 0, DefaultBeatSnap},
		{"the caller said off", BeatSnapOff, 0, 0},
		{"the caller named a tolerance", 0.2, 0.2, 0.2},
		{"the style named one", 0, 0.15, 0.15},
	} {
		preset, err := style.Load("badminton_highlight")
		if err != nil {
			t.Fatal(err)
		}
		preset.BeatSnapTolerance = tc.presetSnap
		bed, err := f.d.prepareBed(ctx, reqFor(f, f.bed, tc.reqSnap), preset, f.d.analysisStore(), f.d.analysisOpts())
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if bed == nil || len(bed.beats) == 0 {
			t.Fatalf("%s: the 120 BPM bed produced no grid", tc.name)
		}
		if preset.BeatSnapTolerance != tc.wantTolerance {
			t.Fatalf("%s: tolerance = %g, want %g", tc.name, preset.BeatSnapTolerance, tc.wantTolerance)
		}
		if bed.musicGain != render.DefaultMusicGain || bed.sourceGain != render.DefaultSourceGain {
			t.Fatalf("%s: mix defaults = %g/%g, want %g/%g", tc.name, bed.musicGain, bed.sourceGain,
				render.DefaultMusicGain, render.DefaultSourceGain)
		}
	}
}

// TestBedMixIsAudibleInTheRenderedFile is the other half of the promise: the file
// a user gets has the music in it, at the levels the document recorded.
//
// The claim is made on transients, not on the file's period: measured here, a mix
// of two metronomes (footage at 0.4 s, bed at 0.5 s) leaves no single grid for the
// estimator to believe — a true statement about competing tracks rather than a
// broken mix, and the reason the ducked-render version of this test was dropped.
// What survives is sharper anyway: onsets that only the bed's lattice explains,
// present in the mixed file and absent in the same cut rendered without one.
func TestBedMixIsAudibleInTheRenderedFile(t *testing.T) {
	f := newBedFixture(t)
	tl, err := f.d.BuildTimeline(f.p, reqFor(f, f.bed, bedSnap))
	if err != nil {
		t.Fatal(err)
	}
	tools := f.d.tools()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	mixed := filepath.Join(t.TempDir(), "mixed.mp4")
	if err := render.Render(ctx, tl, render.Options{Tools: tools, TempDir: t.TempDir()}, mixed); err != nil {
		t.Fatalf("render with the bed: %v", err)
	}
	if !hasAudioStream(t, ctx, tools, mixed) {
		t.Fatal("the mixed reel has no audio stream — the bed did not make it in")
	}
	bare := filepath.Join(t.TempDir(), "bare.mp4")
	plain := *tl
	plain.Metadata = map[string]string{} // the same cut with no bed named
	if err := render.Render(ctx, &plain, render.Options{Tools: tools, TempDir: t.TempDir()}, bare); err != nil {
		t.Fatalf("render without the bed: %v", err)
	}
	mixedDB, bareDB := meanVolume(t, ctx, tools, mixed), meanVolume(t, ctx, tools, bare)
	if math.Abs(mixedDB-bareDB) < 0.5 {
		t.Fatalf("the bed left the level untouched (mixed %.1f dB, bare %.1f dB): nothing was mixed in",
			mixedDB, bareDB)
	}
	// The sharpest available claim about what the bed did: without it the file's
	// pulse is the footage's own 0.4 s, and with the ambience ducked the pulse is
	// the bed's 0.5 s. Two different numbers, both read off the rendered audio.
	bareOnsets := bedOnsets(t, ctx, tools, bare)
	bareGrid, ok := analysis.EstimateBeatGrid(bareOnsets, 10)
	if !ok || math.Abs(bareGrid.Period-videoHitEvery) > 0.05 {
		t.Fatalf("the bedless reel's pulse is %v s (ok=%v), want the footage's %.2f s — the control has to hold or the claim below means nothing",
			bareGrid.Period, ok, videoHitEvery)
	}

	if math.Abs(probeDuration(t, ctx, tools, mixed)-probeDuration(t, ctx, tools, bare)) > 0.2 {
		t.Fatal("the mix changed the reel's length; it should only change what is audible")
	}
	// The bed's fingerprint: transients on its 0.5 s lattice that are far from the
	// footage's 0.4 s one. Present in the mix, absent without it — that pair is
	// the claim, and it survives what a full-grid claim did not (measured here:
	// with two metronomes in one file no single period explains the onsets, which
	// is a true statement about competing tracks, not a broken mix).
	mixedMarks := bedOnlyPulses(t, ctx, tools, mixed)
	bareMarks := bedOnlyPulses(t, ctx, tools, bare)
	if mixedMarks < 3 {
		t.Fatalf("the mixed file shows only %d transients unique to the bed's grid — the music is not audible in it", mixedMarks)
	}
	if bareMarks > 0 {
		t.Fatalf("the bedless file already shows %d bed-unique transients: the control is broken, so the count above proves nothing",
			bareMarks)
	}
	t.Logf("levels: mixed %.1f dB against bare %.1f dB; %d bed-unique transients against %d",
		mixedDB, bareDB, mixedMarks, bareMarks)
}

// TestMissingBedIsRefused pins the promise the document makes: naming a track that
// is gone is an error, not a silent render without music.
func TestMissingBedIsRefused(t *testing.T) {
	f := newBedFixture(t)
	tl, err := f.d.BuildTimeline(f.p, reqFor(f, f.bed, bedSnap))
	if err != nil {
		t.Fatal(err)
	}
	tl.Metadata[timeline.MetaMusic] = filepath.Join(t.TempDir(), "moved-away.m4a")
	out := filepath.Join(t.TempDir(), "out.mp4")
	err = render.Render(context.Background(), tl, render.Options{Tools: f.d.tools(), TempDir: t.TempDir()}, out)
	if err == nil {
		t.Fatal("a missing bed must fail the render")
	}
	if !strings.Contains(err.Error(), "music file is missing") {
		t.Fatalf("error does not say what went wrong: %v", err)
	}
	if _, serr := os.Stat(out); !os.IsNotExist(serr) {
		t.Fatal("a refused render must leave no file behind")
	}
}

func endsOf(clips []timeline.Clip) []float64 {
	out := make([]float64, 0, len(clips))
	for _, c := range clips {
		out = append(out, c.SourceEnd)
	}
	return out
}

// nearPulse reports whether t sits within 0.05 s of a multiple of period, which is
// the same tolerance the estimator's own grid carries.
func nearPulse(t, period float64) bool {
	r := math.Mod(t, period)
	return r <= 0.05 || period-r <= 0.05
}

// bedOnlyPulses counts how many of a file's onsets sit on the bed's lattice and
// far from the footage's — transients only the music can explain.
func bedOnlyPulses(t *testing.T, ctx context.Context, tools media.Tools, path string) int {
	t.Helper()
	n := 0
	for _, o := range bedOnsets(t, ctx, tools, path) {
		if !nearPulse(o, bedHitEvery) {
			continue
		}
		r := math.Mod(o, videoHitEvery)
		if r <= 0.08 || videoHitEvery-r <= 0.08 {
			continue // the footage could have made this one itself
		}
		n++
	}
	return n
}

// probeDuration reads how long a rendered file actually is, so the claim that a
// mix adds audio without stretching the reel is measured on the output.
func probeDuration(t *testing.T, ctx context.Context, tools media.Tools, path string) float64 {
	t.Helper()
	out, err := exec.CommandContext(ctx, tools.FFprobe, "-v", "error",
		"-show_entries", "format=duration", "-of", "csv=p=0", path).CombinedOutput()
	if err != nil {
		t.Fatalf("probe duration %s: %v (%s)", filepath.Base(path), err, out)
	}
	d, perr := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if perr != nil {
		t.Fatalf("no duration for %s: %q", filepath.Base(path), out)
	}
	return d
}

// hasAudioStream asks ffprobe what streams the finished file carries — the claim
// is about the container, not about what ffmpeg reported while writing it.
func hasAudioStream(t *testing.T, ctx context.Context, tools media.Tools, path string) bool {
	t.Helper()
	out, err := exec.CommandContext(ctx, tools.FFprobe, "-v", "error", "-select_streams", "a",
		"-show_entries", "stream=index", "-of", "csv=p=0", path).CombinedOutput()
	if err != nil {
		t.Fatalf("probe %s: %v (%s)", filepath.Base(path), err, out)
	}
	return strings.TrimSpace(string(out)) != ""
}

// bedOnsets reads a rendered file's audio back through the shipped analyzer, so
// "the music is in the reel" is measured on the output.
func bedOnsets(t *testing.T, ctx context.Context, tools media.Tools, path string) []float64 {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	res, err := analysis.AudioOnsetAnalyzer{}.Analyze(ctx,
		analysis.Options{Tools: tools, SampleFPS: 25, AnalysisWidth: 640, CallTimeout: 2 * time.Minute},
		path, true, logger)
	if err != nil {
		t.Fatalf("analyze the rendered audio: %v", err)
	}
	var onsets []float64
	for _, tr := range res {
		if tr.Kind == "audio_onset" {
			for _, s := range tr.Samples {
				onsets = append(onsets, s.T)
			}
		}
	}
	if len(onsets) < 8 {
		t.Fatalf("the rendered file yielded %d onsets — too few to carry a pulse", len(onsets))
	}
	return onsets
}

// meanVolume is ffmpeg's own loudness reading of a file, used to show the mix
// changed what is audible rather than merely adding a stream.
func meanVolume(t *testing.T, ctx context.Context, tools media.Tools, path string) float64 {
	t.Helper()
	out, err := exec.CommandContext(ctx, tools.FFmpeg, "-hide_banner", "-i", path,
		"-map", "a:0", "-af", "volumedetect", "-f", "null", "-").CombinedOutput()
	if err != nil {
		t.Fatalf("volumedetect %s: %v (%s)", filepath.Base(path), err, media.Tail(out, 300))
	}
	text := string(out)
	i := strings.Index(text, "mean_volume:")
	if i < 0 {
		t.Fatalf("no mean_volume in the volumedetect output for %s:\n%s", filepath.Base(path), text)
	}
	fields := strings.Fields(text[i+len("mean_volume:"):])
	if len(fields) == 0 {
		t.Fatalf("mean_volume carried no number for %s", filepath.Base(path))
	}
	v, perr := strconv.ParseFloat(fields[0], 64)
	if perr != nil {
		t.Fatalf("cannot read mean_volume %q for %s: %v", fields[0], filepath.Base(path), perr)
	}
	return v
}
