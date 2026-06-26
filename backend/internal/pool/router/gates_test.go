package router

import (
	"context"
	"testing"
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
