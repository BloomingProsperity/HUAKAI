package adminuserhttp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

// UsageStore 提供管理端用户用量明细所需的只读查询。
type UsageStore interface {
	ListUsageRecords(context.Context, dbbilling.ListUsageRecordsParams) ([]dbbilling.ListUsageRecordsRow, error)
}

type userUsageQuery struct {
	FromTs          pgtype.Timestamptz
	ToTs            pgtype.Timestamptz
	CursorCreatedAt pgtype.Timestamptz
	Limit           int32
	FetchLimit      int32
	HasCursor       bool
	CursorID        int64
	Model           *string
	Provider        *string
	Outcome         *string
}

type userUsageCursor struct {
	Version int    `json:"v"`
	Kind    string `json:"k"`
	Time    string `json:"ts"`
	ID      int64  `json:"id"`
}

type userUsageListResponse struct {
	Items      []userUsageRecord `json:"items"`
	NextCursor string            `json:"next_cursor"`
}

type userUsageRecord struct {
	RequestedModel         string          `json:"requested_model"`
	UpstreamModel          string          `json:"upstream_model"`
	ActualCost             string          `json:"actual_cost"`
	Tokens                 userUsageTokens `json:"tokens"`
	Provider               string          `json:"provider,omitempty"`
	ProviderAccountID      *int64          `json:"provider_account_id,omitempty"`
	LedgerID               string          `json:"ledger_id"`
	VerifyHint             userVerifyHint  `json:"verify_hint"`
	CreatedAt              string          `json:"created_at"`
	Status                 string          `json:"status"`
	RequestID              string          `json:"request_id,omitempty"`
	Stream                 bool            `json:"stream"`
	StreamTerminatedReason string          `json:"stream_terminated_reason,omitempty"`
	RequestedAt            string          `json:"requested_at,omitempty"`
}

type userUsageTokens struct {
	Input         int32 `json:"input"`
	Output        int32 `json:"output"`
	CacheCreation int32 `json:"cache_creation,omitempty"`
	CacheRead     int32 `json:"cache_read,omitempty"`
}

type userVerifyHint struct {
	LedgerID          string `json:"ledger_id,omitempty"`
	TrustVerifyPath   string `json:"trust_verify_path"`
	TrustVerifyMethod string `json:"trust_verify_method"`
	AuditVerifyPath   string `json:"audit_verify_path,omitempty"`
	AuditVerifyMethod string `json:"audit_verify_method,omitempty"`
	RequestID         string `json:"request_id,omitempty"`
	TenantScopeRef    string `json:"tenant_scope_ref,omitempty"`
}

const (
	userUsageCursorKind     = "admin_user_usage"
	maxUserUsageFilterRunes = 200
)

func newUserUsageHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, tenantID, ok := resolveTenantIdentity(w, r, d)
		if !ok {
			return
		}
		if d.UsageStore == nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_not_configured",
				"admin user usage dependency unset")
			return
		}
		userID, ok := pathID(w, r)
		if !ok {
			return
		}
		query, ok := parseUserUsageQuery(w, r.URL)
		if !ok {
			return
		}
		if _, err := d.Store.AdminGetUserForTenant(r.Context(), admindb.AdminGetUserForTenantParams{
			TenantID: tenantID,
			UserID:   userID,
		}); errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "admin_user_not_found", "user not found")
			return
		} else if err != nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_backend_error",
				fmt.Sprintf("get user failed: %v", err))
			return
		}

		rows, err := d.UsageStore.ListUsageRecords(r.Context(), dbbilling.ListUsageRecordsParams{
			TenantID:        &tenantID,
			FromTs:          query.FromTs,
			ToTs:            query.ToTs,
			Model:           query.Model,
			Provider:        query.Provider,
			Outcome:         query.Outcome,
			HasCursor:       query.HasCursor,
			CursorCreatedAt: query.CursorCreatedAt,
			CursorID:        query.CursorID,
			PageLimit:       query.FetchLimit,
			UserID:          &userID,
		})
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_backend_error",
				fmt.Sprintf("list user usage failed: %v", err))
			return
		}

		nextCursor := ""
		if int32(len(rows)) > query.Limit {
			last := rows[query.Limit-1]
			nextCursor = encodeUserUsageCursor(last.CreatedAt, last.ID)
			rows = rows[:query.Limit]
		}
		items := make([]userUsageRecord, 0, len(rows))
		for _, row := range rows {
			items = append(items, mapUserUsageRecord(row, tenantID))
		}
		writeJSON(w, http.StatusOK, userUsageListResponse{Items: items, NextCursor: nextCursor})
	}
}

