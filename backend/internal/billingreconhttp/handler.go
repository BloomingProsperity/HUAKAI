package billingreconhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
)

const maxRepriceBodyBytes = 1 << 16

type AdminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type Service interface {
	RepriceUsageRecords(context.Context, billing.RepriceRequest) (billing.RepriceResult, error)
}

type Deps struct {
	Auth    AdminAuth
	Service Service
}

type repriceRequestBody struct {
	UsageRecordID int64  `json:"usage_record_id"`
	TenantID      int64  `json:"tenant_id"`
	From          string `json:"from"`
	To            string `json:"to"`
	Limit         int    `json:"limit"`
	DryRun        *bool  `json:"dry_run"`
}

type repriceResponse struct {
	Object  string             `json:"object"`
	DryRun  bool               `json:"dry_run"`
	Items   []repriceItem      `json:"items"`
	Summary billingSummaryView `json:"summary"`
}

type billingSummaryView struct {
	Total           int `json:"total"`
	WouldApply      int `json:"would_apply"`
	Repriced        int `json:"repriced"`
	AlreadyRepriced int `json:"already_repriced"`
	Skipped         int `json:"skipped"`
	Failed          int `json:"failed"`
}

type repriceItem struct {
	UsageRecordID     int64  `json:"usage_record_id"`
	TenantID          int64  `json:"tenant_id"`
	Status            string `json:"status"`
	SkippedReason     string `json:"skipped_reason,omitempty"`
	ErrorCode         string `json:"error_code,omitempty"`
	ErrorMessage      string `json:"error_message,omitempty"`
	OriginalCost      string `json:"original_cost"`
	AuthoritativeCost string `json:"authoritative_cost"`
	CostDelta         string `json:"cost_delta"`
	PricingSource     string `json:"pricing_source,omitempty"`
}

func NewHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil || d.Service == nil {
			writeError(w, http.StatusServiceUnavailable, "gateway_not_configured", "billing reprice dependency unset")
			return
		}
		ident, err := d.Auth.Resolve(r.Context(), r)
		if err != nil {
			writeAdminAuthError(w, err)
			return
		}
		if ident.Role != admin.RolePlatformAdmin {
			writeError(w, http.StatusForbidden, "admin_forbidden", "platform_admin role required")
			return
		}
		req, ok := decodeRepriceRequest(w, r)
		if !ok {
			return
		}
		result, err := d.Service.RepriceUsageRecords(r.Context(), req)
		if err != nil {
			writeRepriceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toResponse(result))
	}
}

func decodeRepriceRequest(w http.ResponseWriter, r *http.Request) (billing.RepriceRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRepriceBodyBytes)
	var body repriceRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return billing.RepriceRequest{}, false
	}
	dryRun := true
	if body.DryRun != nil {
		dryRun = *body.DryRun
	}
	req := billing.RepriceRequest{
		UsageRecordID: body.UsageRecordID,
		TenantID:      body.TenantID,
		Limit:         body.Limit,
		DryRun:        dryRun,
	}
	if body.UsageRecordID > 0 {
		if body.TenantID != 0 || strings.TrimSpace(body.From) != "" || strings.TrimSpace(body.To) != "" {
			writeError(w, http.StatusBadRequest, "ambiguous_reprice_scope", "usage_record_id cannot be combined with tenant_id/from/to")
			return billing.RepriceRequest{}, false
		}
		return req, true
	}
	from, ok := parseRequiredTime(w, body.From, "from")
	if !ok {
		return billing.RepriceRequest{}, false
	}
	to, ok := parseRequiredTime(w, body.To, "to")
	if !ok {
		return billing.RepriceRequest{}, false
	}
	req.From = from
	req.To = to
	if body.TenantID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_tenant_id", "tenant_id must be a positive int64")
		return billing.RepriceRequest{}, false
	}
	return req, true
}

func parseRequiredTime(w http.ResponseWriter, raw, name string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		writeError(w, http.StatusBadRequest, "missing_"+name, name+" is required")
		return time.Time{}, false
	}
	ts, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_"+name, name+" must be RFC3339")
		return time.Time{}, false
	}
	return ts.UTC(), true
}

func toResponse(result billing.RepriceResult) repriceResponse {
	items := make([]repriceItem, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, repriceItem{
			UsageRecordID:     item.UsageRecordID,
			TenantID:          item.TenantID,
			Status:            item.Status,
			SkippedReason:     item.SkippedReason,
			ErrorCode:         item.ErrorCode,
			ErrorMessage:      item.ErrorMessage,
			OriginalCost:      item.OriginalCost.StringFixed(8),
			AuthoritativeCost: item.AuthoritativeCost.StringFixed(8),
			CostDelta:         item.CostDelta.StringFixed(8),
			PricingSource:     item.PricingSource,
		})
	}
	return repriceResponse{
		Object: "billing_reprice_report",
		DryRun: result.DryRun,
		Items:  items,
		Summary: billingSummaryView{
			Total:           result.Summary.Total,
			WouldApply:      result.Summary.WouldApply,
			Repriced:        result.Summary.Repriced,
			AlreadyRepriced: result.Summary.AlreadyRepriced,
			Skipped:         result.Summary.Skipped,
			Failed:          result.Summary.Failed,
		},
	}
}

func writeRepriceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, billing.ErrRepriceInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_reprice_request", err.Error())
	case errors.Is(err, billing.ErrPoolNotConfigured):
		writeError(w, http.StatusServiceUnavailable, "billing_reprice_backend_unavailable", "billing reprice backend unavailable")
	default:
		writeError(w, http.StatusServiceUnavailable, "billing_reprice_failed", err.Error())
	}
}

func writeAdminAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, admin.ErrAdminBackend):
		writeError(w, http.StatusServiceUnavailable, "admin_backend_error", "admin auth backend transient failure")
	default:
		writeError(w, http.StatusUnauthorized, "admin_unauthorized", "missing or invalid admin credential")
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]map[string]string{"error": {"code": code, "message": message}})
}
