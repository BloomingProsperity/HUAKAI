package router

import (
	"context"
	"testing"
	"time"
)

// TestProviderAccountHealthGate_DisableCooling verifies the TOKLIFE-02 escape hatch.
//
// Discriminating tests:
//  1. cooldown + disable_cooling=TRUE  → eligible (escape hatch fires)
//  2. cooldown + disable_cooling=FALSE → excluded (DEFAULT SAFETY — flag is the only gate)
//  3. healthy  + disable_cooling=FALSE → eligible (healthy accounts unaffected)
func TestProviderAccountHealthGate_DisableCooling(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	// future cooldown_until so account would normally be benched
	cooldownUntil := now.Add(10 * time.Minute)

	gate := ProviderAccountHealthGate{Now: func() time.Time { return now }}

	t.Run("cooldown+disable_cooling=true → eligible", func(t *testing.T) {
		acc := &AccountSnapshot{
			ID:               1,
			HealthState:      "cooldown",
			HealthStateUntil: cooldownUntil,
			DisableCooling:   true,
		}
		ok, reason, err := gate.Allow(ctx, acc, SelectionRequest{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !ok {
			t.Errorf("expected eligible (disable_cooling=true); got excluded, reason=%s", reason)
		}
	})

	t.Run("cooldown+disable_cooling=false → excluded (default safety)", func(t *testing.T) {
		acc := &AccountSnapshot{
			ID:               2,
			HealthState:      "cooldown",
			HealthStateUntil: cooldownUntil,
			DisableCooling:   false,
		}
		ok, reason, err := gate.Allow(ctx, acc, SelectionRequest{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ok {
			t.Errorf("expected excluded (disable_cooling=false, account in cooldown); got eligible, reason=%s", reason)
		}
	})

	t.Run("healthy+disable_cooling=false → eligible", func(t *testing.T) {
		acc := &AccountSnapshot{
			ID:             3,
			HealthState:    "healthy",
			DisableCooling: false,
		}
		ok, reason, err := gate.Allow(ctx, acc, SelectionRequest{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !ok {
			t.Errorf("expected eligible (healthy account); got excluded, reason=%s", reason)
		}
	})
}
