package gatewayhttp

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

type AdminObservabilityAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type AdminObservabilityStore interface {
	CountUsageRecords(context.Context, dbbilling.CountUsageRecordsParams) (int64, error)
	ListUsageRecords(context.Context, dbbilling.ListUsageRecordsParams) ([]dbbilling.ListUsageRecordsRow, error)
	CountBillingClaims(context.Context, dbbilling.CountBillingClaimsParams) (int64, error)
	ListBillingClaims(context.Context, dbbilling.ListBillingClaimsParams) ([]dbbilling.ListBillingClaimsRow, error)
	CountAuditEvents(context.Context, dbbilling.CountAuditEventsParams) (int64, error)
	ListAuditEvents(context.Context, dbbilling.ListAuditEventsParams) ([]dbbilling.ListAuditEventsRow, error)
}

type AdminObservabilityDeps interface {
	AdminObservabilityAuth() AdminObservabilityAuth
	AdminObservabilityStore() AdminObservabilityStore
}

type obsQuery struct {
	TenantID                                                                    *int64
	FromTs, ToTs, CursorCreatedAt                                               pgtype.Timestamptz
	Limit, FetchLimit                                                           int32
	HasCursor                                                                   bool
	CursorID                                                                    int64
	Provider, Model, Status, EventClass, EventType, Severity, LedgerID, ActorID *string
	PoolID, APIKeyID, ProviderAccountID                                         *int64
	PendingOnly                                                                 bool
}

type obsCursor struct {
	V     int `json:"v"`
	K, TS string
	ID    int64 `json:"id"`
}

type obsListResponse struct {
	Items      any    `json:"items"`
	NextCursor string `json:"next_cursor"`
	Total      int64  `json:"total"`
}

func NewUsageHandler(d AdminObservabilityDeps) http.HandlerFunc {
	return newObsHandler(d, "usage", countUsage, listUsage, func(r dbbilling.ListUsageRecordsRow) (pgtype.Timestamptz, int64) { return r.CreatedAt, r.ID }, identityRow[dbbilling.ListUsageRecordsRow])
}

func NewClaimsHandler(d AdminObservabilityDeps) http.HandlerFunc {
	return newObsHandler(d, "claims", countClaims, listClaims, func(r dbbilling.ListBillingClaimsRow) (pgtype.Timestamptz, int64) { return r.CreatedAt, r.ID }, identityRow[dbbilling.ListBillingClaimsRow])
}

func NewAuditEventsHandler(d AdminObservabilityDeps) http.HandlerFunc {
	return newObsHandler(d, "audit", countAudit, listAudit, func(r dbbilling.ListAuditEventsRow) (pgtype.Timestamptz, int64) { return r.CreatedAt, r.ID }, mapAuditRow)
}

func newObsHandler[T any](d AdminObservabilityDeps, kind string, count func(context.Context, AdminObservabilityStore, obsQuery) (int64, error), list func(context.Context, AdminObservabilityStore, obsQuery) ([]T, error), pos func(T) (pgtype.Timestamptz, int64), mapRow func(T) any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store, q, ok := resolveObs(w, r, d, kind)
		if !ok {
			return
		}
		total, err := count(r.Context(), store, q)
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, kind+"_count_failed", err.Error())
			return
		}
		rows, err := list(r.Context(), store, q)
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, kind+"_query_failed", err.Error())
			return
		}
		next := ""
		if int32(len(rows)) > q.Limit {
			ts, id := pos(rows[q.Limit-1])
			next = encodeObsCursor(kind, ts, id)
			rows = rows[:q.Limit]
		}
		items := make([]any, 0, len(rows))
		for _, row := range rows {
			items = append(items, mapRow(row))
		}
		writeAuditJSON(w, http.StatusOK, obsListResponse{Items: items, NextCursor: next, Total: total})
	}
}

func resolveObs(w http.ResponseWriter, r *http.Request, d AdminObservabilityDeps, kind string) (AdminObservabilityStore, obsQuery, bool) {
	if d == nil || d.AdminObservabilityAuth() == nil || d.AdminObservabilityStore() == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "admin observability dependency unset")
		return nil, obsQuery{}, false
	}
	ident, err := d.AdminObservabilityAuth().Resolve(r.Context(), r)
	if err != nil {
		if errors.Is(err, admin.ErrAdminBackend) {
			writeJSONError(w, http.StatusServiceUnavailable, "admin_backend_error", "admin auth backend transient failure")
		} else {
			writeJSONError(w, http.StatusUnauthorized, "admin_unauthorized", "missing or invalid admin credential")
		}
		return nil, obsQuery{}, false
	}
	values := r.URL.Query()
	if strings.Contains(r.URL.RawQuery, "cursor=") && strings.TrimSpace(values.Get("cursor")) == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid_cursor", "cursor must be an opaque base64 cursor")
		return nil, obsQuery{}, false
	}
	q, ok := parseObsQuery(w, values, ident, kind)
	return d.AdminObservabilityStore(), q, ok
}

