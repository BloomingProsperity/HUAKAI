// HUAKAI · iKun

package invoicehttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/payment"
)

type receiptOrderStub struct {
	order payment.Order
	err   error
	calls int
}

func (s *receiptOrderStub) GetOrder(_ context.Context, tenantID, orderID int64) (payment.Order, error) {
	s.calls++
	if s.order.TenantID != tenantID || s.order.ID != orderID {
		return payment.Order{}, payment.ErrOrderNotFound
	}
	if s.err != nil {
		return payment.Order{}, s.err
	}
	return s.order, nil
}

// receiptReadonlyService intentionally has no credit/debit/refund methods. If the handler
// dependency grows beyond GetOrder, this compile-time assignment fails.
type receiptReadonlyService interface {
	GetOrder(context.Context, int64, int64) (payment.Order, error)
}

var _ receiptReadonlyService = (*receiptOrderStub)(nil)

func TestReceiptRendersOrder(t *testing.T) {
	paidAt := time.Date(2026, 6, 1, 12, 30, 0, 0, time.UTC)
	completedAt := time.Date(2026, 6, 1, 12, 35, 0, 0, time.UTC)
	svc := &receiptOrderStub{order: payment.Order{
		ID:           55,
		TenantID:     7,
		UserID:       42,
		OutTradeNo:   "trade-pay-093",
		AmountCents:  12345,
		CurrencyCode: "USD",
		OrderKind:    payment.OrderKindTopup,
		Status:       payment.StatusCompleted,
		CreatedAt:    time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
		PaidAt:       &paidAt,
		CompletedAt:  &completedAt,
	}}
	router := newReceiptTestRouter(svc, sessionauth.SessionIdentity{TenantID: 7, UserID: 42})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/me/orders/55/receipt", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("Content-Type=%q want text/plain; charset=utf-8", got)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"out_trade_no: trade-pay-093",
		"amount: 123.45 USD",
		"status: completed",
		"created_at: 2026-06-01T12:00:00Z",
		"paid_at: 2026-06-01T12:30:00Z",
		"completed_at: 2026-06-01T12:35:00Z",
	} {
		// MUTATION: hard-code or omit real order fields in the renderer; this assertion turns red.
		if !strings.Contains(body, want) {
			t.Fatalf("receipt body missing %q:\n%s", want, body)
		}
	}
	if svc.calls != 1 {
		t.Fatalf("GetOrder calls=%d want 1", svc.calls)
	}
}

func TestReceiptOwnershipGuard(t *testing.T) {
	svc := &receiptOrderStub{order: payment.Order{
		ID:           55,
		TenantID:     7,
		UserID:       99,
		OutTradeNo:   "other-user-order",
		AmountCents:  99999,
		CurrencyCode: "USD",
		OrderKind:    payment.OrderKindTopup,
		Status:       payment.StatusCompleted,
	}}
	router := newReceiptTestRouter(svc, sessionauth.SessionIdentity{TenantID: 7, UserID: 42})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/me/orders/55/receipt", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-user receipt status=%d body=%s want 404", rec.Code, rec.Body.String())
	}
	// MUTATION: remove ownership guard; the other user's out_trade_no leaks in a 200 body.
	if strings.Contains(rec.Body.String(), "other-user-order") {
		t.Fatalf("cross-user receipt leaked another user's order: %s", rec.Body.String())
	}
}

func TestReceiptRejectsIncompleteOrder(t *testing.T) {
	svc := &receiptOrderStub{order: payment.Order{
		ID:           55,
		TenantID:     7,
		UserID:       42,
		OutTradeNo:   "pending-order",
		AmountCents:  12345,
		CurrencyCode: "USD",
		OrderKind:    payment.OrderKindSubscription,
		Status:       payment.StatusPending,
	}}
	router := newReceiptTestRouter(svc, sessionauth.SessionIdentity{TenantID: 7, UserID: 42})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/me/orders/55/receipt", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("pending receipt status=%d body=%s want 409", rec.Code, rec.Body.String())
	}
}

func TestReceiptNoMoneyCall(t *testing.T) {
	svc := &receiptOrderStub{order: payment.Order{
		ID:           55,
		TenantID:     7,
		UserID:       42,
		AmountCents:  12345,
		CurrencyCode: "USD",
		OrderKind:    payment.OrderKindTopup,
		Status:       payment.StatusCompleted,
	}}
	var readonly OrderReader = svc
	router := newReceiptTestRouter(readonly, sessionauth.SessionIdentity{TenantID: 7, UserID: 42})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/me/orders/55/receipt", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("receipt status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if svc.calls != 1 {
		t.Fatalf("GetOrder calls=%d want 1", svc.calls)
	}
}

func newReceiptTestRouter(svc OrderReader, ident sessionauth.SessionIdentity) http.Handler {
	r := chi.NewRouter()
	r.Route("/v1/me", func(r chi.Router) {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				next.ServeHTTP(w, req.WithContext(sessionauth.ContextWithSession(req.Context(), ident)))
			})
		})
		MountRoutes(r, Deps{Orders: svc})
	})
	return r
}
