package subscriptionhttp

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/payment"
	"github.com/BloomingProsperity/HUAKAI/internal/subscription"
)

// 响应和错误映射保持在 HTTP 层，领域层只返回状态机错误。
func resolveSession(w http.ResponseWriter, r *http.Request, d Deps) (sessionauth.SessionIdentity, bool) {
	if d.Service == nil {
		writeError(w, http.StatusServiceUnavailable, "subscription_service_unavailable", "subscription service unset")
		return sessionauth.SessionIdentity{}, false
	}
	ident, ok := sessionauth.SessionFromContext(r.Context())
	if !ok || ident.TenantID <= 0 || ident.UserID <= 0 {
		writeError(w, http.StatusUnauthorized, "session_required", "session bearer token is required")
		return sessionauth.SessionIdentity{}, false
	}
	return ident, true
}

func resolveAdmin(w http.ResponseWriter, r *http.Request, d Deps) (admin.AdminIdentity, bool) {
	if d.Service == nil || d.AdminAuth == nil {
		writeError(w, http.StatusServiceUnavailable, "subscription_admin_unavailable", "subscription admin dependency unset")
		return admin.AdminIdentity{}, false
	}
	ident, err := d.AdminAuth.Resolve(r.Context(), r)
	if err != nil {
		writeAdminError(w, err)
		return admin.AdminIdentity{}, false
	}
	return ident, true
}

func resolveAdminTenantQuery(w http.ResponseWriter, r *http.Request, d Deps) (admin.AdminIdentity, int64, bool) {
	ident, ok := resolveAdmin(w, r, d)
	if !ok {
		return admin.AdminIdentity{}, 0, false
	}
	tenantID, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("tenant_id")), 10, 64)
	if err != nil || tenantID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_tenant_id", "tenant_id query parameter is required")
		return admin.AdminIdentity{}, 0, false
	}
	return ident, tenantID, true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "invalid_json", "request body is required")
			return false
		}
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return false
	}
	return true
}

func parsePathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(chi.URLParam(r, "id")), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_id", "id must be a positive int64")
		return 0, false
	}
	return id, true
}

func writeSubscriptionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, subscription.ErrInvalidInput), errors.Is(err, payment.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_subscription_request", "subscription request is invalid")
	case errors.Is(err, subscription.ErrPlanNotFound), errors.Is(err, subscription.ErrOrderNotFound):
		writeError(w, http.StatusNotFound, "subscription_not_found", "subscription resource not found")
	case errors.Is(err, payment.ErrUserNotFound):
		writeError(w, http.StatusNotFound, "subscription_user_not_found", "session user not found")
	case errors.Is(err, payment.ErrAccountInactive):
		writeError(w, http.StatusBadRequest, "subscription_account_inactive", "tenant or user is inactive")
	case errors.Is(err, subscription.ErrPlanDisabled):
		writeError(w, http.StatusConflict, "subscription_plan_disabled", "subscription plan is disabled")
	case errors.Is(err, subscription.ErrPurchaseLimit):
		writeError(w, http.StatusConflict, "subscription_purchase_limit", "subscription purchase limit reached")
	case errors.Is(err, subscription.ErrPlanConflict):
		writeError(w, http.StatusConflict, "subscription_plan_conflict", "subscription plan conflicts with an existing plan")
	case errors.Is(err, subscription.ErrOrderStateConflict), errors.Is(err, subscription.ErrPaymentMismatch), errors.Is(err, payment.ErrExternalTradeConflict):
		writeError(w, http.StatusConflict, "subscription_state_conflict", "subscription order state conflict")
	case errors.Is(err, payment.ErrPendingLimit):
		writeError(w, http.StatusConflict, "subscription_payment_pending_limit", "too many pending payment orders")
	case errors.Is(err, payment.ErrDailyAmountLimit):
		writeError(w, http.StatusConflict, "subscription_payment_daily_limit", "daily payment amount limit reached")
	case errors.Is(err, subscription.ErrStoreNotConfigured), errors.Is(err, subscription.ErrPaymentRequired), errors.Is(err, payment.ErrStoreNotConfigured):
		writeError(w, http.StatusServiceUnavailable, "subscription_backend_unavailable", "subscription backend unavailable")
	default:
		writeError(w, http.StatusServiceUnavailable, "subscription_backend_error", "subscription backend transient failure")
	}
}

