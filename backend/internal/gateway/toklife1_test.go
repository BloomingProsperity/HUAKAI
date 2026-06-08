package gateway

import (
	"testing"
	"time"
)

// TOKLIFE-01: cooldown escalates exponentially with the consecutive-error streak
// (no provider Retry-After), capped at 30m, so a thrashing account is parked
// progressively longer instead of re-tried every flat 60s.
func TestWithEscalatedCooldown(t *testing.T) {
	now := time.Unix(1000, 0)
	base := DefaultCooldownDuration
	cap30 := 30 * time.Minute
	cases := []struct {
		streak int
		want   time.Duration
	}{
		{0, base}, // no streak -> base
		{1, base}, // first error -> base
		{2, 2 * base},
		{3, 4 * base},
		{4, 8 * base},
		{6, cap30}, // 32*60s = 32m -> capped at 30m
		{50, cap30},
	}
	for _, tc := range cases {
		// MUTATION GUARD: dropping the <<shift escalation collapses every case to
		// base -> the 2x/4x/8x/cap rows go red.
		c := withEscalatedCooldown(FSMClassification{}, tc.streak, now)
		if got := c.CooldownUntil.Sub(now); got != tc.want {
			t.Fatalf("streak %d: cooldown %v want %v", tc.streak, got, tc.want)
		}
	}

	// provider Retry-After override wins -> NO escalation, CooldownUntil untouched.
	c := withEscalatedCooldown(FSMClassification{RetryAfter: 5 * time.Second}, 10, now)
	if !c.CooldownUntil.IsZero() {
		t.Fatalf("RetryAfter override must not be escalated, got %v", c.CooldownUntil)
	}
	// explicit CooldownUntil preserved.
	explicit := now.Add(time.Hour)
	c2 := withEscalatedCooldown(FSMClassification{CooldownUntil: explicit}, 10, now)
	if c2.CooldownUntil != explicit {
		t.Fatalf("explicit CooldownUntil must be preserved, got %v", c2.CooldownUntil)
	}
}
