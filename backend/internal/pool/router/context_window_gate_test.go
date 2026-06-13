package router

import (
	"context"
	"errors"
	"testing"
)

// ctxWindowReq builds a SelectionRequest carrying the three context-window
// inputs the gate reads.
func ctxWindowReq(estimate, window, maxOut int) SelectionRequest {
	return SelectionRequest{
		TenantID:             1,
		EstimatedInputTokens: estimate,
		ModelContextWindow:   window,
		MaxOutputTokens:      maxOut,
	}
}

// TestContextWindowGate_OverBudget_Excluded proves the gate adds the reserved
// output room to the input estimate before comparing against the window.
//
// Mutation guard: drop the "+ MaxOutputTokens" term and 195000 alone (< 200000)
// wrongly allows the second case → red. Correct vs broken differ exactly on the
// output-reservation term.
func TestContextWindowGate_OverBudget_Excluded(t *testing.T) {
	gate := ContextWindowGate{}

	// 190000 input + 8192 output = 198192 < 200000 → allow.
	ok, _, err := gate.Allow(context.Background(), nil, ctxWindowReq(190000, 200000, 8192))
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("198192 fits in 200000 → must allow")
	}

	// 195000 input + 8192 output = 203192 > 200000 → exclude.
	ok, reason, err := gate.Allow(context.Background(), nil, ctxWindowReq(195000, 200000, 8192))
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("203192 overflows 200000 → must exclude (output reservation must be added)")
	}
	if reason != GateFailureContextWindow {
		t.Fatalf("reason=%q want %q", reason, GateFailureContextWindow)
	}
}

// TestContextWindowGate_UnknownWindow_FailOpen proves a zero/unset per-model
// window never benches an account, however large the estimate.
//
// Mutation guard: change the window<=0 guard to treat 0 as a real cap and the
// huge estimate would overflow 0 → red.
func TestContextWindowGate_UnknownWindow_FailOpen(t *testing.T) {
	gate := ContextWindowGate{}
	ok, _, err := gate.Allow(context.Background(), nil, ctxWindowReq(10_000_000, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("unknown window (0) must fail-open (allow) even for a huge estimate")
	}
}

// TestContextWindowGate_NoEstimate_FailOpen proves a zero/unwired estimate never
// benches an account, even when the reserved output alone exceeds the window.
//
// Mutation guard: remove the estimate<=0 short-circuit so 0+output (output>cap)
// overflows → red.
func TestContextWindowGate_NoEstimate_FailOpen(t *testing.T) {
	gate := ContextWindowGate{}
	// estimate=0, window=1000, output=5000: without the estimate short-circuit,
	// 0+5000 > 1000 would wrongly exclude.
	ok, _, err := gate.Allow(context.Background(), nil, ctxWindowReq(0, 1000, 5000))
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("no estimate (0) must fail-open (allow)")
	}
}

// TestContextWindowGate_AtExactBoundary_Allowed pins the strict-greater-than
// boundary: a request that fits exactly is allowed.
//
// Mutation guard: flip > to >= and the exact-fit case excludes → red.
func TestContextWindowGate_AtExactBoundary_Allowed(t *testing.T) {
	gate := ContextWindowGate{}
	// 191808 + 8192 == 200000 exactly.
	ok, _, err := gate.Allow(context.Background(), nil, ctxWindowReq(191808, 200000, 8192))
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("estimate+output == window must allow (strict > comparison)")
	}
}

// TestContextWindowGate_IgnoresAccount proves the gate decision is independent
// of the candidate account (context window is per-model, not per-account): the
// same overflowing request excludes regardless of which account is passed.
func TestContextWindowGate_IgnoresAccount(t *testing.T) {
	gate := ContextWindowGate{}
	req := ctxWindowReq(250000, 200000, 0)
	for _, acct := range []*AccountSnapshot{nil, {ID: 7}, {ID: 9, MaxSessions: 3}} {
		ok, reason, err := gate.Allow(context.Background(), acct, req)
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			t.Fatalf("overflow must exclude for account %+v", acct)
		}
		if reason != GateFailureContextWindow {
			t.Fatalf("reason=%q want %q", reason, GateFailureContextWindow)
		}
	}
}

// TestContextWindowGate_ChainOrdering_RecordsReason proves the gate is wired
// into DefaultGateChain's ordered() slot with the GateFailureContextWindow
// fallback reason — i.e. running the full chain on an overflowing request
// surfaces the context-window reason, not an empty/wrong one.
//
// Mutation guard: forget the ordered() entry and the chain never runs the gate
// → an overflowing request is allowed → red on the !ok assertion; if the
// fallback reason is wrong, the reason assertion goes red.
func TestContextWindowGate_ChainOrdering_RecordsReason(t *testing.T) {
	chain := DefaultGateChain()
	prepared := chain.ForSelection(context.Background(), ctxWindowReq(300000, 200000, 0))
	ok, reason, err := prepared.Allow(context.Background(), &AccountSnapshot{ID: 1, TenantID: 1}, ctxWindowReq(300000, 200000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("DefaultGateChain must exclude an overflowing request (gate not in ordered()?)")
	}
	if reason != GateFailureContextWindow {
		t.Fatalf("chain reason=%q want %q", reason, GateFailureContextWindow)
	}
}

// TestSelect_AllCandidatesOverflow_TriggersNoEligible proves the end-to-end
// graceful-degradation contract: when the single candidate's per-model window
// can't fit the request, Select returns ErrNoEligibleAccount (the no-capacity
// signal that drives the dispatch-layer model-fallback loop) — NOT a raw error.
//
// Mutation guard: if the gate hard-returned an error instead of (false,reason),
// Select would bubble that raw error and errors.Is(err, ErrNoEligibleAccount)
// would be false → red. If the gate were not in the chain, the account would be
// selected and err would be nil → red.
func TestSelect_AllCandidatesOverflow_TriggersNoEligible(t *testing.T) {
	accounts := []*AccountSnapshot{
		{
			ID:             101,
			TenantID:       7,
			Priority:       1,
			LoadRate:       0.01,
			MaxConcurrency: 4,
			HealthState:    "healthy",
			ProtocolFamily: "anthropic_messages",
		},
	}
	gates := DefaultGateChain()
	gates.ContextWindow = ContextWindowGate{}
	sel := NewDefaultSelector(
		&stubAccountSource{accounts: accounts},
		WithSlotManager(newMemSlotManager()),
		WithGateChain(gates),
	)

	_, err := sel.Select(context.Background(), SelectionRequest{
		TenantID:             7,
		RequestedModel:       "claude-3-5-sonnet",
		ProtocolFamily:       "anthropic_messages",
		EstimatedInputTokens: 250000,
		ModelContextWindow:   200000,
	})
	if !errors.Is(err, ErrNoEligibleAccount) {
		t.Fatalf("all candidates overflow must yield ErrNoEligibleAccount, got %v", err)
	}

	// Control: a fitting request on the SAME setup must select the account,
	// proving the no-eligible above is caused by the overflow, not the fixture.
	res, err := sel.Select(context.Background(), SelectionRequest{
		TenantID:             7,
		RequestedModel:       "claude-3-5-sonnet",
		ProtocolFamily:       "anthropic_messages",
		EstimatedInputTokens: 1000,
		ModelContextWindow:   200000,
	})
	if err != nil {
		t.Fatalf("fitting request must select an account, got err %v", err)
	}
	if res == nil || res.AccountID != 101 {
		t.Fatalf("fitting request should select account 101, got %+v", res)
	}
}
