package publicrankinghttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/usageanalyticshttp"
)

const (
	defaultPublicRankingsLimit = 20
	maxPublicRankingsLimit     = 100
	publicRankingsFetchLimit   = int32(1<<31 - 1)
	publicRankingsSnapshotTTL  = 30 * time.Second
	publicRankingsScope        = "platform"
	publicRankingsMetric       = "request_count"
	snapshotCacheHeader        = "X-Snapshot-Cache"
)

var errInvalidLimit = errors.New("invalid limit")

type Store interface {
	AggregateUsageLeaderboardByModel(context.Context, dbbilling.AggregateUsageLeaderboardByModelParams) ([]dbbilling.AggregateUsageLeaderboardByModelRow, error)
}

type Deps struct {
	Store Store
}

type rankingsResponse struct {
	Scope    string         `json:"scope"`
	Metric   string         `json:"metric"`
	Rankings []rankingEntry `json:"rankings"`
}

type rankingEntry struct {
	Rank         int    `json:"rank"`
	Model        string `json:"model"`
	RequestCount int64  `json:"request_count"`
	TokenTotal   int64  `json:"token_total"`
	RequestShare string `json:"request_share"`
}

func NewHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			writeError(w, http.StatusServiceUnavailable, "public_rankings_dependency_unset", "public rankings dependency unset")
			return
		}

		limit, err := parseLimit(r.URL.Query().Get("limit"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_limit", "limit must be a positive integer")
			return
		}

		value, hit, err := usageanalyticshttp.GetOrLoad(cacheKey(limit), publicRankingsSnapshotTTL, func() (any, error) {
			return loadRankings(r.Context(), d.Store, limit)
		})
		if err != nil {
			w.Header().Set(snapshotCacheHeader, "miss")
			writeError(w, http.StatusServiceUnavailable, "public_rankings_query_failed", "public rankings backend unavailable")
			return
		}
		response, ok := value.(rankingsResponse)
		if !ok {
			w.Header().Set(snapshotCacheHeader, "miss")
			writeError(w, http.StatusServiceUnavailable, "public_rankings_query_failed", "public rankings backend unavailable")
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

func loadRankings(ctx context.Context, store Store, limit int32) (rankingsResponse, error) {
	rows, err := store.AggregateUsageLeaderboardByModel(ctx, dbbilling.AggregateUsageLeaderboardByModelParams{
		SettledSince: pgtype.Timestamptz{Time: time.Unix(0, 0).UTC(), Valid: true},
		RowLimit:     publicRankingsFetchLimit,
	})
	if err != nil {
		return rankingsResponse{}, err
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].RequestCount == rows[j].RequestCount {
			return strings.TrimSpace(rows[i].Key) < strings.TrimSpace(rows[j].Key)
		}
		return rows[i].RequestCount > rows[j].RequestCount
	})
	entries := projectRankings(rows, int(limit))
	return rankingsResponse{
		Scope:    publicRankingsScope,
		Metric:   publicRankingsMetric,
		Rankings: entries,
	}, nil
}

func projectRankings(rows []dbbilling.AggregateUsageLeaderboardByModelRow, limit int) []rankingEntry {
	if len(rows) > limit {
		rows = rows[:limit]
	}
	var totalRequests int64
	for _, row := range rows {
		if row.RequestCount > 0 {
			totalRequests += row.RequestCount
		}
	}
	entries := make([]rankingEntry, 0, len(rows))
	for i, row := range rows {
		requestCount := row.RequestCount
		if requestCount < 0 {
			requestCount = 0
		}
		tokenTotal := row.TotalTokens
		if tokenTotal < 0 {
			tokenTotal = 0
		}
		entries = append(entries, rankingEntry{
			Rank:         i + 1,
			Model:        strings.TrimSpace(row.Key),
			RequestCount: requestCount,
			TokenTotal:   tokenTotal,
			RequestShare: requestShare(requestCount, totalRequests),
		})
	}
	return entries
}

func requestShare(count, total int64) string {
	if count <= 0 || total <= 0 {
		return "0.000000"
	}
	return strconv.FormatFloat(float64(count)/float64(total), 'f', 6, 64)
}

func parseLimit(raw string) (int32, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultPublicRankingsLimit, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 {
		return 0, errInvalidLimit
	}
	if limit > maxPublicRankingsLimit {
		return maxPublicRankingsLimit, nil
	}
	return int32(limit), nil
}

func cacheKey(limit int32) string {
	return "public_rankings:v1|scope=" + publicRankingsScope + "|metric=" + publicRankingsMetric + "|limit=" + strconv.Itoa(int(limit))
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]map[string]string{
		"error": {"code": code, "message": message},
	})
}
