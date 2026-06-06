package alertinghttp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/alerting"
)

const (
	defaultLimit = 50
	maxLimit     = 500
)

func resolveAdmin(deps AdminDeps, w http.ResponseWriter, r *http.Request) (admin.AdminIdentity, bool) {
	if deps.Auth == nil || deps.Service == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "alerting admin dependency unset")
		return admin.AdminIdentity{}, false
	}
	ident, err := deps.Auth.Resolve(r.Context(), r)
	if err != nil {
		writeAdminError(w, err)
		return admin.AdminIdentity{}, false
	}
	return ident, true
}

func tenantFromQuery(w http.ResponseWriter, r *http.Request, ident admin.AdminIdentity) (int64, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("tenant_id"))
	if raw == "" && ident.Role == admin.RoleTenantOperator {
		return tenantFromValue(w, ident, ident.ScopeTenantID)
	}
	if raw == "" {
		writeJSONError(w, http.StatusBadRequest, "tenant_id_required", "tenant_id query parameter must be positive")
		return 0, false
	}
	tenantID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || tenantID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid_tenant_id", "tenant_id must be a positive int64")
		return 0, false
	}
	return tenantFromValue(w, ident, tenantID)
}

func tenantFromValue(w http.ResponseWriter, ident admin.AdminIdentity, tenantID int64) (int64, bool) {
	if tenantID == 0 && ident.Role == admin.RoleTenantOperator {
		tenantID = ident.ScopeTenantID
	}
	if tenantID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "tenant_id_required", "tenant_id must be positive")
		return 0, false
	}
	if err := ident.CanIssueForTenant(tenantID); err != nil {
		writeAdminError(w, err)
		return 0, false
	}
	return tenantID, true
}

func parsePage(w http.ResponseWriter, r *http.Request) (int, int, bool) {
	limit := defaultLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || v < 1 || v > maxLimit {
			writeJSONError(w, http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 500")
			return 0, 0, false
		}
		limit = int(v)
	}
	offset := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || v < 0 {
			writeJSONError(w, http.StatusBadRequest, "invalid_offset", "offset must be non-negative")
			return 0, 0, false
		}
		offset = int(v)
	}
	return limit, offset, true
}

func parsePathID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(chi.URLParam(r, "id")), 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid_"+name, fmt.Sprintf("%s must be a positive int64", name))
		return 0, false
	}
	return id, true
}

func parseOptionalRuleID(w http.ResponseWriter, r *http.Request) (*int64, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("rule_id"))
	if raw == "" {
		return nil, true
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid_rule_id", "rule_id must be a positive int64")
		return nil, false
	}
	return &id, true
}

func decodeRequest(w http.ResponseWriter, r *http.Request, dst any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return false
	}
	return true
}

func writeAlertingError(w http.ResponseWriter, err error, code string) {
	switch {
	case errors.Is(err, alerting.ErrInvalidInput):
		writeJSONError(w, http.StatusBadRequest, code, "alerting request is invalid")
	case errors.Is(err, alerting.ErrRuleExists):
		writeJSONError(w, http.StatusConflict, code, "alert rule name already exists")
	case errors.Is(err, alerting.ErrNotFound):
		writeJSONError(w, http.StatusNotFound, code, "alerting resource not found")
	case errors.Is(err, alerting.ErrStoreNotConfigured):
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "alerting dependency unset")
	default:
		writeJSONError(w, http.StatusServiceUnavailable, code, "alerting backend unavailable")
	}
}

func writeAdminError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, admin.ErrAdminBackend):
		writeJSONError(w, http.StatusServiceUnavailable, "admin_backend_error", "admin auth backend transient failure")
	case errors.Is(err, admin.ErrAdminForbidden):
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "caller cannot act on this tenant scope")
	case errors.Is(err, admin.ErrAdminUnauthorized):
		writeJSONError(w, http.StatusUnauthorized, "admin_unauthorized", "missing or invalid admin credential")
	default:
		writeJSONError(w, http.StatusUnauthorized, "admin_unauthorized", "missing or invalid admin credential")
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func formatTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	formatted := formatTime(*t)
	return &formatted
}
