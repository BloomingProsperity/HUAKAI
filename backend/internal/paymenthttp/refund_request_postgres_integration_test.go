// HUAKAI · iKun
//go:build integration_pg

package paymenthttp

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/payment"
)

func TestPostgresRefundRequestApproveRejectListAndTenantIsolation(t *testing.T) {
	ctx := context.Background()
	pool := openPaymentHTTPPool(t, ctx)
	tenantA, userA := seedPaymentHTTPUser(t, ctx, pool, "refund-request-a")
	tenantB, userB := seedPaymentHTTPUser(t, ctx, pool, "refund-request-b")
	svc := payment.NewService(payment.NewPostgresStore(pool))
	recorder := NewPostgresRefundRequestRecorder(pool, svc)

	approvedOrder := createCompletedTopupForRefundRequest(t, ctx, svc, tenantA, userA, 1200, "pg-approve")
	if _, err := svc.RefundOrder(ctx, payment.RefundOrderInput{
		TenantID: tenantA, OrderID: approvedOrder.ID, AmountCents: 200,
		IdempotencyKey: "pg-approve-prior", ActorKind: payment.ActorKindAdmin, ActorID: 99,
	}); err != nil {
		t.Fatalf("prior partial refund: %v", err)
	}
	approvedReq := createRefundRequestForAdminTest(t, ctx, recorder, tenantA, userA, approvedOrder.ID, "approve me")
	if pending, err := recorder.ListPendingRefundRequests(ctx, tenantA); err != nil || len(pending) != 1 || pending[0].ID != approvedReq.ID {
		t.Fatalf("pending before approve=(%+v,%v), want only request %d", pending, err, approvedReq.ID)
	}
	if _, err := recorder.ApproveRefundRequest(ctx, tenantB, approvedReq.ID, 99, "admin_token:99"); !errors.Is(err, ErrRefundRequestNotFound) {
		t.Fatalf("cross-tenant approve err=%v want ErrRefundRequestNotFound", err)
	}
	first, err := recorder.ApproveRefundRequest(ctx, tenantA, approvedReq.ID, 99, "admin_token:99")
	if err != nil {
		t.Fatalf("first approve: %v", err)
	}
	second, err := recorder.ApproveRefundRequest(ctx, tenantA, approvedReq.ID, 99, "admin_token:99")
	if err != nil {
		t.Fatalf("duplicate approve: %v", err)
	}
	if first.Status != RefundRequestApproved || second.Status != RefundRequestApproved {
		t.Fatalf("approve statuses first=%q second=%q want approved", first.Status, second.Status)
	}
	assertPGCount(t, ctx, pool, `SELECT count(*) FROM payment_refunds WHERE tenant_id=$1 AND order_id=$2`, 2, tenantA, approvedOrder.ID)
	assertPGCount(t, ctx, pool, `SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND event_type='payment_refunded'`, 2, tenantA)
	balance, err := svc.GetBalance(ctx, tenantA, userA)
	if err != nil {
		t.Fatalf("balance after duplicate approve: %v", err)
	}
	if balance.AmountCents != 0 {
		t.Fatalf("balance after duplicate approve=%d want 0", balance.AmountCents)
	}

	rejectedOrder := createCompletedTopupForRefundRequest(t, ctx, svc, tenantB, userB, 900, "pg-reject")
	if _, err := svc.RefundOrder(ctx, payment.RefundOrderInput{
		TenantID: tenantB, OrderID: rejectedOrder.ID, AmountCents: 100,
		IdempotencyKey: "pg-reject-prior", ActorKind: payment.ActorKindAdmin, ActorID: 77,
	}); err != nil {
		t.Fatalf("prior refund before rejected request: %v", err)
	}
	rejectedReq := createRefundRequestForAdminTest(t, ctx, recorder, tenantB, userB, rejectedOrder.ID, "reject me")
	rejected, err := recorder.RejectRefundRequest(ctx, tenantB, rejectedReq.ID, "not eligible", 77, "admin_token:77")
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if rejected.Status != RefundRequestRejected || rejected.DecidedBy != 77 || rejected.DecidedAt == nil {
		t.Fatalf("rejected request=%+v want rejected with decision metadata", rejected)
	}
	assertPGCount(t, ctx, pool, `SELECT count(*) FROM payment_refunds WHERE tenant_id=$1 AND order_id=$2`, 1, tenantB, rejectedOrder.ID)
	assertPGCount(t, ctx, pool, `SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND event_type='payment_refunded'`, 1, tenantB)
	balance, err = svc.GetBalance(ctx, tenantB, userB)
	if err != nil {
		t.Fatalf("balance after reject: %v", err)
	}
	if balance.AmountCents != 800 {
		t.Fatalf("balance after reject=%d want prior-partial-refund balance 800", balance.AmountCents)
	}
	if pending, err := recorder.ListPendingRefundRequests(ctx, tenantB); err != nil || len(pending) != 0 {
		t.Fatalf("pending after reject=(%+v,%v), want none", pending, err)
	}
}

