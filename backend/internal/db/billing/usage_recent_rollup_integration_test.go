//go:build integration_pg

package billing

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
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

func floatNear(got, want, tol float64) bool {
	d := got - want
	if d < 0 {
		d = -d
	}
	return d <= tol
}
