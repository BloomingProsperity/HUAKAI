package router

const (
	LocalityWeight  = 1.0
	HeadroomWeight  = 0.3
	DegradedPenalty = 2.0
)

func LocalityBonus(hasCache bool) float64 {
	if hasCache {
		return LocalityWeight
	}
	return 0
}
