// Package registry resolves a public model alias and tenant id into a
// router.ResolvedModel — Slice 2 of the HUAKAI model registry plan.
//
// Pipeline per docs/process/plans/2026-04-30-n5-model-registry.md:
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
//   - registry NEVER reads provider_accounts.credentials, OAuth
//     tokens, or api_keys.key_hash. Returns metadata only.
//   - rate caps (rpm_limit/tpm_limit/max_parallel_requests) are
//     integer counts NOT decimal cost. Pool still computes no cost.
//   - PostgresRegistry.ResolveModel is SELECT-only. Admin writers
//     bump model_registry_snapshots.version in their own TX,
//     outside this package.

package registry

import (
	"context"
	"encoding/json"
)

// Registry is the interface implemented by PostgresRegistry. Mirrors the
// auth.APIKeyResolver shape so deps wiring stays uniform across layers.
type Registry interface {
	ResolveModel(ctx context.Context, publicAlias string, tenantID int64) (Resolved, error)
}

// Resolved is the registry-specific view of a resolved alias. The chat
// handler converts it into router.ResolvedModel + binding metadata.
//
// 为什么保留独立类型：router.ResolvedModel 是 router 输入契约，只携带
// planner-safe 的 binding 元数据。Rate/quota 字段留在 registry 层，
// 供 rate gate 消费，避免扩大 router 职责面。
type Resolved struct {
	// Identity (mapped into router.ResolvedModel.PublicAlias /
	// InternalModelID / ProviderModelID).
	PublicAlias            string
	CanonicalModelID       string
	DefaultProviderModelID string
	ProviderModelID        string

	// Capabilities + protocol — plain metadata fed into router.
	ContextWindow    int
	Capabilities     []string
	PricingClass     string
	ProtocolFamily   string
	RequestTimeoutMS int

	// Routing — pool candidates 按 binding priority then id 排序。
	// Router PR1 扩展 multi-attempt plan 时保留该顺序。
	PoolCandidates  []int64
	BindingMetadata []BindingMetadata

	// Snapshot stamp (D6). Format: "registry:<tenant_id>:<version>".
	// Router concatenates its own policy version when writing to
	// RoutePlan.SnapshotVersion / usage_records.snapshot_version.
	SnapshotVersion string
}

// BindingMetadata 映射一行 model_pool_bindings，供 downstream router
// planning 与 rate gate 使用。
type BindingMetadata struct {
	BindingID               int64
	PoolGroupID             int64
	Priority                int32
	Weight                  int32
	SelectionMode           string  // 'strict_priority' | 'priority_weighted'
	ProviderModelIDOverride *string // nullable; one-api ModelMapping analogue
	RPMLimit                *int32  // LiteLLM proxy/_types KeyRequestBase.rpm_limit analogue
	TPMLimit                *int32
	MaxParallelRequests     *int32 // LiteLLM GenerateRequestBase.max_parallel_requests analogue
	FallbackClass           string // 'normal'|'context_window'|'safety'|'quota'|'manual'

	// Channel-level request/response controls. These fields are in-memory only
	// in this slice; zero values preserve the pre-existing behavior.
	SystemPrompt                         string
	SystemPromptOverride                 bool
	ForceFormat                          bool
	StatusCodeMapping                    map[int]int
	StripServiceTier                     bool
	StripInferenceGeo                    bool
	StripSpeed                           bool
	StripSafetyIdentifier                bool
	StripStreamOptionsIncludeObfuscation bool
	StripStore                           bool
	BodyParamStrips                      []string
	ParamOverride                        map[string]json.RawMessage
	SensitiveWords                       []string // opt-in keyword obfuscation; empty = disabled
}
