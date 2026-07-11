// Package billing 实现 F-OBS-001 + F-BILL-001 框架:
// 带 Usage Record 终结的 Tx1/Tx2 原子计费。
//
// 已发布规格见 docs/specs/observability-billing.md。
// 当前切片包含基于 PostgreSQL 的 ClaimGate 与 DefaultSettler 实现。
// 动态定价精度与对账 worker 仍属 Phase E+ 工作;scheduler outbox 意图由
// SettleRequest 携带,因此直接结算与投递后恢复保持同一条证据链效果。
package billing

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

var ErrInsufficientBalance = errors.New("billing: insufficient balance")

// ClaimGate 按规格 §Tx1 执行 Tx1 预扣事务。
type ClaimGate interface {
	// Reserve 开启一个 serializable 事务,按固定的六行加锁顺序对各行加行锁,
	// 计算 idempotency_key,查找或插入 claim 行,跨 5 个维度预扣配额,提交。
	Reserve(ctx context.Context, req ReserveRequest) (*ReserveResult, error)
}

// Settler 按规格 §Tx2 执行 Tx2 对账事务。
type Settler interface {
	// Settle 在一个事务里提交 Usage Record + 审计 billing event + claim 状态翻转
	// + 5 项原子效果 + 跨阈值 scheduler outbox + Provider Account 的
	// in_flight_count 递减。这是 HUAKAI 相对参考实现的改进——参考实现把
	// Usage Record 写入剥离了出去。
	Settle(ctx context.Context, req SettleRequest) (*SettleResult, error)

	// Abort 以零成本中止 claim。observedInputTokens 是可选的仅审计输入用量,
	// 用于只有输入就被打断的流;所有成本仍为零。按租户限定,以防通过陈旧
	// claim id 跨租户中止。
	Abort(ctx context.Context, tenantID, claimID int64, reason, auditRequestID string, observedInputTokens int64, protocolLoss json.RawMessage) error

	// CommitCacheHit 把尚未 acquire pool account 的 reserving claim 以零成本
	// committed 终结 — 用于 L2 response cache 命中: 请求已成功返回缓存响应体,
	// 计费 0, 不能记成 aborted (否则审计把成功请求记成中止)。 无 acquisition_token
	// / pool slot / provider account, 故不释放 slot; 但写一条
	// settlement_source=response_cache_l2 的 provider-less usage_records 行,
	// 使 receipt / 用量视图与正常请求一致。 req 复用 SettleRequest。
	CommitCacheHit(ctx context.Context, req SettleRequest) error

	// Refund 给已提交 claim 追加幂等负向 reconciliation event；
	// 原 claim / usage 行保持不可变。
	Refund(ctx context.Context, req RefundRequest) (*RefundResult, error)
}

// ReserveRequest 携带 Tx1 的输入。
type ReserveRequest struct {
	TenantID                   int64
	APIKeyID                   int64
	UserID                     int64
	LogicalRequestID           string
	EndpointFamily             string
	NormalizedPayloadHash      string
	RequestedModel             string
	PoolingGroupID             int64
	BillingPolicyVersion       string
	RequestClass               string
	PredictedCost              decimal.Decimal
	IdempotencyKeyClientHeader string
	BalanceEnforcementMode     BalanceEnforcementMode
}

// ReserveResult 标识 claim 行,以及是否命中可用的缓存历史响应。
type ReserveResult struct {
	ClaimID             int64
	AttemptSeq          int32
	CachedPriorResponse []byte // 除非命中重放,否则为空
	FingerprintConflict bool
	IdempotencyHit      bool
}

// SettleRequest 携带 Tx2 的输入。
type SettleRequest struct {
	ClaimID             int64
	AccountID           int64
	AcquisitionToken    uuid.UUID
	UsageRecordPayload  []byte // 可直接插入
	BillingEventPayload []byte // 可直接插入
	ActualCost          decimal.Decimal
	TenantID            int64
	APIKeyID            int64
	UserID              int64
	ProviderAccountID   int64
	AttemptSeq          int32
	RequestedModel      string
	RequestedAt         time.Time
	UpstreamModel       string
	Provider            string
	Stream              bool
	ProtocolLoss        json.RawMessage
	Draft               gateway.UsageRecordDraft
	StreamAttempt       *Attempt
	Fingerprint         string
	AuditRequestID      string
	// EmitSchedulerOutbox 请求在 Tx2 内写一条 account_quota_changed 的
	// scheduler_outbox 行。它是一个 serializable 意图而非回调,因此投递后的
	// 结算恢复可以重放同样的 outbox 效果。
	EmitSchedulerOutbox bool
	// SnapshotVersion 是由 router.Plan 产生的 registry+router 戳
	//(格式 "registry:<tid>:<v>;router:<rv>")。
	// 写入 usage_records.snapshot_version,使审计重放能够
	// 重建构造该 plan 时的路由配置。
	SnapshotVersion string
}

// SettleResult 是 Tx2 的提交结果。
type SettleResult struct {
	NewUserBalance       decimal.Decimal
	APIKeyQuotaExhausted bool
	OutboxEventsEnqueued int
	TenantID             int64
	UserID               int64
	BillingEventID       int64
}

// RefundRequest 是 append-only 退款 / 修正请求。
type RefundRequest struct {
	TenantID       int64
	ClaimID        int64
	AmountMicroUSD int64
	Reason         string
	AuditRequestID string
}

// RefundResult 标识不可变 adjustment event。
type RefundResult struct {
	RefundMicroUSD int64
	BillingEventID int64
	AdjustmentRef  string
	Idempotent     bool
	// BalanceCredited 标识**本次调用**是否实际回补了 user_balances 余额行(per-call
	// 语义,非"是否曾被回补")。opt-in 余额强制下未 provision 余额行的用户为 false
	// (无行可补,合法 no-op);幂等重放(Idempotent=true)时也为 false——本次调用未再
	// 回补(原始调用若回补过,效果已落),与 Idempotent 标志一致。
	BalanceCredited bool
}

// 表,并为待处理的 usage records 增加对账 worker。
