// HUAKAI · iKun
//go:build integration_pg

package paymenthttp

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/payment"
)

// money-via-login Stage 3 双身份归属回归(真库真路径,迁移 0167):
// ①session-admin(adminActorID=0 + actorRef 非空)审批/驳回退款申请不再被 <=0 硬守卫拒;
// ②决策归属:decided_by NULL(不误归 id 0)+ decided_by_actor=admin_user:N;
// ③放款事实:payment_refunds.actor_ref 落 text 归属、actor_id NULL;token-admin 双写。

// session-admin 审批:守卫放行 + 决策/放款双 text 归属 + 旧 bigint 列 NULL。
func TestRefundRequestApproveSessionAdminAttributesViaActorRef(t *testing.T) {
	ctx := context.Background()
	pool := openPaymentHTTPPool(t, ctx)
	tenantA, userA := seedPaymentHTTPUser(t, ctx, pool, "refund-ar-sess")
	svc := payment.NewService(payment.NewPostgresStore(pool))
	recorder := NewPostgresRefundRequestRecorder(pool, svc)

	order := createCompletedTopupForRefundRequest(t, ctx, svc, tenantA, userA, 900, "ar-sess-approve")
	req := createRefundRequestForAdminTest(t, ctx, recorder, tenantA, userA, order.ID, "session approve")

	got, err := recorder.ApproveRefundRequest(ctx, tenantA, req.ID, 0, "admin_user:42")
	if err != nil {
		t.Fatalf("session-admin 审批应放行(守卫已改判来源),得 %v", err)
	}
	if got.Status != RefundRequestApproved {
		t.Fatalf("状态应 approved,得 %s", got.Status)
	}
	var decidedBy sql.NullInt64
	var decidedByActor sql.NullString
	if err := pool.QueryRow(ctx,
		`SELECT decided_by, decided_by_actor FROM payment_refund_requests WHERE tenant_id=$1 AND id=$2`,
		tenantA, req.ID).Scan(&decidedBy, &decidedByActor); err != nil {
		t.Fatalf("查询决策归属: %v", err)
	}
	if decidedBy.Valid {
		t.Fatalf("session-admin 的 decided_by 应为 NULL(不误归 id 0),得 %+v", decidedBy)
	}
	if !decidedByActor.Valid || decidedByActor.String != "admin_user:42" {
		t.Fatalf("decided_by_actor 应为 admin_user:42,得 %+v", decidedByActor)
	}
	// 放款事实行:actor_ref 落 text 归属、旧 actor_id NULL。
	var refundActorID sql.NullInt64
	var refundActorRef sql.NullString
	if err := pool.QueryRow(ctx,
		`SELECT actor_id, actor_ref FROM payment_refunds WHERE tenant_id=$1 AND order_id=$2`,
		tenantA, order.ID).Scan(&refundActorID, &refundActorRef); err != nil {
		t.Fatalf("查询放款归属: %v", err)
	}
	if refundActorID.Valid {
		t.Fatalf("session-admin 放款 actor_id 应 NULL,得 %+v", refundActorID)
	}
	if !refundActorRef.Valid || refundActorRef.String != "admin_user:42" {
		t.Fatalf("放款 actor_ref 应为 admin_user:42,得 %+v", refundActorRef)
	}
}

// session-admin 驳回:守卫放行 + decided_by_actor 归属。
func TestRefundRequestRejectSessionAdminAllowed(t *testing.T) {
	ctx := context.Background()
	pool := openPaymentHTTPPool(t, ctx)
	tenantA, userA := seedPaymentHTTPUser(t, ctx, pool, "refund-ar-rej")
	svc := payment.NewService(payment.NewPostgresStore(pool))
	recorder := NewPostgresRefundRequestRecorder(pool, svc)

	order := createCompletedTopupForRefundRequest(t, ctx, svc, tenantA, userA, 700, "ar-sess-reject")
	req := createRefundRequestForAdminTest(t, ctx, recorder, tenantA, userA, order.ID, "session reject")

	got, err := recorder.RejectRefundRequest(ctx, tenantA, req.ID, "not eligible", 0, "admin_user:42")
	if err != nil {
		t.Fatalf("session-admin 驳回应放行,得 %v", err)
	}
	if got.Status != RefundRequestRejected {
		t.Fatalf("状态应 rejected,得 %s", got.Status)
	}
	var decidedByActor sql.NullString
	if err := pool.QueryRow(ctx,
		`SELECT decided_by_actor FROM payment_refund_requests WHERE tenant_id=$1 AND id=$2`,
		tenantA, req.ID).Scan(&decidedByActor); err != nil {
		t.Fatalf("查询决策归属: %v", err)
	}
	if !decidedByActor.Valid || decidedByActor.String != "admin_user:42" {
		t.Fatalf("decided_by_actor 应为 admin_user:42,得 %+v", decidedByActor)
	}
}