func parseUserUsageQuery(w http.ResponseWriter, u *url.URL) (userUsageQuery, bool) {
	values := u.Query()
	if strings.Contains(u.RawQuery, "cursor=") && strings.TrimSpace(values.Get("cursor")) == "" {
		writeError(w, http.StatusBadRequest, "invalid_cursor", "cursor must be an opaque base64 cursor")
		return userUsageQuery{}, false
	}

	limit := int32(100)
	if raw := trimUserUsageValue(values, "limit"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || parsed < 1 || parsed > 200 {
			writeError(w, http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 200")
			return userUsageQuery{}, false
		}
		limit = int32(parsed)
	}
	from, ok := parseUserUsageTime(w, trimUserUsageValue(values, "from"), "from")
	if !ok {
		return userUsageQuery{}, false
	}
	to, ok := parseUserUsageTime(w, trimUserUsageValue(values, "to"), "to")
	if !ok {
		return userUsageQuery{}, false
	}
	query := userUsageQuery{
		FromTs:     userUsageTimestampParam(from),
		ToTs:       userUsageTimestampParam(to),
		Limit:      limit,
		FetchLimit: limit + 1,
	}
	if raw := trimUserUsageValue(values, "cursor"); raw != "" {
		cursorTime, cursorID, err := decodeUserUsageCursor(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_cursor", "cursor must be an opaque base64 cursor")
			return userUsageQuery{}, false
		}
		query.HasCursor = true
		query.CursorCreatedAt = userUsageTimestampParam(&cursorTime)
		query.CursorID = cursorID
	}
	if raw := trimUserUsageValue(values, "model"); raw != "" {
		if utf8.RuneCountInString(raw) > maxUserUsageFilterRunes {
			writeError(w, http.StatusBadRequest, "invalid_model", "model must not exceed 200 characters")
			return userUsageQuery{}, false
		}
		query.Model = &raw
	}
	if raw := trimUserUsageValue(values, "provider"); raw != "" {
		if utf8.RuneCountInString(raw) > maxUserUsageFilterRunes {
			writeError(w, http.StatusBadRequest, "invalid_provider", "provider must not exceed 200 characters")
			return userUsageQuery{}, false
		}
		query.Provider = &raw
	}
	switch raw := trimUserUsageValue(values, "status"); raw {
	case "":
	case "success", "error":
		query.Outcome = &raw
	default:
		writeError(w, http.StatusBadRequest, "invalid_status", "status must be success or error")
		return userUsageQuery{}, false
	}
	return query, true
}

func mapUserUsageRecord(row dbbilling.ListUsageRecordsRow, tenantID int64) userUsageRecord {
	ledgerID := userUsageString(row.AuditLedgerID)
	requestID := strings.TrimSpace(row.RequestID)
	return userUsageRecord{
		RequestedModel: strings.TrimSpace(row.RequestedModel),
		UpstreamModel:  userUsageString(row.UpstreamModel),
		ActualCost:     row.ActualCost.StringFixed(8),
		Tokens: userUsageTokens{
			Input:         row.TokensInput,
			Output:        row.TokensOutput,
			CacheCreation: row.CacheCreationTokens,
			CacheRead:     row.CacheReadTokens,
		},
		Provider:               userUsageString(row.Provider),
		ProviderAccountID:      row.ProviderAccountID,
		LedgerID:               ledgerID,
		VerifyHint:             buildUserVerifyHint(ledgerID, requestID, tenantID),
		CreatedAt:              formatUserUsageTimestamp(row.CreatedAt),
		Status:                 userUsageStatus(row),
		RequestID:              requestID,
		Stream:                 row.Stream,
		StreamTerminatedReason: userUsageString(row.StreamTerminatedReason),
		RequestedAt:            formatUserUsageTimestamp(row.RequestedAt),
	}
}

func buildUserVerifyHint(ledgerID, requestID string, tenantID int64) userVerifyHint {
	hint := userVerifyHint{
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

func userUsageStatus(row dbbilling.ListUsageRecordsRow) string {
	if row.PendingReconciliation {
		return "pending_reconciliation"
	}
	return strings.TrimSpace(row.EndClass)
}

func parseUserUsageTime(w http.ResponseWriter, raw, name string) (*time.Time, bool) {
	if raw == "" {
		return nil, true
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_"+name, name+" must be RFC3339")
		return nil, false
	}
	utc := parsed.UTC()
	return &utc, true
}

func encodeUserUsageCursor(createdAt pgtype.Timestamptz, id int64) string {
	body, _ := json.Marshal(userUsageCursor{
		Version: 1,
		Kind:    userUsageCursorKind,
		Time:    formatUserUsageTimestamp(createdAt),
		ID:      id,
	})
	return base64.RawURLEncoding.EncodeToString(body)
}

func decodeUserUsageCursor(raw string) (time.Time, int64, error) {
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		data, err = base64.StdEncoding.DecodeString(raw)
	}
	if err != nil {
		return time.Time{}, 0, err
	}
	var cursor userUsageCursor
	if err := json.Unmarshal(data, &cursor); err != nil || cursor.Version != 1 || cursor.Kind != userUsageCursorKind || cursor.ID <= 0 {
		return time.Time{}, 0, errors.New("bad cursor")
	}
	createdAt, err := time.Parse(time.RFC3339Nano, cursor.Time)
	return createdAt.UTC(), cursor.ID, err
}

func formatUserUsageTimestamp(value pgtype.Timestamptz) string {
	if !value.Valid {
		return ""
	}
	return value.Time.UTC().Format(time.RFC3339Nano)
}

func userUsageTimestampParam(value *time.Time) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *value, Valid: true}
}

func trimUserUsageValue(values url.Values, name string) string {
	return strings.TrimSpace(values.Get(name))
}

func userUsageString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}
