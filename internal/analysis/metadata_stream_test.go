package analysis

import (
	"fmt"
	"strings"
	"testing"
)

// TestMetadataCollectorMatchesBufferParser: the streaming sink must produce
// exactly what the buffer parser produces for the same output — including
// chunk boundaries landing mid-line — because the analyzers now stream and
// the buffer parser remains the reference.
func TestMetadataCollectorMatchesBufferParser(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&b, "frame:%d pts:%d pts_time:%.3f\n", i, i*1000, float64(i)*0.5)
		fmt.Fprintf(&b, "lavfi.signalstats.YDIF=%.6f\n", float64(i)/1000)
		fmt.Fprintf(&b, "lavfi.signalstats.UDIF=%.6f\n", float64(i)/2000)
		// lines the parser must skip: unwanted keys and malformed floats
		fmt.Fprintf(&b, "lavfi.signalstats.YBITDEPTH=8\n")
		if i%10 == 0 {
			fmt.Fprintf(&b, "lavfi.signalstats.YDIF=not-a-float\n")
		}
	}
	raw := []byte(b.String())

	want, err := parseMetadataPrintKeys(raw, yDifKey, uDifKey)
	if err != nil {
		t.Fatal(err)
	}

	// Feed the same bytes through the collector in awkward chunk sizes
	// (1, 7, 64, 4096 bytes) — every split point must parse identically.
	for _, size := range []int{1, 7, 64, 4096} {
		c := newMetadataCollector(yDifKey, uDifKey)
		for off := 0; off < len(raw); off += size {
			end := off + size
			if end > len(raw) {
				end = len(raw)
			}
			if err := c.sink(raw[off:end]); err != nil {
				t.Fatal(err)
			}
		}
		got, err := c.finish()
		if err != nil {
			t.Fatal(err)
		}
		if len(got[yDifKey]) != len(want[yDifKey]) || len(got[uDifKey]) != len(want[uDifKey]) {
			t.Fatalf("chunk %d: sample counts y=%d u=%d, want y=%d u=%d",
				size, len(got[yDifKey]), len(got[uDifKey]), len(want[yDifKey]), len(want[uDifKey]))
		}
		for i := range want[yDifKey] {
			if got[yDifKey][i] != want[yDifKey][i] {
				t.Fatalf("chunk %d: sample %d mismatch %+v vs %+v", size, i, got[yDifKey][i], want[yDifKey][i])
			}
		}
	}
}

// TestMetadataCollectorNoSamples: a stream with none of the wanted keys
// finishes empty (the analyzers turn that into their "no samples" error).
func TestMetadataCollectorNoSamples(t *testing.T) {
	c := newMetadataCollector(yDifKey)
	if err := c.sink([]byte("frame:0 pts:0 pts_time:0\nlavfi.astats.Overall.RMS_level=-12.5\n")); err != nil {
		t.Fatal(err)
	}
	got, err := c.finish()
	if err != nil {
		t.Fatal(err)
	}
	if len(got[yDifKey]) != 0 {
		t.Fatalf("unwanted key leaked in: %v", got)
	}
}
