package router

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNoEligibleAccount   = errors.New("pool has no eligible provider account")
	ErrAllChannelsDegraded = errors.New("pool has no eligible provider account: all_channels_degraded")
	ErrClaimRace           = errors.New("pool claim writeback race")
)

// NoCapacityError 包裹"池内无可用容量"哨兵错误,并携带池内账号最早恢复时刻——即所有被健康冷却
// (HealthStateUntil)或本模型限流(RateLimitResetAt)挡下的账号中,最早能重新可用的那个时刻。供
// HTTP 层据此算精确 Retry-After,替代硬编码常数。EarliestRecoveryAt 为零值表示无可估恢复时刻(账号
// 因非时间原因被挡或恢复时刻已过),调用方据此回退默认值。实现 Unwrap,使
// errors.Is(err, ErrNoEligibleAccount / ErrAllChannelsDegraded) 仍成立,既有分类逻辑不受影响。
type NoCapacityError struct {
	Cause              error
	EarliestRecoveryAt time.Time
}

func (e *NoCapacityError) Error() string {
	if e == nil || e.Cause == nil {
		return ErrNoEligibleAccount.Error()
	}
	return e.Cause.Error()
}

func (e *NoCapacityError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// Selector 按 docs/specs/pool-routing.md §Phase A-D 的分层算法为租户请求
// 选择 Provider Account。
type Selector interface {
	// Select 执行选择流水线和原子 admission writeback。
	Select(ctx context.Context, req SelectionRequest) (*SelectionResult, error)
}

// SelectionRequest 承载 Phase A 候选意图输入。
type SelectionRequest struct {
	TenantID       int64
	UserID         int64
	APIKeyID       int64
	PoolGroupID    int64
	RequestedModel string
	// ModelCooldownKey 是 provider_accounts.model_rate_limits 使用的
	// upstream/provider 模型键。为空时回退到 RequestedModel。
	ModelCooldownKey string
	// ProtocolFamily 是 registry 解析所请求的确切上游协议,
	// 与 providers.upstream_protocol 对应。
	ProtocolFamily   string
	EndpointFamily   string
	CapabilityFlags  []string
	SessionHash      string
	ContinuationKey  string
	ExcludedAccounts map[int64]struct{}
	PinnedAccountID  int64
	AttemptSeq       int
	ClaimID          int64

	// Vendor 来自 ResolvedModel.ProtocolFamily 派生的 vendor 字面量，用于
	// dispatcher 按 vendor 切片 metric；空字符串时不记 vendor 维度。
	Vendor string

	// UserGroup 是调用者订阅档 (users.user_group, 来自 auth.Identity)，供
	// GroupPolicyGate 按 routes.user_group_match 限制可用 pool_group。
	// 空字符串视同无限制 (向后兼容未接线 / 无订阅链路)。
	UserGroup string

	// SelectionMode 是本次请求命中 binding 的选号策略 (model_pool_bindings.selection_mode)，
	// 由 dispatch 端从 activeBindingMetadata 透传:""/"strict_priority" = 同优先级账号均匀
	// Shuffle (接线前一致行为);"priority_weighted" = 按账号 static_weight 加权选号。
	// 生产 RoutingPolicySource 据此字段返回 RoutingPolicy.SelectionMode,opt-in 激活加权分支,
	// 不设/默认时与接线前逐一字节一致 (非全局翻转)。
	SelectionMode string

	// EstimatedInputTokens 是本次请求 prompt 的输入 token 估算 (由 dispatch 端
	// 用 tokenestimate 启发式按 ProtocolFamily 算出)。<=0 视为"未接线/无估算",
	// ContextWindowGate 据此 fail-open。
	EstimatedInputTokens int
	// ModelContextWindow 是被请求模型的有效 context window 上限 (per-MODEL 属性,
	// 来自 registry.Resolved.ContextWindow / models.default_context_window)。
	// HUAKAI 的 context window 是按模型而非按账号配置, 故随 request 传入而非挂在
	// AccountSnapshot 上。<=0 视为"未配置/未知", ContextWindowGate 据此 fail-open。
	ModelContextWindow int
	// MaxOutputTokens 是客户端请求的 max_tokens (预留输出空间)。>0 时由
	// ContextWindowGate 加进 EstimatedInputTokens 后再与 ModelContextWindow 比较,
	// 保证为输出留位; 0 (未指定) 时不影响判定。
	MaxOutputTokens int

	// BindingID / BindingRPMLimit / BindingTPMLimit 是命中 binding 的 per-binding 限流上下文
	// (model_pool_bindings.id / rpm_limit / tpm_limit),由 dispatch 端从 activeBindingMetadata
	// 透传,供 BindingRateLimitSelector 做 per-binding RPM/TPM 预算闸。与 SelectionMode 同款透传:
	// 仅携带数据,是否真强制由该 selector 的计数器(env 门控)+ 限额是否 >0 决定。<=0 视为无该维度限额。
	BindingID       int64
	BindingRPMLimit int64
	BindingTPMLimit int64
	// MaxParallelRequests 是命中 binding 的全局在途上限。正数启用；0 或负数表示不限。
	// 外层 selector 只用它快速拒绝，真正抗并发的裁定仍在 DBSlotManager 的事务内完成。
	MaxParallelRequests int64
}

// StickyState 标记一次 Select 相对 sticky binding 的结果(DM-07)。
type StickyState string

const (
	// StickyStateNone 无 binding(短 prompt / TTL 过期 / 首次)。
	StickyStateNone StickyState = ""
	// StickyStateHit 命中绑定账号。
	StickyStateHit StickyState = "hit"
	// StickyStateMiss 有 binding 但选了别的账号(绑定账号被健康门/限流/
	// 重试排除集挡掉)。responses 链路据此剥 previous_response_id——
	// 跨账号的链 ID 原样转发上游必 404/400。
	StickyStateMiss StickyState = "miss"
)

// SelectionResult 是 Phase C 输出：已拿到的 Provider Account 或等待计划。
type SelectionResult struct {
	AccountID        int64
	AcquisitionToken uuid.UUID
	// Release 仅供不进入 billing settler 的短生命周期端点使用。计费端点必须继续
	// 让 settle/abort 按 claim 原子释放，不能在 handler 中提前调用。
	Release           ReleaseFunc
	WaitPlan          *WaitPlan
	RoutingReasonJSON []byte
	// StickyState 见上;只对 AccountID != 0 的结果有意义。
	StickyState StickyState
}

// WaitPlan 描述 Layer 3 fallback 下的一次排队 admission 尝试。
type WaitPlan struct {
	AccountID      int64
	MaxConcurrency int
	TimeoutMS      int
	MaxWaiting     int
}

type AccountSnapshot struct {
	ID             int64
	TenantID       int64
	ProtocolFamily string
	Priority       int
	// Weight 控制 priority_weighted 同分带的选号。0/未设置会被当作 1,
	// 使旧的账号来源保持均匀行为。
	Weight           int32
	LoadRate         float64
	LastUsedAt       time.Time
	MaxConcurrency   int
	WaitTimeoutMS    int
	MaxWaiting       int
	HealthState      string
	HealthStateUntil time.Time
	ModelRateLimits  map[string]ModelRateLimit
	// WindowCostLimitCents 是运营者配置的 5 小时会话窗口消费上限,单位为分
	// (1/100 美元)。0 或负数表示不限(选择性开启)。
	WindowCostLimitCents int64
	// MaxSessions 是运营者为该账号配置的最大并发活跃会话数。
	// 0 表示不限(选择性开启,默认安全)。
	MaxSessions int
	// DisableCooling 为 true 时让该账号绕过健康/冷却闸门。
	// 默认 false = 与既有行为逐字节一致。用于高价值账号的选择性逃生通道。
	DisableCooling bool
	// RPMLimit 是运营者为该账号配置的主动 requests-per-minute 预算(ROUTE-121)。
	// 0 或负数表示不限(选择性开启),因此未配置预算的账号保持其当前行为完全不变。
	RPMLimit int64
	// TPMLimit 是运营者为该账号配置的主动 tokens-per-minute 预算(ROUTE-121)。
	// 0 或负数表示不限(选择性开启)。
	TPMLimit int64
}

type ModelRateLimit struct {
	RateLimitResetAt time.Time
	Reason           string
}

type RoutingPolicy struct {
	ModelAccountIDs      map[string][]int64
	TopKDefault          int
	BroadTopK            bool
	OperatorScoring      bool
	SelectionMode        SelectionMode
	ScoringPolicyVersion string
	FallbackTimeoutMS    int
	FallbackMaxWaiting   int
}

type SelectionMode string

const (
	SelectionModeStrictPriority   SelectionMode = "strict_priority"
	SelectionModePriorityWeighted SelectionMode = "priority_weighted"
)

type AccountSource interface {
	ListAccounts(ctx context.Context, req SelectionRequest) ([]*AccountSnapshot, error)
}

type RoutingPolicySource interface {
	GetRoutingPolicy(ctx context.Context, req SelectionRequest) (*RoutingPolicy, error)
}

type StickyStore interface {
	Lookup(ctx context.Context, req SelectionRequest) (accountID int64, found bool, err error)
}

type ClaimGate interface {
	WriteAcquisition(ctx context.Context, tenantID, claimID, accountID int64, token uuid.UUID) error
}
