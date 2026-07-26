// Package billing 实现 F-OBS-001 + F-BILL-001 框架:
// 带 Usage Record 终结的 Tx1/Tx2 原子计费。
//
// 当前计费合同见 docs/HUAKAI工程设计手册.md §9。
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

var (
	ErrInsufficientBalance       = errors.New("billing: insufficient balance")
	ErrTenantInactive            = errors.New("billing: tenant inactive")
	ErrInvalidBillingEffect      = errors.New("billing: invalid billing effect")
	ErrRefundNoCapturedCharge    = errors.New("billing: refund has no captured balance charge")
	ErrRefundBalanceRowMissing   = errors.New("billing: refund balance row missing")
	ErrRefundAmountNotCovered    = errors.New("billing: refund amount exceeds captured charge coverage")
	ErrRefundIdempotencyConflict = errors.New("billing: refund idempotency key conflicts with a different request")
	ErrRefundFactInvalid         = errors.New("billing: stored refund fact is invalid")
)

// BillingEffect 区分用户消费与平台运维成本。两者共用完整账务证据链，
// 但只有 UserCharge 可以预扣、扣减或退款用户余额。
type BillingEffect string

const (
	BillingEffectUserCharge      BillingEffect = "user_charge"
	BillingEffectOperationalCost BillingEffect = "operational_cost"
)

func NormalizeBillingEffect(effect BillingEffect) (BillingEffect, error) {
	switch effect {
	case "", BillingEffectUserCharge:
		return BillingEffectUserCharge, nil
	case BillingEffectOperationalCost:
		return BillingEffectOperationalCost, nil
	default:
		return "", ErrInvalidBillingEffect
	}
}

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

	// Refund 只对已有 captured 余额扣款的已提交 claim 追加幂等负向
	// reconciliation event，并在同一事务回补余额；原 claim / usage 行保持不可变。
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
	BillingEffect              BillingEffect
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
	// AuditRouteID 与 AuditPoolGroupID 保存本次实际路由事实，供结算后的
	// 六跳日志链和失败恢复重建使用；它们不参与价格计算或账号选择。
	AuditRouteID     string
	AuditPoolGroupID int64
	// AuditProviderEndpoint 只允许保存协议端点路径或不含凭据的固定地址，
	// 禁止写入 query、Authorization 或任何账号秘密。
	AuditProviderEndpoint string
	// EmitSchedulerOutbox 请求在 Tx2 内写一条 account_quota_changed 的
	// scheduler_outbox 行。它是一个 serializable 意图而非回调,因此投递后的
	// 结算恢复可以重放同样的 outbox 效果。
	EmitSchedulerOutbox bool
	// SnapshotVersion 是由 router.Plan 产生的 registry+router 戳
	//(格式 "registry:<tid>:<v>;router:<rv>")。
	// 写入 usage_records.snapshot_version,使审计重放能够
	// 重建构造该 plan 时的路由配置。
	SnapshotVersion string
	// BillingEffect 由 claim 的持久化值最终裁决；请求携带它用于链路一致性校验和恢复重放。
	BillingEffect BillingEffect
}

// SettleResult 是 Tx2 的提交结果。
type SettleResult struct {
	NewUserBalance       decimal.Decimal
	APIKeyQuotaExhausted bool
	OutboxEventsEnqueued int
	TenantID             int64
	UserID               int64
	BillingEventID       int64
	BillingEffect        BillingEffect
	BalanceChanged       bool
}

// RefundRequest 是 append-only 退款 / 修正请求。
type RefundRequest struct {
	TenantID       int64
	ClaimID        int64
	AmountMicroUSD int64
	Reason         string
	// IdempotencyKey 标识一次退款业务操作；同租户内重复使用时，claim、金额、
	// 原因和精确模式必须全部一致。
	IdempotencyKey string
	// AuditRequestID 只用于审计追踪，不参与退款幂等身份。
	AuditRequestID string
	// RequireExact 把 AmountMicroUSD 解释为该 claim 必须达到的累计补偿目标。
	// 已有负向调整会抵扣本次新增金额；captured 上限不足时整笔失败。
	RequireExact bool
}

// RefundResult 标识不可变 adjustment event。
type RefundResult struct {
	RefundMicroUSD int64
	BillingEventID int64
	AdjustmentRef  string
	Idempotent     bool
	// CoveredMicroUSD 是该 claim 在本次事务后的累计负向调整金额，用于证明
	// RequireExact 请求已被本次与既有调整共同覆盖。
	CoveredMicroUSD int64
	// AlreadySatisfied 表示本次未新增资金效果，但该 claim 的既有负向调整已经
	// 达到可退款上限；BillingEventID/AdjustmentRef 指向既有调整证据。
	AlreadySatisfied bool
	// BalanceCredited 标识本次调用是否实际回补了 user_balances。新退款必须为 true；
	// 幂等重放或既有调整已覆盖时不重复回补，因此为 false。
	BalanceCredited bool
}

// 表,并为待处理的 usage records 增加对账 worker。
