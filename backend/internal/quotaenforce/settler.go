package quotaenforce

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/quota"
)

const DefaultLeaseTTL = 90 * time.Second

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
		return result, err
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
	if errors.Is(err, quota.ErrReservationNotFound) {
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
	return err
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
