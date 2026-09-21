package api

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// TestRenderDownloadHeaderCarriesNoUserBytes: the render endpoint puts a
// user-chosen project name into Content-Disposition, and project names are only
// length-checked — CRLF, a quote and a non-ASCII character all store fine. This
// path had never been exercised, so nothing pinned which characters reach the
// header.
func TestRenderDownloadHeaderCarriesNoUserBytes(t *testing.T) {
	s := testServer(t)
	rec, out := do(t, s, "POST", "/api/v1/projects", `{"name":"win\r\nX-Injected: yes \"quoted\" 场地"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %v", rec.Code, out)
	}
	id, _ := out["project"].(map[string]any)["id"].(string)
	if id == "" {
		t.Fatalf("no project id in %v", out)
	}

	// No render yet: the documented refusal, not a 500 over a missing file.
	if rec, _ = do(t, s, "GET", "/api/v1/projects/"+id+"/render", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("render before it exists = %d, want 404", rec.Code)
	}

	path, err := s.Pipe.DefaultRenderPath(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := []byte("not really an mp4, but the handler only streams bytes")
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatal(err)
	}

	rec, _ = do(t, s, "GET", "/api/v1/projects/"+id+"/render", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("render download = %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "video/mp4" {
		t.Fatalf("Content-Type = %q", ct)
	}
	cd := rec.Header().Get("Content-Disposition")
	// One quoted filename parameter, and only characters that cannot break out
	// of it: the sanitizer maps everything else to _, so a stored quote can never
	// become a parameter boundary and a stored CR/LF can never become a header.
	if !regexp.MustCompile(`^inline; filename="[A-Za-z0-9_-]+\.mp4"$`).MatchString(cd) {
		t.Fatalf("Content-Disposition carries more than the safe set: %q", cd)
	}
	if !bytes.Equal(rec.Body.Bytes(), payload) {
		t.Fatalf("streamed %d of %d bytes", len(rec.Body.Bytes()), len(payload))
	}
}

// TestStylesEndpointListsTheEmbeddedPresets: the picker in the UI reads this
// route; a null or empty list renders an empty menu rather than an error, so the
// shape is the thing worth pinning.
func TestStylesEndpointListsTheEmbeddedPresets(t *testing.T) {
	s := testServer(t)
	rec, out := do(t, s, "GET", "/api/v1/styles", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("styles = %d: %v", rec.Code, out)
	}
	names, ok := out["styles"].([]any)
	if !ok {
		t.Fatalf("styles field = %T, want a JSON array", out["styles"])
	}
	if len(names) == 0 {
		t.Fatal("the styles list is empty; the embedded presets must be served")
	}
	for _, want := range []string{"generic_highlight", "badminton_highlight"} {
		found := false
		for _, n := range names {
			if n == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("preset %q missing from %v", want, names)
		}
	}
}
