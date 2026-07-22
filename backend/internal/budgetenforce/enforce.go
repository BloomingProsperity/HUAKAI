package budgetenforce

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/budget"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/quota"
)

type Budget interface {
	Reserve(context.Context, budget.ReserveRequest) (budget.ReserveResult, error)
	Settle(context.Context, budget.SettleRequest) error
	Release(context.Context, budget.ReleaseRequest) error
}

type QuotaReserver interface {
	Reserve(context.Context, quota.ReserveRequest) (quota.ReserveResult, error)
}

type Reserver struct {
	budget Budget
	quota  QuotaReserver
	// failClosed 决定 budget 后端【基础设施故障】(非额度拒绝)时的取舍:false(默认)
	// fail-open 放行不阻断业务;true fail-close 拒绝,quota 安全优先。运维 env 自决。
	failClosed bool
}

func NewReserver(b Budget, q QuotaReserver) QuotaReserver {
	if b == nil {
		return q
	}
	return &Reserver{budget: b, quota: q, failClosed: budgetFailClosed()}
}

// budgetFailClosed 读运维开关 HUAKAI_BUDGET_FAIL_CLOSED(默认 false=fail-open,
// 保持现有默认行为)。设 true 后 budget 后端故障即拒绝而非放行。
func budgetFailClosed() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("HUAKAI_BUDGET_FAIL_CLOSED")), "true")
}

func (r *Reserver) Reserve(ctx context.Context, req quota.ReserveRequest) (quota.ReserveResult, error) {
	if r == nil {
		return quota.ReserveResult{Allowed: true, Decision: quota.Decision{Kind: quota.DecisionAllow}}, nil
	}
	if r.budget != nil {
		bres, err := r.budget.Reserve(ctx, ReserveRequestFromQuota(req))
		if budget.IsDenied(err) || (err == nil && !bres.Allowed) {
			decision := quotaDecisionFromBudget(bres)
			return quota.ReserveResult{Allowed: false, Decision: decision}, &quota.DenyError{Decision: decision, Cause: err}
		}
		if err != nil {
			if r.failClosed {
				// fail-close:budget 后端基础设施故障时拒绝而非放行(运维显式选 quota 安全优先)。
				decision := quota.Decision{Kind: quota.DecisionDeny, Code: "budget_fail_closed"}
				return quota.ReserveResult{Allowed: false, Decision: decision}, &quota.DenyError{Decision: decision, Cause: err}
			}
			return quota.ReserveResult{Allowed: true, Decision: quota.Decision{Kind: quota.DecisionAllow, Code: "budget_fail_open"}}, nil
		}
	}
	if r.quota == nil {
		return quota.ReserveResult{Allowed: true, Decision: quota.Decision{Kind: quota.DecisionAllow}}, nil
	}
	qres, err := r.quota.Reserve(ctx, req)
	if (quota.IsDenied(err) || (err == nil && !qres.Allowed)) && r.budget != nil {
		_ = r.budget.Release(ctx, budget.ReleaseRequest{TenantID: req.TenantID, ClaimID: req.ClaimID, Reason: "quota_denied"})
	}
	return qres, err
}

type Settler struct {
	inner  billing.Settler
	budget Budget
}

func NewSettler(inner billing.Settler, b Budget) billing.Settler {
	if b == nil {
		return inner
	}
	return &Settler{inner: inner, budget: b}
}

func (s *Settler) Settle(ctx context.Context, req billing.SettleRequest) (*billing.SettleResult, error) {
	if s == nil || s.inner == nil {
		return nil, billing.ErrPoolNotConfigured
	}
	result, err := s.inner.Settle(ctx, req)
	if err != nil {
		return result, err
	}
	if s.budget != nil {
		_ = s.budget.Settle(ctx, budget.SettleRequest{
			TenantID:     req.TenantID,
			ClaimID:      req.ClaimID,
			ActualTokens: ActualTokensFromSettleRequest(req),
		})
	}
	return result, nil
}

