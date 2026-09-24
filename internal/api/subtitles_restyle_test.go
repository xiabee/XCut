package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xiabee/XCut/internal/subs"
)

// TestSubtitlesStatusNamesBothFramesAndRestyleFixesThem is the caption mismatch made
// visible and fixable without a render. The export tap already restyles on its own
// second; what the panel could not do before was *say* that the styled file is sized
// for another frame, or act on it without cutting a reel.
//
// The .ass is put out of shape by the product's own writer (not by editing bytes), and
// the sidecar is then taken away entirely: if the restyle reached for a transcription
// it would fail here rather than pass on a machine that happens to have one.
func TestSubtitlesStatusNamesBothFramesAndRestyleFixesThem(t *testing.T) {
	s, pid := stagedReel(t, "restyled")

	// Before any transcription: the reel claims a frame, the captions claim nothing.
	rec, out := do(t, s, "GET", "/api/v1/projects/"+pid+"/subtitles", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status before transcribe: %d", rec.Code)
	}
	if out["styled_frame"] != "" || out["transcript"] != false || out["mismatch"] != false {
		t.Fatalf("a project with no captions reported %+v, want no styled frame and no transcript", out)
	}
	if out["reel_frame"] != "1080x1920" {
		t.Fatalf("the staged reel is %v, not the 1080x1920 this test restyles back to", out["reel_frame"])
	}

	fakeSidecarScript(t, s, false)
	rec, out = do(t, s, "POST", "/api/v1/projects/"+pid+"/subtitles", "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("transcribe trigger: %d %v", rec.Code, out)
	}
	awaitJob(t, s, out["job_id"].(string))

	rec, out = do(t, s, "GET", "/api/v1/projects/"+pid+"/subtitles", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status after transcribe: %d", rec.Code)
	}
	if out["styled_frame"] != "1080x1920" || out["mismatch"] != false || out["transcript"] != true {
		t.Fatalf("fresh captions read %v / %v / %v, want them agreeing with the reel and a transcript on disk",
			out["styled_frame"], out["mismatch"], out["transcript"])
	}

	// Now the same words laid out for a horizontal frame, which is what a project
	// that switched style is left holding.
	tr, err := subs.Parse([]byte(`{"language":"zh","segments":[{"start":0.5,"end":1.5,"text":"你好"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var wrong strings.Builder
	if err := subs.WriteCaptionASS(tr, subs.KaraokeStyle{Width: 1920, Height: 1080}, &wrong); err != nil {
		t.Fatal(err)
	}
	assPath, err := s.Pipe.SubtitlesPath(pid, "ass")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(assPath, []byte(wrong.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	rec, out = do(t, s, "GET", "/api/v1/projects/"+pid+"/subtitles", "")
	if out["mismatch"] != true || out["styled_frame"] != "1920x1080" {
		t.Fatalf("a .ass sized for another frame read %v / %v, want mismatch true and 1920x1080",
			out["styled_frame"], out["mismatch"])
	}

	s.Pipe.Cfg.Workers.AIBin = "" // no sidecar resolves from here on
	rec, out = do(t, s, "POST", "/api/v1/projects/"+pid+"/subtitles/restyle", "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("restyle with no sidecar: %d %v", rec.Code, out)
	}
	awaitJob(t, s, out["job_id"].(string))
	rec, out = do(t, s, "GET", "/api/v1/projects/"+pid+"/subtitles", "")
	if out["styled_frame"] != "1080x1920" || out["mismatch"] != false {
		t.Fatalf("after the queued restyle the panel reads %v / %v, want the reel's own frame and no mismatch",
			out["styled_frame"], out["mismatch"])
	}
	// The body of the file, not just the report: a status that agreed with itself and
	// a caption file that did not would be the same bug wearing a better message.
	rec, _ = do(t, s, "GET", "/api/v1/projects/"+pid+"/subtitles/file?format=ass", "")
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "PlayResX: 1080") ||
		!strings.Contains(body, "PlayResY: 1920") {
		t.Fatalf("the restyled file is not what the endpoint reported (%d): %s", rec.Code, body)
	}
	if !strings.Contains(body, "你好") {
		t.Errorf("the restyle lost the words it re-laid out: %s", body)
	}
}

// TestRestyleRefusesWithoutATranscript is the endpoint's own negative: with no words on
// disk there is nothing to lay out, and the answer has to name what is missing rather
// than 200 its way to an unchanged file. A project that was never transcribed is the
// same path as one whose transcript was deleted, and it is the one a user actually
// reaches.
func TestRestyleRefusesWithoutATranscript(t *testing.T) {
	s, pid := stagedReel(t, "norestyle")
	rec, out := do(t, s, "POST", "/api/v1/projects/"+pid+"/subtitles/restyle", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("a restyle with no transcript answered %d %v, want 404", rec.Code, out)
	}
	if out["error"] != "not_found" {
		t.Errorf("code %v, want not_found", out["error"])
	}
	msg, _ := out["message"].(string)
	if !strings.Contains(strings.ToLower(msg), "transcript") {
		t.Errorf("the refusal says %q, want it to name the missing transcript", msg)
	}
	// And it refused without producing the thing it could not lay out.
	assPath, err := s.Pipe.SubtitlesPath(pid, "ass")
	if err != nil {
		t.Fatal(err)
	}
	if _, serr := os.Stat(assPath); serr == nil {
		t.Error("a refused restyle still wrote an .ass")
	}
}

// blockingSidecar points the server at a fake transcriber that starts an analyze and
// then waits for a sentinel file, so a test can hold the exclusive subtitles slot with a
// job that is *known to be running* rather than one it hopes has not finished yet. The
// returned path is the sentinel: create it and the job completes.
func blockingSidecar(t *testing.T, s *Server) string {
	t.Helper()
	if pythonBin(t) == "" {
		t.Skip("python not available")
	}
	sentinel := filepath.Join(t.TempDir(), "release")
	script := `#!/usr/bin/env python3
import json, os, sys, time
req = json.loads(sys.stdin.read() or "{}")
op = req.get("op")
gate = os.environ.get("XCUT_FAKE_GATE")
if op == "capabilities":
    result = {"ops": [{"op": "analyze"}], "models": [
        {"name": "transcript", "available": True, "loaded": False, "detail": "fake"}]}
elif op == "analyze":
    if gate:
        for _ in range(600):
            if os.path.exists(gate):
                break
            time.sleep(0.05)
    result = {"language": "zh", "segments": [{"start": 0.5, "end": 1.5, "text": "held"}]}
else:
    result = {}
sys.stdout.write(json.dumps({"protocol": 1, "ok": True, "op": op, "result": result}))
`
	path := filepath.Join(t.TempDir(), "blocking-ai.py")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XCUT_FAKE_GATE", sentinel)
	s.Pipe.Cfg.Workers.AIBin = path
	return sentinel
}

// TestRestyleWaitsBehindATranscription is the reason the re-lay is a queued job rather
// than a write in the request. Both end up writing the same subtitles.ass, and a
// transcription additionally removes the styled file when its own transcript turns out to
// have nothing to lay out — so a re-lay that ran beside one could be deleted by the very
// job it was racing, and the panel would have said "re-laid out" over a missing file.
// `subtitles` is exclusive per project; joining that type is what makes the two wait.
func TestRestyleWaitsBehindATranscription(t *testing.T) {
	s, pid := stagedReel(t, "restylex")

	// A completed transcription first: it puts a transcript on disk, which is what the
	// endpoint's own pre-check asks for.
	fakeSidecarScript(t, s, false)
	_, out := do(t, s, "POST", "/api/v1/projects/"+pid+"/subtitles", "")
	awaitJob(t, s, out["job_id"].(string))

	// Then hold the exclusive slot with a transcription that is known to be running —
	// waiting for that state rather than bounding how long it takes, because on a
	// loaded machine a queue is slow, not wrong.
	release := blockingSidecar(t, s)
	_, out = do(t, s, "POST", "/api/v1/projects/"+pid+"/subtitles", "")
	running := out["job_id"].(string)
	waitForJobState(t, s, running, "running", 60*time.Second)

	rec, out := do(t, s, "POST", "/api/v1/projects/"+pid+"/subtitles/restyle", "")
	if rec.Code != http.StatusConflict {
		t.Errorf("a re-lay beside a running transcription was accepted (%d %v), want 409", rec.Code, out)
	}
	if err := os.WriteFile(release, []byte("go"), 0o644); err != nil {
		t.Fatal(err)
	}
	awaitJob(t, s, running)

	// And with the slot free, the same request is accepted: the refusal above was the
	// queue holding the door, not the endpoint being broken.
	rec, out = do(t, s, "POST", "/api/v1/projects/"+pid+"/subtitles/restyle", "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("restyle once the slot was free: %d %v", rec.Code, out)
	}
	awaitJob(t, s, out["job_id"].(string))
}

// waitForJobState blocks until a job reports the state it names, and fails the test if a
// *terminal* state arrives first — a fixture that finished before the assertion ran would
// otherwise pass by accident.
func waitForJobState(t *testing.T, s *Server, jobID, want string, wait time.Duration) {
	t.Helper()
	deadline := time.Now().Add(wait)
	for {
		rec, out := do(t, s, "GET", "/api/v1/jobs/"+jobID, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("job get: %d", rec.Code)
		}
		switch st := out["job"].(map[string]any)["status"].(string); st {
		case want:
			return
		case "succeeded", "failed":
			if want != st {
				t.Fatalf("job ended %s before the test could observe it as %s", st, want)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("job never reached %s", want)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestSubtitlesStatusNamesStaleMedia: the panel's sentence is chosen from this key, so it
// is asserted on the wire — the raw JSON field, written by repointing the stored envelope at
// an asset the project does not have. Reading a struct field instead would let a renamed tag
// pass while the browser saw nothing.
func TestSubtitlesStatusNamesStaleMedia(t *testing.T) {
	s, pid := stagedReel(t, "stalemedia")
	fakeSidecarScript(t, s, false)
	_, out := do(t, s, "POST", "/api/v1/projects/"+pid+"/subtitles", "")
	awaitJob(t, s, out["job_id"].(string))

	rec, out := do(t, s, "GET", "/api/v1/projects/"+pid+"/subtitles", "")
	if rec.Code != http.StatusOK || out["media_stale"] != false || out["transcript"] != true {
		t.Fatalf("fresh captions read %v / %v, want a usable transcript and nothing stale",
			out["transcript"], out["media_stale"])
	}

	// The documented on-disk location, spelled out here rather than borrowed from an
	// accessor: the file name is part of what OPERATIONS promises, and this is the case
	// that would notice it move.
	tp := filepath.Join(s.Pipe.WS.Root, "projects", pid, "transcript.json")
	raw, err := os.ReadFile(tp)
	if err != nil {
		t.Fatal(err)
	}
	var env map[string]any
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if env["asset_id"] == nil || env["asset_id"] == "" {
		t.Fatalf("the stored envelope carries no asset_id to repoint: %s", raw)
	}
	env["asset_id"] = "asst_a_clip_that_is_gone"
	gone, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tp, gone, 0o644); err != nil {
		t.Fatal(err)
	}

	rec, out = do(t, s, "GET", "/api/v1/projects/"+pid+"/subtitles", "")
	if out["media_stale"] != true {
		t.Errorf("captions from another clip read media_stale %v, want true", out["media_stale"])
	}
	// The two facts stay two facts: the frame still fits, so `mismatch` must not
	// flip, and the payload is still on disk while no longer being usable here.
	if out["mismatch"] != false {
		t.Errorf("a media change was reported as a frame mismatch: %v", out["mismatch"])
	}
	if out["transcript"] != false {
		t.Errorf("a transcript bound to other media still read as re-layable: %v", out["transcript"])
	}
	// And the endpoint refuses the re-lay it can no longer vouch for.
	if rec, _ := do(t, s, "POST", "/api/v1/projects/"+pid+"/subtitles/restyle", ""); rec.Code != http.StatusNotFound {
		t.Errorf("a re-lay of another clip's captions answered %d, want 404", rec.Code)
	}
}
