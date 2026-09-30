package pipeline

import (
	"testing"

	"github.com/xiabee/XCut/internal/timeline"
)

func roiPlan() *timeline.Motion {
	return &timeline.Motion{Zoom: 1.5, From: []float64{0.4, 0.5}, To: []float64{0.4, 0.5}}
}

func carryDoc(asset string, ss, se float64, motion *timeline.Motion, framing string) timeline.Clip {
	c := timeline.Clip{
		ID: "c", AssetID: asset, SourceStart: ss, SourceEnd: se,
		TimelineStart: 0, Speed: 1, Motion: motion,
	}
	if framing != "" {
		c.Metadata = map[string]string{"framing": framing}
	}
	return c
}

func oneClipDoc(c timeline.Clip) *timeline.Timeline {
	return &timeline.Timeline{
		Version: timeline.Version,
		Canvas:  timeline.Canvas{Width: 640, Height: 360, FPS: 30},
		Tracks:  []timeline.Track{{ID: "v1", Kind: "video", Clips: []timeline.Clip{c}}},
	}
}

func TestCarryHandMotionOntoTheSameWindow(t *testing.T) {
	prev := oneClipDoc(carryDoc("a", 10, 18, roiPlan(), "roi"))
	next := oneClipDoc(carryDoc("a", 10, 18, nil, ""))
	if n := carryHandMotion(prev, next); n != 1 {
		t.Fatalf("carried %d plans on an identical window; want 1", n)
	}
	got := next.Tracks[0].Clips[0]
	if got.Motion == nil || got.Motion.Zoom != 1.5 {
		t.Fatalf("the pick did not follow the clip: %+v", got.Motion)
	}
	if got.Metadata["framing"] != "roi" {
		t.Fatalf("framing claim = %q, want the pick's own claim carried beside the plan", got.Metadata["framing"])
	}
}

func TestCarrySurvivesABeatSnapNudge(t *testing.T) {
	// A regenerated end moved by 0.2 s on an 8 s window: IoU 17.8/18.2 ≈ 0.978.
	// The plan aims at the same material, so the nudge must not orphan the pick.
	prev := oneClipDoc(carryDoc("a", 10, 18, roiPlan(), "roi"))
	next := oneClipDoc(carryDoc("a", 10, 18.2, nil, ""))
	if n := carryHandMotion(prev, next); n != 1 {
		t.Fatalf("carried %d plans over a 0.2 s end nudge; want 1", n)
	}
}

func TestCarryRefusesAWindowThatMoved(t *testing.T) {
	// The regeneration re-cut different material from the same asset: the plan
	// aims where the viewer no longer is, so nothing follows.
	prev := oneClipDoc(carryDoc("a", 10, 18, roiPlan(), "roi"))
	next := oneClipDoc(carryDoc("a", 30, 38, nil, ""))
	if n := carryHandMotion(prev, next); n != 0 {
		t.Fatalf("carried %d plans onto re-cut material; want 0", n)
	}
	if next.Tracks[0].Clips[0].Motion != nil {
		t.Fatal("a plan was attached to a clip whose material moved")
	}
}

func TestCarryIoUFloorIsHonestAtTheBoundary(t *testing.T) {
	// 100/111 ≈ 0.9009 carries; 100/112 ≈ 0.8929 does not — the floor is a
	// real line, not a rubber band.
	prev := oneClipDoc(carryDoc("a", 0, 100, roiPlan(), "roi"))
	inside := oneClipDoc(carryDoc("a", 0, 111, nil, ""))
	if n := carryHandMotion(prev, inside); n != 1 {
		t.Fatalf("carried %d at IoU 0.9009; want 1", n)
	}
	outside := oneClipDoc(carryDoc("a", 0, 112, nil, ""))
	if n := carryHandMotion(prev, outside); n != 0 {
		t.Fatalf("carried %d at IoU 0.8929; want 0", n)
	}
}

func TestCarryYieldsToTheStylesOwnPlan(t *testing.T) {
	// Regeneration re-runs the style: where the builder framed the clip itself,
	// a pick from the previous document does not override it — and is not
	// counted as carried.
	prev := oneClipDoc(carryDoc("a", 10, 18, roiPlan(), "roi"))
	next := oneClipDoc(carryDoc("a", 10, 18, &timeline.Motion{Zoom: 2}, "punch_in"))
	if n := carryHandMotion(prev, next); n != 0 {
		t.Fatalf("carried %d over the style's own plan; want 0", n)
	}
	if got := next.Tracks[0].Clips[0].Motion.Zoom; got != 2 {
		t.Fatalf("the style's zoom %v was overwritten by the pick", got)
	}
}

func TestCarryNeverCrossesAssets(t *testing.T) {
	prev := oneClipDoc(carryDoc("a", 10, 18, roiPlan(), "roi"))
	next := oneClipDoc(carryDoc("b", 10, 18, nil, ""))
	if n := carryHandMotion(prev, next); n != 0 {
		t.Fatalf("carried %d across assets; want 0", n)
	}
}

