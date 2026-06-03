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
	defaultLeaderboardLimit  = 20
	maxLeaderboardLimit      = 100
	maxLeaderboardWindow     = 90 * 24 * time.Hour
	maxLeaderboardWindowText = "90d"
)

var errInvalidDuration = errors.New("invalid duration")

type Querier interface {
	AggregateUsageLeaderboardByUser(context.Context, dbbilling.AggregateUsageLeaderboardByUserParams) ([]dbbilling.AggregateUsageLeaderboardByUserRow, error)
	AggregateUsageLeaderboardByModel(context.Context, dbbilling.AggregateUsageLeaderboardByModelParams) ([]dbbilling.AggregateUsageLeaderboardByModelRow, error)
	AggregateUsageLeaderboardByProviderAccount(context.Context, dbbilling.AggregateUsageLeaderboardByProviderAccountParams) ([]dbbilling.AggregateUsageLeaderboardByProviderAccountRow, error)
}

type leaderboardQuery struct {
	by           string
	windowLabel  string
	settledSince pgtype.Timestamptz
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

// NewLeaderboardHandler serves GET /v1/admin/usage/leaderboard after the
// caller wires it behind platform-admin RBAC. It is read-only and intentionally
// reports actual_cost for operator spend analysis.
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
		rows, err := fetchLeaderboardRows(r.Context(), q, query)
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "analytics_query_failed", "analytics backend unavailable")
			return
		}
		entries, err := leaderboardEntries(rows)
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "analytics_query_failed", "analytics backend unavailable")
			return
		}
		writeJSON(w, http.StatusOK, leaderboardResponse{
			Window:  query.windowLabel,
			By:      query.by,
			Entries: entries,
		})
	}
}

func parseLeaderboardQuery(w http.ResponseWriter, u *url.URL, now time.Time) (leaderboardQuery, bool) {
	values := u.Query()
	by := strings.TrimSpace(values.Get("by"))
	if by == "" {
		by = defaultLeaderboardBy
	}
	if by != leaderboardByUser && by != leaderboardByModel && by != leaderboardByProvider {
		writeJSONError(w, http.StatusBadRequest, "invalid_by", "by must be user, model, or provider_account")
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
	return leaderboardQuery{
		by:           by,
		windowLabel:  label,
		settledSince: pgtype.Timestamptz{Time: now.Add(-window), Valid: true},
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
