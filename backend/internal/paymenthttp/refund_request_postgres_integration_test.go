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
	approvedReq := createRefundRequestForAdminTest(t, ctx, recorder, tenantA, userA, approvedOrder.ID, "approve me")
	if pending, err := recorder.ListPendingRefundRequests(ctx, tenantA); err != nil || len(pending) != 1 || pending[0].ID != approvedReq.ID {
		t.Fatalf("pending before approve=(%+v,%v), want only request %d", pending, err, approvedReq.ID)
	}
	if _, err := recorder.ApproveRefundRequest(ctx, tenantB, approvedReq.ID, 99); !errors.Is(err, ErrRefundRequestNotFound) {
		t.Fatalf("cross-tenant approve err=%v want ErrRefundRequestNotFound", err)
	}
	first, err := recorder.ApproveRefundRequest(ctx, tenantA, approvedReq.ID, 99)
	if err != nil {
		t.Fatalf("first approve: %v", err)
	}
	second, err := recorder.ApproveRefundRequest(ctx, tenantA, approvedReq.ID, 99)
	if err != nil {
		t.Fatalf("duplicate approve: %v", err)
	}
	if first.Status != RefundRequestApproved || second.Status != RefundRequestApproved {
		t.Fatalf("approve statuses first=%q second=%q want approved", first.Status, second.Status)
	}
	assertPGCount(t, ctx, pool, `SELECT count(*) FROM payment_refunds WHERE tenant_id=$1 AND order_id=$2`, 1, tenantA, approvedOrder.ID)
	assertPGCount(t, ctx, pool, `SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND event_type='payment_refunded'`, 1, tenantA)
	balance, err := svc.GetBalance(ctx, tenantA, userA)
	if err != nil {
		t.Fatalf("balance after duplicate approve: %v", err)
	}
	if balance.AmountCents != 0 {
		t.Fatalf("balance after duplicate approve=%d want 0", balance.AmountCents)
	}

	rejectedOrder := createCompletedTopupForRefundRequest(t, ctx, svc, tenantB, userB, 900, "pg-reject")
	rejectedReq := createRefundRequestForAdminTest(t, ctx, recorder, tenantB, userB, rejectedOrder.ID, "reject me")
	rejected, err := recorder.RejectRefundRequest(ctx, tenantB, rejectedReq.ID, "not eligible", 77)
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if rejected.Status != RefundRequestRejected || rejected.DecidedBy != 77 || rejected.DecidedAt == nil {
		t.Fatalf("rejected request=%+v want rejected with decision metadata", rejected)
	}
	assertPGCount(t, ctx, pool, `SELECT count(*) FROM payment_refunds WHERE tenant_id=$1 AND order_id=$2`, 0, tenantB, rejectedOrder.ID)
	balance, err = svc.GetBalance(ctx, tenantB, userB)
	if err != nil {
		t.Fatalf("balance after reject: %v", err)
	}
	if balance.AmountCents != 900 {
		t.Fatalf("balance after reject=%d want original 900", balance.AmountCents)
	}
	if pending, err := recorder.ListPendingRefundRequests(ctx, tenantB); err != nil || len(pending) != 0 {
		t.Fatalf("pending after reject=(%+v,%v), want none", pending, err)
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
