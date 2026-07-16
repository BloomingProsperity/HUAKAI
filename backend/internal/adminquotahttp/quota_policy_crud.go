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

// 这些枚举白名单与 quota_policies 的 CHECK 约束保持一致。在此处校验可把非法
// 枚举输入变成 400,而不是让 Postgres 抛出 CHECK(那会表现为 503/500)。
// 这里暴露的是 HUAKAI 的完整超集。
var (
	validScopeKinds = map[string]struct{}{
		"global": {}, "user": {}, "api_key": {}, "channel": {},
		"pool_group": {}, "provider_account": {},
	}
	validMetrics = map[string]struct{}{
		"requests": {}, "tokens_estimated": {}, "cost_usd": {}, "concurrency": {},
	}
	validWindowKinds = map[string]struct{}{
		"none": {}, "fixed": {}, "calendar_day": {}, "calendar_week": {}, "calendar_month": {}, "manual": {},
	}
	validModes = map[string]struct{}{
		"enforce": {}, "observe": {}, "manual_first": {}, "disabled": {},
	}
)

const (
	maxScopeIDLen = 255
	defaultMode   = "enforce"
)

// quotaPolicyItem 是响应 DTO。数值上限以十进制字符串渲染
// (limit_value / burst_value),以免在 JSON 传输中丢失精度；burst_value 表示
// 窗口硬上限在 limit_value 之上的增量。
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

// quotaPolicyRequest 是 create/update 的请求体。指针字段用来区分"省略"
//(套用默认值)与显式给定的值。limit_value / burst_value 为十进制字符串。
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

// validatedPolicy 是经过完整校验、中立的表示形式,由 create/update 共用。
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
