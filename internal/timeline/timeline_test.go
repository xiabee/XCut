package timeline

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"testing"
)

func fmtInt(n int) string { return fmt.Sprintf("%d", n) }

func validTimeline() *Timeline {
	return &Timeline{
		Version: Version,
		Canvas:  Canvas{Width: 1920, Height: 1080, FPS: 30},
		Tracks: []Track{
			{
				ID:   "v1",
				Kind: "video",
				Clips: []Clip{
					{ID: "c1", AssetID: "a1", SourceStart: 0, SourceEnd: 2, TimelineStart: 0, Speed: 1, Volume: 1},
					{ID: "c2", AssetID: "a1", SourceStart: 3, SourceEnd: 5, TimelineStart: 2, Speed: 1, Volume: 1,
						Transition: &Transition{Type: "fade", Duration: 0.3}},
				},
			},
		},
	}
}

func lookupBoth() MediaLookup {
	return FixedLookup(map[string]float64{"a1": 10, "a2": 4})
}

func TestValidateOK(t *testing.T) {
	if err := validTimeline().Validate(lookupBoth()); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
	if err := validTimeline().Validate(nil); err != nil {
		t.Fatalf("expected valid without lookup, got %v", err)
	}
}

func TestValidateFailures(t *testing.T) {
	cases := map[string]func(*Timeline){
		"bad version":      func(tl *Timeline) { tl.Version = 99 },
		"odd width":        func(tl *Timeline) { tl.Canvas.Width = 1921 },
		"zero fps":         func(tl *Timeline) { tl.Canvas.FPS = 0 },
		"nan fps":          func(tl *Timeline) { tl.Canvas.FPS = math.NaN() },
		"huge fps":         func(tl *Timeline) { tl.Canvas.FPS = 1000 },
		"neg start":        func(tl *Timeline) { tl.Tracks[0].Clips[0].TimelineStart = -1 },
		"empty range":      func(tl *Timeline) { tl.Tracks[0].Clips[0].SourceEnd = 0 },
		"reversed range":   func(tl *Timeline) { tl.Tracks[0].Clips[0].SourceEnd = 1; tl.Tracks[0].Clips[0].SourceStart = 2 },
		"zero speed":       func(tl *Timeline) { tl.Tracks[0].Clips[0].Speed = 0 },
		"volume too high":  func(tl *Timeline) { tl.Tracks[0].Clips[0].Volume = 1.5 },
		"nan timeline":     func(tl *Timeline) { tl.Tracks[0].Clips[0].TimelineStart = math.Inf(1) },
		"overlap":          func(tl *Timeline) { tl.Tracks[0].Clips[1].TimelineStart = 1 },
		"dup clip id":      func(tl *Timeline) { tl.Tracks[0].Clips[1].ID = "c1" },
		"empty asset":      func(tl *Timeline) { tl.Tracks[0].Clips[0].AssetID = "" },
		"bad transition":   func(tl *Timeline) { tl.Tracks[0].Clips[1].Transition.Type = "swirl" },
		"trans too long":   func(tl *Timeline) { tl.Tracks[0].Clips[1].Transition.Duration = 5 },
		"past media end":   func(tl *Timeline) { tl.Tracks[0].Clips[0].SourceEnd = 50 },
		"unknown asset":    func(tl *Timeline) { tl.Tracks[0].Clips[0].AssetID = "ghost" },
		"bad track kind":   func(tl *Timeline) { tl.Tracks[0].Kind = "subtitles" },
		"dup clip id two":  func(tl *Timeline) { tl.Tracks[0].Clips[0].ID = "" },
	}
	for name, mutate := range cases {
		tl := validTimeline()
		mutate(tl)
		err := tl.Validate(lookupBoth())
		if err == nil {
			t.Errorf("%s: expected validation failure", name)
			continue
		}
		var me *MultiError
		if !asMulti(err, &me) || len(me.Details()) == 0 {
			t.Errorf("%s: expected details", name)
		}
	}
}

func asMulti(err error, target **MultiError) bool {
	me, ok := err.(*MultiError)
	if ok {
		*target = me
	}
	return ok
}

func TestJSONRoundTrip(t *testing.T) {
	tl := validTimeline()
	b, err := json.Marshal(tl)
	if err != nil {
		t.Fatal(err)
	}
	var back Timeline
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	b2, err := json.Marshal(&back)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != string(b2) {
		t.Fatalf("round trip not lossless:\n%s\n%s", b, b2)
	}
	if !strings.Contains(string(b), `"version":1`) {
		t.Fatal("version field missing")
	}
}

// TestPropertyValidTimelinesPass generates randomized-but-valid timelines and
// asserts validation always passes and invariants hold.
func TestPropertyValidTimelinesPass(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for iter := 0; iter < 300; iter++ {
		tl := &Timeline{Version: Version, Canvas: Canvas{Width: 640, Height: 360, FPS: 30}}
		tr := Track{ID: "v1", Kind: "video"}
		cursor := 0.0
		for i := 0; i < 1+rng.Intn(8); i++ {
			dur := 0.5 + rng.Float64()*3
			src := rng.Float64() * (8 - dur)
			tr.Clips = append(tr.Clips, Clip{
				ID: "c" + fmtInt(i), AssetID: "a1",
				SourceStart:   src,
				SourceEnd:     src + dur,
				TimelineStart: cursor,
				Speed:         1,
				Volume:        rng.Float64(),
			})
			cursor += dur
		}
		tl.Tracks = []Track{tr}
		if err := tl.Validate(FixedLookup(map[string]float64{"a1": 8})); err != nil {
			t.Fatalf("iteration %d: valid timeline rejected: %v", iter, err)
		}
		for _, c := range tr.Clips {
			if !(c.Duration() > 0) || c.SourceStart < 0 || c.SourceEnd > 8 {
				t.Fatalf("iteration %d: invariant broken: %+v", iter, c)
			}
		}
		if tl.Duration() <= 0 {
			t.Fatalf("iteration %d: total duration non-positive", iter)
		}
	}
}
