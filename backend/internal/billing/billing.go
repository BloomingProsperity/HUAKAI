// Package billing implements F-OBS-001 + F-BILL-001 framing:
// Tx1/Tx2 atomic billing with Usage Record finalization.
//
// See docs/specs/observability-billing.md for the released spec.
// Current slice includes PostgreSQL-backed ClaimGate and DefaultSettler
// implementations. Dynamic pricing, outbox emission, and reconciliation
// workers remain Phase E+ work.
package billing

import (
	"context"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// ClaimGate runs the Tx1 reservation transaction per spec §Tx1.
type ClaimGate interface {
	// Reserve opens a serializable transaction with row-locks in the fixed
	// six-row lock order, computes idempotency_key, looks up or inserts the
	// claim row, reserves quota across 5 dimensions, commits.
	Reserve(ctx context.Context, req ReserveRequest) (*ReserveResult, error)
}

// Settler runs the Tx2 reconcile transaction per spec §Tx2.
type Settler interface {
	// Settle commits Usage Record + audit billing event + claim status flip
	// + 5 atomic effects + cross-threshold scheduler outbox + Provider Account
	// in_flight_count decrement, all in one transaction. HUAKAI's improvement
	// over Sub2API which detaches Usage Record write.
	Settle(ctx context.Context, req SettleRequest) (*SettleResult, error)

	// Abort aborts the claim with usage_values=0 (terminal upstream failure
	// or AMBIGUOUS_USAGE end class). Tenant-scoped to prevent cross-tenant
	// abort via stale claim id.
	Abort(ctx context.Context, tenantID, claimID int64, reason string) error
}

// ReserveRequest carries Tx1 inputs.
type ReserveRequest struct {
	TenantID                   int64
	APIKeyID                   int64
	UserID                     int64
	LogicalRequestID           string
	EndpointFamily             string
	NormalizedPayloadHash      string
	RequestedModel             string
	PoolingGroupID             int64
	BillingPolicyVersion       string
	RequestClass               string
	PredictedCost              decimal.Decimal
	IdempotencyKeyClientHeader string
}

// ReserveResult identifies the claim row and whether a cached prior response applies.
type ReserveResult struct {
	ClaimID             int64
	CachedPriorResponse []byte // empty unless replay hit
	FingerprintConflict bool
	IdempotencyHit      bool
}

// SettleRequest carries Tx2 inputs.
type SettleRequest struct {
	ClaimID             int64
	AccountID           int64
	AcquisitionToken    uuid.UUID
	UsageRecordPayload  []byte // ready-to-insert
	BillingEventPayload []byte // ready-to-insert
	ActualCost          decimal.Decimal
	TenantID            int64
	APIKeyID            int64
	UserID              int64
	ProviderAccountID   int64
	AttemptSeq          int32
	RequestedModel      string
	RequestedAt         time.Time
	UpstreamModel       string
	Stream              bool
	Draft               gateway.UsageRecordDraft
	Fingerprint         string
	OutboxEmitter       func() bool
	// SnapshotVersion is the registry+router stamp produced by
	// router.Plan as of N+5b (format "registry:<tid>:<v>;router:<rv>").
	// Written into usage_records.snapshot_version so audit replay can
	// reconstruct the routing config that built this plan.
	SnapshotVersion string
}

// SettleResult is the Tx2 commit outcome.
type SettleResult struct {
	NewUserBalance       decimal.Decimal
	APIKeyQuotaExhausted bool
	OutboxEventsEnqueued int
}

// TODO(phase-e): replace placeholder pricing with versioned pricing tables,
// wire scheduler outbox emission, and add reconciliation workers for pending
// usage records.
