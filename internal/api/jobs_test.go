package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
