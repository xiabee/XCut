package timeline

import (
	"reflect"
	"strconv"
	"testing"
)

// gridDoc builds a one-track document whose single clip carries source range
// [ss, se] at speed s, laid at output position ts, and states the given grid.
func gridDoc(bpm, phase, ss, se, ts, speed float64) *Timeline {
	return &Timeline{
		Version: Version,
		Canvas:  Canvas{Width: 640, Height: 360, FPS: 30},
		Tracks: []Track{{ID: "v1", Kind: "video", Clips: []Clip{{
			ID: "c0", AssetID: "a", SourceStart: ss, SourceEnd: se,
			TimelineStart: ts, Speed: speed,
		}}}},
		Metadata: map[string]string{
			MetaBeatBPM:   strconv.FormatFloat(bpm, 'f', 4, 64),
			MetaBeatPhase: strconv.FormatFloat(phase, 'f', 4, 64),
		},
	}
}

func TestBeatTicksMapsThroughTheClipsOwnWindow(t *testing.T) {
	// A 0.5 s grid phased at 0.25; the clip plays source 10.0–13.0 at output 2.0,
	// speed 1. Its beats are 10.25…12.75; each lands 0.25 s past the clip's own
	// source start, so output positions are 2.25…4.75 — the trim's offset and the
	// clip's place in the reel both move the marks.
	got := gridDoc(120, 0.25, 10.0, 13.0, 2.0, 1).BeatTicks()
	if got == nil {
		t.Fatal("a stated grid answered null; the ruler would draw nothing")
	}
	if got.BPM != 120 {
		t.Errorf("BPM = %v, want 120", got.BPM)
	}
	want := []float64{2.25, 2.75, 3.25, 3.75, 4.25, 4.75}
	if !reflect.DeepEqual(got.Ticks, want) {
		t.Errorf("ticks = %v, want %v (beats mapped through trim and output offset)", got.Ticks, want)
	}
}

func TestBeatTicksHonoursSpeed(t *testing.T) {
	// The same clip at 2×: a beat every 0.5 source-second becomes one every
	// 0.25 output-seconds, and the clip's own end beat (source 4.0) lands on the
	// output end at 2.0. A mapping that ignores speed puts them twice as far
	// apart as the viewer experiences.
	got := gridDoc(120, 0.0, 0.0, 4.0, 0.0, 2).BeatTicks()
	want := []float64{0, 0.25, 0.5, 0.75, 1.0, 1.25, 1.5, 1.75, 2.0}
	if !reflect.DeepEqual(got.Ticks, want) {
		t.Errorf("ticks = %v, want %v (source seconds halved by 2× speed)", got.Ticks, want)
	}
}

func TestBeatTicksExcludesBeatsOutsideThePlayedMaterial(t *testing.T) {
	// Grid every 1 s; the clip plays source 2.4–3.6, so only the beat at 3.0
	// falls inside. Including the neighbours would draw marks over material the
	// reel never shows.
	got := gridDoc(60, 0.0, 2.4, 3.6, 0.0, 1).BeatTicks()
	want := []float64{0.6}
	if !reflect.DeepEqual(got.Ticks, want) {
		t.Errorf("ticks = %v, want %v (only the beat inside [2.4, 3.6])", got.Ticks, want)
	}
}

func TestBeatTicksDeduplicatesSharedBeats(t *testing.T) {
	// Three clips: the first two cut from the same source range and laid at the
	// same output position (a repeat), the third elsewhere. The shared beat at
	// source 5.0 maps to output 0.5 twice and must draw once — four raw
	// mappings, three marks.
	tl := &Timeline{
		Version: Version,
		Canvas:  Canvas{Width: 640, Height: 360, FPS: 30},
		Tracks: []Track{{ID: "v1", Kind: "video", Clips: []Clip{
			{ID: "c0", AssetID: "a", SourceStart: 4.5, SourceEnd: 5.5, TimelineStart: 0, Speed: 1},
			{ID: "c1", AssetID: "a", SourceStart: 4.5, SourceEnd: 5.5, TimelineStart: 0, Speed: 1},
			{ID: "c2", AssetID: "a", SourceStart: 0.0, SourceEnd: 1.0, TimelineStart: 2.0, Speed: 1},
		}}},
		Metadata: map[string]string{MetaBeatBPM: "60", MetaBeatPhase: "0"},
	}
	got := tl.BeatTicks()
	want := []float64{0.5, 2.0, 3.0}
	if !reflect.DeepEqual(got.Ticks, want) {
		t.Errorf("ticks = %v, want %v (the shared 0.5 beat once, the third clip's own beats beside it)", got.Ticks, want)
	}
}

