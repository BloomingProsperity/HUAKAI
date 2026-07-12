package adminhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

const (
	providerAccountRecentRequestsDefaultLimit = 20
	providerAccountRecentRequestsMaxLimit     = 100
)

type ProviderAccountRecentRequestsDeps struct {
	Auth     providerAccountHealthAuth
	Accounts providerAccountHealthStore
	Requests providerAccountRecentRequestsStore
}

type providerAccountRecentRequestsStore interface {
	ListProviderAccountRecentRequests(context.Context, dbbilling.ListProviderAccountRecentRequestsParams) ([]dbbilling.ListProviderAccountRecentRequestsRow, error)
}

type providerAccountRecentRequestsResponse struct {
	Items  []providerAccountRecentRequestItem `json:"items"`
	Source string                             `json:"source"`
}

type providerAccountRecentRequestItem struct {
	At            string  `json:"at"`
	Model         string  `json:"model"`
	UpstreamModel *string `json:"upstream_model"`
	Status        string  `json:"status"`
	LatencyMS     int64   `json:"latency_ms"`
	TTFTMS        *int64  `json:"ttft_ms"`
	TokensIn      int32   `json:"tokens_in"`
	TokensOut     int32   `json:"tokens_out"`
	Stream        bool    `json:"stream"`
	AttemptSeq    int32   `json:"attempt_seq"`
}

func MountProviderAccountRecentRequestsRoutes(r chi.Router, d ProviderAccountRecentRequestsDeps) {
	r.Get("/{id}/recent-requests", newProviderAccountRecentRequestsHandler(d))
}

func newProviderAccountRecentRequestsHandler(d ProviderAccountRecentRequestsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil || d.Accounts == nil || d.Requests == nil {
			writeError(w, http.StatusServiceUnavailable, "gateway_not_configured", "provider account recent requests dependency unset")
			return
		}
		_, tenantID, ok := resolveProviderAccountHealthTenant(w, r, ProviderAccountHealthDeps{
			Auth:  d.Auth,
			Store: d.Accounts,
		})
		if !ok {
			return
		}
		accountID, ok := parseProviderAccountHealthID(w, r)
		if !ok {
			return
		}
		limit, ok := parseProviderAccountRecentRequestsLimit(w, r)
		if !ok {
			return
		}

		// 先按 tenant+account 确认资源存在，避免把跨租户账号与合法空历史混为一谈。
		if _, err := d.Accounts.GetAdminProviderAccountHealth(r.Context(), admindb.GetAdminProviderAccountHealthParams{
			TenantID: tenantID,
			ID:       accountID,
		}); err != nil {
			writeProviderAccountRecentRequestsAccountError(w, err)
			return
		}

		rows, err := d.Requests.ListProviderAccountRecentRequests(r.Context(), dbbilling.ListProviderAccountRecentRequestsParams{
			ProviderAccountID: accountID,
			TenantID:          tenantID,
			RowLimit:          limit,
		})
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "provider_account_recent_requests_unavailable", "provider account recent requests are unavailable")
			return
		}
		items := make([]providerAccountRecentRequestItem, 0, len(rows))
		for _, row := range rows {
			items = append(items, providerAccountRecentRequestFromRow(row))
		}
		writeProviderAccountRecentRequestsJSON(w, providerAccountRecentRequestsResponse{
			Items:  items,
			Source: "settled_usage_records",
		})
	}
}

func parseProviderAccountRecentRequestsLimit(w http.ResponseWriter, r *http.Request) (int32, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return providerAccountRecentRequestsDefaultLimit, true
	}
	value, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || value <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_limit", "limit must be a positive integer")
		return 0, false
	}
	if value > providerAccountRecentRequestsMaxLimit {
		value = providerAccountRecentRequestsMaxLimit
	}
	return int32(value), true
}

func writeProviderAccountRecentRequestsAccountError(w http.ResponseWriter, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "provider_account_not_found", "provider account not found")
		return
	}
	writeError(w, http.StatusServiceUnavailable, "provider_account_recent_requests_unavailable", "provider account recent requests are unavailable")
}

func providerAccountRecentRequestFromRow(row dbbilling.ListProviderAccountRecentRequestsRow) providerAccountRecentRequestItem {
	return providerAccountRecentRequestItem{
		At:            formatProviderAccountRecentRequestTime(row.RequestedAt.Time, row.RequestedAt.Valid),
		Model:         row.RequestedModel,
		UpstreamModel: row.UpstreamModel,
		Status:        providerAccountRecentRequestStatus(row.EndClass),
		LatencyMS:     providerAccountRecentRequestDurationMS(row.RequestedAt.Time, row.RequestedAt.Valid, row.SettledAt.Time, row.SettledAt.Valid),
		TTFTMS:        providerAccountRecentRequestOptionalDurationMS(row.UpstreamRequestAt.Time, row.UpstreamRequestAt.Valid, row.FirstByteAt.Time, row.FirstByteAt.Valid),
		TokensIn:      row.TokensInput,
		TokensOut:     row.TokensOutput,
		Stream:        row.Stream,
		AttemptSeq:    row.AttemptSeq,
	}
}

func providerAccountRecentRequestStatus(endClass string) string {
	if endClass == "stream_end_graceful" || endClass == "non_streaming" {
		return "success"
	}
	return "error"
}

func formatProviderAccountRecentRequestTime(value time.Time, valid bool) string {
	if !valid {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func providerAccountRecentRequestDurationMS(start time.Time, startValid bool, end time.Time, endValid bool) int64 {
	if !startValid || !endValid {
		return 0
	}
	return end.Sub(start).Milliseconds()
}

func providerAccountRecentRequestOptionalDurationMS(start time.Time, startValid bool, end time.Time, endValid bool) *int64 {
	if !startValid || !endValid {
		return nil
	}
	value := end.Sub(start).Milliseconds()
	return &value
}

func writeProviderAccountRecentRequestsJSON(w http.ResponseWriter, body providerAccountRecentRequestsResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(body)
}
