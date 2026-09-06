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
