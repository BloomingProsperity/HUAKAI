// HUAKAI · iKun
//go:build integration_pg

package payment

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// money-via-login Stage 1 的双身份归属回归(真库真路径,迁移 0165):
// AdminAdjustBalance 的 ActorRef(AuditActor() 串)必须落 payment_orders.created_by_actor /
// confirmed_by_actor 与 payment_audit_events.actor_ref 三处 text 列;旧 bigint 列语义不变。
//
// 判别性:若 ActorRef 没穿进 createOrderRecord / UPDATE confirmed_by_actor / auditInsert
// 任一处(§14 变异=删掉对应赋值),对应断言读到 NULL → RED。

// queryOrderActorTexts 读订单两列 text 归属。
func queryOrderActorTexts(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, orderID int64) (created, confirmed sql.NullString) {
	t.Helper()
	if err := pool.QueryRow(ctx,
		`SELECT created_by_actor, confirmed_by_actor FROM payment_orders WHERE tenant_id=$1 AND id=$2`,
		tenantID, orderID).Scan(&created, &confirmed); err != nil {
		t.Fatalf("查询订单 text 归属: %v", err)
	}
	return created, confirmed
}

// queryAuditActorRef 读某事件类型的 actor_ref(并顺带返回 actor_id 供 bigint 语义断言)。
func queryAuditActorRef(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, orderID int64, eventType string) (ref sql.NullString, id sql.NullInt64) {
	t.Helper()
	if err := pool.QueryRow(ctx,
		`SELECT actor_ref, actor_id FROM payment_audit_events
		 WHERE tenant_id=$1 AND payment_order_id=$2 AND event_type=$3`,
		tenantID, orderID, eventType).Scan(&ref, &id); err != nil {
		t.Fatalf("查询审计 actor_ref(%s): %v", eventType, err)
	}
	return ref, id
}

// token-admin(hk_admin 令牌通道):双写——旧 bigint 列=TokenID、新 text 列=admin_token:N。
func TestAdminAdjustBalanceTokenAdminDualWritesActorRef(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)

	svc := NewService(NewPostgresStore(pool))
	credit, err := svc.AdminAdjustBalance(ctx, AdminBalanceAdjustmentInput{
		TenantID:        f.tenantA,
		UserID:          f.userA,
		Amount:          decimal.RequireFromString("50.00000000"),
		CurrencyCode:    "USD",
		ActorID:         "7",             // 旧 bigint 载体(handler 传 fmt.Sprintf("%d", TokenID))
		ActorRef:        "admin_token:7", // 新 text 载体(handler 传 ident.AuditActor())
		Reason:          "token-admin dual attribution",
		ExternalTradeNo: "actor-ref-token-50",
		Now:             time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("AdminAdjustBalance(token-admin): %v", err)
	}

	created, confirmed := queryOrderActorTexts(t, ctx, pool, f.tenantA, credit.RechargeOrderID)
	if !created.Valid || created.String != "admin_token:7" {
		t.Fatalf("created_by_actor 应为 admin_token:7,得 %+v", created)
	}
	if !confirmed.Valid || confirmed.String != "admin_token:7" {
		t.Fatalf("confirmed_by_actor 应为 admin_token:7,得 %+v", confirmed)
	}
	// 四个审计写点全带 actor_ref(建单/确认/开始履约/入账),且旧 bigint 列同时=7(双写不回退)。
	for _, ev := range []string{AuditOrderCreated, AuditPaidConfirmed, AuditFulfillmentStarted, AuditCredited} {
		ref, id := queryAuditActorRef(t, ctx, pool, f.tenantA, credit.RechargeOrderID, ev)
		if !ref.Valid || ref.String != "admin_token:7" {
			t.Fatalf("审计 %s 的 actor_ref 应为 admin_token:7,得 %+v", ev, ref)
		}
		if !id.Valid || id.Int64 != 7 {
			t.Fatalf("审计 %s 的旧 actor_id 应保持 7(双写),得 %+v", ev, id)
		}
	}
}

