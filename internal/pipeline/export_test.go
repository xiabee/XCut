package pipeline

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xiabee/XCut/internal/job"
	"github.com/xiabee/XCut/internal/storage"
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
