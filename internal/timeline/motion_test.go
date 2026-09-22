package timeline

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
)

// The framing plan crosses a boundary the Go types cannot protect: it is written
// by the style engine, stored as JSON in the project directory, edited by the web
// UI, and read by the renderer. So the wire names are the contract, and a rename
// that keeps the Go fields intact would still break every existing document.

func writeDocWithMotion(t *testing.T, clipJSON string) map[string]any {
	t.Helper()
	doc := `{"version":1,"project_id":"tl","canvas":{"width":640,"height":360,"fps":30},` +
		`"tracks":[{"id":"v0","kind":"video","clips":[` + clipJSON + `]}]}`
	var parsed map[string]any
	if err := json.Unmarshal([]byte(doc), &parsed); err != nil {
		t.Fatal(err)
	}
	clips := parsed["tracks"].([]any)[0].(map[string]any)["clips"].([]any)
	return clips[0].(map[string]any)
}

func TestMotionWireFormat(t *testing.T) {
	c := Clip{
		ID: "clip_1", AssetID: "a1", SourceStart: 0, SourceEnd: 4,
		TimelineStart: 0, Speed: 1, Volume: 1,
		Motion: &Motion{Zoom: 0.6, From: []float64{0.4, 0.5}, To: []float64{0.7, 0.5}},
	}
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	mv, ok := doc["motion"].(map[string]any)
	if !ok {
		t.Fatalf("no `motion` object on the wire, document was:\n%s", raw)
	}
	for _, key := range []string{"zoom", "from", "to"} {
		if _, has := mv[key]; !has {
			t.Fatalf("motion lost the %q key — the renderer and every stored document read it by name: %v", key, mv)
		}
	}
	if mv["zoom"].(float64) != 0.6 {
		t.Fatalf("zoom serialized as %v", mv["zoom"])
	}

	// A clip without a plan must not grow the key at all: an old document and a
	// new one have to stay indistinguishable when the feature is unused.
	plain, err := json.Marshal(Clip{ID: "c", AssetID: "a", SourceEnd: 1, Speed: 1})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), `"motion"`) {
		t.Fatalf("a clip with no motion wrote the key anyway: %s", plain)
	}

	// And decoding a document that predates the field still works.
	old := writeDocWithMotion(t, `{"id":"c1","asset_id":"a1","source_start":0,"source_end":4,"timeline_start":0,"speed":1,"volume":1}`)
	if _, has := old["motion"]; has {
		t.Fatal("the fixture document should not carry motion")
	}
}

func TestMotionValidated(t *testing.T) {
	base := func(m *Motion) *Timeline {
		return &Timeline{
			Version: Version, ProjectID: "tl",
			Canvas: Canvas{Width: 640, Height: 360, FPS: 30},
			Tracks: []Track{{ID: "v0", Kind: "video", Clips: []Clip{{
				ID: "c1", AssetID: "a1", SourceEnd: 4, Speed: 1, Volume: 1, Motion: m,
			}}}},
		}
	}
	for _, tc := range []struct {
		name string
		m    *Motion
		want string // "" = must validate
	}{
		{"nil plan", nil, ""},
		{"full frame", &Motion{Zoom: 1}, ""},
		{"punch-in", &Motion{Zoom: 0.5, From: []float64{0.5, 0.5}}, ""},
		{"drift", &Motion{Zoom: 0.7, From: []float64{0.2, 0.5}, To: []float64{0.8, 0.5}}, ""},
		{"zero zoom", &Motion{Zoom: 0}, "motion.zoom"},
		{"over-one zoom", &Motion{Zoom: 1.5}, "motion.zoom"},
		{"negative zoom", &Motion{Zoom: -1}, "motion.zoom"},
		{"NaN zoom", &Motion{Zoom: math.NaN()}, "non-finite|motion"},
		{"one coordinate", &Motion{Zoom: 0.5, From: []float64{0.5}}, "motion.from"},
		{"three coordinates", &Motion{Zoom: 0.5, To: []float64{0.5, 0.5, 0.5}}, "motion.to"},
		{"out of frame", &Motion{Zoom: 0.5, From: []float64{1.4, 0.5}}, "motion.from"},
		{"negative coordinate", &Motion{Zoom: 0.5, To: []float64{-0.1, 0.5}}, "motion.to"},
	} {
		err := base(tc.m).Validate(nil)
		if tc.want == "" {
			if err != nil {
				t.Errorf("%s: rejected: %v", tc.name, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: accepted, want a refusal naming %q", tc.name, tc.want)
			continue
		}
		var names []string
		if me, ok := err.(*MultiError); ok {
			names = me.Details()
		} else {
			names = []string{err.Error()}
		}
		joined := strings.Join(names, "\n")
		matched := false
		for _, alt := range strings.Split(tc.want, "|") {
			if strings.Contains(joined, alt) {
				matched = true
			}
		}
		if !matched {
			t.Errorf("%s: refusal did not name %q, said: %s", tc.name, tc.want, joined)
		}
	}
}

// The renderer is the only consumer, and it reads the plan through one accessor
// pair; this guards the field list itself against growing a second spelling.
func TestMotionFieldSet(t *testing.T) {
	typ := reflect.TypeOf(Motion{})
	want := map[string]bool{"Zoom": true, "From": true, "To": true}
	if typ.NumField() != len(want) {
		t.Fatalf("Motion has %d fields, want exactly %v", typ.NumField(), want)
	}
	for i := 0; i < typ.NumField(); i++ {
		if !want[typ.Field(i).Name] {
			t.Fatalf("unexpected Motion field %q (update the renderer and the UI together)", typ.Field(i).Name)
		}
	}
}