// 模拟旧版本留下的拆分事务窗口：钱已退、申请仍 pending。系统必须先阻止误拒绝，
// 再允许另一位管理员接管并收敛为 approved，且不能改写原资金操作者。
func TestPostgresRefundRequestCrashWindowRejectBlockedAndDifferentAdminRecovers(t *testing.T) {
	ctx := context.Background()
	pool := openPaymentHTTPPool(t, ctx)
	tenantID, userID := seedPaymentHTTPUser(t, ctx, pool, "refund-request-resolved")
	svc := payment.NewService(payment.NewPostgresStore(pool))
	recorder := NewPostgresRefundRequestRecorder(pool, svc)

	order := createCompletedTopupForRefundRequest(t, ctx, svc, tenantID, userID, 1100, "pg-resolved")
	req := createRefundRequestForAdminTest(t, ctx, recorder, tenantID, userID, order.ID, "refund then crash")
	if _, err := svc.RefundOrder(ctx, payment.RefundOrderInput{
		TenantID:       tenantID,
		OrderID:        order.ID,
		AmountCents:    order.AmountCents,
		IdempotencyKey: refundRequestIdempotencyKey(req.ID),
		Reason:         req.Reason,
		ActorKind:      payment.ActorKindAdmin,
		ActorID:        99,
		ActorRef:       "admin_token:99",
	}); err != nil {
		t.Fatalf("seed refund fact: %v", err)
	}

	_, err := recorder.RejectRefundRequest(ctx, tenantID, req.ID, "operator changed mind", 77, "admin_token:77")
	if !errors.Is(err, ErrRefundRequestAlreadyResolved) {
		t.Fatalf("reject after refund fact err=%v want ErrRefundRequestAlreadyResolved", err)
	}
	assertPGRefundRequestStatus(t, ctx, pool, tenantID, req.ID, RefundRequestPending)
	assertPGCount(t, ctx, pool, `SELECT count(*) FROM payment_refunds WHERE tenant_id=$1 AND idempotency_key=$2`, 1, tenantID, refundRequestIdempotencyKey(req.ID))

	recovered, err := recorder.ApproveRefundRequest(ctx, tenantID, req.ID, 77, "admin_token:77")
	if err != nil {
		t.Fatalf("different admin recover approve: %v", err)
	}
	if recovered.Status != RefundRequestApproved || recovered.DecidedBy != 77 || recovered.DecidedAt == nil {
		t.Fatalf("recovered request=%+v want approved by takeover admin 77", recovered)
	}
	assertPGCount(t, ctx, pool, `SELECT count(*) FROM payment_refunds WHERE tenant_id=$1 AND idempotency_key=$2`, 1, tenantID, refundRequestIdempotencyKey(req.ID))
	assertPGCount(t, ctx, pool, `SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND event_type='payment_refunded'`, 1, tenantID)
	var moneyActorID int64
	if err := pool.QueryRow(ctx, `SELECT actor_id FROM payment_refunds WHERE tenant_id=$1 AND idempotency_key=$2`, tenantID, refundRequestIdempotencyKey(req.ID)).Scan(&moneyActorID); err != nil {
		t.Fatalf("read original money actor: %v", err)
	}
	if moneyActorID != 99 {
		t.Fatalf("money actor=%d want original admin 99", moneyActorID)
	}
}

