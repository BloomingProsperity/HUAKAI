package auth

import (
	"context"
	"testing"
	"time"
)

// fixedClockController builds a scope-budgeted controller whose clock is the
// dereferenced *clk, so a test can advance time deterministically.
func fixedClockController(cfg StormScopeConfig, clk *time.Time) *StormController {
	c := NewStormControllerWithScopeBudget(nil, cfg)
	c.now = func() time.Time { return *clk }
	return c
}

// TestStormControllerEndpointScopeDeniesWhenBudgetExhausted: with endpoint burst=2,
// the third same-endpoint acquire in the same instant is denied. Mutation: make
// scopeBucket.tryAcquire always return true → the third acquire admits → red.
func TestStormControllerEndpointScopeDeniesWhenBudgetExhausted(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	c := fixedClockController(StormScopeConfig{PerEndpointRate: 1, PerEndpointBurst: 2}, &now)
	for i := 1; i <= 2; i++ {
		if _, outcome, err := c.AcquireProviderEndpoint(context.Background(), 1, "anthropic", ""); outcome != "" || err != nil {
			t.Fatalf("acquire #%d: outcome=%q err=%v, want admit", i, outcome, err)
		}
	}
	refund, outcome, err := c.AcquireProviderEndpoint(context.Background(), 1, "anthropic", "")
	if err != nil || outcome != OutcomeStormBudgetExhausted || refund != nil {
		t.Fatalf("acquire #3: refund!=nil=%v outcome=%q err=%v, want storm_budget_exhausted denial", refund != nil, outcome, err)
	}
}

// TestStormControllerEndpointScopeRefillsOverTime: burst=1 rate=1/s. Acquire, deny
// immediately, advance 1s, acquire again. Mutation: skip the elapsed-time refill in
// refillLocked → the post-advance acquire denies → red.
func TestStormControllerEndpointScopeRefillsOverTime(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	c := fixedClockController(StormScopeConfig{PerEndpointRate: 1, PerEndpointBurst: 1}, &now)
	if _, outcome, _ := c.AcquireProviderEndpoint(context.Background(), 1, "anthropic", ""); outcome != "" {
		t.Fatalf("first acquire denied: %q", outcome)
	}
	if _, outcome, _ := c.AcquireProviderEndpoint(context.Background(), 1, "anthropic", ""); outcome != OutcomeStormBudgetExhausted {
		t.Fatalf("immediate second acquire outcome=%q, want denial", outcome)
	}
	now = now.Add(time.Second)
	if _, outcome, _ := c.AcquireProviderEndpoint(context.Background(), 1, "anthropic", ""); outcome != "" {
		t.Fatalf("acquire after 1s refill outcome=%q, want admit", outcome)
	}
}

// TestStormControllerEndpointBucketsIndependentPerProvider: provider A exhausted
// must not deny provider B. Mutation: key every endpoint to one shared bucket →
// provider B denies → red.
func TestStormControllerEndpointBucketsIndependentPerProvider(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	c := fixedClockController(StormScopeConfig{PerEndpointRate: 1, PerEndpointBurst: 1}, &now)
	if _, outcome, _ := c.AcquireProviderEndpoint(context.Background(), 1, "anthropic", ""); outcome != "" {
		t.Fatalf("provider A first acquire denied: %q", outcome)
	}
	if _, outcome, _ := c.AcquireProviderEndpoint(context.Background(), 1, "anthropic", ""); outcome != OutcomeStormBudgetExhausted {
		t.Fatalf("provider A second acquire outcome=%q, want denial", outcome)
	}
	if _, outcome, _ := c.AcquireProviderEndpoint(context.Background(), 1, "openai", ""); outcome != "" {
		t.Fatalf("provider B acquire outcome=%q, want admit (independent per-endpoint bucket)", outcome)
	}
}

// TestStormControllerEndpointRefundReturnsToken: with burst=1 the first acquire
// consumes the token, refund returns it, the next acquire admits. Mutation: make
// scopeBucket.refund a no-op → the post-refund acquire denies → red.
func TestStormControllerEndpointRefundReturnsToken(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	c := fixedClockController(StormScopeConfig{PerEndpointRate: 1, PerEndpointBurst: 1}, &now)
	refund, outcome, _ := c.AcquireProviderEndpoint(context.Background(), 1, "anthropic", "")
	if outcome != "" || refund == nil {
		t.Fatalf("first acquire outcome=%q refund!=nil=%v, want admit", outcome, refund != nil)
	}
	refund()
	if _, outcome, _ := c.AcquireProviderEndpoint(context.Background(), 1, "anthropic", ""); outcome != "" {
		t.Fatalf("acquire after refund outcome=%q, want admit (refund must return the token)", outcome)
	}
}

// TestStormControllerGlobalScopeDeniesWhenExhausted: global burst=1 admits once
// then denies. Mutation: skip the global bucket check in AcquireGlobal → the second
// acquire admits → red.
func TestStormControllerGlobalScopeDeniesWhenExhausted(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	c := fixedClockController(StormScopeConfig{GlobalRate: 1, GlobalBurst: 1}, &now)
	if _, outcome, _ := c.AcquireGlobal(context.Background(), 1); outcome != "" {
		t.Fatalf("first global acquire denied: %q", outcome)
	}
	if _, outcome, err := c.AcquireGlobal(context.Background(), 1); outcome != OutcomeStormBudgetExhausted || err != nil {
		t.Fatalf("second global acquire outcome=%q err=%v, want denial", outcome, err)
	}
}

// TestStormControllerSubUnitBurstTreatedAsDisabled: a burst < 1 can never admit a
// whole token, so the layer treats the scope as OFF (admit-all) rather than
// every refresh. Mutation: loosen endpointEnabled to accept burst>0 → acquires route
// into a 0.5-token bucket and deny → red.
func TestStormControllerSubUnitBurstTreatedAsDisabled(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	c := fixedClockController(StormScopeConfig{PerEndpointRate: 1, PerEndpointBurst: 0.5}, &now)
	for i := 1; i <= 3; i++ {
		if _, outcome, err := c.AcquireProviderEndpoint(context.Background(), 1, "anthropic", ""); outcome != "" || err != nil {
			t.Fatalf("acquire #%d with sub-unit burst: outcome=%q err=%v, want admit (scope disabled)", i, outcome, err)
		}
	}
}

// TestStormControllerNilSafeScopes proves a nil controller admits both scopes
// without panicking (defensive: a misconfigured caller must not crash the worker).
func TestStormControllerNilSafeScopes(t *testing.T) {
	var c *StormController
	if r, outcome, err := c.AcquireProviderEndpoint(context.Background(), 1, "anthropic", ""); r == nil || outcome != "" || err != nil {
		t.Fatalf("nil controller endpoint: r!=nil=%v outcome=%q err=%v, want admit", r != nil, outcome, err)
	}
	if r, outcome, err := c.AcquireGlobal(context.Background(), 1); r == nil || outcome != "" || err != nil {
		t.Fatalf("nil controller global: r!=nil=%v outcome=%q err=%v, want admit", r != nil, outcome, err)
	}
}
