// Package router is the HUAKAI Router Engine — the cross-pool / cross-model /
// cross-cost / cross-policy decision tier. Per Owner directive 2026-04-30 +
// docs/specs/_invariants/cross-module-boundaries.md:
//
//   Router Engine    — decides which routes to try, in what order
//   Resource Pool    — decides which resource within ONE route can be claimed
//   Gateway Executor — runs the per-attempt loop (claim, forward, settle)
//
// This package MUST NOT import internal/auth (CMB-1: Router does not read
// credentials), MUST NOT hold decimal fields (CMB-2: cost lives in Ledger),
// and MUST NOT write to the database (CMB-7: Router writes nothing).
//
// The package is import-pure: caller flows are
//   Auth → Registry → Router.Plan(...) → Executor loop → Pool.Claim(...)

package router

import (
	"context"
)

// Router produces a plan describing which route(s) the executor should try
// for one inbound request. The plan is data only — no IO, no credential
// resolution, no DB writes.
type Router interface {
	Plan(ctx context.Context, req PlanInput) (RoutePlan, error)
}

// PlanInput bundles what the Router needs to decide: who's calling, what
// model they asked for, what they want done, and (Phase C v0.1) which
// pool group to target. Each piece comes from a different upstream layer
// (auth / registry / handler).
//
// Phase C v0.1: ExplicitPoolGroupID is a temporary carrier. The chat
// handler resolves pool_group_id from the request body and threads it
// here directly, since Slice 2 (Model Registry) hasn't landed yet. Once
// Registry is in place, ResolvedModel will carry candidate pools and
// this field gets removed. Plan() prefers ExplicitPoolGroupID when set.
type PlanInput struct {
	Context             RequestContext
	Model               ResolvedModel
	Features            RequestFeatures
	ExplicitPoolGroupID int64
}

// PlanError is the typed error Router returns when no plan is buildable.
type PlanError struct {
	Code    string // e.g. "no_eligible_pool", "model_unsupported", "policy_block"
	Message string
}

func (e *PlanError) Error() string { return e.Code + ": " + e.Message }
