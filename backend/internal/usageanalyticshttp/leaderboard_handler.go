package usageanalyticshttp

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

const (
	defaultLeaderboardBy     = "user"
	leaderboardByUser        = "user"
	leaderboardByModel       = "model"
	leaderboardByProvider    = "provider_account"
	leaderboardByApiKey      = "api_key"
	defaultLeaderboardLimit  = 20
	maxLeaderboardLimit      = 100
	maxLeaderboardWindow     = 90 * 24 * time.Hour
	maxLeaderboardWindowText = "90d"
)

var errInvalidDuration = errors.New("invalid duration")

var leaderboardSnapshotTTL = 30 * time.Second

type Querier interface {
	AggregateUsageLeaderboardByUser(context.Context, dbbilling.AggregateUsageLeaderboardByUserParams) ([]dbbilling.AggregateUsageLeaderboardByUserRow, error)
	AggregateUsageLeaderboardByModel(context.Context, dbbilling.AggregateUsageLeaderboardByModelParams) ([]dbbilling.AggregateUsageLeaderboardByModelRow, error)
	AggregateUsageLeaderboardByProviderAccount(context.Context, dbbilling.AggregateUsageLeaderboardByProviderAccountParams) ([]dbbilling.AggregateUsageLeaderboardByProviderAccountRow, error)
	AggregateUsageLeaderboardByApiKey(context.Context, dbbilling.AggregateUsageLeaderboardByApiKeyParams) ([]dbbilling.AggregateUsageLeaderboardByApiKeyRow, error)
	AggregateUsagePerformanceByModel(context.Context, dbbilling.AggregateUsagePerformanceByModelParams) ([]dbbilling.AggregateUsagePerformanceByModelRow, error)
	AggregateUsagePerformanceByProviderAccount(context.Context, dbbilling.AggregateUsagePerformanceByProviderAccountParams) ([]dbbilling.AggregateUsagePerformanceByProviderAccountRow, error)
	AggregateUsagePerformanceSummary(context.Context, dbbilling.AggregateUsagePerformanceSummaryParams) (dbbilling.AggregateUsagePerformanceSummaryRow, error)
	AggregateUsageLatencyPercentiles(context.Context, dbbilling.AggregateUsageLatencyPercentilesParams) (dbbilling.AggregateUsageLatencyPercentilesRow, error)
	AggregateUsagePerformanceByModelBucketed(context.Context, dbbilling.AggregateUsagePerformanceByModelBucketedParams) ([]dbbilling.AggregateUsagePerformanceByModelBucketedRow, error)
	AggregateUsageOverviewTotals(context.Context, pgtype.Timestamptz) (dbbilling.AggregateUsageOverviewTotalsRow, error)
	AggregateUsageOverviewTrendByDay(context.Context, pgtype.Timestamptz) ([]dbbilling.AggregateUsageOverviewTrendByDayRow, error)
}

type leaderboardQuery struct {
	by           string
	windowLabel  string
	settledSince pgtype.Timestamptz
	tenantID     int64
	limit        int32
}

type leaderboardEntry struct {
	Rank         int    `json:"rank"`
	Key          string `json:"key"`
	TotalCost    string `json:"total_cost"`
	TotalTokens  int64  `json:"total_tokens"`
	RequestCount int64  `json:"request_count"`
}

type leaderboardResponse struct {
	Window  string             `json:"window"`
	By      string             `json:"by"`
	Entries []leaderboardEntry `json:"entries"`
}

type leaderboardRow struct {
	key          string
	totalCost    string
	totalTokens  int64
	requestCount int64
}

