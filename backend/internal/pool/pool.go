// Package pool implements F-POOL-001: Provider Account Pool Selection.
//
// See docs/specs/pool-routing.md for the released spec.
// Current slice includes the L0 DefaultSelector, PostgreSQL account source,
// DB slot acquisition, and Pattern B claim writeback. Redis sticky state and
// scheduler-driven queueing remain Phase E+ work.
package pool

import (
	"context"

	"github.com/google/uuid"
)

// Selector chooses a Provider Account for a tenant request per the layered
// algorithm in docs/specs/pool-routing.md §Phase A-D.
type Selector interface {
	// Select runs the 5-layer selection (routing config → sticky-within-routing
	// → sticky-standalone → load-aware fresh → fallback queue) plus the
	// Phase C atomic admission with Pattern B writeback.
	Select(ctx context.Context, req SelectionRequest) (*SelectionResult, error)
}

// SelectionRequest carries Phase A candidate intent inputs.
type SelectionRequest struct {
	TenantID         int64
	UserID           int64
	APIKeyID         int64
	PoolGroupID      int64
	RequestedModel   string
	EndpointFamily   string
	CapabilityFlags  []string
	SessionHash      string
	ContinuationKey  string
	ExcludedAccounts map[int64]struct{}
	AttemptSeq       int
	ClaimID          int64
}

// SelectionResult is the Phase C output: Provider Account acquired or wait plan.
type SelectionResult struct {
	AccountID         int64
	AcquisitionToken  uuid.UUID
	WaitPlan          *WaitPlan
	RoutingReasonJSON []byte
}

// WaitPlan describes a queued admission attempt per Layer 3 fallback.
type WaitPlan struct {
	AccountID      int64
	MaxConcurrency int
	TimeoutMS      int
	MaxWaiting     int
}

// TODO(phase-e): add Redis-backed sticky state, queue/scheduler integration,
// and Serializable retry loops around slot acquisition conflicts.
