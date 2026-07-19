// HUAKAI · iKun

package paymenthttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/payment"
)

func TestAdminRefundRequestApproveRefundsOnce(t *testing.T) {
	ctx := context.Background()
	svc := payment.NewService(payment.NewMemoryStore())
	order := createCompletedTopupForRefundRequest(t, ctx, svc, 5, 7, 1200, "approve-once")
	if _, err := svc.RefundOrder(ctx, payment.RefundOrderInput{
		TenantID: 5, OrderID: order.ID, AmountCents: 200,
		IdempotencyKey: "approve-once-prior", ActorKind: payment.ActorKindAdmin, ActorID: 99,
	}); err != nil {
		t.Fatalf("prior partial refund: %v", err)
	}
	recorder := NewMemoryRefundRequestRecorderWithRefunds(svc)
	req := createRefundRequestForAdminTest(t, ctx, recorder, 5, 7, order.ID, "user asked")
	router := newRefundRequestAdminRouter(svc, recorder)

	first := postRefundRequestAdminJSON(router, "/payments/refund-requests/"+itoa(req.ID)+"/approve", `{"tenant_id":5}`)
	if first.Code != http.StatusOK {
		t.Fatalf("first approve status=%d want 200; body=%s", first.Code, first.Body.String())
	}
	assertRefundRequestStatusInBody(t, first.Body.Bytes(), RefundRequestApproved)
	balance, err := svc.GetBalance(ctx, 5, 7)
	if err != nil {
		t.Fatalf("balance after first approve: %v", err)
	}
	if balance.AmountCents != 0 {
		t.Fatalf("balance after first approve=%d want 0（审批只补齐此前部分冲正的剩余金额）", balance.AmountCents)
	}

	second := postRefundRequestAdminJSON(router, "/payments/refund-requests/"+itoa(req.ID)+"/approve", `{"tenant_id":5}`)
	if second.Code != http.StatusOK {
		t.Fatalf("second approve status=%d want 200; body=%s", second.Code, second.Body.String())
	}
	balance, err = svc.GetBalance(ctx, 5, 7)
	if err != nil {
		t.Fatalf("balance after second approve: %v", err)
	}
	if balance.AmountCents != 0 {
		t.Fatalf("balance after duplicate approve=%d want 0; mutation removing status/idempotency would double-refund", balance.AmountCents)
	}
}

func TestAdminRefundRequestRejectDoesNotRefund(t *testing.T) {
	ctx := context.Background()
	refunds := &countingRefundService{}
	recorder := NewMemoryRefundRequestRecorderWithRefunds(refunds)
	req := createRefundRequestForAdminTest(t, ctx, recorder, 5, 7, 101, "changed mind")
	router := newRefundRequestAdminRouter(&captureService{}, recorder)

	resp := postRefundRequestAdminJSON(router, "/payments/refund-requests/"+itoa(req.ID)+"/reject", `{"tenant_id":5,"reason":"not eligible"}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("reject status=%d want 200; body=%s", resp.Code, resp.Body.String())
	}
	assertRefundRequestStatusInBody(t, resp.Body.Bytes(), RefundRequestRejected)
	if refunds.refundCalls != 0 {
		t.Fatalf("reject called RefundOrder %d times; reject must not move money", refunds.refundCalls)
	}
}

func TestAdminRefundRequestsListOnlyPending(t *testing.T) {
	ctx := context.Background()
	recorder := NewMemoryRefundRequestRecorder()
	pending := createRefundRequestForAdminTest(t, ctx, recorder, 5, 7, 101, "pending")
	rejected := createRefundRequestForAdminTest(t, ctx, recorder, 5, 8, 102, "reject me")
	if _, err := recorder.RejectRefundRequest(ctx, 5, rejected.ID, "no", 99, "admin_token:99"); err != nil {
		t.Fatalf("reject setup: %v", err)
	}
	router := newRefundRequestAdminRouter(&captureService{}, recorder)

	req := httptest.NewRequest(http.MethodGet, "/payments/refund-requests?tenant_id=5", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("list status=%d want 200; body=%s", resp.Code, resp.Body.String())
	}
	var body struct {
		RefundRequests []refundRequestView `json:"refund_requests"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode list: %v body=%s", err, resp.Body.String())
	}
	if len(body.RefundRequests) != 1 || body.RefundRequests[0].ID != pending.ID {
		t.Fatalf("pending list=%+v want only request %d", body.RefundRequests, pending.ID)
	}
}

func TestRefundRequestAlreadyResolvedMapsTo409(t *testing.T) {
	rec := httptest.NewRecorder()

	writeRefundRequestError(rec, ErrRefundRequestAlreadyResolved)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d want 409; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if body.Error.Code != "refund_request_already_resolved" {
		t.Fatalf("error code=%q want refund_request_already_resolved", body.Error.Code)
	}
}