func (s *Settler) Abort(ctx context.Context, tenantID, claimID int64, reason, auditRequestID string, observedInputTokens int64, protocolLoss json.RawMessage) error {
	if s == nil || s.inner == nil {
		return billing.ErrPoolNotConfigured
	}
	if err := s.inner.Abort(ctx, tenantID, claimID, reason, auditRequestID, observedInputTokens, protocolLoss); err != nil {
		return err
	}
	if s.budget != nil {
		_ = s.budget.Release(ctx, budget.ReleaseRequest{TenantID: tenantID, ClaimID: claimID, Reason: reason})
	}
	return nil
}

func (s *Settler) CommitCacheHit(ctx context.Context, req billing.SettleRequest) error {
	if s == nil || s.inner == nil {
		return billing.ErrPoolNotConfigured
	}
	if err := s.inner.CommitCacheHit(ctx, req); err != nil {
		return err
	}
	if s.budget != nil {
		_ = s.budget.Settle(ctx, budget.SettleRequest{TenantID: req.TenantID, ClaimID: req.ClaimID, ActualTokens: 0})
	}
	return nil
}

func (s *Settler) Refund(ctx context.Context, req billing.RefundRequest) (*billing.RefundResult, error) {
	if s == nil || s.inner == nil {
		return nil, billing.ErrPoolNotConfigured
	}
	return s.inner.Refund(ctx, req)
}

func ReserveRequestFromQuota(req quota.ReserveRequest) budget.ReserveRequest {
	out := budget.ReserveRequest{
		TenantID:       req.TenantID,
		ClaimID:        req.ClaimID,
		RequestedModel: req.RequestedModel,
		ReservedTokens: req.ReservedTokens,
		At:             req.At,
	}
	for _, scope := range req.Scopes {
		id, err := strconv.ParseInt(strings.TrimSpace(scope.ID), 10, 64)
		if err != nil || id <= 0 {
			continue
		}
		switch scope.Kind {
		case quota.ScopeUser:
			out.UserID = id
		case quota.ScopeAPIKey:
			out.APIKeyID = id
		case quota.ScopePoolGroup:
			out.PoolGroupID = id
		}
	}
	return out
}

func quotaDecisionFromBudget(res budget.ReserveResult) quota.Decision {
	decision := res.Decision
	metric := quota.MetricRequests
	if decision.Counter == budget.CounterTPM {
		metric = quota.MetricTokensEstimated
	}
	return quota.Decision{
		Kind:       quota.DecisionDeny,
		Code:       firstNonEmpty(decision.Code, budget.CodeLimitExceeded),
		Reason:     firstNonEmpty(decision.Reason, "budget limit exceeded"),
		RetryAfter: decision.RetryAfter,
		Metric:     metric,
		Scope: quota.Scope{
			TenantID: decision.Scope.TenantID,
			Kind:     quotaScopeKind(decision.Scope.Kind),
			ID:       decision.Scope.ID,
		},
	}
}

func quotaDenyFromBudget(res budget.ReserveResult) error {
	decision := quotaDecisionFromBudget(res)
	return &quota.DenyError{Decision: decision, Cause: budget.ErrDenied}
}

func quotaScopeKind(kind budget.ScopeKind) quota.ScopeKind {
	switch kind {
	case budget.ScopeAPIKey:
		return quota.ScopeAPIKey
	case budget.ScopePoolGroup:
		return quota.ScopePoolGroup
	default:
		return quota.ScopeUser
	}
}

func ActualTokensFromSettleRequest(req billing.SettleRequest) int64 {
	input := int64(req.Draft.TokensInput)
	output := int64(req.Draft.TokensOutput)
	if req.StreamAttempt != nil {
		attempt := billing.AttemptFromSettleRequest(req)
		output = attempt.DeliveredTokenCount
	}
	total := input + output + cacheTokens(req.Draft)
	if total < 0 {
		return 0
	}
	return total
}

func cacheTokens(draft gateway.UsageRecordDraft) int64 {
	total := int64(draft.CacheCreationTokens + draft.CacheCreation5mTokens + draft.CacheCreation1hTokens + draft.CacheReadTokens)
	if total < 0 {
		return 0
	}
	return total
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

var _ billing.Settler = (*Settler)(nil)
