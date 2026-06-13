package router

import (
	"context"
)

// GateFailureContextWindow is the failure reason recorded when a candidate is
// dropped from selection because the request's estimated input tokens plus the
// reserved output room exceed the requested model's effective context window.
const GateFailureContextWindow GateFailureReason = "context_window"

// ContextWindowGate implements Gate. It is a pre-dispatch admission precheck:
// it compares the estimated prompt size (input tokens plus any client-reserved
// output room) against the per-MODEL context window carried on the
// SelectionRequest, and excludes the candidate when the request cannot fit.
//
// Context window in HUAKAI is a per-model property, not a per-account one, so
// (unlike WindowCostGate / SessionCountGate) this gate reads only request
// fields and ignores the AccountSnapshot entirely — every candidate for the
// same request shares the same window, so when one overflows they all do, which
// is exactly the signal that should fall through to model fallback.
//
// It NEVER hard-rejects. When all candidates overflow, Select returns
// ErrNoEligibleAccount, which the dispatch layer maps to no-capacity and routes
// into the existing model-fallback loop — graceful degradation, not a 4xx.
//
// Fail-open by default:
//   - ModelContextWindow <= 0 (unknown/unconfigured window) → always allow;
//   - EstimatedInputTokens <= 0 (no estimate wired) → always allow.
//
// Only when both are positive AND EstimatedInputTokens + reservedOutput strictly
// exceeds the window is the candidate excluded. The comparison is strict (>),
// so a request that fits exactly is allowed.
type ContextWindowGate struct{}

// ContextWindowGateIface is a named gate interface for the chain slot.
type ContextWindowGateIface interface{ Gate }

func (ContextWindowGate) Allow(_ context.Context, _ *AccountSnapshot, req SelectionRequest) (bool, GateFailureReason, error) {
	window := req.ModelContextWindow
	if window <= 0 {
		// Unknown / unconfigured per-model window → fail-open (never bench).
		return true, "", nil
	}
	estimate := req.EstimatedInputTokens
	if estimate <= 0 {
		// No prompt estimate wired for this request → fail-open.
		return true, "", nil
	}
	reservedOutput := req.MaxOutputTokens
	if reservedOutput < 0 {
		reservedOutput = 0
	}
	if estimate+reservedOutput > window {
		return false, GateFailureContextWindow, nil
	}
	return true, "", nil
}
