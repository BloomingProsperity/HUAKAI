package meusagehttp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

type AuthResolver interface {
	Resolve(context.Context, *http.Request) (auth.Identity, error)
}

type UsageStore interface {
	ListUsageRecords(context.Context, dbbilling.ListUsageRecordsParams) ([]dbbilling.ListUsageRecordsRow, error)
}

type Deps struct {
	Auth  AuthResolver
	Store UsageStore
}

type queryParams struct {
	FromTs, ToTs, CursorCreatedAt pgtype.Timestamptz
	Limit, FetchLimit             int32
	HasCursor                     bool
	CursorID                      int64
}

type usageCursor struct {
	V  int    `json:"v"`
	K  string `json:"k"`
	TS string `json:"ts"`
	ID int64  `json:"id"`
}

type listResponse struct {
	Items      []usageRecord `json:"items"`
	NextCursor string        `json:"next_cursor"`
}

type usageRecord struct {
	RequestedModel    string      `json:"requested_model"`
	UpstreamModel     string      `json:"upstream_model"`
	ActualCost        string      `json:"actual_cost"`
	Tokens            usageTokens `json:"tokens"`
	Provider          string      `json:"provider,omitempty"`
	ProviderAccountID *int64      `json:"provider_account_id,omitempty"`
	LedgerID          string      `json:"ledger_id"`
	VerifyHint        verifyHint  `json:"verify_hint"`
	CreatedAt         string      `json:"created_at"`
	Status            string      `json:"status"`
	RequestID         string      `json:"request_id,omitempty"`
}

// usageTokens surfaces the per-request token breakdown already stored in
// usage_records (input/output always present; cache counts emitted only when
// non-zero). This is the genuine residual of the "relay request log" feature:
// GET /v1/me/usage already served model / cost / status / provider / verify_hint
// with keyset pagination and self-scoped relay-key auth, so we surface the token
// columns ListUsageRecords already SELECTs — instead of building a redundant
// relay_request_logs table plus a fail-open money-path settler hook.
type usageTokens struct {
	Input         int32 `json:"input"`
	Output        int32 `json:"output"`
	CacheCreation int32 `json:"cache_creation,omitempty"`
	CacheRead     int32 `json:"cache_read,omitempty"`
}

type verifyHint struct {
	LedgerID          string `json:"ledger_id,omitempty"`
	TrustVerifyPath   string `json:"trust_verify_path"`
	TrustVerifyMethod string `json:"trust_verify_method"`
	AuditVerifyPath   string `json:"audit_verify_path,omitempty"`
	AuditVerifyMethod string `json:"audit_verify_method,omitempty"`
	RequestID         string `json:"request_id,omitempty"`
	TenantScopeRef    string `json:"tenant_scope_ref,omitempty"`
}

const cursorKind = "me_usage"

func NewHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil || d.Store == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "me usage dependency unset")
			return
		}
		ident, err := d.Auth.Resolve(r.Context(), r)
		if errors.Is(err, auth.ErrAuthMisconfigured) {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "auth tables unavailable")
			return
		}
		if errors.Is(err, auth.ErrAuthBackend) {
			writeJSONError(w, http.StatusServiceUnavailable, "auth_backend_error", "auth backend transient failure")
			return
		}
		if errors.Is(err, auth.ErrForbidden) {
			writeJSONError(w, http.StatusForbidden, "forbidden", "api key policy forbids this request")
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized", "invalid bearer")
			return
		}
		q, ok := parseQuery(w, r.URL)
		if !ok {
			return
		}
		tenantID := ident.TenantID
		apiKeyID := ident.APIKeyID
		rows, err := d.Store.ListUsageRecords(r.Context(), dbbilling.ListUsageRecordsParams{
			TenantID:        &tenantID,
			FromTs:          q.FromTs,
			ToTs:            q.ToTs,
			APIKeyID:        &apiKeyID,
			HasCursor:       q.HasCursor,
			CursorCreatedAt: q.CursorCreatedAt,
			CursorID:        q.CursorID,
			PageLimit:       q.FetchLimit,
		})
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "usage_query_failed", "usage backend unavailable")
			return
		}
		next := ""
		if int32(len(rows)) > q.Limit {
			last := rows[q.Limit-1]
			next = encodeCursor(last.CreatedAt, last.ID)
			rows = rows[:q.Limit]
		}
		items := make([]usageRecord, 0, len(rows))
		for _, row := range rows {
			items = append(items, mapUsageRecord(row, ident.TenantID))
		}
		writeJSON(w, http.StatusOK, listResponse{Items: items, NextCursor: next})
	}
}

