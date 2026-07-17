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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

const providerAccountBulkMaxTargets = 1000

var errProviderAccountBulkTxPoolUnset = errors.New("provider account bulk transaction pool unset")

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
	UpdateProviderAccountByTagWithAudit(context.Context, providerAccountBulkItemParams) (providerAccountBulkItemOutcome, error)
}

type providerAccountBulkListStore interface {
	ListAdminProviderAccounts(context.Context, admindb.ListAdminProviderAccountsParams) ([]admindb.AdminProviderAccountRow, error)
}

type providerAccountBulkStoreAdapter struct {
	base providerAccountBulkListStore
	pool *pgxpool.Pool
}

type providerAccountBulkItemParams struct {
	TenantID     int64
	ID           int64
	Tag          string
	ActorID      string
	ActorRole    string
	RequestID    *string
	Enabled      *bool
	Priority     *int32
	StaticWeight *int32
}

type providerAccountBulkItemOutcome struct {
	Status string
	Code   string
}

type providerAccountBulkRequest struct {
	Tag          string `json:"tag"`
	Enabled      *bool  `json:"enabled,omitempty"`
	Priority     *int32 `json:"priority,omitempty"`
	StaticWeight *int32 `json:"static_weight,omitempty"`
}

type providerAccountBulkItemResult struct {
	ID        int64  `json:"id"`
	Status    string `json:"status"`
	Code      string `json:"code,omitempty"`
	Message   string `json:"message,omitempty"`
	Retryable bool   `json:"retryable"`
}

type providerAccountBulkResponse struct {
	AffectedIDs []int64                         `json:"affected_ids"`
	Count       int                             `json:"count"`
	Total       int                             `json:"total"`
	Succeeded   int                             `json:"succeeded"`
	Failed      int                             `json:"failed"`
	Skipped     int                             `json:"skipped"`
	Results     []providerAccountBulkItemResult `json:"results"`
}

// NewProviderAccountBulkStoreAdapter 接入逐账号短事务。列表只负责确定批次候选，
// 每个候选在事务内重新锁定并确认标签，再原子提交账号修改和管理员审计。
func NewProviderAccountBulkStoreAdapter(base providerAccountBulkListStore, pool *pgxpool.Pool) providerAccountBulkStore {
	return providerAccountBulkStoreAdapter{base: base, pool: pool}
}

func (s providerAccountBulkStoreAdapter) ListAdminProviderAccounts(ctx context.Context, arg admindb.ListAdminProviderAccountsParams) ([]admindb.AdminProviderAccountRow, error) {
	if s.base == nil {
		return nil, errProviderAccountBulkTxPoolUnset
	}
	return s.base.ListAdminProviderAccounts(ctx, arg)
}

func (s providerAccountBulkStoreAdapter) UpdateProviderAccountByTagWithAudit(ctx context.Context, arg providerAccountBulkItemParams) (providerAccountBulkItemOutcome, error) {
	if s.pool == nil {
		return providerAccountBulkItemOutcome{}, errProviderAccountBulkTxPoolUnset
	}

	outcome := providerAccountBulkItemOutcome{Status: "succeeded"}
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var enabled bool
		var priority int32
		var staticWeight int32
		err := tx.QueryRow(ctx, `
			SELECT enabled, priority, static_weight
			FROM provider_accounts
			WHERE id = $1
			  AND tenant_id = $2
			  AND deleted_at IS NULL
			  AND tags @> ARRAY[$3::text]
			FOR UPDATE
		`, arg.ID, arg.TenantID, arg.Tag).Scan(&enabled, &priority, &staticWeight)
		if errors.Is(err, pgx.ErrNoRows) {
			outcome = providerAccountBulkItemOutcome{
				Status: "skipped",
				Code:   "target_no_longer_matches",
			}
			return nil
		}
		if err != nil {
			return err
		}

		if providerAccountBulkAlreadyApplied(enabled, priority, staticWeight, arg) {
			outcome = providerAccountBulkItemOutcome{
				Status: "skipped",
				Code:   "already_in_desired_state",
			}
			return nil
		}

		q := admindb.New(tx)
		if _, err := q.UpdateAdminProviderAccount(ctx, admindb.UpdateAdminProviderAccountParams{
			ID:           arg.ID,
			TenantID:     arg.TenantID,
			ActorID:      &arg.ActorID,
			Enabled:      arg.Enabled,
			Priority:     arg.Priority,
			StaticWeight: arg.StaticWeight,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				outcome = providerAccountBulkItemOutcome{
					Status: "skipped",
					Code:   "target_no_longer_matches",
				}
				return nil
			}
			return err
		}

		payload, err := json.Marshal(map[string]any{
			"tenant_id": arg.TenantID,
			"id":        arg.ID,
			"tag":       arg.Tag,
			"before": map[string]any{
				"enabled":       enabled,
				"priority":      priority,
				"static_weight": staticWeight,
			},
			"after": map[string]any{
				"enabled":       valueOrCurrentBool(arg.Enabled, enabled),
				"priority":      valueOrCurrentInt32(arg.Priority, priority),
				"static_weight": valueOrCurrentInt32(arg.StaticWeight, staticWeight),
			},
		})
		if err != nil {
			return err
		}
		targetID := arg.ID
		if _, err := q.InsertAdminAuditEvent(ctx, admindb.InsertAdminAuditEventParams{
			TenantID:   &arg.TenantID,
			ActorID:    arg.ActorID,
			ActorRole:  arg.ActorRole,
			Action:     "update_provider_account",
			TargetType: "provider_account",
			TargetID:   &targetID,
			RequestID:  arg.RequestID,
			Payload:    payload,
		}); err != nil {
			return err
		}
		return nil
	})
	return outcome, err
}

