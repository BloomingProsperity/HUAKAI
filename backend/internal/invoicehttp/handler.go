// HUAKAI · iKun

// 包 invoicehttp 暴露只读的用户发票 / 收据端点。
package invoicehttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/payment"
)

// OrderReader 是收据所需的完整资金安全依赖面。
// 它有意不暴露任何 credit、debit、billing 或 refund 操作。
type OrderReader interface {
	GetOrder(ctx context.Context, tenantID, orderID int64) (payment.Order, error)
}

type Deps struct {
	Orders OrderReader
}

func MountRoutes(r chi.Router, d Deps) {
	r.Get("/orders/{id}/receipt", newGetReceiptHandler(d))
}

func newGetReceiptHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Orders == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "receipt_orders_unavailable", "order receipt dependency unset")
			return
		}
		ident, ok := sessionauth.SessionFromContext(r.Context())
		if !ok || ident.TenantID <= 0 || ident.UserID <= 0 {
			writeJSONError(w, http.StatusUnauthorized, "session_token_required", "session bearer token is required")
			return
		}
		id, ok := parseOrderID(w, r)
		if !ok {
			return
		}
		order, err := d.Orders.GetOrder(r.Context(), ident.TenantID, id)
		if err != nil {
			writePaymentReadError(w, err)
			return
		}
		if order.UserID != ident.UserID {
			writeJSONError(w, http.StatusNotFound, "order_not_found", "payment order not found")
			return
		}
		if !receiptEligibleKind(order.OrderKind) {
			writeJSONError(w, http.StatusNotFound, "order_receipt_not_found", "order receipt not found")
			return
		}
		if order.Status != payment.StatusCompleted {
			writeJSONError(w, http.StatusConflict, "order_receipt_unavailable", "receipt is available only for completed orders")
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(renderReceipt(order)))
	}
}

func parseOrderID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid_order_id", "order id must be a positive int64")
		return 0, false
	}
	return id, true
}

func receiptEligibleKind(kind string) bool {
	return kind == payment.OrderKindTopup || kind == payment.OrderKindSubscription
}

func renderReceipt(order payment.Order) string {
	var b strings.Builder
	b.WriteString("HUAKAI order receipt\n")
	fmt.Fprintf(&b, "order_id: %d\n", order.ID)
	fmt.Fprintf(&b, "tenant_id: %d\n", order.TenantID)
	fmt.Fprintf(&b, "user_id: %d\n", order.UserID)
	fmt.Fprintf(&b, "out_trade_no: %s\n", order.OutTradeNo)
	fmt.Fprintf(&b, "order_kind: %s\n", order.OrderKind)
	fmt.Fprintf(&b, "status: %s\n", order.Status)
	fmt.Fprintf(&b, "amount: %s %s\n", formatCents(order.AmountCents), order.CurrencyCode)
	fmt.Fprintf(&b, "created_at: %s\n", formatTime(order.CreatedAt))
	fmt.Fprintf(&b, "paid_at: %s\n", formatOptionalTime(order.PaidAt))
	fmt.Fprintf(&b, "completed_at: %s\n", formatOptionalTime(order.CompletedAt))
	b.WriteString("tax: not_collected\n")
	return b.String()
}

func formatCents(cents int64) string {
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	return fmt.Sprintf("%s%d.%02d", sign, cents/100, cents%100)
}

func formatOptionalTime(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return formatTime(*t)
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}

func writePaymentReadError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, payment.ErrInvalidInput):
		writeJSONError(w, http.StatusBadRequest, "invalid_payment_request", "payment request is invalid")
	case errors.Is(err, payment.ErrOrderNotFound):
		writeJSONError(w, http.StatusNotFound, "order_not_found", "payment order not found")
	default:
		writeJSONError(w, http.StatusServiceUnavailable, "payment_backend_error", "payment service unavailable")
	}
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": message}})
}