func parseQuery(w http.ResponseWriter, u *url.URL) (queryParams, bool) {
	values := u.Query()
	if strings.Contains(u.RawQuery, "cursor=") && strings.TrimSpace(values.Get("cursor")) == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid_cursor", "cursor must be an opaque base64 cursor")
		return queryParams{}, false
	}
	limit := int32(100)
	if raw := trim(values, "limit"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || n < 1 || n > 200 {
			writeJSONError(w, http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 200")
			return queryParams{}, false
		}
		limit = int32(n)
	}
	from, ok := parseQueryTime(w, trim(values, "from"), "from")
	if !ok {
		return queryParams{}, false
	}
	to, ok := parseQueryTime(w, trim(values, "to"), "to")
	if !ok {
		return queryParams{}, false
	}
	q := queryParams{FromTs: tsParam(from), ToTs: tsParam(to), Limit: limit, FetchLimit: limit + 1}
	if raw := trim(values, "cursor"); raw != "" {
		ts, id, err := decodeCursor(raw)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_cursor", "cursor must be an opaque base64 cursor")
			return queryParams{}, false
		}
		q.HasCursor, q.CursorCreatedAt, q.CursorID = true, tsParam(&ts), id
	}
	return q, true
}

func mapUsageRecord(row dbbilling.ListUsageRecordsRow, tenantID int64) usageRecord {
	ledgerID := valueString(row.AuditLedgerID)
	requestID := strings.TrimSpace(row.RequestID)
	return usageRecord{
		RequestedModel: strings.TrimSpace(row.RequestedModel),
		UpstreamModel:  valueString(row.UpstreamModel),
		ActualCost:     row.ActualCost.StringFixed(8),
		Tokens: usageTokens{
			Input:         row.TokensInput,
			Output:        row.TokensOutput,
			CacheCreation: row.CacheCreationTokens,
			CacheRead:     row.CacheReadTokens,
		},
		Provider:          valueString(row.Provider),
		ProviderAccountID: row.ProviderAccountID,
		LedgerID:          ledgerID,
		VerifyHint:        buildVerifyHint(ledgerID, requestID, tenantID),
		CreatedAt:         formatTS(row.CreatedAt),
		Status:            usageStatus(row),
		RequestID:         requestID,
	}
}

func mapGenerationUsageRecord(row dbbilling.GetUsageRecordByRequestIDRow, tenantID int64) usageRecord {
	return mapUsageRecord(dbbilling.ListUsageRecordsRow{
		RequestedModel:        row.RequestedModel,
		UpstreamModel:         row.UpstreamModel,
		ActualCost:            decimalFromNumeric(row.ActualCost),
		TokensInput:           row.TokensInput,
		TokensOutput:          row.TokensOutput,
		CacheCreationTokens:   row.CacheCreationTokens,
		CacheReadTokens:       row.CacheReadTokens,
		Provider:              row.Provider,
		ProviderAccountID:     row.ProviderAccountID,
		AuditLedgerID:         row.AuditLedgerID,
		CreatedAt:             row.CreatedAt,
		EndClass:              row.EndClass,
		PendingReconciliation: row.PendingReconciliation,
		RequestID:             row.RequestID,
	}, tenantID)
}

func decimalFromNumeric(value pgtype.Numeric) decimal.Decimal {
	if !value.Valid || value.Int == nil {
		return decimal.Zero
	}
	return decimal.NewFromBigInt(value.Int, value.Exp)
}

func buildVerifyHint(ledgerID, requestID string, tenantID int64) verifyHint {
	hint := verifyHint{
		LedgerID:          ledgerID,
		TrustVerifyPath:   "/v1/trust/verify",
		TrustVerifyMethod: http.MethodPost,
	}
	if requestID != "" {
		hint.AuditVerifyPath = "/v1/audit/verify"
		hint.AuditVerifyMethod = http.MethodGet
		hint.RequestID = requestID
		hint.TenantScopeRef = auditledger.TenantScopeRef(tenantID)
	}
	return hint
}

func usageStatus(row dbbilling.ListUsageRecordsRow) string {
	if row.PendingReconciliation {
		return "pending_reconciliation"
	}
	return strings.TrimSpace(row.EndClass)
}

func parseQueryTime(w http.ResponseWriter, raw, name string) (*time.Time, bool) {
	if raw == "" {
		return nil, true
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_"+name, name+" must be RFC3339")
		return nil, false
	}
	utc := t.UTC()
	return &utc, true
}

func encodeCursor(ts pgtype.Timestamptz, id int64) string {
	body, _ := json.Marshal(usageCursor{V: 1, K: cursorKind, TS: formatTS(ts), ID: id})
	return base64.RawURLEncoding.EncodeToString(body)
}

func decodeCursor(raw string) (time.Time, int64, error) {
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		data, err = base64.StdEncoding.DecodeString(raw)
	}
	if err != nil {
		return time.Time{}, 0, err
	}
	var c usageCursor
	if err := json.Unmarshal(data, &c); err != nil || c.V != 1 || c.K != cursorKind || c.ID <= 0 {
		return time.Time{}, 0, errors.New("bad cursor")
	}
	ts, err := time.Parse(time.RFC3339Nano, c.TS)
	return ts.UTC(), c.ID, err
}

func formatTS(ts pgtype.Timestamptz) string {
	if !ts.Valid {
		return ""
	}
	return ts.Time.UTC().Format(time.RFC3339Nano)
}

func tsParam(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

func trim(v url.Values, name string) string { return strings.TrimSpace(v.Get(name)) }

func valueString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}
