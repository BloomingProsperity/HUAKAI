package adminhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

type AdminProviderCatalogDeps struct {
	Auth    adminCatalogAuth
	Queries adminProviderCatalogQueries
}

type adminCatalogAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type adminProviderCatalogQueries interface {
	ListAdminProvidersByTenant(context.Context, admindb.ListAdminProvidersByTenantParams) ([]admindb.ListAdminProvidersByTenantRow, error)
}

type providerCatalogListResponse struct {
	Object string                `json:"object"`
	Items  []providerCatalogItem `json:"items"`
	Limit  int32                 `json:"limit"`
	Offset int32                 `json:"offset"`
}

type providerCatalogItem struct {
	ID               int64  `json:"id"`
	Code             string `json:"code"`
	DisplayName      string `json:"display_name"`
	UpstreamProtocol string `json:"upstream_protocol"`
	Enabled          bool   `json:"enabled"`
	CreatedAt        string `json:"created_at"`
}

type adminCatalogPage struct {
	TenantID int64
	Limit    int32
	Offset   int32
}

func MountProviderCatalogRoutes(r chi.Router, d AdminProviderCatalogDeps) {
	r.Get("/", NewProviderCatalogListHandler(d))
}

func NewProviderCatalogListHandler(d AdminProviderCatalogDeps) http.HandlerFunc {
	return newProviderCatalogListHandler(d)
}

func newProviderCatalogListHandler(d AdminProviderCatalogDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil || d.Queries == nil {
			writeError(w, http.StatusServiceUnavailable, "gateway_not_configured",
				"admin provider catalog dependency unset")
			return
		}
		ident, err := d.Auth.Resolve(r.Context(), r)
		if err != nil {
			writeAdminAuthError(w, err)
			return
		}
		page, ok := parseAdminCatalogPage(w, r, ident)
		if !ok {
			return
		}

		rows, err := d.Queries.ListAdminProvidersByTenant(r.Context(), admindb.ListAdminProvidersByTenantParams{
			TenantID:   page.TenantID,
			PageLimit:  page.Limit,
			PageOffset: page.Offset,
		})
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "registry_backend_error",
				fmt.Sprintf("admin providers list failed: %v", err))
			return
		}

		items := make([]providerCatalogItem, 0, len(rows))
		for _, row := range rows {
			items = append(items, providerCatalogItem{
				ID:               row.ID,
				Code:             row.Code,
				DisplayName:      row.DisplayName,
				UpstreamProtocol: row.UpstreamProtocol,
				Enabled:          row.Enabled,
				CreatedAt:        formatCatalogTime(row.CreatedAt),
			})
		}
		writeAdminCatalogJSON(w, http.StatusOK, providerCatalogListResponse{
			Object: "admin_providers_list",
			Items:  items,
			Limit:  page.Limit,
			Offset: page.Offset,
		})
	}
}

func parseAdminCatalogPage(w http.ResponseWriter, r *http.Request, ident admin.AdminIdentity) (adminCatalogPage, bool) {
	tenantID, ok := parseAdminCatalogTenant(w, r, ident)
	if !ok {
		return adminCatalogPage{}, false
	}
	limit := int32(50)
	if s := r.URL.Query().Get("limit"); s != "" {
		v, err := strconv.ParseInt(s, 10, 32)
		if err != nil || v < 1 || v > 500 {
			writeError(w, http.StatusBadRequest, "invalid_limit",
				"limit must be a positive integer between 1 and 500")
			return adminCatalogPage{}, false
		}
		limit = int32(v)
	}
	offset := int32(0)
	if s := r.URL.Query().Get("offset"); s != "" {
		v, err := strconv.ParseInt(s, 10, 32)
		if err != nil || v < 0 {
			writeError(w, http.StatusBadRequest, "invalid_offset",
				"offset must be a non-negative integer")
			return adminCatalogPage{}, false
		}
		offset = int32(v)
	}
	return adminCatalogPage{TenantID: tenantID, Limit: limit, Offset: offset}, true
}

func parseAdminCatalogTenant(w http.ResponseWriter, r *http.Request, ident admin.AdminIdentity) (int64, bool) {
	tenantParam := r.URL.Query().Get("tenant_id")
	var tenantID int64
	if tenantParam == "" {
		if ident.Role != admin.RoleTenantOperator {
			writeError(w, http.StatusBadRequest, "tenant_id_required",
				"tenant_id query param required")
			return 0, false
		}
		tenantID = ident.ScopeTenantID
	} else {
		v, err := strconv.ParseInt(tenantParam, 10, 64)
		if err != nil || v <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_tenant_id",
				"tenant_id must be a positive int64")
			return 0, false
		}
		tenantID = v
	}
	if tenantID <= 0 {
		writeAdminError(w, admin.ErrAdminForbidden)
		return 0, false
	}
	if err := ident.CanIssueForTenant(tenantID); err != nil {
		writeAdminError(w, err)
		return 0, false
	}
	return tenantID, true
}

func formatCatalogTime(ts pgtype.Timestamptz) string {
	if !ts.Valid {
		return ""
	}
	return ts.Time.UTC().Format(time.RFC3339)
}

func writeAdminCatalogJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
