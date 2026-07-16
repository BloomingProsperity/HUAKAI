package adminhttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

const providerAccountBulkMaxMatched = 1000

var errProviderAccountBulkTooLarge = errors.New("provider account bulk scope too large")

// ProviderAccountBulkDeps 持有 bulk-by-tag handler 所需的依赖。
type ProviderAccountBulkDeps struct {
	Auth  providerAccountBulkAuth
	Store providerAccountBulkStore
}

type providerAccountBulkAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type providerAccountBulkStore interface {
	ListAdminProviderAccounts(context.Context, admindb.ListAdminProviderAccountsParams) ([]admindb.AdminProviderAccountRow, error)
	UpdateAdminProviderAccountWithAudit(context.Context, admindb.UpdateAdminProviderAccountWithAuditParams) (admindb.AdminProviderAccountRow, error)
}

type providerAccountBulkRequest struct {
	Tag          string `json:"tag"`
	Enabled      *bool  `json:"enabled,omitempty"`
	Priority     *int32 `json:"priority,omitempty"`
	StaticWeight *int32 `json:"static_weight,omitempty"`
}

type providerAccountBulkResponse struct {
	AffectedIDs  []int64                         `json:"affected_ids"`
	Failed       []providerAccountBulkFailedItem `json:"failed"`
	Count        int                             `json:"count"`
	FailedCount  int                             `json:"failed_count"`
	MatchedCount int                             `json:"matched_count"`
	Complete     bool                            `json:"complete"`
}

type providerAccountBulkFailedItem struct {
	ID      int64  `json:"id"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// MountProviderAccountBulkRoutes 注册 POST /bulk-by-tag 路由。
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

		rows, err := listAllProviderAccountsByTag(r.Context(), d.Store, tenantID, tag)
		if err != nil {
			if errors.Is(err, errProviderAccountBulkTooLarge) {
				writeError(w, http.StatusUnprocessableEntity, "bulk_scope_too_large", "matched accounts exceed the per-request limit")
				return
			}
			writeError(w, http.StatusServiceUnavailable, "provider_account_list_failed", "provider account list is temporarily unavailable")
			return
		}

		affectedIDs := make([]int64, 0, len(rows))
		failed := make([]providerAccountBulkFailedItem, 0)
		actorIDStr := ident.AuditActor()
		reqID := middleware.GetReqID(r.Context())
		var reqIDArg *string
		if reqID != "" {
			reqIDArg = &reqID
		}
		actorRole := ident.Role
		if actorRole == "" {
			actorRole = admin.RoleTenantOperator
		}

		for _, row := range rows {
			auditPayload, err := json.Marshal(map[string]any{
				"source":        "bulk_by_tag",
				"tenant_id":     tenantID,
				"id":            row.ID,
				"tag":           tag,
				"enabled":       req.Enabled,
				"priority":      req.Priority,
				"static_weight": req.StaticWeight,
			})
			if err != nil {
				failed = append(failed, providerAccountBulkFailedItem{
					ID: row.ID, Code: "audit_payload_failed", Message: err.Error(),
				})
				continue
			}
			rowID := row.ID
			_, err = d.Store.UpdateAdminProviderAccountWithAudit(r.Context(), admindb.UpdateAdminProviderAccountWithAuditParams{
				Update: admindb.UpdateAdminProviderAccountParams{
					ID:           row.ID,
					TenantID:     tenantID,
					ActorID:      &actorIDStr,
					Enabled:      req.Enabled,
					Priority:     req.Priority,
					StaticWeight: req.StaticWeight,
				},
				Audit: admindb.InsertAdminAuditEventParams{
					TenantID:   &tenantID,
					ActorID:    actorIDStr,
					ActorRole:  actorRole,
					Action:     "update_provider_account",
					TargetType: "provider_account",
					TargetID:   &rowID,
					RequestID:  reqIDArg,
					Payload:    auditPayload,
				},
			})
			if err != nil {
				failed = append(failed, providerAccountBulkFailedItem{
					ID: row.ID, Code: bulkFailureCode(err), Message: bulkFailureMessage(err),
				})
				continue
			}

			affectedIDs = append(affectedIDs, row.ID)
		}

		writeAdminCatalogJSON(w, http.StatusOK, providerAccountBulkResponse{
			AffectedIDs:  affectedIDs,
			Failed:       failed,
			Count:        len(affectedIDs),
			FailedCount:  len(failed),
			MatchedCount: len(rows),
			Complete:     len(failed) == 0,
		})
	}
}

func listAllProviderAccountsByTag(ctx context.Context, store providerAccountBulkStore, tenantID int64, tag string) ([]admindb.AdminProviderAccountRow, error) {
	const pageSize = int32(200)
	afterID := int64(0)
	rows := make([]admindb.AdminProviderAccountRow, 0)
	for {
		page, err := store.ListAdminProviderAccounts(ctx, admindb.ListAdminProviderAccountsParams{
			TenantID: tenantID, AfterID: afterID, TagFilter: tag, LimitCount: pageSize,
		})
		if err != nil {
			return nil, err
		}
		if len(rows)+len(page) > providerAccountBulkMaxMatched {
			return nil, errProviderAccountBulkTooLarge
		}
		rows = append(rows, page...)
		if len(page) < int(pageSize) {
			return rows, nil
		}
		nextAfterID := page[len(page)-1].ID
		if nextAfterID <= afterID {
			return nil, errors.New("provider account bulk pagination did not advance")
		}
		afterID = nextAfterID
	}
}

func bulkFailureCode(err error) string {
	if errors.Is(err, admindb.ErrProviderAccountBulkTransactionUnavailable) {
		return "transaction_unavailable"
	}
	return "update_and_audit_failed"
}

func bulkFailureMessage(err error) string {
	if errors.Is(err, admindb.ErrProviderAccountBulkTransactionUnavailable) {
		return "atomic update is temporarily unavailable"
	}
	return "account update and audit failed"
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
