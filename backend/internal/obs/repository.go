// Package obs is the read-only audit/observability surface for the
// HUAKAI ledger. Per docs/specs/_invariants/cross-module-boundaries.md
// CMB-7 this package writes nothing — every method is a SELECT. CMB-5
// guarantees credentials never appear in returned rows by virtue of the
// underlying SQL never selecting credential columns.
//
// Reader is what admin endpoints (Phase E) call to render usage,
// claim, audit, and billing-event lists. Phase C smoke does not exercise
// this package; integration tests do.

package obs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

// Reader is the public read API. All methods are tenant-scoped —
// the tenantID parameter is enforced server-side via SQL WHERE; cross-
// tenant reads are not possible through this interface.
type Reader interface {
	ListUsage(ctx context.Context, tenantID int64, page Page) ([]UsageRow, error)
	GetClaim(ctx context.Context, tenantID, claimID int64) (ClaimRow, error)
	ListBillingEvents(ctx context.Context, tenantID int64, eventTypeFilter string, page Page) ([]BillingEventRow, error)
	CountClaimsByStatus(ctx context.Context, tenantID int64) (map[string]int64, error)
}

// Page is the standard pagination parameter. Limit clamped to [1, 500];
// offset must be non-negative.
type Page struct {
	Limit  int32
	Offset int32
}

// ErrNotFound is returned by GetClaim when the (tenantID, claimID) tuple
// has no row — either it doesn't exist or it belongs to another tenant.
// We collapse both cases to ErrNotFound so cross-tenant probing is
// indistinguishable from "doesn't exist".
var ErrNotFound = errors.New("obs: not found")

// UsageRow is one row from usage_records, with the credential and
// secret-bearing columns deliberately absent. Timestamps are surfaced
// for chronological audit ordering (codex pass2 P2 fix).
type UsageRow struct {
	ID                    int64
	TenantID              int64
	ClaimID               int64
	APIKeyID              int64
	UserID                int64
	ProviderAccountID     int64
	AttemptSeq            int32
	TokensInput           int32
	TokensOutput          int32
	CacheCreationTokens   int32
	CacheReadTokens       int32
	ActualCost            decimal.Decimal
	EndClass              string
	UsageSource           string
	PendingReconciliation bool
	RequestedModel        string
	UpstreamModel         string // empty when null in DB
	Stream                bool
	RequestedAt           time.Time  // when the gateway received the request
	SettledAt             *time.Time // when Tx2 committed; nil means in-flight
}

// ClaimRow is the audit-mode view of one billing_ledger_claims row.
// ReservedAt is required (NOT NULL in schema); SettledAt is nil when
// the claim is still 'reserving'.
type ClaimRow struct {
	ID                   int64
	TenantID             int64
	APIKeyID             int64
	UserID               int64
	LogicalRequestID     string
	EndpointFamily       string
	RequestedModel       string
	PoolingGroupID       int64 // 0 when null
	BillingPolicyVersion string
	RequestClass         string
	ProviderAccountID    int64 // 0 when null (pre-acquire)
	AttemptSeq           int32
	PredictedCost        decimal.Decimal
	ActualCost           decimal.Decimal // zero when status='reserving'
	CurrencyCode         string
	Status               string
	AbortedReason        string // empty when not aborted
	RequestFingerprint   string
	ReservedAt           time.Time
	SettledAt            *time.Time
}

// BillingEventRow is the audit-grade event view. OccurredAt is the
// canonical chronological key for audit listings.
type BillingEventRow struct {
	ID               int64
	TenantID         int64
	ClaimID          int64
	EventType        string
	ActualCost       decimal.Decimal
	ActualCostSigned decimal.Decimal
	EndClass         string
	UsageSource      string
	Fingerprint      string
	OccurredAt       time.Time
}

// PgxReader is a Reader backed by sqlc.Queries. Construct via
// NewPgxReader.
type PgxReader struct {
	q *db.Queries
}

// NewPgxReader wraps a sqlc.Queries handle. Pass a *db.Queries
// derived from a pgxpool.Pool; the caller manages pool lifecycle.
func NewPgxReader(q *db.Queries) *PgxReader {
	return &PgxReader{q: q}
}

