package router

import (
	"context"
	"testing"
	"time"
)

func TestPinnedAccountGate(t *testing.T) {
	ctx := context.Background()
	chain := DefaultGateChain()
	accounts := []*AccountSnapshot{
		{ID: 10, TenantID: 1, MaxConcurrency: 4, HealthState: "healthy"},
		{ID: 20, TenantID: 1, MaxConcurrency: 4, HealthState: "healthy"},
		{ID: 30, TenantID: 1, MaxConcurrency: 4, HealthState: "healthy"},
	}

	req := SelectionRequest{TenantID: 1, PinnedAccountID: 20}
	for _, account := range accounts {
		ok, reason, err := chain.Allow(ctx, account, req)
		if err != nil {
			t.Fatalf("account %d Allow error: %v", account.ID, err)
		}
		if account.ID == 20 {
			if !ok || reason != "" {
				t.Fatalf("pinned account Allow(%d)=(%v,%q), want allow", account.ID, ok, reason)
			}
			continue
		}
		// 变异:把谓词取反，或用 AllowAllGate 替换 pinned gate；这样未 pin 的
		// 账号会通过，而不是返回 GateFailurePinnedAccount。
		if ok || reason != GateFailurePinnedAccount {
			t.Fatalf("non-pinned account Allow(%d)=(%v,%q), want reject %q",
				account.ID, ok, reason, GateFailurePinnedAccount)
		}
	}

	req.PinnedAccountID = 0
	for _, account := range accounts {
		ok, reason, err := chain.Allow(ctx, account, req)
		if err != nil {
			t.Fatalf("default account %d Allow error: %v", account.ID, err)
		}
		if !ok || reason != "" {
			t.Fatalf("zero pinned account Allow(%d)=(%v,%q), want unchanged allow", account.ID, ok, reason)
		}
	}
}

func TestModelRateLimitGateBlocksOnlyCurrentAccountAndModelUntilReset(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	resetAt := base.Add(5 * time.Minute)
	gate := modelRateLimitGate{Now: func() time.Time { return base }}
	accountA := &AccountSnapshot{
		ID:          10,
		TenantID:    1,
		HealthState: "healthy",
		ModelRateLimits: map[string]ModelRateLimit{
			"model-x": {RateLimitResetAt: resetAt},
		},
	}
	accountB := &AccountSnapshot{ID: 20, TenantID: 1, HealthState: "healthy"}

	ok, reason, err := gate.Allow(ctx, accountA, SelectionRequest{TenantID: 1, RequestedModel: "public-model", ModelCooldownKey: "model-x"})
	if err != nil || ok || reason != GateFailureModelCooldown {
		t.Fatalf("account A model-x Allow=(%v,%s,%v) want blocked by model gate", ok, reason, err)
	}
	ok, reason, err = gate.Allow(ctx, accountA, SelectionRequest{TenantID: 1, RequestedModel: "public-model", ModelCooldownKey: "model-y"})
	if err != nil || !ok || reason != "" {
		t.Fatalf("account A model-y Allow=(%v,%s,%v) want allowed", ok, reason, err)
	}
	ok, reason, err = gate.Allow(ctx, accountB, SelectionRequest{TenantID: 1, RequestedModel: "public-model", ModelCooldownKey: "model-x"})
	if err != nil || !ok || reason != "" {
		t.Fatalf("account B model-x Allow=(%v,%s,%v) want allowed", ok, reason, err)
	}

	gate.Now = func() time.Time { return resetAt.Add(time.Second) }
	ok, reason, err = gate.Allow(ctx, accountA, SelectionRequest{TenantID: 1, RequestedModel: "public-model", ModelCooldownKey: "model-x"})
	if err != nil || !ok || reason != "" {
		t.Fatalf("account A model-x after reset Allow=(%v,%s,%v) want allowed", ok, reason, err)
	}
	healthOK, healthReason, err := (ProviderAccountHealthGate{Now: func() time.Time { return base }}).Allow(ctx, accountA, SelectionRequest{TenantID: 1})
	if err != nil || !healthOK || healthReason != "" {
		t.Fatalf("model cooldown must not degrade account health gate: Allow=(%v,%s,%v)", healthOK, healthReason, err)
	}
}

func TestDefaultSelectorModelRateLimitFallsThroughToAnotherAccountForSameModel(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	accounts := []*AccountSnapshot{
		{
			ID:             10,
			TenantID:       1,
			Priority:       1,
			MaxConcurrency: 4,
			ModelRateLimits: map[string]ModelRateLimit{
				"model-x": {RateLimitResetAt: base.Add(5 * time.Minute)},
			},
		},
		{ID: 20, TenantID: 1, Priority: 2, MaxConcurrency: 4},
	}
	selector := NewDefaultSelector(
		&stubAccountSource{accounts: accounts},
		WithNow(func() time.Time { return base }),
		WithSlotManager(newMemSlotManager()),
	)

	res, err := selector.Select(ctx, SelectionRequest{TenantID: 1, RequestedModel: "public-model", ModelCooldownKey: "model-x"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if res.AccountID != 20 {
		t.Fatalf("selected account=%d want 20; account 10 only blocked for model-x", res.AccountID)
	}
}
