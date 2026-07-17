package quotaenforce

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/quota"
)

// DefaultLeaseTTL 并发槽租约窗口, 与 billing claim 租约同源派生 —— 槽和 claim 一样
// 必须活过整个请求生命周期(流式可达 HUAKAI_STREAM_TOTAL_TIMEOUT 默认 600s)。
// acquire DB 函数在 COUNT 前会清扫已过 lease 的槽: 窗口短于请求时长时, 长流的槽
// 中途被当空位扫掉、新请求顶上, 并发上限被静默突破。正常路径 settle/abort 即时释放,
// 本窗口只兜真孤儿(进程崩溃), 代价仅是崩溃后槽位回收延迟变长。
const DefaultLeaseTTL = billing.DefaultClaimLeaseWindow

type Reserver interface {
	Reserve(context.Context, quota.ReserveRequest) (quota.ReserveResult, error)
}

type Finalizer interface {
	Settle(context.Context, quota.SettleRequest) (quota.SettleResult, error)
	Release(context.Context, quota.ReleaseRequest) (quota.ReleaseResult, error)
	CommitCacheHit(context.Context, quota.CacheHitRequest) (quota.CacheHitResult, error)
}

type ReserveInput struct {
	TenantID           int64
	UserID             int64
	APIKeyID           int64
	ClaimID            int64
	PoolGroupID        int64
	RequestFingerprint string
	RequestedModel     string
	ReservedTokens     int64
	PredictedCost      decimal.Decimal
	At                 time.Time
	LeaseExpiresAt     time.Time
}

type Settler struct {
	inner billing.Settler
	quota Finalizer
}

func NewSettler(inner billing.Settler, quotaFinalizer Finalizer) billing.Settler {
	if quotaFinalizer == nil {
		return inner
	}
	return &Settler{inner: inner, quota: quotaFinalizer}
}

func BuildReserveRequest(input ReserveInput) quota.ReserveRequest {
	at := input.At
	if at.IsZero() {
		at = time.Now().UTC()
	} else {
		at = at.UTC()
	}
	leaseExpiresAt := input.LeaseExpiresAt
	if leaseExpiresAt.IsZero() {
		leaseExpiresAt = at.Add(DefaultLeaseTTL)
	} else {
		leaseExpiresAt = leaseExpiresAt.UTC()
	}
	return quota.ReserveRequest{
		TenantID:            input.TenantID,
		ClaimID:             input.ClaimID,
		RequestFingerprint:  input.RequestFingerprint,
		Scopes:              Scopes(input.TenantID, input.UserID, input.APIKeyID, input.PoolGroupID),
		RequestedModel:      input.RequestedModel,
		ReservedTokens:      input.ReservedTokens,
		PredictedCost:       input.PredictedCost,
		NeedConcurrencySlot: true,
		LeaseExpiresAt:      leaseExpiresAt,
		At:                  at,
	}
}

func Scopes(tenantID int64, userID int64, apiKeyID int64, poolGroupID int64) []quota.Scope {
	scopes := []quota.Scope{{TenantID: tenantID, Kind: quota.ScopeGlobal, ID: "*"}}
	if userID > 0 {
		scopes = append(scopes, quota.Scope{TenantID: tenantID, Kind: quota.ScopeUser, ID: strconv.FormatInt(userID, 10)})
	}
	if apiKeyID > 0 {
		scopes = append(scopes, quota.Scope{TenantID: tenantID, Kind: quota.ScopeAPIKey, ID: strconv.FormatInt(apiKeyID, 10)})
	}
	if poolGroupID > 0 {
		scopes = append(scopes, quota.Scope{TenantID: tenantID, Kind: quota.ScopePoolGroup, ID: strconv.FormatInt(poolGroupID, 10)})
	}
	return scopes
}

func IsDenied(err error) bool {
	return quota.IsDenied(err)
}

// DenyRetryAfter 取配额拒绝决策里引擎算好的"距窗口重置还有多久"(window-bound
// metric 才非零;无窗口/无重置返回 0)。来源二选一:DenyError 包裹的 Decision,
// 或 fail-soft 路径上 !Allowed 的 ReserveResult.Decision。供 HTTP 层吐
// Retry-After / window_resets_at，让客户端按窗口边界智能退避。
func DenyRetryAfter(result quota.ReserveResult, err error) time.Duration {
	var deny *quota.DenyError
	if errors.As(err, &deny) {
		return deny.Decision.RetryAfter
	}
	if !result.Allowed {
		return result.Decision.RetryAfter
	}
	return 0
}

// DenyWindowKind 取配额拒绝决策命中的窗口种类标签(calendar_day/week/month/fixed 等),供 HTTP 层
// 在 429 里透出"是哪个窗口超了",让客户端区分日额/月额满。来源与 DenyRetryAfter 同(DenyError 包裹
// 的 Decision,或 fail-soft !Allowed 的 ReserveResult.Decision)。none/manual(无固定重置窗口)与未知
// 一律返回空串,调用方据此不透出窗口名 —— 既与 window_resets_at(对 manual/none 本就为空)解耦,
// 又保证对未配多窗口策略的租户零行为变化。
func DenyWindowKind(result quota.ReserveResult, err error) string {
	var deny *quota.DenyError
	if errors.As(err, &deny) {
		return windowKindLabel(deny.Decision.WindowKind)
	}
	if !result.Allowed {
		return windowKindLabel(result.Decision.WindowKind)
	}
	return ""
}

