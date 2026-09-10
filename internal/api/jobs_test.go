package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xiabee/XCut/internal/config"
	"github.com/xiabee/XCut/internal/pipeline"
	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/testmedia"
	"github.com/xiabee/XCut/internal/workspace"
)

// TestAsyncJobFlow runs the whole pipeline through HTTP: create project →
// import (path) → analyze → timeline → render, polling job status between
// steps, then verifies the produced MP4. Requires ffmpeg; skips otherwise.
func TestAsyncJobFlow(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	root := t.TempDir()
	fixture := filepath.Join(root, "fixture.mp4")
	if _, err := testmedia.Generate(root, "fixture.mp4", testmedia.DefaultFixture(), 320, 240, 10); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Workspace = root
	_ = config.Resolve(cfg)
	ws := workspace.New(root)
	if err := ws.Ensure(); err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(ws.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := &Server{DB: db, Pipe: pipeline.NewDeps(t.Context(), db, ws, cfg, logger)}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	defer s.Shutdown()

	post := func(path, body string) map[string]any {
		t.Helper()
		resp, err := http.Post(ts.URL+path, "application/json", bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		if resp.StatusCode >= 300 {
			t.Fatalf("POST %s → %d: %v", path, resp.StatusCode, out)
		}
		return out
	}
	get := func(path string) map[string]any {
		t.Helper()
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return out
	}
	waitJob := func(jobID string) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Minute)
		for time.Now().Before(deadline) {
			j := get("/api/v1/jobs/" + jobID)["job"].(map[string]any)
			switch j["status"] {
			case "succeeded":
				return
			case "failed":
				t.Fatalf("job %s failed: %v", jobID, j["error_message"])
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Fatalf("job %s did not finish in time", jobID)
	}

	// Create project.
	out := post("/api/v1/projects", `{"name":"http-e2e"}`)
	p := out["project"].(map[string]any)
	id := p["id"].(string)

	// Import → analyze → timeline → render, all async, all via HTTP.
	out = post("/api/v1/projects/"+id+"/assets", fmt.Sprintf(`{"path":%q}`, fixture))
	jobID := out["job_id"].(string)
	waitJob(jobID)

	out = post("/api/v1/projects/"+id+"/analyze", `{}`)
	waitJob(out["job_id"].(string))

	out = post("/api/v1/projects/"+id+"/timeline", `{"style":"generic_highlight"}`)
	waitJob(out["job_id"].(string))

	outPath := filepath.Join(root, "http-render.mp4")
	out = post("/api/v1/projects/"+id+"/render", fmt.Sprintf(`{"out":%q}`, outPath))
	waitJob(out["job_id"].(string))

	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("render output missing: %v", err)
	}

	// Jobs list reflects the full lifecycle.
	jobs := get("/api/v1/projects/" + id + "/jobs")["jobs"].([]any)
	if len(jobs) != 4 {
		t.Fatalf("jobs recorded = %d, want 4", len(jobs))
	}
}

// TestRenderOverwriteGuardHTTP: requesting a render whose `out` points at
// the imported source must fail with 4xx *before* any job runs, and the
// original file must stay byte-identical (imports are referenced in place).
func TestRenderOverwriteGuardHTTP(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	root := t.TempDir()
	fixture := filepath.Join(root, "fixture.mp4")
	if _, err := testmedia.Generate(root, "fixture.mp4", testmedia.DefaultFixture(), 320, 240, 10); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Workspace = root
	_ = config.Resolve(cfg)
	ws := workspace.New(root)
	if err := ws.Ensure(); err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(ws.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := &Server{DB: db, Pipe: pipeline.NewDeps(t.Context(), db, ws, cfg, logger)}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	defer s.Shutdown()

	post := func(path, body string) map[string]any {
		t.Helper()
		resp, err := http.Post(ts.URL+path, "application/json", bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		if resp.StatusCode < 300 {
			t.Fatalf("POST %s must be refused, got %d: %v", path, resp.StatusCode, out)
		}
		if resp.StatusCode < 400 || resp.StatusCode > 499 {
			t.Fatalf("POST %s: want 4xx, got %d", path, resp.StatusCode)
		}
		return out
	}

	// Project + import (sync enough: wait for the asset row via jobs API is
	// overkill — import is RunInline through the pipeline).
	resp, err := http.Post(ts.URL+"/api/v1/projects", "application/json", bytes.NewBufferString(`{"name":"guard"}`))
	if err != nil {
		t.Fatal(err)
	}
	var proj struct {
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&proj)
	resp.Body.Close()

	importResp, err := http.Post(ts.URL+"/api/v1/projects/"+proj.Project.ID+"/assets",
		"application/json", bytes.NewBufferString(fmt.Sprintf(`{"path":%q}`, fixture)))
	if err != nil {
		t.Fatal(err)
	}
	var imported map[string]any
	_ = json.NewDecoder(importResp.Body).Decode(&imported)
	importResp.Body.Close()
	jobID, _ := imported["job_id"].(string)
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		jr, err := http.Get(ts.URL + "/api/v1/jobs/" + jobID)
		if err != nil {
			t.Fatal(err)
		}
		var jout map[string]any
		_ = json.NewDecoder(jr.Body).Decode(&jout)
		jr.Body.Close()
		job := jout["job"].(map[string]any)
		if job["status"] == "succeeded" {
			break
		}
		if job["status"] == "failed" {
			t.Fatalf("import failed: %v", job["error_message"])
		}
		time.Sleep(200 * time.Millisecond)
	}

	post("/api/v1/projects/"+proj.Project.ID+"/render", fmt.Sprintf(`{"out":%q}`, fixture))

	after, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("source media was modified by a refused HTTP render")
	}
	if _, err := os.Stat(fixture + ".partial"); err == nil {
		t.Fatal("refused render must not leave a .partial next to the source")
	}
}

// TestRenderBodyAndDuplicateGuard covers the render endpoint's request
// handling: empty body is accepted, malformed body is exactly one 400 with
// no job queued, and a duplicate active render conflicts with 409.
// No ffmpeg needed — the refused paths never reach the pipeline and the
// accepted path fails fast at "no timeline".
func TestRenderBodyAndDuplicateGuard(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.Workspace = root
	_ = config.Resolve(cfg)
	ws := workspace.New(root)
	if err := ws.Ensure(); err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(ws.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := &Server{DB: db, Pipe: pipeline.NewDeps(t.Context(), db, ws, cfg, logger)}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	defer s.Shutdown()

	// Project row (created through the API for realism).
	resp, err := http.Post(ts.URL+"/api/v1/projects", "application/json", bytes.NewBufferString(`{"name":"dup-guard"}`))
	if err != nil {
		t.Fatal(err)
	}
	var projOut struct {
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&projOut)
	resp.Body.Close()
	renderURL := "/api/v1/projects/" + projOut.Project.ID + "/render"

	// 1. Malformed body → single 400, nothing queued.
	resp, err = http.Post(ts.URL+renderURL, "application/json", bytes.NewBufferString(`{"out": broken`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed body → %d, want 400", resp.StatusCode)
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	var first map[string]any
	if err := dec.Decode(&first); err != nil {
		t.Fatalf("400 body is not JSON: %v (%s)", err, body)
	}
	if dec.More() {
		t.Fatalf("response carries more than one JSON value (double write?): %s", body)
	}
	jobs := getJobs(t, ts.URL, projOut.Project.ID)
	if len(jobs) != 0 {
		t.Fatalf("malformed body queued %d jobs, want 0", len(jobs))
	}

	// 2. Empty body → 202, job queued (it will fail later on missing
	// timeline, which is irrelevant here).
	resp, err = http.Post(ts.URL+renderURL, "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	var accepted struct {
		JobID string `json:"job_id"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&accepted)
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("empty body → %d, want 202", resp.StatusCode)
	}
	// Wait for the job to reach a terminal state before staging the
	// duplicate marker below (storage lessons: the unique index does not
	// care who enqueued it).
	waitTerminal(t, ts.URL, accepted.JobID)

	// 3. Duplicate render while one is queued/running → 409.
	if _, err := db.CreateJob(t.Context(), "render", projOut.Project.ID, "CPU_HEAVY", ""); err != nil {
		t.Fatal(err)
	}
	resp, err = http.Post(ts.URL+renderURL, "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate render → %d, want 409", resp.StatusCode)
	}
}

func waitTerminal(t *testing.T, baseURL, jobID string) {
	t.Helper()
	if jobID == "" {
		t.Fatal("no job id")
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/api/v1/jobs/" + jobID)
		if err != nil {
			t.Fatal(err)
		}
		var out struct {
			Job struct {
				Status string `json:"status"`
			} `json:"job"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		resp.Body.Close()
		switch out.Job.Status {
		case "succeeded", "failed", "cancelled":
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("job %s did not reach a terminal state in time", jobID)
}

func getJobs(t *testing.T, baseURL, projectID string) []any {
	t.Helper()
	resp, err := http.Get(baseURL + "/api/v1/projects/" + projectID + "/jobs")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Jobs []any `json:"jobs"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out.Jobs
}

// TestJobCancelEndpoint covers POST /api/v1/jobs/{id}/cancel: a running job
// is accepted (202) and reaches the cancelled terminal state, a terminal job
// conflicts (409), an unknown id is 404, and an active row with no live
// runner in this process (crash leftover) conflicts with the remediation in
// the message. No ffmpeg needed — the runner is a stub.
func TestJobCancelEndpoint(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.Workspace = root
	_ = config.Resolve(cfg)
	ws := workspace.New(root)
	if err := ws.Ensure(); err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(ws.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := &Server{DB: db, Pipe: pipeline.NewDeps(t.Context(), db, ws, cfg, logger)}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	defer s.Shutdown()

	ctx := t.Context()
	started := make(chan struct{})
	jobID, err := s.Pipe.Queue.RunAsync(ctx, "import", "", "IO_HEAVY", nil,
		func(ctx context.Context, progress func(float64)) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		})
	if err != nil {
		t.Fatal(err)
	}
	<-started

	// Wait for running.
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := http.Get(ts.URL + "/api/v1/jobs/" + jobID)
		if err != nil {
			t.Fatal(err)
		}
		var out struct {
			Job struct {
				Status string `json:"status"`
			} `json:"job"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		resp.Body.Close()
		if out.Job.Status == "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job never started, status=%s", out.Job.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Cancel → 202.
	resp, err := http.Post(ts.URL+"/api/v1/jobs/"+jobID+"/cancel", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("cancel → %d, want 202", resp.StatusCode)
	}

	// Terminal state is cancelled.
	deadline = time.Now().Add(5 * time.Second)
	for {
		resp, err := http.Get(ts.URL + "/api/v1/jobs/" + jobID)
		if err != nil {
			t.Fatal(err)
		}
		var out struct {
			Job struct {
				Status string `json:"status"`
			} `json:"job"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		resp.Body.Close()
		if out.Job.Status == "cancelled" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job did not cancel in time, status=%s", out.Job.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Cancel again → 409 (already terminal).
	resp, err = http.Post(ts.URL+"/api/v1/jobs/"+jobID+"/cancel", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("second cancel → %d, want 409", resp.StatusCode)
	}

	// Unknown id → 404.
	resp, err = http.Post(ts.URL+"/api/v1/jobs/job_nope/cancel", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown cancel → %d, want 404", resp.StatusCode)
	}

	// Active row with no live runner (crash leftover) → 409 with remediation.
	orphan, err := db.CreateJob(ctx, "render", "", "CPU_HEAVY", "")
	if err != nil {
		t.Fatal(err)
	}
	resp, err = http.Post(ts.URL+"/api/v1/jobs/"+orphan.ID+"/cancel", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("orphan cancel → %d, want 409", resp.StatusCode)
	}
	if !strings.Contains(string(body), "reconciled") {
		t.Fatalf("orphan cancel message lacks remediation: %s", body)
	}
}
