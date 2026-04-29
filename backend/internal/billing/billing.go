// Package billing implements F-OBS-001 + F-BILL-001 framing:
// Tx1/Tx2 atomic billing with Usage Record finalization.
//
// See docs/specs/observability-billing.md for the released spec.
// Phase 3 skeleton ONLY — no business logic per DR-008.
package billing

import (
	"context"

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
	// or AMBIGUOUS_USAGE end class).
	Abort(ctx context.Context, claimID int64, reason string) error
}

// ReserveRequest carries Tx1 inputs.
type ReserveRequest struct {
	TenantID                  int64
	APIKeyID                  int64
	UserID                    int64
	LogicalRequestID          string
	EndpointFamily            string
	NormalizedPayloadHash     string
	RequestedModel            string
	PoolingGroupID            int64
	BillingPolicyVersion      string
	RequestClass              string
	PredictedCost             decimal.Decimal
	IdempotencyKeyClientHeader string
}

// ReserveResult identifies the claim row and whether a cached prior response applies.
type ReserveResult struct {
	ClaimID                int64
	CachedPriorResponse    []byte // empty unless replay hit
	FingerprintConflict    bool
	IdempotencyHit         bool
}

// SettleRequest carries Tx2 inputs.
type SettleRequest struct {
	ClaimID                int64
	AccountID              int64
	AcquisitionToken       uuid.UUID
	UsageRecordPayload     []byte // ready-to-insert
	BillingEventPayload    []byte // ready-to-insert
	ActualCost             decimal.Decimal
}

// SettleResult is the Tx2 commit outcome.
type SettleResult struct {
	NewUserBalance         decimal.Decimal
	APIKeyQuotaExhausted   bool
	OutboxEventsEnqueued   int
}

// TODO(phase-4): implement ClaimGate + Settler against billing_ledger_claims,
// billing_events, usage_records, billing_ledger_adjustments tables per
// docs/schema/observability-billing.sql.
