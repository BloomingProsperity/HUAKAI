// HUAKAI · iKun

package paymenthttp

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/payment"
)

type retryRequest struct {
	TenantID int64 `json:"tenant_id"`
}

type providerConfigRequest struct {
	Enabled     *bool  `json:"enabled"`
	CheckoutURL string `json:"checkout_url,omitempty"`
}

type providerConfigView struct {
	ProviderKind string     `json:"provider_kind"`
	Enabled      bool       `json:"enabled"`
	CheckoutURL  string     `json:"checkout_url"`
	Source       string     `json:"source"`
	UpdatedBy    string     `json:"updated_by,omitempty"`
	UpdatedAt    *time.Time `json:"updated_at,omitempty"`
}

type dailyStatsView struct {
	Date        string `json:"date"`
	OrderCount  int    `json:"order_count"`
	AmountCents int64  `json:"amount_cents"`
}

type dashboardStatsView struct {
	TotalAmountCents   int64            `json:"total_amount_cents"`
	TotalCount         int              `json:"total_count"`
	TodayCount         int              `json:"today_count"`
	AverageAmountCents int64            `json:"average_amount_cents"`
	DailySeries        []dailyStatsView `json:"daily_series"`
}

func newAdminListOrdersHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolveAdmin(w, r, d); !ok {
			return
		}
		filter, ok := parseOrderListFilter(w, r)
		if !ok {
			return
		}
		orders, err := d.Service.AdminListOrders(r.Context(), filter)
		if err != nil {
			writePaymentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"orders": toAdminOrderViews(orders)})
	}
}

func newAdminDashboardHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolveAdmin(w, r, d); !ok {
			return
		}
		filter, ok := parseDashboardFilter(w, r)
		if !ok {
			return
		}
		stats, err := d.Service.DashboardStats(r.Context(), filter)
		if err != nil {
			writePaymentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toDashboardStatsView(stats))
	}
}

func newAdminAuditHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolveAdmin(w, r, d); !ok {
			return
		}
		tenantID, ok := parsePositiveQuery(w, r, "tenant_id")
		if !ok {
			return
		}
		id, ok := parsePathID(w, r)
		if !ok {
			return
		}
		events, err := d.Service.ListAuditEvents(r.Context(), tenantID, id)
		if err != nil {
			writePaymentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"audit_events": toAuditEventViews(events)})
	}
}

func newAdminRetryHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(w, r, d)
		if !ok {
			return
		}
		id, ok := parsePathID(w, r)
		if !ok {
			return
		}
		var req retryRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		res, err := d.Service.RetryFulfillment(r.Context(), payment.RetryFulfillmentInput{
			TenantID:     req.TenantID,
			OrderID:      id,
			ActorAdminID: ident.TokenID,
			RequestID:    requestID(r),
		})
		if err != nil {
			writePaymentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, fulfillResponse(res))
	}
}

func newAdminGetProviderConfigHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolveAdmin(w, r, d); !ok {
			return
		}
		kind, ok := providerKindFromPath(w, r)
		if !ok {
			return
		}
		svc := providerConfigService(d)
		if svc == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "payment provider config dependency unset")
			return
		}
		cfg, err := svc.GetProviderRuntimeConfig(r.Context(), kind)
		if err != nil {
			writePaymentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"provider": toProviderConfigView(cfg)})
	}
}

func newAdminPutProviderConfigHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(w, r, d)
		if !ok {
			return
		}
		kind, ok := providerKindFromPath(w, r)
		if !ok {
			return
		}
		var req providerConfigRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.Enabled == nil {
			writeJSONError(w, http.StatusBadRequest, "provider_enabled_required", "enabled is required")
			return
		}
		svc := providerConfigService(d)
		if svc == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "payment provider config dependency unset")
			return
		}
		cfg, err := svc.SetProviderRuntimeConfig(r.Context(), payment.ProviderRuntimeConfigInput{
			ProviderKind: kind,
			Enabled:      *req.Enabled,
			CheckoutURL:  req.CheckoutURL,
			UpdatedBy:    strconv.FormatInt(ident.TokenID, 10),
		})
		if err != nil {
			writePaymentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"provider": toProviderConfigView(cfg)})
	}
}

