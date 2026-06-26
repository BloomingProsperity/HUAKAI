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
	"github.com/BloomingProsperity/HUAKAI/internal/healthscore"
)

const (
	defaultPerfMetricsBucket = "hour"
	perfMetricsBucketHour    = "hour"
	perfMetricsBucketDay     = "day"
	perfMetricsByModel       = "model"
)

type perfMetricsQuery struct {
	windowLabel  string
	settledSince pgtype.Timestamptz
	model        *string
}

type perfMetricsBucketQuery struct {
	windowLabel  string
	settledSince pgtype.Timestamptz
	bucket       string
	limit        int32
}

type latencyPercentiles struct {
	P50 float64 `json:"p50"`
	P95 float64 `json:"p95"`
	P99 float64 `json:"p99"`
}

type perfMetricsSummary struct {
	AvgTTFTMS    string `json:"avg_ttft_ms"`
	AvgTPS       string `json:"avg_tps"`
	RequestCount int64  `json:"request_count"`
	ErrorCount   int64  `json:"error_count"`
	ErrorRate    string `json:"error_rate"`
}

type perfMetricsSummaryResponse struct {
	Window             string             `json:"window"`
	Model              *string            `json:"model,omitempty"`
	Summary            perfMetricsSummary `json:"summary"`
	LatencyPercentiles latencyPercentiles `json:"latency_percentiles_ms"`
}

type perfMetricsBucketEntry struct {
	Bucket       string `json:"bucket"`
	Key          string `json:"key"`
	AvgTTFTMS    string `json:"avg_ttft_ms"`
	AvgTPS       string `json:"avg_tps"`
	RequestCount int64  `json:"request_count"`
	ErrorCount   int64  `json:"error_count"`
	ErrorRate    string `json:"error_rate"`
}

type perfMetricsBucketResponse struct {
	Window  string                   `json:"window"`
	Bucket  string                   `json:"bucket"`
	By      string                   `json:"by"`
	Entries []perfMetricsBucketEntry `json:"entries"`
}

type healthScoreResponse struct {
	Window        string             `json:"window"`
	OverallScore  int                `json:"overall_score"`
	BusinessScore int                `json:"business_score"`
	InfraScore    int                `json:"infra_score"`
	Signals       healthScoreSignals `json:"signals"`
}

type healthScoreSignals struct {
	ErrorRate string  `json:"error_rate"`
	TTFTP99MS float64 `json:"ttft_p99_ms"`
}

// NewPerfMetricsSummaryHandler 提供只读的平台管理员性能汇总，
// 包含 global/requested_model 维度的延迟分位数。
func NewPerfMetricsSummaryHandler(q Querier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if q == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "analytics dependency unset")
			return
		}
		query, ok := parsePerfMetricsQuery(w, r.URL, time.Now().UTC())
		if !ok {
			return
		}
		response, err := loadPerfMetricsSummary(r.Context(), q, query)
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "analytics_query_failed", "analytics backend unavailable")
			return
		}
		writeJSON(w, http.StatusOK, response)
	}
}

// NewPerfMetricsByBucketHandler 提供只读的 requested_model 性能指标，
// 按小时/天的 requested_at 桶分组。
func NewPerfMetricsByBucketHandler(q Querier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if q == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "analytics dependency unset")
			return
		}
		query, ok := parsePerfMetricsBucketQuery(w, r.URL, time.Now().UTC())
		if !ok {
			return
		}
		rows, err := q.AggregateUsagePerformanceByModelBucketed(r.Context(), dbbilling.AggregateUsagePerformanceByModelBucketedParams{
			SettledSince: query.settledSince,
			Bucket:       query.bucket,
			RowLimit:     query.limit,
		})
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "analytics_query_failed", "analytics backend unavailable")
			return
		}
		entries, err := perfMetricsBucketEntries(rows)
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "analytics_query_failed", "analytics backend unavailable")
			return
		}
		writeJSON(w, http.StatusOK, perfMetricsBucketResponse{
			Window:  query.windowLabel,
			Bucket:  query.bucket,
			By:      perfMetricsByModel,
			Entries: entries,
		})
	}
}

// NewHealthScoreHandler 提供只读的 0-100 健康分，依据近期
// 业务可见的错误率与 TTFT p99 计算。
func NewHealthScoreHandler(q Querier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if q == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "analytics dependency unset")
			return
		}
		query, ok := parsePerfMetricsQuery(w, r.URL, time.Now().UTC())
		if !ok {
			return
		}
		totals, err := q.AggregateUsageOverviewTotals(r.Context(), query.settledSince)
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "analytics_query_failed", "analytics backend unavailable")
			return
		}
		percentiles, err := q.AggregateUsageLatencyPercentiles(r.Context(), dbbilling.AggregateUsageLatencyPercentilesParams{
			SettledSince: query.settledSince,
		})
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "analytics_query_failed", "analytics backend unavailable")
			return
		}
		errorCount := totals.RequestCount - totals.SuccessCount
		if errorCount < 0 {
			errorCount = 0
		}
		errorRate := errorRateValue(errorCount, totals.RequestCount)
		businessScore := healthscore.Business(errorRate, percentiles.P99Ms)
		infraScore := healthscore.Business(errorRate, percentiles.P99Ms)
		writeJSON(w, http.StatusOK, healthScoreResponse{
			Window:        query.windowLabel,
			BusinessScore: businessScore,
			InfraScore:    infraScore,
			OverallScore:  healthscore.Overall(businessScore, infraScore),
			Signals: healthScoreSignals{
				ErrorRate: errorRateText(errorCount, totals.RequestCount),
				TTFTP99MS: percentiles.P99Ms,
			},
		})
	}
}

