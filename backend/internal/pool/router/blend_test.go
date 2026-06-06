package router

import "testing"

func TestBlend_LocalityBeatsHeadroom(t *testing.T) {
	cached := Blend(Candidate{HasCache: true, LoadRate: 0.95})
	uncached := Blend(Candidate{HasCache: false, LoadRate: 0.0})
	if cached <= uncached {
		t.Fatalf("cached score=%v must beat uncached score=%v", cached, uncached)
	}
}

func TestBlend_DegradedPenaltyWins(t *testing.T) {
	active := Blend(Candidate{HasCache: false, LoadRate: 0.9})
	degraded := Blend(Candidate{HasCache: true, LoadRate: 0.0, Degraded: true})
	if degraded >= active {
		t.Fatalf("degraded score=%v must be below active score=%v", degraded, active)
	}
}
