// Package registry 把公开模型别名与租户 id 解析为
// router.ResolvedModel——HUAKAI 模型注册表计划的第 2 个切片。
//
// 流水线参见 docs/process/plans/2026-04-30-n5-model-registry.md:
//
//	解析 alias -> AliasNormalize -> LookupTenantAlias
//	  若 tenant alias 处于 active   -> 使用它
//	  若 tenant alias 处于 disabled -> ErrModelDisabled(D3 显式拒绝)
//	  若 tenant alias 缺失          -> 若 inherit_global_catalog
//	                              -> LookupGlobalAlias
//	                              否则 ErrUnknownModel
//	GetModelByID -> 检查 status -> ListCapabilities -> ListBindings
//	-> 盖上 snapshot version -> ResolvedModel
//
// 边界契约(docs/specs/_invariants/cross-module-boundaries.md):
//   - registry 绝不读取 provider_accounts.credentials、OAuth
//     token 或 api_keys.key_hash。仅返回元数据。
//   - 速率上限(rpm_limit/tpm_limit/max_parallel_requests)是
//     整数计数,而非小数成本。Pool 仍不计算成本。
//   - PostgresRegistry.ResolveModel 只做 SELECT。Admin 写入方在
//     自己的事务中递增 model_registry_snapshots.version,
//     位于本包之外。

package registry

import (
	"context"
	"encoding/json"
)

// Registry 是由 PostgresRegistry 实现的接口。它对齐 auth.APIKeyResolver
// 的形态,使各层之间的依赖接线保持统一。
type Registry interface {
	ResolveModel(ctx context.Context, publicAlias string, tenantID int64) (Resolved, error)
}

// Resolved 是已解析别名在 registry 层的专用视图。chat handler 会把它
// 转换为 router.ResolvedModel + binding 元数据。
//
// 为什么保留独立类型：router.ResolvedModel 是 router 输入契约，只携带
// planner-safe 的 binding 元数据。Rate/quota 字段留在 registry 层，
// 供 rate gate 消费，避免扩大 router 职责面。
type Resolved struct {
	// 身份(映射到 router.ResolvedModel.PublicAlias /
	// InternalModelID / ProviderModelID)。
	PublicAlias            string
	CanonicalModelID       string
	DefaultProviderModelID string
	ProviderModelID        string

	// Capabilities + 协议——喂给 router 的纯元数据。
	ContextWindow    int
	Capabilities     []string
	PricingClass     string
	ProtocolFamily   string
	RequestTimeoutMS int

	// Routing — pool candidates 按 binding priority then id 排序。
	// Router PR1 扩展 multi-attempt plan 时保留该顺序。
	PoolCandidates  []int64
	BindingMetadata []BindingMetadata

	// 快照标记(D6)。格式:"registry:<tenant_id>:<version>"。
	// Router 在写入 RoutePlan.SnapshotVersion / usage_records.snapshot_version
	// 时会拼接其自身的 policy version。
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
	ProviderModelIDOverride *string // 可空;类比上游的 ModelMapping
	RPMLimit                *int32  // 每分钟请求上限
	TPMLimit                *int32
	MaxParallelRequests     *int32 // 最大并行请求数
	FallbackClass           string // 'normal'|'context_window'|'safety'|'quota'|'manual'

	// 渠道级请求/响应控制。在本切片中这些字段仅存于内存;
	// 零值保持既有行为不变。
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
	SensitiveWords                       []string // 选择性开启的关键词混淆;空 = 禁用
}
