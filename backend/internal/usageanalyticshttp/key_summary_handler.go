package usageanalyticshttp

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/userkey"
)

type KeyUsageSummaryStore interface {
	AggregateMyUsageTotals(context.Context, dbbilling.AggregateMyUsageTotalsParams) (dbbilling.AggregateMyUsageTotalsRow, error)
}

type KeyUsageSummaryKeyService interface {
	Get(ctx context.Context, tenantID, userID, apiKeyID int64) (userkey.KeyDescriptor, error)
}

type KeyUsageSummaryDeps struct {
	Store KeyUsageSummaryStore
	Keys  KeyUsageSummaryKeyService
}

type keyUsageSummaryResponse struct {
	APIKeyID                 int64   `json:"api_key_id"`
	TotalCost                string  `json:"total_cost"`
	TotalTokensInput         int64   `json:"total_tokens_input"`
	TotalTokensOutput        int64   `json:"total_tokens_output"`
	TotalCacheReadTokens     int64   `json:"total_cache_read_tokens"`
	TotalCacheCreationTokens int64   `json:"total_cache_creation_tokens"`
	RequestCount             int64   `json:"request_count"`
	From                     *string `json:"from"`
	To                       *string `json:"to"`
}

type optionalUsageWindow struct {
	from *time.Time
	to   *time.Time
}

// NewKeyUsageSummaryHandler serves GET /v1/me/keys/{id}/usage-summary for a
// session-authenticated user. Ownership is checked through userkey.Service.Get
// before any usage aggregation, and non-owned/missing keys collapse to 404.
func NewKeyUsageSummaryHandler(d KeyUsageSummaryDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil || d.Keys == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "key usage summary dependency unset")
			return
		}
		ident, ok := sessionauth.SessionFromContext(r.Context())
		if !ok || ident.TenantID <= 0 || ident.UserID <= 0 {
			writeJSONError(w, http.StatusUnauthorized, "session_required", "session bearer token is required")
			return
		}
		apiKeyID, ok := parseSummaryAPIKeyID(w, r)
		if !ok {
			return
		}
		window, ok := parseOptionalUsageWindow(w, r.URL)
		if !ok {
			return
		}
		if _, err := d.Keys.Get(r.Context(), ident.TenantID, ident.UserID, apiKeyID); err != nil {
			writeKeyUsageOwnershipError(w, err)
			return
		}
		row, err := d.Store.AggregateMyUsageTotals(r.Context(), dbbilling.AggregateMyUsageTotalsParams{
			TenantID: ident.TenantID,
			APIKeyID: apiKeyID,
			FromTs:   optionalUsageTS(window.from),
			ToTs:     optionalUsageTS(window.to),
		})
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "usage_summary_query_failed", "usage summary backend unavailable")
			return
		}
		writeJSON(w, http.StatusOK, keyUsageSummaryResponse{
			APIKeyID:                 apiKeyID,
			TotalCost:                strings.TrimSpace(row.TotalCost),
			TotalTokensInput:         row.TotalTokensInput,
			TotalTokensOutput:        row.TotalTokensOutput,
			TotalCacheReadTokens:     row.TotalCacheReadTokens,
			TotalCacheCreationTokens: row.TotalCacheCreationTokens,
			RequestCount:             row.RequestCount,
			From:                     optionalUsageTimeString(window.from),
			To:                       optionalUsageTimeString(window.to),
		})
	}
}

func parseSummaryAPIKeyID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := strings.TrimSpace(chi.URLParam(r, "id"))
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid_api_key_id", "api_key_id must be a positive int64")
		return 0, false
	}
	return id, true
}

func parseOptionalUsageWindow(w http.ResponseWriter, u *url.URL) (optionalUsageWindow, bool) {
	from, ok := parseOptionalUsageTime(w, strings.TrimSpace(u.Query().Get("from")), "from")
	if !ok {
		return optionalUsageWindow{}, false
	}
	to, ok := parseOptionalUsageTime(w, strings.TrimSpace(u.Query().Get("to")), "to")
	if !ok {
		return optionalUsageWindow{}, false
	}
	if from != nil && to != nil && !to.After(*from) {
		writeJSONError(w, http.StatusBadRequest, "invalid_window", "to must be after from")
		return optionalUsageWindow{}, false
	}
	return optionalUsageWindow{from: from, to: to}, true
}

func parseOptionalUsageTime(w http.ResponseWriter, raw, name string) (*time.Time, bool) {
	if raw == "" {
		return nil, true
	}
	ts, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_"+name, name+" must be RFC3339")
		return nil, false
	}
	utc := ts.UTC()
	return &utc, true
}

func optionalUsageTS(ts *time.Time) pgtype.Timestamptz {
	if ts == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *ts, Valid: true}
}

func optionalUsageTimeString(ts *time.Time) *string {
	if ts == nil {
		return nil
	}
	out := ts.UTC().Format(time.RFC3339)
	return &out
}

func writeKeyUsageOwnershipError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, userkey.ErrNotFound):
		writeJSONError(w, http.StatusNotFound, "api_key_not_found", "api_key not found")
	case errors.Is(err, userkey.ErrServiceMisconfig):
		writeJSONError(w, http.StatusServiceUnavailable, "userkey_service_unavailable", "user api key service unavailable")
	default:
		writeJSONError(w, http.StatusServiceUnavailable, "userkey_backend_error", "user api key backend transient failure")
	}
}
