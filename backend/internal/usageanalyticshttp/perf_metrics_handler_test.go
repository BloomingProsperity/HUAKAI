package usageanalyticshttp

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

type perfMetricsQueryStub struct {
	calls []string

	summaryArg     dbbilling.AggregateUsagePerformanceSummaryParams
	percentilesArg dbbilling.AggregateUsageLatencyPercentilesParams
	bucketArg      dbbilling.AggregateUsagePerformanceByModelBucketedParams
	overviewArg    pgtype.Timestamptz

	summaryRow     dbbilling.AggregateUsagePerformanceSummaryRow
	percentileRow  dbbilling.AggregateUsageLatencyPercentilesRow
	bucketRows     []dbbilling.AggregateUsagePerformanceByModelBucketedRow
	overviewTotals dbbilling.AggregateUsageOverviewTotalsRow
}

func (s *perfMetricsQueryStub) AggregateUsageLeaderboardByUser(context.Context, dbbilling.AggregateUsageLeaderboardByUserParams) ([]dbbilling.AggregateUsageLeaderboardByUserRow, error) {
	return nil, nil
}

func (s *perfMetricsQueryStub) AggregateUsageLeaderboardByModel(context.Context, dbbilling.AggregateUsageLeaderboardByModelParams) ([]dbbilling.AggregateUsageLeaderboardByModelRow, error) {
	return nil, nil
}

func (s *perfMetricsQueryStub) AggregateUsageLeaderboardByProviderAccount(context.Context, dbbilling.AggregateUsageLeaderboardByProviderAccountParams) ([]dbbilling.AggregateUsageLeaderboardByProviderAccountRow, error) {
	return nil, nil
}

func (s *perfMetricsQueryStub) AggregateUsageLeaderboardByApiKey(context.Context, dbbilling.AggregateUsageLeaderboardByApiKeyParams) ([]dbbilling.AggregateUsageLeaderboardByApiKeyRow, error) {
	return nil, nil
}

func (s *perfMetricsQueryStub) AggregateUsagePerformanceByModel(context.Context, dbbilling.AggregateUsagePerformanceByModelParams) ([]dbbilling.AggregateUsagePerformanceByModelRow, error) {
	return nil, nil
}

func (s *perfMetricsQueryStub) AggregateUsagePerformanceByProviderAccount(context.Context, dbbilling.AggregateUsagePerformanceByProviderAccountParams) ([]dbbilling.AggregateUsagePerformanceByProviderAccountRow, error) {
	return nil, nil
}

func (s *perfMetricsQueryStub) AggregateUsagePerformanceSummary(_ context.Context, arg dbbilling.AggregateUsagePerformanceSummaryParams) (dbbilling.AggregateUsagePerformanceSummaryRow, error) {
	s.calls = append(s.calls, "summary")
	s.summaryArg = arg
	return s.summaryRow, nil
}

func (s *perfMetricsQueryStub) AggregateUsageLatencyPercentiles(_ context.Context, arg dbbilling.AggregateUsageLatencyPercentilesParams) (dbbilling.AggregateUsageLatencyPercentilesRow, error) {
	s.calls = append(s.calls, "percentiles")
	s.percentilesArg = arg
	return s.percentileRow, nil
}

func (s *perfMetricsQueryStub) AggregateUsagePerformanceByModelBucketed(_ context.Context, arg dbbilling.AggregateUsagePerformanceByModelBucketedParams) ([]dbbilling.AggregateUsagePerformanceByModelBucketedRow, error) {
	s.calls = append(s.calls, "bucketed")
	s.bucketArg = arg
	return s.bucketRows, nil
}

func (s *perfMetricsQueryStub) AggregateUsageOverviewTotals(_ context.Context, settledSince pgtype.Timestamptz) (dbbilling.AggregateUsageOverviewTotalsRow, error) {
	s.calls = append(s.calls, "overview")
	s.overviewArg = settledSince
	return s.overviewTotals, nil
}

func (s *perfMetricsQueryStub) AggregateUsageOverviewTrendByDay(context.Context, pgtype.Timestamptz) ([]dbbilling.AggregateUsageOverviewTrendByDayRow, error) {
	return nil, nil
}

