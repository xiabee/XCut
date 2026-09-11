package analysis

import (
	"math"
	"testing"
)

func TestParseMetadataPrintKeys(t *testing.T) {
	out := []byte(`frame:0    pts:0         pts_time:0
lavfi.signalstats.YDIF=0
frame:0    pts:0         pts_time:0
lavfi.signalstats.UDIF=3
frame:0    pts:0         pts_time:0
lavfi.signalstats.VDIF=112
frame:1    pts:125       pts_time:0.5
lavfi.signalstats.YDIF=40
frame:1    pts:125       pts_time:0.5
lavfi.signalstats.UDIF=1
frame:1    pts:125       pts_time:0.5
lavfi.signalstats.VDIF=159
`)
	perKey, err := parseMetadataPrintKeys(out, yDifKey, uDifKey, vDifKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(perKey[yDifKey]) != 2 || len(perKey[uDifKey]) != 2 || len(perKey[vDifKey]) != 2 {
		t.Fatalf("per-key counts: Y=%d U=%d V=%d, want 2/2/2",
			len(perKey[yDifKey]), len(perKey[uDifKey]), len(perKey[vDifKey]))
	}
	if perKey[vDifKey][1].T != 0.5 || perKey[vDifKey][1].V != 159 {
		t.Fatalf("V sample: %+v", perKey[vDifKey][1])
	}

	// Single-key wrapper (ROI analyzer path).
	y, err := parseMetadataPrint(out, yDifKey)
	if err != nil || len(y) != 2 {
		t.Fatalf("single-key parse: %d, %v", len(y), err)
	}
	if _, err := parseMetadataPrint(out, "lavfi.signalstats.YBITDEPTH"); err == nil {
		t.Fatal("missing key must error")
	}
}

func TestCombineChannelDiffs(t *testing.T) {
	y := []Sample{{T: 0, V: 25.5}, {T: 0.5, V: 40}, {T: 1, V: 0}}  // 0.1, 0.157, 0
	u := []Sample{{T: 0, V: 224}, {T: 0.5, V: 1}}                  // 1.0 (clamps), 0.0045
	v := []Sample{{T: 0, V: 112}, {T: 0.5, V: 159}, {T: 1, V: 50}} // 0.5, 0.71, 0.223

	got := combineChannelDiffs(y, u, v)
	if len(got) != 3 {
		t.Fatalf("samples = %d, want 3 (Y list is the backbone)", len(got))
	}
	want := []float64{1.0, 159.0 / 224.0, 50.0 / 224.0}
	for i, s := range got {
		if math.Abs(s.V-want[i]) > 1e-9 {
			t.Fatalf("sample %d: V = %v, want %v", i, s.V, want[i])
		}
		if s.T != float64(i)*0.5 {
			t.Fatalf("sample %d: T = %v", i, s.T)
		}
	}

	// U/V absent entirely: pure luma behavior (0..1 normalized).
	luma := combineChannelDiffs(y, nil, nil)
	if luma[0].V != 0.1 || luma[1].V != 40.0/255.0 || luma[2].V != 0 {
		t.Fatalf("luma-only merge: %+v", luma)
	}
}
