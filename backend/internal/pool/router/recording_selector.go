package router

import (
	"context"

	"github.com/BloomingProsperity/HUAKAI/internal/rate/precheck"
)

// RecordingSelector wraps a Selector and, after a concrete account selection,
// records one request (plus its estimated input tokens) against that account's
// RPM/TPM budget in the shared precheck.Counter. Paired with RatePrecheckGate
// this closes the ROUTE-121 proactive rate-limit loop: the gate reads the
// budget while choosing an account; this consumes the budget once a winner is
// committed, so the next request sees the updated counts (reserve-on-select).
//
// Only a concrete selection consumes budget — wait-plan admissions (Layer-3
// queue) and error/empty results are skipped, matching the "valid result"
// condition the dispatcher uses for shadow sampling. A nil counter makes Record
// a no-op, so the wrapper is safe to install unconditionally; the gateway only
// wraps when the proactive limiter is enabled.
type RecordingSelector struct {
	inner   Selector
	counter *precheck.Counter
}

// NewRecordingSelector wraps inner so a successful Select consumes rate budget.
func NewRecordingSelector(inner Selector, counter *precheck.Counter) *RecordingSelector {
	return &RecordingSelector{inner: inner, counter: counter}
}

func (s *RecordingSelector) Select(ctx context.Context, req SelectionRequest) (*SelectionResult, error) {
	res, err := s.inner.Select(ctx, req)
	if err == nil && res != nil && res.AccountID != 0 && res.WaitPlan == nil {
		est := int64(req.EstimatedInputTokens)
		if est < 0 {
			est = 0
		}
		s.counter.Record(res.AccountID, est)
	}
	return res, err
}

var _ Selector = (*RecordingSelector)(nil)
