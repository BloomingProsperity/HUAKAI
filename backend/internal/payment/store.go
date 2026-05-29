// HUAKAI · iKun

package payment

import (
	"context"
	"time"
)

// Store 支付持久化抽象。两段式履约: BeginFulfill 短事务把订单推进 recharging 并持久提交,
// CompleteFulfill 在 SERIALIZABLE 事务里写 credit + billing_event + completed。崩溃后从 recharging 续跑。
type Store interface {
	CreateOrder(ctx context.Context, rec createOrderRecord) (Order, bool, error)
	GetOrder(ctx context.Context, tenantID, orderID int64) (Order, error)
	GetOrderByOutTradeNo(ctx context.Context, tenantID int64, outTradeNo string) (Order, error)
	ConfirmPaid(ctx context.Context, rec confirmRecord) (Order, error)
	BeginFulfill(ctx context.Context, rec fulfillRecord) (Order, beginFulfillOutcome, error)
	CompleteFulfill(ctx context.Context, rec fulfillRecord) (FulfillResult, error)
	ListOrdersByUser(ctx context.Context, tenantID, userID int64, limit int) ([]Order, error)
	UserBalanceCents(ctx context.Context, tenantID, userID int64) (int64, error)
	ListAuditEvents(ctx context.Context, tenantID, orderID int64) ([]AuditEvent, error)
}

// beginFulfillOutcome 表示 BeginFulfill 后订单可继续 phase2 还是已完成。
type beginFulfillOutcome int

const (
	beginFulfillTransitioned beginFulfillOutcome = iota // 进入/保持 recharging, 可继续 phase2
	beginFulfillAlreadyDone                             // 已 completed, phase2 直接返回幂等
)

type createOrderRecord struct {
	TenantID           int64
	UserID             int64
	OutTradeNo         string
	AmountCents        int64
	CurrencyCode       string
	ProviderKind       ProviderKind
	ProviderOrderRef   string
	RequestFingerprint string
	CreatedByAdminID   int64
	RequestID          string
	ExpiresAt          *time.Time
	OrderKind          string // 缺省 topup
	SubscriptionPlanID *int64 // 订阅单必填
	Now                time.Time
}

type confirmRecord struct {
	TenantID      int64
	OrderID       int64
	AdminID       int64
	ActorKind     string // admin = 管理员手动 (P1); system = 支付回调自动 (P2a)。空缺省按 system。
	ConfirmReason string
	RequestID     string
	Now           time.Time
}

type fulfillRecord struct {
	TenantID  int64
	OrderID   int64
	ActorKind string
	ActorID   int64
	RequestID string
	Now       time.Time
}
