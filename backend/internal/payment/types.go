// HUAKAI · iKun

// Package payment 实现 HUAKAI 内部支付/充值机器 (Slice P1)。
// 入账走 voucher 同款 seam: 在 SERIALIZABLE 事务里写一条 billing_events(payment_credited),
// 余额由 billing_events / payment_credits 派生 SUM, 不落独立可变余额表; 零 import internal/billing。
// P1 只提供 manual / test provider, 真实支付渠道 SDK 留 Owner-gated 后续切片。
package payment

import (
	"errors"
	"time"
)

// 订单缺省过期时间 (pending 未支付窗口)。
const defaultOrderTTL = 24 * time.Hour

// 单笔金额上限 ($1,000,000,000): 远低于 billing_events.actual_cost numeric(20,8) 可表示范围,
// 防超大 int64 金额在入账时数值溢出, 导致订单卡在 recharging 反复履约失败。
const maxAmountCents = 100_000_000_000

// OrderStatus 订单状态机:
//
//	pending -> paid -> recharging -> completed
//
// 旁路: expired / cancelled / failed。recharging 是显式可崩溃恢复断点。
type OrderStatus string

const (
	StatusPending    OrderStatus = "pending"
	StatusPaid       OrderStatus = "paid"
	StatusRecharging OrderStatus = "recharging"
	StatusCompleted  OrderStatus = "completed"
	StatusExpired    OrderStatus = "expired"
	StatusCancelled  OrderStatus = "cancelled"
	StatusFailed     OrderStatus = "failed"
)

// ProviderKind 支付渠道种类。P1 仅 manual / test。
type ProviderKind string

const (
	ProviderManual ProviderKind = "manual"
	ProviderTest   ProviderKind = "test"
)

// Order 支付订单。金额用 amount_cents (对齐 voucher_redemption), currency_code 三位币种。
type Order struct {
	ID                 int64
	TenantID           int64
	UserID             int64
	OutTradeNo         string
	AmountCents        int64
	CurrencyCode       string
	Status             OrderStatus
	ProviderKind       ProviderKind
	ProviderOrderRef   string
	RequestFingerprint string
	CreatedByAdminID   int64
	ConfirmedByAdminID int64
	ConfirmReason      string
	FailureCode        string
	FailureMessage     string
	CreatedAt          time.Time
	UpdatedAt          time.Time
	ExpiresAt          *time.Time
	PaidAt             *time.Time
	RechargingAt       *time.Time
	CompletedAt        *time.Time
	FailedAt           *time.Time
}

// CreditRecord 一次已入账事实, 一张订单最多一条。
type CreditRecord struct {
	ID             int64
	TenantID       int64
	OrderID        int64
	UserID         int64
	AmountCents    int64
	CurrencyCode   string
	ReasonClass    string
	BillingEventID int64
	CreatedAt      time.Time
}

// CreateOrderInput service 层建单输入。
type CreateOrderInput struct {
	TenantID     int64
	UserID       int64
	AmountCents  int64
	CurrencyCode string
	OutTradeNo   string // 缺省由 service 生成 tenant-aware 难碰撞值
	ProviderKind ProviderKind
	ActorAdminID int64
	RequestID    string
	ExpiresIn    time.Duration // 0 = 默认 TTL
}

// CreateOrderResult 建单结果。Idempotent=true 表示同 out_trade_no 重放命中已有单。
type CreateOrderResult struct {
	Order      Order
	Idempotent bool
}

// AdminConfirmPaidInput 管理员手动确认支付输入。
type AdminConfirmPaidInput struct {
	TenantID      int64
	OrderID       int64
	ActorAdminID  int64
	RequestID     string
	ConfirmReason string
}

// FulfillInput 履约输入。
type FulfillInput struct {
	TenantID  int64
	OrderID   int64
	ActorKind string
	ActorID   int64
	RequestID string
}

// FulfillResult 履约结果。Idempotent=true 表示订单此前已完成, 本次未重复入账。
type FulfillResult struct {
	Order        Order
	Credit       CreditRecord
	BalanceCents int64
	Idempotent   bool
}

// Balance 用户支付来源余额 (来自 payment_credits 派生 SUM)。
type Balance struct {
	TenantID    int64
	UserID      int64
	AmountCents int64
}

var (
	ErrStoreNotConfigured  = errors.New("payment: store not configured")
	ErrOrderNotFound       = errors.New("payment: order not found")
	ErrInvalidAmount       = errors.New("payment: amount must be positive")
	ErrInvalidInput        = errors.New("payment: invalid input")
	ErrOrderNotConfirmable = errors.New("payment: order not in a confirmable state")
	ErrOrderNotFulfillable = errors.New("payment: order not in a fulfillable state")
	ErrIdempotencyConflict = errors.New("payment: out_trade_no reused with different order fields")
	ErrProviderUnknown     = errors.New("payment: unknown provider kind")
	ErrUnsupportedCurrency = errors.New("payment: unsupported currency (P1 ledger is USD-only)")
	// P2a 自动回调路径错误。
	ErrProviderNoCallback = errors.New("payment: provider does not support callbacks")            // provider 存在但非 CallbackVerifier (如 manual)
	ErrCallbackUnverified = errors.New("payment: callback signature verification failed")          // 验签失败 → 零入账
	ErrCallbackRejected   = errors.New("payment: callback rejected by business validation")        // 验签通过但金额/币种/渠道不符 → 零入账
)