func TestBeatTicksSortedAcrossClips(t *testing.T) {
	// Clips stored out of order (hook first): the ruler reads left to right in
	// output time, so the ticks must come back sorted regardless of clip order.
	tl := &Timeline{
		Version: Version,
		Canvas:  Canvas{Width: 640, Height: 360, FPS: 30},
		Tracks: []Track{{ID: "v1", Kind: "video", Clips: []Clip{
			{ID: "c1", AssetID: "a", SourceStart: 9.0, SourceEnd: 10.0, TimelineStart: 1.0, Speed: 1},
			{ID: "c0", AssetID: "a", SourceStart: 0.0, SourceEnd: 1.0, TimelineStart: 0.0, Speed: 1},
		}}},
		Metadata: map[string]string{MetaBeatBPM: "60", MetaBeatPhase: "0"},
	}
	got := tl.BeatTicks()
	want := []float64{0, 1, 2}
	if !reflect.DeepEqual(got.Ticks, want) {
		t.Errorf("ticks = %v, want %v (output order, not clip order)", got.Ticks, want)
	}
}

func TestBeatTicksRefusesADocumentThatStatesNoGrid(t *testing.T) {
	for name, md := range map[string]map[string]string{
		"no metadata":        nil,
		"tempo only":         {MetaBeatBPM: "120"},
		"phase only":         {MetaBeatPhase: "0.25"},
		"unparsable tempo":   {MetaBeatBPM: "fast", MetaBeatPhase: "0.25"},
		"non-positive tempo": {MetaBeatBPM: "0", MetaBeatPhase: "0.25"},
		"unparsable phase":   {MetaBeatBPM: "120", MetaBeatPhase: "somewhere"},
		"negative phase":     {MetaBeatBPM: "120", MetaBeatPhase: "-0.5"},
		"wrong-key tempo":    {MetaMusicBPM: "120", MetaBeatPhase: "0.25"},
	} {
		tl := gridDoc(120, 0.25, 0, 10, 0, 1)
		tl.Metadata = md
		if got := tl.BeatTicks(); got != nil {
			t.Errorf("%s: BeatTicks = %+v, want nil — a phase this document does not state must not be invented", name, got)
		}
	}
}

func TestBeatTicksHandlesAnEmptyDocumentAndZeroSpeedClips(t *testing.T) {
	if got := (*Timeline)(nil).BeatTicks(); got != nil {
		t.Errorf("nil timeline answered %+v, want nil", got)
	}
	// A clip whose speed is unset (0) contributes nothing — Duration() already
	// refuses it — and a document left with no contributions still reports the
	// tempo with an empty tick list rather than pretending there is no grid.
	tl := gridDoc(60, 0, 0, 1, 0, 0)
	got := tl.BeatTicks()
	if got == nil || got.BPM != 60 || len(got.Ticks) != 0 {
		t.Errorf("zero-speed clip: got %+v, want BPM 60 with no ticks (grid stated, none drawable)", got)
	}
}

func TestBeatTicksIsBounded(t *testing.T) {
	// A hand-built 100 000 s document under a 120 BPM grid promises 200 001
	// beats; the cap is a work bound as much as a wire bound, so enumeration
	// stops at it instead of building the whole list first (and the test stays
	// microseconds-cheap, which an enumerate-then-truncate shape would not be).
	tl := gridDoc(120, 0, 0, 100000, 0, 1)
	got := tl.BeatTicks()
	if got == nil || len(got.Ticks) != maxBeatTicks {
		t.Fatalf("ticks = %d, want the %d cap", len(got.Ticks), maxBeatTicks)
	}
	// The survivors are the earliest beats, not a sample: half-second steps
	// from zero, ending at the cap's own edge.
	if got.Ticks[0] != 0 || got.Ticks[1] != 0.5 ||
		got.Ticks[len(got.Ticks)-1] != float64(maxBeatTicks-1)*0.5 {
		t.Errorf("the capped list is not the earliest beats: first %v last %v",
			got.Ticks[0], got.Ticks[len(got.Ticks)-1])
	}
}
