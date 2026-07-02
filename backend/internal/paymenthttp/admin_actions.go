// HUAKAI · iKun

package paymenthttp

import (
	"net/http"

	"github.com/BloomingProsperity/HUAKAI/internal/payment"
)

type confirmRequest struct {
	TenantID      int64  `json:"tenant_id"`
	ConfirmReason string `json:"confirm_reason,omitempty"`
}

type cancelRequest struct {
	TenantID int64  `json:"tenant_id"`
	Reason   string `json:"reason,omitempty"`
}

func newAdminConfirmHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(w, r, d)
		if !ok {
			return
		}
		id, ok := parsePathID(w, r)
		if !ok {
			return
		}
		var req confirmRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		res, err := d.Service.AdminConfirmPaid(r.Context(), payment.AdminConfirmPaidInput{
			TenantID:      req.TenantID,
			OrderID:       id,
			ActorAdminID:  ident.TokenID,
			ActorRef:      ident.AuditActor(),
			ConfirmReason: req.ConfirmReason,
			RequestID:     requestID(r),
		})
		if err != nil {
			writePaymentError(w, err)
			return
		}
		resp := map[string]any{
			"order":      toAdminOrderView(res.Order),
			"idempotent": res.Idempotent,
		}
		if res.Subscription != nil {
			resp["subscription"] = toSubscriptionGrantView(res.Subscription)
		} else {
			resp["credit"] = toCreditView(res.Credit)
			resp["balance_cents"] = res.BalanceCents
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// newAdminCancelHandler 管理员取消任意 pending 订单(运营撤单)。
func newAdminCancelHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(w, r, d)
		if !ok {
			return
		}
		id, ok := parsePathID(w, r)
		if !ok {
			return
		}
		var req cancelRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		order, err := d.Service.CancelOrder(r.Context(), payment.CancelOrderInput{
			TenantID:  req.TenantID,
			OrderID:   id,
			UserID:    0,
			ActorKind: payment.ActorKindAdmin,
			ActorID:   ident.TokenID,
			Reason:    req.Reason,
			RequestID: requestID(r),
		})
		if err != nil {
			writePaymentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"order": toAdminOrderView(order)})
	}
}
