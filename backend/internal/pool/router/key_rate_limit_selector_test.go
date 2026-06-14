package router

import (
	"context"
	"errors"
	"testing"
)

// A key that has exhausted its RPM budget is rejected before selection with
// ErrKeyRateLimited. Mutation guard: if the Check verdict is ignored, the
// request passes and this goes red.
func TestKeyRateLimitSelector_OverBudget_Rejects(t *testing.T) {
	c := recCounter()
	c.Record(7, 0) // key 7 has used its RPM-1
	sel := NewKeyRateLimitSelector(fakeRecSelector{res: &SelectionResult{AccountID: 9}}, c, 1, 0)
	res, err := sel.Select(context.Background(), SelectionRequest{APIKeyID: 7})
	if !errors.Is(err, ErrKeyRateLimited) || res != nil {
		t.Fatalf("over-budget key must be rejected with ErrKeyRateLimited, got res=%v err=%v", res, err)
	}
	// A different key is unaffected.
	if _, err := sel.Select(context.Background(), SelectionRequest{APIKeyID: 8}); err != nil {
		t.Fatalf("untouched key must pass, got %v", err)
	}
}

// Under budget the request selects normally AND is recorded — proven by a third
// request crossing an RPM-2 budget after two successful selects.
func TestKeyRateLimitSelector_UnderBudget_PassesAndRecords(t *testing.T) {
	c := recCounter()
	sel := NewKeyRateLimitSelector(fakeRecSelector{res: &SelectionResult{AccountID: 9}}, c, 2, 0)
	req := SelectionRequest{APIKeyID: 7}
	for i := 0; i < 2; i++ {
		if res, err := sel.Select(context.Background(), req); err != nil || res.AccountID != 9 {
			t.Fatalf("request %d under RPM-2 must pass, got res=%v err=%v", i, res, err)
		}
	}
	if _, err := sel.Select(context.Background(), req); !errors.Is(err, ErrKeyRateLimited) {
		t.Fatalf("3rd request over RPM-2 must be rejected (proves each select recorded), got %v", err)
	}
}

// TPM is enforced from the request's estimated input tokens.
func TestKeyRateLimitSelector_TPM(t *testing.T) {
	c := recCounter()
	sel := NewKeyRateLimitSelector(fakeRecSelector{res: &SelectionResult{AccountID: 9}}, c, 0, 100)
	if _, err := sel.Select(context.Background(), SelectionRequest{APIKeyID: 7, EstimatedInputTokens: 101}); !errors.Is(err, ErrKeyRateLimited) {
		t.Fatalf("a single 101-token request must be rejected on a TPM-100 budget, got %v", err)
	}
}

// Inert by default: zero limits, nil counter, and no API key id all pass through.
func TestKeyRateLimitSelector_InertByDefault(t *testing.T) {
	c := recCounter()
	c.Record(7, 0)
	c.Record(7, 0)
	pass := &SelectionResult{AccountID: 9}
	// zero limits = off
	if _, err := NewKeyRateLimitSelector(fakeRecSelector{res: pass}, c, 0, 0).Select(context.Background(), SelectionRequest{APIKeyID: 7}); err != nil {
		t.Fatalf("zero limits must pass, got %v", err)
	}
	// nil counter
	if _, err := NewKeyRateLimitSelector(fakeRecSelector{res: pass}, nil, 1, 1).Select(context.Background(), SelectionRequest{APIKeyID: 7}); err != nil {
		t.Fatalf("nil counter must pass, got %v", err)
	}
	// no API key id (unauthenticated path) — never rate-limited by key
	if _, err := NewKeyRateLimitSelector(fakeRecSelector{res: pass}, c, 1, 0).Select(context.Background(), SelectionRequest{APIKeyID: 0}); err != nil {
		t.Fatalf("APIKeyID 0 must pass, got %v", err)
	}
}

// A wait-plan result does not consume the key budget (only a concrete selection).
func TestKeyRateLimitSelector_WaitPlanNotRecorded(t *testing.T) {
	c := recCounter()
	wp := &SelectionResult{AccountID: 9, WaitPlan: &WaitPlan{AccountID: 9}}
	sel := NewKeyRateLimitSelector(fakeRecSelector{res: wp}, c, 1, 0)
	if _, err := sel.Select(context.Background(), SelectionRequest{APIKeyID: 7}); err != nil {
		t.Fatalf("wait-plan select must pass, got %v", err)
	}
	// budget not consumed → a following request still allowed
	if _, err := sel.Select(context.Background(), SelectionRequest{APIKeyID: 7}); err != nil {
		t.Fatalf("wait-plan must not consume budget, got %v", err)
	}
}
