package budgetenforce

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/budget"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/quota"
)

func TestReserverRunsBudgetBeforeQuotaAndShortCircuitsDeny(t *testing.T) {
	// Mutation check: delegating quota before budget increments quota.calls and
	// can consume durable quota for a request budget already rejected.
	b := &budgetStub{
		reserveResult: budget.ReserveResult{
			Allowed: false,
			Decision: budget.Decision{
				Code:       budget.CodeLimitExceeded,
				Counter:    budget.CounterRPM,
				RetryAfter: 17 * time.Second,
				Scope:      budget.Scope{TenantID: 1, Kind: budget.ScopeUser, ID: "2"},
			},
		},
		reserveErr: &budget.DenyError{},
	}
	q := &quotaStub{}
	reserver := NewReserver(b, q)

	res, err := reserver.Reserve(context.Background(), quota.ReserveRequest{
		TenantID: 1, ClaimID: 10, Scopes: []quota.Scope{{TenantID: 1, Kind: quota.ScopeUser, ID: "2"}},
	})
	if !quota.IsDenied(err) {
		t.Fatalf("err=%v want quota-style deny", err)
	}
	if res.Allowed {
		t.Fatalf("allowed=true want false")
	}
	if q.calls != 0 {
		t.Fatalf("quota calls=%d want 0", q.calls)
	}
	if res.Decision.RetryAfter != 17*time.Second {
		t.Fatalf("retry_after=%s want 17s", res.Decision.RetryAfter)
	}
}

func TestReserverReleasesBudgetWhenDelegateQuotaDenies(t *testing.T) {
	// Mutation check: deleting this release leaves RPM/TPM reserved for a request
	// that never enters the gateway path after durable quota denial.
	b := &budgetStub{reserveResult: budget.ReserveResult{Allowed: true}}
	q := &quotaStub{err: &quota.DenyError{Decision: quota.Decision{Kind: quota.DecisionDeny, Code: "quota_limit_exceeded"}}}
	reserver := NewReserver(b, q)

	_, err := reserver.Reserve(context.Background(), quota.ReserveRequest{
		TenantID: 1, ClaimID: 11, Scopes: []quota.Scope{{TenantID: 1, Kind: quota.ScopeUser, ID: "2"}},
	})
	if !quota.IsDenied(err) {
		t.Fatalf("err=%v want quota deny", err)
	}
	if b.releaseCalls != 1 || b.lastRelease.ClaimID != 11 {
		t.Fatalf("release calls/claim=%d/%d want 1/11", b.releaseCalls, b.lastRelease.ClaimID)
	}
}

func TestSettlerSettleAppliesActualTokenDeltaAfterInnerCommit(t *testing.T) {
	// Mutation check: running budget settlement before inner Settle charges TPM
	// even when billing fails; omitting output tokens undercounts actual usage.
	b := &budgetStub{}
	inner := &settlerStub{}
	settler := NewSettler(inner, b)
	req := billing.SettleRequest{
		TenantID: 3,
		ClaimID:  30,
		Draft: gateway.UsageRecordDraft{
			TokensInput:         123,
			TokensOutput:        77,
			CacheCreationTokens: 11,
			CacheReadTokens:     5,
		},
	}
	if _, err := settler.Settle(context.Background(), req); err != nil {
		t.Fatalf("settle: %v", err)
	}
	if inner.settleCalls != 1 {
		t.Fatalf("inner settle calls=%d want 1", inner.settleCalls)
	}
	if b.settleCalls != 1 || b.lastSettle.ActualTokens != 216 {
		t.Fatalf("budget settle calls/tokens=%d/%d want 1/216", b.settleCalls, b.lastSettle.ActualTokens)
	}
}

func TestSettlerDoesNotTouchBudgetWhenInnerSettleFails(t *testing.T) {
	// Mutation check: ignoring the inner error and reconciling budget would make
	// budget state diverge from failed durable billing.
	b := &budgetStub{}
	inner := &settlerStub{settleErr: errors.New("billing failed")}
	settler := NewSettler(inner, b)

	if _, err := settler.Settle(context.Background(), billing.SettleRequest{TenantID: 4, ClaimID: 40}); err == nil {
		t.Fatalf("settle err=nil want inner billing failure")
	}
	if b.settleCalls != 0 {
		t.Fatalf("budget settle calls=%d want 0", b.settleCalls)
	}
}

func TestSettlerAbortReleasesBudgetAfterInnerAbort(t *testing.T) {
	// Mutation check: omitting budget release leaves failed/aborted requests with
	// phantom RPM/TPM in their reserved minute.
	b := &budgetStub{}
	inner := &settlerStub{}
	settler := NewSettler(inner, b)

	if err := settler.Abort(context.Background(), 5, 50, "upstream_error", "req-1", 0, nil); err != nil {
		t.Fatalf("abort: %v", err)
	}
	if inner.abortCalls != 1 {
		t.Fatalf("inner abort calls=%d want 1", inner.abortCalls)
	}
	if b.releaseCalls != 1 || b.lastRelease.ClaimID != 50 {
		t.Fatalf("budget release calls/claim=%d/%d want 1/50", b.releaseCalls, b.lastRelease.ClaimID)
	}
}

