package proxyadminhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/proxyadmin"
)

type tenantDefaultProxyService interface {
	Get(context.Context, int64) (proxyadmin.TenantDefaultProxy, error)
	Set(context.Context, int64, *int64, proxyadmin.TenantDefaultAudit) (proxyadmin.TenantDefaultProxy, error)
}

// MountTenantRoutes 注册租户默认出口的独立管理面。调用方把它挂在
// /admin/v1/tenants 下，path 中的租户 ID 是唯一目标租户来源。
func MountTenantRoutes(r chi.Router, d Deps) {
	r.Get("/{id}/default-proxy", newTenantDefaultProxyGetHandler(d))
	r.Put("/{id}/default-proxy", newTenantDefaultProxyPutHandler(d))
}

type tenantDefaultProxyRequest struct {
	ProxyID json.RawMessage `json:"proxy_id"`
}

type tenantDefaultProxyResponse struct {
	ProxyID *int64 `json:"proxy_id"`
}

func newTenantDefaultProxyGetHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID, _, ok := resolveTenantDefaultIdentity(w, r, d)
		if !ok {
			return
		}
		value, err := d.TenantDefaults.Get(r.Context(), tenantID)
		if err != nil {
			writeTenantDefaultError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, tenantDefaultProxyResponse{ProxyID: value.ProxyID})
	}
}

func newTenantDefaultProxyPutHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID, ident, ok := resolveTenantDefaultIdentity(w, r, d)
		if !ok {
			return
		}
		var req tenantDefaultProxyRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		proxyID, ok := parseDefaultProxyID(w, req.ProxyID)
		if !ok {
			return
		}
		value, err := d.TenantDefaults.Set(r.Context(), tenantID, proxyID, proxyadmin.TenantDefaultAudit{
			ActorID:   ident.AuditActor(),
			ActorRole: ident.Role,
			RequestID: strings.TrimSpace(r.Header.Get("X-Request-ID")),
		})
		if err != nil {
			writeTenantDefaultError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, tenantDefaultProxyResponse{ProxyID: value.ProxyID})
	}
}

func resolveTenantDefaultIdentity(w http.ResponseWriter, r *http.Request, d Deps) (int64, admin.AdminIdentity, bool) {
	if d.Auth == nil || d.TenantDefaults == nil {
		writeError(w, http.StatusServiceUnavailable, "admin_tenant_default_proxy_not_configured",
			"tenant default proxy dependency unset")
		return 0, admin.AdminIdentity{}, false
	}
	ident, err := d.Auth.Resolve(r.Context(), r)
	if err != nil {
		writeAdminAuthError(w, err)
		return 0, admin.AdminIdentity{}, false
	}
	if ident.Role != admin.RoleTenantOperator && ident.Role != admin.RolePlatformAdmin {
		writeError(w, http.StatusForbidden, "admin_forbidden_scope", "admin role required")
		return 0, admin.AdminIdentity{}, false
	}
	tenantID, ok := tenantPathID(w, r)
	if !ok {
		return 0, admin.AdminIdentity{}, false
	}
	if err := ident.CanIssueForTenant(tenantID); err != nil {
		writeAdminAuthError(w, err)
		return 0, admin.AdminIdentity{}, false
	}
	return tenantID, ident, true
}

func tenantPathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := strings.TrimSpace(chi.URLParam(r, "id"))
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_tenant_id", "tenant id must be a positive int64")
		return 0, false
	}
	return id, true
}

func parseDefaultProxyID(w http.ResponseWriter, raw json.RawMessage) (*int64, bool) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		writeError(w, http.StatusBadRequest, "proxy_id_required", "proxy_id is required and may be null")
		return nil, false
	}
	if trimmed == "null" {
		return nil, true
	}
	var proxyID int64
	if err := json.Unmarshal(raw, &proxyID); err != nil || proxyID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_proxy_id", "proxy_id must be a positive int64 or null")
		return nil, false
	}
	return &proxyID, true
}

func writeTenantDefaultError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, proxyadmin.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_tenant_default_proxy", "tenant or proxy id is invalid")
	case errors.Is(err, proxyadmin.ErrNotFound):
		writeError(w, http.StatusNotFound, "admin_proxy_not_found", "proxy not found in tenant")
	case errors.Is(err, proxyadmin.ErrTenantNotFound):
		writeError(w, http.StatusNotFound, "admin_tenant_not_found", "tenant not found")
	default:
		writeError(w, http.StatusServiceUnavailable, "admin_tenant_default_proxy_backend_error",
			"tenant default proxy backend unavailable")
	}
}
