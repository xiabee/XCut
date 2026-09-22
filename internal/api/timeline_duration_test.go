package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// The API is where a remote UI or a script sets reel length, so the bound must
// be refused here with a readable error rather than reaching the job queue and
// failing (or silently succeeding) later.
func TestTimelineDurationRejectedAtTheAPIBoundary(t *testing.T) {
	s := testServer(t)
	p, err := s.DB.CreateProject(context.Background(), "duration-api")
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"style":"generic_highlight","duration":0.5}`,
		`{"style":"generic_highlight","duration":999999}`,
		`{"style":"generic_highlight","duration":-5}`,
	} {
		rec, out := do(t, s, "POST", "/api/v1/projects/"+p.ID+"/timeline", body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %s: status %d, want 400 (%s)", body, rec.Code, rec.Body.String())
		}
		if msg, _ := out["message"].(string); !strings.Contains(msg, "duration") {
			t.Fatalf("body %s: message %q does not name the offending field", body, msg)
		}
		if code, _ := out["error"].(string); code != "validation" {
			t.Fatalf("body %s: error code %q, want validation", body, code)
		}
		if strings.Contains(rec.Body.String(), "job_id") {
			t.Fatalf("body %s: a job was accepted despite the bad duration", body)
		}
	}
}

// Where the API can prove a value travelled, it is a rejection: a 400 naming the
// field can only happen if the JSON reached TimelineRequest and then pipeline
// validation. What is deliberately NOT tested here is an accepted request:
// the endpoint is asynchronous, and a test that returns after the 202 leaves
// the job writing into the workspace after TempDir cleanup starts — invisible
// on Windows, "directory not empty" on Linux (found by the cross-platform leg,
// session #15). Cover "the value reaches the engine" at the pipeline level,
// where there is no goroutine to wait for.

// The same boundary argument for the beat-snap knob, whose wire name is the one
// thing a client and the server must not disagree about: the JSON key is
// `beat_snap`, and a rename of the struct tag would silently turn every request
// into "keep the style's own tolerance".
func TestTimelineBeatSnapRejectedAtTheAPIBoundary(t *testing.T) {
	s := testServer(t)
	p, err := s.DB.CreateProject(context.Background(), "beatsnap-api")
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"style":"generic_highlight","beat_snap":2}`,
		`{"style":"generic_highlight","beat_snap":-2}`,
		`{"style":"generic_highlight","beat_snap":1e3}`,
	} {
		rec, out := do(t, s, "POST", "/api/v1/projects/"+p.ID+"/timeline", body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %s: status %d, want 400 (%s)", body, rec.Code, rec.Body.String())
		}
		msg, _ := out["message"].(string)
		if !strings.Contains(msg, "beat snap") {
			t.Fatalf("body %s: message %q does not name the offending knob", body, msg)
		}
		if code, _ := out["error"].(string); code != "validation" {
			t.Fatalf("body %s: error code %q, want validation", body, code)
		}
		if strings.Contains(rec.Body.String(), "job_id") {
			t.Fatalf("body %s: a job was accepted despite the bad tolerance", body)
		}
	}
}