func parseObsQuery(w http.ResponseWriter, v url.Values, ident admin.AdminIdentity, kind string) (obsQuery, bool) {
	tenantID, ok := parseTenantScope(w, trim(v, "tenant_id"), ident)
	if !ok {
		return obsQuery{}, false
	}
	limit := int32(100)
	if raw := trim(v, "limit"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || n < 1 || n > 200 {
			writeJSONError(w, http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 200")
			return obsQuery{}, false
		}
		limit = int32(n)
	}
	from, ok := parseQueryTime(w, trim(v, "from"), "from")
	if !ok {
		return obsQuery{}, false
	}
	to, ok := parseQueryTime(w, trim(v, "to"), "to")
	if !ok {
		return obsQuery{}, false
	}
	q := obsQuery{TenantID: tenantID, FromTs: tsParam(from), ToTs: tsParam(to), Limit: limit, FetchLimit: limit + 1,
		Provider: strPtr(trim(v, "provider")), Model: strPtr(trim(v, "model")), Status: strPtr(trim(v, "status")),
		EventClass: strPtr(trim(v, "event_class")), EventType: strPtr(trim(v, "event_type")), Severity: strPtr(trim(v, "severity")),
		LedgerID: strPtr(trim(v, "ledger_id")), ActorID: strPtr(trim(v, "actor_id")), PendingOnly: trim(v, "pending_reconciliation_only") == "true"}
	if q.PoolID, ok = parseIntFilter(w, v, "pool_id"); !ok {
		return obsQuery{}, false
	}
	if q.APIKeyID, ok = parseIntFilter(w, v, "api_key_id"); !ok {
		return obsQuery{}, false
	}
	if q.ProviderAccountID, ok = parseIntFilter(w, v, "provider_account_id"); !ok {
		return obsQuery{}, false
	}
	if raw := trim(v, "cursor"); raw != "" {
		ts, id, err := decodeObsCursor(raw, kind)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_cursor", "cursor must be an opaque base64 cursor")
			return obsQuery{}, false
		}
		q.HasCursor, q.CursorCreatedAt, q.CursorID = true, tsParam(&ts), id
	}
	return q, true
}

func parseTenantScope(w http.ResponseWriter, raw string, ident admin.AdminIdentity) (*int64, bool) {
	var tenantID *int64
	if raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			writeJSONError(w, http.StatusBadRequest, "invalid_tenant_id", "tenant_id must be a positive int64")
			return nil, false
		}
		tenantID = &id
	}
	if ident.Role == admin.RolePlatformAdmin {
		return tenantID, true
	}
	if ident.Role != admin.RoleTenantOperator || ident.ScopeTenantID <= 0 {
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "tenant scope required")
		return nil, false
	}
	if tenantID != nil && *tenantID != ident.ScopeTenantID {
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "caller cannot act on this tenant scope")
		return nil, false
	}
	id := ident.ScopeTenantID
	return &id, true
}

func countUsage(ctx context.Context, s AdminObservabilityStore, q obsQuery) (int64, error) {
	return s.CountUsageRecords(ctx, dbbilling.CountUsageRecordsParams{TenantID: q.TenantID, FromTs: q.FromTs, ToTs: q.ToTs, Provider: q.Provider, PoolID: q.PoolID, APIKeyID: q.APIKeyID, ProviderAccountID: q.ProviderAccountID, Model: q.Model, PendingReconciliationOnly: q.PendingOnly})
}
func listUsage(ctx context.Context, s AdminObservabilityStore, q obsQuery) ([]dbbilling.ListUsageRecordsRow, error) {
	return s.ListUsageRecords(ctx, dbbilling.ListUsageRecordsParams{TenantID: q.TenantID, FromTs: q.FromTs, ToTs: q.ToTs, Provider: q.Provider, PoolID: q.PoolID, APIKeyID: q.APIKeyID, ProviderAccountID: q.ProviderAccountID, Model: q.Model, PendingReconciliationOnly: q.PendingOnly, HasCursor: q.HasCursor, CursorCreatedAt: q.CursorCreatedAt, CursorID: q.CursorID, PageLimit: q.FetchLimit})
}
func countClaims(ctx context.Context, s AdminObservabilityStore, q obsQuery) (int64, error) {
	return s.CountBillingClaims(ctx, dbbilling.CountBillingClaimsParams{TenantID: q.TenantID, FromTs: q.FromTs, ToTs: q.ToTs, Status: q.Status, Provider: q.Provider, PoolID: q.PoolID, APIKeyID: q.APIKeyID, ProviderAccountID: q.ProviderAccountID, Model: q.Model})
}
func listClaims(ctx context.Context, s AdminObservabilityStore, q obsQuery) ([]dbbilling.ListBillingClaimsRow, error) {
	return s.ListBillingClaims(ctx, dbbilling.ListBillingClaimsParams{TenantID: q.TenantID, FromTs: q.FromTs, ToTs: q.ToTs, Status: q.Status, Provider: q.Provider, PoolID: q.PoolID, APIKeyID: q.APIKeyID, ProviderAccountID: q.ProviderAccountID, Model: q.Model, HasCursor: q.HasCursor, CursorCreatedAt: q.CursorCreatedAt, CursorID: q.CursorID, PageLimit: q.FetchLimit})
}
func countAudit(ctx context.Context, s AdminObservabilityStore, q obsQuery) (int64, error) {
	return s.CountAuditEvents(ctx, dbbilling.CountAuditEventsParams{TenantID: q.TenantID, FromTs: q.FromTs, ToTs: q.ToTs, EventClass: q.EventClass, EventType: q.EventType, Severity: q.Severity, LedgerID: q.LedgerID, ActorID: q.ActorID})
}
func listAudit(ctx context.Context, s AdminObservabilityStore, q obsQuery) ([]dbbilling.ListAuditEventsRow, error) {
	return s.ListAuditEvents(ctx, dbbilling.ListAuditEventsParams{TenantID: q.TenantID, FromTs: q.FromTs, ToTs: q.ToTs, EventClass: q.EventClass, EventType: q.EventType, Severity: q.Severity, LedgerID: q.LedgerID, ActorID: q.ActorID, HasCursor: q.HasCursor, CursorCreatedAt: q.CursorCreatedAt, CursorID: q.CursorID, PageLimit: q.FetchLimit})
}
