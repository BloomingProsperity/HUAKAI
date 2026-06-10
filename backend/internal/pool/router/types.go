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
	// ModelCooldownKey is the upstream/provider model key used by
	// provider_accounts.model_rate_limits. Empty falls back to RequestedModel.
	ModelCooldownKey string
	// ProtocolFamily is the exact upstream protocol requested by registry
	// resolution, matching providers.upstream_protocol.
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
	AccountID         int64
	AcquisitionToken  uuid.UUID
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
	// Weight controls priority_weighted tie-band selection. 0/unset is
	// treated as 1 so legacy account sources keep uniform behavior.
	Weight           int32
	LoadRate         float64
	LastUsedAt       time.Time
	MaxConcurrency   int
	WaitTimeoutMS    int
	MaxWaiting       int
	HealthState      string
	HealthStateUntil time.Time
	ModelRateLimits  map[string]ModelRateLimit
	// WindowCostLimitCents is the operator-configured 5-hour session window
	// spend cap in cents (1/100 USD). 0 or negative means unlimited (opt-in).
	WindowCostLimitCents int64
	// MaxSessions is the operator-configured maximum concurrent active sessions
	// for this account. 0 means unlimited (opt-in, default safety).
	MaxSessions int
	// DisableCooling bypasses the health/cooldown gate for this account when true.
	// Default false = exact existing behavior. Opt-in escape hatch for high-value accounts.
	DisableCooling bool
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
