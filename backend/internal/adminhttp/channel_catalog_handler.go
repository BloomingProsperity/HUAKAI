package adminhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

type AdminChannelCatalogDeps struct {
	Auth    adminCatalogAuth
	Queries adminChannelCatalogQueries
	Store   adminChannelCatalogStore
}

type adminChannelCatalogQueries interface {
	ListAdminChannelsByTenant(context.Context, admindb.ListAdminChannelsByTenantParams) ([]admindb.ListAdminChannelsByTenantRow, error)
	GetAdminChannel(context.Context, admindb.GetAdminChannelParams) (admindb.GetAdminChannelRow, error)
}

type channelCatalogListResponse struct {
	Object string               `json:"object"`
	Items  []channelCatalogItem `json:"items"`
	Limit  int32                `json:"limit"`
	Offset int32                `json:"offset"`
}

type channelCatalogItem struct {
	ID                  int64           `json:"id"`
	PoolGroupID         int64           `json:"pool_group_id"`
	Name                string          `json:"name"`
	FailoverStatusCodes []int32         `json:"failover_status_codes"`
	BodyParamStrips     []string        `json:"body_param_strips"`
	ParamOverride       json.RawMessage `json:"param_override"`
	SensitiveWords      []string        `json:"sensitive_words"`
	Enabled             bool            `json:"enabled"`
	CreatedAt           string          `json:"created_at"`
}

func NewChannelCatalogListHandler(d AdminChannelCatalogDeps) http.HandlerFunc {
	return newChannelCatalogListHandler(d)
}

func NewChannelCatalogGetHandler(d AdminChannelCatalogDeps) http.HandlerFunc {
	return newChannelCatalogGetHandler(d)
}

func newChannelCatalogListHandler(d AdminChannelCatalogDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		queries := channelCatalogQueries(d)
		if d.Auth == nil || queries == nil {
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

		rows, err := queries.ListAdminChannelsByTenant(r.Context(), admindb.ListAdminChannelsByTenantParams{
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
			items = append(items, channelCatalogItemFromListRow(row))
		}
		writeAdminCatalogJSON(w, http.StatusOK, channelCatalogListResponse{
			Object: "admin_channels_list",
			Items:  items,
			Limit:  page.Limit,
			Offset: page.Offset,
		})
	}
}

func newChannelCatalogGetHandler(d AdminChannelCatalogDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		queries := channelCatalogQueries(d)
		if d.Auth == nil || queries == nil {
			writeError(w, http.StatusServiceUnavailable, "gateway_not_configured",
				"admin channel catalog dependency unset")
			return
		}
		ident, err := d.Auth.Resolve(r.Context(), r)
		if err != nil {
			writeAdminAuthError(w, err)
			return
		}
		tenantID, ok := parseAdminCatalogTenant(w, r, ident)
		if !ok {
			return
		}
		id, ok := parseChannelCatalogID(w, r)
		if !ok {
			return
		}
		row, err := queries.GetAdminChannel(r.Context(), admindb.GetAdminChannelParams{
			TenantID: tenantID,
			ID:       id,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(w, http.StatusNotFound, "channel_not_found", "channel not found")
				return
			}
			writeError(w, http.StatusServiceUnavailable, "channel_get_failed",
				fmt.Sprintf("admin channel get failed: %v", err))
			return
		}
		writeAdminCatalogJSON(w, http.StatusOK, channelCatalogItemFromGetRow(row))
	}
}

func channelCatalogQueries(d AdminChannelCatalogDeps) adminChannelCatalogQueries {
	if d.Queries != nil {
		return d.Queries
	}
	if d.Store != nil {
		return d.Store
	}
	return nil
}

func channelCatalogItemFromListRow(row admindb.ListAdminChannelsByTenantRow) channelCatalogItem {
	return channelCatalogItem{
		ID: row.ID, PoolGroupID: row.PoolGroupID, Name: row.Name,
		FailoverStatusCodes: copyChannelCatalogInt32s(row.FailoverStatusCodes),
		BodyParamStrips:     copyChannelCatalogStrings(row.BodyParamStrips),
		ParamOverride:       normalizedChannelCatalogObject(row.ParamOverride),
		SensitiveWords:      copyChannelCatalogStrings(row.SensitiveWords),
		Enabled:             row.Enabled, CreatedAt: formatCatalogTime(row.CreatedAt),
	}
}

func channelCatalogItemFromGetRow(row admindb.GetAdminChannelRow) channelCatalogItem {
	return channelCatalogItem{
		ID: row.ID, PoolGroupID: row.PoolGroupID, Name: row.Name,
		FailoverStatusCodes: copyChannelCatalogInt32s(row.FailoverStatusCodes),
		BodyParamStrips:     copyChannelCatalogStrings(row.BodyParamStrips),
		ParamOverride:       normalizedChannelCatalogObject(row.ParamOverride),
		SensitiveWords:      copyChannelCatalogStrings(row.SensitiveWords),
		Enabled:             row.Enabled, CreatedAt: formatCatalogTime(row.CreatedAt),
	}
}

func channelCatalogItemFromCreateRow(row admindb.CreateChannelRow) channelCatalogItem {
	return channelCatalogItem{
		ID: row.ID, PoolGroupID: row.PoolGroupID, Name: row.Name,
		FailoverStatusCodes: copyChannelCatalogInt32s(row.FailoverStatusCodes),
		BodyParamStrips:     copyChannelCatalogStrings(row.BodyParamStrips),
		ParamOverride:       normalizedChannelCatalogObject(row.ParamOverride),
		SensitiveWords:      copyChannelCatalogStrings(row.SensitiveWords), Enabled: row.Enabled,
		CreatedAt: formatCatalogTime(row.CreatedAt),
	}
}

func channelCatalogItemFromUpdateRow(row admindb.UpdateChannelRow) channelCatalogItem {
	return channelCatalogItem{
		ID: row.ID, PoolGroupID: row.PoolGroupID, Name: row.Name,
		FailoverStatusCodes: copyChannelCatalogInt32s(row.FailoverStatusCodes),
		BodyParamStrips:     copyChannelCatalogStrings(row.BodyParamStrips),
		ParamOverride:       normalizedChannelCatalogObject(row.ParamOverride),
		SensitiveWords:      copyChannelCatalogStrings(row.SensitiveWords), Enabled: row.Enabled,
		CreatedAt: formatCatalogTime(row.CreatedAt),
	}
}

func channelCatalogItemFromSoftDeleteRow(row admindb.SoftDeleteChannelRow) channelCatalogItem {
	return channelCatalogItem{
		ID: row.ID, PoolGroupID: row.PoolGroupID, Name: row.Name,
		FailoverStatusCodes: copyChannelCatalogInt32s(row.FailoverStatusCodes),
		BodyParamStrips:     []string{},
		ParamOverride:       json.RawMessage(`{}`),
		SensitiveWords:      []string{}, Enabled: row.Enabled,
		CreatedAt: formatCatalogTime(row.CreatedAt),
	}
}

func copyChannelCatalogStrings(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	return append([]string(nil), values...)
}

func copyChannelCatalogInt32s(values []int32) []int32 {
	if len(values) == 0 {
		return []int32{}
	}
	return append([]int32(nil), values...)
}

func normalizedChannelCatalogObject(raw []byte) json.RawMessage {
	if len(bytes.TrimSpace(raw)) == 0 {
		return json.RawMessage(`{}`)
	}
	return append(json.RawMessage(nil), raw...)
}