func TestPerfMetricsSummaryReturnsPercentilesAndTotals(t *testing.T) {
	model := "gpt-fast"
	store := &perfMetricsQueryStub{
		summaryRow: dbbilling.AggregateUsagePerformanceSummaryRow{
			AvgTtftMs:    "250",
			AvgTps:       "42",
			RequestCount: 4,
			ErrorCount:   1,
		},
		percentileRow: dbbilling.AggregateUsageLatencyPercentilesRow{
			P50Ms: 100,
			P95Ms: 350,
			P99Ms: 500,
		},
	}

	rec := invoke(NewPerfMetricsSummaryHandler(store), "/v1/admin/usage/perf-metrics/summary?window=24h&model="+model)
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Window  string `json:"window"`
		Model   string `json:"model"`
		Summary struct {
			AvgTTFTMS    string `json:"avg_ttft_ms"`
			AvgTPS       string `json:"avg_tps"`
			RequestCount int64  `json:"request_count"`
			ErrorRate    string `json:"error_rate"`
		} `json:"summary"`
		LatencyPercentiles struct {
			P50 float64 `json:"p50"`
			P95 float64 `json:"p95"`
			P99 float64 `json:"p99"`
		} `json:"latency_percentiles_ms"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body=%s err=%v", rec.Body.String(), err)
	}
	if body.Window != "24h" || body.Model != model {
		t.Fatalf("window/model=%q/%q want 24h/%s", body.Window, body.Model, model)
	}
	if body.Summary.AvgTTFTMS != "250.0000" || body.Summary.AvgTPS != "42.0000" || body.Summary.RequestCount != 4 || body.Summary.ErrorRate != "0.2500" {
		t.Fatalf("summary=%+v want avg/error/request totals", body.Summary)
	}
	if body.LatencyPercentiles.P50 != 100 || body.LatencyPercentiles.P95 != 350 || body.LatencyPercentiles.P99 != 500 {
		t.Fatalf("percentiles=%+v want p50=100 p95=350 p99=500; mutation collapsing p95/p99 to p50 makes this red", body.LatencyPercentiles)
	}
	if store.summaryArg.Model == nil || *store.summaryArg.Model != model || store.percentilesArg.Model == nil || *store.percentilesArg.Model != model {
		t.Fatalf("model filter not passed to both summary and percentile queries: summary=%v percentiles=%v", store.summaryArg.Model, store.percentilesArg.Model)
	}
}

func TestPerfMetricsByBucketGroupsByHour(t *testing.T) {
	first := time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC)
	second := first.Add(time.Hour)
	store := &perfMetricsQueryStub{
		bucketRows: []dbbilling.AggregateUsagePerformanceByModelBucketedRow{
			{
				Bucket:       pgtype.Timestamptz{Time: first, Valid: true},
				Key:          "gpt-fast",
				AvgTtftMs:    "100",
				AvgTps:       "20",
				RequestCount: 2,
				ErrorCount:   0,
			},
			{
				Bucket:       pgtype.Timestamptz{Time: second, Valid: true},
				Key:          "gpt-fast",
				AvgTtftMs:    "300",
				AvgTps:       "10",
				RequestCount: 1,
				ErrorCount:   1,
			},
		},
	}

	rec := invoke(NewPerfMetricsByBucketHandler(store), "/v1/admin/usage/perf-metrics/by-bucket?window=24h&bucket=hour&limit=5")
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Window  string `json:"window"`
		Bucket  string `json:"bucket"`
		By      string `json:"by"`
		Entries []struct {
			Bucket       string `json:"bucket"`
			Key          string `json:"key"`
			AvgTTFTMS    string `json:"avg_ttft_ms"`
			RequestCount int64  `json:"request_count"`
			ErrorRate    string `json:"error_rate"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body=%s err=%v", rec.Body.String(), err)
	}
	if body.Window != "24h" || body.Bucket != "hour" || body.By != "model" {
		t.Fatalf("window/bucket/by=%q/%q/%q want 24h/hour/model", body.Window, body.Bucket, body.By)
	}
	if len(body.Entries) != 2 {
		t.Fatalf("entries=%d want 2 hourly buckets; mutation removing date_trunc grouping returns one bucket", len(body.Entries))
	}
	if body.Entries[0].Bucket != "2026-06-08T10:00:00Z" || body.Entries[1].Bucket != "2026-06-08T11:00:00Z" {
		t.Fatalf("bucket timestamps=%q/%q want consecutive UTC hours", body.Entries[0].Bucket, body.Entries[1].Bucket)
	}
	if store.bucketArg.Bucket != "hour" || store.bucketArg.RowLimit != 5 {
		t.Fatalf("bucket args=%+v want bucket=hour limit=5", store.bucketArg)
	}
}

func TestPerfMetricsByBucketRejectsUnsupportedBucket(t *testing.T) {
	store := &perfMetricsQueryStub{}
	rec := invoke(NewPerfMetricsByBucketHandler(store), "/v1/admin/usage/perf-metrics/by-bucket?window=24h&bucket=week")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code=%d want 400 body=%s", rec.Code, rec.Body.String())
	}
	if len(store.calls) != 0 {
		t.Fatalf("invalid bucket called queries=%v", store.calls)
	}
}

func TestHealthScoreHandlerCombinesOverviewErrorRateAndP99(t *testing.T) {
	store := &perfMetricsQueryStub{
		overviewTotals: dbbilling.AggregateUsageOverviewTotalsRow{
			RequestCount: 10,
			SuccessCount: 9,
		},
		percentileRow: dbbilling.AggregateUsageLatencyPercentilesRow{
			P50Ms: 500,
			P95Ms: 1800,
			P99Ms: 3000,
		},
	}

	rec := invoke(NewHealthScoreHandler(store), "/v1/admin/usage/health-score?window=24h")
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Window        string `json:"window"`
		OverallScore  int    `json:"overall_score"`
		BusinessScore int    `json:"business_score"`
		InfraScore    int    `json:"infra_score"`
		Signals       struct {
			ErrorRate string  `json:"error_rate"`
			TTFTP99MS float64 `json:"ttft_p99_ms"`
		} `json:"signals"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body=%s err=%v", rec.Body.String(), err)
	}
	if body.Window != "24h" || body.Signals.ErrorRate != "0.1000" || body.Signals.TTFTP99MS != 3000 {
		t.Fatalf("signals/window=%+v/%q want error_rate=0.1000 p99=3000 window=24h", body.Signals, body.Window)
	}
	if body.BusinessScore != 0 || body.InfraScore != 0 || body.OverallScore != 0 {
		t.Fatalf("scores=%d/%d/%d want 0 at 10%% error and 3000ms p99", body.BusinessScore, body.InfraScore, body.OverallScore)
	}
}
