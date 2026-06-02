package subscriptionhttp

import (
	"net/http"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/subscription"
)

// 用户路由只接受 session 中的租户和用户身份，避免请求体伪造归属。
type createOrderRequest struct {
	PlanID   int64  `json:"plan_id"`
	Provider string `json:"provider"`
}

type orderResponse struct {
	ID              int64  `json:"id"`
	PlanID          int64  `json:"plan_id"`
	RechargeOrderID int64  `json:"recharge_order_id"`
	TradeNo         string `json:"trade_no"`
	Status          string `json:"status"`
	Price           string `json:"price"`
	CurrencyCode    string `json:"currency_code"`
	Provider        string `json:"provider"`
	CreatedAt       string `json:"created_at"`
}

func newCreateSubscriptionOrderHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveSession(w, r, d)
		if !ok {
			return
		}
		var req createOrderRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		provider := strings.ToLower(strings.TrimSpace(req.Provider))
		if req.PlanID <= 0 || provider == "" {
			writeError(w, http.StatusBadRequest, "invalid_subscription_order", "plan_id and provider are required")
			return
		}
		binding, ok := d.Providers[provider]
		if !ok || binding.Provider == nil {
			writeError(w, http.StatusBadRequest, "payment_provider_unavailable", "payment provider is not configured")
			return
		}
		order, err := d.Service.CreateOrder(r.Context(), subscription.CreateOrderInput{
			TenantID: ident.TenantID,
			UserID:   ident.UserID,
			PlanID:   req.PlanID,
			Provider: provider,
		})
		if err != nil {
			writeSubscriptionError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, orderToResponse(order))
	}
}

func newListUserSubscriptionsHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveSession(w, r, d)
		if !ok {
			return
		}
		activeOnly := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("active_only")), "true")
		rows, err := d.Service.ListUserSubscriptions(r.Context(), subscription.ListUserSubscriptionsInput{
			TenantID:   ident.TenantID,
			UserID:     ident.UserID,
			ActiveOnly: activeOnly,
		})
		if err != nil {
			writeSubscriptionError(w, err)
			return
		}
		items := make([]subscriptionResponse, 0, len(rows))
		for _, row := range rows {
			items = append(items, userSubscriptionToResponse(row))
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
	}
}
