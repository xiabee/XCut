package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/storage"
)

// The picker asks the server what a motion name means, and the server answers with
// the same rule the reel builder applies — so these cases are about the wire keys
// the page reads, whose region is used, and what gets refused. The geometry's exact
// numbers belong to style's own tests; repeating them here would be a second copy
// that can agree with itself while both drift.

// planAsset is a project with one asset, optionally carrying a region.
func planAsset(t *testing.T, name string, roi *storage.MotionROI) (*Server, string, string) {
	t.Helper()
	s := testServer(t)
	if _, err := s.DB.CreateProject(context.Background(), name); err != nil {
		t.Fatal(err)
	}
	p, err := s.DB.GetProjectByName(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	asset := storageAssetFor(p.ID)
	if err := s.DB.UpsertAsset(context.Background(), &asset); err != nil {
		t.Fatal(err)
	}
	if roi != nil {
		if err := s.DB.SetAssetROI(context.Background(), asset.ID, roi); err != nil {
			t.Fatal(err)
		}
	}
	return s, p.ID, asset.ID
}

func plan(t *testing.T, s *Server, pid, body string) (int, map[string]any) {
	t.Helper()
	rec, out := do(t, s, "POST", "/api/v1/projects/"+pid+"/motion/plan", body)
	return rec.Code, out
}

func motionOf(t *testing.T, out map[string]any) map[string]any {
	t.Helper()
	m, ok := out["motion"].(map[string]any)
	if !ok {
		t.Fatalf("no motion object in the reply: %v", out)
	}
	return m
}

func TestMotionPlanWire(t *testing.T) {
	s, pid, aid := planAsset(t, "plan-wire", nil)
	code, out := plan(t, s, pid, `{"mode":"drift","asset":"`+aid+`","ordinal":1}`)
	if code != http.StatusOK {
		t.Fatalf("plan: %d %v", code, out)
	}
	if out["mode"] != "drift" || out["framing"] != "drift" {
		t.Errorf("the reply lost the mode it was asked for: %v", out)
	}
	m := motionOf(t, out)
	for _, key := range []string{"zoom", "from", "to"} {
		if m[key] == nil {
			t.Errorf("the motion the page reads has no %q key: %v", key, m)
		}
	}
	// The default window is the server's number, not one the page invents.
	if m["zoom"].(float64) != 0.85 {
		t.Errorf("zoom = %v, want the picker's default window 0.85", m["zoom"])
	}
}

// TestMotionPlanAimsAtTheRegionsRow: the region comes from the asset, which is what
// makes a hand pick agree with the reel the style would have built.
func TestMotionPlanAimsAtTheRegionsRow(t *testing.T) {
	s, pid, aid := planAsset(t, "plan-roi", &storage.MotionROI{X: 0.2, Y: 0.1, W: 0.4, H: 0.3})
	code, out := plan(t, s, pid, `{"mode":"roi","asset":"`+aid+`"}`)
	if code != http.StatusOK {
		t.Fatalf("roi plan: %d %v", code, out)
	}
	m := motionOf(t, out)
	from, _ := m["from"].([]any)
	to, _ := m["to"].([]any)
	if len(from) != 2 || len(to) != 2 {
		t.Fatalf("a roi plan needs both centres: %v", m)
	}
	// Centre of 0.2,0.1 + 0.4,0.3 is 0.4, 0.25; a still window, so both ends agree.
	if from[0] != 0.4 || from[1] != 0.25 || to[0] != 0.4 || to[1] != 0.25 {
		t.Errorf("the window sits at %v → %v, want the region centre 0.4,0.25", from, to)
	}
}

func TestMotionPlanRefusesWithoutARegion(t *testing.T) {
	s, pid, aid := planAsset(t, "plan-noroi", nil)
	code, out := plan(t, s, pid, `{"mode":"roi","asset":"`+aid+`"}`)
	if code == http.StatusOK {
		t.Fatalf("an asset with no region produced a plan: %v", out)
	}
	if msg, _ := out["message"].(string); !strings.Contains(msg, "region") {
		t.Errorf("refused with %q, which does not say what to draw first", msg)
	}
}

// TestMotionPlanNoneClaimsNothing: a clip with no window must not come back holding
// the word that says it has one.
func TestMotionPlanNoneClaimsNothing(t *testing.T) {
	s, pid, aid := planAsset(t, "plan-none", nil)
	for _, mode := range []string{"none", ""} {
		code, out := plan(t, s, pid, `{"mode":"`+mode+`","asset":"`+aid+`"}`)
		if code != http.StatusOK {
			t.Fatalf("mode %q: %d %v", mode, code, out)
		}
		if out["motion"] != nil {
			t.Errorf("mode %q returned a window: %v", mode, out["motion"])
		}
		if out["framing"] != "" {
			t.Errorf("mode %q still claims framing %q", mode, out["framing"])
		}
	}
}

func TestMotionPlanRefusesWhatItCannotDraw(t *testing.T) {
	s, pid, aid := planAsset(t, "plan-bad", nil)
	for _, c := range []struct{ body, want string }{
		{`{"mode":"orbit","asset":"` + aid + `"}`, "not one of"},
		{`{"mode":"punch_in","asset":"` + aid + `","zoom":1.5}`, "window"},
		{`{"mode":"drift","asset":"` + aid + `","zoom":-0.2}`, "window"},
	} {
		code, out := plan(t, s, pid, c.body)
		if code == http.StatusOK {
			t.Errorf("%s was answered with a plan: %v", c.body, out)
			continue
		}
		if msg, _ := out["message"].(string); !strings.Contains(msg, c.want) {
			t.Errorf("%s refused with %q, which does not say %q", c.body, msg, c.want)
		}
	}
}

// TestMotionPlanStaysInsideItsProject: another project's asset is not a region to
// aim at, it is a 404 — the same rule every other per-asset endpoint holds to.
func TestMotionPlanStaysInsideItsProject(t *testing.T) {
	s1, _, aid1 := planAsset(t, "plan-mine", &storage.MotionROI{X: 0.1, Y: 0.1, W: 0.2, H: 0.2})
	s2, pid2, _ := planAsset(t, "plan-theirs", nil)
	if s1 == s2 {
		t.Fatal("the fixture gave one server to both projects")
	}
	code, out := plan(t, s2, pid2, `{"mode":"roi","asset":"`+aid1+`"}`)
	if code != http.StatusNotFound {
		t.Errorf("another project's asset answered %d %v, want 404", code, out)
	}
}

// TestThePickerWritesGeometryAndItsClaimTogether: the write-back lives in the page,
// and no browser runs in CI, so the cheapest honest guard is the pairing itself —
// the two lines that must travel together. If either goes missing, a clip ends up
// with a window it does not describe or a description of a window it does not have.
func TestThePickerWritesGeometryAndItsClaimTogether(t *testing.T) {
	_, js, _ := i18nAssets(t)
	for _, want := range []string{"c.motion = res.motion", "{ framing: res.framing }", "delete c.motion"} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js no longer contains %q — the picker's geometry and its claim are written apart", want)
		}
	}
}