type budgetStub struct {
	reserveResult budget.ReserveResult
	reserveErr    error
	lastReserve   budget.ReserveRequest

	settleCalls int
	lastSettle  budget.SettleRequest

	releaseCalls int
	lastRelease  budget.ReleaseRequest
}

func (s *budgetStub) Reserve(_ context.Context, req budget.ReserveRequest) (budget.ReserveResult, error) {
	s.lastReserve = req
	return s.reserveResult, s.reserveErr
}

func (s *budgetStub) Settle(_ context.Context, req budget.SettleRequest) error {
	s.settleCalls++
	s.lastSettle = req
	return nil
}

func (s *budgetStub) Release(_ context.Context, req budget.ReleaseRequest) error {
	s.releaseCalls++
	s.lastRelease = req
	return nil
}

type quotaStub struct {
	calls int
	err   error
}

func (s *quotaStub) Reserve(_ context.Context, req quota.ReserveRequest) (quota.ReserveResult, error) {
	s.calls++
	if s.err != nil {
		return quota.ReserveResult{Decision: quota.Decision{Kind: quota.DecisionDeny, Code: "quota_limit_exceeded"}}, s.err
	}
	return quota.ReserveResult{Allowed: true, Decision: quota.Decision{Kind: quota.DecisionAllow}, Reservation: quota.Reservation{TenantID: req.TenantID, ClaimID: req.ClaimID}}, nil
}

type settlerStub struct {
	settleCalls int
	settleErr   error
	abortCalls  int
}

func (s *settlerStub) Settle(context.Context, billing.SettleRequest) (*billing.SettleResult, error) {
	s.settleCalls++
	return &billing.SettleResult{}, s.settleErr
}

func (s *settlerStub) Abort(context.Context, int64, int64, string, string, int64, json.RawMessage) error {
	s.abortCalls++
	return nil
}

func (s *settlerStub) CommitCacheHit(context.Context, billing.SettleRequest) error {
	return nil
}

func (s *settlerStub) Refund(context.Context, billing.RefundRequest) (*billing.RefundResult, error) {
	return &billing.RefundResult{}, nil
}

var _ billing.Settler = (*settlerStub)(nil)

func TestQuotaReserveConversionCarriesModelAndTokenEstimate(t *testing.T) {
	// Mutation check: dropping RequestedModel/ReservedTokens prevents per-model
	// budgets and TPM pre-reserve from seeing the gateway estimate.
	req := ReserveRequestFromQuota(quota.ReserveRequest{
		TenantID:       9,
		ClaimID:        90,
		RequestedModel: "gpt-4o",
		ReservedTokens: 333,
		Scopes: []quota.Scope{
			{TenantID: 9, Kind: quota.ScopeAPIKey, ID: "700"},
			{TenantID: 9, Kind: quota.ScopeUser, ID: "70"},
			{TenantID: 9, Kind: quota.ScopePoolGroup, ID: "7"},
		},
	})
	if req.APIKeyID != 700 || req.UserID != 70 || req.PoolGroupID != 7 {
		t.Fatalf("converted ids key/user/group=%d/%d/%d want 700/70/7", req.APIKeyID, req.UserID, req.PoolGroupID)
	}
	if req.RequestedModel != "gpt-4o" || req.ReservedTokens != 333 {
		t.Fatalf("model/tokens=%q/%d want gpt-4o/333", req.RequestedModel, req.ReservedTokens)
	}
}

func TestActualTokensFromSettleRequestUsesStreamAttemptWhenPresent(t *testing.T) {
	// Mutation check: ignoring StreamAttempt undercounts streaming paths whose
	// Draft is not fully populated before settlement.
	req := billing.SettleRequest{
		Draft: gateway.UsageRecordDraft{TokensInput: 10, TokensOutput: 1},
		StreamAttempt: &billing.Attempt{
			DeliveredTokenCount: 20,
		},
	}
	if got := ActualTokensFromSettleRequest(req); got != 30 {
		t.Fatalf("actual tokens=%d want stream attempt 30", got)
	}
}

func TestBudgetDenyErrorCarriesQuotaDecisionCode(t *testing.T) {
	// Mutation check: mapping budget denial to a generic infra error makes the
	// existing handler fail-open instead of returning a deterministic 429.
	err := quotaDenyFromBudget(budget.ReserveResult{Decision: budget.Decision{Code: budget.CodeLimitExceeded}})
	if !quota.IsDenied(err) {
		t.Fatalf("err=%v want quota deny", err)
	}
}

func TestNoBudgetReturnsDelegate(t *testing.T) {
	// Mutation check: returning nil when budget is disabled would silently remove
	// the existing quota layer.
	q := &quotaStub{}
	reserver := NewReserver(nil, q)
	if _, err := reserver.Reserve(context.Background(), quota.ReserveRequest{TenantID: 1, ClaimID: 1}); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if q.calls != 1 {
		t.Fatalf("quota calls=%d want 1", q.calls)
	}
}
