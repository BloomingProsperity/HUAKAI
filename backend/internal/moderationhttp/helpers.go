package moderationhttp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
)

func resolveAdmin(deps ModerationAdminDeps, w http.ResponseWriter, r *http.Request) (admin.AdminIdentity, bool) {
	if deps.Auth == nil || deps.Store == nil {
		writeError(w, http.StatusServiceUnavailable, "gateway_not_configured",
			"moderation admin dependency unset")
		return admin.AdminIdentity{}, false
	}
	ident, err := deps.Auth.Resolve(r.Context(), r)
	if err != nil {
		writeAdminError(w, err)
		return admin.AdminIdentity{}, false
	}
	return ident, true
}

func requireTenant(w http.ResponseWriter, ident admin.AdminIdentity, tenantID int64) bool {
	if tenantID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_tenant_id",
			"tenant_id must be a positive int64")
		return false
	}
	if err := ident.CanActOnTenant(tenantID); err != nil {
		writeAdminError(w, err)
		return false
	}
	return true
}

func tenantFromQuery(w http.ResponseWriter, r *http.Request, ident admin.AdminIdentity) (int64, bool) {
	raw := r.URL.Query().Get("tenant_id")
	if raw == "" && ident.Role == admin.RoleTenantOperator {
		tenantID := ident.ScopeTenantID()
		if !requireTenant(w, ident, tenantID) {
			return 0, false
		}
		return tenantID, true
	}
	if raw == "" {
		writeError(w, http.StatusBadRequest, "tenant_id_required",
			"tenant_id query param required")
		return 0, false
	}
	tenantID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_tenant_id",
			"tenant_id must be a positive int64")
		return 0, false
	}
	if !requireTenant(w, ident, tenantID) {
		return 0, false
	}
	return tenantID, true
}

func readJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	return readJSONLimit(w, r, dst, 1<<16)
}

func readJSONLimit(w http.ResponseWriter, r *http.Request, dst any, limit int64) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		writeError(w, http.StatusBadRequest, "body_read_error", err.Error())
		return false
	}
	if err := json.Unmarshal(body, dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return false
	}
	return true
}

func parsePage(w http.ResponseWriter, r *http.Request) (int32, int32, bool) {
	limit := int32(50)
	offset := int32(0)
	if raw := r.URL.Query().Get("limit"); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || v < 1 || v > 500 {
			writeError(w, http.StatusBadRequest, "invalid_limit",
				"limit must be between 1 and 500")
			return 0, 0, false
		}
		limit = int32(v)
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || v < 0 {
			writeError(w, http.StatusBadRequest, "invalid_offset",
				"offset must be non-negative")
			return 0, 0, false
		}
		offset = int32(v)
	}
	return limit, offset, true
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeAdminError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, admin.ErrAdminUnauthorized):
		writeError(w, http.StatusUnauthorized, "admin_unauthorized", "")
	case errors.Is(err, admin.ErrAdminForbidden):
		writeError(w, http.StatusForbidden, "admin_forbidden", "admin role cannot access tenant")
	case errors.Is(err, admin.ErrAdminBackend):
		writeError(w, http.StatusServiceUnavailable, "admin_backend_error", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "admin_unknown_error", err.Error())
	}
}

func writeError(w http.ResponseWriter, status int, code string, message string) {
	writeJSON(w, status, map[string]map[string]string{
		"error": {"code": code, "message": message},
	})
}

func positivePathID(w http.ResponseWriter, raw string, name string) (int64, bool) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_"+name,
			fmt.Sprintf("%s must be a positive int64", name))
		return 0, false
	}
	return id, true
}

func adminActorID(ident admin.AdminIdentity) string {
	// 统一审计 actor 归属:token 源返 admin_token:<id>,session 源返 admin_user:<id>
	return ident.AuditActor()
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
