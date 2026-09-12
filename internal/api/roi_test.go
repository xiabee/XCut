package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStyleROIRoundTrip: GET reports the embedded preset's roi (null by
// default), PUT writes a workspace override carrying the rect, GET then
// reads it back, DELETE clears it from the override. The override file is
// the mechanism court ROI tuning uses (normalized 0..1 rect for
// frame_diff_roi).
func TestStyleROIRoundTrip(t *testing.T) {
	s := testServer(t)

	rec, out := do(t, s, "GET", "/api/v1/styles/badminton_highlight/roi", "")
	if rec.Code != 200 {
		t.Fatalf("initial roi get: %d %v", rec.Code, out)
	}
	if out["roi"] != nil {
		t.Fatalf("embedded preset unexpectedly carries a roi: %v", out["roi"])
	}

	rec, out = do(t, s, "PUT", "/api/v1/styles/badminton_highlight/roi",
		`{"x":0.1,"y":0.2,"w":0.5,"h":0.6}`)
	if rec.Code != 200 {
		t.Fatalf("roi put: %d %v", rec.Code, out)
	}

	// The override file landed in the workspace styles dir and parses.
	path := filepath.Join(s.Pipe.WS.Root, "styles", "badminton_highlight.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("override file missing: %v", err)
	}
	var preset struct {
		Name      string          `json:"name"`
		MotionROI *map[string]any `json:"motion_roi"`
	}
	if err := json.Unmarshal(b, &preset); err != nil {
		t.Fatalf("override file not a valid preset: %v", err)
	}
	if preset.Name != "badminton_highlight" || preset.MotionROI == nil {
		t.Fatalf("override content unexpected: %s", b)
	}

	rec, out = do(t, s, "GET", "/api/v1/styles/badminton_highlight/roi", "")
	if rec.Code != 200 {
		t.Fatalf("roi get after put: %d", rec.Code)
	}
	roi, ok := out["roi"].(map[string]any)
	if !ok || roi["x"] != 0.1 || roi["h"] != 0.6 {
		t.Fatalf("roi round-trip mismatch: %v", out["roi"])
	}

	rec, out = do(t, s, "DELETE", "/api/v1/styles/badminton_highlight/roi", "")
	if rec.Code != 200 || out["roi"] != nil {
		t.Fatalf("roi delete: %d %v", rec.Code, out)
	}
	rec, _ = do(t, s, "GET", "/api/v1/styles/badminton_highlight/roi", "")
	_ = json.NewDecoder(rec.Body).Decode(&map[string]any{})
}

func TestStyleROIBadInput(t *testing.T) {
	s := testServer(t)

	cases := []struct {
		name string
		path string
		body string
		want int
	}{
		{"unknown style", "/api/v1/styles/nope/roi", `{"x":0,"y":0,"w":1,"h":1}`, 404},
		{"invalid name", "/api/v1/styles/..%2Fetc/roi", `{"x":0,"y":0,"w":1,"h":1}`, 400},
		{"rect out of bounds", "/api/v1/styles/generic_highlight/roi", `{"x":0.5,"y":0,"w":0.6,"h":1}`, 400},
		{"negative origin", "/api/v1/styles/generic_highlight/roi", `{"x":-0.1,"y":0,"w":0.5,"h":0.5}`, 400},
		{"zero width", "/api/v1/styles/generic_highlight/roi", `{"x":0,"y":0,"w":0,"h":0.5}`, 400},
		{"not finite", "/api/v1/styles/generic_highlight/roi", `{"x":0,"y":0,"w":0.5,"h":1e999}`, 400},
	}
	for _, c := range cases {
		rec, out := do(t, s, "PUT", c.path, c.body)
		if rec.Code != c.want {
			t.Errorf("%s: status %d, want %d (%v)", c.name, rec.Code, c.want, out)
		}
	}

	// DELETE without an override is a 404, not a silent no-op.
	rec, out := do(t, s, "DELETE", "/api/v1/styles/generic_highlight/roi", "")
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), "override") {
		t.Fatalf("delete without override: %d %v", rec.Code, out)
	}
}
