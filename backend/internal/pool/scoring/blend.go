package scoring

type Candidate struct {
	HasCache bool
	LoadRate float64
	Degraded bool
}

func Blend(c Candidate) float64 {
	score := LocalityBonus(c.HasCache)
	score += (1 - c.LoadRate) * HeadroomWeight
	if c.Degraded {
		score -= DegradedPenalty
	}
	return score
}