func loadPerfMetricsSummary(ctx context.Context, q Querier, query perfMetricsQuery) (perfMetricsSummaryResponse, error) {
	summaryRow, err := q.AggregateUsagePerformanceSummary(ctx, dbbilling.AggregateUsagePerformanceSummaryParams{
		SettledSince: query.settledSince,
		Model:        query.model,
	})
	if err != nil {
		return perfMetricsSummaryResponse{}, err
	}
	percentileRow, err := q.AggregateUsageLatencyPercentiles(ctx, dbbilling.AggregateUsageLatencyPercentilesParams{
		SettledSince: query.settledSince,
		Model:        query.model,
	})
	if err != nil {
		return perfMetricsSummaryResponse{}, err
	}
	avgTTFT, err := fixedDecimalText(summaryRow.AvgTtftMs)
	if err != nil {
		return perfMetricsSummaryResponse{}, err
	}
	avgTPS, err := fixedDecimalText(summaryRow.AvgTps)
	if err != nil {
		return perfMetricsSummaryResponse{}, err
	}
	return perfMetricsSummaryResponse{
		Window: query.windowLabel,
		Model:  query.model,
		Summary: perfMetricsSummary{
			AvgTTFTMS:    avgTTFT,
			AvgTPS:       avgTPS,
			RequestCount: summaryRow.RequestCount,
			ErrorCount:   summaryRow.ErrorCount,
			ErrorRate:    errorRateText(summaryRow.ErrorCount, summaryRow.RequestCount),
		},
		LatencyPercentiles: latencyPercentiles{
			P50: percentileRow.P50Ms,
			P95: percentileRow.P95Ms,
			P99: percentileRow.P99Ms,
		},
	}, nil
}

func parsePerfMetricsQuery(w http.ResponseWriter, u *url.URL, now time.Time) (perfMetricsQuery, bool) {
	window, label, err := parseLeaderboardWindow(u.Query().Get("window"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_window", "window must be a positive duration such as 24h, 7d, or 30d")
		return perfMetricsQuery{}, false
	}
	model := optionalModelFilter(u.Query().Get("model"))
	return perfMetricsQuery{
		windowLabel:  label,
		settledSince: pgtype.Timestamptz{Time: now.Add(-window), Valid: true},
		model:        model,
	}, true
}

func parsePerfMetricsBucketQuery(w http.ResponseWriter, u *url.URL, now time.Time) (perfMetricsBucketQuery, bool) {
	window, label, err := parseLeaderboardWindow(u.Query().Get("window"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_window", "window must be a positive duration such as 24h, 7d, or 30d")
		return perfMetricsBucketQuery{}, false
	}
	bucket := strings.TrimSpace(u.Query().Get("bucket"))
	if bucket == "" {
		bucket = defaultPerfMetricsBucket
	}
	if bucket != perfMetricsBucketHour && bucket != perfMetricsBucketDay {
		writeJSONError(w, http.StatusBadRequest, "invalid_bucket", "bucket must be hour or day")
		return perfMetricsBucketQuery{}, false
	}
	limit, err := parseLeaderboardLimit(u.Query().Get("limit"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_limit", "limit must be a positive integer")
		return perfMetricsBucketQuery{}, false
	}
	return perfMetricsBucketQuery{
		windowLabel:  label,
		settledSince: pgtype.Timestamptz{Time: now.Add(-window), Valid: true},
		bucket:       bucket,
		limit:        int32(limit),
	}, true
}

func optionalModelFilter(raw string) *string {
	model := strings.TrimSpace(raw)
	if model == "" {
		return nil
	}
	return &model
}

func perfMetricsBucketEntries(rows []dbbilling.AggregateUsagePerformanceByModelBucketedRow) ([]perfMetricsBucketEntry, error) {
	entries := make([]perfMetricsBucketEntry, 0, len(rows))
	for _, row := range rows {
		avgTTFT, err := fixedDecimalText(row.AvgTtftMs)
		if err != nil {
			return nil, err
		}
		avgTPS, err := fixedDecimalText(row.AvgTps)
		if err != nil {
			return nil, err
		}
		entries = append(entries, perfMetricsBucketEntry{
			Bucket:       formatTimestamp(row.Bucket),
			Key:          strings.TrimSpace(row.Key),
			AvgTTFTMS:    avgTTFT,
			AvgTPS:       avgTPS,
			RequestCount: row.RequestCount,
			ErrorCount:   row.ErrorCount,
			ErrorRate:    errorRateText(row.ErrorCount, row.RequestCount),
		})
	}
	return entries, nil
}

func formatTimestamp(ts pgtype.Timestamptz) string {
	if !ts.Valid {
		return ""
	}
	return ts.Time.UTC().Format(time.RFC3339)
}

func errorRateValue(errorCount, requestCount int64) float64 {
	if requestCount <= 0 {
		return 0
	}
	return decimal.NewFromInt(errorCount).Div(decimal.NewFromInt(requestCount)).InexactFloat64()
}