// 双无身份(id=0 且 ref 空)仍必须被守卫拒——守卫是"改判来源"不是"取消"。
// 变异:把守卫改成恒放行 → 本测 RED。
func TestRefundRequestApproveNoIdentityStillRejected(t *testing.T) {
	ctx := context.Background()
	pool := openPaymentHTTPPool(t, ctx)
	tenantA, userA := seedPaymentHTTPUser(t, ctx, pool, "refund-ar-noid")
	svc := payment.NewService(payment.NewPostgresStore(pool))
	recorder := NewPostgresRefundRequestRecorder(pool, svc)

	order := createCompletedTopupForRefundRequest(t, ctx, svc, tenantA, userA, 500, "ar-noid")
	req := createRefundRequestForAdminTest(t, ctx, recorder, tenantA, userA, order.ID, "no identity")

	if _, err := recorder.ApproveRefundRequest(ctx, tenantA, req.ID, 0, ""); !errors.Is(err, ErrRefundRequestInvalidInput) {
		t.Fatalf("无任何 admin 身份应仍被拒 ErrRefundRequestInvalidInput,得 %v", err)
	}
}

// token-admin 审批:双写(decided_by=99 + decided_by_actor=admin_token:99;放款 actor_id=99+actor_ref)。
func TestRefundRequestApproveTokenAdminDualWrites(t *testing.T) {
	ctx := context.Background()
	pool := openPaymentHTTPPool(t, ctx)
	tenantA, userA := seedPaymentHTTPUser(t, ctx, pool, "refund-ar-token")
	svc := payment.NewService(payment.NewPostgresStore(pool))
	recorder := NewPostgresRefundRequestRecorder(pool, svc)

	order := createCompletedTopupForRefundRequest(t, ctx, svc, tenantA, userA, 1100, "ar-token")
	req := createRefundRequestForAdminTest(t, ctx, recorder, tenantA, userA, order.ID, "token approve")

	if _, err := recorder.ApproveRefundRequest(ctx, tenantA, req.ID, 99, "admin_token:99"); err != nil {
		t.Fatalf("token-admin 审批: %v", err)
	}
	var decidedBy sql.NullInt64
	var decidedByActor sql.NullString
	if err := pool.QueryRow(ctx,
		`SELECT decided_by, decided_by_actor FROM payment_refund_requests WHERE tenant_id=$1 AND id=$2`,
		tenantA, req.ID).Scan(&decidedBy, &decidedByActor); err != nil {
		t.Fatalf("查询决策归属: %v", err)
	}
	if !decidedBy.Valid || decidedBy.Int64 != 99 || !decidedByActor.Valid || decidedByActor.String != "admin_token:99" {
		t.Fatalf("token-admin 应双写 decided_by=99+actor=admin_token:99,得 %+v/%+v", decidedBy, decidedByActor)
	}
	var refundActorID sql.NullInt64
	var refundActorRef sql.NullString
	if err := pool.QueryRow(ctx,
		`SELECT actor_id, actor_ref FROM payment_refunds WHERE tenant_id=$1 AND order_id=$2`,
		tenantA, order.ID).Scan(&refundActorID, &refundActorRef); err != nil {
		t.Fatalf("查询放款归属: %v", err)
	}
	if !refundActorID.Valid || refundActorID.Int64 != 99 || !refundActorRef.Valid || refundActorRef.String != "admin_token:99" {
		t.Fatalf("放款应双写 actor_id=99+actor_ref=admin_token:99,得 %+v/%+v", refundActorID, refundActorRef)
	}
}
