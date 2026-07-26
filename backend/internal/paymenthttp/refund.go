// HUAKAI · iKun

package paymenthttp

import (
	"net/http"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/payment"
)

type refundRequest struct {
	TenantID       int64  `json:"tenant_id"`
	AmountCents    int64  `json:"amount_cents"`
	IdempotencyKey string `json:"idempotency_key"`
	Reason         string `json:"reason,omitempty"`
}

type refundView struct {
	ID                   int64     `json:"id"`
	AmountCents          int64     `json:"amount_cents"`
	RequestedAmountCents int64     `json:"requested_amount_cents"`
	RequireExact         bool      `json:"require_exact"`
	CurrencyCode         string    `json:"currency_code"`
	IdempotencyKey       string    `json:"idempotency_key"`
	Reason               string    `json:"reason,omitempty"`
	BillingEventID       int64     `json:"billing_event_id,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
}

func toRefundView(r payment.RefundRecord) refundView {
	return refundView{
		ID: r.ID, AmountCents: r.AmountCents, RequestedAmountCents: r.RequestedAmountCents,
		RequireExact: r.RequireExact, CurrencyCode: r.CurrencyCode,
		IdempotencyKey: r.IdempotencyKey, Reason: r.Reason,
		BillingEventID: r.BillingEventID, CreatedAt: r.CreatedAt,
	}
}

// newAdminRefundHandler 管理员退款已入账充值订单。
func newAdminRefundHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(w, r, d)
		if !ok {
			return
		}
		id, ok := parsePathID(w, r)
		if !ok {
			return
		}
		var req refundRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if !authorizePaymentTenant(w, ident, req.TenantID, d.PlatformTenantID) {
			return
		}
		res, err := d.Service.RefundOrder(r.Context(), payment.RefundOrderInput{
			TenantID:       req.TenantID,
			OrderID:        id,
			AmountCents:    req.AmountCents,
			IdempotencyKey: req.IdempotencyKey,
			Reason:         req.Reason,
			ActorKind:      payment.ActorKindAdmin,
			ActorID:        ident.TokenID,
			ActorRef:       ident.AuditActor(), // 双身份归属:session-admin 靠此列,token 双写
			RequestID:      requestID(r),
		})
		if err != nil {
			writePaymentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"order":                      toAdminOrderView(res.Order),
			"refund":                     toRefundView(res.Refund),
			"balance_cents":              res.BalanceCents,
			"cumulative_refunded_cents":  res.CumulativeRefundedCents,
			"remaining_refundable_cents": res.RemainingRefundableCents,
			"idempotent":                 res.Idempotent,
			"already_satisfied":          res.AlreadySatisfied,
		})
	}
}
