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
	"github.com/BloomingProsperity/HUAKAI/internal/snapshotcache"
)

const (
	defaultPerformanceBy = "model"
	performanceByModel   = "model"
)

var performanceSnapshotTTL = 30 * time.Second

type performanceQuery struct {
	by           string
	windowLabel  string
	settledSince pgtype.Timestamptz
	limit        int32
}

type performanceEntry struct {
	Rank         int    `json:"rank"`
	Key          string `json:"key"`
	AvgTTFTMS    string `json:"avg_ttft_ms"`
	AvgTPS       string `json:"avg_tps"`
	RequestCount int64  `json:"request_count"`
	ErrorRate    string `json:"error_rate"`
}

type performanceResponse struct {
	Window  string             `json:"window"`
	By      string             `json:"by"`
	Entries []performanceEntry `json:"entries"`
}

type performanceRow struct {
	key          string
	avgTTFTMS    string
	avgTPS       string
	requestCount int64
	errorCount   int64
}

// NewPerformanceHandler serves GET /v1/admin/usage/performance after the
// caller wires it behind platform-admin RBAC. It is read-only and returns no
// cost fields: only latency, throughput, request count, and error rate.
func NewPerformanceHandler(q Querier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if q == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "analytics dependency unset")
			return
		}
		query, ok := parsePerformanceQuery(w, r.URL, time.Now().UTC())
		if !ok {
			return
		}
		value, hit, err := snapshotcache.GetOrLoad(performanceSnapshotCacheKey(query), performanceSnapshotTTL, func() (any, error) {
			return loadPerformanceResponse(r.Context(), q, query)
		})
		if err != nil {
			w.Header().Set(snapshotCacheHeader, "miss")
			writeJSONError(w, http.StatusServiceUnavailable, "analytics_query_failed", "analytics backend unavailable")
			return
		}
		response, ok := value.(performanceResponse)
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

func loadPerformanceResponse(ctx context.Context, q Querier, query performanceQuery) (performanceResponse, error) {
	rows, err := fetchPerformanceRows(ctx, q, query)
	if err != nil {
		return performanceResponse{}, err
	}
	entries, err := performanceEntries(rows)
	if err != nil {
		return performanceResponse{}, err
	}
	return performanceResponse{
		Window:  query.windowLabel,
		By:      query.by,
		Entries: entries,
	}, nil
}

func performanceSnapshotCacheKey(query performanceQuery) string {
	return "admin_usage_performance:v1|by=" + query.by + "|window=" + query.windowLabel + "|limit=" + strconv.Itoa(int(query.limit))
}

func parsePerformanceQuery(w http.ResponseWriter, u *url.URL, now time.Time) (performanceQuery, bool) {
	values := u.Query()
	by := strings.TrimSpace(values.Get("by"))
	if by == "" {
		by = defaultPerformanceBy
	}
	if by != performanceByModel && by != leaderboardByProvider {
		writeJSONError(w, http.StatusBadRequest, "invalid_by", "by must be model or provider_account")
		return performanceQuery{}, false
	}
	window, label, err := parseLeaderboardWindow(values.Get("window"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_window", "window must be a positive duration such as 24h, 7d, or 30d")
		return performanceQuery{}, false
	}
	limit, err := parseLeaderboardLimit(values.Get("limit"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_limit", "limit must be a positive integer")
		return performanceQuery{}, false
	}
	return performanceQuery{
		by:           by,
		windowLabel:  label,
		settledSince: pgtype.Timestamptz{Time: now.Add(-window), Valid: true},
		limit:        int32(limit),
	}, true
}

func fetchPerformanceRows(ctx context.Context, q Querier, query performanceQuery) ([]performanceRow, error) {
	switch query.by {
	case performanceByModel:
		rows, err := q.AggregateUsagePerformanceByModel(ctx, dbbilling.AggregateUsagePerformanceByModelParams{
			SettledSince: query.settledSince,
			RowLimit:     query.limit,
		})
		return modelPerformanceRows(rows), err
	case leaderboardByProvider:
		rows, err := q.AggregateUsagePerformanceByProviderAccount(ctx, dbbilling.AggregateUsagePerformanceByProviderAccountParams{
			SettledSince: query.settledSince,
			RowLimit:     query.limit,
		})
		return providerPerformanceRows(rows), err
	default:
		return nil, errors.New("unsupported performance dimension")
	}
}

func modelPerformanceRows(rows []dbbilling.AggregateUsagePerformanceByModelRow) []performanceRow {
	out := make([]performanceRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, performanceRow{
			key:          row.Key,
			avgTTFTMS:    row.AvgTtftMs,
			avgTPS:       row.AvgTps,
			requestCount: row.RequestCount,
			errorCount:   row.ErrorCount,
		})
	}
	return out
}

func providerPerformanceRows(rows []dbbilling.AggregateUsagePerformanceByProviderAccountRow) []performanceRow {
	out := make([]performanceRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, performanceRow{
			key:          row.Key,
			avgTTFTMS:    row.AvgTtftMs,
			avgTPS:       row.AvgTps,
			requestCount: row.RequestCount,
			errorCount:   row.ErrorCount,
		})
	}
	return out
}

func performanceEntries(rows []performanceRow) ([]performanceEntry, error) {
	entries := make([]performanceEntry, 0, len(rows))
	for i, row := range rows {
		avgTTFT, err := fixedDecimalText(row.avgTTFTMS)
		if err != nil {
			return nil, err
		}
		avgTPS, err := fixedDecimalText(row.avgTPS)
		if err != nil {
			return nil, err
		}
		entries = append(entries, performanceEntry{
			Rank:         i + 1,
			Key:          strings.TrimSpace(row.key),
			AvgTTFTMS:    avgTTFT,
			AvgTPS:       avgTPS,
			RequestCount: row.requestCount,
			ErrorRate:    errorRateText(row.errorCount, row.requestCount),
		})
	}
	return entries, nil
}

func fixedDecimalText(raw string) (string, error) {
	value, err := decimal.NewFromString(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	return value.StringFixed(4), nil
}

func errorRateText(errorCount, requestCount int64) string {
	if requestCount <= 0 {
		return "0.0000"
	}
	return decimal.NewFromInt(errorCount).Div(decimal.NewFromInt(requestCount)).StringFixed(4)
}
