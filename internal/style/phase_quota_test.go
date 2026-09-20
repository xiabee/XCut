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
