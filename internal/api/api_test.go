package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/xcerr"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	db, err := storage.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return &Server{DB: db}
}

func do(t *testing.T, s *Server, method, path, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

func TestHealth(t *testing.T) {
	s := testServer(t)
	rec, out := do(t, s, "GET", "/api/v1/health", "")
	if rec.Code != http.StatusOK || out["ok"] != true {
		t.Fatalf("health: %d %v", rec.Code, out)
	}
}

func TestProjectCRUD(t *testing.T) {
	s := testServer(t)

	// Create.
	rec, out := do(t, s, "POST", "/api/v1/projects", `{"name":"e2e"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %v", rec.Code, out)
	}
	p := out["project"].(map[string]any)
	id := p["id"].(string)

	// Duplicate name → 400.
	if rec, _ = do(t, s, "POST", "/api/v1/projects", `{"name":"e2e"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("duplicate create: %d", rec.Code)
	}

	// List.
	rec, out = do(t, s, "GET", "/api/v1/projects", "")
	if rec.Code != http.StatusOK || len(out["projects"].([]any)) != 1 {
		t.Fatalf("list: %d %v", rec.Code, out)
	}

	// Get.
	rec, out = do(t, s, "GET", "/api/v1/projects/"+id, "")
	if rec.Code != http.StatusOK || out["project"] == nil {
		t.Fatalf("get: %d", rec.Code)
	}

	// Unknown → 404.
	if rec, _ = do(t, s, "GET", "/api/v1/projects/prj_nope", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown project: %d", rec.Code)
	}

	// Delete then 404.
	if rec, _ = do(t, s, "DELETE", "/api/v1/projects/"+id, ""); rec.Code != http.StatusOK {
		t.Fatalf("delete: %d", rec.Code)
	}
	if rec, _ = do(t, s, "GET", "/api/v1/projects/"+id, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete: %d", rec.Code)
	}
}

func TestCreateValidation(t *testing.T) {
	s := testServer(t)
	for _, body := range []string{
		`{"name":""}`,
		`{"name":"  "}`,
		`not json`,
		`{"name":"` + strings.Repeat("x", 200) + `"}`,
	} {
		rec, _ := do(t, s, "POST", "/api/v1/projects", body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %q: got %d, want 400", body, rec.Code)
		}
	}
}

func TestJobsEndpoints(t *testing.T) {
	s := testServer(t)
	rec, out := do(t, s, "GET", "/api/v1/jobs", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("jobs: %d", rec.Code)
	}
	if _, ok := out["jobs"].([]any); !ok {
		t.Fatalf("jobs not a list: %v", out)
	}
	// Unknown project jobs → 404.
	if rec, _ = do(t, s, "GET", "/api/v1/projects/prj_x/jobs", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown project jobs: %d", rec.Code)
	}
}

func TestErrorCodesDoNotLeakInternals(t *testing.T) {
	s := testServer(t)
	rec, out := do(t, s, "GET", "/api/v1/projects/prj_x", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d", rec.Code)
	}
	e, _ := out["error"].(string)
	if e != string(xcerr.CodeNotFound) {
		t.Fatalf("error code = %v", out)
	}
}
