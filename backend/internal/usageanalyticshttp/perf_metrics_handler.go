package usageanalyticshttp

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/healthscore"
)

// ChannelHealthSummarizer 是 health-score handler 取"基础设施面"信号的窄只读接口:汇总某租户上游渠道
// 的健康状态分布(active/degraded/cooling_down/...)。仅声明所需方法,避免直接耦合整个 channelhealth.Service
// (与本包 ProviderAccountCountsQuerier 等窄接口风格一致)。生产实现 = channelhealth.Service。
type ChannelHealthSummarizer interface {
	SummarizeChannelHealth(ctx context.Context, tenantID int64) (channelhealth.ChannelHealthSummary, error)
}

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
	// 基础设施面信号(与上面的业务面错误率/延迟物理不同源):自动托管上游渠道的健康分布。
	// ChannelHealthAvailable=false 表示未传 tenant_id 或取数不可用,infra_score 走保守满分降级。
	ChannelHealthAvailable bool  `json:"channel_health_available"`
	HealthyChannels        int64 `json:"healthy_channels"`
	ManagedChannels        int64 `json:"managed_channels"`
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

// NewHealthScoreHandler 提供只读的 0-100 健康分。业务面=近期业务可见的错误率与 TTFT p99;
// 基础设施面=按 ?tenant_id 取的上游渠道健康状态分布(与业务面物理不同源,缺 tenant_id 则保守降级);
// 二者经 healthscore.Overall 按 70/30 合成。
func NewHealthScoreHandler(q Querier, ch ChannelHealthSummarizer) http.HandlerFunc {
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
		// 业务面:用户可见的错误率 + TTFT p99。
		businessScore := healthscore.Business(errorRate, percentiles.P99Ms)
		// 基础设施面:上游渠道健康分布(与业务面物理不同源)。按 ?tenant_id 取该租户的渠道健康汇总;
		// 缺 tenant_id / 无冲减器 / 取数失败时,infra 走保守满分降级(ChannelHealthAvailable=false),
		// 绝不回退成"复制业务分"——那会让两路输入相同、Overall 的 70/30 加权退化。
		infraSignal := computeInfraSignal(r, ch)
		infraScore := healthscore.Infra(infraSignal.HealthyChannels, infraSignal.ManagedChannels)
		writeJSON(w, http.StatusOK, healthScoreResponse{
			Window:        query.windowLabel,
			BusinessScore: businessScore,
			InfraScore:    infraScore,
			OverallScore:  healthscore.Overall(businessScore, infraScore),
			Signals: healthScoreSignals{
				ErrorRate:              errorRateText(errorCount, totals.RequestCount),
				TTFTP99MS:              percentiles.P99Ms,
				ChannelHealthAvailable: infraSignal.Available,
				HealthyChannels:        infraSignal.HealthyChannels,
				ManagedChannels:        infraSignal.ManagedChannels,
			},
		})
	}
}

type infraHealthSignal struct {
	Available       bool
	HealthyChannels int64
	ManagedChannels int64
}

// computeInfraSignal 从请求的 ?tenant_id 取该租户上游渠道健康分布,折算成"可服务 / 自动托管"两数,
// 供 healthscore.Infra 打分。SummarizeChannelHealth 要求 tenantID>0;缺 tenant_id、无冲减器或取数失败
// 一律降级为 Available=false(infra 走保守满分),绝不 500、绝不回退复制业务分。
func computeInfraSignal(r *http.Request, ch ChannelHealthSummarizer) infraHealthSignal {
	if ch == nil {
		return infraHealthSignal{}
	}
	tenantID, err := parseHealthScoreTenantID(r.URL.Query().Get("tenant_id"))
	if err != nil || tenantID <= 0 {
		return infraHealthSignal{}
	}
	summary, err := ch.SummarizeChannelHealth(r.Context(), tenantID)
	if err != nil {
		return infraHealthSignal{}
	}
	healthy := summary.ByState[channelhealth.StateActive] + summary.ByState[channelhealth.StateRamping]
	// 自动托管 = 健康 + 自动失败态(degraded/cooling_down/disabled);手动暂停(manual_paused)是人为意图,
	// 不进分母也不算健康。
	managed := healthy +
		summary.ByState[channelhealth.StateDegraded] +
		summary.ByState[channelhealth.StateCoolingDown] +
		summary.ByState[channelhealth.StateDisabled]
	return infraHealthSignal{Available: true, HealthyChannels: healthy, ManagedChannels: managed}
}

func parseHealthScoreTenantID(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	return strconv.ParseInt(raw, 10, 64)
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
