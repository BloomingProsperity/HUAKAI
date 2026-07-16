package adminhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/recentreq"
)

type ProviderAccountHealthDeps struct {
	Auth  providerAccountHealthAuth
	Store providerAccountHealthStore
	// RecentReqRing 暴露进程内的近期请求结果(MGMT-RECENTREQ-01)。
	// 传 nil 是安全的:此时响应中会省略 recent_requests 字段。
	RecentReqRing *recentreq.Ring
}

type providerAccountHealthAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type providerAccountHealthStore interface {
	GetAdminProviderAccountHealth(context.Context, admindb.GetAdminProviderAccountHealthParams) (admindb.GetAdminProviderAccountHealthRow, error)
	SummarizeProviderAccountHealth(context.Context, int64) ([]admindb.SummarizeProviderAccountHealthRow, error)
}

type providerAccountHealthResponseBody struct {
	ID                         int64    `json:"id"`
	HealthState                string   `json:"health_state"`
	HealthStateUntil           *string  `json:"health_state_until,omitempty"`
	LastProbeLatencyMS         *int32   `json:"last_probe_latency_ms"`
	LastProbeAt                *string  `json:"last_probe_at"`
	LastObservedAt             *string  `json:"last_request_observed_at"`
	ObservationSource          string   `json:"last_request_observation_source"`
	ModelSyncLastCheckAt       *string  `json:"model_sync_last_check_at"`
	SessionWindow5hStart       *string  `json:"session_window_5h_start"`
	SessionWindow5hEnd         *string  `json:"session_window_5h_end"`
	SessionWindow5hStatus      *string  `json:"session_window_5h_status"`
	SessionWindow5hUtilization *float64 `json:"session_window_5h_utilization"`
	SessionWindow7dStart       *string  `json:"session_window_7d_start"`
	SessionWindow7dEnd         *string  `json:"session_window_7d_end"`
	SessionWindow7dStatus      *string  `json:"session_window_7d_status"`
	SessionWindow7dUtilization *float64 `json:"session_window_7d_utilization"`
	LastRefreshAt              *string  `json:"last_refresh_at"`
	LastRefreshOutcome         *string  `json:"last_refresh_outcome"`
	FailureClass               *string  `json:"failure_class"`
	FailureCount               int32    `json:"failure_count"`
	Enabled                    bool     `json:"enabled"`
	RequiresAction             bool     `json:"requires_action"`
	UpdatedAt                  string   `json:"updated_at"`
	// 当没有进程内数据可用时(ring 为 nil 或没有记录到请求),
	// RecentRequests 会被省略。零值不会被输出。
	RecentRequests *recentRequestsSummary `json:"recent_requests,omitempty"`
}

// recentRequestsSummary 是进程内近期请求计数对应的 JSON 结构。
type recentRequestsSummary struct {
	Total   int    `json:"total"`
	Success int    `json:"success"`
	Failure int    `json:"failure"`
	LastAt  string `json:"last_at,omitempty"`
}

func MountProviderAccountHealthRoutes(r chi.Router, d ProviderAccountHealthDeps) {
	r.Get("/{id}/health", newProviderAccountHealthHandler(d))
	// 账号池健康聚合(B9):跨整个租户池计数,静态路径须先于 /{id}/health 无冲突(chi 精确段优先)。
	r.Get("/health-summary", newProviderAccountHealthSummaryHandler(d))
}

func newProviderAccountHealthHandler(d ProviderAccountHealthDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, tenantID, ok := resolveProviderAccountHealthTenant(w, r, d)
		if !ok {
			return
		}
		id, ok := parseProviderAccountHealthID(w, r)
		if !ok {
			return
		}
		row, err := d.Store.GetAdminProviderAccountHealth(r.Context(), admindb.GetAdminProviderAccountHealthParams{
			TenantID: tenantID,
			ID:       id,
		})
		if err != nil {
			writeProviderAccountHealthReadError(w, err)
			return
		}
		writeProviderAccountHealthJSON(w, http.StatusOK, providerAccountHealthResponse(row, d.RecentReqRing))
	}
}

func resolveProviderAccountHealthTenant(w http.ResponseWriter, r *http.Request, d ProviderAccountHealthDeps) (admin.AdminIdentity, int64, bool) {
	if d.Auth == nil || d.Store == nil {
		writeError(w, http.StatusServiceUnavailable, "gateway_not_configured", "provider account health dependency unset")
		return admin.AdminIdentity{}, 0, false
	}
	ident, err := d.Auth.Resolve(r.Context(), r)
	if err != nil {
		writeAdminAuthError(w, err)
		return admin.AdminIdentity{}, 0, false
	}
	switch ident.Role {
	case admin.RoleTenantOperator:
		if ident.ScopeTenantID <= 0 {
			writeError(w, http.StatusForbidden, "admin_forbidden", "tenant_operator scope_tenant_id required")
			return admin.AdminIdentity{}, 0, false
		}
		return ident, ident.ScopeTenantID, true
	case admin.RolePlatformAdmin:
		if ident.ScopeTenantID > 0 {
			return ident, ident.ScopeTenantID, true
		}
		tenantID, ok := resolvePlatformAdminQueryTenant(w, r, ident)
		if !ok {
			return admin.AdminIdentity{}, 0, false
		}
		return ident, tenantID, true
	default:
		writeError(w, http.StatusForbidden, "admin_forbidden", "admin role required")
		return admin.AdminIdentity{}, 0, false
	}
}

