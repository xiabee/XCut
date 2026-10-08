package player

import "testing"

// TestSigHashMRNamespacesTheModelKind pins the cache-routing half of the
// multi-region wiring: the band model's hash is its own namespace (mr:
// prefix), deterministic, and a malformed band list claims nothing.
func TestSigHashMRNamespacesTheModelKind(t *testing.T) {
	mr := MultiRegionSignature{Bands: [RegionCount][]float64{
		make([]float64, HueBins*SatBins*ValBins),
		make([]float64, HueBins*SatBins*ValBins),
		make([]float64, HueBins*SatBins*ValBins),
	}}
	mr.Bands[1][3] = 0.5

	h := SigHashMR(mr.Bands[:])
	if len(h) == 0 || h[:4] != "mr3:" {
		t.Fatalf("band hash %q does not carry the model-kind prefix", h[:12])
	}
	if again := SigHashMR(mr.Bands[:]); again != h {
		t.Fatal("the band hash is not deterministic")
	}
	// The single-histogram hash of the same middle band must differ — one
	// namespace each, so neither scan can satisfy the other's cache key.
	if SigHash(mr.Bands[1]) == h {
		t.Fatal("a band model and its single band share one hash namespace")
	}
	if SigHashMR(nil) != "" || SigHashMR(mr.Bands[:2]) != "" {
		t.Fatal("a malformed band list produced a hash instead of claiming nothing")
	}
}
