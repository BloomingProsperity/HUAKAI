package router

import (
	"context"
)

// DefaultRouter is the L0 minimum implementation. It produces a single-
// attempt plan from ResolvedModel.PoolCandidates[0]. Slice 5 replaces it
// with a real planner that enumerates fallback candidates and emits
// multi-attempt plans against an Executor loop.
type DefaultRouter struct {
	// SnapshotVersion identifies the Router policy at planning time;
	// concatenated onto the Registry stamp in plan.SnapshotVersion so
	// audit replay can reconstruct both layers from one column.
	SnapshotVersion string
}

// NewDefaultRouter returns a Router whose Plan output for any input is a
// 1-attempt plan against ResolvedModel.PoolCandidates[0]. The Registry
// (N+5a) is responsible for ordering and filtering the candidate list;
// Router only takes the head.
func NewDefaultRouter() *DefaultRouter {
	return &DefaultRouter{SnapshotVersion: "v0.1-phase-c"}
}

// Plan implements Router. The minimum logic:
//
//  1. Validate the request has a non-empty RequestID, TenantID, and
//     ResolvedModel.ProtocolFamily.
//  2. Read PoolCandidates[0]; if absent return no_eligible_pool.
//  3. Concatenate Registry's snapshot with Router's policy version onto
//     RoutePlan.SnapshotVersion.
//  4. Emit a 1-attempt plan with that pool group + capabilities derived
//     from Features.
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
			Message: "ResolvedModel.PoolCandidates is empty; Registry should have surfaced ErrTenantNoAccess upstream",
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
		SnapshotVersion:     stampSnapshot(req.Model.SnapshotVersion, r.SnapshotVersion),
	}, nil
}

// requestPoolGroupID resolves the pool group for one Plan input. After
// N+5b the only carrier is Registry-resolved PoolCandidates[0]; the old
// ExplicitPoolGroupID escape hatch is removed.
func requestPoolGroupID(req PlanInput) int64 {
	if len(req.Model.PoolCandidates) > 0 {
		return req.Model.PoolCandidates[0]
	}
	return 0
}

// stampSnapshot concatenates the Registry stamp (e.g. "registry:42:7")
// with the Router policy version (e.g. "v0.1-phase-c") into the audit
// replay format documented in migration 0008
// (registry:<tid>:<v>;router:<router_policy_v>). Empty model stamp falls
// back to "registry:unknown" so the format is never broken.
func stampSnapshot(modelStamp, routerVersion string) string {
	if modelStamp == "" {
		modelStamp = "registry:unknown"
	}
	return modelStamp + ";router:" + routerVersion
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

// Compile-time assertion that DefaultRouter implements Router.
var _ Router = (*DefaultRouter)(nil)
