package style

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/xiabee/XCut/internal/event"
	"github.com/xiabee/XCut/internal/timeline"
)

func testPreset() *Preset {
	return &Preset{
		Name:            "test",
		Title:           "Test",
		Version:         1,
		Canvas:          timeline.Canvas{Width: 640, Height: 360, FPS: 30},
		TargetDuration:  10,
		MinClipDuration: 1,
		MaxClipDuration: 5,
		Scoring:         Scoring{Motion: 0.5, Audio: 0.3, Duration: 0.2},
		EventConfig:     event.DefaultConfig(),
		Transition:      Transition{Type: "cut"},
		Audio:           Audio{Gain: 1},
	}
}

func TestParseEmbeddedPresets(t *testing.T) {
	// Shipped := the set this test names, validated := every file the embed
	// actually carries. Reading the list from `embedded` is the point: presets are
	// embedded by wildcard, so a hand-copied name list here would keep passing
	// green while a newly added preset went unvalidated.
	shipped := map[string]bool{
		"generic_highlight": true, "generic_xfade": true, "badminton_highlight": true,
		"ktv_mv": true, "beat_shortform": true, "sports_vertical": true,
	}
	for name := range shipped {
		if _, ok := embedded[name]; !ok {
			t.Errorf("preset %q is no longer embedded — a style disappeared", name)
		}
	}
	for name := range embedded {
		if !shipped[name] {
			t.Errorf("embedded preset %q is not in this test's list; add it deliberately, not silently", name)
			continue
		}
		p, err := Load(name)
		if err != nil {
			t.Fatalf("load %s: %v", name, err)
		}
		if p.Name != name {
			t.Fatalf("name mismatch: %s (embedded as %s)", p.Name, name)
		}
	}
	if _, err := Load("nope"); err == nil {
		t.Fatal("expected unknown style error")
	}
	if _, err := Load("../evil"); err == nil {
		t.Fatal("expected invalid style name error")
	}
}

func TestParseRejectsUnknownFields(t *testing.T) {
	bad := []byte(`{"name":"x","version":1,"traget_duration":30}`)
	if _, err := Parse(bad); err == nil {
		t.Fatal("expected unknown-field rejection")
	}
}

