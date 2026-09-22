package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The export tap answers with a plan before any of it runs: which stages will
// happen, which are already done, and which will not happen at all. The last one
// is the reason this file exists — a reel that silently lost its captions is the
// failure mode the tap was built to avoid, so a skip has to be a stated answer
// and not an absence.

func stepOf(t *testing.T, out map[string]any, name string) map[string]any {
	t.Helper()
	raw, ok := out["steps"].([]any)
	if !ok {
		t.Fatalf("response carries no steps array: %v", out)
	}
	for _, e := range raw {
		s, ok := e.(map[string]any)
		if !ok {
			t.Fatalf("a step is not an object: %v", e)
		}
		if s["step"] == name {
			return s
		}
	}
	t.Fatalf("no %q step among %v", name, raw)
	return nil
}

func newProject(t *testing.T, s *Server, name string) string {
	t.Helper()
	if _, err := s.DB.CreateProject(t.Context(), name); err != nil {
		t.Fatal(err)
	}
	p, err := s.DB.GetProjectByName(t.Context(), name)
	if err != nil {
		t.Fatal(err)
	}
	return p.ID
}

func TestExportPlanIsOnTheWireWithItsNames(t *testing.T) {
	s := testServer(t)
	pid := newProject(t, s, "export-plan")
	rec, out := do(t, s, "POST", "/api/v1/projects/"+pid+"/export", "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("export: %d %v", rec.Code, out)
	}
	if out["job_id"] == nil || out["queued"] != true {
		t.Fatalf("the accepted envelope lost its job fields: %v", out)
	}
	var got []string
	for _, e := range out["steps"].([]any) {
		s := e.(map[string]any)
		got = append(got, fmt.Sprint(s["step"]))
		if s["action"] == nil {
			t.Errorf("step %v carries no action", s["step"])
		}
	}
	// The three stages, in the order the tap runs them.
	if strings.Join(got, ",") != "timeline,subtitles,render" {
		t.Errorf("steps = %v, want timeline,subtitles,render", got)
	}
	if stepOf(t, out, "timeline")["action"] != "create" {
		t.Errorf("a project with no reel was told it can reuse one: %v", out)
	}
	if stepOf(t, out, "render")["action"] != "create" {
		t.Errorf("the render is the one stage that always happens: %v", out)
	}
	do(t, s, "POST", "/api/v1/jobs/"+out["job_id"].(string)+"/cancel", "")
}

