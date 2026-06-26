package usageanalyticshttp

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

type ProviderAccountCountsQuerier interface {
	AggregateUsageCountsByProviderAccount(context.Context, dbbilling.AggregateUsageCountsByProviderAccountParams) ([]dbbilling.AggregateUsageCountsByProviderAccountRow, error)
}

type providerAccountCountsQuery struct {
	from     time.Time
	to       time.Time
	tenantID *int64
}

type providerAccountCount struct {
	ProviderAccountID int64  `json:"provider_account_id"`
	RequestCount      int64  `json:"request_count"`
	TotalInputTokens  int64  `json:"total_input_tokens"`
	TotalOutputTokens int64  `json:"total_output_tokens"`
	TotalCost         string `json:"total_cost"`
}

type providerAccountCountsResponse struct {
	From   string                 `json:"from"`
	To     string                 `json:"to"`
	Counts []providerAccountCount `json:"counts"`
}

// NewProviderAccountCountsHandler 提供只读的 admin 用量计数/费用聚合，
// 按 provider account 维度汇总。它绝不触碰结算、usage-record 写入、
// billing ledger 行或 quota 状态。
func NewProviderAccountCountsHandler(q ProviderAccountCountsQuerier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if q == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "analytics dependency unset")
			return
		}
		query, ok := parseProviderAccountCountsQuery(w, r.URL)
		if !ok {
			return
		}
		rows, err := q.AggregateUsageCountsByProviderAccount(r.Context(), dbbilling.AggregateUsageCountsByProviderAccountParams{
			FromTs:   pgtype.Timestamptz{Time: query.from, Valid: true},
			ToTs:     pgtype.Timestamptz{Time: query.to, Valid: true},
			TenantID: query.tenantID,
		})
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "analytics_query_failed", "analytics backend unavailable")
			return
		}
		counts := make([]providerAccountCount, 0, len(rows))
		for _, row := range rows {
			counts = append(counts, providerAccountCount{
				ProviderAccountID: row.ProviderAccountID,
				RequestCount:      row.RequestCount,
				TotalInputTokens:  row.TotalInputTokens,
				TotalOutputTokens: row.TotalOutputTokens,
				TotalCost:         strings.TrimSpace(row.TotalCost),
			})
		}
		writeJSON(w, http.StatusOK, providerAccountCountsResponse{
			From:   query.from.Format(time.RFC3339),
			To:     query.to.Format(time.RFC3339),
			Counts: counts,
		})
	}
}

func parseProviderAccountCountsQuery(w http.ResponseWriter, u *url.URL) (providerAccountCountsQuery, bool) {
	values := u.Query()
	fromRaw := strings.TrimSpace(values.Get("from"))
	toRaw := strings.TrimSpace(values.Get("to"))
	if fromRaw == "" || toRaw == "" {
		writeJSONError(w, http.StatusBadRequest, "missing_window", "from and to (RFC3339) are required")
		return providerAccountCountsQuery{}, false
	}
	from, err := time.Parse(time.RFC3339, fromRaw)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_from", "from must be RFC3339")
		return providerAccountCountsQuery{}, false
	}
	to, err := time.Parse(time.RFC3339, toRaw)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_to", "to must be RFC3339")
		return providerAccountCountsQuery{}, false
	}
	from = from.UTC()
	to = to.UTC()
	if !to.After(from) {
		writeJSONError(w, http.StatusBadRequest, "invalid_window", "to must be after from")
		return providerAccountCountsQuery{}, false
	}
	if to.Sub(from) > maxLeaderboardWindow {
		writeJSONError(w, http.StatusBadRequest, "window_too_large", "window must not exceed 90 days")
		return providerAccountCountsQuery{}, false
	}
	var tenantID *int64
	if rawTenant := strings.TrimSpace(values.Get("tenant_id")); rawTenant != "" {
		parsed, err := strconv.ParseInt(rawTenant, 10, 64)
		if err != nil || parsed <= 0 {
			writeJSONError(w, http.StatusBadRequest, "invalid_tenant_id", "tenant_id must be a positive integer")
			return providerAccountCountsQuery{}, false
		}
		tenantID = &parsed
	}
	return providerAccountCountsQuery{from: from, to: to, tenantID: tenantID}, true
}
