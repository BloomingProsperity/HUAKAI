package router

import (
	"context"
	"errors"

	"github.com/BloomingProsperity/HUAKAI/internal/rate/precheck"
)

// ErrKeyRateLimited is returned by KeyRateLimitSelector when the authenticated
// API key has exhausted its per-minute request (RPM) or token (TPM) budget. The
// gateway maps it to HTTP 429. SEC-249/250.
var ErrKeyRateLimited = errors.New("pool: per-key rate limit exceeded")

// KeyRateLimitSelector enforces a per-authenticated-API-key RPM/TPM budget
// BEFORE account selection (SEC-249/250). Because the budget is keyed on the
// resolved APIKeyID — not the client IP — a caller cannot bypass it by rotating
// IPs, which the reactive IP-based path allowed. Limits are global (the same cap
// applies to every key) and sourced from config; a zero limit means unlimited,
// so the limiter is strictly opt-in and OFF by default.
//
// On a successful selection the request is recorded against the key's budget
// (reserve-on-select). A nil counter or APIKeyID<=0 makes the wrapper a
// transparent pass-through.
type KeyRateLimitSelector struct {
	inner   Selector
	counter *precheck.Counter
	limits  precheck.Limits
}

// NewKeyRateLimitSelector wraps inner with a per-key RPM/TPM budget. rpm/tpm <=0
// means that dimension is unlimited; both <=0 makes the wrapper inert.
func NewKeyRateLimitSelector(inner Selector, counter *precheck.Counter, rpm, tpm int64) *KeyRateLimitSelector {
	return &KeyRateLimitSelector{inner: inner, counter: counter, limits: precheck.Limits{RPM: rpm, TPM: tpm}}
}

func (s *KeyRateLimitSelector) active(req SelectionRequest) bool {
	return s.counter != nil && req.APIKeyID > 0 && (s.limits.RPM > 0 || s.limits.TPM > 0)
}

func estTokens(req SelectionRequest) int64 {
	if req.EstimatedInputTokens < 0 {
		return 0
	}
	return int64(req.EstimatedInputTokens)
}

func (s *KeyRateLimitSelector) Select(ctx context.Context, req SelectionRequest) (*SelectionResult, error) {
	if s.active(req) {
		if d := s.counter.Check(req.APIKeyID, s.limits, estTokens(req)); !d.Allowed {
			return nil, ErrKeyRateLimited
		}
	}
	res, err := s.inner.Select(ctx, req)
	if err == nil && res != nil && res.AccountID != 0 && res.WaitPlan == nil && s.active(req) {
		s.counter.Record(req.APIKeyID, estTokens(req))
	}
	return res, err
}

var _ Selector = (*KeyRateLimitSelector)(nil)