func TestCarryTakesTheBestMatchAndDropsTheRest(t *testing.T) {
	better := carryDoc("a", 10, 18, &timeline.Motion{Zoom: 1.2}, "roi")
	worse := carryDoc("a", 10.5, 18.2, &timeline.Motion{Zoom: 3}, "drift")
	prev := &timeline.Timeline{
		Version: timeline.Version,
		Canvas:  timeline.Canvas{Width: 640, Height: 360, FPS: 30},
		Tracks:  []timeline.Track{{ID: "v1", Kind: "video", Clips: []timeline.Clip{worse, better}}},
	}
	// New window 10–18: the "better" clip matches exactly (IoU 1), "worse"
	// 10.5–18.2 overlaps 7.5 of a 9.7 union ≈ 0.773 — under the floor anyway.
	next := oneClipDoc(carryDoc("a", 10, 18, nil, ""))
	carryHandMotion(prev, next)
	if got := next.Tracks[0].Clips[0].Motion.Zoom; got != 1.2 {
		t.Fatalf("carried zoom %v, want the exact-window pick's 1.2", got)
	}

	// Both above the floor: the higher IoU wins. Window 10–17.9: exact = 7.9/7.9
	// (IoU 1) versus 10.5–18.2 → 7.4/8.2 ≈ 0.902.
	prevTwo := oneClipDoc(carryDoc("a", 10.5, 18.2, &timeline.Motion{Zoom: 3}, "drift"))
	prevTwo.Tracks[0].Clips = append(prevTwo.Tracks[0].Clips, carryDoc("a", 10, 18, &timeline.Motion{Zoom: 1.2}, "roi"))
	nextTwo := oneClipDoc(carryDoc("a", 10, 17.9, nil, ""))
	carryHandMotion(prevTwo, nextTwo)
	if got := nextTwo.Tracks[0].Clips[0].Motion.Zoom; got != 1.2 {
		t.Fatalf("carried zoom %v, want the higher-IoU pick's 1.2", got)
	}
}

func TestCarryWithoutAFramingClaimForgesNone(t *testing.T) {
	// A pick can exist without its framing word (hand-written documents): the
	// plan follows, the claim is not invented.
	prev := oneClipDoc(carryDoc("a", 10, 18, roiPlan(), ""))
	next := oneClipDoc(carryDoc("a", 10, 18, nil, ""))
	if n := carryHandMotion(prev, next); n != 1 {
		t.Fatalf("carried %d; want 1", n)
	}
	if _, has := next.Tracks[0].Clips[0].Metadata["framing"]; has {
		t.Fatal("a framing claim was forged for a pick that carried none")
	}
}

func TestCarryHandlesNilAndEmptyDocuments(t *testing.T) {
	next := oneClipDoc(carryDoc("a", 10, 18, nil, ""))
	if n := carryHandMotion(nil, next); n != 0 {
		t.Fatalf("nil prev carried %d", n)
	}
	if n := carryHandMotion(next, nil); n != 0 {
		t.Fatalf("nil next carried %d", n)
	}
	empty := &timeline.Timeline{Version: timeline.Version}
	if n := carryHandMotion(empty, next); n != 0 {
		t.Fatalf("an empty prev carried %d", n)
	}
}

func TestSpanIoU(t *testing.T) {
	if got := spanIoU(10, 18, 10, 18); got != 1 {
		t.Errorf("identical spans: %v, want 1", got)
	}
	if got := spanIoU(10, 18, 20, 28); got != 0 {
		t.Errorf("disjoint spans: %v, want 0", got)
	}
	if got := spanIoU(10, 18, 12, 16); got != 0.5 {
		t.Errorf("nested spans: %v, want 0.5 (4 of 8)", got)
	}
}

func TestCarryTieGoesToTheEarlierClip(t *testing.T) {
	// Two picks on the same exact window: the earlier clip's plan is the one
	// that follows (>= keeps the first match; a > here would silently hand the
	// tie to whichever clip the document lists last).
	first := carryDoc("a", 10, 18, &timeline.Motion{Zoom: 1}, "roi")
	second := carryDoc("a", 10, 18, &timeline.Motion{Zoom: 2}, "drift")
	prev := &timeline.Timeline{
		Version: timeline.Version,
		Canvas:  timeline.Canvas{Width: 640, Height: 360, FPS: 30},
		Tracks:  []timeline.Track{{ID: "v1", Kind: "video", Clips: []timeline.Clip{first, second}}},
	}
	next := oneClipDoc(carryDoc("a", 10, 18, nil, ""))
	carryHandMotion(prev, next)
	if got := next.Tracks[0].Clips[0].Motion.Zoom; got != 1 {
		t.Fatalf("the tie went to zoom %v; want the earlier clip's 1", got)
	}
}
