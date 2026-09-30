package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/timeline"
)

// The UI's ruler reads the derived `beat` object the timeline GET computes, so
// the contract between them is the wire format — keys on both sides, the same
// way the pacing chip is pinned. The mapping (source beats → output positions)
// is asserted in internal/timeline; here the question is what the wire carries.

func TestTimelineGETCarriesTheStatedGridOnTheWire(t *testing.T) {
	s := testServer(t)
	p := projectWithAsset(t, s, "beatticks")
	tl := pacingDoc(firstAssetID(t, s, p), []string{""}, []float64{4})
	tl.Metadata = map[string]string{
		timeline.MetaBeatBPM:   "120.0000",
		timeline.MetaBeatPhase: "0.0000",
	}
	body := marshalTimeline(t, tl)
	if rec, out := do(t, s, "PUT", "/api/v1/projects/"+p.ID+"/timeline", body); rec.Code != http.StatusOK {
		t.Fatalf("valid timeline rejected: %d %v", rec.Code, out)
	}

	rec, _ := do(t, s, "GET", "/api/v1/projects/"+p.ID+"/timeline", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET timeline: %d %s", rec.Code, rec.Body.String())
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	raw, ok := envelope["beat"]
	if !ok {
		t.Fatalf("the timeline envelope has no beat object: %s", rec.Body.String())
	}
	var beat map[string]json.RawMessage
	if err := json.Unmarshal(raw, &beat); err != nil {
		t.Fatalf("beat is not an object: %s", raw)
	}
	if got := string(beat["bpm"]); got != "120" {
		t.Errorf("beat.bpm = %s, want 120", got)
	}
	var ticks []float64
	if err := json.Unmarshal(beat["ticks"], &ticks); err != nil {
		t.Fatalf("beat.ticks does not parse: %s", beat["ticks"])
	}
	if len(ticks) != 9 || ticks[0] != 0 || ticks[8] != 4 {
		t.Errorf("beat.ticks = %v, want the 0.5 s lattice 0…4 mapped through the one 4 s clip", ticks)
	}
}

func TestTimelineGETAnswersNullWithoutAStatedGrid(t *testing.T) {
	s := testServer(t)
	p := projectWithAsset(t, s, "nobeat")
	// A hand-built document with no grid metadata: the honest answer is null —
	// "this document states no grid" — not ticks at an invented phase zero.
	tl := pacingDoc(firstAssetID(t, s, p), []string{""}, []float64{4})
	body := marshalTimeline(t, tl)
	if rec, out := do(t, s, "PUT", "/api/v1/projects/"+p.ID+"/timeline", body); rec.Code != http.StatusOK {
		t.Fatalf("valid timeline rejected: %d %v", rec.Code, out)
	}
	rec, _ := do(t, s, "GET", "/api/v1/projects/"+p.ID+"/timeline", "")
	if !strings.Contains(rec.Body.String(), `"beat":null`) {
		t.Errorf("a gridless document answered %q…, want \"beat\":null", snippet(rec.Body.String(), `"beat`))
	}
}

func TestTheBeatRidesTheSameRefusalAsPacing(t *testing.T) {
	// The derived object rides the envelope; a client echoing the envelope back
	// is refused for the nested document, so `beat` can never become stored
	// input either. After the refused save the derivation must be unchanged.
	s := testServer(t)
	p := projectWithAsset(t, s, "beatroundtrip")
	tl := pacingDoc(firstAssetID(t, s, p), []string{""}, []float64{3})
	tl.Metadata = map[string]string{timeline.MetaBeatBPM: "60.0000", timeline.MetaBeatPhase: "0.0000"}
	if rec, out := do(t, s, "PUT", "/api/v1/projects/"+p.ID+"/timeline", marshalTimeline(t, tl)); rec.Code != http.StatusOK {
		t.Fatalf("setup PUT rejected: %d %v", rec.Code, out)
	}
	rec, _ := do(t, s, "GET", "/api/v1/projects/"+p.ID+"/timeline", "")
	if rec2, _ := do(t, s, "PUT", "/api/v1/projects/"+p.ID+"/timeline", rec.Body.String()); rec2.Code == http.StatusOK {
		t.Fatal("saving the raw GET envelope was accepted; the derived beat could round-trip into storage")
	}
	rec3, _ := do(t, s, "GET", "/api/v1/projects/"+p.ID+"/timeline", "")
	if !strings.Contains(rec3.Body.String(), `"bpm":60`) {
		t.Errorf("after the refused save the derived beat changed: %s", rec3.Body.String())
	}
}

func snippet(s, anchor string) string {
	i := strings.Index(s, anchor)
	if i < 0 {
		return s[:min(len(s), 120)]
	}
	return s[i:min(i+40, len(s))]
}
