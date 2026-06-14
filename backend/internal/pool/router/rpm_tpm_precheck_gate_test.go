package router

import (
	"context"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/rate/precheck"
)

func precheckClock() func() time.Time {
	base := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return base }
}

// At the RPM budget the gate excludes the account; mutation guard: if the
// Counter.Check result is ignored this MUST go red.
func TestRatePrecheckGate_AtRPM_Excludes(t *testing.T) {
	c := precheck.New(time.Minute, precheckClock())
	acc := &AccountSnapshot{ID: 42, RPMLimit: 3}
	g := RatePrecheckGate{Counter: c}
	// Fill the budget by recording 3 requests for this account.
	for i := 0; i < 3; i++ {
		c.Record(42, 0)
	}
	ok, reason, err := g.Allow(context.Background(), acc, SelectionRequest{})
	if err != nil || ok || reason != GateFailureRatePrecheck {
		t.Fatalf("at-budget account must be excluded, got ok=%v reason=%q err=%v", ok, reason, err)
	}
	// Discriminating: a different account that hasn't spent its budget is allowed.
	other := &AccountSnapshot{ID: 99, RPMLimit: 3}
	if ok, _, _ := g.Allow(context.Background(), other, SelectionRequest{}); !ok {
		t.Fatalf("untouched account must still be allowed")
	}
}

// Below the budget the gate allows.
func TestRatePrecheckGate_UnderBudget_Allows(t *testing.T) {
	c := precheck.New(time.Minute, precheckClock())
	acc := &AccountSnapshot{ID: 7, RPMLimit: 5}
	c.Record(7, 0)
	g := RatePrecheckGate{Counter: c}
	if ok, _, _ := g.Allow(context.Background(), acc, SelectionRequest{}); !ok {
		t.Fatalf("1 of 5 used must allow")
	}
}

// TPM uses the request's EstimatedInputTokens so an oversized single request is
// pre-excluded before it can provoke an upstream token-rate 429.
func TestRatePrecheckGate_TPM_UsesEstimatedTokens(t *testing.T) {
	c := precheck.New(time.Minute, precheckClock())
	acc := &AccountSnapshot{ID: 8, TPMLimit: 100}
	g := RatePrecheckGate{Counter: c}
	req := SelectionRequest{EstimatedInputTokens: 101}
	if ok, reason, _ := g.Allow(context.Background(), acc, req); ok || reason != GateFailureRatePrecheck {
		t.Fatalf("oversized request must be excluded on tpm, got ok=%v reason=%q", ok, reason)
	}
	if ok, _, _ := g.Allow(context.Background(), acc, SelectionRequest{EstimatedInputTokens: 100}); !ok {
		t.Fatalf("a request exactly at the tpm budget must fit")
	}
}

// Fail-open: no configured limit, nil counter, and nil account all allow.
func TestRatePrecheckGate_FailOpen(t *testing.T) {
	c := precheck.New(time.Minute, precheckClock())
	// account with no limit set → always allowed even after heavy spend
	noLimit := &AccountSnapshot{ID: 5}
	for i := 0; i < 1000; i++ {
		c.Record(5, 1000)
	}
	if ok, _, _ := (RatePrecheckGate{Counter: c}).Allow(context.Background(), noLimit, SelectionRequest{}); !ok {
		t.Fatalf("account with no rpm/tpm limit must always be allowed")
	}
	// nil counter → fail-open
	if ok, _, _ := (RatePrecheckGate{}).Allow(context.Background(), &AccountSnapshot{ID: 1, RPMLimit: 1}, SelectionRequest{}); !ok {
		t.Fatalf("nil counter must fail-open")
	}
	// nil account → allow
	if ok, _, _ := (RatePrecheckGate{Counter: c}).Allow(context.Background(), nil, SelectionRequest{}); !ok {
		t.Fatalf("nil account must allow")
	}
}

// The default chain carries a fail-open RatePrecheck gate and the gate sits in
// the ordered chain (so the wiring is real, not dangling).
func TestRatePrecheckGate_InDefaultChainFailOpen(t *testing.T) {
	chain := DefaultGateChain()
	if chain.RatePrecheck == nil {
		t.Fatalf("default chain must wire a RatePrecheck gate")
	}
	var found bool
	for _, ng := range chain.ordered() {
		if ng.fallback == GateFailureRatePrecheck {
			found = true
		}
	}
	if !found {
		t.Fatalf("RatePrecheck gate must be in the ordered chain")
	}
	// Default chain (nil counter) must not exclude a budgeted account.
	ok, _, err := chain.Allow(context.Background(), &AccountSnapshot{ID: 3, RPMLimit: 1}, SelectionRequest{})
	if err != nil || !ok {
		t.Fatalf("default chain must fail-open for rate precheck, got ok=%v err=%v", ok, err)
	}
}