func TestExportReusesWhatTheProjectAlreadyHas(t *testing.T) {
	s, pid := stagedReel(t, "export-reuse")
	rec, out := do(t, s, "POST", "/api/v1/projects/"+pid+"/export", `{"subs": false}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("export: %d %v", rec.Code, out)
	}
	if got := stepOf(t, out, "timeline")["action"]; got != "reuse" {
		t.Errorf("timeline step = %v, want reuse (the project has a saved document)", got)
	}
	st := stepOf(t, out, "subtitles")
	if st["action"] != "skip" || st["reason"] != "not asked for" {
		t.Errorf("subs=false must skip out loud, got %v / %v", st["action"], st["reason"])
	}
	do(t, s, "POST", "/api/v1/jobs/"+out["job_id"].(string)+"/cancel", "")
}

// TestExportNamesASidecarThatIsNotThere: nothing on PATH can transcribe, so the
// tap cannot caption the reel. It exports anyway — and says why the captions are
// missing, in the field that would otherwise have promised them.
func TestExportNamesASidecarThatIsNotThere(t *testing.T) {
	s, pid := stagedReel(t, "export-nosidecar")
	t.Setenv("PATH", t.TempDir()) // no xcut-ai, no sidecar script: nothing to resolve
	s.Pipe.Cfg.Workers.AIBin = ""
	rec, out := do(t, s, "POST", "/api/v1/projects/"+pid+"/export", "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("export without a sidecar: %d %v", rec.Code, out)
	}
	st := stepOf(t, out, "subtitles")
	if st["action"] != "skip" {
		t.Fatalf("a missing sidecar produced %q, want skip", st["action"])
	}
	if !strings.Contains(fmt.Sprint(st["reason"]), "sidecar") {
		t.Errorf("the skip reason does not name what to install: %v", st["reason"])
	}
	do(t, s, "POST", "/api/v1/jobs/"+out["job_id"].(string)+"/cancel", "")
}

// TestExportIsExclusive: a second tap while the first is mid-transcript is a
// duplicate, not a second reel. The sidecar sleeps so the first export is
// genuinely still running when the second request arrives — a check that races
// past a finished job would prove nothing.
func TestExportIsExclusive(t *testing.T) {
	s, pid := stagedReel(t, "export-exclusive")
	s.Pipe.Cfg.Workers.AIBin = slowSidecar(t, 4)
	rec, out := do(t, s, "POST", "/api/v1/projects/"+pid+"/export", "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("first export: %d %v", rec.Code, out)
	}
	first := out["job_id"].(string)
	t.Cleanup(func() { s.Pipe.Queue.Cancel(first) })
	deadline := time.Now().Add(8 * time.Second)
	var running bool
	for time.Now().Before(deadline) {
		_, j := do(t, s, "GET", "/api/v1/jobs/"+first, "")
		if j["job"].(map[string]any)["status"] == "running" {
			running = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !running {
		t.Fatal("the first export never reached running, so the duplicate check proves nothing")
	}
	recDup, outDup := do(t, s, "POST", "/api/v1/projects/"+pid+"/export", "")
	if recDup.Code != http.StatusConflict {
		t.Fatalf("second export while the first runs: %d %v, want 409", recDup.Code, outDup)
	}
	if outDup["error"] != "conflict" || !strings.Contains(fmt.Sprint(outDup["message"]), "export") {
		t.Errorf("the conflict should name the job type it refused: %v", outDup)
	}
	// The plan said this project's captions were worth making; the second tap must
	// not have quietly rewritten that answer.
	if stepOf(t, out, "subtitles")["action"] != "create" {
		t.Errorf("with a sidecar configured the tap should plan to transcribe: %v", out)
	}
}

// slowSidecar writes a transcript sidecar that answers, but slowly enough for a
// test to catch the work in progress.
func slowSidecar(t *testing.T, seconds int) string {
	t.Helper()
	if pythonBin(t) == "" {
		t.Skip("python not available")
	}
	script := strings.Replace(transcriptSidecarTemplate, "import json, sys", "import json, sys, time", 1)
	script = strings.Replace(script, "elif op == \"analyze\":",
		fmt.Sprintf("elif op == \"analyze\":\n    time.sleep(%d)", seconds), 1)
	if !strings.Contains(script, "time.sleep") || !strings.Contains(script, "import json, sys, time") {
		t.Fatal("the slow sidecar template was patched into something else")
	}
	path := filepath.Join(t.TempDir(), "slow-ai.py")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestTheClientPostsTheTapWhereTheServerAnswers: the button and the route are one
// agreement written in two languages, and nothing in the toolchain reads both. If
// either side is renamed, the UI would post to a path only this file's tests know
// how to reach — so the literal the client uses is pinned here, next to the tests
// that prove that exact path answers.
func TestTheClientPostsTheTapWhereTheServerAnswers(t *testing.T) {
	_, js, _ := i18nAssets(t)
	// The exact literal app.js builds its request from, with only the project id
	// interpolated.
	const clientPath = "/api/v1/projects/${currentProject.id}/export"
	if n := strings.Count(js, clientPath); n != 1 {
		t.Fatalf("app.js posts to the export route %d times, want exactly 1", n)
	}
	s := testServer(t)
	pid := newProject(t, s, "export-wire")
	rec, out := do(t, s, "POST", "/api/v1/projects/"+pid+"/export", "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("the path the client posts to answers %d %v, want 202", rec.Code, out)
	}
	do(t, s, "POST", "/api/v1/jobs/"+out["job_id"].(string)+"/cancel", "")
}
