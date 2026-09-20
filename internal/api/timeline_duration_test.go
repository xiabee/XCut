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

// An in-range duration has to reach the pipeline as a *request*, not be
// swallowed: the empty project fails later with "no assets", which is the
// proof that validation passed and the value travelled.
func TestTimelineDurationAcceptedWhenInRange(t *testing.T) {
	s := testServer(t)
	p, err := s.DB.CreateProject(context.Background(), "duration-ok")
	if err != nil {
		t.Fatal(err)
	}
	rec, out := do(t, s, "POST", "/api/v1/projects/"+p.ID+"/timeline",
		`{"style":"generic_highlight","duration":45}`)
	if rec.Code == http.StatusBadRequest {
		msg, _ := out["message"].(string)
		if strings.Contains(msg, "duration") {
			t.Fatalf("45s refused: %q", msg)
		}
	}
	body := rec.Body.String()
	if !strings.Contains(body, "job_id") && !strings.Contains(body, "no assets") {
		t.Fatalf("unexpected response for a valid duration: %s", body)
	}
}