func TestPostgresRefundRequestApprovalRollsBackMoneyWhenDecisionWriteFails(t *testing.T) {
	ctx := context.Background()
	pool := openPaymentHTTPPool(t, ctx)
	tenantID, userID := seedPaymentHTTPUser(t, ctx, pool, "refund-request-atomic")
	svc := payment.NewService(payment.NewPostgresStore(pool))
	recorder := NewPostgresRefundRequestRecorder(pool, svc)
	order := createCompletedTopupForRefundRequest(t, ctx, svc, tenantID, userID, 1300, "pg-atomic")
	req := createRefundRequestForAdminTest(t, ctx, recorder, tenantID, userID, order.ID, "force decision failure")

	if _, err := pool.Exec(ctx, `
CREATE OR REPLACE FUNCTION huakai_test_fail_refund_request_decision() RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'forced refund request decision failure';
END;
$$ LANGUAGE plpgsql`); err != nil {
		t.Fatalf("create decision failure function: %v", err)
	}
	if _, err := pool.Exec(ctx, `
CREATE TRIGGER huakai_test_fail_refund_request_decision
BEFORE UPDATE ON payment_refund_requests
FOR EACH ROW EXECUTE FUNCTION huakai_test_fail_refund_request_decision()`); err != nil {
		t.Fatalf("create decision failure trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS huakai_test_fail_refund_request_decision ON payment_refund_requests`)
		_, _ = pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS huakai_test_fail_refund_request_decision()`)
	})

	if _, err := recorder.ApproveRefundRequest(ctx, tenantID, req.ID, 88, "admin_token:88"); err == nil {
		t.Fatal("approval unexpectedly succeeded while decision write was forced to fail")
	}
	assertPGRefundRequestStatus(t, ctx, pool, tenantID, req.ID, RefundRequestPending)
	assertPGCount(t, ctx, pool, `SELECT count(*) FROM payment_refunds WHERE tenant_id=$1 AND idempotency_key=$2`, 0, tenantID, refundRequestIdempotencyKey(req.ID))
	assertPGCount(t, ctx, pool, `SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND event_type='payment_refunded'`, 0, tenantID)
	balance, err := svc.GetBalance(ctx, tenantID, userID)
	if err != nil {
		t.Fatalf("balance after rolled-back approval: %v", err)
	}
	if balance.AmountCents != order.AmountCents {
		t.Fatalf("balance after rolled-back approval=%d want %d", balance.AmountCents, order.AmountCents)
	}
}

func assertPGCount(t *testing.T, ctx context.Context, pool pgQueryer, query string, want int64, args ...any) {
	t.Helper()
	var n int64
	if err := pool.QueryRow(ctx, query, args...).Scan(&n); err != nil {
		t.Fatalf("count query %q: %v", query, err)
	}
	if n != want {
		t.Fatalf("count query %q got %d want %d", query, n, want)
	}
}

type pgQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func assertPGRefundRequestStatus(t *testing.T, ctx context.Context, pool pgQueryer, tenantID, requestID int64, want RefundRequestStatus) {
	t.Helper()
	var status RefundRequestStatus
	if err := pool.QueryRow(ctx, `SELECT status FROM payment_refund_requests WHERE tenant_id=$1 AND id=$2`, tenantID, requestID).Scan(&status); err != nil {
		t.Fatalf("read refund request status: %v", err)
	}
	if status != want {
		t.Fatalf("refund request status=%q want %q", status, want)
	}
}
