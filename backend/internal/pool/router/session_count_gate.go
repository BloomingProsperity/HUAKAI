package router

import (
	"context"

	"github.com/BloomingProsperity/HUAKAI/internal/sessioncap"
)

// GateFailureSessionCount is the failure reason when an account is pulled from
// rotation because adding the current session would exceed its max concurrent
// sessions cap.
const GateFailureSessionCount GateFailureReason = "session_count_limit"

// SessionCountGate implements Gate. It excludes an account from selection when:
//   - the account has a positive MaxSessions, AND
//   - the registry is non-nil, AND
//   - adding the request sessionHash as a NEW session would exceed the cap.
//
// In all other cases (MaxSessions==0, nil registry, existing session, under
// cap) the gate is a no-op -- fail-open by design so a bug can only make the
// cap less effective, never wrongly bench a healthy account.
type SessionCountGate struct {
	Registry *sessioncap.Registry // nil -> always allow (fail-open)
}


func (g SessionCountGate) Allow(_ context.Context, account *AccountSnapshot, req SelectionRequest) (bool, GateFailureReason, error) {
	if account == nil {
		return true, "", nil
	}
	max := account.MaxSessions
	if max <= 0 {
		// opt-in: limit not set -> always eligible (default safety)
		return true, "", nil
	}
	if g.Registry == nil {
		// no registry injected -> fail-open
		return true, "", nil
	}
	if g.Registry.WouldExceed(account.ID, req.SessionHash, max) {
		return false, GateFailureSessionCount, nil
	}
	return true, "", nil
}
