package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/storage"
	"github.com/xiabee/XCut/internal/timeline"
)

// The UI's pacing chip reads the pacing object the timeline GET computes from the
// stored document, so the contract between them is the wire format: the keys, not
// the Go field names. Asserting the keys is what makes a rename on either side
// fail here rather than render blanks in the browser.

// pacingDoc lays shots back to back on the timeline. Every shot reads its own
// window from the start of the asset, so the fixture stays inside a 10 s source
// while the numbers below are about output positions, not source ones.
func pacingDoc(asset string, scores []string, lengths []float64) *timeline.Timeline {
	clips := make([]timeline.Clip, 0, len(lengths))
	cursor := 0.0
	for i, l := range lengths {
		c := timeline.Clip{
			ID: "c" + strconv.Itoa(i), AssetID: asset,
			SourceStart: 0, SourceEnd: l,
			TimelineStart: cursor, Speed: 1, Volume: 1,
		}
		if scores[i] != "" {
			c.Metadata = map[string]string{"score": scores[i]}
		}
		clips = append(clips, c)
		cursor += l
	}
	return &timeline.Timeline{
		Version: timeline.Version,
		Canvas:  timeline.Canvas{Width: 640, Height: 360, FPS: 30},
		Tracks:  []timeline.Track{{ID: "v1", Kind: "video", Clips: clips}},
	}
}

func projectWithAsset(t *testing.T, s *Server, name string) *storage.Project {
	t.Helper()
	if _, err := s.DB.CreateProject(t.Context(), name); err != nil {
		t.Fatal(err)
	}
	p, err := s.DB.GetProjectByName(t.Context(), name)
	if err != nil {
		t.Fatal(err)
	}
	asset := storageAssetFor(p.ID)
	if err := s.DB.UpsertAsset(t.Context(), &asset); err != nil {
		t.Fatal(err)
	}
	assets, _ := s.DB.ListAssets(t.Context(), p.ID)
	if len(assets) == 0 {
		t.Fatal("no asset row")
	}
	return p
}

func pacingFromGet(t *testing.T, s *Server, p *storage.Project) map[string]json.RawMessage {
	t.Helper()
	rec, _ := do(t, s, "GET", "/api/v1/projects/"+p.ID+"/timeline", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET timeline: %d %s", rec.Code, rec.Body.String())
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	raw, ok := envelope["pacing"]
	if !ok {
		t.Fatalf("the timeline envelope has no pacing object: %s", rec.Body.String())
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func num(t *testing.T, m map[string]json.RawMessage, key string) float64 {
	t.Helper()
	raw, ok := m[key]
	if !ok {
		t.Fatalf("pacing object has no %q key; keys present: %v", key, keysOf(m))
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("pacing.%s is not a number: %s", key, string(raw))
	}
	return f
}

func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestTimelineGETCarriesPacingOnTheWire(t *testing.T) {
	s := testServer(t)
	p := projectWithAsset(t, s, "pacing")
	// Shots of 4 s, 2 s and 6 s, scored 0.3 / 0.9 / 0.4: mean 4, median 4,
	// longest 6, and the top shot begins 4 s into the reel.
	tl := pacingDoc(firstAssetID(t, s, p), []string{"0.3", "0.9", "0.4"}, []float64{4, 2, 6})
	body := marshalTimeline(t, tl)
	if rec, out := do(t, s, "PUT", "/api/v1/projects/"+p.ID+"/timeline", body); rec.Code != http.StatusOK {
		t.Fatalf("valid timeline rejected: %d %v", rec.Code, out)
	}
	pac := pacingFromGet(t, s, p)
	for _, key := range []string{"shots", "mean_seconds", "median_seconds", "longest_seconds", "hook_seconds", "hook_score", "scored_shots"} {
		if _, ok := pac[key]; !ok {
			t.Errorf("pacing is missing the %q key (present: %v)", key, keysOf(pac))
		}
	}
	if got := num(t, pac, "shots"); got != 3 {
		t.Errorf("shots = %v, want 3", got)
	}
	if got := num(t, pac, "mean_seconds"); got != 4 {
		t.Errorf("mean_seconds = %v, want 4", got)
	}
	if got := num(t, pac, "median_seconds"); got != 4 {
		t.Errorf("median_seconds = %v, want 4", got)
	}
	if got := num(t, pac, "longest_seconds"); got != 6 {
		t.Errorf("longest_seconds = %v, want 6", got)
	}
	if got := num(t, pac, "hook_seconds"); got != 4 {
		t.Errorf("hook_seconds = %v, want 4 (the 0.9 shot's output position)", got)
	}
	if got := num(t, pac, "scored_shots"); got != 3 {
		t.Errorf("scored_shots = %v, want 3", got)
	}
}

// TestPacingSurvivesTheSaveRoundTrip: the UI saves whatever it fetched, and the
// derived object rides along in the envelope. A field the server computed must not
// become a field the client can poison — the save is refused or the extra key is
// ignored, never accepted into the stored document.
func TestPacingSurvivesTheSaveRoundTrip(t *testing.T) {
	s := testServer(t)
	p := projectWithAsset(t, s, "roundtrip")
	tl := pacingDoc(firstAssetID(t, s, p), []string{"0.5"}, []float64{3})
	body := marshalTimeline(t, tl)
	if rec, out := do(t, s, "PUT", "/api/v1/projects/"+p.ID+"/timeline", body); rec.Code != http.StatusOK {
		t.Fatalf("setup PUT rejected: %d %v", rec.Code, out)
	}
	// Send the whole GET envelope back as the save body: the document is nested
	// under "timeline" here, so a client doing this by mistake must be refused —
	// a tracks-less document cannot reach the renderer.
	rec, _ := do(t, s, "GET", "/api/v1/projects/"+p.ID+"/timeline", "")
	env := rec.Body.String()
	if rec2, _ := do(t, s, "PUT", "/api/v1/projects/"+p.ID+"/timeline", env); rec2.Code == http.StatusOK {
		t.Fatalf("saving the raw GET envelope was accepted; the document it stored has no tracks: %s", env)
	}
	// The stored document still measures the same after that attempt.
	if got := num(t, pacingFromGet(t, s, p), "shots"); got != 1 {
		t.Fatalf("shots = %v after the refused save, want the document unchanged (1)", got)
	}
}

func firstAssetID(t *testing.T, s *Server, p *storage.Project) string {
	t.Helper()
	assets, err := s.DB.ListAssets(t.Context(), p.ID)
	if err != nil || len(assets) == 0 {
		t.Fatalf("no assets: %v", err)
	}
	return assets[0].ID
}

// TestTheScriptReadsTheseWireKeys closes the other half of the contract. The
// browser is not in CI, so the only repeatable proof that the chip still reads the
// fields the server writes is this: the names appear on both sides of the wire, in
// the served bytes and in the shipped script. A rename on either end fails here —
// the tag mutation fails the wire test, a script-side rename fails this one.
func TestTheScriptReadsTheseWireKeys(t *testing.T) {
	src := staticFile(t, "static/app.js")
	for _, field := range []string{
		"p.shots", "p.mean_seconds", "p.median_seconds", "p.longest_seconds",
		"p.scored_shots", "p.hook_seconds",
	} {
		if !strings.Contains(src, field) {
			t.Errorf("app.js never reads %q, but the pacing object still writes it — the chip is showing a name that no longer exists", field)
		}
	}
}
