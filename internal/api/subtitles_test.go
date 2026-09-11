package api

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeSidecarScript writes a canned transcript sidecar and points the
// server's AI bin at it. The transcript has word timings so both subtitle
// artifacts appear.
func fakeSidecarScript(t *testing.T, s *Server) {
	t.Helper()
	if pythonBin(t) == "" {
		t.Skip("python not available")
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
        {"start": 0.5, "end": 1.5, "text": "你好", "words": [
            {"start": 0.5, "end": 1.0, "word": "你"},
            {"start": 1.0, "end": 1.5, "word": "好"}]}]}
else:
    result = {}
sys.stdout.write(json.dumps({"protocol": 1, "ok": True, "op": op, "result": result}))
`
	path := filepath.Join(t.TempDir(), "fake-ai.py")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	s.Pipe.Cfg.Workers.AIBin = path
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

// TestSubtitlesFlow: transcribe → status flips true → file downloads with
// the canned content, and the render endpoint accepts subs=true (the burn
// itself is covered by the render tests).
func TestSubtitlesFlow(t *testing.T) {
	s := testServer(t)
	if _, err := s.DB.CreateProject(t.Context(), "subbed"); err != nil {
		t.Fatal(err)
	}
	p, _ := s.DB.GetProjectByName(t.Context(), "subbed")
	asset := storageAssetFor(p.ID)
	if err := s.DB.UpsertAsset(t.Context(), &asset); err != nil {
		t.Fatal(err)
	}

	// Status before any transcription: both false, not an error.
	rec, out := do(t, s, "GET", "/api/v1/projects/"+p.ID+"/subtitles", "")
	if rec.Code != http.StatusOK || out["srt"] != false || out["ass"] != false {
		t.Fatalf("initial status: %d %v", rec.Code, out)
	}

	fakeSidecarScript(t, s)

	// File download before transcription → 404.
	if rec, _ := do(t, s, "GET", "/api/v1/projects/"+p.ID+"/subtitles/file?format=srt", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("file before transcribe: %d, want 404", rec.Code)
	}

	// Trigger transcription and wait for the job to finish.
	rec, out = do(t, s, "POST", "/api/v1/projects/"+p.ID+"/subtitles", "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("transcribe trigger: %d %v", rec.Code, out)
	}
	jobID := out["job_id"].(string)
	deadline := time.Now().Add(15 * time.Second)
	for {
		rec, out = do(t, s, "GET", "/api/v1/jobs/"+jobID, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("job get: %d", rec.Code)
		}
		job := out["job"].(map[string]any)
		st := job["status"].(string)
		if st == "succeeded" || st == "failed" {
			if st != "succeeded" {
				t.Fatalf("transcription failed: %v", out)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("transcription never finished (still %s)", st)
		}
		time.Sleep(20 * time.Millisecond)
	}

	rec, out = do(t, s, "GET", "/api/v1/projects/"+p.ID+"/subtitles", "")
	if rec.Code != http.StatusOK || out["srt"] != true || out["ass"] != true {
		t.Fatalf("status after transcribe: %d %v", rec.Code, out)
	}
	rec, _ = do(t, s, "GET", "/api/v1/projects/"+p.ID+"/subtitles/file?format=srt", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "00:00:00,500 --> 00:00:01,500") {
		t.Fatalf("srt download: %d %s", rec.Code, rec.Body.String())
	}
	rec, _ = do(t, s, "GET", "/api/v1/projects/"+p.ID+"/subtitles/file?format=ass", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `{\kf50}你`) {
		t.Fatalf("ass download: %d %s", rec.Code, rec.Body.String())
	}

	// Render with subs=true now resolves the artifact (job queued → 202),
	// then cancel it so no ffmpeg work actually runs.
	rec, out = do(t, s, "POST", "/api/v1/projects/"+p.ID+"/render", `{"subs": true}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("render with subs: %d %v", rec.Code, out)
	}
	renderJob := out["job_id"].(string)
	do(t, s, "POST", "/api/v1/jobs/"+renderJob+"/cancel", "")
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
