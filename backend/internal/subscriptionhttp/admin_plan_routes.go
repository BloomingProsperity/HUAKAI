package subscriptionhttp

import (
	"net/http"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/subscription"
)

// 管理端计划 CRUD 只处理计划资料，不直接改支付或余额入账。
type planRequest struct {
	TenantID                  int64           `json:"tenant_id"`
	Code                      string          `json:"code"`
	Name                      string          `json:"name"`
	Description               string          `json:"description,omitempty"`
	Enabled                   *bool           `json:"enabled,omitempty"`
	Price                     decimal.Decimal `json:"price"`
	Currency                  string          `json:"currency,omitempty"`
	CurrencyCode              string          `json:"currency_code,omitempty"`
	DurationUnit              string          `json:"duration_unit"`
	DurationValue             int             `json:"duration_value"`
	DurationSeconds           int64           `json:"duration_seconds,omitempty"`
	QuotaLimit                int64           `json:"quota_limit,omitempty"`
	QuotaResetPeriod          string          `json:"quota_reset_period,omitempty"`
	QuotaResetIntervalSeconds int64           `json:"quota_reset_interval_seconds,omitempty"`
	MaxPurchasesPerUser       int             `json:"max_purchases_per_user,omitempty"`
	SortOrder                 int             `json:"sort_order,omitempty"`
}

type planPatchRequest struct {
	Code                      *string          `json:"code,omitempty"`
	Name                      *string          `json:"name,omitempty"`
	Description               *string          `json:"description,omitempty"`
	Enabled                   *bool            `json:"enabled,omitempty"`
	Price                     *decimal.Decimal `json:"price,omitempty"`
	Currency                  *string          `json:"currency,omitempty"`
	CurrencyCode              *string          `json:"currency_code,omitempty"`
	DurationUnit              *string          `json:"duration_unit,omitempty"`
	DurationValue             *int             `json:"duration_value,omitempty"`
	DurationSeconds           *int64           `json:"duration_seconds,omitempty"`
	QuotaLimit                *int64           `json:"quota_limit,omitempty"`
	QuotaResetPeriod          *string          `json:"quota_reset_period,omitempty"`
	QuotaResetIntervalSeconds *int64           `json:"quota_reset_interval_seconds,omitempty"`
	MaxPurchasesPerUser       *int             `json:"max_purchases_per_user,omitempty"`
	SortOrder                 *int             `json:"sort_order,omitempty"`
}

type planResponse struct {
	ID                        int64  `json:"id"`
	TenantID                  int64  `json:"tenant_id"`
	Code                      string `json:"code"`
	Name                      string `json:"name"`
	Description               string `json:"description"`
	Enabled                   bool   `json:"enabled"`
	Price                     string `json:"price"`
	CurrencyCode              string `json:"currency_code"`
	DurationUnit              string `json:"duration_unit"`
	DurationValue             int    `json:"duration_value"`
	DurationSeconds           int64  `json:"duration_seconds"`
	QuotaLimit                int64  `json:"quota_limit"`
	QuotaResetPeriod          string `json:"quota_reset_period"`
	QuotaResetIntervalSeconds int64  `json:"quota_reset_interval_seconds"`
	MaxPurchasesPerUser       int    `json:"max_purchases_per_user"`
	SortOrder                 int    `json:"sort_order"`
	CreatedAt                 string `json:"created_at"`
	UpdatedAt                 string `json:"updated_at"`
}

func newCreatePlanHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(w, r, d)
		if !ok {
			return
		}
		var req planRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if err := ident.CanIssueForTenant(req.TenantID); err != nil {
			writeAdminError(w, err)
			return
		}
		enabled := true
		if req.Enabled != nil {
			enabled = *req.Enabled
		}
		plan, err := d.Service.CreatePlan(r.Context(), subscription.PlanInput{
			TenantID:                  req.TenantID,
			Code:                      req.Code,
			Name:                      req.Name,
			Description:               req.Description,
			Enabled:                   enabled,
			Price:                     req.Price,
			CurrencyCode:              firstNonEmpty(req.CurrencyCode, req.Currency),
			DurationUnit:              subscription.DurationUnit(req.DurationUnit),
			DurationValue:             req.DurationValue,
			DurationSeconds:           req.DurationSeconds,
			QuotaLimit:                req.QuotaLimit,
			QuotaResetPeriod:          subscription.ResetPeriod(req.QuotaResetPeriod),
			QuotaResetIntervalSeconds: req.QuotaResetIntervalSeconds,
			MaxPurchasesPerUser:       req.MaxPurchasesPerUser,
			SortOrder:                 req.SortOrder,
		})
		if err != nil {
			writeSubscriptionError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, planToResponse(plan))
	}
}

func newListPlansHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, tenantID, ok := resolveAdminTenantQuery(w, r, d)
		if !ok {
			return
		}
		if err := ident.CanIssueForTenant(tenantID); err != nil {
			writeAdminError(w, err)
			return
		}
		includeArchived := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("include_archived")), "true")
		rows, err := d.Service.ListPlans(r.Context(), tenantID, includeArchived)
		if err != nil {
			writeSubscriptionError(w, err)
			return
		}
		items := make([]planResponse, 0, len(rows))
		for _, row := range rows {
			items = append(items, planToResponse(row))
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
	}
}

func newGetPlanHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, tenantID, ok := resolveAdminTenantQuery(w, r, d)
		if !ok {
			return
		}
		if err := ident.CanIssueForTenant(tenantID); err != nil {
			writeAdminError(w, err)
			return
		}
		planID, ok := parsePathID(w, r)
		if !ok {
			return
		}
		plan, err := d.Service.GetPlan(r.Context(), tenantID, planID)
		if err != nil {
			writeSubscriptionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, planToResponse(plan))
	}
}

func newUpdatePlanHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, tenantID, ok := resolveAdminTenantQuery(w, r, d)
		if !ok {
			return
		}
		if err := ident.CanIssueForTenant(tenantID); err != nil {
			writeAdminError(w, err)
			return
		}
		planID, ok := parsePathID(w, r)
		if !ok {
			return
		}
		var req planPatchRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		patch := subscription.PlanPatch{TenantID: tenantID, ID: planID}
		if req.Code != nil && strings.TrimSpace(*req.Code) != "" {
			patch.Code = req.Code
		}
		if req.Name != nil && strings.TrimSpace(*req.Name) != "" {
			patch.Name = req.Name
		}
		patch.Description = req.Description
		patch.Enabled = req.Enabled
		if req.Price != nil {
			patch.Price = req.Price
		}
		if req.CurrencyCode != nil && strings.TrimSpace(*req.CurrencyCode) != "" {
			patch.CurrencyCode = req.CurrencyCode
		} else if req.Currency != nil && strings.TrimSpace(*req.Currency) != "" {
			patch.CurrencyCode = req.Currency
		}
		if req.DurationUnit != nil && strings.TrimSpace(*req.DurationUnit) != "" {
			unit := subscription.DurationUnit(*req.DurationUnit)
			patch.DurationUnit = &unit
		}
		if req.DurationValue != nil {
			patch.DurationValue = req.DurationValue
		}
		if req.DurationSeconds != nil {
			patch.DurationSeconds = req.DurationSeconds
		}
		if req.QuotaLimit != nil {
			patch.QuotaLimit = req.QuotaLimit
		}
		if req.QuotaResetPeriod != nil && strings.TrimSpace(*req.QuotaResetPeriod) != "" {
			period := subscription.ResetPeriod(*req.QuotaResetPeriod)
			patch.QuotaResetPeriod = &period
		}
		if req.QuotaResetIntervalSeconds != nil {
			patch.QuotaResetIntervalSeconds = req.QuotaResetIntervalSeconds
		}
		if req.MaxPurchasesPerUser != nil {
			patch.MaxPurchasesPerUser = req.MaxPurchasesPerUser
		}
		patch.SortOrder = req.SortOrder
		plan, err := d.Service.UpdatePlan(r.Context(), patch)
		if err != nil {
			writeSubscriptionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, planToResponse(plan))
	}
}

func newArchivePlanHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, tenantID, ok := resolveAdminTenantQuery(w, r, d)
		if !ok {
			return
		}
		if err := ident.CanIssueForTenant(tenantID); err != nil {
			writeAdminError(w, err)
			return
		}
		planID, ok := parsePathID(w, r)
		if !ok {
			return
		}
		if err := d.Service.ArchivePlan(r.Context(), tenantID, planID); err != nil {
			writeSubscriptionError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
