package router

import "testing"

func TestShouldDemote(t *testing.T) {
	if ShouldDemote(1, 2) {
		t.Fatal("miss count below threshold should not demote")
	}
	if !ShouldDemote(2, 2) {
		t.Fatal("miss count at threshold should demote")
	}
	if ShouldDemote(10, 0) {
		t.Fatal("zero threshold should disable demotion")
	}
}
