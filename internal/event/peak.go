package event

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
