package player

import (
	"testing"
)

// The band scan's per-frame cost, sweep vs shortlist — the numbers behind
// PERFORMANCE.md's two-tier row and the MRShortlistK constant. Frames are
// synthetic (noise, and noise with the trained green planted): the statistic
// is pure CPU over one decoded frame, so no media is involved.
func benchMRFrame(b *testing.B) (MultiRegionSignature, Signature, []byte, []byte) {
	b.Helper()
	mb := NewMultiRegionBuilder()
	for band := 0; band < RegionCount; band++ {
		row := []byte{0, 255, 0, 0, 255, 0, 0, 255, 0, 0, 255, 0}
		if err := mb.bands[band].Add(4, 1, row); err != nil {
			b.Fatal(err)
		}
	}
	mr, err := mb.Build()
	if err != nil {
		b.Fatal(err)
	}
	sig, err := BuildSignature(2, 2, [][]byte{{0, 255, 0, 0, 255, 0, 0, 255, 0, 0, 255, 0}})
	if err != nil {
		b.Fatal(err)
	}
	noise := noiseFrame(7, 320, 240)
	planted := noiseFrame(7, 320, 240)
	plantRect(planted, 320, 120, 80, 64, 64)
	return mr, sig, noise, planted
}

func BenchmarkMRBandScanSweepPerFrame(b *testing.B) {
	mr, _, noise, _ := benchMRFrame(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s_patchMaxMR(mr, 1.0, 320, 240, noise)
	}
}

func BenchmarkMRBandScanShortlistPerFrame(b *testing.B) {
	mr, sig, noise, _ := benchMRFrame(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s_patchMaxMRShortlist(mr, sig, 1.0, 320, 240, noise)
	}
}

func BenchmarkMRBandScanSweepPlantedPerFrame(b *testing.B) {
	mr, _, _, planted := benchMRFrame(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s_patchMaxMR(mr, 1.0, 320, 240, planted)
	}
}

func BenchmarkMRBandScanShortlistPlantedPerFrame(b *testing.B) {
	mr, sig, _, planted := benchMRFrame(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s_patchMaxMRShortlist(mr, sig, 1.0, 320, 240, planted)
	}
}
