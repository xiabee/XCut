package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/testmedia"
)

// seedUploadProject creates a project and returns its id.
func seedUploadProject(t *testing.T, s *Server, name string) string {
	t.Helper()
	rec, out := do(t, s, "POST", "/api/v1/projects", `{"name":"`+name+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create project: %d %v", rec.Code, out)
	}
	return out["project"].(map[string]any)["id"].(string)
}

func uploadReq(t *testing.T, s *Server, pid, filename string, body string) (rec *httptest.ResponseRecorder, out map[string]any) {
	t.Helper()
	path := "/api/v1/projects/" + pid + "/assets/upload"
	if filename != "" {
		path += "?filename=" + url.QueryEscape(filename)
	}
	return do(t, s, "POST", path, body)
}

// TestAssetUploadImportsCopy: a real small video uploaded as content lands
// under imports/<project>/, probes cleanly, and produces an asset row.
func TestAssetUploadImportsCopy(t *testing.T) {
	s := testServer(t)
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	pid := seedUploadProject(t, s, "upload-ok")

	root := t.TempDir()
	src, err := testmedia.Generate(root, "clip.mp4", testmedia.DefaultFixture(), 160, 120, 6)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}

	rec, out := uploadReq(t, s, pid, "match cam 1.mp4", string(body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload: %d %v", rec.Code, out)
	}
	asset := out["asset"].(map[string]any)
	if asset["filename"] != "match cam 1.mp4" {
		t.Fatalf("asset filename = %v", asset["filename"])
	}

	// The landed copy lives in the workspace imports dir, one folder per project.
	landed := filepath.Join(s.Pipe.WS.ImportsDir(), pid, "match cam 1.mp4")
	if _, err := os.Stat(landed); err != nil {
		t.Fatalf("uploaded copy missing at %s: %v", landed, err)
	}

	// A second upload of the same name must not overwrite: it lands as a new
	// file and becomes a new asset (distinct path → distinct asset id).
	rec2, out2 := uploadReq(t, s, pid, "match cam 1.mp4", string(body))
	if rec2.Code != http.StatusCreated {
		t.Fatalf("second upload: %d %v", rec2.Code, out2)
	}
	asset2 := out2["asset"].(map[string]any)
	if asset2["path"] == asset["path"] {
		t.Fatal("second upload overwrote the first landed copy")
	}
}

// TestAssetUploadRefusesJunk: path-traversal names are reduced to a bare
// name inside imports/, empty bodies are refused, and non-media content is
// cleaned up instead of littering the imports dir.
func TestAssetUploadRefusesJunk(t *testing.T) {
	s := testServer(t)
	pid := seedUploadProject(t, s, "upload-junk")

	// Traversal attempt: the name is reduced; the landed file (had the
	// content been media) could only live inside imports/.
	rec, out := uploadReq(t, s, pid, `..\..\evil.mp4`, "x")
	if rec.Code == http.StatusCreated {
		t.Fatalf("junk content must not import, got %d", rec.Code)
	}
	landed := filepath.Join(s.Pipe.WS.ImportsDir(), pid)
	entries, err := os.ReadDir(landed)
	if err == nil {
		for _, e := range entries {
			if strings.Contains(e.Name(), "..") {
				t.Fatalf("traversal name landed: %s", e.Name())
			}
		}
	}

	// Empty body.
	rec, out = uploadReq(t, s, pid, "empty.mp4", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty upload: %d %v", rec.Code, out)
	}

	// Missing filename.
	rec, out = uploadReq(t, s, pid, "", "x")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing filename: %d %v", rec.Code, out)
	}

	// Non-media content: the copy is probed, refused, and cleaned up.
	rec, out = uploadReq(t, s, pid, "notes.txt", "definitely not video")
	if rec.Code == http.StatusCreated {
		t.Fatalf("text content must not import, got %d", rec.Code)
	}
	if entries, err := os.ReadDir(landed); err == nil && len(entries) != 0 {
		t.Fatalf("failed probe left litter in imports/: %d entries", len(entries))
	}
}

// TestProjectDeleteRemovesImports: deleting the project takes its
// uploaded-copy directory with it — the artifacts must not accumulate on
// disk forever.
func TestProjectDeleteRemovesImports(t *testing.T) {
	s := testServer(t)
	if !testmedia.HasFFmpeg() {
		t.Skip("ffmpeg not available")
	}
	pid := seedUploadProject(t, s, "delete-imports")

	root := t.TempDir()
	src, err := testmedia.Generate(root, "clip.mp4", testmedia.DefaultFixture(), 160, 120, 6)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if rec, out := uploadReq(t, s, pid, "clip.mp4", string(body)); rec.Code != http.StatusCreated {
		t.Fatalf("upload: %d %v", rec.Code, out)
	}
	imports := filepath.Join(s.Pipe.WS.ImportsDir(), pid)
	if entries, err := os.ReadDir(imports); err != nil || len(entries) == 0 {
		t.Fatalf("uploaded copy missing pre-delete: %v", err)
	}

	rec, out := do(t, s, "DELETE", "/api/v1/projects/"+pid, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %v", rec.Code, out)
	}
	if _, err := os.Stat(imports); !os.IsNotExist(err) {
		t.Fatalf("imports dir must die with the project, err=%v", err)
	}
}
