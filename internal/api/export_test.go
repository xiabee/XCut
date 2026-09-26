package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xiabee/XCut/internal/storage"
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

// slowSidecar points the caller at this test binary in slow-sidecar mode: it
// answers capabilities at once and holds each analyze for the named seconds,
// so a test can catch the work in progress. The returned path is the test
// binary itself; the hold is set by environment (TestMain reads it).
func slowSidecar(t *testing.T, seconds int) string {
	t.Helper()
	t.Setenv("XCUT_TEST_SIDECAR", "slow")
	t.Setenv("XCUT_TEST_SIDECAR_SLEEP", strconv.Itoa(seconds))
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return bin
}

// TestExportQueuesTheChildRenderAsTheNewestRow: the client follows the tap's
// child render by reading the FIRST render-typed row from the project's jobs
// list — newest-first is the endpoint's order (created_at DESC, id DESC), so
// an older reel's job row must never be mistaken for the row this tap queued.
func TestExportQueuesTheChildRenderAsTheNewestRow(t *testing.T) {
	s, pid := stagedReel(t, "export-follow")
	ctx := context.Background()
	// Yesterday's terminal render row: a reel the tap did not queue. It is
	// terminal on purpose — an active one would 409 the tap's own child.
	old, err := s.DB.CreateJob(ctx, "render", pid, "CPU_HEAVY", `{"out":"old.mp4"}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DB.FinishJob(ctx, old.ID, "failed", "render_failure", "superseded by the test's fixture"); err != nil {
		t.Fatal(err)
	}
	// created_at carries second resolution: without a full second between the
	// seeded row and the tap's child, the id tie-break orders them
	// arbitrarily and "newest first" means nothing.
	time.Sleep(1100 * time.Millisecond)

	rec, out := do(t, s, "POST", "/api/v1/projects/"+pid+"/export", "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("export: %d %v", rec.Code, out)
	}
	awaitJob(t, s, out["job_id"].(string))

	jobs, err := s.DB.ListJobs(ctx, pid)
	if err != nil {
		t.Fatal(err)
	}
	var firstRender *storage.Job
	oldIndex, childIndex := -1, -1
	for i := range jobs {
		if jobs[i].Type != "render" {
			continue
		}
		if firstRender == nil {
			firstRender = &jobs[i]
		}
		if jobs[i].ID == old.ID {
			oldIndex = i
		} else {
			childIndex = i
		}
	}
	if firstRender == nil {
		t.Fatal("no render row exists after the tap — it queued none")
	}
	if childIndex < 0 {
		t.Fatal("the tap's child render row is missing")
	}
	if oldIndex >= 0 && childIndex > oldIndex {
		t.Fatal("the seeded old render row lists ahead of the tap's child — newest-first among renders is the client's load-bearing assumption")
	}
}

// TestTheExportWatcherFollowsTheChildRender: the client's one tap must end at
// the reel the tap produced — the watcher follows the child render row, and
// the stale-player shortcut is gone from the export path (it pointed the
// player at the pre-export reel, or a 404, minutes before the real reel
// existed). The render button keeps its shortcut: the reel it names arrives
// in seconds and its own watcher corrects the player on completion.
func TestTheExportWatcherFollowsTheChildRender(t *testing.T) {
	_, js, _ := i18nAssets(t)
	if n := strings.Count(js, `type === "export"`); n != 1 {
		t.Fatalf("app.js switches on the export terminal state %d times, want exactly 1", n)
	}
	if !strings.Contains(js, "followExportRender(pid)") || !strings.Contains(js, `jobs.find((j) => j.type === "render" && !`) {
		t.Fatal("the export watcher must follow an in-flight render row (the tap's child), newest-first as fallback")
	}
	if n := strings.Count(js, "showPlayerSoon()"); n != 2 {
		t.Fatalf("showPlayerSoon appears %d times, want exactly 2 (definition + render button) — the export path must not point the player at the old reel", n)
	}
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

// TestExportRefusesBeforeWorkWhenARenderIsActive: the tap's own render
// collides with an active one (the exclusive set again), and the collision
// used to surface only after the reel was rebuilt and the captions possibly
// transcribed — minutes of work, then a failed row naming a job the user
// never connected to the button. The refusal belongs at POST time, before
// any stage runs, and no export row may exist on the way out.
func TestExportRefusesBeforeWorkWhenARenderIsActive(t *testing.T) {
	s, pid := stagedReel(t, "export-conflict")
	ctx := context.Background()
	if _, err := s.DB.CreateJob(ctx, "render", pid, "CPU_HEAVY", `{"out":"busy.mp4"}`); err != nil {
		t.Fatal(err)
	}
	rec, out := do(t, s, "POST", "/api/v1/projects/"+pid+"/export", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("export beside an active render answered %d %v, want 409 before any stage ran", rec.Code, out)
	}
	jobs, err := s.DB.ListJobs(ctx, pid)
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range jobs {
		if j.Type == "export" {
			t.Fatal("an export row was queued despite the collision — the refusal must precede every stage")
		}
	}
}
