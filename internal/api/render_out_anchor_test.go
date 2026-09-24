package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"log/slog"

	"github.com/xiabee/XCut/internal/config"
	"github.com/xiabee/XCut/internal/pipeline"
	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/testmedia"
	"github.com/xiabee/XCut/internal/workspace"
)

// postJSON is the tiny POST-and-decode every test here needs.
func postJSON(t *testing.T, ts *httptest.Server, path, body string) map[string]any {
	t.Helper()
	resp, err := http.Post(ts.URL+path, "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out
}

// A render `out` is a path the caller reads back, but the serve process's
// working directory is invisible to them — a relative out used to land
// wherever serve happened to be started (measured: a probe serve launched
// from the repo root dropped "reel-edited.mp4" into the checkout). The API
// anchors a relative out at the workspace root; the traversal arm is refused
// by the same SafeJoin every other workspace-internal path goes through.

func newAnchorServer(t *testing.T, root string) *Server {
	t.Helper()
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
	t.Cleanup(func() { db.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return &Server{DB: db, Pipe: pipeline.NewDeps(t.Context(), db, ws, cfg, logger)}
}

func postRenderOut(t *testing.T, ts *httptest.Server, projectID, body string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Post(ts.URL+"/api/v1/projects/"+projectID+"/render", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestRelativeRenderOutAnchorsToTheWorkspace(t *testing.T) {
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	root := t.TempDir()
	fixture := filepath.Join(root, "fixture.mp4")
	if _, err := testmedia.Generate(root, "fixture.mp4", testmedia.DefaultFixture(), 320, 240, 10); err != nil {
		t.Fatal(err)
	}
	s := newAnchorServer(t, root)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	defer func() { _ = s.Shutdown(context.Background()) }()

	out := postJSON(t, ts, "/api/v1/projects", `{"name":"anchored"}`)
	id := out["project"].(map[string]any)["id"].(string)

	waitJob := func(jobID string) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Minute)
		for time.Now().Before(deadline) {
			resp, err := http.Get(ts.URL + "/api/v1/jobs/" + jobID)
			if err != nil {
				t.Fatal(err)
			}
			var jout map[string]any
			_ = json.NewDecoder(resp.Body).Decode(&jout)
			resp.Body.Close()
			job := jout["job"].(map[string]any)
			switch job["status"] {
			case "succeeded":
				return
			case "failed":
				t.Fatalf("job %s failed: %v", jobID, job["error_message"])
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Fatalf("job %s did not finish in time", jobID)
	}

	resp, err := http.Post(ts.URL+"/api/v1/projects/"+id+"/assets", "application/json",
		bytes.NewBufferString(fmt.Sprintf(`{"path":%q}`, fixture)))
	if err != nil {
		t.Fatal(err)
	}
	var imported map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&imported)
	resp.Body.Close()
	waitJob(imported["job_id"].(string))

	out = postJSON(t, ts, "/api/v1/projects/"+id+"/analyze", `{}`)
	waitJob(out["job_id"].(string))
	out = postJSON(t, ts, "/api/v1/projects/"+id+"/timeline", `{"style":"generic_highlight"}`)
	waitJob(out["job_id"].(string))

	const rel = "anchored-reel.mp4"
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// A leftover from an earlier run in this directory must not read as "the
	// anchor did not fire" — the absence claim needs a clean start.
	_ = os.Remove(filepath.Join(cwd, rel))
	code, body := postRenderOut(t, ts, id, fmt.Sprintf(`{"out":%q}`, rel))
	if code >= 300 {
		t.Fatalf("relative render out refused: %d %v", code, body)
	}
	waitJob(body["job_id"].(string))

	anchored := filepath.Join(root, rel)
	if _, err := os.Stat(anchored); err != nil {
		t.Fatalf("the reel did not land in the workspace the API anchors relative outs to: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cwd, rel)); err == nil {
		t.Fatalf("a copy of the reel landed in the server's working directory (%s) — the anchor did not fire", cwd)
	}
}

// TestRenderAcceptanceEchoesTheResolvedOut: the 202 body names the resolved
// destination, so a caller that asked for a relative out learns where the
// workspace root put it without probing the filesystem.
func TestRenderAcceptanceEchoesTheResolvedOut(t *testing.T) {
	root := t.TempDir()
	s := newAnchorServer(t, root)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	defer func() { _ = s.Shutdown(context.Background()) }()

	out := postJSON(t, ts, "/api/v1/projects", `{"name":"echo"}`)
	id := out["project"].(map[string]any)["id"].(string)

	code, body := postRenderOut(t, ts, id, `{"out":"echoed-reel.mp4"}`)
	if code >= 300 {
		t.Fatalf("relative render out refused: %d %v", code, body)
	}
	got, _ := body["out"].(string)
	if got != filepath.Join(root, "echoed-reel.mp4") {
		t.Fatalf("acceptance out = %q, want the workspace-anchored path %q", got, filepath.Join(root, "echoed-reel.mp4"))
	}
}

// TestRenderAndExportRefuseRelativeTraversal: `..` in a relative out must be
// refused before any job runs, from both endpoints that carry an out.
func TestRenderAndExportRefuseRelativeTraversal(t *testing.T) {
	root := t.TempDir()
	s := newAnchorServer(t, root)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	defer func() { _ = s.Shutdown(context.Background()) }()

	out := postJSON(t, ts, "/api/v1/projects", `{"name":"traversal"}`)
	id := out["project"].(map[string]any)["id"].(string)

	for _, path := range []string{"/render", "/export"} {
		resp, err := http.Post(ts.URL+"/api/v1/projects/"+id+path, "application/json",
			bytes.NewBufferString(`{"out":"../escape.mp4"}`))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode < 400 || resp.StatusCode > 499 {
			t.Fatalf("%s: want 4xx for a traversal out, got %d: %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "escape.mp4")); err == nil {
		t.Fatal("the traversal out escaped the workspace anyway")
	}
}
