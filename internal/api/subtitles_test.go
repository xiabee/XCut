package api

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xiabee/XCut/internal/timeline"
)

// fakeSidecarScript writes a canned transcript sidecar and points the
// server's AI bin at it. With wordTimings the segment carries one word per
// syllable and both subtitle artifacts appear; without it the sidecar knows
// only where each line falls, which is what most of them return.
func fakeSidecarScript(t *testing.T, s *Server, wordTimings bool) {
	t.Helper()
	if pythonBin(t) == "" {
		t.Skip("python not available")
	}
	segment := `{"start": 0.5, "end": 1.5, "text": "你好"}`
	if wordTimings {
		segment = `{"start": 0.5, "end": 1.5, "text": "你好", "words": [
            {"start": 0.5, "end": 1.0, "word": "你"},
            {"start": 1.0, "end": 1.5, "word": "好"}]}`
	}
	script := strings.Replace(transcriptSidecarTemplate, "__SEGMENT__", segment, 1)
	path := filepath.Join(t.TempDir(), "fake-ai.py")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	s.Pipe.Cfg.Workers.AIBin = path
}

const transcriptSidecarTemplate = `#!/usr/bin/env python3
import json, sys
req = json.loads(sys.stdin.read() or "{}")
op = req.get("op")
if op == "capabilities":
    result = {"ops": [{"op": "analyze"}], "models": [
        {"name": "transcript", "available": True, "loaded": False, "detail": "fake"}]}
elif op == "analyze":
    result = {"language": "zh", "segments": [__SEGMENT__]}
else:
    result = {}
sys.stdout.write(json.dumps({"protocol": 1, "ok": True, "op": op, "result": result}))
`

// verticalReel is a one-clip 9:16 timeline: the canvas the caption style has
// to be laid out against.
func verticalReel(assetID string) *timeline.Timeline {
	return &timeline.Timeline{
		Version: timeline.Version,
		Canvas:  timeline.Canvas{Width: 1080, Height: 1920, FPS: 30},
		Tracks: []timeline.Track{{ID: "v1", Kind: "video", Clips: []timeline.Clip{{
			ID: "c1", AssetID: assetID, SourceStart: 0, SourceEnd: 3,
			TimelineStart: 0, Speed: 1, Volume: 1,
		}}}},
	}
}

