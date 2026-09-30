package timeline

import (
	"fmt"
	"testing"
)

// benchDoc builds a reel of n clips laid back to back (8 s each, trimmed from
// a long source), stating the given grid — the shape a real document has when
// the GET derives its beat object.
func benchDoc(n int, bpm, phase float64) *Timeline {
	clips := make([]Clip, 0, n)
	var at float64
	for i := 0; i < n; i++ {
		clips = append(clips, Clip{
			ID: "c" + fmt.Sprint(i), AssetID: "a",
			SourceStart: float64(i) * 10, SourceEnd: float64(i)*10 + 8,
			TimelineStart: at, Speed: 1,
		})
		at += 8
	}
	return &Timeline{
		Version: Version,
		Canvas:  Canvas{Width: 1920, Height: 1080, FPS: 30},
		Tracks:  []Track{{ID: "v1", Kind: "video", Clips: clips}},
		Metadata: map[string]string{
			MetaBeatBPM:   fmt.Sprintf("%.4f", bpm),
			MetaBeatPhase: fmt.Sprintf("%.4f", phase),
		},
	}
}

// BenchmarkBeatTicks is the cost a timeline GET pays for the ruler's beat
// object on a realistic reel (27 shots of 8 s, a 120 BPM grid → 432 ticks)
// and on the pathological one (a grid dense enough to reach the 4 096 cap,
// where enumeration stops early instead of walking every beat).
func BenchmarkBeatTicks(b *testing.B) {
	reel := benchDoc(27, 120, 0)
	b.Run("reel-27clips-432ticks", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if bt := reel.BeatTicks(); bt == nil || len(bt.Ticks) != 433 {
				b.Fatalf("ticks = %d, want 433 (27 clips x 17 beats minus the 26 shared boundaries)", len(bt.Ticks))
			}
		}
	})
	huge := benchDoc(27, 28800, 0) // 240 beats/s → the cap is the bound
	b.Run("reel-27clips-capped-4096", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if bt := huge.BeatTicks(); bt == nil || len(bt.Ticks) != maxBeatTicks-1 {
				b.Fatalf("ticks = %d, want cap-1 (the raw cap is hit, then the shared boundary beat dedupes)", len(bt.Ticks))
			}
		}
	})
}