// ListUsage implements Reader.
func (r *PgxReader) ListUsage(ctx context.Context, tenantID int64, page Page) ([]UsageRow, error) {
	limit, offset := normalizePage(page)
	rows, err := r.q.ListUsageByTenant(ctx, db.ListUsageByTenantParams{
		TenantID:   tenantID,
		PageLimit:  limit,
		PageOffset: offset,
	})
	if err != nil {
		return nil, fmt.Errorf("obs: list usage: %w", err)
	}
	out := make([]UsageRow, 0, len(rows))
	for _, row := range rows {
		u := UsageRow{
			ID:                    row.ID,
			TenantID:              row.TenantID,
			ClaimID:               row.ClaimID,
			APIKeyID:              row.APIKeyID,
			UserID:                row.UserID,
			ProviderAccountID:     row.ProviderAccountID,
			AttemptSeq:            row.AttemptSeq,
			TokensInput:           row.TokensInput,
			TokensOutput:          row.TokensOutput,
			CacheCreationTokens:   row.CacheCreationTokens,
			CacheReadTokens:       row.CacheReadTokens,
			ActualCost:            row.ActualCost,
			EndClass:              row.EndClass,
			UsageSource:           row.UsageSource,
			PendingReconciliation: row.PendingReconciliation,
			RequestedModel:        row.RequestedModel,
			Stream:                row.Stream,
		}
		if row.UpstreamModel != nil {
			u.UpstreamModel = *row.UpstreamModel
		}
		if row.RequestedAt.Valid {
			u.RequestedAt = row.RequestedAt.Time
		}
		if row.SettledAt.Valid {
			t := row.SettledAt.Time
			u.SettledAt = &t
		}
		out = append(out, u)
	}
	return out, nil
}

// GetClaim implements Reader.
func (r *PgxReader) GetClaim(ctx context.Context, tenantID, claimID int64) (ClaimRow, error) {
	row, err := r.q.GetClaimByID(ctx, db.GetClaimByIDParams{ID: claimID, TenantID: tenantID})
	if errors.Is(err, pgx.ErrNoRows) {
		return ClaimRow{}, ErrNotFound
	}
	if err != nil {
		return ClaimRow{}, fmt.Errorf("obs: get claim: %w", err)
	}
	c := ClaimRow{
		ID:                   row.ID,
		TenantID:             row.TenantID,
		APIKeyID:             row.APIKeyID,
		UserID:               row.UserID,
		LogicalRequestID:     row.LogicalRequestID,
		EndpointFamily:       row.EndpointFamily,
		RequestedModel:       row.RequestedModel,
		BillingPolicyVersion: row.BillingPolicyVersion,
		RequestClass:         row.RequestClass,
		AttemptSeq:           row.AttemptSeq,
		PredictedCost:        row.PredictedCost,
		CurrencyCode:         row.CurrencyCode,
		Status:               row.Status,
		RequestFingerprint:   row.RequestFingerprint,
	}
	if row.PoolingGroupID != nil {
		c.PoolingGroupID = *row.PoolingGroupID
	}
	if row.ProviderAccountID != nil {
		c.ProviderAccountID = *row.ProviderAccountID
	}
	if row.ActualCost.Valid {
		c.ActualCost = row.ActualCost.Decimal
	}
	if row.AbortedReason != nil {
		c.AbortedReason = *row.AbortedReason
	}
	if row.ReservedAt.Valid {
		c.ReservedAt = row.ReservedAt.Time
	}
	if row.SettledAt.Valid {
		t := row.SettledAt.Time
		c.SettledAt = &t
	}
	return c, nil
}

// ListBillingEvents implements Reader. eventTypeFilter="" means no filter.
func (r *PgxReader) ListBillingEvents(ctx context.Context, tenantID int64, eventTypeFilter string, page Page) ([]BillingEventRow, error) {
	limit, offset := normalizePage(page)
	rows, err := r.q.ListBillingEventsByTenant(ctx, db.ListBillingEventsByTenantParams{
		TenantID:          tenantID,
		EventTypeFilter:   eventTypeFilter,
		PageLimit:         limit,
		PageOffset:        offset,
	})
	if err != nil {
		return nil, fmt.Errorf("obs: list billing_events: %w", err)
	}
	out := make([]BillingEventRow, 0, len(rows))
	for _, row := range rows {
		e := BillingEventRow{
			ID:               row.ID,
			TenantID:         row.TenantID,
			ClaimID:          row.ClaimID,
			EventType:        row.EventType,
			ActualCost:       row.ActualCost,
			ActualCostSigned: row.ActualCostSigned,
			Fingerprint:      row.Fingerprint,
		}
		if row.EndClass != nil {
			e.EndClass = *row.EndClass
		}
		if row.UsageSource != nil {
			e.UsageSource = *row.UsageSource
		}
		if row.OccurredAt.Valid {
			e.OccurredAt = row.OccurredAt.Time
		}
		out = append(out, e)
	}
	return out, nil
}

// CountClaimsByStatus implements Reader.
func (r *PgxReader) CountClaimsByStatus(ctx context.Context, tenantID int64) (map[string]int64, error) {
	rows, err := r.q.CountClaimsByStatus(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("obs: count claims by status: %w", err)
	}
	out := make(map[string]int64, len(rows))
	for _, r := range rows {
		out[r.Status] = r.ClaimCount
	}
	return out, nil
}

func normalizePage(p Page) (int32, int32) {
	limit := p.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	offset := p.Offset
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

var _ Reader = (*PgxReader)(nil)
