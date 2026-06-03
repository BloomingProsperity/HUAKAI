package userkeycontrolshttp

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/quota"
	"github.com/BloomingProsperity/HUAKAI/internal/userkeycontrols"
)

type setQuotaRequest struct {
	LimitUSD      string `json:"limit_usd"`
	WindowKind    string `json:"window_kind,omitempty"`
	WindowSeconds int32  `json:"window_seconds,omitempty"`
	Mode          string `json:"mode,omitempty"`
}

type setGroupRequest struct {
	GroupID *int64 `json:"group_id"`
}

func newSetQuotaHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveSession(w, r, d.Service != nil)
		if !ok {
			return
		}
		apiKeyID, ok := parsePathID(w, r)
		if !ok {
			return
		}
		var req setQuotaRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		limit, ok := parseLimitUSD(w, req.LimitUSD)
		if !ok {
			return
		}
		out, err := d.Service.SetKeyQuota(r.Context(), userkeycontrols.SetKeyQuotaRequest{
			TenantID:      ident.TenantID,
			UserID:        ident.UserID,
			APIKeyID:      apiKeyID,
			LimitUSD:      limit,
			WindowKind:    quota.WindowKind(strings.TrimSpace(req.WindowKind)),
			WindowSeconds: req.WindowSeconds,
			Mode:          quota.Mode(strings.TrimSpace(req.Mode)),
			RequestID:     requestIDFromReq(r),
		})
		if err != nil {
			writeControlsError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func newGetQuotaHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveSession(w, r, d.Service != nil)
		if !ok {
			return
		}
		apiKeyID, ok := parsePathID(w, r)
		if !ok {
			return
		}
		out, err := d.Service.GetKeyQuota(r.Context(), ident.TenantID, ident.UserID, apiKeyID)
		if err != nil {
			writeControlsError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func newSetGroupHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveSession(w, r, d.Service != nil)
		if !ok {
			return
		}
		apiKeyID, ok := parsePathID(w, r)
		if !ok {
			return
		}
		var req setGroupRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.GroupID != nil && *req.GroupID <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_group_id", "group_id must be a positive int64 or null")
			return
		}
		out, err := d.Service.SetKeyGroup(r.Context(), userkeycontrols.SetKeyGroupRequest{
			TenantID:  ident.TenantID,
			UserID:    ident.UserID,
			APIKeyID:  apiKeyID,
			GroupID:   req.GroupID,
			RequestID: requestIDFromReq(r),
		})
		if err != nil {
			writeControlsError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func newGetGroupHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveSession(w, r, d.Service != nil)
		if !ok {
			return
		}
		apiKeyID, ok := parsePathID(w, r)
		if !ok {
			return
		}
		out, err := d.Service.GetKeyGroup(r.Context(), ident.TenantID, ident.UserID, apiKeyID)
		if err != nil {
			writeControlsError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func parsePathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_api_key_id", "api_key_id must be a positive int64")
		return 0, false
	}
	return id, true
}

func parseLimitUSD(w http.ResponseWriter, raw string) (decimal.Decimal, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		writeError(w, http.StatusBadRequest, "invalid_limit_usd", "limit_usd must be a non-negative decimal string")
		return decimal.Zero, false
	}
	limit, err := decimal.NewFromString(raw)
	if err != nil || limit.IsNegative() {
		writeError(w, http.StatusBadRequest, "invalid_limit_usd", "limit_usd must be a non-negative decimal string")
		return decimal.Zero, false
	}
	return limit, true
}
