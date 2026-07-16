package riskoverviewhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
)

// AdminAuth 从请求派生平台/租户运营者身份。鉴权失败一律拒(防跨租户 IDOR)。
type AdminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type AdminDeps struct {
	Auth  AdminAuth
	Store Store
}

// MountAdminRoutes 挂只读风控总览端点。仅 GET,零写入。
func MountAdminRoutes(r chi.Router, deps AdminDeps) {
	r.Get("/admin/v1/risk/overview", newOverviewHandler(deps))
}

type overviewResponse struct {
	Object            string `json:"object"`
	TenantID          int64  `json:"tenant_id"`
	DisabledKeys      int64  `json:"disabled_keys"`
	FiringAlerts      int64  `json:"firing_alerts"`
	DisabledUsers     int64  `json:"disabled_users"`
	IPBlacklistedKeys int64  `json:"ip_blacklisted_keys"`
}

func newOverviewHandler(deps AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(deps, w, r)
		if !ok {
			return
		}
		tenantID, ok := tenantFromQuery(w, r, ident)
		if !ok {
			return
		}
		ov, err := deps.Store.Overview(r.Context(), tenantID)
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "risk_overview_failed", "risk overview backend unavailable")
			return
		}
		writeJSON(w, http.StatusOK, overviewResponse{
			Object:            "risk_overview",
			TenantID:          tenantID,
			DisabledKeys:      ov.DisabledKeys,
			FiringAlerts:      ov.FiringAlerts,
			DisabledUsers:     ov.DisabledUsers,
			IPBlacklistedKeys: ov.IPBlacklistedKeys,
		})
	}
}

// ── admin 鉴权 + 租户隔离(范式同 alertinghttp,防跨租户 IDOR)──

func resolveAdmin(deps AdminDeps, w http.ResponseWriter, r *http.Request) (admin.AdminIdentity, bool) {
	if deps.Auth == nil || deps.Store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "risk overview dependency unset")
		return admin.AdminIdentity{}, false
	}
	ident, err := deps.Auth.Resolve(r.Context(), r)
	if err != nil {
		writeAdminError(w, err)
		return admin.AdminIdentity{}, false
	}
	return ident, true
}

// tenantFromQuery 取 tenant_id 查询参;租户运营者缺省回退自身 scope;平台 admin 必须显式传。
// 最终经 CanIssueForTenant 校验调用者能否操作该租户——身份只信认证上下文,绝不信请求体。
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
		"error": map[string]string{"code": code, "message": message},
	})
}
