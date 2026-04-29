// Package obs implements F-OBS-001 observability layer (Usage Record query,
// audit event query, DLQ replay).
//
// Closely coupled with internal/billing for Tx2 atomic settlement.
// See docs/specs/observability-billing.md for the released spec.
// Phase 3 skeleton ONLY per DR-008.
package obs

import "context"

// Repository exposes operator query operations over usage_records,
// billing_ledger_claims, audit-event tables, and usage_record_dlq.
type Repository interface {
	QueryUsageRecords(ctx context.Context, filter UsageFilter) (*UsagePage, error)
	QueryClaims(ctx context.Context, filter ClaimFilter) (*ClaimPage, error)
	QueryAuditEvents(ctx context.Context, filter AuditFilter) (*AuditPage, error)
	ReplayDLQ(ctx context.Context, dlqID int64, actorID string) error
}

// UsageFilter, ClaimFilter, AuditFilter mirror admin API query parameters.
type UsageFilter struct {
	TenantID                  int64
	From, To                  string // RFC3339
	APIKeyID                  int64
	ProviderAccountID         int64
	Model                     string
	PendingReconciliationOnly bool
	Cursor                    string
	Limit                     int
}

type ClaimFilter struct {
	TenantID int64
	Status   string
	From     string
	Cursor   string
	Limit    int
}

type AuditFilter struct {
	TenantID   int64
	EventClass string // pool_routing | rate_limit | oauth_refresh
	ActorID    string
	From       string
	Cursor     string
	Limit      int
}

// PageMeta per docs/openapi/openapi.yaml.
type PageMeta struct {
	Cursor     string `json:"cursor,omitempty"`
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}

// UsagePage / ClaimPage / AuditPage are paginated result envelopes.
type UsagePage struct {
	Items []byte // raw JSON array; typed shape generated from openapi.yaml in Phase 4
	Page  PageMeta
}

type ClaimPage struct {
	Items []byte
	Page  PageMeta
}

type AuditPage struct {
	Items []byte
	Page  PageMeta
}

// TODO(phase-4): implement Repository against PostgreSQL via sqlc-generated
// queries; reconciliation worker for pending_reconciliation Usage Records;
// outbox consumer with lag metric.
