package usageanalyticshttp

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

const snapshotCacheHeader = "X-Snapshot-Cache"

var overviewSnapshotTTL = 30 * time.Second

type overviewQuery struct {
	windowLabel  string
	settledSince pgtype.Timestamptz
}

type overviewTotals struct {
	Requests      int64  `json:"requests"`
	TotalCost     string `json:"total_cost"`
	TotalTokens   int64  `json:"total_tokens"`
	ActiveUsers   int64  `json:"active_users"`
	ActiveAPIKeys int64  `json:"active_api_keys"`
	SuccessRate   string `json:"success_rate"`
}

type overviewTrendPoint struct {
	Day      string `json:"day"`
	Requests int64  `json:"requests"`
	Cost     string `json:"cost"`
}

type overviewResponse struct {
	Window string               `json:"window"`
	Totals overviewTotals       `json:"totals"`
	Trend  []overviewTrendPoint `json:"trend"`
}

// NewOverviewHandler serves GET /v1/admin/usage/overview after the caller
// wires it behind platform-admin RBAC. It is read-only and intentionally
// reports actual_cost for operator spend analysis.
func NewOverviewHandler(q Querier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if q == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "analytics dependency unset")
			return
		}
		query, ok := parseOverviewQuery(w, r.URL, time.Now().UTC())
		if !ok {
			return
		}
		value, hit, err := GetOrLoad(overviewSnapshotCacheKey(query), overviewSnapshotTTL, func() (any, error) {
			return loadOverviewResponse(r.Context(), q, query)
		})
		if err != nil {
			w.Header().Set(snapshotCacheHeader, "miss")
			writeJSONError(w, http.StatusServiceUnavailable, "analytics_query_failed", "analytics backend unavailable")
			return
		}
		response, ok := value.(overviewResponse)
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

func loadOverviewResponse(ctx context.Context, q Querier, query overviewQuery) (overviewResponse, error) {
	totalsRow, err := q.AggregateUsageOverviewTotals(ctx, query.settledSince)
	if err != nil {
		return overviewResponse{}, err
	}
	trendRows, err := q.AggregateUsageOverviewTrendByDay(ctx, query.settledSince)
	if err != nil {
		return overviewResponse{}, err
	}
	totals, err := overviewTotalsFromRow(totalsRow)
	if err != nil {
		return overviewResponse{}, err
	}
	trend, err := overviewTrendFromRows(trendRows)
	if err != nil {
		return overviewResponse{}, err
	}
	return overviewResponse{
		Window: query.windowLabel,
		Totals: totals,
		Trend:  trend,
	}, nil
}

func overviewSnapshotCacheKey(query overviewQuery) string {
	return "admin_usage_overview:v1|window=" + query.windowLabel
}

func parseOverviewQuery(w http.ResponseWriter, u *url.URL, now time.Time) (overviewQuery, bool) {
	window, label, err := parseLeaderboardWindow(u.Query().Get("window"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_window", "window must be a positive duration such as 24h, 7d, or 30d")
		return overviewQuery{}, false
	}
	return overviewQuery{
		windowLabel:  label,
		settledSince: pgtype.Timestamptz{Time: now.Add(-window), Valid: true},
	}, true
}

func overviewTotalsFromRow(row dbbilling.AggregateUsageOverviewTotalsRow) (overviewTotals, error) {
	cost, err := fixedMoneyText(row.TotalCost)
	if err != nil {
		return overviewTotals{}, err
	}
	return overviewTotals{
		Requests:      row.RequestCount,
		TotalCost:     cost,
		TotalTokens:   row.TotalTokens,
		ActiveUsers:   row.ActiveUsers,
		ActiveAPIKeys: row.ActiveApiKeys,
		SuccessRate:   successRateText(row.SuccessCount, row.RequestCount),
	}, nil
}

func overviewTrendFromRows(rows []dbbilling.AggregateUsageOverviewTrendByDayRow) ([]overviewTrendPoint, error) {
	trend := make([]overviewTrendPoint, 0, len(rows))
	for _, row := range rows {
		cost, err := fixedMoneyText(row.TotalCost)
		if err != nil {
			return nil, err
		}
		trend = append(trend, overviewTrendPoint{
			Day:      formatDay(row.Day),
			Requests: row.RequestCount,
			Cost:     cost,
		})
	}
	return trend, nil
}

func fixedMoneyText(raw string) (string, error) {
	value, err := decimal.NewFromString(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	return value.StringFixed(8), nil
}

func successRateText(successCount, requestCount int64) string {
	if requestCount <= 0 {
		return "0.0000"
	}
	return decimal.NewFromInt(successCount).Div(decimal.NewFromInt(requestCount)).StringFixed(4)
}
