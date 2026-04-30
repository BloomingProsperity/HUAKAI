package router

import (
	"context"
	"errors"
)

// DefaultRouter is the Phase C v0.1 minimum implementation. It produces a
// single-attempt plan from the request's pool_group_id (no fallback, no
// cross-pool ordering). Slice 2+ replaces this with a real planner that
// reads a Model Registry snapshot and enumerates fallback candidates.
//
// The minimum impl exists so the Executor can switch to Router.Plan(...)
// today without changing observable behavior — Phase C smoke must stay
// green after the wiring change.
type DefaultRouter struct {
	// SnapshotVersion stamped on every plan; static for v0.1.
	SnapshotVersion string
}

// NewDefaultRouter returns a Router whose Plan output for any input is a
// 1-attempt plan against PlanInput.Context-derived pool group. Caller
// passes pool_group_id via ResolvedModel — Phase C handler reads it from
// the request body and threads it through Registry into ResolvedModel.
//
// In Slice 2 ResolvedModel will gain a richer "candidate pools" field;
// for now we synthesize a single-attempt plan from the input directly.
func NewDefaultRouter() *DefaultRouter {
	return &DefaultRouter{SnapshotVersion: "v0.1-phase-c"}
}

// Plan implements Router. The minimum logic:
//
//  1. Validate the request has a non-empty RequestID and TenantID.
//  2. Validate ResolvedModel.ProtocolFamily is non-empty.
//  3. Emit a 1-attempt plan whose PoolGroupID comes from the (Phase C v0.1)
//     RequestPoolGroupID() helper applied to the input — the chat handler
//     forwards the body's pool_group_id through ResolvedModel.PricingClass
//     for now; a richer carrier lands in Slice 2.
//
// This is intentionally a minimal "preserve current behavior" planner.
func (r *DefaultRouter) Plan(_ context.Context, req PlanInput) (RoutePlan, error) {
	if req.Context.RequestID == "" {
		return RoutePlan{}, &PlanError{
			Code:    "missing_request_id",
			Message: "RequestContext.RequestID must be set by chi middleware before Router.Plan",
		}
	}
	if req.Context.TenantID == 0 {
		return RoutePlan{}, &PlanError{
			Code:    "missing_tenant",
			Message: "RequestContext.TenantID must be resolved by Auth before Router.Plan",
		}
	}
	if req.Model.ProtocolFamily == "" {
		return RoutePlan{}, &PlanError{
			Code:    "model_unsupported",
			Message: "ResolvedModel.ProtocolFamily required (Phase C: 'anthropic_messages' or 'openai_chat')",
		}
	}

	poolGroupID := requestPoolGroupID(req)
	if poolGroupID == 0 {
		return RoutePlan{}, &PlanError{
			Code:    "no_eligible_pool",
			Message: "no pool_group_id resolvable from request (Slice 2 will resolve via Registry)",
		}
	}

	return RoutePlan{
		Attempts: []AttemptPlan{
			{
				Index:                0,
				PoolGroupID:          poolGroupID,
				RequiredCapabilities: requiredCapabilities(req.Features),
				MaxConcurrencyHint:   0,
				Reason:               "primary",
			},
		},
		AttemptBudget:       1,
		RetryableEndClasses: nil,
		SnapshotVersion:     r.SnapshotVersion,
	}, nil
}

// requestPoolGroupID resolves the pool group for one Plan input.
//
// Phase C v0.1 carrier rules (preference order):
//
//	1. PlanInput.ExplicitPoolGroupID (chat handler thread-through)
//	2. (TODO slice-2) ResolvedModel.PoolCandidates[0] when Registry lands
//
// Returns 0 when no carrier resolves; Plan() then emits no_eligible_pool.
func requestPoolGroupID(req PlanInput) int64 {
	if req.ExplicitPoolGroupID != 0 {
		return req.ExplicitPoolGroupID
	}
	// TODO(slice-2): return req.Model.PoolCandidates[0] after Registry lands.
	return 0
}

// requiredCapabilities maps Features into the capability strings the
// Pool's intra-pool gate uses. Order is stable so audit comparisons work.
func requiredCapabilities(f RequestFeatures) []string {
	caps := make([]string, 0, 4)
	if f.Stream {
		caps = append(caps, "stream")
	}
	if f.WantsToolUse {
		caps = append(caps, "tools")
	}
	if f.WantsVision {
		caps = append(caps, "vision")
	}
	if f.WantsJSON {
		caps = append(caps, "json")
	}
	return caps
}

// errPoolGroupRequired is the canned error returned when the caller passes
// PlanInput without a resolvable pool group. Phase C v0.1 chat handler
// avoids this by passing pool_group_id directly into AttemptPlan and
// calling DefaultRouter for validation only — Slice 2 wires the full path.
var errPoolGroupRequired = errors.New("router: pool_group_id required")

// PlanWithPoolGroupID is the Phase C escape hatch — the chat handler
// passes a known-good pool_group_id directly. This bypasses the not-yet-
// implemented Registry resolution. The output is the same shape as
// Plan(...) so the Executor sees no difference.
//
// Slice 2 deletes this method when Registry can resolve the pool from
// the model alias. To prevent the escape hatch from being a quieter
// validator than Plan(), this method delegates: it threads
// ExplicitPoolGroupID into the input and calls Plan() so the same
// RequestID + TenantID + ProtocolFamily guards apply (codex pass2 P2 fix).
func (r *DefaultRouter) PlanWithPoolGroupID(ctx context.Context, req PlanInput, poolGroupID int64) (RoutePlan, error) {
	if poolGroupID == 0 {
		return RoutePlan{}, errPoolGroupRequired
	}
	req.ExplicitPoolGroupID = poolGroupID
	return r.Plan(ctx, req)
}

// Compile-time assertion that DefaultRouter implements Router.
var _ Router = (*DefaultRouter)(nil)