func writeAdminError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, admin.ErrAdminUnauthorized):
		writeError(w, http.StatusUnauthorized, "admin_unauthorized", "admin bearer token is required")
	case errors.Is(err, admin.ErrAdminForbidden):
		writeError(w, http.StatusForbidden, "admin_forbidden", "admin token cannot access this tenant")
	case errors.Is(err, admin.ErrAdminBadRequest):
		writeError(w, http.StatusBadRequest, "admin_bad_request", "admin request is invalid")
	default:
		writeError(w, http.StatusServiceUnavailable, "admin_backend_error", "admin backend unavailable")
	}
}

func planToResponse(plan subscription.Plan) planResponse {
	return planResponse{
		ID:                        plan.ID,
		TenantID:                  plan.TenantID,
		Code:                      plan.Code,
		Name:                      plan.Name,
		Description:               plan.Description,
		Enabled:                   plan.Enabled,
		Price:                     plan.Price.StringFixed(8),
		CurrencyCode:              strings.TrimSpace(plan.CurrencyCode),
		DurationUnit:              string(plan.DurationUnit),
		DurationValue:             plan.DurationValue,
		DurationSeconds:           plan.DurationSeconds,
		QuotaLimit:                plan.QuotaLimit,
		QuotaResetPeriod:          string(plan.QuotaResetPeriod),
		QuotaResetIntervalSeconds: plan.QuotaResetIntervalSeconds,
		MaxPurchasesPerUser:       plan.MaxPurchasesPerUser,
		SortOrder:                 plan.SortOrder,
		CreatedAt:                 formatTime(plan.CreatedAt),
		UpdatedAt:                 formatTime(plan.UpdatedAt),
	}
}

func orderToResponse(order subscription.Order) orderResponse {
	return orderResponse{
		ID:              order.ID,
		PlanID:          order.PlanID,
		RechargeOrderID: order.RechargeOrderID,
		TradeNo:         order.TradeNo,
		Status:          string(order.Status),
		Price:           order.Price.StringFixed(8),
		CurrencyCode:    strings.TrimSpace(order.CurrencyCode),
		Provider:        order.Provider,
		CreatedAt:       formatTime(order.CreatedAt),
	}
}

type subscriptionResponse struct {
	ID               int64  `json:"id"`
	PlanID           int64  `json:"plan_id"`
	SourceOrderID    int64  `json:"source_order_id,omitempty"`
	Status           string `json:"status"`
	QuotaLimit       int64  `json:"quota_limit"`
	QuotaUsed        int64  `json:"quota_used"`
	NextQuotaResetAt string `json:"next_quota_reset_at,omitempty"`
	ExpiresAt        string `json:"expires_at,omitempty"`
}

func userSubscriptionToResponse(sub subscription.UserSubscription) subscriptionResponse {
	resp := subscriptionResponse{
		ID:            sub.ID,
		PlanID:        sub.PlanID,
		SourceOrderID: sub.SourceOrderID,
		Status:        string(sub.Status),
		QuotaLimit:    sub.QuotaLimit,
		QuotaUsed:     sub.QuotaUsed,
	}
	if sub.NextQuotaResetAt != nil {
		resp.NextQuotaResetAt = formatTime(*sub.NextQuotaResetAt)
	}
	if sub.ExpiresAt != nil {
		resp.ExpiresAt = formatTime(*sub.ExpiresAt)
	}
	return resp
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]map[string]string{
		"error": {"code": code, "message": message},
	})
}
