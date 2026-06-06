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
	CancelOrder(ctx context.Context, rec cancelRecord) (Order, error)
	RefundOrder(ctx context.Context, rec refundRecord) (RefundResult, error)
	ExpireStalePendingOrders(ctx context.Context, now time.Time, limit int) (expired int, err error)
	BeginFulfill(ctx context.Context, rec fulfillRecord) (Order, beginFulfillOutcome, error)
	CompleteFulfill(ctx context.Context, rec fulfillRecord) (FulfillResult, error)
	ListOrdersByUser(ctx context.Context, tenantID, userID int64, limit int) ([]Order, error)
	AdminListOrders(ctx context.Context, filter OrderListFilter) ([]Order, error)
	DashboardStats(ctx context.Context, filter DashboardFilter, now time.Time) (DashboardStats, error)
	UserBalanceCents(ctx context.Context, tenantID, userID int64) (int64, error)
	ListAuditEvents(ctx context.Context, tenantID, orderID int64) ([]AuditEvent, error)
}

type RechargeCapStore interface {
	CountPendingOrders(ctx context.Context, tenantID, userID int64, now time.Time) (int, error)
	SumRechargeAmountSince(ctx context.Context, tenantID, userID int64, since, now time.Time) (int64, error)
}

type adminOrderExportStore interface {
	AdminExportOrders(ctx context.Context, filter OrderExportFilter) ([]Order, error)
}

type subscriptionPlanPriceSnapshot struct {
	TenantID     int64
	PlanID       int64
	AmountCents  int64
	CurrencyCode string
	Enabled      bool
}

type subscriptionPlanPriceStore interface {
	GetSubscriptionPlanPriceSnapshot(ctx context.Context, tenantID, planID int64) (subscriptionPlanPriceSnapshot, error)
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
	CreatedActorKind   string
	CreatedActorID     int64
	RequestID          string
	ExpiresAt          *time.Time
	OrderKind          string // 缺省 topup
	SubscriptionPlanID *int64 // 订阅单必填
	Now                time.Time
}

func sameOptionalInt64(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
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

type cancelRecord struct {
	TenantID  int64
	OrderID   int64
	UserID    int64 // >0: 用户自助取消(校验归属); 0: admin 取消(不限用户)。
	ActorKind string
	ActorID   int64
	Reason    string
	RequestID string
	Now       time.Time
}

type refundRecord struct {
	TenantID       int64
	OrderID        int64
	AmountCents    int64
	IdempotencyKey string
	Reason         string
	ActorKind      string
	ActorID        int64
	RequestID      string
	Now            time.Time
}

type fulfillRecord struct {
	TenantID  int64
	OrderID   int64
	ActorKind string
	ActorID   int64
	RequestID string
	Now       time.Time
}
