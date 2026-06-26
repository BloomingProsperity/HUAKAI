package hermesops

import (
	"context"
	"sort"
)

// roleRank orders admin roles from lowest to highest authority. A caller may
// run a tool iff their rank >= the tool's required-role rank. tenant_operator
// is the lowest allowed actor in this wave; platform_admin is above it.
func roleRank(role string) int {
	switch role {
	case RoleTenantOperator:
		return 1
	case RolePlatformAdmin:
		return 2
	default:
		return 0 // unknown / unauthenticated role: below every tool
	}
}

// RoleAllowed reports whether actorRole satisfies requiredRole. The
// tenant-scope check (CanIssueForTenant) is a SEPARATE authority enforced by the
// HTTP layer before dispatch; this function only checks the role floor.
func RoleAllowed(actorRole, requiredRole string) bool {
	return roleRank(actorRole) >= roleRank(requiredRole) && roleRank(actorRole) > 0
}

// Registry holds the registered diagnostic tools and performs RBAC + dispatch.
// It is constructed once at wiring time and is read-only thereafter (no
// concurrent registration), so it needs no lock for the request path.
type Registry struct {
	tools map[string]ToolSpec
}

// NewRegistry builds an empty registry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]ToolSpec)}
}

// Register adds a spec. A duplicate name overwrites (last-wins), matching the
// module-registry convention; wiring registers each tool exactly once.
func (r *Registry) Register(spec ToolSpec) {
	if r == nil || spec.Name == "" {
		return
	}
	r.tools[spec.Name] = spec
}

// Get returns a registered spec.
func (r *Registry) Get(name string) (ToolSpec, bool) {
	if r == nil {
		return ToolSpec{}, false
	}
	s, ok := r.tools[name]
	return s, ok
}

// List returns all specs sorted by name (stable output for GET /v1/hermes/tools).
func (r *Registry) List() []ToolSpec {
	if r == nil {
		return nil
	}
	out := make([]ToolSpec, 0, len(r.tools))
	for _, s := range r.tools {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Authorize checks the role floor for a tool WITHOUT running it. It returns
// ErrToolUnknown for an unregistered name and ErrToolForbidden when the role is
// below the tool's floor. The HTTP layer calls this (and CanIssueForTenant)
// before Run so a denial is recorded as a denied tool-call row.
func (r *Registry) Authorize(name, actorRole string) (ToolSpec, error) {
	spec, ok := r.Get(name)
	if !ok {
		return ToolSpec{}, ErrToolUnknown
	}
	if !RoleAllowed(actorRole, spec.RequiredRole) {
		return spec, ErrToolForbidden
	}
	return spec, nil
}

// Run authorizes then dispatches a READ-ONLY tool. It is the single read-only
// dispatch entry; it never bypasses the role floor, and it REFUSES a mutating
// tool (ErrNotMutating) so a state change can never sneak through the read-only
// path. Tenant-scope authorization is the caller's responsibility (enforced
// before Run via CanIssueForTenant) — Run trusts req.TenantID as already
// scope-checked.
func (r *Registry) Run(ctx context.Context, name string, req ToolRequest) (ToolResult, error) {
	spec, err := r.Authorize(name, req.Role)
	if err != nil {
		return ToolResult{}, err
	}
	if spec.Mutating {
		// A mutating tool must NEVER execute through the read-only path — it
		// bypasses dry-run/confirm + advisory lock + atomic audit. Fail closed.
		return ToolResult{}, ErrNotMutating
	}
	if spec.Run == nil {
		return ToolResult{}, ErrDependencyUnwired
	}
	return spec.Run(ctx, req)
}

// AuthorizeMutating authorizes a MUTATING tool's role floor WITHOUT running it,
// and refuses a read-only tool (ErrNotMutating) so the confirm path cannot be
// pointed at a diagnostic tool. Tenant-scope is enforced separately by the
// caller (the H1 middleware + the per-tool Resolve re-check against the target
// row's tenant).
func (r *Registry) AuthorizeMutating(name, actorRole string) (ToolSpec, error) {
	spec, err := r.Authorize(name, actorRole)
	if err != nil {
		return ToolSpec{}, err
	}
	if !spec.Mutating {
		return spec, ErrNotMutating
	}
	if spec.Resolve == nil || spec.Mutate == nil {
		return spec, ErrDependencyUnwired
	}
	return spec, nil
}

// ResolveProposal authorizes + DRY-RUN-resolves a MUTATING tool for the LLM-propose
// path and returns ONLY the read-only MutationPlan. It deliberately returns NEITHER
// the ToolSpec NOR any handle to Mutate, so the caller (the conversational internal
// tool handler) has no route to a state change — the LLM-propose path is
// STRUCTURALLY read-only (the gate is the absence of a Mutate handle, not a runtime
// check the caller could skip). The real mutation runs ONLY later, when the OPERATOR
// confirms via the separate operator-authenticated H1 confirm path.
//
// It refuses, fail-closed:
//   - a read-only tool (ErrNotMutating, via AuthorizeMutating);
//   - a mutating tool NOT marked Proposable (ErrNotProposable) — e.g. credential
//     rotation: an operator may drive it directly, the LLM never proposes it;
//   - an insufficient role (ErrToolForbidden, via the role floor).
//
// The returned plan carries the sanitized Preview + TargetType/TargetID the caller
// pins into the single-use correlation_id. Tenant scope is enforced inside Resolve
// (it re-checks the target row belongs to req.TenantID).
func (r *Registry) ResolveProposal(ctx context.Context, name, actorRole string, req ToolRequest) (MutationPlan, error) {
	spec, err := r.AuthorizeMutating(name, actorRole)
	if err != nil {
		return MutationPlan{}, err
	}
	if !spec.Proposable {
		return MutationPlan{}, ErrNotProposable
	}
	// READ-ONLY dry-run ONLY. spec.Mutate is never referenced on this path; the
	// state change happens only at operator-confirm time, through a different entry.
	return spec.Resolve(ctx, req)
}
