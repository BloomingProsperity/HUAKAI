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

const defaultProviderAccountHealthPlatformTenantID = int64(1)

type ProviderAccountHealthDeps struct {
	Auth  providerAccountHealthAuth
	Store providerAccountHealthStore
	// RecentReqRing exposes in-process recent request outcomes (MGMT-RECENTREQ-01).
	// nil is safe: recent_requests field will be omitted from the response.
	RecentReqRing *recentreq.Ring
}

type providerAccountHealthAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type providerAccountHealthStore interface {
	GetAdminProviderAccountHealth(context.Context, admindb.GetAdminProviderAccountHealthParams) (admindb.GetAdminProviderAccountHealthRow, error)
}

type providerAccountHealthResponseBody struct {
	ID                    int64   `json:"id"`
	HealthState           string  `json:"health_state"`
	HealthStateUntil      *string `json:"health_state_until,omitempty"`
	LastProbeLatencyMS    *int32  `json:"last_probe_latency_ms"`
	LastProbeAt           *string `json:"last_probe_at"`
	ModelSyncLastCheckAt  *string `json:"model_sync_last_check_at"`
	SessionWindow5hStart  *string `json:"session_window_5h_start"`
	SessionWindow5hEnd    *string `json:"session_window_5h_end"`
	SessionWindow5hStatus *string `json:"session_window_5h_status"`
	LastRefreshAt         *string `json:"last_refresh_at"`
	LastRefreshOutcome    *string `json:"last_refresh_outcome"`
	FailureClass          *string `json:"failure_class"`
	FailureCount          int32   `json:"failure_count"`
	Enabled               bool    `json:"enabled"`
	RequiresAction        bool    `json:"requires_action"`
	UpdatedAt             string  `json:"updated_at"`
	// RecentRequests is omitted when no in-process data is available (nil ring or
	// no recorded requests). Zero-value is not emitted.
	RecentRequests *recentRequestsSummary `json:"recent_requests,omitempty"`
}

// recentRequestsSummary is the JSON shape for in-process recent request counts.
type recentRequestsSummary struct {
	Total   int    `json:"total"`
	Success int    `json:"success"`
	Failure int    `json:"failure"`
	LastAt  string `json:"last_at,omitempty"`
}

func MountProviderAccountHealthRoutes(r chi.Router, d ProviderAccountHealthDeps) {
	r.Get("/{id}/health", newProviderAccountHealthHandler(d))
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
		return ident, defaultProviderAccountHealthPlatformTenantID, true
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
	// requires_action 是确定性 admin 视图规则,不从请求输入或上游响应推断。
	requiresAction := row.HealthState == "revoked" || row.FailureCount > 3
	return providerAccountHealthResponseBody{
		ID:                    row.ID,
		HealthState:           row.HealthState,
		HealthStateUntil:      formatProviderAccountHealthTime(row.HealthStateUntil),
		LastProbeLatencyMS:    row.LastProbeLatencyMS,
		LastProbeAt:           formatProviderAccountHealthTime(row.LastProbeAt),
		ModelSyncLastCheckAt:  formatProviderAccountHealthTime(row.ModelSyncLastCheckAt),
		SessionWindow5hStart:  formatProviderAccountHealthTime(row.SessionWindow5hStart),
		SessionWindow5hEnd:    formatProviderAccountHealthTime(row.SessionWindow5hEnd),
		SessionWindow5hStatus: row.SessionWindow5hStatus,
		LastRefreshAt:         formatProviderAccountHealthTime(row.LastRefreshAt),
		LastRefreshOutcome:    row.LastRefreshOutcome,
		FailureClass:          row.FailureClass,
		FailureCount:          row.FailureCount,
		Enabled:               row.Enabled,
		RequiresAction:        requiresAction,
		UpdatedAt:             requiredProviderAccountHealthTime(row.UpdatedAt),
		RecentRequests:        recentRequestsSummaryFor(ring, row.ID),
	}
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

// recentRequestsSummaryFor builds the optional recent_requests payload from an
// in-process ring. Returns nil when the ring is nil or has no data for the account
// (preserves omitempty: the field is absent from the JSON response).
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
