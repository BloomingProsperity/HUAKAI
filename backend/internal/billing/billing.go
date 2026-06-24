// Package billing implements F-OBS-001 + F-BILL-001 framing:
// Tx1/Tx2 atomic billing with Usage Record finalization.
//
// See docs/specs/observability-billing.md for the released spec.
// Current slice includes PostgreSQL-backed ClaimGate and DefaultSettler
// implementations. Dynamic pricing precision and reconciliation workers remain
// Phase E+ work; scheduler outbox intent is carried by SettleRequest so direct
// settlement and post-delivery recovery preserve the same proof-chain effect.
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

// ClaimGate runs the Tx1 reservation transaction per spec §Tx1.
type ClaimGate interface {
	// Reserve opens a serializable transaction with row-locks in the fixed
	// six-row lock order, computes idempotency_key, looks up or inserts the
	// claim row, reserves quota across 5 dimensions, commits.
	Reserve(ctx context.Context, req ReserveRequest) (*ReserveResult, error)
}

// Settler 运行 spec §Tx2 定义的结算 reconcile 事务。
type Settler interface {
	// Settle 在一个事务内提交 Usage Record、audit billing event、claim 状态翻转、
	// 5 类原子效果、cross-threshold scheduler outbox 以及 Provider Account
	// in_flight_count 递减。HUAKAI 将用量记录写入同一结算事务,避免结算与审计分离。
	Settle(ctx context.Context, req SettleRequest) (*SettleResult, error)

	// Abort 以零成本终止 claim。observedInputTokens 仅用于输入阶段中断流的审计,
	// 不产生任何成本。tenant 作用域用于阻止 stale claim id 跨租户 abort。
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

// ReserveRequest carries Tx1 inputs.
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

// ReserveResult identifies the claim row and whether a cached prior response applies.
type ReserveResult struct {
	ClaimID             int64
	CachedPriorResponse []byte // empty unless replay hit
	FingerprintConflict bool
	IdempotencyHit      bool
}

// SettleRequest carries Tx2 inputs.
type SettleRequest struct {
	ClaimID             int64
	AccountID           int64
	AcquisitionToken    uuid.UUID
	UsageRecordPayload  []byte // ready-to-insert
	BillingEventPayload []byte // ready-to-insert
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
	// EmitSchedulerOutbox requests an account_quota_changed scheduler_outbox
	// row inside Tx2. It is a serializable intent instead of a callback so
	// post-delivery settlement recovery can replay the same outbox effect.
	EmitSchedulerOutbox bool
	// SnapshotVersion is the registry+router stamp produced by
	// router.Plan (format "registry:<tid>:<v>;router:<rv>").
	// Written into usage_records.snapshot_version so audit replay can
	// reconstruct the routing config that built this plan.
	SnapshotVersion string
}

// SettleResult is the Tx2 commit outcome.
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

// tables and add reconciliation workers for pending usage records.
