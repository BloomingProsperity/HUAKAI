package adminquotahttp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	dbquota "github.com/BloomingProsperity/HUAKAI/internal/db/quotaadmin"
)

// Enum allowlists mirror the quota_policies CHECK constraints. Validating here
// turns bad enum input into a 400 instead of letting Postgres raise a CHECK
// (which would surface as a 503/500). The full HUAKAI superset is exposed.
var (
	validScopeKinds = map[string]struct{}{
		"global": {}, "user": {}, "api_key": {}, "channel": {},
		"pool_group": {}, "provider_account": {},
	}
	validMetrics = map[string]struct{}{
		"requests": {}, "tokens_estimated": {}, "cost_usd": {}, "concurrency": {},
	}
	validWindowKinds = map[string]struct{}{
		"none": {}, "fixed": {}, "calendar_day": {}, "calendar_week": {}, "manual": {},
	}
	validModes = map[string]struct{}{
		"enforce": {}, "observe": {}, "manual_first": {}, "disabled": {},
	}
)

const (
	maxScopeIDLen = 255
	defaultMode   = "enforce"
)

// quotaPolicyItem is the response DTO. Numeric caps are rendered as decimal
// strings (limit_value / burst_value) so no precision is lost across JSON.
type quotaPolicyItem struct {
	ID                  int64   `json:"id"`
	TenantID            int64   `json:"tenant_id"`
	ScopeKind           string  `json:"scope_kind"`
	ScopeID             string  `json:"scope_id"`
	Metric              string  `json:"metric"`
	WindowKind          string  `json:"window_kind"`
	WindowSeconds       int32   `json:"window_seconds"`
	LimitValue          string  `json:"limit_value"`
	BurstValue          string  `json:"burst_value"`
	Mode                string  `json:"mode"`
	Priority            int32   `json:"priority"`
	Enabled             bool    `json:"enabled"`
	ValidFrom           string  `json:"valid_from"`
	ValidUntil          *string `json:"valid_until,omitempty"`
	CreatedByActor      *string `json:"created_by_actor,omitempty"`
	LastModifiedByActor *string `json:"last_modified_by_actor,omitempty"`
	CreatedAt           string  `json:"created_at"`
	UpdatedAt           string  `json:"updated_at"`
}

type quotaPolicyListResponse struct {
	Object string            `json:"object"`
	Items  []quotaPolicyItem `json:"items"`
	Limit  int32             `json:"limit"`
	Offset int32             `json:"offset"`
}

type quotaPolicyDeleteResponse struct {
	Object  string `json:"object"`
	ID      int64  `json:"id"`
	Deleted bool   `json:"deleted"`
}

// quotaPolicyRequest is the create/update body. Pointer fields distinguish
// "omitted" (apply default) from an explicit value. limit_value / burst_value
// are decimal strings.
type quotaPolicyRequest struct {
	ScopeKind     string  `json:"scope_kind"`
	ScopeID       string  `json:"scope_id"`
	Metric        string  `json:"metric"`
	WindowKind    string  `json:"window_kind"`
	WindowSeconds *int32  `json:"window_seconds"`
	LimitValue    string  `json:"limit_value"`
	BurstValue    *string `json:"burst_value"`
	Mode          string  `json:"mode"`
	Priority      *int32  `json:"priority"`
	Enabled       *bool   `json:"enabled"`
	ValidFrom     *string `json:"valid_from"`
	ValidUntil    *string `json:"valid_until"`
	Reason        string  `json:"reason"`
}

// validatedPolicy is the fully-checked, neutral form shared by create/update.
type validatedPolicy struct {
	scopeKind     string
	scopeID       string
	metric        string
	windowKind    string
	windowSeconds int32
	limitValue    decimal.Decimal
	burstValue    decimal.Decimal
	mode          string
	priority      int32
	enabled       bool
	validFrom     time.Time
	validUntil    *time.Time
}

func newListHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, tenantID, ok := resolveTenantIdentity(w, r, d)
		if !ok {
			return
		}
		limit, offset, ok := pagination(w, r)
		if !ok {
			return
		}
		arg := dbquota.ListQuotaPoliciesForAdminParams{
			TenantID:   tenantID,
			PageLimit:  limit,
			PageOffset: offset,
		}
		if v, ok := filterValue(w, r, "scope_kind", validScopeKinds, "invalid_scope_kind"); !ok {
			return
		} else {
			arg.ScopeKind = v
		}
		if raw := strings.TrimSpace(r.URL.Query().Get("scope_id")); raw != "" {
			arg.ScopeID = &raw
		}
		if v, ok := filterValue(w, r, "metric", validMetrics, "invalid_metric"); !ok {
			return
		} else {
			arg.Metric = v
		}
		if enabled, ok := enabledFilter(w, r); !ok {
			return
		} else {
			arg.Enabled = enabled
		}
		rows, err := d.Store.ListQuotaPoliciesForAdmin(r.Context(), arg)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "admin_quota_backend_error",
				fmt.Sprintf("list quota policies failed: %v", err))
			return
		}
		items := make([]quotaPolicyItem, 0, len(rows))
		for _, row := range rows {
			items = append(items, itemFromRow(row))
		}
		writeJSON(w, http.StatusOK, quotaPolicyListResponse{
			Object: "admin_quota_policies_list",
			Items:  items,
			Limit:  limit,
			Offset: offset,
		})
	}
}

func newGetHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, tenantID, ok := resolveTenantIdentity(w, r, d)
		if !ok {
			return
		}
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		row, err := d.Store.GetQuotaPolicyByID(r.Context(), dbquota.GetQuotaPolicyByIDParams{
			TenantID: tenantID, ID: id,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "quota_policy_not_found", "quota policy not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "admin_quota_backend_error",
				fmt.Sprintf("get quota policy failed: %v", err))
			return
		}
		writeJSON(w, http.StatusOK, itemFromRow(row))
	}
}

func newCreateHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, tenantID, ok := resolveTenantIdentity(w, r, d)
		if !ok {
			return
		}
		var req quotaPolicyRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		vp, ok := validateRequest(w, req)
		if !ok {
			return
		}
		actor := actorAttribution(ident)
		insert, ok := buildInsertParams(w, tenantID, vp, actor)
		if !ok {
			return
		}
		audit, ok := buildAudit(w, r, ident, tenantID, "create_quota_policy", req.Reason, vp)
		if !ok {
			return
		}
		row, err := d.Store.CreateQuotaPolicyWithAudit(r.Context(),
			quotaPolicyCreateParams{insert: insert}, audit)
		if err != nil {
			writeMutationError(w, err, "quota_policy_create_failed")
			return
		}
		writeJSON(w, http.StatusCreated, itemFromRow(row))
	}
}

func newUpdateHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, tenantID, ok := resolveTenantIdentity(w, r, d)
		if !ok {
			return
		}
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		var req quotaPolicyRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		vp, ok := validateRequest(w, req)
		if !ok {
			return
		}
		actor := actorAttribution(ident)
		update, ok := buildUpdateParams(w, tenantID, id, vp, actor)
		if !ok {
			return
		}
		audit, ok := buildAudit(w, r, ident, tenantID, "update_quota_policy", req.Reason, vp)
		if !ok {
			return
		}
		row, err := d.Store.UpdateQuotaPolicyWithAudit(r.Context(),
			quotaPolicyUpdateParams{update: update}, audit)
		if err != nil {
			writeMutationError(w, err, "quota_policy_update_failed")
			return
		}
		writeJSON(w, http.StatusOK, itemFromRow(row))
	}
}

func newDeleteHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, tenantID, ok := resolveTenantIdentity(w, r, d)
		if !ok {
			return
		}
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		var req quotaPolicyRequest
		if !decodeOptionalJSON(w, r, &req) {
			return
		}
		payload, err := json.Marshal(map[string]any{"tenant_id": tenantID, "id": id, "deleted": true})
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "audit_payload_failed", err.Error())
			return
		}
		audit := auditInput{
			TenantID: tenantID, ActorID: actorID(ident), ActorRole: actorRole(ident),
			Action: "delete_quota_policy", RequestID: requestID(r),
			Reason: reasonPtr(req.Reason), Payload: payload,
		}
		deletedID, err := d.Store.DeleteQuotaPolicyWithAudit(r.Context(),
			quotaPolicyDeleteParams{TenantID: tenantID, ID: id}, audit)
		if err != nil {
			writeMutationError(w, err, "quota_policy_delete_failed")
			return
		}
		writeJSON(w, http.StatusOK, quotaPolicyDeleteResponse{
			Object: "admin_quota_policy_deleted", ID: deletedID, Deleted: true,
		})
	}
}
