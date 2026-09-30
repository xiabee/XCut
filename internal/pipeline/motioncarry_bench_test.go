package pipeline

import (
	"fmt"
	"testing"

	"github.com/xiabee/XCut/internal/timeline"
)

// benchCarryDocs builds a previous and a next document of n clips each (same
// windows — the deterministic-regeneration case), with every other previous
// clip carrying a hand pick.
func benchCarryDocs(n int) (*timeline.Timeline, *timeline.Timeline) {
	prevClips := make([]timeline.Clip, 0, n)
	nextClips := make([]timeline.Clip, 0, n)
	for i := 0; i < n; i++ {
		ss, se := float64(i)*10, float64(i)*10+8
		c := timeline.Clip{
			ID: "c" + fmt.Sprint(i), AssetID: "a",
			SourceStart: ss, SourceEnd: se, TimelineStart: float64(i) * 8, Speed: 1,
		}
		prev := c
		if i%2 == 0 {
			prev.Motion = &timeline.Motion{Zoom: 1.5, From: []float64{0.4, 0.5}, To: []float64{0.4, 0.5}}
			prev.Metadata = map[string]string{"framing": "roi"}
		}
		prevClips = append(prevClips, prev)
		nextClips = append(nextClips, c)
	}
	mk := func(clips []timeline.Clip) *timeline.Timeline {
		return &timeline.Timeline{
			Version: timeline.Version,
			Canvas:  timeline.Canvas{Width: 1920, Height: 1080, FPS: 30},
			Tracks:  []timeline.Track{{ID: "v1", Kind: "video", Clips: clips}},
		}
	}
	return mk(prevClips), mk(nextClips)
}

// BenchmarkCarryHandMotion is the cost WriteRegeneratedTimeline adds while
// publishing: one pass over the new clips against the old document's picks.
// The next document is reset in place between iterations, so the loop carries
// the carry and nothing else.
func BenchmarkCarryHandMotion(b *testing.B) {
	for _, n := range []int{27, 240} {
		b.Run(fmt.Sprintf("clips-%d", n), func(b *testing.B) {
			prev, next := benchCarryDocs(n)
			clips := &next.Tracks[0].Clips
			want := (n + 1) / 2
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if got := carryHandMotion(prev, next); got != want {
					b.Fatalf("carried %d, want %d", got, want)
				}
				for j := range *clips {
					(*clips)[j].Motion = nil
					(*clips)[j].Metadata = nil
				}
			}
		})
	}
}
