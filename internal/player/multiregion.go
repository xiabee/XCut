package player

import (
	"fmt"

	"github.com/xiabee/XCut/internal/xcerr"
)

// RegionCount splits the spot rect into this many horizontal bands.
const RegionCount = 3

// Region weights: the torso band (shirt) is the most stable indicator; head
// (skin + hair) and legs (shorts + shoes) add discrimination. The weights sum
// to 1.0.
var regionWeights = [RegionCount]float64{0.25, 0.45, 0.30} // head, torso, legs

// MultiRegionSignature is three per-band HSV histograms (head / torso / legs),
// each L1-normalized. The bands come from splitting the spot rect vertically:
// [0, 25%) = head, [25%, 65%) = torso, [65%, 100%] = legs.
type MultiRegionSignature struct {
	Bands [RegionCount][]float64 // each len == HueBins*SatBins*ValBins
}

// MultiRegionBuilder accumulates pixels per band across streamed frames.
type MultiRegionBuilder struct {
	bands  [RegionCount]Builder
	bounds [RegionCount]float64 // band boundaries as fractions of spot height (cumulative)
	ready  bool
}

// NewMultiRegionBuilder splits the spot into RegionCount horizontal bands and
// returns a builder with one histogram per band.
func NewMultiRegionBuilder() MultiRegionBuilder {
	mr := MultiRegionBuilder{}
	mr.bounds = [RegionCount]float64{0.25, 0.65, 1.0}
	for i := range mr.bands {
		mr.bands[i] = NewBuilder()
	}
	mr.ready = true
	return mr
}

// AddFeed feeds one spot-cropped RGB frame. The frame's height is split into
// RegionCount horizontal bands, each feeding its own histogram.
func (m *MultiRegionBuilder) AddFeed(width, height int, frame []byte) error {
	if !m.ready {
		return xcerr.E(xcerr.CodeInternal, "builder not initialized", nil)
	}
	for band := 0; band < RegionCount; band++ {
		loF := 0.0
		if band > 0 {
			loF = bandFracAt(band - 1)
		}
		hiF := 1.0
		if band < RegionCount-1 {
			hiF = bandFracAt(band)
		}
		lo := int(float64(loF) * float64(height))
		hi := int(float64(hiF) * float64(height))
		if hi-lo < 2 {
			continue
		}
		for y := lo; y < hi; y++ {
			row := frame[y*width*3 : (y+1)*width*3]
			if err := m.bands[band].Add(width, 1, row); err != nil {
				return err
			}
		}
	}
	return nil
}

func bandFracAt(band int) float64 {
	switch band {
	case 0:
		return 0.25
	case 1:
		return 0.65
	default:
		return 1.0
	}
}

// Build normalizes each band's histogram into a MultiRegionSignature.
func (m *MultiRegionBuilder) Build() (MultiRegionSignature, error) {
	var mr MultiRegionSignature
	for i := 0; i < RegionCount; i++ {
		sig, err := m.bands[i].Build()
		if err != nil {
			return MultiRegionSignature{}, xcerr.E(xcerr.CodeValidation,
				fmt.Sprintf("band %d held no usable pixels", i), err)
		}
		mr.Bands[i] = sig.Bins
	}
	return mr, nil
}

// ScoreFrame backprojects one RGB frame against all three bands and returns a
// weighted vote: the torso band must contribute, head/legs add bonus. The
// result is 0..1.
func (mr MultiRegionSignature) ScoreFrame(width, height int, frame []byte) (float64, error) {
	stride := width * height * 3
	if len(frame) != stride {
		return 0, xcerr.E(xcerr.CodeValidation,
			fmt.Sprintf("frame has %d bytes, want %d", len(frame), stride), nil)
	}

	bandH := height / RegionCount
	bandScores := make([]float64, RegionCount)

	for band := 0; band < RegionCount; band++ {
		lo := band * bandH
		hi := lo + bandH
		if band == RegionCount-1 {
			hi = height
		}
		if lo >= hi {
			bandScores[band] = 0
			continue
		}
		total, matched := 0, 0
		for y := lo; y < hi; y++ {
			for px := y * width * 3; px+2 < (y+1)*width*3; px += 9 {
				r, g, bl := float64(frame[px]), float64(frame[px+1]), float64(frame[px+2])
				if r+g+bl < 24 {
					continue
				}
				total++
				h, s, v := rgbToHSV(r, g, bl)
				if mr.Bands[band][binIndex(h, s, v)] > 0 {
					matched++
				}
			}
		}
		if total > 0 {
			bandScores[band] = float64(matched) / float64(total)
		}
	}

	// Weighted vote: torso (band 1) is the anchor.
	score := regionWeights[0]*bandScores[0] +
		regionWeights[1]*bandScores[1] +
		regionWeights[2]*bandScores[2]
	return score, nil
}
