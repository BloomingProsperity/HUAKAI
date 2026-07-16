package pricingcataloghttp

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
)

type catalogPage struct {
	TenantID int64
	Limit    int32
	Offset   int32
}

// SYNC:从 internal/adminhttp/provider_catalog_handler.go 与
// internal/adminhttp/api_keys_handler.go 拷贝而来,因为这块切片独立在
// internal/pricingcataloghttp 内,而不是新增 adminhttp 文件。
func parseCatalogPage(w http.ResponseWriter, r *http.Request, ident admin.AdminIdentity) (catalogPage, bool) {
	tenantID, ok := parseCatalogTenant(w, r, ident)
	if !ok {
		return catalogPage{}, false
	}
	limit := int32(50)
	if s := r.URL.Query().Get("limit"); s != "" {
		v, err := strconv.ParseInt(s, 10, 32)
		if err != nil || v < 1 || v > 500 {
			writeError(w, http.StatusBadRequest, "invalid_limit", "limit must be a positive integer between 1 and 500")
			return catalogPage{}, false
		}
		limit = int32(v)
	}
	offset := int32(0)
	if s := r.URL.Query().Get("offset"); s != "" {
		v, err := strconv.ParseInt(s, 10, 32)
		if err != nil || v < 0 {
			writeError(w, http.StatusBadRequest, "invalid_offset", "offset must be a non-negative integer")
			return catalogPage{}, false
		}
		offset = int32(v)
	}
	return catalogPage{TenantID: tenantID, Limit: limit, Offset: offset}, true
}

func parseCatalogTenant(w http.ResponseWriter, r *http.Request, ident admin.AdminIdentity) (int64, bool) {
	tenantParam := r.URL.Query().Get("tenant_id")
	var tenantID int64
	if tenantParam == "" {
		if ident.Role != admin.RoleTenantOperator {
			writeError(w, http.StatusBadRequest, "tenant_id_required", "tenant_id query param required")
			return 0, false
		}
		tenantID = ident.ScopeTenantID()
	} else {
		v, err := strconv.ParseInt(tenantParam, 10, 64)
		if err != nil || v <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_tenant_id", "tenant_id must be a positive int64")
			return 0, false
		}
		tenantID = v
	}
	if tenantID <= 0 {
		writeAdminError(w, admin.ErrAdminForbidden)
		return 0, false
	}
	if err := ident.CanActOnTenant(tenantID); err != nil {
		writeAdminError(w, err)
		return 0, false
	}
	return tenantID, true
}

func writeAdminAuthError(w http.ResponseWriter, err error) {
	if errors.Is(err, admin.ErrAdminBackend) {
		writeError(w, http.StatusServiceUnavailable, "admin_backend_error", "admin auth backend transient failure")
		return
	}
	writeError(w, http.StatusUnauthorized, "admin_unauthorized", "missing or invalid admin credential")
}

func writeAdminError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, admin.ErrAdminUnauthorized):
		writeError(w, http.StatusUnauthorized, "admin_unauthorized", "")
	case errors.Is(err, admin.ErrAdminForbidden):
		writeError(w, http.StatusForbidden, "admin_forbidden", "caller cannot act on this tenant scope")
	case errors.Is(err, admin.ErrAdminBackend):
		writeError(w, http.StatusServiceUnavailable, "admin_backend_error", "admin backend transient failure")
	default:
		writeError(w, http.StatusInternalServerError, "admin_unknown_error", err.Error())
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]map[string]string{
		"error": {"code": code, "message": message},
	})
}
