//go:build integration_pg

package billing

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dboverview "github.com/BloomingProsperity/HUAKAI/internal/db/usageoverview"
)

func TestRecentUsageRollupByTenant(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openUsageOutcomePool(t, ctx)
	defer pool.Close()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin recent usage rollup tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tenantA := seedUsageOutcomeFixture(t, ctx, tx)
	_ = seedUsageOutcomeFixture(t, ctx, tx)
	base := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	seedUsageOutcomeRecord(t, ctx, tx, tenantA, "old-outside-window", "upstream_error_5xx", base.Add(-2*time.Hour))

	q := New(tx)
	// 变异:去掉 tenant_id 或 settled_at 过滤 -> 租户 B 或租户 A 的旧行会虚增这个汇总 -> 变红。
	got, err := q.RecentUsageRollupByTenant(ctx, RecentUsageRollupByTenantParams{
		TenantID: tenantA.tenantID,
		SettledSince: pgtype.Timestamptz{
			Time:  base.Add(-time.Minute),
			Valid: true,
		},
	})
	if err != nil {
		t.Fatalf("RecentUsageRollupByTenant: %v", err)
	}
	if got.RequestCount != 3 {
		t.Fatalf("request_count=%d want 3", got.RequestCount)
	}
	if got.SuccessCount != 2 {
		t.Fatalf("success_count=%d want 2", got.SuccessCount)
	}
	if got.ErrorCount != 1 {
		t.Fatalf("error_count=%d want 1", got.ErrorCount)
	}
	if got.TotalCost != "0.03000000" {
		t.Fatalf("total_cost=%q want 0.03000000", got.TotalCost)
	}
}