func TestParseRejectsBadValues(t *testing.T) {
	base := map[string]any{
		"name": "x", "title": "X", "version": 1,
		"canvas":          map[string]any{"width": 640, "height": 360, "fps": 30.0},
		"target_duration": 30.0, "min_clip_duration": 1.0, "max_clip_duration": 6.0,
		"scoring":      map[string]any{"motion": 0.4, "audio": 0.4, "duration": 0.2},
		"event_config": map[string]any{"cut_threshold": 0.3, "motion_floor": 0.05, "silence_db": -40.0, "merge_gap": 1.0, "min_duration": 1.0},
		"transition":   map[string]any{"type": "cut", "duration": 0.0},
		"audio":        map[string]any{"gain": 0.9},
	}
	build := func(m map[string]any) []byte {
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	if _, err := Parse(build(base)); err != nil {
		t.Fatalf("base must parse: %v", err)
	}

	mutate := func(key string, val any) map[string]any {
		m := map[string]any{}
		for k, v := range base {
			m[k] = v
		}
		m[key] = val
		return m
	}

	cases := map[string]map[string]any{
		"version":    {"version": 2},
		"fps":        {"canvas": map[string]any{"width": 640, "height": 360, "fps": 0.0}},
		"width":      {"canvas": map[string]any{"width": 641, "height": 360, "fps": 30.0}},
		"gain":       {"audio": map[string]any{"gain": 1.9}},
		"min>max":    {"min_clip_duration": 99.0},
		"transition": {"transition": map[string]any{"type": "swirl", "duration": 0.0}},
		"bad events": {"event_config": map[string]any{"cut_threshold": -1.0, "motion_floor": 0.05, "silence_db": -40.0, "merge_gap": 1.0, "min_duration": 1.0}},
	}
	for name, patch := range cases {
		if _, err := Parse(build(mutate(name2key(name), patch[name]))); err == nil {
			t.Errorf("mutation %s should fail", name)
		}
	}
}

func name2key(name string) string {
	// mutation key doubles as the top-level key to patch
	return name
}

func TestLoadFromFileOverride(t *testing.T) {
	dir := t.TempDir()
	content := []byte(`{
		"name": "my_style",
		"title": "My Style",
		"version": 1,
		"canvas": {"width": 640, "height": 360, "fps": 30},
		"target_duration": 30,
		"min_clip_duration": 1,
		"max_clip_duration": 6,
		"scoring": {"motion": 0.4, "audio": 0.4, "duration": 0.2},
		"event_config": {"cut_threshold": 0.3, "motion_floor": 0.05, "silence_db": -40, "merge_gap": 1, "min_duration": 1},
		"transition": {"type": "cut", "duration": 0},
		"audio": {"gain": 0.9}
	}`)
	if err := os.WriteFile(filepath.Join(dir, "my_style.json"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := Load("my_style", dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Audio.Gain != 0.9 {
		t.Fatalf("gain = %v", p.Audio.Gain)
	}

	bad := []byte(`{"bogus": true}`)
	if err := os.WriteFile(filepath.Join(dir, "bad_style.json"), bad, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load("bad_style", dir); err == nil {
		t.Fatal("expected invalid override to fail")
	}
}

func seg(start, end, motion, db float64) event.Segment {
	return event.Segment{Start: start, End: end, Score: 0.5, MeanMotion: motion, MeanAudioDB: db}
}

func TestBuildDeterministicAndSane(t *testing.T) {
	p := testPreset()
	items := []AssetEvents{
		{Asset: AssetInfo{ID: "a1", Path: "a.mp4", DurationSec: 60}, Segments: []event.Segment{
			seg(2, 10, 0.25, -18),
			seg(14, 18, 0.10, -30),
			seg(22, 40, 0.28, -12),
			seg(50, 58, 0.20, -16),
		}},
	}
	tl1, err := Build(p, "prj", items)
	if err != nil {
		t.Fatal(err)
	}
	tl2, err := Build(p, "prj", items)
	if err != nil {
		t.Fatal(err)
	}
	b1, _ := json.Marshal(tl1)
	b2, _ := json.Marshal(tl2)
	if string(b1) != string(b2) {
		t.Fatal("timeline build not deterministic")
	}
	if tl1.Duration() > float64(p.TargetDuration)+0.01 {
		t.Fatalf("duration %v exceeds target", tl1.Duration())
	}
	for i := 1; i < len(tl1.Tracks[0].Clips); i++ {
		prev := tl1.Tracks[0].Clips[i-1]
		cur := tl1.Tracks[0].Clips[i]
		if cur.TimelineStart < prev.TimelineStart+prev.Duration()-1e-6 {
			t.Fatal("clips overlap on track")
		}
	}
}

func TestBuildTargetDurationRespected(t *testing.T) {
	p := testPreset()
	p.TargetDuration = 6
	items := []AssetEvents{
		{Asset: AssetInfo{ID: "a1", Path: "a.mp4", DurationSec: 120}, Segments: []event.Segment{
			seg(0, 30, 0.3, -12),
			seg(40, 70, 0.3, -12),
			seg(80, 110, 0.3, -12),
		}},
	}
	tl, err := Build(p, "prj", items)
	if err != nil {
		t.Fatal(err)
	}
	d := tl.Duration()
	if d > float64(p.TargetDuration)+0.01 {
		t.Fatalf("duration %v exceeds target %v", d, p.TargetDuration)
	}
	if d < float64(p.TargetDuration-p.MaxClipDuration) {
		t.Fatalf("duration %v far below target %v", d, p.TargetDuration)
	}
}

func TestBuildSourceRangeWithinMedia(t *testing.T) {
	p := testPreset()
	// Pathological: the segment starts past the end of the media, so no
	// placement of any clip window leaves anything usable ⇒ Build must reject
	// loudly. (A segment that merely *overruns* the end is the mild case below
	// and is legitimately salvaged, so it cannot stand in for this one.)
	items := []AssetEvents{
		{Asset: AssetInfo{ID: "a1", Path: "a.mp4", DurationSec: 5}, Segments: []event.Segment{
			seg(8, 20, 0.3, -12),
		}},
	}
	if _, err := Build(p, "prj", items); err == nil {
		t.Fatal("expected rejection when clipping leaves nothing")
	}

	// Mild overrun: clip window is clamped to the real media end and the
	// resulting timeline validates against the true duration.
	items[0].Asset.DurationSec = 10
	tl, err := Build(p, "prj", items)
	if err != nil {
		t.Fatal(err)
	}
	if err := tl.Validate(timeline.FixedLookup(map[string]float64{"a1": 10})); err != nil {
		t.Fatalf("built timeline invalid: %v", err)
	}
	for _, c := range tl.Tracks[0].Clips {
		if c.SourceEnd > 10 {
			t.Fatalf("clip exceeds media: %+v", c)
		}
	}
}

func TestBuildRejectsAllFiltered(t *testing.T) {
	p := testPreset()
	p.MinClipDuration = 5
	items := []AssetEvents{
		{Asset: AssetInfo{ID: "a1", Path: "a.mp4", DurationSec: 60}, Segments: []event.Segment{
			seg(0, 2, 0.3, -12),
		}},
	}
	if _, err := Build(p, "prj", items); err == nil {
		t.Fatal("expected rejection when all events filtered")
	}
}

func TestScoreSegmentWeights(t *testing.T) {
	// Factors are relative within the candidate set (absolute caps saturate
	// on real footage — every chunk exceeds 12 hits — flattening the rank to
	// "earliest first"). With motion-dominant weights the higher-motion
	// candidate must outscore the other regardless of the absolute values,
	// and a component identical across candidates stays neutral (1.0).
	p := testPreset()
	p.Scoring = Scoring{Motion: 1, Audio: 1, Duration: 0}
	rels := relativize([]rawFactors{
		{motion: 0.30, audio: -12, duration: 10, hits: 60, density: 2.5},
		{motion: 0.15, audio: -12, duration: 10, hits: 60, density: 2.5},
	})
	got := rels[0].weighted(p)
	want := p.Scoring.Motion*1.0 + p.Scoring.Audio*1.0 // motion spread 1.0, audio identical -> 1.0
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("top candidate score = %v, want %v", got, want)
	}
	if !(rels[0].weighted(p) > rels[1].weighted(p)) {
		t.Fatalf("higher motion must outrank: %v vs %v", rels[0].weighted(p), rels[1].weighted(p))
	}
}

func TestBuildDiversitySuppressesDuplicates(t *testing.T) {
	p := testPreset()
	p.TargetDuration = 30
	p.Diversity = Diversity{MinGap: 5, MaxOverlapIoU: 0.5}
	asset := AssetInfo{ID: "a1", Path: "a.mp4", DurationSec: 120}
	items := []AssetEvents{{Asset: asset, Segments: []event.Segment{
		seg(10, 20, 0.30, -6),  // strongest: a 10s moment
		seg(12, 22, 0.29, -6),  // near-clone of the same moment
		seg(50, 55, 0.20, -12), // different moment, weaker
	}}}
	tl, err := Build(p, "prj", items)
	if err != nil {
		t.Fatal(err)
	}
	clips := tl.Tracks[0].Clips
	if len(clips) != 2 {
		t.Fatalf("diversity picked %d clips, want 2 (twin suppressed): %+v", len(clips), clips)
	}
	for i := 0; i < len(clips); i++ {
		for j := i + 1; j < len(clips); j++ {
			inter := math.Min(clips[i].SourceEnd, clips[j].SourceEnd) - math.Max(clips[i].SourceStart, clips[j].SourceStart)
			if inter > 0 {
				t.Fatalf("selected clips overlap: %+v %+v", clips[i], clips[j])
			}
		}
	}
}

func TestBuildWithoutDiversityKeepsTwins(t *testing.T) {
	p := testPreset()
	p.TargetDuration = 30
	asset := AssetInfo{ID: "a1", Path: "a.mp4", DurationSec: 120}
	items := []AssetEvents{{Asset: asset, Segments: []event.Segment{
		seg(10, 20, 0.30, -6),
		seg(12, 22, 0.29, -6),
	}}}
	tl, err := Build(p, "prj", items)
	if err != nil {
		t.Fatal(err)
	}
	if len(tl.Tracks[0].Clips) != 2 {
		t.Fatalf("without diversity both twins must be picked, got %d", len(tl.Tracks[0].Clips))
	}
}

func TestBuildExplainsSelection(t *testing.T) {
	p := testPreset()
	asset := AssetInfo{ID: "a1", Path: "a.mp4", DurationSec: 60}
	items := []AssetEvents{{Asset: asset, Segments: []event.Segment{
		seg(5, 15, 0.30, -6),
	}}}
	tl, err := Build(p, "prj", items)
	if err != nil {
		t.Fatal(err)
	}
	clip := tl.Tracks[0].Clips[0]
	md := clip.Metadata
	if md == nil {
		t.Fatal("clip has no metadata")
	}
	score, err := strconv.ParseFloat(md["score"], 64)
	if err != nil || score <= 0 || score > 1.5 {
		t.Errorf("metadata score %q not parseable into (0,1.5]", md["score"])
	}
	bd := md["score_breakdown"]
	for _, want := range []string{"motion", "audio", "duration", "total"} {
		if !strings.Contains(bd, want) {
			t.Errorf("breakdown %q missing %q", bd, want)
		}
	}
	if md["reason"] == "" {
		t.Error("metadata reason empty")
	}
}

func TestPresetDiversityValidation(t *testing.T) {
	p := testPreset()
	p.Diversity = Diversity{MinGap: -1}
	if err := p.Validate(); err == nil {
		t.Error("negative min_gap must be rejected")
	}
	p.Diversity = Diversity{MaxOverlapIoU: 1.5}
	if err := p.Validate(); err == nil {
		t.Error("max_overlap_iou > 1 must be rejected")
	}
	p.Diversity = Diversity{MinGap: 3, MaxOverlapIoU: 0.5}
	if err := p.Validate(); err != nil {
		t.Errorf("valid diversity rejected: %v", err)
	}
	// The floor is only meaningful between 0 and the ceiling it lives inside:
	// a window that must hold 3 clips where another may hold 2 has no answer,
	// and a floor without windows has nothing to floor.
	p.Diversity = Diversity{MaxPerWindow: 2, Phases: 5, MinPerWindow: 1}
	if err := p.Validate(); err != nil {
		t.Errorf("a floor inside the ceiling rejected: %v", err)
	}
	p.Diversity = Diversity{MaxPerWindow: 2, Phases: 5, MinPerWindow: 3}
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "max_per_window") {
		t.Errorf("a floor above the ceiling must name the ceiling it contradicts, got %v", err)
	}
	p.Diversity = Diversity{MaxPerWindow: 2, MinPerWindow: 1}
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "phases") {
		t.Errorf("a floor with no windows must say so, got %v", err)
	}
	p.Diversity = Diversity{MaxPerWindow: 2, Phases: 5, MinPerWindow: -1}
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "min_per_window") {
		t.Errorf("a negative floor must be refused by name, got %v", err)
	}
}

func TestPresetMotionROIParseAndAutoWire(t *testing.T) {
	b, err := Parse([]byte(`{
		"name": "roi_style", "title": "ROI", "version": 1,
		"canvas": {"width": 640, "height": 360, "fps": 30},
		"target_duration": 30, "min_clip_duration": 1, "max_clip_duration": 6,
		"scoring": {"motion": 0.5, "audio": 0.3, "duration": 0.2},
		"event_config": {"cut_threshold": 0.3, "motion_floor": 0.05, "silence_db": -40, "merge_gap": 1, "min_duration": 1},
		"transition": {"type": "cut", "duration": 0},
		"audio": {"gain": 1},
		"motion_roi": {"x": 0.1, "y": 0.1, "w": 0.6, "h": 0.6}
	}`))
	if err != nil {
		t.Fatalf("roi preset: %v", err)
	}
	if b.EventConfig.MotionTrack != "frame_diff_roi" {
		t.Fatalf("motion_track not auto-wired: %q", b.EventConfig.MotionTrack)
	}
	extra := b.Analyzers()
	if len(extra) != 1 {
		t.Fatalf("expected 1 style analyzer, got %d", len(extra))
	}
	if got := extra[0].Name(); got != "frame_diff_roi[x0.1000_y0.1000_w0.6000_h0.6000]" {
		t.Fatalf("analyzer name %q", got)
	}

	// Explicit motion_track is respected, not overwritten.
	c, err := Parse([]byte(`{
		"name": "roi_style2", "title": "ROI2", "version": 1,
		"canvas": {"width": 640, "height": 360, "fps": 30},
		"target_duration": 30, "min_clip_duration": 1, "max_clip_duration": 6,
		"scoring": {"motion": 0.5, "audio": 0.3, "duration": 0.2},
		"event_config": {"cut_threshold": 0.3, "motion_floor": 0.05, "silence_db": -40, "merge_gap": 1, "min_duration": 1, "motion_track": "frame_diff"},
		"transition": {"type": "cut", "duration": 0},
		"audio": {"gain": 1},
		"motion_roi": {"x": 0, "y": 0, "w": 0.5, "h": 0.5}
	}`))
	if err != nil {
		t.Fatalf("roi preset 2: %v", err)
	}
	if c.EventConfig.MotionTrack != "frame_diff" {
		t.Fatalf("explicit motion_track overwritten: %q", c.EventConfig.MotionTrack)
	}

	// Out-of-range ROI rejected.
	bad := testPreset()
	bad.MotionROI = &MotionROI{X: 0.8, Y: 0, W: 0.5, H: 0.5}
	if err := bad.Validate(); err == nil {
		t.Fatal("x+w>1 ROI must be rejected")
	}
}

func TestBuildXfadePlacement(t *testing.T) {
	p := testPreset()
	p.TargetDuration = 30
	p.Transition = Transition{Type: "xfade", Duration: 1}
	asset := AssetInfo{ID: "a1", Path: "a.mp4", DurationSec: 60}
	items := []AssetEvents{{Asset: asset, Segments: []event.Segment{
		seg(0, 4, 0.20, -10),
		seg(10, 14, 0.21, -10),
		seg(20, 24, 0.22, -10),
	}}}
	tl, err := Build(p, "prj", items)
	if err != nil {
		t.Fatal(err)
	}
	clips := tl.Tracks[0].Clips
	if len(clips) != 3 {
		t.Fatalf("clips %d, want 3", len(clips))
	}
	// xfade 1s: starts at 0, 3, 6 → timeline duration 10 (Σ 12 − 2).
	for i, want := range []float64{0, 3, 6} {
		if clips[i].TimelineStart != want {
			t.Errorf("clip %d starts %g, want %g", i, clips[i].TimelineStart, want)
		}
	}
	if math.Abs(tl.Duration()-10) > 1e-9 {
		t.Errorf("timeline duration %g, want 10", tl.Duration())
	}
	// The last clip carries no outgoing transition.
	if clips[2].Transition != nil {
		t.Error("last clip must not carry a transition")
	}
	if clips[0].Transition.Type != "xfade" || clips[0].Transition.Duration != 1 {
		t.Errorf("unexpected transition: %+v", clips[0].Transition)
	}
}

// TestNamesSortedAndHonest: Names is built for UI pickers, so it must be
// sorted (map iteration is randomized per process — an unsorted list flips
// the default-selected style across serve restarts) and must not list names
// Load would reject.
func TestNamesSortedAndHonest(t *testing.T) {
	dir := t.TempDir()
	valid := []byte(`{
		"name": "aaa_custom",
		"title": "AAA",
		"version": 1,
		"canvas": {"width": 640, "height": 360, "fps": 30},
		"target_duration": 30,
		"min_clip_duration": 1,
		"max_clip_duration": 6,
		"scoring": {"motion": 0.4, "audio": 0.4, "duration": 0.2},
		"event_config": {"cut_threshold": 0.3, "motion_floor": 0.05, "silence_db": -40, "merge_gap": 1, "min_duration": 1},
		"transition": {"type": "cut", "duration": 0},
		"audio": {"gain": 1}
	}`)
	if err := os.WriteFile(filepath.Join(dir, "aaa_custom.json"), valid, 0o644); err != nil {
		t.Fatal(err)
	}
	// A file whose name Load would never accept must not be advertised.
	if err := os.WriteFile(filepath.Join(dir, "My Style.json"), valid, 0o644); err != nil {
		t.Fatal(err)
	}

	names := Names(dir)
	if len(names) == 0 {
		t.Fatal("no names returned")
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] >= names[i] {
			t.Fatalf("names not sorted: %v", names)
		}
	}
	found := false
	for _, n := range names {
		if n == "aaa_custom" {
			found = true
		}
		if n == "My Style" {
			t.Errorf("Names advertises %q which Load rejects", n)
		}
		if !validName(n) {
			t.Errorf("Names advertises invalid name %q", n)
		}
	}
	if !found {
		t.Errorf("extra-dir preset missing from names: %v", names)
	}
	// Embedded presets are always present.
	embeddedSeen := false
	for _, n := range names {
		if _, ok := embedded[n]; ok {
			embeddedSeen = true
		}
	}
	if !embeddedSeen {
		t.Errorf("embedded presets missing from names: %v", names)
	}
}

