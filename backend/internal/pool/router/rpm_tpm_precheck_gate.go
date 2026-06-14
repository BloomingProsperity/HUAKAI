package router

import (
	"context"

	"github.com/BloomingProsperity/HUAKAI/internal/rate/precheck"
)

// GateFailureRatePrecheck is the failure reason when an account is pulled from
// selection because admitting one more request would cross its operator
// configured per-minute request (RPM) or token (TPM) budget. This is the
// proactive counterpart to the reactive modelRateLimitGate, which only reacts
// after the upstream has already returned a 429 — by then the request has
// already failed. ROUTE-121.
const GateFailureRatePrecheck GateFailureReason = "rate_precheck_limit"

// RatePrecheckGate implements Gate. It excludes an account from selection when:
//   - the account has a positive RPMLimit or TPMLimit (opt-in), AND
//   - a precheck counter is injected, AND
//   - admitting one more request of req.EstimatedInputTokens tokens would cross
//     the account's current per-minute window budget.
//
// In all other cases (no limit configured, nil counter, nil account) it is a
// no-op — fail-open by design so a bug can only make the cap less effective,
// never wrongly bench a healthy account. The gate is read-only: the budget is
// consumed at dispatch time via Counter.Record, never here, so running the gate
// across every candidate account does not over-count.
type RatePrecheckGate struct {
	Counter *precheck.Counter // nil → always allow (fail-open)
}

// RatePrecheckGateIface is the named gate interface for the chain slot.
type RatePrecheckGateIface interface{ Gate }

func (g RatePrecheckGate) Allow(_ context.Context, account *AccountSnapshot, req SelectionRequest) (bool, GateFailureReason, error) {
	if account == nil || g.Counter == nil {
		return true, "", nil
	}
	lim := precheck.Limits{RPM: account.RPMLimit, TPM: account.TPMLimit}
	est := int64(req.EstimatedInputTokens)
	if est < 0 {
		est = 0
	}
	if d := g.Counter.Check(account.ID, lim, est); !d.Allowed {
		return false, GateFailureRatePrecheck, nil
	}
	return true, "", nil
}
