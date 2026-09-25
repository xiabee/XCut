package render

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xiabee/XCut/internal/media"
	"github.com/xiabee/XCut/internal/timeline"
)

// TestRenderParallelClipsVerified drives the worker pool explicitly: more
// ClipWorkers than clips exercises the clamp, and the reel must be identical
// in shape to the serial render's (same duration, verified by the renderer
// itself and re-probed here).
func TestRenderParallelClipsVerified(t *testing.T) {
	tools := requireTools(t)
	src := fixture(t)
	tl := twoClipTimeline(src)
	out := filepath.Join(t.TempDir(), "parallel.mp4")

	err := Render(context.Background(), tl, Options{
		Tools:       tools,
		TempDir:     t.TempDir(),
		ClipWorkers: 4,
	}, out)
	if err != nil {
		t.Fatal(err)
	}
	probe, err := media.ProbeFile(context.Background(), tools, out)
	if err != nil {
		t.Fatal(err)
	}
	if probe.DurationSec < 5.4 || probe.DurationSec > 6.6 {
		t.Fatalf("parallel render duration %.2f, want ~6", probe.DurationSec)
	}
}

// TestVideoFilterChainOrder pins the speed-motivated filter order: the fps
// resample runs before scale/pad (dropping frames is cheaper than resampling
// them), setpts (speed) stays in front of fps, and the motion crop lands
// between fps and scale.
func TestVideoFilterChainOrder(t *testing.T) {
	tl := twoClipTimeline("src.mp4")
	tl.Tracks[0].Clips[0].Motion = &timeline.Motion{Zoom: 1.2, From: []float64{0.3, 0.3}, To: []float64{0.7, 0.7}}

	c := tl.Tracks[0].Clips[0]
	c.Speed = 1.25
	got := videoFilterChain(tl, c, 3.0, [2]float64{0.5, 0.5})
	wantFps := "fps=15"
	wantMotion := "crop="
	wantScale := "scale=320:240"
	iFps := index(got, wantFps)
	iMotion := index(got, wantMotion)
	iScale := index(got, wantScale)
	if iFps < 0 || iMotion < 0 || iScale < 0 {
		t.Fatalf("chain missing stages: %q", got)
	}
	if !(iFps < iMotion && iMotion < iScale) {
		t.Fatalf("fps must precede crop must precede scale: %q", got)
	}
	if !strings.HasPrefix(got, "setpts=") {
		t.Fatalf("speed must be the first stage: %q", got)
	}
}

// fourClipTimeline fans one source into four adjacent ranges: enough
// independent normalization work that ClipWorkers=4 really overlaps.
func fourClipTimeline(src string) *timeline.Timeline {
	tl := twoClipTimeline(src)
	clips := make([]timeline.Clip, 4)
	for i := range clips {
		clips[i] = timeline.Clip{
			ID: "c" + string(rune('1'+i)), AssetID: "a", SourcePath: src,
			SourceStart: float64(i), SourceEnd: float64(i) + 2,
			TimelineStart: float64(i * 2), Speed: 1, Volume: 1,
		}
	}
	tl.Tracks[0].Clips = clips
	return tl
}

// TestClipParallelProgressIsDeliveredSerially pins the delivery contract the
// clip-parallel normalizer owes its callers: OnProgress never runs while
// another delivery is inside. The pipeline's monotonic-percentage closure
// reads and writes its `last` unsynchronized, so concurrent delivery raced
// exactly there — the gate's race subset caught it inside
// TestAutoAppliesScoreboardRegion. (Done counts may legitimately repeat
// across phases — (4,4) then (4,5) at the concat hand-off — so monotonicity
// is the consumer's `pct > last` concern, not the delivery's.)
//
// The overlap probe is deterministic rather than timing luck: the first
// callback parks until a second one enters (bounded, so the serialized case
// pays a fixed 2 s and moves on). Without the delivery lock the second
// entrant arrives immediately and the max-in-flight assert goes red.
func TestClipParallelProgressIsDeliveredSerially(t *testing.T) {
	tools := requireTools(t)
	src := fixture(t)
	tl := fourClipTimeline(src)
	out := filepath.Join(t.TempDir(), "par.mp4")

	var inFlight, maxInFlight, deliveries atomic.Int64
	var firstOnce sync.Once
	secondIn := make(chan struct{})

	err := Render(context.Background(), tl, Options{
		Tools: tools, TempDir: t.TempDir(), ClipWorkers: 4,
		OnProgress: func(done, total int) {
			cur := inFlight.Add(1)
			for {
				old := maxInFlight.Load()
				if cur <= old || maxInFlight.CompareAndSwap(old, cur) {
					break
				}
			}
			deliveries.Add(1)
			firstOnce.Do(func() {
				select {
				case <-secondIn: // a concurrent entrant — the race this test hunts
				case <-time.After(2 * time.Second): // serialized delivery: pay the bound, move on
				}
			})
			inFlight.Add(-1)
		},
	}, out)
	if err != nil {
		t.Fatal(err)
	}

	if maxInFlight.Load() != 1 {
		t.Errorf("OnProgress ran concurrently (max %d in flight), want serialized delivery", maxInFlight.Load())
	}
	if deliveries.Load() < 4 {
		t.Errorf("%d deliveries, want at least one per clip (4)", deliveries.Load())
	}
}

func index(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
