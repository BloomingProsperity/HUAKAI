package router

// RequestContext 是已解析的鉴权上下文 —— auth.ResolveInboundAuth 的输出。
// 它是纯粹的 struct，没有方法。Router 把它当作只读元数据处理。
type RequestContext struct {
	TenantID  int64
	UserID    int64
	APIKeyID  int64
	RequestID string // 由 chi middleware 设置；调用 Plan 时必须非空
}

// ResolvedModel 是 registry 解析出的 model 身份 —— registry.ResolveModel
// 的输出。自 N+5b 起完全由 Registry 填充；遗留的
// PlanInput.ExplicitPoolGroupID 后门已被移除。
type ResolvedModel struct {
	PublicAlias     string   // 客户端请求的名字，例如 "claude-3-5-sonnet"
	InternalModelID string   // 规范 id，例如 "anthropic/claude-3.5-sonnet-20241022"
	ProviderModelID string   // 上游 provider 的 id（不同 provider 可能不同）
	ContextWindow   int      // 最大 token 数
	Capabilities    []string // "stream" / "tools" / "vision" / "json"
	PricingClass    string   // 用于 pricing-table 查找的自由格式 tag；不是数字
	ProtocolFamily  string   // "openai_chat" / "anthropic_messages" 等

	// PoolCandidates 是 Registry 为这个 (alias, tenant) 对解析出的
	// pool_group_id 有序列表，先按 binding priority 再按 id 排序。
	// 下标 0 是主候选。自 N+5b 起，这是进入 Router.Plan 的唯一 pool
	// 载体；遗留的 ExplicitPoolGroupID 后门已被移除。
	PoolCandidates []int64

	// PoolMetadata 按 PoolGroupID 对齐可选 binding 元数据。为空时
	// Router 必须只按 PoolCandidates 顺序规划。
	PoolMetadata []PoolCandidateMeta

	// SnapshotVersion 是 registry.ResolveModel 生成的 Registry 部分
	// stamp："registry:<tenant_id>:<version>"。Router 在写入
	// RoutePlan.SnapshotVersion 时会拼接上它自己的策略版本。审计回放
	// 从 usage_records.snapshot_version（在 migration 0008 中加入）
	// 读回该值。
	SnapshotVersion string
}

// PoolCandidateMeta 表示 Router 当前实际消费的候选 pool binding 元数据。
// Registry 提供 Priority 硬分层顺序，Router 只在连续同优先级段内按 Weight
// 生成加权无放回顺序。
type PoolCandidateMeta struct {
	PoolGroupID     int64
	ProviderModelID string
	// Priority 是不可跨越的绑定级优先级；Router 不跨值交换，Registry
	// 输入排序保证数值更小的候选段在前。
	Priority int32
	// Weight 是同 Priority 候选间的相对权重；非正值按 1 消费，避免饿死。
	Weight int32
	// SelectionMode 透传 binding 的同优先级选号策略
	// (model_pool_bindings.selection_mode):""/"strict_priority" = 均匀 Shuffle,
	// "priority_weighted" = 按账号 static_weight 加权。dispatch 端据此填
	// SelectionRequest.SelectionMode,激活 pool/router 加权选号分支。
	SelectionMode string
}

// RequestFeatures 表达这次请求实际想要完成什么。Router 用它来过滤掉
// 缺少某项 capability 的 pool / provider。
type RequestFeatures struct {
	Stream       bool
	WantsToolUse bool
	WantsVision  bool
	WantsJSON    bool
	WantsAudio   bool
}

// RoutePlan 是 Router 的输出 —— 一个有序的 attempt 列表，Executor 应当
// 按顺序逐个尝试。每个 attempt 指定一个 pool group；之后由该 pool 决定
// claim 组内具体哪个 account。
type RoutePlan struct {
	// Attempts 在 Plan 成功时非空。顺序很重要：Executor 先尝试
	// Attempts[0]，遇到可重试失败后再尝试 [1]，依此类推。
	Attempts []AttemptPlan

	// AttemptBudget 限定 Executor 总共会做的 attempt 数。当 Router
	// 枚举的候选数超过每租户重试预算允许的数量时，列表长度可能超过它；
	// Executor 在该上限处停止。
	AttemptBudget int

	// RetryableEndClasses 是 Executor 可以重试的 F-GW-002 流式 end class
	// 集合。集合之外的任何情况都会用原始错误终止循环。Nil 表示
	// “不重试，只做第 1 次 attempt”。
	RetryableEndClasses []string

	// SnapshotVersion 标识用于构建该 plan 的 Registry/策略快照。记录在
	// 每条 usage_record/billing_event 上
	SnapshotVersion string
}

// AttemptPlan 描述一次上游尝试。Executor 收到它后，请求 Pool 去 Claim
// 一个匹配该 Plan 的资源，然后请求 Adapter 经由该资源 Forward。
type AttemptPlan struct {
	// Index 是在父 RoutePlan.Attempts 切片中的下标。在整个请求生命周期
	// 内保持稳定 —— 在 Slice 3 的 schema migration（会在 usage_records
	// 上新增一个真正的 attempt_id 列）落地前，它被用作 attempt_id
	// 推导的一部分。
	Index int

	// PoolGroupID 是路由层级的决策：进入哪个 pool。之后由该 Pool 运行
	// 它的 9-gate 池内选号。
	PoolGroupID int64

	// RequiredCapabilities 是 Pool 在过滤 account 时必须强制要求的
	// ResolvedModel.Capabilities 子集。之所以单独存储，是因为某些
	// attempt 可能放宽 capability 要求（例如重试时回退到一个非 vision
	// 的 model）。
	RequiredCapabilities []string

	// MaxConcurrencyHint 可用于在多个 account 都 eligible 时，优先选择
	// 处于并发上限以内的 account。它是提示而非约束 —— Pool 可以基于
	// 池内健康/负载将其覆盖。
	MaxConcurrencyHint int

	// Reason 是描述该 attempt 为何出现在 plan 中的简短 tag
	// （例如 "primary"、"fallback_after_5xx"、"cheaper_alt"）。记录在
	// 审计中；不做强制。
	Reason string

	// UpstreamModelID 是本次 pool binding 对应的真实上游 model id。
	// 同一 public alias 跨 pool failover 时可能不同。
	UpstreamModelID string
}
