package timeline

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/testmedia"
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
		"bad version":     func(tl *Timeline) { tl.Version = 99 },
		"odd width":       func(tl *Timeline) { tl.Canvas.Width = 1921 },
		"zero fps":        func(tl *Timeline) { tl.Canvas.FPS = 0 },
		"nan fps":         func(tl *Timeline) { tl.Canvas.FPS = math.NaN() },
		"huge fps":        func(tl *Timeline) { tl.Canvas.FPS = 1000 },
		"neg start":       func(tl *Timeline) { tl.Tracks[0].Clips[0].TimelineStart = -1 },
		"empty range":     func(tl *Timeline) { tl.Tracks[0].Clips[0].SourceEnd = 0 },
		"reversed range":  func(tl *Timeline) { tl.Tracks[0].Clips[0].SourceEnd = 1; tl.Tracks[0].Clips[0].SourceStart = 2 },
		"zero speed":      func(tl *Timeline) { tl.Tracks[0].Clips[0].Speed = 0 },
		"tiny speed":      func(tl *Timeline) { tl.Tracks[0].Clips[0].Speed = 0.001 },
		"subnormal speed": func(tl *Timeline) { tl.Tracks[0].Clips[0].Speed = 1e-320 },
		"clip over cap":   func(tl *Timeline) { tl.Tracks[0].Clips[0].SourceEnd = 100000; tl.Tracks[0].Clips[0].Speed = 0.1 },
		"volume too high": func(tl *Timeline) { tl.Tracks[0].Clips[0].Volume = 1.5 },
		"nan timeline":    func(tl *Timeline) { tl.Tracks[0].Clips[0].TimelineStart = math.Inf(1) },
		"overlap":         func(tl *Timeline) { tl.Tracks[0].Clips[1].TimelineStart = 1 },
		"dup clip id":     func(tl *Timeline) { tl.Tracks[0].Clips[1].ID = "c1" },
		"empty asset":     func(tl *Timeline) { tl.Tracks[0].Clips[0].AssetID = "" },
		"bad transition":  func(tl *Timeline) { tl.Tracks[0].Clips[1].Transition.Type = "swirl" },
		"trans too long":  func(tl *Timeline) { tl.Tracks[0].Clips[1].Transition.Duration = 5 },
		"past media end":  func(tl *Timeline) { tl.Tracks[0].Clips[0].SourceEnd = 50 },
		"unknown asset":   func(tl *Timeline) { tl.Tracks[0].Clips[0].AssetID = "ghost" },
		"bad track kind":  func(tl *Timeline) { tl.Tracks[0].Kind = "subtitles" },
		"dup clip id two": func(tl *Timeline) { tl.Tracks[0].Clips[0].ID = "" },
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

// TestValidateRejectsGap: cut/fade joins must be flush — the renderer joins
// clips back-to-back, so a placement gap can never be honored and must be
// rejected at validation time with a gap-naming error.
func TestValidateRejectsGap(t *testing.T) {
	dir := t.TempDir()
	pathA, err := testmedia.Generate(dir, "a.mp4", testmedia.DefaultFixture()[:2], 320, 240, 10)
	if err != nil {
		t.Fatal(err)
	}
	clip := func(id string, timelineStart float64) Clip {
		return Clip{
			ID: id, AssetID: id, SourcePath: pathA,
			SourceStart: 0, SourceEnd: 2, Speed: 1, Volume: 1,
			TimelineStart: timelineStart,
		}
	}
	gapped := &Timeline{
		Version: Version,
		Canvas:  Canvas{Width: 320, Height: 240, FPS: 10},
		Tracks: []Track{{ID: "v1", Kind: "video", Clips: []Clip{
			clip("c1", 0), clip("c2", 5), // 3s gap
		}}},
	}
	err = gapped.Validate(nil)
	if err == nil {
		t.Fatal("gap must be rejected")
	}
	var multi *MultiError
	if errors.As(err, &multi) {
		found := false
		for _, d := range multi.Details() {
			if strings.Contains(d, "gap") {
				found = true
			}
		}
		if !found {
			t.Fatalf("gap error must name the gap: %v", err)
		}
	}

	// A flush joint stays valid.
	flush := &Timeline{
		Version: Version,
		Canvas:  Canvas{Width: 320, Height: 240, FPS: 10},
		Tracks: []Track{{ID: "v1", Kind: "video", Clips: []Clip{
			clip("c1", 0), clip("c2", 2),
		}}},
	}
	if err := flush.Validate(nil); err != nil {
		t.Fatalf("flush join must stay valid: %v", err)
	}
}

// TestValidateRejectsOverlongTimeline: many sub-cap clips must not compose
// an over-cap timeline (the per-clip cap alone would not catch it).
func TestValidateRejectsOverlongTimeline(t *testing.T) {
	tl := validTimeline()
	// Each clip is 10000s at speed 0.1... keep speeds legal: 1000s source
	// range at speed 0.1 is 10000s playback; five of them ≈ 14h < 24h, so
	// use twenty ≈ 55h total.
	tl.Tracks[0].Clips = nil
	for i := 0; i < 20; i++ {
		tl.Tracks[0].Clips = append(tl.Tracks[0].Clips, Clip{
			ID: fmt.Sprintf("c%d", i), AssetID: "a1",
			SourceStart: 0, SourceEnd: 1000, TimelineStart: float64(i) * 10000,
			Speed: 0.1, Volume: 1,
		})
	}
	err := tl.Validate(FixedLookup(map[string]float64{"a1": 1000}))
	if err == nil {
		t.Fatal("55h timeline must be rejected")
	}
	for _, d := range err.(*MultiError).Details() {
		if strings.Contains(d, "render cap") {
			return
		}
	}
	t.Fatalf("error must name the cap: %v", err)
}
