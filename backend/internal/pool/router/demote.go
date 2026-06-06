package router

func ShouldDemote(missCount, threshold uint32) bool {
	return threshold > 0 && missCount >= threshold
}