// The clip window sits at the start of its segment, not its middle: rally
// chunks are cut on quiet valleys, so the beginning of a segment is where the
// action is, while the middle of a long chunk can be the pause after a point.
func TestBuildAnchorsClipAtSegmentStart(t *testing.T) {
	p := testPreset()
	p.TargetDuration = 5
	p.MaxClipDuration = 5
	items := []AssetEvents{
		{Asset: AssetInfo{ID: "a1", Path: "a.mp4", DurationSec: 120}, Segments: []event.Segment{
			seg(10, 40, 0.5, -12),
		}},
	}
	tl, err := Build(p, "prj", items)
	if err != nil {
		t.Fatal(err)
	}
	c := tl.Tracks[0].Clips[0]
	if c.SourceStart != 10 {
		t.Fatalf("source start = %v, want the segment start 10 (middle-trim would give 27.5)", c.SourceStart)
	}
	if c.SourceEnd-c.SourceStart > p.MaxClipDuration+0.001 {
		t.Fatalf("clip %v exceeds max duration", c.SourceEnd-c.SourceStart)
	}
}

// diversity.max_per_window is what keeps a highlight covering the match: the
// local rules (min_gap / overlap) only push picks apart, so one loud stretch can
// still take the whole reel.
func TestBuildWindowCapSpreadsPicks(t *testing.T) {
	items := []AssetEvents{
		{Asset: AssetInfo{ID: "a1", Path: "a.mp4", DurationSec: 120}, Segments: []event.Segment{
			seg(2, 12, 0.90, -5),   // phase 0 (0-20s of 120s/6): the two best
			seg(6, 16, 0.85, -6),   // window 0
			seg(80, 90, 0.30, -25), // window 4: clearly weaker
		}},
	}
	build := func(capN int) []timeline.Clip {
		t.Helper()
		p := testPreset()
		p.TargetDuration = 10
		p.MaxClipDuration = 5
		p.Diversity = Diversity{MaxPerWindow: capN, Phases: 6}
		tl, err := Build(p, "prj", items)
		if err != nil {
			t.Fatal(err)
		}
		return tl.Tracks[0].Clips
	}

	uncapped := build(0)
	inW0 := 0
	for _, c := range uncapped {
		if c.SourceStart < 20 {
			inW0++
		}
	}
	if inW0 < 2 {
		t.Fatalf("expected the cap-less reel to concentrate in window 0, got %d of %d clips", inW0, len(uncapped))
	}

	capped := build(1)
	perWindow := map[int]int{}
	for _, c := range capped {
		perWindow[int(c.SourceStart/20)]++
	}
	for w, n := range perWindow {
		if n > 1 {
			t.Fatalf("window %d holds %d clips, cap is 1", w, n)
		}
	}
	if len(perWindow) < 2 {
		t.Fatalf("cap did not spread the picks: windows %v", perWindow)
	}
	// The weaker late moment must now be present: that is the point of the rule.
	if perWindow[4] != 1 {
		t.Errorf("window 4 not represented (windows %v) — a capped reel should reach the closing phase", perWindow)
	}
}
