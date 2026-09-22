package style

import (
	"math"
	"testing"

	"github.com/xiabee/XCut/internal/event"
)

// A 603 s match measured the bug this pins: with a fixed 5-phase × 2-per-window
// quota, asking for a 240 s reel stopped at 10 clips / 80 s and silently kept
// 160 s of budget unused. The quota now buys more windows instead of a looser
// discipline, so a longer reel covers more of the source while the spread rule
// still holds inside every window.
func TestPhaseQuotaScalesWithReelBudget(t *testing.T) {
	p := testPreset()
	p.MaxClipDuration = 8
	p.MinClipDuration = 2
	p.Diversity = Diversity{MinGap: 4, MaxOverlapIoU: 0.4, MaxPerWindow: 2, Phases: 5}
	asset := AssetInfo{ID: "a1", Path: "a.mp4", DurationSec: 600}
	var segs []event.Segment
	for i := 0; i < 40; i++ {
		st := float64(4 + i*14)
		segs = append(segs, seg(st, st+12, 0.4, -8))
	}
	items := []AssetEvents{{Asset: asset, Segments: segs}}

	p.TargetDuration = 60
	short, err := Build(p, "prj", items)
	if err != nil {
		t.Fatal(err)
	}
	shortClips := short.Tracks[0].Clips
	// The configured quota is exactly what a 60 s reel needs, so it must not
	// bind yet — and the old ceiling must still be respected here. That is the
	// no-regression half of the claim: default behaviour is untouched.
	if len(shortClips) == 0 || len(shortClips) > 10 {
		t.Fatalf("60s reel took %d clips, want 1..10 (5 phases x 2)", len(shortClips))
	}

	p.TargetDuration = 240
	long, err := Build(p, "prj", items)
	if err != nil {
		t.Fatal(err)
	}
	longClips := long.Tracks[0].Clips
	if len(longClips) <= len(shortClips) {
		t.Fatalf("a 4x longer budget produced %d clips, not more than %d", len(longClips), len(shortClips))
	}
	if len(longClips) <= 10 {
		t.Fatalf("still capped at the fixed quota: %d clips for a 240s target", len(longClips))
	}
	// Spread must survive the growth: at most MaxPerWindow per scaled window.
	room := int(math.Ceil(p.TargetDuration / p.MaxClipDuration))
	phases := math.Max(float64(p.Diversity.Phases), math.Ceil(float64(room)/float64(p.Diversity.MaxPerWindow)))
	win := asset.DurationSec / phases
	perWindow := map[int]int{}
	for _, c := range longClips {
		perWindow[int(c.SourceStart/win)]++
	}
	for w, n := range perWindow {
		if n > p.Diversity.MaxPerWindow {
			t.Fatalf("window %d holds %d clips, want at most %d (spread rule lost)", w, n, p.Diversity.MaxPerWindow)
		}
	}
}

// The ceiling has a hole the owner's match showed: a 60 s reel took eight clips
// out of a 603 s match and left ten consecutive rallies unrepresented, because
// "at most 2 per window" is satisfied by two clips in window 0 and nothing in
// window 4. A floor is the other half of the same rule — before a window gets
// its second clip, the windows still empty get their first one.
func TestPhaseFloorTakesAnEmptyWindowBeforeDoublingUp(t *testing.T) {
	p := testPreset()
	p.MaxClipDuration = 8
	p.MinClipDuration = 2
	p.TargetDuration = 40 // five clips at the ceiling of 8 s each
	p.Diversity = Diversity{MinGap: 1, MaxOverlapIoU: 0.4, MaxPerWindow: 2, Phases: 5}
	asset := AssetInfo{ID: "a1", Path: "a.mp4", DurationSec: 600}

	// Six strong segments in the first window (0..120 s) and one merely good
	// segment in each later window: the ceiling lets a window take two, so a
	// five-clip reel spends four of them on the two richest openings and never
	// reaches the closing phases. That is the shape the floor is for.
	var segs []event.Segment
	for i := 0; i < 6; i++ {
		st := float64(5 + i*18)
		segs = append(segs, seg(st, st+10, 0.9, -6))
	}
	for i := 0; i < 4; i++ {
		st := float64(130 + i*18)
		segs = append(segs, seg(st, st+10, 0.8, -7))
	}
	for i, st := range []float64{250, 370, 490} {
		segs = append(segs, seg(st, st+10, 0.5-float64(i)*0.01, -14))
	}
	items := []AssetEvents{{Asset: asset, Segments: segs}}

	// Positive control first: without a floor this reel must leave a phase empty
	// while doubling up elsewhere, or the assertion below would pass on a reel
	// that spread anyway.
	noFloor, err := Build(p, "prj", items)
	if err != nil {
		t.Fatal(err)
	}
	// 600 s / 5 phases = a 120 s window.
	const win = 120.0
	clustered := map[int]int{}
	for _, c := range noFloor.Tracks[0].Clips {
		clustered[int(c.SourceStart/win)]++
	}
	if len(clustered) >= len(noFloor.Tracks[0].Clips) {
		t.Fatalf("control broken: the floorless reel already covered every clip's own window %v — this fixture no longer shows the bug", clustered)
	}

	p.Diversity.MinPerWindow = 1
	floored, err := Build(p, "prj", items)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int]int{}
	for _, c := range floored.Tracks[0].Clips {
		seen[int(c.SourceStart/win)]++
	}
	for w := 0; w < p.Diversity.Phases; w++ {
		if seen[w] == 0 {
			t.Fatalf("floor left window %d empty anyway: %v (floorless reel: %v)", w, seen, clustered)
		}
	}
	// The floor must not cost the reel: same budget, same number of clips.
	if len(floored.Tracks[0].Clips) != len(noFloor.Tracks[0].Clips) {
		t.Fatalf("floored reel has %d clips against %d without a floor", len(floored.Tracks[0].Clips), len(noFloor.Tracks[0].Clips))
	}
	// ...and it must not throw the top-ranked moment away either: window 0 keeps
	// its best clip, it just stops taking a second before the tail has a first.
	if seen[0] == 0 {
		t.Fatalf("floor dropped window 0 entirely: %v", seen)
	}
}