// awaitJob blocks out loud: a transcription that failed is reported with its
// error, one that never finished with how long it was given. 30s because on
// AV-scanned machines a cold python sidecar start can be slow — a remote CI
// node flaked once at 15s, and the immediate re-run passed.
func awaitJob(t *testing.T, s *Server, jobID string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		rec, out := do(t, s, "GET", "/api/v1/jobs/"+jobID, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("job get: %d", rec.Code)
		}
		job := out["job"].(map[string]any)
		st := job["status"].(string)
		if st == "succeeded" || st == "failed" {
			if st != "succeeded" {
				t.Fatalf("transcription failed: %v", out)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("transcription never finished (still %s)", st)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func pythonBin(t *testing.T) string {
	t.Helper()
	for _, py := range []string{"python", "python3"} {
		if p, err := exec.LookPath(py); err == nil && p != "" {
			return p
		}
	}
	return ""
}

// stagedReel is the state a caption is generated in: a project with one asset
// and a 9:16 timeline PUT before transcription. The canvas matters — the style
// is laid out against it — so a project with no timeline gets the writer's
// shipped 1280×720 reference instead.
func stagedReel(t *testing.T, name string) (*Server, string) {
	t.Helper()
	s := testServer(t)
	if _, err := s.DB.CreateProject(t.Context(), name); err != nil {
		t.Fatal(err)
	}
	p, err := s.DB.GetProjectByName(t.Context(), name)
	if err != nil {
		t.Fatal(err)
	}
	asset := storageAssetFor(p.ID)
	if err := s.DB.UpsertAsset(t.Context(), &asset); err != nil {
		t.Fatal(err)
	}
	assets, err := s.DB.ListAssets(t.Context(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rec, out := do(t, s, "PUT", "/api/v1/projects/"+p.ID+"/timeline",
		marshalTimeline(t, verticalReel(assets[0].ID))); rec.Code != http.StatusOK {
		t.Fatalf("vertical timeline rejected: %d %v", rec.Code, out)
	}
	return s, p.ID
}

// TestSubtitlesFlow: transcribe → status flips true → file downloads with
// the canned content, and the render endpoint accepts subs=true (the burn
// itself is covered by the render tests).
func TestSubtitlesFlow(t *testing.T) {
	s, pid := stagedReel(t, "subbed")

	// Status before any transcription: both false, not an error.
	rec, out := do(t, s, "GET", "/api/v1/projects/"+pid+"/subtitles", "")
	if rec.Code != http.StatusOK || out["srt"] != false || out["ass"] != false {
		t.Fatalf("initial status: %d %v", rec.Code, out)
	}

	fakeSidecarScript(t, s, true)

	// File download before transcription → 404.
	if rec, _ := do(t, s, "GET", "/api/v1/projects/"+pid+"/subtitles/file?format=srt", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("file before transcribe: %d, want 404", rec.Code)
	}

	// Trigger transcription; a duplicate trigger while the first is queued
	// must 409 (subtitles joined the exclusive job set — concurrent runs
	// would pair one run's .srt with another's .ass).
	rec, out = do(t, s, "POST", "/api/v1/projects/"+pid+"/subtitles", "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("transcribe trigger: %d %v", rec.Code, out)
	}
	jobID := out["job_id"].(string)
	recDup, outDup := do(t, s, "POST", "/api/v1/projects/"+pid+"/subtitles", "")
	if recDup.Code != http.StatusConflict {
		t.Fatalf("duplicate transcribe: %d %v (want 409)", recDup.Code, outDup)
	}
	awaitJob(t, s, jobID)

	rec, out = do(t, s, "GET", "/api/v1/projects/"+pid+"/subtitles", "")
	if rec.Code != http.StatusOK || out["srt"] != true || out["ass"] != true {
		t.Fatalf("status after transcribe: %d %v", rec.Code, out)
	}
	// The panel's ready sentence is chosen from this key, so a word-timed sidecar
	// has to arrive as karaoke or the UI says "styled" over a file that sweeps.
	if out["karaoke"] != true {
		t.Errorf("a word-timed transcription reported karaoke %v, want true", out["karaoke"])
	}
	rec, _ = do(t, s, "GET", "/api/v1/projects/"+pid+"/subtitles/file?format=srt", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "00:00:00,500 --> 00:00:01,500") {
		t.Fatalf("srt download: %d %s", rec.Code, rec.Body.String())
	}
	rec, _ = do(t, s, "GET", "/api/v1/projects/"+pid+"/subtitles/file?format=ass", "")
	assBody := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(assBody, `{\kf50}你`) {
		t.Fatalf("ass download: %d %s", rec.Code, assBody)
	}
	// The style has to be laid out for *this* reel. libass scales a script by its
	// declared PlayRes, so a file that says 1280×720 puts the caption at the size and
	// height of a horizontal frame no matter what is playing behind it.
	if !strings.Contains(assBody, "PlayResX: 1080") || !strings.Contains(assBody, "PlayResY: 1920") {
		t.Fatalf("the .ass was styled against a frame the project does not have (head: %.120q)", assBody)
	}

	// Render with subs=true now resolves the artifact (job queued → 202),
	// then cancel it so no ffmpeg work actually runs.
	rec, out = do(t, s, "POST", "/api/v1/projects/"+pid+"/render", `{"subs": true}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("render with subs: %d %v", rec.Code, out)
	}
	renderJob := out["job_id"].(string)
	do(t, s, "POST", "/api/v1/jobs/"+renderJob+"/cancel", "")
}

// TestPlainTranscriptGetsStyledCaptions: which artifact a burn draws used to
// turn on a sidecar detail nobody chose. The same words with per-syllable
// timings got a designed caption frame; without them they got an SRT and
// whatever libass defaults to. Both now get the same frame and the same canvas.
func TestPlainTranscriptGetsStyledCaptions(t *testing.T) {
	s, pid := stagedReel(t, "plainsubs")
	fakeSidecarScript(t, s, false)
	rec, out := do(t, s, "POST", "/api/v1/projects/"+pid+"/subtitles", "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("transcribe trigger: %d %v", rec.Code, out)
	}
	awaitJob(t, s, out["job_id"].(string))

	rec, out = do(t, s, "GET", "/api/v1/projects/"+pid+"/subtitles", "")
	if rec.Code != http.StatusOK || out["srt"] != true || out["ass"] != true {
		t.Fatalf("status after transcribe: %d %v", rec.Code, out)
	}
	if out["karaoke"] != false {
		t.Errorf("a transcript with no word timings reported karaoke %v, want false", out["karaoke"])
	}
	rec, _ = do(t, s, "GET", "/api/v1/projects/"+pid+"/subtitles/file?format=ass", "")
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("ass download: %d %s", rec.Code, body)
	}
	for _, want := range []string{"Style: Caption,", "PlayResX: 1080", "PlayResY: 1920", "你好"} {
		if !strings.Contains(body, want) {
			t.Errorf("the plain transcript's .ass lacks %q:\n%s", want, body)
		}
	}
	// Karaoke sweeps without word timings would tell libass to fill in text that
	// was never timed: the highlight would sit wherever the clock says, not where
	// the syllable is.
	if strings.Contains(body, `\k`) {
		t.Errorf("a transcript with no word timings produced karaoke tags:\n%s", body)
	}
}

// TestReTranscribeReplacesTheKaraokeFile: a project transcribed twice, the
// second time by a sidecar that does not time words. The karaoke file it
// replaces is the one burn-in prefers, so leaving it behind — or removing it
// and offering nothing in its place — both publish the wrong caption.
func TestReTranscribeReplacesTheKaraokeFile(t *testing.T) {
	s, pid := stagedReel(t, "twice")
	fakeSidecarScript(t, s, true)
	rec, out := do(t, s, "POST", "/api/v1/projects/"+pid+"/subtitles", "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("karaoke transcribe: %d %v", rec.Code, out)
	}
	awaitJob(t, s, out["job_id"].(string))
	if rec, _ := do(t, s, "GET", "/api/v1/projects/"+pid+"/subtitles/file?format=ass", ""); !strings.Contains(rec.Body.String(), `{\kf`) {
		t.Fatal("the first pass was supposed to be karaoke")
	}

	fakeSidecarScript(t, s, false)
	rec, out = do(t, s, "POST", "/api/v1/projects/"+pid+"/subtitles", "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("plain transcribe: %d %v", rec.Code, out)
	}
	awaitJob(t, s, out["job_id"].(string))

	rec, _ = do(t, s, "GET", "/api/v1/projects/"+pid+"/subtitles/file?format=ass", "")
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("the second pass left no .ass at all (%d): burn would fall back to the .srt and lose the styling", rec.Code)
	}
	if !strings.Contains(body, "Style: Caption,") {
		t.Errorf("the stale karaoke file survived the second transcript:\n%s", body)
	}
	if strings.Contains(body, `\k`) {
		t.Errorf("karaoke sweeps from the first pass are still in the file:\n%s", body)
	}
}

// TestRenderSubsRequiresTranscription: subs=true without artifacts is a
// clean 404 with a hint, not a failed render job.
func TestRenderSubsRequiresTranscription(t *testing.T) {
	s := testServer(t)
	if _, err := s.DB.CreateProject(t.Context(), "nosubs"); err != nil {
		t.Fatal(err)
	}
	p, _ := s.DB.GetProjectByName(t.Context(), "nosubs")
	rec, out := do(t, s, "POST", "/api/v1/projects/"+p.ID+"/render", `{"subs": true}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("render with subs before transcribe: %d %v (want 404)", rec.Code, out)
	}
}
