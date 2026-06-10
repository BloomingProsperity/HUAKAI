package router

import (
	"context"
	"testing"
)

// MUTATION: Select 去掉 StickyState 盖章 defer → hit/miss 断言红;trySticky
// 不再消费前置 boundID(永远 fall-through)→ hit 用例选错账号红(DM-07)。
func TestSelectStampsStickyState(t *testing.T) {
	mk := func() []*AccountSnapshot {
		return []*AccountSnapshot{
			{ID: 101, TenantID: 7, Priority: 1, LoadRate: 0.01, MaxConcurrency: 4, HealthState: "healthy"},
			{ID: 202, TenantID: 7, Priority: 50, LoadRate: 0.01, MaxConcurrency: 4, HealthState: "healthy"},
		}
	}
	sel := NewDefaultSelector(&stubAccountSource{accounts: mk()},
		WithSlotManager(newMemSlotManager()),
		WithStickyStore(&stubSticky{bindings: map[string]int64{"sess-1": 202}}))
	res, err := sel.Select(context.Background(), SelectionRequest{TenantID: 7, RequestedModel: "m", SessionHash: "sess-1"})
	if err != nil {
		t.Fatalf("Select(hit): %v", err)
	}
	if res.AccountID != 202 || res.StickyState != StickyStateHit {
		t.Fatalf("want sticky hit on 202; got id=%d state=%q", res.AccountID, res.StickyState)
	}

	// 绑定账号 999 不在候选(等价被健康门/排除集挡掉)→ fresh 换号 → miss
	sel = NewDefaultSelector(&stubAccountSource{accounts: mk()},
		WithSlotManager(newMemSlotManager()),
		WithStickyStore(&stubSticky{bindings: map[string]int64{"sess-2": 999}}))
	res, err = sel.Select(context.Background(), SelectionRequest{TenantID: 7, RequestedModel: "m", SessionHash: "sess-2"})
	if err != nil {
		t.Fatalf("Select(miss): %v", err)
	}
	if res.AccountID == 0 || res.StickyState != StickyStateMiss {
		t.Fatalf("want sticky miss; got id=%d state=%q", res.AccountID, res.StickyState)
	}

	// 无 binding → none(空)
	sel = NewDefaultSelector(&stubAccountSource{accounts: mk()},
		WithSlotManager(newMemSlotManager()),
		WithStickyStore(&stubSticky{bindings: map[string]int64{}}))
	res, err = sel.Select(context.Background(), SelectionRequest{TenantID: 7, RequestedModel: "m", SessionHash: "sess-3"})
	if err != nil {
		t.Fatalf("Select(none): %v", err)
	}
	if res.StickyState != StickyStateNone {
		t.Fatalf("want none; got %q", res.StickyState)
	}
}
