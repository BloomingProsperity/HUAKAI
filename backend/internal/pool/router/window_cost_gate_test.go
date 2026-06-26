package router

import (
	"context"
	"testing"
)

// fakeReader 是 windowcost.CostReader 的测试实现。
type fakeReader struct {
	cents int64
	fresh bool
}

func (f *fakeReader) CurrentCost(_ int64) (int64, bool) {
	return f.cents, f.fresh
}

func snapWindowCost(id int64, limitCents int64) *AccountSnapshot {
	return &AccountSnapshot{ID: id, WindowCostLimitCents: limitCents}
}

func TestWindowCostGate_OverLimit_Fresh_Excluded(t *testing.T) {
	gate := WindowCostGate{Reader: &fakeReader{cents: 1000, fresh: true}}
	ok, reason, err := gate.Allow(context.Background(), snapWindowCost(1, 500), SelectionRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected account to be excluded when cost >= limit")
	}
	if reason != GateFailureWindowCost {
		t.Fatalf("expected reason %q, got %q", GateFailureWindowCost, reason)
	}
}

func TestWindowCostGate_UnderLimit_Included(t *testing.T) {
	gate := WindowCostGate{Reader: &fakeReader{cents: 400, fresh: true}}
	ok, _, err := gate.Allow(context.Background(), snapWindowCost(1, 500), SelectionRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected account to be included when cost < limit")
	}
}

func TestWindowCostGate_LimitZero_Included_DefaultSafety(t *testing.T) {
	// 默认安全:limit=0 表示不限——即使成本极大也必须纳入。
	gate := WindowCostGate{Reader: &fakeReader{cents: 999999, fresh: true}}
	ok, _, err := gate.Allow(context.Background(), snapWindowCost(1, 0), SelectionRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("limit=0 must never exclude (default safety)")
	}
}

func TestWindowCostGate_NotFresh_Included_FailOpen(t *testing.T) {
	// fail-open:缓存条目陈旧/缺失 → 纳入。
	gate := WindowCostGate{Reader: &fakeReader{cents: 999999, fresh: false}}
	ok, _, err := gate.Allow(context.Background(), snapWindowCost(1, 500), SelectionRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("stale cache must fail-open (include)")
	}
}

func TestWindowCostGate_NilReader_Included_FailOpen(t *testing.T) {
	// fail-open:reader 为 nil → 纳入。
	gate := WindowCostGate{Reader: nil}
	ok, _, err := gate.Allow(context.Background(), snapWindowCost(1, 500), SelectionRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("nil reader must fail-open (include)")
	}
}

func TestWindowCostGate_NegativeLimit_Included(t *testing.T) {
	// 负数 limit 与 0 同等对待——选择性开启需要正值。
	gate := WindowCostGate{Reader: &fakeReader{cents: 999, fresh: true}}
	ok, _, err := gate.Allow(context.Background(), snapWindowCost(1, -1), SelectionRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("negative limit must not exclude")
	}
}