// session-admin(登录通道,TokenID=0):旧 bigint 列落 NULL(不误归 token 0),
// 新 text 列落 admin_user:N —— 归属经由 text 列保住,这正是本切片的存在意义。
func TestAdminAdjustBalanceSessionAdminAttributesViaActorRef(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)

	svc := NewService(NewPostgresStore(pool))
	credit, err := svc.AdminAdjustBalance(ctx, AdminBalanceAdjustmentInput{
		TenantID:        f.tenantA,
		UserID:          f.userA,
		Amount:          decimal.RequireFromString("30.00000000"),
		CurrencyCode:    "USD",
		ActorID:         "0",             // session-admin:TokenID=0 → 旧列 NULL
		ActorRef:        "admin_user:42", // 归属靠 text 列
		Reason:          "session-admin attribution via text column",
		ExternalTradeNo: "actor-ref-session-30",
		Now:             time.Date(2026, 7, 1, 11, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("AdminAdjustBalance(session-admin): %v", err)
	}

	created, confirmed := queryOrderActorTexts(t, ctx, pool, f.tenantA, credit.RechargeOrderID)
	if !created.Valid || created.String != "admin_user:42" {
		t.Fatalf("created_by_actor 应为 admin_user:42,得 %+v", created)
	}
	if !confirmed.Valid || confirmed.String != "admin_user:42" {
		t.Fatalf("confirmed_by_actor 应为 admin_user:42,得 %+v", confirmed)
	}
	// 旧 bigint 列必须 NULL(绝不能把 session-admin 误归成 token id 0 或别的数)。
	var createdID, confirmedID sql.NullInt64
	if err := pool.QueryRow(ctx,
		`SELECT created_by_admin_id, confirmed_by_admin_id FROM payment_orders WHERE tenant_id=$1 AND id=$2`,
		f.tenantA, credit.RechargeOrderID).Scan(&createdID, &confirmedID); err != nil {
		t.Fatalf("查询旧 bigint 归属: %v", err)
	}
	if createdID.Valid || confirmedID.Valid {
		t.Fatalf("session-admin 的旧 bigint 归属应为 NULL,得 created=%+v confirmed=%+v", createdID, confirmedID)
	}
	ref, id := queryAuditActorRef(t, ctx, pool, f.tenantA, credit.RechargeOrderID, AuditCredited)
	if !ref.Valid || ref.String != "admin_user:42" {
		t.Fatalf("审计 AuditCredited 的 actor_ref 应为 admin_user:42,得 %+v", ref)
	}
	if id.Valid {
		t.Fatalf("session-admin 的审计旧 actor_id 应为 NULL,得 %+v", id)
	}
}

// 幂等重放:同 ExternalTradeNo 二次调用返回既有结果,不重复入账、归属列不被改写。
func TestAdminAdjustBalanceActorRefIdempotentReplay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)

	svc := NewService(NewPostgresStore(pool))
	in := AdminBalanceAdjustmentInput{
		TenantID: f.tenantA, UserID: f.userA,
		Amount: decimal.RequireFromString("10.00000000"), CurrencyCode: "USD",
		ActorID: "0", ActorRef: "admin_user:42",
		Reason: "replay keeps attribution", ExternalTradeNo: "actor-ref-replay-10",
		Now: time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC),
	}
	first, err := svc.AdminAdjustBalance(ctx, in)
	if err != nil {
		t.Fatalf("首次充值: %v", err)
	}
	replay, err := svc.AdminAdjustBalance(ctx, in)
	if err != nil {
		t.Fatalf("幂等重放: %v", err)
	}
	if replay.RechargeOrderID != first.RechargeOrderID || !replay.NewBalance.Equal(first.NewBalance) {
		t.Fatalf("重放应返回既有订单与余额:first=%+v replay=%+v", first, replay)
	}
	created, _ := queryOrderActorTexts(t, ctx, pool, f.tenantA, first.RechargeOrderID)
	if !created.Valid || created.String != "admin_user:42" {
		t.Fatalf("重放后归属应保持 admin_user:42,得 %+v", created)
	}
}
