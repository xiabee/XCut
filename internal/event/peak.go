package event

import "sort"

// peakWindow finds the densest maxDur-second window within sorted hits.
// Returns the window bounds and the hit count inside it.
func peakWindow(hits []float64, maxDur float64) (start, end float64, count int) {
	if len(hits) == 0 {
		return 0, 0, 0
	}
	n := len(hits)
	best, bestCount := 0, 0
	for i := 0; i < n; i++ {
		j := sort.SearchFloat64s(hits, hits[i]+maxDur+0.001)
		if c := j - i; c > bestCount {
			bestCount = c
			best = i
		}
	}
	return hits[best], hits[best] + maxDur, bestCount
}

// peakOnsetRate finds the max onset rate (hits/sec) in any 1-second window.
func peakOnsetRate(hits []float64) float64 {
	if len(hits) <= 1 {
		return float64(len(hits))
	}
	best := 1.0
	for i := 0; i < len(hits); i++ {
		for j := i + 1; j < len(hits); j++ {
			d := hits[j] - hits[i]
			if d > 0 {
				if r := float64(j-i) / d; r > best {
					best = r
				}
			}
		}
	}
	return best
}

// gapPenalty counts internal quiet gaps (> gapSec between consecutive hits).
func gapPenalty(hits []float64, gapSec float64) int {
	n := 0
	for i := 1; i < len(hits); i++ {
		if hits[i]-hits[i-1] > gapSec {
			n++
		}
	}
	return n
}
