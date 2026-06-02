package adminhttp

import (
	"context"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

type AdminChannelCatalogDeps struct {
	Auth    adminCatalogAuth
	Queries adminChannelCatalogQueries
}

type adminChannelCatalogQueries interface {
	ListAdminChannelsByTenant(context.Context, admindb.ListAdminChannelsByTenantParams) ([]admindb.ListAdminChannelsByTenantRow, error)
}

type channelCatalogListResponse struct {
	Object string               `json:"object"`
	Items  []channelCatalogItem `json:"items"`
	Limit  int32                `json:"limit"`
	Offset int32                `json:"offset"`
}

type channelCatalogItem struct {
	ID                  int64   `json:"id"`
	PoolGroupID         int64   `json:"pool_group_id"`
	Name                string  `json:"name"`
	FailoverStatusCodes []int32 `json:"failover_status_codes"`
	Enabled             bool    `json:"enabled"`
	CreatedAt           string  `json:"created_at"`
}

func MountChannelCatalogRoutes(r chi.Router, d AdminChannelCatalogDeps) {
	r.Get("/", NewChannelCatalogListHandler(d))
}

func NewChannelCatalogListHandler(d AdminChannelCatalogDeps) http.HandlerFunc {
	return newChannelCatalogListHandler(d)
}

func newChannelCatalogListHandler(d AdminChannelCatalogDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil || d.Queries == nil {
			writeError(w, http.StatusServiceUnavailable, "gateway_not_configured",
				"admin channel catalog dependency unset")
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

		rows, err := d.Queries.ListAdminChannelsByTenant(r.Context(), admindb.ListAdminChannelsByTenantParams{
			TenantID:   page.TenantID,
			PageLimit:  page.Limit,
			PageOffset: page.Offset,
		})
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "registry_backend_error",
				fmt.Sprintf("admin channels list failed: %v", err))
			return
		}

		items := make([]channelCatalogItem, 0, len(rows))
		for _, row := range rows {
			items = append(items, channelCatalogItem{
				ID:                  row.ID,
				PoolGroupID:         row.PoolGroupID,
				Name:                row.Name,
				FailoverStatusCodes: row.FailoverStatusCodes,
				Enabled:             row.Enabled,
				CreatedAt:           formatCatalogTime(row.CreatedAt),
			})
		}
		writeAdminCatalogJSON(w, http.StatusOK, channelCatalogListResponse{
			Object: "admin_channels_list",
			Items:  items,
			Limit:  page.Limit,
			Offset: page.Offset,
		})
	}
}
