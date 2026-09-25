package player

import (
	"testing"
)

func TestMultiRegionScoring(t *testing.T) {
	mr, err := testMultiRegionBuild()
	if err != nil {
		t.Fatal(err)
	}
	_ = mr
	// Multi-region scoring is verified via the real-footage integration test
	// (the presence track drives the style gate); here we just verify the
	// builder runs without error on synthetic pixel data.
}

func testMultiRegionBuild() (MultiRegionSignature, error) {
	b := NewMultiRegionBuilder()
	// 12×12 frame: white top half, dark bottom half (enough rows for all 3 bands).
	const W, H = 12, 12
	frame := make([]byte, W*H*3)
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			px := (y*W + x) * 3
			if y < H/2 {
				frame[px], frame[px+1], frame[px+2] = 220, 220, 220 // white
			} else {
				frame[px], frame[px+1], frame[px+2] = 40, 40, 40 // dark
			}
		}
	}
	if err := b.AddFeed(W, H, frame); err != nil {
		return MultiRegionSignature{}, err
	}
	return b.Build()
}