// NewLeaderboardHandler 在调用方将其接在 platform-admin RBAC 之后后,
// 服务 GET /v1/admin/usage/leaderboard。它是只读的,并刻意报告 actual_cost
// 以供运维做开销分析。
func NewLeaderboardHandler(q Querier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if q == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "analytics dependency unset")
			return
		}
		query, ok := parseLeaderboardQuery(w, r.URL, time.Now().UTC())
		if !ok {
			return
		}
		value, hit, err := GetOrLoad(leaderboardSnapshotCacheKey(query), leaderboardSnapshotTTL, func() (any, error) {
			return loadLeaderboardResponse(r.Context(), q, query)
		})
		if err != nil {
			w.Header().Set(snapshotCacheHeader, "miss")
			writeJSONError(w, http.StatusServiceUnavailable, "analytics_query_failed", "analytics backend unavailable")
			return
		}
		response, ok := value.(leaderboardResponse)
		if !ok {
			w.Header().Set(snapshotCacheHeader, "miss")
			writeJSONError(w, http.StatusServiceUnavailable, "analytics_query_failed", "analytics backend unavailable")
			return
		}
		if hit {
			w.Header().Set(snapshotCacheHeader, "hit")
		} else {
			w.Header().Set(snapshotCacheHeader, "miss")
		}
		writeJSON(w, http.StatusOK, response)
	}
}

func loadLeaderboardResponse(ctx context.Context, q Querier, query leaderboardQuery) (leaderboardResponse, error) {
	rows, err := fetchLeaderboardRows(ctx, q, query)
	if err != nil {
		return leaderboardResponse{}, err
	}
	entries, err := leaderboardEntries(rows)
	if err != nil {
		return leaderboardResponse{}, err
	}
	return leaderboardResponse{
		Window:  query.windowLabel,
		By:      query.by,
		Entries: entries,
	}, nil
}

func leaderboardSnapshotCacheKey(query leaderboardQuery) string {
	return "admin_usage_leaderboard:v1|by=" + query.by + "|window=" + query.windowLabel + "|tenant=" + strconv.FormatInt(query.tenantID, 10) + "|limit=" + strconv.Itoa(int(query.limit))
}

func parseLeaderboardQuery(w http.ResponseWriter, u *url.URL, now time.Time) (leaderboardQuery, bool) {
	values := u.Query()
	by := strings.TrimSpace(values.Get("by"))
	if by == "" {
		by = defaultLeaderboardBy
	}
	if by != leaderboardByUser && by != leaderboardByModel && by != leaderboardByProvider && by != leaderboardByApiKey {
		writeJSONError(w, http.StatusBadRequest, "invalid_by", "by must be user, model, provider_account, or api_key")
		return leaderboardQuery{}, false
	}
	window, label, err := parseLeaderboardWindow(values.Get("window"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_window", "window must be a positive duration such as 24h, 7d, or 30d")
		return leaderboardQuery{}, false
	}
	limit, err := parseLeaderboardLimit(values.Get("limit"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_limit", "limit must be a positive integer")
		return leaderboardQuery{}, false
	}
	var tenantID int64
	if by == leaderboardByApiKey {
		tenantID, err = parseLeaderboardTenantID(values.Get("tenant_id"))
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_tenant_id", "tenant_id must be a positive integer")
			return leaderboardQuery{}, false
		}
	}
	return leaderboardQuery{
		by:           by,
		windowLabel:  label,
		settledSince: pgtype.Timestamptz{Time: now.Add(-window), Valid: true},
		tenantID:     tenantID,
		limit:        int32(limit),
	}, true
}

func parseLeaderboardWindow(raw string) (time.Duration, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, "", errInvalidDuration
	}
	parseable := raw
	if strings.HasSuffix(raw, "d") {
		days, err := strconv.ParseInt(strings.TrimSuffix(raw, "d"), 10, 32)
		if err != nil || days <= 0 {
			return 0, "", errInvalidDuration
		}
		if days > 90 {
			return maxLeaderboardWindow, maxLeaderboardWindowText, nil
		}
		parseable = strconv.FormatInt(days*24, 10) + "h"
	}
	window, err := time.ParseDuration(parseable)
	if err != nil || window <= 0 {
		return 0, "", errInvalidDuration
	}
	if window > maxLeaderboardWindow {
		return maxLeaderboardWindow, maxLeaderboardWindowText, nil
	}
	return window, raw, nil
}

