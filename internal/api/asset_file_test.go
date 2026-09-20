package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAssetFileEndpoint: the preview endpoint streams only DB-registered
// asset paths, enforces project ownership, and supports range requests.
// The client never supplies a path.
func TestAssetFileEndpoint(t *testing.T) {
	s := testServer(t)
	ctx := t.Context()

	p1, err := s.DB.CreateProject(ctx, "mine")
	if err != nil {
		t.Fatal(err)
	}
	p2, err := s.DB.CreateProject(ctx, "other")
	if err != nil {
		t.Fatal(err)
	}

	// Real file on disk, registered as p1's asset.
	dir := t.TempDir()
	mediaPath := filepath.Join(dir, "clip.mp4")
	content := "FAKE MP4 BYTES FOR RANGE"
	if err := os.WriteFile(mediaPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	a := storageAssetFor(p1.ID)
	a.Path = mediaPath
	a.Filename = "clip.mp4"
	if err := s.DB.UpsertAsset(ctx, &a); err != nil {
		t.Fatal(err)
	}

	// Full GET streams the file.
	rec, _ := do(t, s, "GET", "/api/v1/projects/"+p1.ID+"/assets/"+a.ID+"/file", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("asset file: %d", rec.Code)
	}
	if got := rec.Body.String(); got != content {
		t.Fatalf("asset file body %q", got)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "video/mp4" {
		t.Errorf("content type %q, want video/mp4", ct)
	}

	// Range request (browser seeking) returns the requested slice.
	req := httptest.NewRequest("GET", "/api/v1/projects/"+p1.ID+"/assets/"+a.ID+"/file", nil)
	req.RemoteAddr = "127.0.0.1:52000" // local client; the gate is auth_test.go's job
	req.Header.Set("Range", "bytes=5-8")
	rec2 := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec2, req)
	if rec2.Code != http.StatusPartialContent {
		t.Fatalf("range request: %d, want 206", rec2.Code)
	}
	if got := rec2.Body.String(); got != content[5:9] {
		t.Fatalf("range body %q, want %q", got, content[5:9])
	}

	// Unknown asset id → 404.
	rec3, _ := do(t, s, "GET", "/api/v1/projects/"+p1.ID+"/assets/nope/file", "")
	if rec3.Code != http.StatusNotFound {
		t.Fatalf("unknown asset: %d", rec3.Code)
	}

	// Another project's asset → 404 (ownership enforced).
	a2 := storageAssetFor(p2.ID)
	a2.Path = mediaPath
	a2.Filename = "clip.mp4"
	if err := s.DB.UpsertAsset(ctx, &a2); err != nil {
		t.Fatal(err)
	}
	rec4, _ := do(t, s, "GET", "/api/v1/projects/"+p1.ID+"/assets/"+a2.ID+"/file", "")
	if rec4.Code != http.StatusNotFound {
		t.Fatalf("cross-project asset: %d, want 404", rec4.Code)
	}

	// A registered asset whose file vanished → 404.
	missing := filepath.Join(dir, "gone.mp4")
	a3 := storageAssetFor(p1.ID)
	a3.Path = missing
	a3.Filename = "gone.mp4"
	if err := s.DB.UpsertAsset(ctx, &a3); err != nil {
		t.Fatal(err)
	}
	rec5, _ := do(t, s, "GET", "/api/v1/projects/"+p1.ID+"/assets/"+a3.ID+"/file", "")
	if rec5.Code != http.StatusNotFound {
		t.Fatalf("missing file: %d, want 404", rec5.Code)
	}
	if !strings.Contains(rec5.Body.String(), "missing") && rec5.Code == http.StatusNotFound {
		// 404 body shape is the JSON error envelope; nothing path-like leaks.
		if strings.Contains(rec5.Body.String(), dir) {
			t.Error("error body must not leak the on-disk path")
		}
	}
}

// TestAssetFileContentType: content-type derives from the asset's real
// extension (imports may be .mov/.webm/...), not a hardcoded mp4 label.
func TestAssetFileContentType(t *testing.T) {
	s := testServer(t)
	ctx := t.Context()
	p, err := s.DB.CreateProject(ctx, "ct")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, tc := range []struct {
		name, want string
	}{
		{"clip.mov", "video/quicktime"},
		{"clip.webm", "video/webm"},
		{"clip.mkv", "video/x-matroska"},
		{"clip.mp4", "video/mp4"},
	} {
		path := filepath.Join(dir, tc.name)
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		a := storageAssetFor(p.ID)
		a.Path = path
		a.Filename = tc.name
		if err := s.DB.UpsertAsset(ctx, &a); err != nil {
			t.Fatal(err)
		}
		rec, _ := do(t, s, "GET", "/api/v1/projects/"+p.ID+"/assets/"+a.ID+"/file", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d", tc.name, rec.Code)
		}
		if got := rec.Header().Get("Content-Type"); got != tc.want {
			t.Errorf("%s: content-type %q, want %q", tc.name, got, tc.want)
		}
	}
}
