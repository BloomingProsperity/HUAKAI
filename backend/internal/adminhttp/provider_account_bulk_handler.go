package adminhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

// ProviderAccountBulkDeps holds the dependencies for the bulk-by-tag handler.
type ProviderAccountBulkDeps struct {
	Auth  providerAccountBulkAuth
	Store providerAccountBulkStore
}

type providerAccountBulkAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type providerAccountBulkStore interface {
	ListAdminProviderAccounts(context.Context, admindb.ListAdminProviderAccountsParams) ([]admindb.AdminProviderAccountRow, error)
	UpdateAdminProviderAccount(context.Context, admindb.UpdateAdminProviderAccountParams) (admindb.AdminProviderAccountRow, error)
	InsertAdminAuditEvent(context.Context, admindb.InsertAdminAuditEventParams) (admindb.InsertAdminAuditEventRow, error)
}

type providerAccountBulkRequest struct {
	Tag          string `json:"tag"`
	Enabled      *bool  `json:"enabled,omitempty"`
	Priority     *int32 `json:"priority,omitempty"`
	StaticWeight *int32 `json:"static_weight,omitempty"`
}

type providerAccountBulkResponse struct {
	AffectedIDs []int64 `json:"affected_ids"`
	Count       int     `json:"count"`
}

// MountProviderAccountBulkRoutes registers the POST /bulk-by-tag route.
func MountProviderAccountBulkRoutes(r chi.Router, d ProviderAccountBulkDeps) {
	r.Post("/bulk-by-tag", newProviderAccountBulkHandler(d))
}

func newProviderAccountBulkHandler(d ProviderAccountBulkDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, tenantID, ok := resolveProviderAccountBulkAdmin(w, r, d)
		if !ok {
			return
		}

		var req providerAccountBulkRequest
		if !decodeProviderAccountBulkJSON(w, r, &req) {
			return
		}

		tag := strings.TrimSpace(req.Tag)
		if tag == "" {
			writeError(w, http.StatusBadRequest, "tag_required", "tag is required and must be non-empty")
			return
		}
		if req.Enabled == nil && req.Priority == nil && req.StaticWeight == nil {
			writeError(w, http.StatusBadRequest, "no_field_to_set", "at least one of enabled, priority, static_weight must be provided")
			return
		}

		const bulkListLimit = 1000
		rows, err := d.Store.ListAdminProviderAccounts(r.Context(), admindb.ListAdminProviderAccountsParams{
			TenantID:   tenantID,
			TagFilter:  tag,
			LimitCount: bulkListLimit,
		})
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "provider_account_list_failed", err.Error())
			return
		}

		affectedIDs := make([]int64, 0, len(rows))
		actorIDStr := fmt.Sprintf("%d", ident.TokenID)

		for _, row := range rows {
			_, err := d.Store.UpdateAdminProviderAccount(r.Context(), admindb.UpdateAdminProviderAccountParams{
				ID:           row.ID,
				TenantID:     tenantID,
				ActorID:      &actorIDStr,
				Enabled:      req.Enabled,
				Priority:     req.Priority,
				StaticWeight: req.StaticWeight,
			})
			if err != nil {
				writeError(w, http.StatusServiceUnavailable, "provider_account_update_failed",
					fmt.Sprintf("update failed after %d succeeded: %s", len(affectedIDs), err.Error()))
				return
			}

			auditPayload, err := json.Marshal(map[string]any{
				"tenant_id":     tenantID,
				"id":            row.ID,
				"tag":           tag,
				"enabled":       req.Enabled,
				"priority":      req.Priority,
				"static_weight": req.StaticWeight,
			})
			if err != nil {
				writeError(w, http.StatusServiceUnavailable, "audit_payload_failed", err.Error())
				return
			}

			reqID := middleware.GetReqID(r.Context())
			var reqIDArg *string
			if reqID != "" {
				reqIDArg = &reqID
			}

			actorRole := ident.Role
			if actorRole == "" {
				actorRole = admin.RoleTenantOperator
			}

			rowID := row.ID
			_, err = d.Store.InsertAdminAuditEvent(r.Context(), admindb.InsertAdminAuditEventParams{
				TenantID:   &tenantID,
				ActorID:    actorIDStr,
				ActorRole:  actorRole,
				Action:     "provider_account.bulk_update_by_tag",
				TargetType: "provider_account",
				TargetID:   &rowID,
				RequestID:  reqIDArg,
				Payload:    auditPayload,
			})
			if err != nil {
				writeError(w, http.StatusServiceUnavailable, "audit_insert_failed", err.Error())
				return
			}

			affectedIDs = append(affectedIDs, row.ID)
		}

		writeAdminCatalogJSON(w, http.StatusOK, providerAccountBulkResponse{
			AffectedIDs: affectedIDs,
			Count:       len(affectedIDs),
		})
	}
}

func resolveProviderAccountBulkAdmin(w http.ResponseWriter, r *http.Request, d ProviderAccountBulkDeps) (admin.AdminIdentity, int64, bool) {
	if d.Auth == nil || d.Store == nil {
		writeError(w, http.StatusServiceUnavailable, "gateway_not_configured", "provider account bulk dependency unset")
		return admin.AdminIdentity{}, 0, false
	}
	ident, err := d.Auth.Resolve(r.Context(), r)
	if err != nil {
		writeAdminAuthError(w, err)
		return admin.AdminIdentity{}, 0, false
	}
	switch ident.Role {
	case admin.RoleTenantOperator, admin.RolePlatformAdmin:
	default:
		writeError(w, http.StatusForbidden, "admin_forbidden", "admin role required")
		return admin.AdminIdentity{}, 0, false
	}
	tenantID, ok := parseAdminCatalogTenant(w, r, ident)
	if !ok {
		return admin.AdminIdentity{}, 0, false
	}
	return ident, tenantID, true
}

func decodeProviderAccountBulkJSON(w http.ResponseWriter, r *http.Request, dst *providerAccountBulkRequest) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "body_read_error", err.Error())
		return false
	}
	if strings.TrimSpace(string(body)) == "" {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body is required")
		return false
	}
	if err := json.Unmarshal(body, dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return false
	}
	return true
}