func parseProviderAccountHealthID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_provider_account_id", "id must be a positive int64")
		return 0, false
	}
	return id, true
}

func writeProviderAccountHealthReadError(w http.ResponseWriter, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "provider_account_not_found", "provider account not found")
		return
	}
	writeError(w, http.StatusServiceUnavailable, "provider_account_health_unavailable", "provider account health is unavailable")
}

func providerAccountHealthResponse(row admindb.GetAdminProviderAccountHealthRow, ring *recentreq.Ring) providerAccountHealthResponseBody {
	return providerAccountHealthResponseAt(row, ring, time.Now().UTC())
}

func providerAccountHealthResponseAt(row admindb.GetAdminProviderAccountHealthRow, ring *recentreq.Ring, now time.Time) providerAccountHealthResponseBody {
	// requires_action 是确定性 admin 视图规则,不从请求输入或上游响应推断。
	requiresAction := row.HealthState == "revoked" || row.FailureCount > 3
	status5h, utilization5h := activeSessionWindowView(row.SessionWindow5hEnd, row.SessionWindow5hStatus, row.SessionWindow5hUtilization, now)
	status7d, utilization7d := activeSessionWindowView(row.SessionWindow7dEnd, row.SessionWindow7dStatus, row.SessionWindow7dUtilization, now)
	return providerAccountHealthResponseBody{
		ID:                         row.ID,
		HealthState:                row.HealthState,
		HealthStateUntil:           formatProviderAccountHealthTime(row.HealthStateUntil),
		LastProbeLatencyMS:         row.LastProbeLatencyMS,
		LastProbeAt:                formatProviderAccountHealthTime(row.LastProbeAt),
		LastObservedAt:             formatProviderAccountHealthTime(row.LastRequestObservedAt),
		ObservationSource:          "request_completion_event",
		ModelSyncLastCheckAt:       formatProviderAccountHealthTime(row.ModelSyncLastCheckAt),
		SessionWindow5hStart:       formatProviderAccountHealthTime(row.SessionWindow5hStart),
		SessionWindow5hEnd:         formatProviderAccountHealthTime(row.SessionWindow5hEnd),
		SessionWindow5hStatus:      status5h,
		SessionWindow5hUtilization: utilization5h,
		SessionWindow7dStart:       formatProviderAccountHealthTime(row.SessionWindow7dStart),
		SessionWindow7dEnd:         formatProviderAccountHealthTime(row.SessionWindow7dEnd),
		SessionWindow7dStatus:      status7d,
		SessionWindow7dUtilization: utilization7d,
		LastRefreshAt:              formatProviderAccountHealthTime(row.LastRefreshAt),
		LastRefreshOutcome:         row.LastRefreshOutcome,
		FailureClass:               row.FailureClass,
		FailureCount:               row.FailureCount,
		Enabled:                    row.Enabled,
		RequiresAction:             requiresAction,
		UpdatedAt:                  requiredProviderAccountHealthTime(row.UpdatedAt),
		RecentRequests:             recentRequestsSummaryFor(ring, row.ID),
	}
}

func activeSessionWindowView(end pgtype.Timestamptz, status *string, utilization pgtype.Numeric, now time.Time) (*string, *float64) {
	if end.Valid && end.Time.Before(now.UTC()) {
		expired := "expired"
		return &expired, nil
	}
	if !end.Valid {
		return status, nil
	}
	value, err := utilization.Float64Value()
	if err != nil || !value.Valid {
		return status, nil
	}
	return status, &value.Float64
}

func formatProviderAccountHealthTime(ts pgtype.Timestamptz) *string {
	if !ts.Valid {
		return nil
	}
	value := ts.Time.UTC().Format(time.RFC3339)
	return &value
}

func requiredProviderAccountHealthTime(ts pgtype.Timestamptz) string {
	if !ts.Valid {
		return ""
	}
	return ts.Time.UTC().Format(time.RFC3339)
}

// recentRequestsSummaryFor 从进程内 ring 构建可选的 recent_requests 载荷。
// 当 ring 为 nil 或该账号没有数据时返回 nil
// (保留 omitempty 语义:该字段会从 JSON 响应中缺省)。
func recentRequestsSummaryFor(ring *recentreq.Ring, accountID int64) *recentRequestsSummary {
	if ring == nil {
		return nil
	}
	s := ring.Summary(accountID)
	if s.Total == 0 {
		return nil
	}
	var lastAt string
	if !s.LastAt.IsZero() {
		lastAt = s.LastAt.UTC().Format(time.RFC3339)
	}
	return &recentRequestsSummary{
		Total:   s.Total,
		Success: s.Success,
		Failure: s.Failure,
		LastAt:  lastAt,
	}
}

func writeProviderAccountHealthJSON(w http.ResponseWriter, status int, body providerAccountHealthResponseBody) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
