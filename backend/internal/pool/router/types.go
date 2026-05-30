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
	TenantID         int64
	UserID           int64
	APIKeyID         int64
	PoolGroupID      int64
	RequestedModel   string
	EndpointFamily   string
	CapabilityFlags  []string
	SessionHash      string
	ContinuationKey  string
	ExcludedAccounts map[int64]struct{}
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

// SelectionResult 是 Phase C 输出：已拿到的 Provider Account 或等待计划。
type SelectionResult struct {
	AccountID         int64
	AcquisitionToken  uuid.UUID
	WaitPlan          *WaitPlan
	RoutingReasonJSON []byte
}

// WaitPlan 描述 Layer 3 fallback 下的一次排队 admission 尝试。
type WaitPlan struct {
	AccountID      int64
	MaxConcurrency int
	TimeoutMS      int
	MaxWaiting     int
}

type AccountSnapshot struct {
	ID               int64
	TenantID         int64
	Priority         int
	LoadRate         float64
	LastUsedAt       time.Time
	MaxConcurrency   int
	WaitTimeoutMS    int
	MaxWaiting       int
	HealthState      string
	HealthStateUntil time.Time
}

type RoutingPolicy struct {
	ModelAccountIDs      map[string][]int64
	TopKDefault          int
	BroadTopK            bool
	OperatorScoring      bool
	ScoringPolicyVersion string
	FallbackTimeoutMS    int
	FallbackMaxWaiting   int
}

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