func providerConfigService(d AdminDeps) ProviderRuntimeConfigService {
	if d.ProviderConfig != nil {
		return d.ProviderConfig
	}
	return d.Service
}

func parseOrderListFilter(w http.ResponseWriter, r *http.Request) (payment.OrderListFilter, bool) {
	tenantID, ok := parsePositiveQuery(w, r, "tenant_id")
	if !ok {
		return payment.OrderListFilter{}, false
	}
	userID, ok := parseOptionalPositiveQuery(w, r, "user_id")
	if !ok {
		return payment.OrderListFilter{}, false
	}
	from, ok := parseOptionalTimeQuery(w, r, "created_from")
	if !ok {
		return payment.OrderListFilter{}, false
	}
	to, ok := parseOptionalTimeQuery(w, r, "created_to")
	if !ok {
		return payment.OrderListFilter{}, false
	}
	return payment.OrderListFilter{
		TenantID: tenantID,
		UserID:   userID,
		Status:   payment.OrderStatus(strings.TrimSpace(r.URL.Query().Get("status"))),
		From:     from,
		To:       to,
		Limit:    parseLimit(r),
		Offset:   parseOffset(r),
	}, true
}

func parseDashboardFilter(w http.ResponseWriter, r *http.Request) (payment.DashboardFilter, bool) {
	tenantID, ok := parsePositiveQuery(w, r, "tenant_id")
	if !ok {
		return payment.DashboardFilter{}, false
	}
	from, ok := parseOptionalTimeQuery(w, r, "created_from")
	if !ok {
		return payment.DashboardFilter{}, false
	}
	to, ok := parseOptionalTimeQuery(w, r, "created_to")
	if !ok {
		return payment.DashboardFilter{}, false
	}
	filter := payment.DashboardFilter{TenantID: tenantID}
	if from != nil {
		filter.From = *from
	}
	if to != nil {
		filter.To = *to
	}
	return filter, true
}

func parseOptionalPositiveQuery(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return 0, true
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		writeJSONError(w, http.StatusBadRequest, name+"_invalid", name+" query parameter must be positive")
		return 0, false
	}
	return n, true
}

func parseOptionalTimeQuery(w http.ResponseWriter, r *http.Request, name string) (*time.Time, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return nil, true
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, name+"_invalid", name+" query parameter must be RFC3339")
		return nil, false
	}
	t = t.UTC()
	return &t, true
}

func parseOffset(r *http.Request) int {
	raw := strings.TrimSpace(r.URL.Query().Get("offset"))
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func providerKindFromPath(w http.ResponseWriter, r *http.Request) (payment.ProviderKind, bool) {
	kind := payment.ProviderKind(strings.TrimSpace(chi.URLParam(r, "provider")))
	if kind != payment.ProviderManual && kind != payment.ProviderTaobao {
		writeJSONError(w, http.StatusBadRequest, "unknown_provider", "provider kind is not supported")
		return "", false
	}
	return kind, true
}

func fulfillResponse(res payment.FulfillResult) map[string]any {
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
	return resp
}

func toProviderConfigView(cfg payment.ProviderRuntimeConfig) providerConfigView {
	view := providerConfigView{
		ProviderKind: string(cfg.ProviderKind),
		Enabled:      cfg.Enabled,
		CheckoutURL:  cfg.CheckoutURL,
		Source:       cfg.Source,
		UpdatedBy:    cfg.UpdatedBy,
	}
	if !cfg.UpdatedAt.IsZero() {
		updatedAt := cfg.UpdatedAt.UTC()
		view.UpdatedAt = &updatedAt
	}
	return view
}

func toDashboardStatsView(stats payment.DashboardStats) dashboardStatsView {
	out := dashboardStatsView{
		TotalAmountCents:   stats.TotalAmountCents,
		TotalCount:         stats.TotalCount,
		TodayCount:         stats.TodayCount,
		AverageAmountCents: stats.AverageAmountCents,
		DailySeries:        make([]dailyStatsView, 0, len(stats.DailySeries)),
	}
	for _, day := range stats.DailySeries {
		out.DailySeries = append(out.DailySeries, dailyStatsView(day))
	}
	return out
}
