// HUAKAI · iKun
//go:build integration_pg

package payment

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// money-via-login 收口审计 S1 修复回归(真库):放开给 session-admin 的建单/取消路径归属必须可追。
// 修复前:CreateOrderInput 无 ActorRef → session-admin(TokenID=0)建单 created_by_admin_id 与
// created_by_actor 双 NULL,且 createOrderActorKind 误标 actor_kind='system'(真人操作伪称系统单)。

// session-admin 建单:created_by_actor=admin_user:42、旧列 NULL、审计 actor_kind='admin' 非 'system'。
// 变异一:删 service CreateOrder 的 CreatedByActor 赋值 → created_by_actor NULL → RED。
// 变异二:createOrderActorKind 去掉 ActorRef 子句 → actor_kind='system' → RED。
func TestAdminCreateOrderSessionAdminAttribution(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)

	svc := NewService(NewPostgresStore(pool))
	res, err := svc.CreateOrder(ctx, CreateOrderInput{
		TenantID:     f.tenantA,
		UserID:       f.userA,
		AmountCents:  2500,
		CurrencyCode: "USD",
		ProviderKind: ProviderManual,
		OutTradeNo:   "ar-create-2500",
		ActorAdminID: 0,               // session-admin:TokenID=0
		ActorRef:     "admin_user:42", // 归属靠 text 列
		RequestID:    "req-ar-create",
	})
	if err != nil {
		t.Fatalf("CreateOrder(session-admin): %v", err)
	}
	var createdBy sql.NullInt64
	var createdActor sql.NullString
	if err := pool.QueryRow(ctx,
		`SELECT created_by_admin_id, created_by_actor FROM payment_orders WHERE tenant_id=$1 AND id=$2`,
		f.tenantA, res.Order.ID).Scan(&createdBy, &createdActor); err != nil {
		t.Fatalf("查询建单归属: %v", err)
	}
	if createdBy.Valid {
		t.Fatalf("session-admin 的 created_by_admin_id 应 NULL,得 %+v", createdBy)
	}
	if !createdActor.Valid || createdActor.String != "admin_user:42" {
		t.Fatalf("created_by_actor 应为 admin_user:42,得 %+v", createdActor)
	}
	var kind string
	var ref sql.NullString
	if err := pool.QueryRow(ctx,
		`SELECT actor_kind, actor_ref FROM payment_audit_events
		 WHERE tenant_id=$1 AND payment_order_id=$2 AND event_type=$3`,
		f.tenantA, res.Order.ID, AuditOrderCreated).Scan(&kind, &ref); err != nil {
		t.Fatalf("查询建单审计: %v", err)
	}
	if kind != ActorKindAdmin {
		t.Fatalf("session-admin 建单审计 actor_kind 应为 admin(不得伪标 system),得 %s", kind)
	}
	if !ref.Valid || ref.String != "admin_user:42" {
		t.Fatalf("建单审计 actor_ref 应为 admin_user:42,得 %+v", ref)
	}
}

// session-admin 取消 pending 单:取消审计 actor_ref=admin_user:42(修复前 NULL 不可追)。
// 变异:删 CancelOrder 的 cancelRecord.ActorRef 穿线 → RED。
func TestAdminCancelOrderSessionAdminAttribution(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)

	svc := NewService(NewPostgresStore(pool))
	res, err := svc.CreateOrder(ctx, CreateOrderInput{
		TenantID: f.tenantA, UserID: f.userA, AmountCents: 1200, CurrencyCode: "USD",
		ProviderKind: ProviderManual, OutTradeNo: "ar-cancel-1200", ActorAdminID: 0, ActorRef: "admin_user:42",
	})
	if err != nil {
		t.Fatalf("建单: %v", err)
	}
	if _, err := svc.CancelOrder(ctx, CancelOrderInput{
		TenantID: f.tenantA, OrderID: res.Order.ID,
		ActorKind: ActorKindAdmin, ActorID: 0, ActorRef: "admin_user:42",
		Reason: "session cancel attribution",
	}); err != nil {
		t.Fatalf("CancelOrder(session-admin): %v", err)
	}
	var kind string
	var ref sql.NullString
	if err := pool.QueryRow(ctx,
		`SELECT actor_kind, actor_ref FROM payment_audit_events
		 WHERE tenant_id=$1 AND payment_order_id=$2 AND event_type=$3`,
		f.tenantA, res.Order.ID, AuditOrderCancelled).Scan(&kind, &ref); err != nil {
		t.Fatalf("查询取消审计: %v", err)
	}
	if kind != ActorKindAdmin || !ref.Valid || ref.String != "admin_user:42" {
		t.Fatalf("取消审计应 kind=admin/ref=admin_user:42,得 kind=%s ref=%+v", kind, ref)
	}
}
