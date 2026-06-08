package router

import (
	"context"
	"testing"
)

// fakeReader is a test implementation of windowcost.CostReader.
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
	// DEFAULT SAFETY: limit=0 means unlimited — must include even if cost is huge.
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
	// FAIL-OPEN: stale/missing cache entry → include.
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
	// FAIL-OPEN: nil reader → include.
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
	// Negative limit treated same as 0 — opt-in requires positive value.
	gate := WindowCostGate{Reader: &fakeReader{cents: 999, fresh: true}}
	ok, _, err := gate.Allow(context.Background(), snapWindowCost(1, -1), SelectionRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("negative limit must not exclude")
	}
}