func TestAggregateUsageOverviewTotalsPreservesTokenAndCostBreakdown(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openUsageOutcomePool(t, ctx)
	defer pool.Close()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("开始运营总览事务: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	fixture := seedUsageOutcomeFixture(t, ctx, tx)
	base := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	seedUsageOverviewBreakdownRecord(t, ctx, tx, fixture, "overview-breakdown", base.Add(4*time.Second))

	got, err := New(tx).AggregateUsageOverviewTotals(ctx, pgtype.Timestamptz{
		Time: base.Add(-time.Minute), Valid: true,
	})
	if err != nil {
		t.Fatalf("AggregateUsageOverviewTotals: %v", err)
	}
	if got.RequestCount != 4 || got.TotalCost != "0.08000000" || got.TotalTokens != 160 {
		t.Fatalf("总览总量=%+v，期望请求/费用/输入输出 Token=4/0.08/160", got)
	}
	if got.TotalTokensInput != 60 || got.TotalTokensOutput != 100 ||
		got.TotalCacheCreationTokens != 5 || got.TotalCacheReadTokens != 7 ||
		got.TotalImageOutputTokens != 2 {
		t.Fatalf("Token 分项=%+v，期望 input/output/cache-create/cache-read/image=60/100/5/7/2", got)
	}
	if got.TotalInputCost != "0.02200000" || got.TotalOutputCost != "0.03800000" ||
		got.TotalCacheCreationCost != "0.00300000" || got.TotalCacheReadCost != "0.00400000" ||
		got.TotalImageOutputCost != "0.00500000" {
		t.Fatalf("费用分项=%+v，必须与相同结算窗口的不可变记录一致", got)
	}
}

func TestAggregateTenantUsageOverviewIsolatesTenants(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openUsageOutcomePool(t, ctx)
	defer pool.Close()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("开始租户总览事务: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tenantA := seedUsageOutcomeFixture(t, ctx, tx)
	tenantB := seedUsageOutcomeFixture(t, ctx, tx)
	base := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	seedUsageOverviewBreakdownRecord(t, ctx, tx, tenantA, "tenant-overview-a", base.Add(4*time.Second))

	q := dboverview.New(tx)
	since := pgtype.Timestamptz{Time: base.Add(-time.Minute), Valid: true}
	gotA, err := q.AggregateTenantUsageOverviewTotals(ctx, dboverview.AggregateTenantUsageOverviewTotalsParams{
		TenantID: tenantA.tenantID, SettledSince: since,
	})
	if err != nil {
		t.Fatalf("租户 A totals: %v", err)
	}
	gotB, err := q.AggregateTenantUsageOverviewTotals(ctx, dboverview.AggregateTenantUsageOverviewTotalsParams{
		TenantID: tenantB.tenantID, SettledSince: since,
	})
	if err != nil {
		t.Fatalf("租户 B totals: %v", err)
	}
	// fixture=3 行 0.03/90 Token；A 另加 breakdown 0.05/70。变异:去掉 tenant_id -> A/B 都会吃到对方 -> 红。
	if gotA.RequestCount != 4 || gotA.TotalCost != "0.08000000" || gotA.TotalTokens != 160 {
		t.Fatalf("租户 A totals=%+v，期望 4/0.08/160", gotA)
	}
	if gotA.TotalTokensInput != 60 || gotA.TotalTokensOutput != 100 ||
		gotA.TotalCacheCreationTokens != 5 || gotA.TotalCacheReadTokens != 7 ||
		gotA.TotalImageOutputTokens != 2 {
		t.Fatalf("租户 A Token 分项=%+v，期望 60/100/5/7/2", gotA)
	}
	if gotB.RequestCount != 3 || gotB.TotalCost != "0.03000000" || gotB.TotalTokens != 90 {
		t.Fatalf("租户 B totals=%+v，必须看不见 A 的 breakdown", gotB)
	}
	if gotB.TotalCacheCreationTokens != 0 || gotB.TotalCacheReadTokens != 0 || gotB.TotalImageOutputTokens != 0 {
		t.Fatalf("租户 B 提示缓存/图像分项=%+v 必须为 0", gotB)
	}

	trendA, err := q.AggregateTenantUsageOverviewTrendByDay(ctx, dboverview.AggregateTenantUsageOverviewTrendByDayParams{
		TenantID: tenantA.tenantID, SettledSince: since,
	})
	if err != nil {
		t.Fatalf("租户 A 趋势: %v", err)
	}
	trendB, err := q.AggregateTenantUsageOverviewTrendByDay(ctx, dboverview.AggregateTenantUsageOverviewTrendByDayParams{
		TenantID: tenantB.tenantID, SettledSince: since,
	})
	if err != nil {
		t.Fatalf("租户 B 趋势: %v", err)
	}
	if len(trendA) != 1 || trendA[0].RequestCount != 4 || trendA[0].TotalCost != "0.08000000" {
		t.Fatalf("租户 A 日趋势=%+v，期望一日 4/0.08", trendA)
	}
	if len(trendB) != 1 || trendB[0].RequestCount != 3 || trendB[0].TotalCost != "0.03000000" {
		t.Fatalf("租户 B 日趋势=%+v，必须与 A 分列", trendB)
	}
}

func TestAggregateTenantUsageHourlyTrendIsolatesTenants(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openUsageOutcomePool(t, ctx)
	defer pool.Close()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("开始小时趋势事务: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tenantA := seedUsageOutcomeFixture(t, ctx, tx)
	tenantB := seedUsageOutcomeFixture(t, ctx, tx)
	base := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	seedUsageOverviewBreakdownRecord(t, ctx, tx, tenantA, "tenant-hourly-a", base.Add(4*time.Second))
	seedUsageOutcomeRecord(t, ctx, tx, tenantA, "tenant-hourly-a-13", "non_streaming", base.Add(time.Hour))

	q := dboverview.New(tx)
	since := pgtype.Timestamptz{Time: base.Add(-time.Minute), Valid: true}
	gotA, err := q.AggregateTenantUsageOverviewTotals(ctx, dboverview.AggregateTenantUsageOverviewTotalsParams{
		TenantID: tenantA.tenantID, SettledSince: since,
	})
	if err != nil {
		t.Fatalf("租户 A totals: %v", err)
	}
	hoursA, err := q.AggregateTenantUsageHourlyTrend(ctx, dboverview.AggregateTenantUsageHourlyTrendParams{
		TenantID: tenantA.tenantID, SettledSince: since,
	})
	if err != nil {
		t.Fatalf("租户 A 小时: %v", err)
	}
	hoursB, err := q.AggregateTenantUsageHourlyTrend(ctx, dboverview.AggregateTenantUsageHourlyTrendParams{
		TenantID: tenantB.tenantID, SettledSince: since,
	})
	if err != nil {
		t.Fatalf("租户 B 小时: %v", err)
	}
	// fixture 3 行在 12:00；breakdown 同小时；13:00 再一行。变异:去掉 tenant_id -> A 会吃到 B。
	if gotA.RequestCount != 5 || gotA.TotalCost != "0.09000000" || gotA.TotalTokens != 190 {
		t.Fatalf("租户 A totals=%+v，期望 5/0.09/190", gotA)
	}
	if gotA.TotalTokensInput != 70 || gotA.TotalTokensOutput != 120 ||
		gotA.TotalCacheCreationTokens != 5 || gotA.TotalCacheReadTokens != 7 {
		t.Fatalf("租户 A Token 分项=%+v，期望 70/120/5/7", gotA)
	}
	if len(hoursA) != 2 {
		t.Fatalf("租户 A 小时点数=%d 期望 2，禁止合成一日或补零", len(hoursA))
	}
	if hoursA[0].TokensInput != 60 || hoursA[0].TokensOutput != 100 ||
		hoursA[0].CacheCreationTokens != 5 || hoursA[0].CacheReadTokens != 7 ||
		hoursA[0].TotalCost != "0.08000000" {
		t.Fatalf("租户 A 12 点=%+v，期望 60/100/5/7/0.08", hoursA[0])
	}
	if hoursA[1].TokensInput != 10 || hoursA[1].TokensOutput != 20 ||
		hoursA[1].CacheCreationTokens != 0 || hoursA[1].CacheReadTokens != 0 ||
		hoursA[1].TotalCost != "0.01000000" {
		t.Fatalf("租户 A 13 点=%+v，期望 10/20/0/0/0.01", hoursA[1])
	}
	var sumIn, sumOut, sumCreate, sumRead int64
	for _, hour := range hoursA {
		sumIn += hour.TokensInput
		sumOut += hour.TokensOutput
		sumCreate += hour.CacheCreationTokens
		sumRead += hour.CacheReadTokens
	}
	if sumIn != gotA.TotalTokensInput || sumOut != gotA.TotalTokensOutput ||
		sumCreate != gotA.TotalCacheCreationTokens || sumRead != gotA.TotalCacheReadTokens {
		t.Fatalf("小时序列之和必须与同窗 totals 对账 hours=%+v totals=%+v", hoursA, gotA)
	}
	if len(hoursB) != 1 || hoursB[0].TokensInput != 30 || hoursB[0].TokensOutput != 60 ||
		hoursB[0].CacheCreationTokens != 0 || hoursB[0].TotalCost != "0.03000000" {
		t.Fatalf("租户 B 小时=%+v，必须看不见 A 的 breakdown 与 13 点", hoursB)
	}
}

func TestAggregateTenantUsageCacheCompositionIsolatesTenants(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openUsageOutcomePool(t, ctx)
	defer pool.Close()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("开始缓存构成事务: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tenantA := seedUsageOutcomeFixture(t, ctx, tx)
	tenantB := seedUsageOutcomeFixture(t, ctx, tx)
	base := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	seedUsageOverviewBreakdownRecord(t, ctx, tx, tenantA, "tenant-cache-a", base.Add(4*time.Second))
	seedUsageResponseCacheHitRecord(t, ctx, tx, tenantA, "tenant-cache-a-l2", base.Add(8*time.Second))

	q := dboverview.New(tx)
	since := pgtype.Timestamptz{Time: base.Add(-time.Minute), Valid: true}
	gotA, err := q.AggregateTenantUsageCacheComposition(ctx, dboverview.AggregateTenantUsageCacheCompositionParams{
		TenantID: tenantA.tenantID, SettledSince: since,
	})
	if err != nil {
		t.Fatalf("租户 A 构成: %v", err)
	}
	gotB, err := q.AggregateTenantUsageCacheComposition(ctx, dboverview.AggregateTenantUsageCacheCompositionParams{
		TenantID: tenantB.tenantID, SettledSince: since,
	})
	if err != nil {
		t.Fatalf("租户 B 构成: %v", err)
	}
	totalsA, err := q.AggregateTenantUsageOverviewTotals(ctx, dboverview.AggregateTenantUsageOverviewTotalsParams{
		TenantID: tenantA.tenantID, SettledSince: since,
	})
	if err != nil {
		t.Fatalf("租户 A totals: %v", err)
	}
	// fixture 3 上游 + breakdown 1 上游 + L2 1。L2 草稿 Token=99/88 不得进提示缓存栏。
	if gotA.RequestCount != 5 || gotA.UpstreamRequests != 4 || gotA.ResponseCacheHits != 1 {
		t.Fatalf("租户 A 构成计数=%+v，期望 5/4/1", gotA)
	}
	if gotA.PromptCacheCreationTokens != 5 || gotA.PromptCacheReadTokens != 7 {
		t.Fatalf("租户 A 提示缓存=%+v，期望 5/7，禁止把 L2 草稿 99/88 折进来", gotA)
	}
	if gotA.ResponseCacheCost != "0.00000000" {
		t.Fatalf("L2 费用=%q 必须为 0", gotA.ResponseCacheCost)
	}
	if totalsA.RequestCount != gotA.RequestCount {
		t.Fatalf("同窗 totals.requests=%d 必须与构成 requests=%d 对账", totalsA.RequestCount, gotA.RequestCount)
	}
	if gotB.RequestCount != 3 || gotB.ResponseCacheHits != 0 || gotB.PromptCacheReadTokens != 0 {
		t.Fatalf("租户 B 构成=%+v 必须看不见 A", gotB)
	}
}

// TTFT(first_byte_at - requested_at)的 p95/p99 只能在记录了 first byte 的行上
// 计算,而没有记录任何 first byte 的租户必须 COALESCE 成 0(而非 NULL -> scan 报错)。
// 额外三行带有不同的 TTFT,分别是 1000/2000/3000 ms,其 percentile_cont(0.95)=2900
// 和 (0.99)=2980 是不同的 —— 所以一个对两列都输出相同百分位的查询会变红,
// 而一个全为 NULL 的租户若返回 NULL 而非 0 则会让 scan 失败。
func TestRecentUsageRollupByTenant_LatencyPercentiles(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openUsageOutcomePool(t, ctx)
	defer pool.Close()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin latency rollup tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	withLatency := seedUsageOutcomeFixture(t, ctx, tx)
	noLatency := seedUsageOutcomeFixture(t, ctx, tx)

	// usage_records 是仅追加的,所以 first byte 时间在插入时就固定了。
	base := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	seedUsageRecordTTFT(t, ctx, tx, withLatency, "ttft-1000", base.Add(10*time.Second), 1000*time.Millisecond)
	seedUsageRecordTTFT(t, ctx, tx, withLatency, "ttft-2000", base.Add(11*time.Second), 2000*time.Millisecond)
	seedUsageRecordTTFT(t, ctx, tx, withLatency, "ttft-3000", base.Add(12*time.Second), 3000*time.Millisecond)

	since := pgtype.Timestamptz{
		Time:  time.Date(2026, 6, 7, 11, 59, 0, 0, time.UTC),
		Valid: true,
	}
	q := New(tx)

	got, err := q.RecentUsageRollupByTenant(ctx, RecentUsageRollupByTenantParams{
		TenantID:     withLatency.tenantID,
		SettledSince: since,
	})
	if err != nil {
		t.Fatalf("RecentUsageRollupByTenant(withLatency): %v", err)
	}
	// 3 条 fixture 行(无 first byte) + 3 条 TTFT 行 = 6;百分位只看那 3 条。
	if got.RequestCount != 6 {
		t.Fatalf("request_count=%d want 6", got.RequestCount)
	}
	if !floatNear(got.LatencyP95Ms, 2900, 0.5) {
		t.Fatalf("latency_p95_ms=%v want ~2900", got.LatencyP95Ms)
	}
	if !floatNear(got.LatencyP99Ms, 2980, 0.5) {
		t.Fatalf("latency_p99_ms=%v want ~2980", got.LatencyP99Ms)
	}
	if got.LatencyP95Ms >= got.LatencyP99Ms {
		t.Fatalf("p95=%v must be < p99=%v (distinct percentiles)", got.LatencyP95Ms, got.LatencyP99Ms)
	}

	// 一个所有行都从未记录 first byte 的租户必须报告 0,而非 NULL。
	gotNull, err := q.RecentUsageRollupByTenant(ctx, RecentUsageRollupByTenantParams{
		TenantID:     noLatency.tenantID,
		SettledSince: since,
	})
	if err != nil {
		t.Fatalf("RecentUsageRollupByTenant(noLatency): %v", err)
	}
	if gotNull.RequestCount != 3 {
		t.Fatalf("noLatency request_count=%d want 3", gotNull.RequestCount)
	}
	if gotNull.LatencyP95Ms != 0 || gotNull.LatencyP99Ms != 0 {
		t.Fatalf("no-first-byte tenant latency=%v/%v want 0/0", gotNull.LatencyP95Ms, gotNull.LatencyP99Ms)
	}
}

// seedUsageRecordTTFT 插入一条已提交的 claim + usage record,其 first_byte_at
// 等于 settledAt 对应的 requested_at 加上 ttft,这样汇总的 TTFT 百分位就能看到
// 一个已知的延迟。usage_records 是仅追加的,所以该值在插入时就设定。
func seedUsageRecordTTFT(t *testing.T, ctx context.Context, tx pgx.Tx, f usageOutcomeFixture, logicalRequestID string, settledAt time.Time, ttft time.Duration) {
	t.Helper()
	acquisitionToken := uuid.New()
	requestedAt := settledAt.Add(-time.Second)
	var claimID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO billing_ledger_claims (
			tenant_id, idempotency_key, request_fingerprint, api_key_id, user_id,
			logical_request_id, endpoint_family, requested_model, pooling_group_id,
			provider_account_id, acquisition_token, attempt_seq, billing_policy_version,
			request_class, predicted_cost, actual_cost, currency_code, status, settled_at,
			lease_expires_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, 'messages', 'claude-usage-outcome', $7,
			$8, $9, 1, 'bp-test', 'standard', 0.01000000, 0.01000000, 'USD',
			'committed', $10, $11)
		RETURNING id
	`, f.tenantID, "idem-"+logicalRequestID, "fingerprint-"+logicalRequestID, f.apiKeyID, f.userID, logicalRequestID, f.poolID, f.providerAccountID, acquisitionToken, settledAt, settledAt.Add(time.Hour)).Scan(&claimID); err != nil {
		t.Fatalf("insert claim %s: %v", logicalRequestID, err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO usage_records (
			tenant_id, claim_id, api_key_id, user_id, provider_account_id,
			acquisition_token, attempt_seq, tokens_input, tokens_output, actual_cost,
			input_cost, output_cost, end_class, usage_source, pending_reconciliation,
			requested_at, first_byte_at, settled_at, requested_model, upstream_model, stream,
			settlement_source
		)
		VALUES ($1, $2, $3, $4, $5, $6, 1, 10, 20, 0.01000000, 0.00400000,
			0.00600000, 'non_streaming', 'reported', false, $7, $8, $9, 'claude-usage-outcome',
			'claude-usage-outcome-upstream', false, 'provider_upstream')
	`, f.tenantID, claimID, f.apiKeyID, f.userID, f.providerAccountID, acquisitionToken, requestedAt, requestedAt.Add(ttft), settledAt); err != nil {
		t.Fatalf("insert usage %s: %v", logicalRequestID, err)
	}
}

func seedUsageOverviewBreakdownRecord(t *testing.T, ctx context.Context, tx pgx.Tx, f usageOutcomeFixture, logicalRequestID string, settledAt time.Time) {
	t.Helper()
	acquisitionToken := uuid.New()
	var claimID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO billing_ledger_claims (
			tenant_id, idempotency_key, request_fingerprint, api_key_id, user_id,
			logical_request_id, endpoint_family, requested_model, pooling_group_id,
			provider_account_id, acquisition_token, attempt_seq, billing_policy_version,
			request_class, predicted_cost, actual_cost, currency_code, status, settled_at,
			lease_expires_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,'messages','claude-overview',$7,$8,$9,1,'bp-test',
			'standard',0.05,0.05,'USD','committed',$10,$11
		) RETURNING id`,
		f.tenantID, "idem-"+logicalRequestID, "fingerprint-"+logicalRequestID,
		f.apiKeyID, f.userID, logicalRequestID, f.poolID, f.providerAccountID,
		acquisitionToken, settledAt, settledAt.Add(time.Hour),
	).Scan(&claimID); err != nil {
		t.Fatalf("写入总览 claim %s: %v", logicalRequestID, err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO usage_records (
			tenant_id, claim_id, api_key_id, user_id, provider_account_id,
			acquisition_token, attempt_seq, tokens_input, tokens_output,
			cache_creation_tokens, cache_read_tokens, image_output_tokens,
			actual_cost, input_cost, output_cost, cache_creation_cost, cache_read_cost,
			image_output_cost, end_class, usage_source, pending_reconciliation,
			requested_at, settled_at, requested_model, upstream_model, stream,
			settlement_source
		) VALUES (
			$1,$2,$3,$4,$5,$6,1,30,40,5,7,2,0.05,0.01,0.02,0.003,0.004,
			0.005,'non_streaming','reported',false,$7,$8,'claude-overview',
			'claude-overview',false,'provider_upstream'
		)`,
		f.tenantID, claimID, f.apiKeyID, f.userID, f.providerAccountID,
		acquisitionToken, settledAt.Add(-time.Second), settledAt,
	); err != nil {
		t.Fatalf("写入总览 usage %s: %v", logicalRequestID, err)
	}
}

func seedUsageResponseCacheHitRecord(t *testing.T, ctx context.Context, tx pgx.Tx, f usageOutcomeFixture, logicalRequestID string, settledAt time.Time) {
	t.Helper()
	var claimID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO billing_ledger_claims (
			tenant_id, idempotency_key, request_fingerprint, api_key_id, user_id,
			logical_request_id, endpoint_family, requested_model, attempt_seq,
			billing_policy_version, request_class, predicted_cost, actual_cost,
			currency_code, status, settled_at, lease_expires_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,'messages','claude-l2',1,'bp-test','standard',
			0,0,'USD','committed',$7,$8
		) RETURNING id`,
		f.tenantID, "idem-"+logicalRequestID, "fingerprint-"+logicalRequestID,
		f.apiKeyID, f.userID, logicalRequestID, settledAt, settledAt.Add(time.Hour),
	).Scan(&claimID); err != nil {
		t.Fatalf("写入 L2 claim %s: %v", logicalRequestID, err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO usage_records (
			tenant_id, claim_id, api_key_id, user_id, attempt_seq,
			tokens_input, tokens_output, cache_creation_tokens, cache_read_tokens,
			actual_cost, input_cost, output_cost, cache_creation_cost, cache_read_cost,
			end_class, usage_source, pending_reconciliation, requested_at, settled_at,
			requested_model, stream, settlement_source
		) VALUES (
			$1,$2,$3,$4,1,8,2,99,88,0,0,0,0,0,'non_streaming','reported',false,
			$5,$6,'claude-l2',false,'response_cache_l2'
		)`,
		f.tenantID, claimID, f.apiKeyID, f.userID, settledAt.Add(-time.Second), settledAt,
	); err != nil {
		t.Fatalf("写入 L2 usage %s: %v", logicalRequestID, err)
	}
}

func floatNear(got, want, tol float64) bool {
	d := got - want
	if d < 0 {
		d = -d
	}
	return d <= tol
}
