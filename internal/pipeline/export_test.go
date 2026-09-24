package pipeline

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xiabee/XCut/internal/job"
	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/subs"
	"github.com/xiabee/XCut/internal/testmedia"
	"github.com/xiabee/XCut/internal/timeline"
)

// The export tap is one call that ends with a file on disk. Every stage it runs is
// covered by its own test elsewhere; what this file tests is the sequence — that
// the reel exists before the captions are styled for it, that the render is
// queued rather than swallowed into the tap's own slot, and that a second tap on
// the same project leaves the artifacts it found exactly as it found them.

// fakeTranscriptSidecar is a sidecar that knows where two lines fall and nothing
// about the words inside them — the ordinary transcript, and the one whose
// captions the karaoke writer used to refuse to style.
func fakeTranscriptSidecar(t *testing.T) string {
	t.Helper()
	if !hasPython() {
		t.Skip("no python interpreter on PATH")
	}
	script := `#!/usr/bin/env python3
import json, sys
req = json.loads(sys.stdin.read() or "{}")
op = req.get("op")
if op == "capabilities":
    result = {"ops": [{"op": "analyze"}], "models": [
        {"name": "transcript", "available": True, "loaded": False, "detail": "fake"}]}
elif op == "analyze":
    result = {"language": "zh", "segments": [
        {"start": 0.5, "end": 1.5, "text": "第一句台词"},
        {"start": 2.0, "end": 3.0, "text": "第二句台词"}]}
else:
    result = {}
sys.stdout.write(json.dumps({"protocol": 1, "ok": True, "op": op, "result": result}))
`
	path := filepath.Join(t.TempDir(), "fake-xcut-ai.py")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func hasPython() bool {
	for _, py := range []string{"python", "python3"} {
		if p, err := exec.LookPath(py); err == nil && p != "" {
			return true
		}
	}
	return false
}

func stepNamed(steps []ExportStep, name string) ExportStep {
	for _, s := range steps {
		if s.Step == name {
			return s
		}
	}
	return ExportStep{Step: name, Action: "absent"}
}

func awaitTerminal(t *testing.T, d Deps, id string) *storage.Job {
	t.Helper()
	deadline := time.Now().Add(180 * time.Second)
	for {
		j, err := d.DB.GetJob(context.Background(), id)
		if err != nil || j == nil {
			t.Fatalf("GetJob(%s): %v, %+v", id, err, j)
		}
		if j.Status != storage.StatusQueued && j.Status != storage.StatusRunning {
			return j
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s never finished (still %s)", id, j.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestExportTapReachesAFile(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	root := t.TempDir()
	media, err := testmedia.GenerateRally(root, "hall.mp4", 320, 240, 25, 14,
		[]testmedia.RallySpec{{Start: 0, End: 14, HitEvery: 1.2}})
	if err != nil {
		t.Fatal(err)
	}

	d, p := bareDeps(t)
	tlPath, err := d.TimelinePath(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	assPath, err := d.SubtitlesPath(p.ID, "ass")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.ImportAsset(p, media); err != nil {
		t.Fatal(err)
	}
	if err := d.AnalyzeProject(p, nil); err != nil {
		t.Fatal(err)
	}
	d.Cfg.Workers.AIBin = fakeTranscriptSidecar(t)
	// The preconditions the plan has to read the other way on: none of this exists
	// until the tap makes it.
	if _, err := os.Stat(tlPath); err == nil {
		t.Fatal("the fixture already has a reel; the tap's first step would prove nothing")
	}
	if _, err := os.Stat(assPath); !os.IsNotExist(err) {
		t.Fatal("the fixture already has captions")
	}

	id, steps, err := d.ExportProjectAsync(p, ExportRequest{
		Timeline: TimelineRequest{Style: DefaultExportStyle},
		Subs:     true,
		Out:      filepath.Join(root, "tap1.mp4"),
	})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	for _, name := range []string{"timeline", "subtitles", "render"} {
		if got := stepNamed(steps, name).Action; got != "create" {
			t.Errorf("step %s = %q on an empty project, want create", name, got)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	if err := d.Queue.WaitContext(ctx); err != nil {
		t.Fatalf("the tap never finished: %v", err)
	}
	if j := awaitTerminal(t, d, id); j.Status != storage.StatusSucceeded {
		t.Fatalf("export job ended %s: %s (%s)", j.Status, j.ErrorMessage, j.ErrorCode)
	}

	tlBytes, err := os.ReadFile(tlPath)
	if err != nil {
		t.Fatalf("the tap left no reel: %v", err)
	}
	if !strings.Contains(string(tlBytes), `"clips"`) {
		t.Errorf("the reel written by the tap carries no clips:\n%.200s", tlBytes)
	}
	ass, err := os.ReadFile(assPath)
	if err != nil {
		t.Fatalf("the tap left no captions: %v", err)
	}
	// The plain sidecar too: the panel offers both downloads, and a tap that wrote
	// only the styled one would leave the other button 404ing.
	srtPath, err := d.SubtitlesPath(p.ID, "srt")
	if err != nil {
		t.Fatal(err)
	}
	srt, err := os.ReadFile(srtPath)
	if err != nil {
		t.Fatalf("the tap left no .srt: %v", err)
	}
	if !strings.Contains(string(srt), "第一句台词") {
		t.Errorf("the .srt the tap wrote has no transcript in it:\n%s", srt)
	}
	head := string(ass)
	if len(head) > 600 {
		head = head[:600]
	}
	// The ordering property: these captions are styled against the canvas of a
	// reel that did not exist when the request arrived. A tap that transcribed
	// before it built would land on the writer's shipped 1280×720 reference here.
	for _, want := range []string{"PlayResX: 1080", "PlayResY: 1920", "Style: Caption,"} {
		if !strings.Contains(head, want) {
			t.Errorf("the captions the tap wrote lack %q:\n%s", want, head)
		}
	}
	if strings.Contains(string(ass), `\k`) {
		t.Errorf("no syllable was timed, yet the file carries karaoke sweeps:\n%s", head)
	}

	out, err := os.Stat(filepath.Join(root, "tap1.mp4"))
	if err != nil {
		t.Fatalf("no render on disk: %v", err)
	}
	if out.Size() == 0 {
		t.Fatal("the render file is empty")
	}

	// The render is a job of its own, queued by the tap — not work the tap did in
	// its own slot. That is what keeps resource.max_render_workers true.
	jobs, err := d.DB.ListJobs(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	var exports, renders int
	for _, j := range jobs {
		switch j.Type {
		case job.TypeExport:
			exports++
		case job.TypeRender:
			renders++
			if j.Status != storage.StatusSucceeded {
				t.Errorf("the render the tap queued ended %s: %s", j.Status, j.ErrorMessage)
			}
		}
	}
	if exports != 1 || renders != 1 {
		t.Errorf("one tap wrote %d export and %d render rows, want 1 and 1 — a render run inside the export body leaves no row of its own",
			exports, renders)
	}

	// Second tap on the same project: nothing to rebuild, nothing to re-transcribe,
	// and the bytes it found are the bytes it leaves.
	before, _ := os.ReadFile(assPath)
	id2, steps2, err := d.ExportProjectAsync(p, ExportRequest{
		Timeline: TimelineRequest{Style: DefaultExportStyle},
		Subs:     true,
		Out:      filepath.Join(root, "tap2.mp4"),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"timeline", "subtitles"} {
		if got := stepNamed(steps2, name).Action; got != "reuse" {
			t.Errorf("second tap: step %s = %q, want reuse", name, got)
		}
	}
	if err := d.Queue.WaitContext(ctx); err != nil {
		t.Fatalf("the second tap never finished: %v", err)
	}
	if j := awaitTerminal(t, d, id2); j.Status != storage.StatusSucceeded {
		t.Fatalf("second export job ended %s: %s", j.Status, j.ErrorMessage)
	}
	after, _ := os.ReadFile(assPath)
	if string(before) != string(after) {
		t.Errorf("the second tap rewrote captions it was told to reuse:\n%s\n---\n%s", before, after)
	}
	reel2, _ := os.ReadFile(tlPath)
	if string(tlBytes) != string(reel2) {
		t.Error("the second tap rebuilt a reel it was told to reuse")
	}
	if _, err := os.Stat(filepath.Join(root, "tap2.mp4")); err != nil {
		t.Errorf("the second tap produced no render: %v", err)
	}
}

// TestExportWithoutASidecarStillExports: the transcript is the one stage a machine
// may not be able to do, and a tap that gives up on the whole reel for it is worse
// than useless — the user asked for a video, not a lesson in sidecar installation.
func TestExportWithoutASidecarStillExports(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	root := t.TempDir()
	media, err := testmedia.GenerateRally(root, "hall.mp4", 320, 240, 25, 8,
		[]testmedia.RallySpec{{Start: 0, End: 8, HitEvery: 1.2}})
	if err != nil {
		t.Fatal(err)
	}
	d, p := bareDeps(t)
	assPath, err := d.SubtitlesPath(p.ID, "ass")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.ImportAsset(p, media); err != nil {
		t.Fatal(err)
	}
	if err := d.AnalyzeProject(p, nil); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Join(root, "empty-path")) // nothing resolves — no sidecar anywhere
	d.Cfg.Workers.AIBin = ""

	id, steps, err := d.ExportProjectAsync(p, ExportRequest{
		Timeline: TimelineRequest{Style: DefaultExportStyle},
		Subs:     true,
		Out:      filepath.Join(root, "silent.mp4"),
	})
	if err != nil {
		t.Fatal(err)
	}
	subs := stepNamed(steps, "subtitles")
	if subs.Action != "skip" || !strings.Contains(subs.Reason, "sidecar") {
		t.Errorf("with no sidecar the subtitles step read %q / %q, want skip naming the sidecar",
			subs.Action, subs.Reason)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	if err := d.Queue.WaitContext(ctx); err != nil {
		t.Fatal(err)
	}
	if j := awaitTerminal(t, d, id); j.Status != storage.StatusSucceeded {
		t.Fatalf("a missing sidecar failed the whole tap: %s (%s)", j.Status, j.ErrorMessage)
	}
	if _, err := os.Stat(assPath); !os.IsNotExist(err) {
		t.Error("captions appeared for a transcript that could not have been made")
	}
	// PATH was emptied to make "no sidecar" a fact rather than a hope, which also
	// means the render this queues may not find ffmpeg. Not a problem for the
	// claim: the tap reached a terminal, successful state on its own, and the
	// render is a separate row precisely so that its fate cannot be the tap's.
	jobs, err := d.DB.ListJobs(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	var renders int
	for _, j := range jobs {
		if j.Type == job.TypeRender {
			renders++
		}
	}
	if renders != 1 {
		t.Errorf("the tap queued %d render jobs, want exactly 1", renders)
	}
}

// TestExportReusesArtifactsItDidNotMake: a project whose reel and captions came
// from somewhere else — another style, another tool, a hand edit — is not the
// export's to overwrite. Both halves are written by hand here so a stage that ran
// anyway leaves a trace: a rebuilt reel changes the canvas, a re-transcribed one
// loses the sentinel text.
func TestExportReusesArtifactsItDidNotMake(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	root := t.TempDir()
	media, err := testmedia.GenerateRally(root, "hall.mp4", 320, 240, 25, 10,
		[]testmedia.RallySpec{{Start: 0, End: 10, HitEvery: 1.2}})
	if err != nil {
		t.Fatal(err)
	}
	d, p := bareDeps(t)
	assPath, err := d.SubtitlesPath(p.ID, "ass")
	if err != nil {
		t.Fatal(err)
	}
	tlPath, err := d.TimelinePath(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.ImportAsset(p, media); err != nil {
		t.Fatal(err)
	}
	if err := d.AnalyzeProject(p, nil); err != nil {
		t.Fatal(err)
	}
	// A horizontal reel, on purpose: the tap's own default is the vertical one, so
	// the canvas says which of the two wrote the file.
	if _, err := d.BuildTimeline(p, TimelineRequest{Style: "generic_highlight", Duration: 4}); err != nil {
		t.Fatal(err)
	}
	sentinel := "Dialogue: 0,0:00:00.50,0:00:01.50,Caption,,0,0,0,,手写的一句\n"
	if err := os.WriteFile(assPath, []byte(sentinel), 0o644); err != nil {
		t.Fatal(err)
	}
	reelBefore, err := os.ReadFile(tlPath)
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := timeline.LoadFile(tlPath)
	if err != nil {
		t.Fatal(err)
	}
	if fixture.Canvas.Width != 1920 {
		t.Fatalf("the fixture reel is %d×%d; the horizontal preset is what makes a rebuild visible here",
			fixture.Canvas.Width, fixture.Canvas.Height)
	}

	d.Cfg.Workers.AIBin = fakeTranscriptSidecar(t)
	id, steps, err := d.ExportProjectAsync(p, ExportRequest{
		Timeline: TimelineRequest{Style: DefaultExportStyle},
		Subs:     true,
		Out:      filepath.Join(root, "reuse.mp4"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := stepNamed(steps, "timeline").Action; got != "reuse" {
		t.Errorf("timeline step = %q with a saved reel present, want reuse", got)
	}
	if got := stepNamed(steps, "subtitles").Action; got != "reuse" {
		t.Errorf("subtitles step = %q with captions present, want reuse", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	if err := d.Queue.WaitContext(ctx); err != nil {
		t.Fatal(err)
	}
	if j := awaitTerminal(t, d, id); j.Status != storage.StatusSucceeded {
		t.Fatalf("export ended %s: %s", j.Status, j.ErrorMessage)
	}
	reelAfter, _ := os.ReadFile(tlPath)
	if string(reelBefore) != string(reelAfter) {
		t.Errorf("the tap rebuilt a reel it was told to reuse:\n%.300s\n---\n%.300s", reelBefore, reelAfter)
	}
	assAfter, _ := os.ReadFile(assPath)
	if string(assAfter) != sentinel {
		t.Errorf("the tap transcribed over captions it was told to reuse:\n%s", assAfter)
	}
}

// TestExportBurnsThePlainTranscriptWhenTheStyledOneCannotFit covers the third answer to a
// caption/reel mismatch: with no sidecar to lay the words out again, a .srt of the same
// transcript is chosen over a .ass whose PlayRes pair says it was sized for another frame.
// It asserts the *body's* choice through the line it puts on the record, because the plan
// sentence alone would still pass if the render burned the wrong file anyway.
func TestExportBurnsThePlainTranscriptWhenTheStyledOneCannotFit(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	root := t.TempDir()
	media, err := testmedia.GenerateRally(root, "hall.mp4", 320, 240, 25, 14,
		[]testmedia.RallySpec{{Start: 0, End: 14, HitEvery: 1.2}})
	if err != nil {
		t.Fatal(err)
	}
	d, p := bareDeps(t)
	var log bytes.Buffer
	d.Log = slog.New(slog.NewTextHandler(&log, nil))
	if _, err := d.ImportAsset(p, media); err != nil {
		t.Fatal(err)
	}
	if err := d.AnalyzeProject(p, nil); err != nil {
		t.Fatal(err)
	}
	// The reel exists before the tap is asked, so the canvas the captions are compared
	// with is knowable at ask time rather than only after a build.
	if _, err := d.BuildTimeline(p, TimelineRequest{Style: "generic_highlight", Duration: 4}); err != nil {
		t.Fatal(err)
	}
	tlPath, err := d.TimelinePath(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := timeline.LoadFile(tlPath)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Canvas.Width == 1080 && doc.Canvas.Height == 1920 {
		t.Fatalf("the fixture's reel is the same frame as the captions it must disagree with (%dx%d)",
			doc.Canvas.Width, doc.Canvas.Height)
	}

	// Both caption files exist, written by the product's own writers: the styled one
	// claims a frame the reel is not, the plain one claims nothing.
	tr := &subs.Transcript{Segments: []subs.Segment{{Start: 0.5, End: 2.5, Text: "hello there"}}}
	assPath, err := d.SubtitlesPath(p.ID, "ass")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(assPath), 0o755); err != nil {
		t.Fatal(err)
	}
	var styled strings.Builder
	if err := subs.WriteCaptionASS(tr, subs.KaraokeStyle{Width: 1080, Height: 1920}, &styled); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(assPath, []byte(styled.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	srtPath, err := d.SubtitlesPath(p.ID, "srt")
	if err != nil {
		t.Fatal(err)
	}
	var plain strings.Builder
	if err := subs.WriteSRT(tr, &plain); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srtPath, []byte(plain.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	// No sidecar: the words cannot be re-laid out, which is the only world where the
	// fallback applies — with one, the plan says "re-transcribed" instead. Narrowing
	// PATH to the FFmpeg directory keeps the render runnable while nothing that could
	// transcribe resolves, which clearing PATH entirely does not (it also hides ffmpeg,
	// and the render then fails for a reason this test is not about).
	d.Cfg.Workers.AIBin = ""
	ffmpegPath, ferr := exec.LookPath("ffmpeg")
	if ferr != nil {
		t.Skipf("no ffmpeg to keep on PATH: %v", ferr)
	}
	ffdir := filepath.Dir(ffmpegPath)
	if _, perr := os.Stat(filepath.Join(ffdir, "ffprobe")); perr != nil {
		if _, xerr := os.Stat(filepath.Join(ffdir, "ffprobe.exe")); xerr != nil {
			t.Skipf("ffprobe does not sit beside ffmpeg (%s), so narrowing PATH would break the render", ffdir)
		}
	}
	t.Setenv("PATH", ffdir)

	out := filepath.Join(root, "reel.mp4")
	id, steps, err := d.ExportProjectAsync(p, ExportRequest{
		Timeline: TimelineRequest{Style: DefaultExportStyle},
		Subs:     true,
		Out:      out,
	})
	if err != nil {
		t.Fatalf("ExportProjectAsync: %v", err)
	}
	got := stepNamed(steps, "subtitles")
	if !strings.HasPrefix(got.Reason, ExportFallbackSubtitles) {
		t.Errorf("plan said %q (%s), want the plain-transcript answer %q",
			got.Reason, got.Action, ExportFallbackSubtitles)
	}
	if strings.HasPrefix(got.Reason, ExportStaleSubtitles) {
		t.Error("a project with a usable .srt got the sentence that means the ill-fitted file will burn")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	if err := d.Queue.WaitContext(ctx); err != nil {
		t.Fatalf("the tap never finished: %v", err)
	}
	j := awaitTerminal(t, d, id)
	if j.Status != storage.StatusSucceeded {
		t.Fatalf("export job ended %s: %s (%s)", j.Status, j.ErrorMessage, j.ErrorCode)
	}
	text := log.String()
	for _, want := range []string{"burning the plain transcript", filepath.Base(srtPath)} {
		if !strings.Contains(text, want) {
			t.Errorf("the body never put %q on the record; the log was: %s", want, text)
		}
	}
	jobs, err := d.DB.ListJobs(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range jobs {
		if j.Type == job.TypeRender && j.Status != storage.StatusSucceeded {
			t.Fatalf("the render the tap queued ended %s: %s (%s)", j.Status, j.ErrorMessage, j.ErrorCode)
		}
	}
	if _, err := os.Stat(out); err != nil {
		t.Errorf("no reel at %s: %v", out, err)
	}
	// The rule selects; it does not repair. The file that was passed over is the bytes
	// it was, which is also what keeps an always-restyle rule from passing this test.
	b, err := os.ReadFile(assPath)
	if err != nil {
		t.Fatal(err)
	}
	if w, h, ok := subs.ReadASSFrame(bytes.NewReader(b)); !ok || w != 1080 || h != 1920 {
		t.Errorf("the fallback rewrote the file it decided not to use: %dx%d ok=%v", w, h, ok)
	}
}

// restyledReel stages a project whose captions were transcribed against one canvas and
// whose reel then changed shape, with the sidecar out of reach for the tap that follows:
// AIBin emptied and PATH narrowed to the directory holding ffmpeg, so a tap that still
// tried to re-transcribe fails for a reason this staging did not intend instead of
// quietly succeeding through something it forgot to hide. Clearing PATH entirely is not
// an option — it hides ffmpeg too, and the render then dies for an unrelated reason.
func restyledReel(t *testing.T) (Deps, *storage.Project, *bytes.Buffer, string, string) {
	t.Helper()
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	root := t.TempDir()
	media, err := testmedia.GenerateRally(root, "hall.mp4", 320, 240, 25, 14,
		[]testmedia.RallySpec{{Start: 0, End: 14, HitEvery: 1.2}})
	if err != nil {
		t.Fatal(err)
	}
	d, p := bareDeps(t)
	var log bytes.Buffer
	d.Log = slog.New(slog.NewTextHandler(&log, nil))
	if _, err := d.ImportAsset(p, media); err != nil {
		t.Fatal(err)
	}
	if err := d.AnalyzeProject(p, nil); err != nil {
		t.Fatal(err)
	}
	// Captions laid out for a horizontal reel, written by the real transcription path so
	// the stored transcript is the product's own artifact and not one this test typed out.
	if _, err := d.BuildTimeline(p, TimelineRequest{Style: "generic_highlight", Duration: 4}); err != nil {
		t.Fatal(err)
	}
	d.Cfg.Workers.AIBin = fakeTranscriptSidecar(t)
	if err := d.TranscribeProject(p, ""); err != nil {
		t.Fatal(err)
	}
	assPath, err := d.SubtitlesPath(p.ID, "ass")
	if err != nil {
		t.Fatal(err)
	}
	srtPath, err := d.SubtitlesPath(p.ID, "srt")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(assPath)
	if err != nil {
		t.Fatal(err)
	}
	if w, h, ok := subs.ReadASSFrame(bytes.NewReader(b)); !ok || w != 1920 || h != 1080 {
		t.Fatalf("the fixture's captions declare %dx%d (ok=%v), want the 1920x1080 reel they were written for",
			w, h, ok)
	}
	// The reel changes shape after the captions exist.
	if _, err := d.BuildTimeline(p, TimelineRequest{Style: DefaultExportStyle, Duration: 4}); err != nil {
		t.Fatal(err)
	}
	d.Cfg.Workers.AIBin = ""
	hideTheSidecarKeepFFmpeg(t)
	return d, p, &log, assPath, srtPath
}

// hideTheSidecarKeepFFmpeg removes every way a sidecar can resolve while leaving the
// render runnable. Clearing PATH outright would hide ffmpeg too, and the tap would then
// die for a reason the case is not about; AIBin alone is not enough either, because a
// real xcut-ai on PATH would answer and the test would pass on a machine nobody is
// shipping to.
func hideTheSidecarKeepFFmpeg(t *testing.T) {
	t.Helper()
	ffmpegPath, ferr := exec.LookPath("ffmpeg")
	if ferr != nil {
		t.Skipf("no ffmpeg to keep on PATH: %v", ferr)
	}
	ffdir := filepath.Dir(ffmpegPath)
	if _, perr := os.Stat(filepath.Join(ffdir, "ffprobe")); perr != nil {
		if _, xerr := os.Stat(filepath.Join(ffdir, "ffprobe.exe")); xerr != nil {
			t.Skipf("ffprobe does not sit beside ffmpeg (%s), so narrowing PATH would break the render", ffdir)
		}
	}
	t.Setenv("PATH", ffdir)
}

// TestExportRelaysOutStoredCaptionsWithoutTheSidecar is the cheap half of the caption
// mismatch fix. The words are already on disk where the last transcription left them, so
// the tap lays them out again for the reel's own frame instead of paying for another
// transcription — and, since the mismatch used to be unfixable without a sidecar,
// instead of passing the ill-fitted file over for a plain .srt. That the job finishes at
// all with no sidecar resolvable is part of the claim: re-transcription was not an option
// this run could have taken.
func TestExportRelaysOutStoredCaptionsWithoutTheSidecar(t *testing.T) {
	d, p, log, assPath, srtPath := restyledReel(t)
	tp, err := d.transcriptPath(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(tp)
	if err != nil {
		t.Fatalf("transcription stored no transcript to re-lay out: %v", err)
	}
	srtBefore, err := os.ReadFile(srtPath)
	if err != nil {
		t.Fatal(err)
	}

	id, steps, err := d.ExportProjectAsync(p, ExportRequest{
		Timeline: TimelineRequest{Style: DefaultExportStyle},
		Subs:     true,
		Out:      filepath.Join(t.TempDir(), "restyle.mp4"),
	})
	if err != nil {
		t.Fatal(err)
	}
	got := stepNamed(steps, "subtitles")
	if got.Action != "restyle" || !strings.HasPrefix(got.Reason, ExportRelaidSubtitles) {
		t.Fatalf("the tap planned %q / %q, want a restyle from the stored transcript", got.Action, got.Reason)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	if err := d.Queue.WaitContext(ctx); err != nil {
		t.Fatalf("the tap never finished: %v", err)
	}
	if j := awaitTerminal(t, d, id); j.Status != storage.StatusSucceeded {
		t.Fatalf("export ended %s: %s (%s)", j.Status, j.ErrorMessage, j.ErrorCode)
	}

	// The file that burns is the styled one, now sized for the reel it lands on.
	b, err := os.ReadFile(assPath)
	if err != nil {
		t.Fatal(err)
	}
	if w, h, ok := subs.ReadASSFrame(bytes.NewReader(b)); !ok || w != 1080 || h != 1920 {
		t.Errorf("captions after the restyle declare %dx%d (ok=%v), want the reel's 1080x1920", w, h, ok)
	}
	// Restyle re-lays out; it does not re-transcribe. The transcript the tap read is the
	// bytes it was, and the plain sibling it did not need is untouched too.
	if after, rerr := os.ReadFile(tp); rerr != nil || !bytes.Equal(stored, after) {
		t.Errorf("the restyle rewrote the transcript it read (rerr=%v)", rerr)
	}
	if after, rerr := os.ReadFile(srtPath); rerr != nil || !bytes.Equal(srtBefore, after) {
		t.Error("the restyle rewrote the .srt it did not use")
	}
	text := log.String()
	for _, want := range []string{"re-laid out for the reel's canvas", filepath.Base(assPath)} {
		if !strings.Contains(text, want) {
			t.Errorf("the body never put %q on the record; the log was: %s", want, text)
		}
	}
}

// TestExportWritesCaptionsFromTheStoredTranscriptWithNoSidecar is the state the tap
// used to call "no captions": the project was transcribed once, its caption files were
// removed, and nothing is configured to speak to a speechrecognizer. The words are still
// on disk, so the honest plan line is not the skip line — and the reel that comes out
// carries the sentences the sidecar heard, sized for its own frame.
func TestExportWritesCaptionsFromTheStoredTranscriptWithNoSidecar(t *testing.T) {
	d, p, log, assPath, srtPath := restyledReel(t)
	// restyledReel leaves the reel disagreeing with the captions; here they are meant to
	// agree, so rebuild at the shape the captions were laid out for and drop the files.
	if _, err := d.BuildTimeline(p, TimelineRequest{Style: "generic_highlight", Duration: 4}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{assPath, srtPath} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	if !d.HasStoredTranscript(p.ID) {
		t.Fatal("the staging left no transcript, so this case would prove nothing")
	}

	id, steps, err := d.ExportProjectAsync(p, ExportRequest{
		Timeline: TimelineRequest{Style: "generic_highlight"},
		Subs:     true,
		Out:      filepath.Join(t.TempDir(), "fromtranscript.mp4"),
	})
	if err != nil {
		t.Fatal(err)
	}
	got := stepNamed(steps, "subtitles")
	if got.Action != "create" || !strings.HasPrefix(got.Reason, ExportCaptionsFromTranscript) {
		t.Fatalf("the tap planned %q / %q, want captions written from the stored transcript",
			got.Action, got.Reason)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	if err := d.Queue.WaitContext(ctx); err != nil {
		t.Fatalf("the tap never finished: %v", err)
	}
	if j := awaitTerminal(t, d, id); j.Status != storage.StatusSucceeded {
		t.Fatalf("export ended %s: %s (%s)", j.Status, j.ErrorMessage, j.ErrorCode)
	}
	b, err := os.ReadFile(assPath)
	if err != nil {
		t.Fatalf("no styled captions were written: %v", err)
	}
	if w, h, ok := subs.ReadASSFrame(bytes.NewReader(b)); !ok || w != 1920 || h != 1080 {
		t.Errorf("the rebuilt .ass declares %dx%d (ok=%v), want the reel's 1920x1080", w, h, ok)
	}
	// The words, not just a file: a header-only rebuild would satisfy the frame check
	// above while burning an empty caption track.
	for _, want := range []string{"第一句台词", "第二句台词"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the rebuilt captions lost %q:\n%s", want, b)
		}
	}
	// What this does NOT do is bring back the .srt: the artifact a transcript rebuilds
	// is the styled one, which is also what burn prefers. Asserting the plain file was
	// rewritten would pin a promise the feature never made.
	if text := log.String(); !strings.Contains(text, "captions written from the stored transcript") {
		t.Errorf("the body never said where the captions came from; the log was: %s", text)
	}
}

// TestExportStillTranscribesWhenASidecarIsConfigured keeps the new arm from eating the
// old one. With a sidecar available the plan goes on saying it will transcribe, because a
// stored payload cannot tell the tap whether the project's asset is still the one that was
// spoken over — the question a mismatch does not ask.
func TestExportStillTranscribesWhenASidecarIsConfigured(t *testing.T) {
	root := t.TempDir()
	media, err := testmedia.GenerateRally(root, "hall.mp4", 320, 240, 25, 14,
		[]testmedia.RallySpec{{Start: 0, End: 14, HitEvery: 1.2}})
	if err != nil {
		t.Fatal(err)
	}
	d, p := bareDeps(t)
	if _, err := d.ImportAsset(p, media); err != nil {
		t.Fatal(err)
	}
	if err := d.AnalyzeProject(p, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := d.BuildTimeline(p, TimelineRequest{Style: "generic_highlight", Duration: 4}); err != nil {
		t.Fatal(err)
	}
	d.Cfg.Workers.AIBin = fakeTranscriptSidecar(t)
	if err := d.TranscribeProject(p, ""); err != nil {
		t.Fatal(err)
	}
	assPath, err := d.SubtitlesPath(p.ID, "ass")
	if err != nil {
		t.Fatal(err)
	}
	srtPath, err := d.SubtitlesPath(p.ID, "srt")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{assPath, srtPath} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}

	id, steps, err := d.ExportProjectAsync(p, ExportRequest{
		Timeline: TimelineRequest{Style: "generic_highlight"},
		Subs:     true,
		Out:      filepath.Join(root, "re.mp4"),
	})
	if err != nil {
		t.Fatal(err)
	}
	got := stepNamed(steps, "subtitles")
	if got.Action != "create" || strings.HasPrefix(got.Reason, ExportCaptionsFromTranscript) {
		t.Errorf("with a sidecar configured the plan read %q / %q, want a transcription",
			got.Action, got.Reason)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	if err := d.Queue.WaitContext(ctx); err != nil {
		t.Fatalf("the tap never finished: %v", err)
	}
	if j := awaitTerminal(t, d, id); j.Status != storage.StatusSucceeded {
		t.Fatalf("export ended %s: %s (%s)", j.Status, j.ErrorMessage, j.ErrorCode)
	}
	// The transcription really ran: the two files the test removed are back.
	if _, err := os.Stat(assPath); err != nil {
		t.Errorf("the sidecar arm planned a transcription and left no .ass: %v", err)
	}
}

// TestExportStillFallsBackWithoutAStoredTranscript is the other half of the same
// sentence: the restyle arm is keyed to the transcript file, not to captions merely being
// present. Delete it and an unfixable mismatch goes back to burning the plain .srt with
// the ill-fitted file left alone — which is what the new arm would otherwise be
// indistinguishable from.
// TestExportDoesNotPromiseARestyleItCannotPerform is the difference between a
// transcript file existing and a transcript being usable. A payload that fails
// validation cannot be laid out again, so the tap must give the mismatch the answer it
// gives with no payload at all — burn the plain .srt, say so — rather than queueing a
// restyle that dies inside the job.
func TestExportDoesNotPromiseARestyleItCannotPerform(t *testing.T) {
	d, p, _, assPath, _ := restyledReel(t)
	tp, err := d.transcriptPath(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Truncated the way a killed writer leaves it: present, non-empty, unparsable.
	if err := os.WriteFile(tp, []byte(`{"language":"zh","segments":[{"start":0.`), 0o644); err != nil {
		t.Fatal(err)
	}
	if d.HasStoredTranscript(p.ID) {
		t.Fatal("an unparsable payload read as usable")
	}

	_, steps, err := d.ExportProjectAsync(p, ExportRequest{
		Timeline: TimelineRequest{Style: DefaultExportStyle},
		Subs:     true,
		Out:      filepath.Join(t.TempDir(), "broken.mp4"),
	})
	if err != nil {
		t.Fatal(err)
	}
	got := stepNamed(steps, "subtitles")
	if got.Action == "restyle" {
		t.Fatalf("the tap promised a restyle from a transcript it cannot parse: %+v", got)
	}
	if !strings.HasPrefix(got.Reason, ExportFallbackSubtitles) {
		t.Fatalf("a broken payload planned %q / %q, want the plain-transcript answer", got.Action, got.Reason)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	if err := d.Queue.WaitContext(ctx); err != nil {
		t.Fatalf("the tap never finished: %v", err)
	}
	// The unusable payload also went untouched: refusing to read it is not the same
	// as repairing or replacing it.
	b, rerr := os.ReadFile(assPath)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if w, h, ok := subs.ReadASSFrame(bytes.NewReader(b)); !ok || w != 1920 || h != 1080 {
		t.Errorf("the ill-fitted .ass was rewritten by a tap that could not restyle it: %dx%d ok=%v", w, h, ok)
	}
}

func TestExportStillFallsBackWithoutAStoredTranscript(t *testing.T) {
	d, p, _, assPath, srtPath := restyledReel(t)
	tp, err := d.transcriptPath(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(tp); err != nil {
		t.Fatal(err)
	}

	_, steps, err := d.ExportProjectAsync(p, ExportRequest{
		Timeline: TimelineRequest{Style: DefaultExportStyle},
		Subs:     true,
		Out:      filepath.Join(t.TempDir(), "fallback.mp4"),
	})
	if err != nil {
		t.Fatal(err)
	}
	got := stepNamed(steps, "subtitles")
	if got.Action != "reuse" || !strings.HasPrefix(got.Reason, ExportFallbackSubtitles) {
		t.Fatalf("with no transcript on disk the tap planned %q / %q, want the plain-transcript answer",
			got.Action, got.Reason)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	if err := d.Queue.WaitContext(ctx); err != nil {
		t.Fatalf("the tap never finished: %v", err)
	}
	b, err := os.ReadFile(assPath)
	if err != nil {
		t.Fatal(err)
	}
	if w, h, ok := subs.ReadASSFrame(bytes.NewReader(b)); !ok || w != 1920 || h != 1080 {
		t.Errorf("with nothing to restyle from the ill-fitted file was still rewritten: %dx%d ok=%v, want the 1920x1080 it arrived as",
			w, h, ok)
	}
	if _, serr := os.Stat(srtPath); serr != nil {
		t.Errorf("the .srt the tap burned is gone: %v", serr)
	}
}