func providerAccountBulkAlreadyApplied(enabled bool, priority, staticWeight int32, arg providerAccountBulkItemParams) bool {
	return (arg.Enabled == nil || *arg.Enabled == enabled) &&
		(arg.Priority == nil || *arg.Priority == priority) &&
		(arg.StaticWeight == nil || *arg.StaticWeight == staticWeight)
}

func valueOrCurrentBool(value *bool, current bool) bool {
	if value == nil {
		return current
	}
	return *value
}

func valueOrCurrentInt32(value *int32, current int32) int32 {
	if value == nil {
		return current
	}
	return *value
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

		rows, err := d.Store.ListAdminProviderAccounts(r.Context(), admindb.ListAdminProviderAccountsParams{
			TenantID:   tenantID,
			TagFilter:  tag,
			LimitCount: providerAccountBulkMaxTargets + 1,
		})
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "provider_account_list_failed", "provider account list is temporarily unavailable")
			return
		}
		if len(rows) > providerAccountBulkMaxTargets {
			writeError(w, http.StatusConflict, "bulk_target_limit_exceeded", "matching provider accounts exceed the 1000 item bulk limit")
			return
		}

		resp := providerAccountBulkResponse{
			AffectedIDs: []int64{},
			Total:       len(rows),
			Results:     make([]providerAccountBulkItemResult, 0, len(rows)),
		}
		actorIDStr := ident.AuditActor()
		actorRole := ident.Role
		if actorRole == "" {
			actorRole = admin.RoleTenantOperator
		}
		reqID := middleware.GetReqID(r.Context())
		var reqIDArg *string
		if reqID != "" {
			reqIDArg = &reqID
		}

		for _, row := range rows {
			outcome, err := d.Store.UpdateProviderAccountByTagWithAudit(r.Context(), providerAccountBulkItemParams{
				TenantID:     tenantID,
				ID:           row.ID,
				Tag:          tag,
				ActorID:      actorIDStr,
				ActorRole:    actorRole,
				RequestID:    reqIDArg,
				Enabled:      req.Enabled,
				Priority:     req.Priority,
				StaticWeight: req.StaticWeight,
			})
			if err != nil {
				resp.Failed++
				resp.Results = append(resp.Results, providerAccountBulkItemResult{
					ID:        row.ID,
					Status:    "failed",
					Code:      "provider_account_update_failed",
					Message:   "provider account update and audit transaction failed",
					Retryable: true,
				})
				continue
			}
			switch outcome.Status {
			case "skipped":
				resp.Skipped++
				resp.Results = append(resp.Results, providerAccountBulkItemResult{
					ID:        row.ID,
					Status:    "skipped",
					Code:      outcome.Code,
					Retryable: false,
				})
			default:
				resp.Succeeded++
				resp.AffectedIDs = append(resp.AffectedIDs, row.ID)
				resp.Results = append(resp.Results, providerAccountBulkItemResult{
					ID:        row.ID,
					Status:    "succeeded",
					Retryable: false,
				})
			}
		}
		resp.Count = resp.Succeeded

		status := http.StatusOK
		if resp.Failed > 0 {
			status = http.StatusMultiStatus
		}
		writeAdminCatalogJSON(w, status, resp)
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
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "invalid_json", "request body is required")
			return false
		}
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			writeError(w, http.StatusBadRequest, "invalid_json", "request body must contain a single JSON object")
		} else {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		}
		return false
	}
	return true
}
