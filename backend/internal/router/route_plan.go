package router

// RequestContext is the resolved auth context — output of
// auth.ResolveInboundAuth. Plain struct, no methods. Router treats it as
// read-only metadata.
type RequestContext struct {
	TenantID  int64
	UserID    int64
	APIKeyID  int64
	RequestID string // chi middleware-set; must be non-empty when Plan is called
}

// ResolvedModel is the registry-resolved model identity — output of
// registry.ResolveModel. Populated entirely by the Registry as of N+5b;
// the legacy PlanInput.ExplicitPoolGroupID escape hatch is gone.
type ResolvedModel struct {
	PublicAlias        string   // what the client asked for, e.g. "claude-3-5-sonnet"
	InternalModelID    string   // canonical id, e.g. "anthropic/claude-3.5-sonnet-20241022"
	ProviderModelID    string   // upstream provider's id (may differ per provider)
	ContextWindow      int      // max tokens
	Capabilities       []string // "stream" / "tools" / "vision" / "json"
	PricingClass       string   // free-form tag for Phase E pricing-table lookup; not a number
	ProtocolFamily     string   // "openai_chat" / "anthropic_messages" / etc.

	// PoolCandidates is the ordered list of pool_group_id values the
	// Registry resolved for this (alias, tenant) pair, sorted by binding
	// priority then id. Index 0 is the primary candidate. As of N+5b
	// this is the ONLY pool carrier into Router.Plan; the legacy
	// ExplicitPoolGroupID escape hatch is gone.
	PoolCandidates []int64

	// SnapshotVersion is the Registry-portion stamp produced by
	// registry.ResolveModel: "registry:<tenant_id>:<version>". The Router
	// concatenates its own policy version when writing
	// RoutePlan.SnapshotVersion. Audit replay reads this back from
	// usage_records.snapshot_version (added in migration 0008).
	SnapshotVersion string
}

// RequestFeatures expresses what the request actually wants done. Used by
// the Router to filter pools / providers that lack a capability.
type RequestFeatures struct {
	Stream       bool
	WantsToolUse bool
	WantsVision  bool
	WantsJSON    bool
}

// RoutePlan is the Router's output — an ordered list of attempts the
// Executor should try in sequence. Each attempt names a pool group; the
// pool then decides which specific account inside that group to claim.
type RoutePlan struct {
	// Attempts is non-empty when Plan succeeds. Order is significant: the
	// Executor tries Attempts[0] first, then [1] on retryable failure, etc.
	Attempts []AttemptPlan

	// AttemptBudget caps total attempts the Executor will make. The list
	// length may exceed this when Router enumerates more candidates than
	// the per-tenant retry budget allows; the Executor stops at this cap.
	AttemptBudget int

	// RetryableEndClasses is the set of F-GW-002 stream end classes the
	// Executor MAY retry on. Anything outside this set terminates the
	// loop with the original error. Nil means "no retry, attempt 1 only".
	RetryableEndClasses []string

	// SnapshotVersion identifies the Registry/policy snapshot used to
	// build this plan. Recorded on every usage_record/billing_event for
	// audit-mode replay (codex B02).
	SnapshotVersion string
}

// AttemptPlan describes one upstream try. The Executor receives this,
// asks the Pool to Claim a resource matching the Plan, then asks the
// Adapter to Forward via that resource.
type AttemptPlan struct {
	// Index in the parent RoutePlan.Attempts slice. Stable across the
	// request lifecycle — used as part of the attempt_id derivation
	// pending the Slice 3 schema migration that adds a real attempt_id
	// column on usage_records.
	Index int

	// PoolGroupID is the route-level decision: which pool to enter. The
	// Pool then runs its 9-gate intra-pool selection.
	PoolGroupID int64

	// RequiredCapabilities is the subset of ResolvedModel.Capabilities
	// the Pool must enforce when filtering accounts. Stored separately
	// because some attempts may relax capability requirements (e.g.
	// fallback to a non-vision model on retry).
	RequiredCapabilities []string

	// MaxConcurrencyHint can be used to prefer accounts under a cap
	// when multiple are eligible. Hint, not constraint — Pool may
	// override based on intra-pool health/load.
	MaxConcurrencyHint int

	// Reason is a short tag describing why this attempt is in the plan
	// (e.g. "primary", "fallback_after_5xx", "cheaper_alt"). Recorded
	// in audit; not enforced.
	Reason string
}
