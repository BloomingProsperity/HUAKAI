package router

import (
	"context"

	"github.com/BloomingProsperity/HUAKAI/internal/windowcost"
)

// GateFailureWindowCost is the failure reason when an account is pulled from
// rotation because its 5-hour session window spend has reached the operator
// configured limit.
const GateFailureWindowCost GateFailureReason = "window_cost_limit"

// WindowCostGate implements Gate. It excludes an account from selection when:
//   - the account has a positive WindowCostLimitCents, AND
//   - the cache has a fresh entry for the account, AND
//   - the cached cost >= the limit.
//
// In all other cases (limit==0, cache miss, stale entry, nil reader) the gate
// is a no-op — fail-open by design so a bug can only make the cap less
// effective, never wrongly bench a healthy account.
type WindowCostGate struct {
	Reader windowcost.CostReader // nil → always allow (fail-open)
}

// WindowCostGateInterface is a named gate interface for the chain slot.
type WindowCostGateInterface interface{ Gate }

func (g WindowCostGate) Allow(_ context.Context, account *AccountSnapshot, _ SelectionRequest) (bool, GateFailureReason, error) {
	if account == nil {
		return true, "", nil
	}
	limit := account.WindowCostLimitCents
	if limit <= 0 {
		// opt-in: limit not set → always eligible
		return true, "", nil
	}
	if g.Reader == nil {
		// no cache injected → fail-open
		return true, "", nil
	}
	cents, fresh := g.Reader.CurrentCost(account.ID)
	if !fresh {
		// cache miss or stale → fail-open
		return true, "", nil
	}
	if cents >= limit {
		return false, GateFailureWindowCost, nil
	}
	return true, "", nil
}
