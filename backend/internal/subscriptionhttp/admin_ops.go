// HUAKAI · iKun

package subscriptionhttp

import (
	"net/http"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/subscription"
)

type extendAssignmentRequest struct {
	TenantID int64      `json:"tenant_id"`
	Days     int        `json:"days,omitempty"`
	Until    *time.Time `json:"until,omitempty"`
}

type bulkAssignRequest struct {
	TenantID int64   `json:"tenant_id"`
	UserIDs  []int64 `json:"user_ids"`
	PlanID   int64   `json:"plan_id"`
}

type revokeAssignmentRequest struct {
	TenantID int64  `json:"tenant_id"`
	Reason   string `json:"reason"`
}

type changePlanRequest struct {
	TenantID       int64 `json:"tenant_id"`
	NewPlanID      int64 `json:"new_plan_id"`
	AllowDowngrade bool  `json:"allow_downgrade,omitempty"`
}

type bulkAssignUserView struct {
	UserID       int64                  `json:"user_id"`
	OK           bool                   `json:"ok"`
	Error        string                 `json:"error,omitempty"`
	Idempotent   bool                   `json:"idempotent,omitempty"`
	Subscription *adminSubscriptionView `json:"subscription,omitempty"`
}

func newAdminUpdatePlanHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(w, r, d)
		if !ok {
			return
		}
		id, ok := parsePathID(w, r)
		if !ok {
			return
		}
		var req createPlanRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if !authorizeSubscriptionTenant(w, ident, req.TenantID, d.PlatformTenantID) {
			return
		}
		if req.ForSale == nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_plan", "for_sale is required for plan update")
			return
		}
		daily, ok := parseCap(w, req.DailyCapUSD, "daily_cap_usd")
		if !ok {
			return
		}
		weekly, ok := parseCap(w, req.WeeklyCapUSD, "weekly_cap_usd")
		if !ok {
			return
		}
		monthly, ok := parseCap(w, req.MonthlyCapUSD, "monthly_cap_usd")
		if !ok {
			return
		}
		plan, err := d.Service.UpdatePlan(r.Context(), subscription.UpdatePlanInput{
			TenantID:      req.TenantID,
			PlanID:        id,
			Name:          req.Name,
			Description:   req.Description,
			PriceCents:    req.PriceCents,
			CurrencyCode:  req.CurrencyCode,
			ValidityDays:  req.ValidityDays,
			GrantedGroup:  req.GrantedGroup,
			DailyCapUSD:   daily,
			WeeklyCapUSD:  weekly,
			MonthlyCapUSD: monthly,
			ForSale:       *req.ForSale,
			SortOrder:     req.SortOrder,
			ActorAdminID:  ident.TokenID, ActorRef: ident.AuditActor(),
			RequestID: requestID(r),
		})
		if err != nil {
			writeSubscriptionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"plan": toPlanView(plan)})
	}
}

func newAdminBulkAssignHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(w, r, d)
		if !ok {
			return
		}
		var req bulkAssignRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if !authorizeSubscriptionTenant(w, ident, req.TenantID, d.PlatformTenantID) {
			return
		}
		result, err := d.Service.BulkAssign(r.Context(), subscription.BulkAssignInput{
			TenantID: req.TenantID, UserIDs: req.UserIDs, PlanID: req.PlanID,
			ActorAdminID: ident.TokenID, ActorRef: ident.AuditActor(), RequestID: requestID(r),
		})
		if err != nil {
			writeSubscriptionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"results": toBulkAssignUserViews(result.Results)})
	}
}

func newAdminExtendHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(w, r, d)
		if !ok {
			return
		}
		id, ok := parsePathID(w, r)
		if !ok {
			return
		}
		var req extendAssignmentRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if !authorizeSubscriptionTenant(w, ident, req.TenantID, d.PlatformTenantID) {
			return
		}
		sub, err := d.Service.ExtendSubscription(r.Context(), subscription.ExtendSubscriptionInput{
			TenantID: req.TenantID, SubscriptionID: id, ActorAdminID: ident.TokenID, ActorRef: ident.AuditActor(),
			RequestID: requestID(r), Days: req.Days, Until: req.Until,
		})
		if err != nil {
			writeSubscriptionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"subscription": toAdminSubscriptionView(sub)})
	}
}

func newAdminResetQuotaHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(w, r, d)
		if !ok {
			return
		}
		id, ok := parsePathID(w, r)
		if !ok {
			return
		}
		var req tenantBodyRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if !authorizeSubscriptionTenant(w, ident, req.TenantID, d.PlatformTenantID) {
			return
		}
		sub, err := d.Service.ResetQuota(r.Context(), subscription.ResetQuotaInput{
			TenantID: req.TenantID, SubscriptionID: id, ActorAdminID: ident.TokenID, ActorRef: ident.AuditActor(), RequestID: requestID(r),
		})
		if err != nil {
			writeSubscriptionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"subscription": toAdminSubscriptionView(sub)})
	}
}

func newAdminChangePlanHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(w, r, d)
		if !ok {
			return
		}
		id, ok := parsePathID(w, r)
		if !ok {
			return
		}
		var req changePlanRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if !authorizeSubscriptionTenant(w, ident, req.TenantID, d.PlatformTenantID) {
			return
		}
		sub, err := d.Service.ChangePlan(r.Context(), subscription.ChangePlanInput{
			TenantID: req.TenantID, SubscriptionID: id, NewPlanID: req.NewPlanID,
			AllowDowngrade: req.AllowDowngrade, ActorAdminID: ident.TokenID, ActorRef: ident.AuditActor(), RequestID: requestID(r),
		})
		if err != nil {
			writeSubscriptionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"subscription": toAdminSubscriptionView(sub)})
	}
}

func newAdminRevokeHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(w, r, d)
		if !ok {
			return
		}
		id, ok := parsePathID(w, r)
		if !ok {
			return
		}
		var req revokeAssignmentRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if !authorizeSubscriptionTenant(w, ident, req.TenantID, d.PlatformTenantID) {
			return
		}
		sub, err := d.Service.RevokeSubscription(r.Context(), subscription.RevokeSubscriptionInput{
			TenantID: req.TenantID, SubscriptionID: id, ActorAdminID: ident.TokenID, ActorRef: ident.AuditActor(),
			Reason: strings.TrimSpace(req.Reason), RequestID: requestID(r),
		})
		if err != nil {
			writeSubscriptionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"subscription": toAdminSubscriptionView(sub)})
	}
}

func toBulkAssignUserViews(results []subscription.BulkAssignUserResult) []bulkAssignUserView {
	out := make([]bulkAssignUserView, 0, len(results))
	for _, item := range results {
		view := bulkAssignUserView{
			UserID: item.UserID, OK: item.OK, Error: item.Error, Idempotent: item.Idempotent,
		}
		if item.OK {
			sub := toAdminSubscriptionView(item.Subscription)
			view.Subscription = &sub
		}
		out = append(out, view)
	}
	return out
}
