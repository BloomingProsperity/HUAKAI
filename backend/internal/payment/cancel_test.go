package payment

import (
	"context"
	"errors"
	"testing"
	"time"
)

func newCancelSvc() *Service {
	fixed := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)
	return NewService(NewMemoryStore(), WithTestProvider(), WithClock(func() time.Time { return fixed }))
}

// 守 C1: 用户取消自己的 pending 订单 -> cancelled + 写 audit; 重复取消幂等。
// Mutation: CancelOrder 把 default 分支也当 pending 处理 / 去掉幂等 case -> 红。
func TestService_CancelPendingOrderIdempotent(t *testing.T) {
	ctx := context.Background()
	svc := newCancelSvc()
	r, err := svc.CreateOrder(ctx, CreateOrderInput{TenantID: 1, UserID: 2, AmountCents: 500, OutTradeNo: "cx1", ProviderKind: ProviderManual})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	o, err := svc.CancelOrder(ctx, CancelOrderInput{TenantID: 1, OrderID: r.Order.ID, UserID: 2, ActorKind: ActorKindUser, RequestID: "rq1"})
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if o.Status != StatusCancelled {
		t.Fatalf("status=%s want cancelled", o.Status)
	}
	o2, err := svc.CancelOrder(ctx, CancelOrderInput{TenantID: 1, OrderID: r.Order.ID, UserID: 2, ActorKind: ActorKindUser, RequestID: "rq2"})
	if err != nil || o2.Status != StatusCancelled {
		t.Fatalf("double cancel must be idempotent: o=%+v err=%v", o2, err)
	}
}

// 守 C1: 非 pending(已确认/完成)订单不可取消 -> ErrOrderNotCancelable。
func TestService_CancelNonPendingRejected(t *testing.T) {
	ctx := context.Background()
	svc := newCancelSvc()
	r, err := svc.CreateOrder(ctx, CreateOrderInput{TenantID: 1, UserID: 2, AmountCents: 500, OutTradeNo: "cx2", ProviderKind: ProviderTest})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.AdminConfirmPaid(ctx, AdminConfirmPaidInput{TenantID: 1, OrderID: r.Order.ID, ActorAdminID: 9}); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	_, err = svc.CancelOrder(ctx, CancelOrderInput{TenantID: 1, OrderID: r.Order.ID, ActorKind: ActorKindAdmin, RequestID: "rq3"})
	if !errors.Is(err, ErrOrderNotCancelable) {
		t.Fatalf("cancel confirmed order err=%v want ErrOrderNotCancelable", err)
	}
}

// 守 C1: 用户不能取消他人订单 -> ErrOrderNotFound(不泄露存在性), 订单仍 pending。
func TestService_CancelCrossUserRejected(t *testing.T) {
	ctx := context.Background()
	svc := newCancelSvc()
	r, err := svc.CreateOrder(ctx, CreateOrderInput{TenantID: 1, UserID: 2, AmountCents: 500, OutTradeNo: "cx3", ProviderKind: ProviderManual})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err = svc.CancelOrder(ctx, CancelOrderInput{TenantID: 1, OrderID: r.Order.ID, UserID: 999, ActorKind: ActorKindUser, RequestID: "rq4"})
	if !errors.Is(err, ErrOrderNotFound) {
		t.Fatalf("cross-user cancel err=%v want ErrOrderNotFound", err)
	}
	o, err := svc.GetOrder(ctx, 1, r.Order.ID)
	if err != nil || o.Status != StatusPending {
		t.Fatalf("order must remain pending after rejected cross-user cancel: %s err=%v", o.Status, err)
	}
}