func TestAdminRefundRequestTenantIsolation(t *testing.T) {
	ctx := context.Background()
	refunds := &countingRefundService{}
	recorder := NewMemoryRefundRequestRecorderWithRefunds(refunds)
	req := createRefundRequestForAdminTest(t, ctx, recorder, 5, 7, 101, "tenant A")
	router := newRefundRequestAdminRouter(&captureService{}, recorder)

	resp := postRefundRequestAdminJSON(router, "/payments/refund-requests/"+itoa(req.ID)+"/approve", `{"tenant_id":6}`)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant approve status=%d want 404; body=%s", resp.Code, resp.Body.String())
	}
	if refunds.refundCalls != 0 {
		t.Fatalf("cross-tenant approve called RefundOrder %d times", refunds.refundCalls)
	}
	stillPending, err := recorder.ApproveRefundRequest(ctx, 6, req.ID, 99, "admin_token:99")
	if err == nil || stillPending.ID != 0 {
		t.Fatalf("direct cross-tenant approve returned request=%+v err=%v, want not found", stillPending, err)
	}
	got, err := recorder.ApproveRefundRequest(ctx, 5, req.ID, 99, "admin_token:99")
	if err != nil {
		t.Fatalf("same-tenant approve after isolation check: %v", err)
	}
	if got.Status != RefundRequestApproved || refunds.refundCalls != 1 {
		t.Fatalf("same-tenant approve status=%q refundCalls=%d, want approved/1", got.Status, refunds.refundCalls)
	}
	again, err := recorder.ApproveRefundRequest(ctx, 5, req.ID, 99, "admin_token:99")
	if err != nil {
		t.Fatalf("duplicate approve after approved: %v", err)
	}
	if again.Status != RefundRequestApproved || refunds.refundCalls != 1 {
		t.Fatalf("duplicate approve status=%q refundCalls=%d, want approved and no second RefundOrder call", again.Status, refunds.refundCalls)
	}
}

func createCompletedTopupForRefundRequest(t *testing.T, ctx context.Context, svc *payment.Service, tenantID, userID, amount int64, tradeSuffix string) payment.Order {
	t.Helper()
	created, err := svc.CreateOrder(ctx, payment.CreateOrderInput{
		TenantID:    tenantID,
		UserID:      userID,
		AmountCents: amount,
		OutTradeNo:  "refund-req-" + tradeSuffix,
		ActorKind:   payment.ActorKindAdmin,
		ActorID:     99,
	})
	if err != nil {
		t.Fatalf("create order: %v", err)
	}
	fulfilled, err := svc.AdminConfirmPaid(ctx, payment.AdminConfirmPaidInput{
		TenantID:     tenantID,
		OrderID:      created.Order.ID,
		ActorAdminID: 99,
	})
	if err != nil {
		t.Fatalf("confirm order: %v", err)
	}
	if fulfilled.Order.Status != payment.StatusCompleted || fulfilled.BalanceCents != amount {
		t.Fatalf("setup status=%q balance=%d want completed/%d", fulfilled.Order.Status, fulfilled.BalanceCents, amount)
	}
	return fulfilled.Order
}

func createRefundRequestForAdminTest(t *testing.T, ctx context.Context, recorder RefundRequestRecorder, tenantID, userID, orderID int64, reason string) RefundRequest {
	t.Helper()
	req, err := recorder.CreateRefundRequest(ctx, RefundRequestInput{
		TenantID: tenantID,
		UserID:   userID,
		OrderID:  orderID,
		Reason:   reason,
		Now:      time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("create refund request: %v", err)
	}
	return req
}

func newRefundRequestAdminRouter(svc Service, recorder RefundRequestRecorder) http.Handler {
	r := chi.NewRouter()
	r.Route("/payments", func(r chi.Router) {
		MountPaymentAdminRoutes(r, AdminDeps{
			Auth:           fakeAdminAuth{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin, TokenID: 99}},
			Service:        svc,
			RefundRequests: recorder,
		})
	})
	return r
}

func postRefundRequestAdminJSON(router http.Handler, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-Id", "req-admin-refund-request")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	return resp
}

func assertRefundRequestStatusInBody(t *testing.T, raw []byte, want RefundRequestStatus) {
	t.Helper()
	var body struct {
		RefundRequest refundRequestView `json:"refund_request"`
	}
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&body); err != nil {
		t.Fatalf("decode refund request body: %v body=%s", err, string(raw))
	}
	if body.RefundRequest.Status != string(want) {
		t.Fatalf("refund_request status=%q want %q body=%s", body.RefundRequest.Status, want, string(raw))
	}
}

func itoa(v int64) string {
	return strconv.FormatInt(v, 10)
}

type countingRefundService struct {
	refundCalls int
}

func (s *countingRefundService) GetOrder(context.Context, int64, int64) (payment.Order, error) {
	return payment.Order{ID: 101, TenantID: 5, UserID: 7, AmountCents: 100, Status: payment.StatusCompleted, OrderKind: payment.OrderKindTopup}, nil
}

func (s *countingRefundService) RefundOrder(context.Context, payment.RefundOrderInput) (payment.RefundResult, error) {
	s.refundCalls++
	return payment.RefundResult{Order: payment.Order{ID: 101, Status: payment.StatusRefunded}}, nil
}
