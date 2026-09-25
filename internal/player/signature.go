// Package player implements the person filter without AI: a color-signature
// model the user seeds by drawing a small rect around themselves in one frame,
// and a per-frame backprojection score that answers "how strongly does this
// frame show the person" — deterministic, offline, and explainable.
//
// Why color, and what it is not: on a badminton court the discriminating
// channel is the shirt (white / light blue) against the floor (green) and the
// walls (blue), so hue+saturation+value carries a real signal. The model is a
// histogram, not a tracker — it does not follow a specific body, and two
// players in the same shirt are indistinguishable to it. It answers the
// operator's actual question ("which segments show ME playing") on follow-cam
// and fixed-wide footage alike, with zero model downloads.
package player

import (
	"fmt"
	"math"

	"github.com/xiabee/XCut/internal/xcerr"
)

// Histogram geometry. Hue carries the shirt-vs-court discrimination; fine hue
// bins with coarse S/V keep the model robust to shading while still telling
// white (low S, high V) from green floor (H≈60-90) and blue walls (H≈140-160).
const (
	HueBins = 18
	SatBins = 3
	ValBins = 3
)

// MatchThreshold is the backprojection score a frame needs (0..1) to count as
// "the signature is present in this frame".
const MatchThreshold = 0.35

// Signature is the flattened HSV histogram of the spot region, L1-normalized.
type Signature struct {
	Bins []float64
}

// Builder accumulates pixels across streamed frames and normalizes once at
// the end. Zero-value ready; Build returns an error when nothing usable was
// accumulated.
type Builder struct {
	hist    []float64
	counted int
}

// NewBuilder returns a Builder sized for the histogram geometry.
func NewBuilder() Builder {
	return Builder{hist: make([]float64, HueBins*SatBins*ValBins)}
}

// Add feeds one RGB frame (width*height*3 bytes, row-major). Frames must all
// share the geometry of the first.
func (b *Builder) Add(width, height int, frame []byte) error {
	stride := width * height * 3
	if len(frame) != stride {
		return xcerr.E(xcerr.CodeValidation,
			fmt.Sprintf("frame has %d bytes, want %d (RGB %dx%d)", len(frame), stride, width, height), nil)
	}
	if b.hist == nil {
		b.hist = make([]float64, HueBins*SatBins*ValBins)
	}
	// Subsample pixels: every 3rd pixel keeps the model cheap and decorrelates
	// neighbors (adjacent pixels are the same shirt).
	for px := 0; px+2 < len(frame); px += 9 {
		r, g, bl := float64(frame[px]), float64(frame[px+1]), float64(frame[px+2])
		if r+g+bl < 24 {
			continue // near-black: undecodable, not a signal
		}
		h, s, v := rgbToHSV(r, g, bl)
		b.hist[binIndex(h, s, v)]++
		b.counted++
	}
	return nil
}

// Build normalizes the accumulated histogram into a Signature.
func (b *Builder) Build() (Signature, error) {
	if b.counted == 0 {
		return Signature{}, xcerr.E(xcerr.CodeValidation, "signature region held no usable pixels", nil)
	}
	bins := make([]float64, len(b.hist))
	inv := 1.0 / float64(b.counted)
	for i, v := range b.hist {
		bins[i] = v * inv
	}
	return Signature{Bins: bins}, nil
}

// BuildSignature builds the model from RGB pixel rows: frames is a sequence of
// width*height*3-byte RGB frames. Convenience wrapper over Builder.
func BuildSignature(width, height int, frames []([]byte)) (Signature, error) {
	b := NewBuilder()
	for _, f := range frames {
		if err := b.Add(width, height, f); err != nil {
			return Signature{}, err
		}
	}
	return b.Build()
}

// ScoreFrame backprojects one RGB frame against the signature and answers
// "what fraction of this frame's pixels look like the person". Frames are
// subsampled like BuildSignature so both sides of the comparison weight
// pixels the same way.
func (s Signature) ScoreFrame(width, height int, frame []byte) (float64, error) {
	if len(s.Bins) != HueBins*SatBins*ValBins {
		return 0, xcerr.E(xcerr.CodeValidation, "signature model has the wrong bin count", nil)
	}
	stride := width * height * 3
	if len(frame) != stride {
		return 0, xcerr.E(xcerr.CodeValidation,
			fmt.Sprintf("frame has %d bytes, want %d (RGB %dx%d)", len(frame), stride, width, height), nil)
	}
	total, matched := 0, 0
	for px := 0; px+2 < len(frame); px += 9 {
		r, g, b := float64(frame[px]), float64(frame[px+1]), float64(frame[px+2])
		if r+g+b < 24 {
			continue
		}
		total++
		if s.Bins[binIndex(rgbToHSV(r, g, b))] > 0 {
			matched++
		}
	}
	if total == 0 {
		return 0, nil
	}
	return float64(matched) / float64(total), nil
}

// binIndex maps HSV into the flattened histogram. H wraps (mod 360), S and V
// clamp. Inputs are 0..255 RGB-converted channels.
func binIndex(hDeg, s01, v01 float64) int {
	h := int(hDeg/360.0*HueBins) % HueBins
	if h < 0 {
		h += HueBins
	}
	si := clampBin(int(s01*SatBins), SatBins)
	vi := clampBin(int(v01*ValBins), ValBins)
	return h*SatBins*ValBins + si*ValBins + vi
}

func clampBin(v, n int) int {
	if v < 0 {
		return 0
	}
	if v >= n {
		return n - 1
	}
	return v
}

// rgbToHSV converts one 0..255 RGB triple to (hue degrees 0..360,
// saturation 0..1, value 0..1).
func rgbToHSV(r, g, b float64) (h, s, v float64) {
	r, g, b = r/255, g/255, b/255
	max := math.Max(r, math.Max(g, b))
	min := math.Min(r, math.Min(g, b))
	v = max
	if max <= 0 {
		return 0, 0, 0
	}
	s = (max - min) / max
	if s <= 0 {
		return 0, 0, v
	}
	d := max - min
	switch max {
	case r:
		h = 60 * math.Mod((g-b)/d, 6)
	case g:
		h = 60 * ((b-r)/d + 2)
	default:
		h = 60 * ((r-g)/d + 4)
	}
	if h < 0 {
		h += 360
	}
	return h, s, v
}

// SigHash is the stable short hash ConfigKey carries for cache invalidation
// when the spot is re-seeded.
func SigHash(bins []float64) string {
	if len(bins) == 0 {
		return ""
	}
	h := fmt.Sprintf("%d:", len(bins))
	for _, b := range bins {
		h += fmt.Sprintf("%.5f,", b)
	}
	return h
}