// windowKindLabel 把窗口种类归一为对外标签:无固定窗口(WindowNone)与空值不透出(返回空串),
// 其余(日历日/周/月、固定秒、手动)原样透出。
func windowKindLabel(kind quota.WindowKind) string {
	if kind == quota.WindowNone || kind == "" {
		return ""
	}
	return string(kind)
}

func (s *Settler) Settle(ctx context.Context, req billing.SettleRequest) (*billing.SettleResult, error) {
	if s == nil || s.inner == nil {
		return nil, billing.ErrPoolNotConfigured
	}
	result, err := s.inner.Settle(ctx, req)
	if err != nil {
		return result, err
	}
	if s.quota == nil {
		return result, nil
	}
	_, err = s.quota.Settle(ctx, quota.SettleRequest{
		TenantID:   req.TenantID,
		ClaimID:    req.ClaimID,
		ActualCost: actualCostForQuota(req),
	})
	if errors.Is(err, quota.ErrReservationNotFound) {
		return result, nil
	}
	if err != nil {
		// billing 已成功提交后, quota finalizer 是次级闸的 post-commit 补账动作;
		// 这里 fail-open 给客户端成功,并把异常交给指标/日志供后续对账处理。
		observePostCommitFinalizeFailure()
		slog.WarnContext(ctx, "quota settle finalizer failed after billing commit",
			slog.Int64("tenant_id", req.TenantID),
			slog.Int64("claim_id", req.ClaimID),
			slog.String("error", err.Error()))
		return result, nil
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
	if s.quota == nil {
		return nil
	}
	_, err := s.quota.Release(ctx, quota.ReleaseRequest{
		TenantID: tenantID,
		ClaimID:  claimID,
		Reason:   releaseReasonForQuota(reason),
	})
	if errors.Is(err, quota.ErrReservationNotFound) ||
		errors.Is(err, quota.ErrReleaseInvalidatedByRevival) ||
		errors.Is(err, quota.ErrReleaseDeferredForRevival) {
		return nil
	}
	return err
}

func (s *Settler) CommitCacheHit(ctx context.Context, req billing.SettleRequest) error {
	if s == nil || s.inner == nil {
		return billing.ErrPoolNotConfigured
	}
	if err := s.inner.CommitCacheHit(ctx, req); err != nil {
		return err
	}
	if s.quota == nil {
		return nil
	}
	_, err := s.quota.CommitCacheHit(ctx, quota.CacheHitRequest{
		TenantID:    req.TenantID,
		ClaimID:     req.ClaimID,
		CommittedAt: req.RequestedAt,
	})
	if errors.Is(err, quota.ErrReservationNotFound) {
		return nil
	}
	if err != nil {
		// 与 Settle 同语义:billing CommitCacheHit 已提交,quota 是次级闸的
		// post-commit 补账;fail-open 给客户端成功,异常进指标/WARN 供对账。
		observePostCommitFinalizeFailure()
		slog.WarnContext(ctx, "quota cache-hit finalizer failed after billing commit",
			slog.Int64("tenant_id", req.TenantID),
			slog.Int64("claim_id", req.ClaimID),
			slog.String("error", err.Error()))
	}
	return nil
}

func (s *Settler) Refund(ctx context.Context, req billing.RefundRequest) (*billing.RefundResult, error) {
	if s == nil || s.inner == nil {
		return nil, billing.ErrPoolNotConfigured
	}
	return s.inner.Refund(ctx, req)
}

func actualCostForQuota(req billing.SettleRequest) decimal.Decimal {
	actualCost := req.ActualCost
	if actualCost.IsZero() && !req.Draft.ActualCost.IsZero() {
		actualCost = req.Draft.ActualCost
	}
	return billing.CostForAttempt(actualCost, billing.AttemptFromSettleRequest(req))
}

func releaseReasonForQuota(reason string) string {
	reason = strings.TrimSpace(reason)
	switch reason {
	case "abort", "upstream_error", "caller_cancelled", "pre_billing_failure":
		return reason
	case "client_cancelled":
		return "caller_cancelled"
	}
	switch {
	case strings.Contains(reason, "upstream"):
		return "upstream_error"
	case strings.Contains(reason, "client") || strings.Contains(reason, "caller"):
		return "caller_cancelled"
	case strings.Contains(reason, "audit") ||
		strings.Contains(reason, "cache") ||
		strings.Contains(reason, "canonical") ||
		strings.Contains(reason, "invalid") ||
		strings.Contains(reason, "pricing"):
		return "pre_billing_failure"
	default:
		return "abort"
	}
}

var _ billing.Settler = (*Settler)(nil)
