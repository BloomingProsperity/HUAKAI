package router

import (
	"context"
	"testing"
	"time"
)

// TestProviderAccountHealthGate_DisableCooling 验证 TOKLIFE-02 的逃生舱。
//
// 区分性测试:
//  1. cooldown + disable_cooling=TRUE  → 合格(逃生舱生效)
//  2. cooldown + disable_cooling=FALSE → 剔除(默认安全 —— 该标志是唯一的开关)
//  3. healthy  + disable_cooling=FALSE → 合格(健康账号不受影响)
func TestProviderAccountHealthGate_DisableCooling(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	// 未来的 cooldown_until,正常情况下该账号会被剔除
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
