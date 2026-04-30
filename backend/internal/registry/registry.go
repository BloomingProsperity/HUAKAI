// Package registry resolves a public model alias and tenant id into a
// router.ResolvedModel — Slice 2 of the HUAKAI N+5 plan.
//
// Pipeline per docs/plans/2026-04-30-n5-model-registry.md:
//
//	parse alias -> AliasNormalize -> LookupTenantAlias
//	  IF tenant alias active   -> use it
//	  IF tenant alias disabled -> ErrModelDisabled (D3 explicit deny)
//	  IF tenant alias missing  -> if inherit_global_catalog
//	                              -> LookupGlobalAlias
//	                              else ErrUnknownModel
//	GetModelByID -> check status -> ListCapabilities -> ListBindings
//	-> stamp snapshot version -> ResolvedModel
//
// Boundary contracts (docs/specs/_invariants/cross-module-boundaries.md):
//   - CMB-1: registry NEVER reads provider_accounts.credentials, OAuth
//     tokens, or api_keys.key_hash. Returns metadata only.
//   - CMB-2: rate caps (rpm_limit/tpm_limit/max_parallel_requests) are
//     integer counts NOT decimal cost. Pool still computes no cost.
//   - CMB-7: PostgresRegistry.ResolveModel is SELECT-only. Admin writers
//     (Phase E) bump model_registry_snapshots.version in their own TX,
//     outside this package.

package registry

import "context"

// Registry is the interface implemented by PostgresRegistry. Mirrors the
// auth.APIKeyResolver shape so deps wiring stays uniform across layers.
type Registry interface {
	ResolveModel(ctx context.Context, publicAlias string, tenantID int64) (Resolved, error)
}

// Resolved is the registry-specific view of a resolved alias. The chat
// handler converts it into router.ResolvedModel + binding metadata.
//
// Why a separate type: ResolvedModel in router/route_plan.go is the
// router-input contract and intentionally does NOT carry binding-level
// metadata (rpm_limit, fallback_class, ...). Those live here so they
// can be threaded into Phase E rate gate without expanding the router
// surface area.
type Resolved struct {
	// Identity (mapped into router.ResolvedModel.PublicAlias /
	// InternalModelID / ProviderModelID).
	PublicAlias        string
	CanonicalModelID   string
	ProviderModelID    string

	// Capabilities + protocol — plain metadata fed into router.
	ContextWindow      int
	Capabilities       []string
	PricingClass       string
	ProtocolFamily     string
	RequestTimeoutMS   int

	// Routing — pool candidates ordered by binding priority then id.
	// Slice 5 will plumb selection_mode + weight; L0 always honors
	// PoolCandidates[0] only (AttemptBudget=1 documented limitation).
	PoolCandidates  []int64
	BindingMetadata []BindingMetadata

	// Snapshot stamp (D6). Format: "registry:<tenant_id>:<version>".
	// Router concatenates its own policy version when writing to
	// RoutePlan.SnapshotVersion / usage_records.snapshot_version.
	SnapshotVersion string
}

// BindingMetadata mirrors one model_pool_bindings row's reference-derived
// fields for downstream Phase E rate gate / Slice 5 weighted executor.
// At L0 the chat handler reads only PoolGroupID (via Resolved.PoolCandidates).
type BindingMetadata struct {
	BindingID                  int64
	PoolGroupID                int64
	Priority                   int32
	Weight                     int32
	SelectionMode              string  // 'strict_priority' | 'priority_weighted'
	ProviderModelIDOverride    *string // nullable; one-api ModelMapping analogue
	RPMLimit                   *int32  // LiteLLM proxy/_types KeyRequestBase.rpm_limit analogue
	TPMLimit                   *int32
	MaxParallelRequests        *int32  // LiteLLM GenerateRequestBase.max_parallel_requests analogue
	FallbackClass              string  // 'normal'|'context_window'|'safety'|'quota'|'manual'
}