func parseLeaderboardLimit(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultLeaderboardLimit, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 {
		return 0, errors.New("invalid limit")
	}
	if limit > maxLeaderboardLimit {
		return maxLeaderboardLimit, nil
	}
	return limit, nil
}

func parseLeaderboardTenantID(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	tenantID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || tenantID <= 0 {
		return 0, errors.New("invalid tenant_id")
	}
	return tenantID, nil
}

func fetchLeaderboardRows(ctx context.Context, q Querier, query leaderboardQuery) ([]leaderboardRow, error) {
	switch query.by {
	case leaderboardByUser:
		rows, err := q.AggregateUsageLeaderboardByUser(ctx, dbbilling.AggregateUsageLeaderboardByUserParams{
			SettledSince: query.settledSince,
			RowLimit:     query.limit,
		})
		return userLeaderboardRows(rows), err
	case leaderboardByModel:
		rows, err := q.AggregateUsageLeaderboardByModel(ctx, dbbilling.AggregateUsageLeaderboardByModelParams{
			SettledSince: query.settledSince,
			RowLimit:     query.limit,
		})
		return modelLeaderboardRows(rows), err
	case leaderboardByProvider:
		rows, err := q.AggregateUsageLeaderboardByProviderAccount(ctx, dbbilling.AggregateUsageLeaderboardByProviderAccountParams{
			SettledSince: query.settledSince,
			RowLimit:     query.limit,
		})
		return providerLeaderboardRows(rows), err
	case leaderboardByApiKey:
		rows, err := q.AggregateUsageLeaderboardByApiKey(ctx, dbbilling.AggregateUsageLeaderboardByApiKeyParams{
			SettledSince: query.settledSince,
			TenantID:     query.tenantID,
			RowLimit:     query.limit,
		})
		return apiKeyLeaderboardRows(rows), err
	default:
		return nil, errors.New("unsupported leaderboard dimension")
	}
}

func userLeaderboardRows(rows []dbbilling.AggregateUsageLeaderboardByUserRow) []leaderboardRow {
	out := make([]leaderboardRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, leaderboardRow{key: row.Key, totalCost: row.TotalCost, totalTokens: row.TotalTokens, requestCount: row.RequestCount})
	}
	return out
}

func modelLeaderboardRows(rows []dbbilling.AggregateUsageLeaderboardByModelRow) []leaderboardRow {
	out := make([]leaderboardRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, leaderboardRow{key: row.Key, totalCost: row.TotalCost, totalTokens: row.TotalTokens, requestCount: row.RequestCount})
	}
	return out
}

func providerLeaderboardRows(rows []dbbilling.AggregateUsageLeaderboardByProviderAccountRow) []leaderboardRow {
	out := make([]leaderboardRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, leaderboardRow{key: row.Key, totalCost: row.TotalCost, totalTokens: row.TotalTokens, requestCount: row.RequestCount})
	}
	return out
}

func apiKeyLeaderboardRows(rows []dbbilling.AggregateUsageLeaderboardByApiKeyRow) []leaderboardRow {
	out := make([]leaderboardRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, leaderboardRow{key: row.Key, totalCost: row.TotalCost, totalTokens: row.TotalTokens, requestCount: row.RequestCount})
	}
	return out
}

func leaderboardEntries(rows []leaderboardRow) ([]leaderboardEntry, error) {
	entries := make([]leaderboardEntry, 0, len(rows))
	for i, row := range rows {
		cost, err := decimal.NewFromString(strings.TrimSpace(row.totalCost))
		if err != nil {
			return nil, err
		}
		entries = append(entries, leaderboardEntry{
			Rank:         i + 1,
			Key:          strings.TrimSpace(row.key),
			TotalCost:    cost.StringFixed(8),
			TotalTokens:  row.totalTokens,
			RequestCount: row.requestCount,
		})
	}
	return entries, nil
}
