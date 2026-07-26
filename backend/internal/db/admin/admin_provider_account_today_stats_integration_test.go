//go:build integration_pg

package admin

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestListAdminProviderAccountsProjectsBoundedTodayStats(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openAdminAuditIntegrationPool(t, ctx)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("开始事务: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	suffix := fmt.Sprintf("admin-today-%d", time.Now().UnixNano())
	var tenantID, userID, apiKeyID, providerID, poolGroupID, channelID, accountID int64
	if err := tx.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, suffix).Scan(&tenantID); err != nil {
		t.Fatalf("写入租户: %v", err)
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO users (tenant_id, email, display_name) VALUES ($1,$2,$3) RETURNING id`,
		tenantID, suffix+"@example.test", "今日统计",
	).Scan(&userID); err != nil {
		t.Fatalf("写入用户: %v", err)
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix)
		 VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		tenantID, userID, suffix, "hash-"+suffix, "prefix-"+suffix[:8],
	).Scan(&apiKeyID); err != nil {
		t.Fatalf("写入用户 Key: %v", err)
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
		 VALUES ($1,$2,$3,'anthropic_messages') RETURNING id`,
		tenantID, "provider-"+suffix, "Provider "+suffix,
	).Scan(&providerID); err != nil {
		t.Fatalf("写入平台: %v", err)
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO pool_groups (tenant_id, name) VALUES ($1,$2) RETURNING id`,
		tenantID, "pool-"+suffix,
	).Scan(&poolGroupID); err != nil {
		t.Fatalf("写入池组: %v", err)
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1,$2,$3) RETURNING id`,
		tenantID, poolGroupID, "channel-"+suffix,
	).Scan(&channelID); err != nil {
		t.Fatalf("写入渠道: %v", err)
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO provider_accounts (
			tenant_id, provider_id, channel_id, name, account_type, health_state
		 ) VALUES ($1,$2,$3,$4,'api_key','healthy') RETURNING id`,
		tenantID, providerID, channelID, "account-"+suffix,
	).Scan(&accountID); err != nil {
		t.Fatalf("写入上游账号: %v", err)
	}

	windowStart := time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC)
	observedAt := windowStart.Add(12 * time.Hour)
	insertAdminTodayUsage(t, ctx, tx, adminTodayUsageInput{
		TenantID: tenantID, UserID: userID, APIKeyID: apiKeyID, PoolGroupID: poolGroupID,
		AccountID: accountID, LogicalID: suffix + "-success-100", SettledAt: windowStart.Add(time.Hour),
		EndClass: "non_streaming", TTFT: 100 * time.Millisecond,
	})
	insertAdminTodayUsage(t, ctx, tx, adminTodayUsageInput{
		TenantID: tenantID, UserID: userID, APIKeyID: apiKeyID, PoolGroupID: poolGroupID,
		AccountID: accountID, LogicalID: suffix + "-success-300", SettledAt: windowStart.Add(2 * time.Hour),
		EndClass: "stream_end_graceful", TTFT: 300 * time.Millisecond,
	})
	insertAdminTodayUsage(t, ctx, tx, adminTodayUsageInput{
		TenantID: tenantID, UserID: userID, APIKeyID: apiKeyID, PoolGroupID: poolGroupID,
		AccountID: accountID, LogicalID: suffix + "-failure-500", SettledAt: windowStart.Add(3 * time.Hour),
		EndClass: "upstream_error_5xx", TTFT: 500 * time.Millisecond,
	})
	insertAdminTodayUsage(t, ctx, tx, adminTodayUsageInput{
		TenantID: tenantID, UserID: userID, APIKeyID: apiKeyID, PoolGroupID: poolGroupID,
		AccountID: accountID, LogicalID: suffix + "-old", SettledAt: windowStart.Add(-time.Second),
		EndClass: "upstream_error_5xx", TTFT: 900 * time.Millisecond,
	})
	insertAdminTodayUsage(t, ctx, tx, adminTodayUsageInput{
		TenantID: tenantID, UserID: userID, APIKeyID: apiKeyID, PoolGroupID: poolGroupID,
		AccountID: accountID, LogicalID: suffix + "-future", SettledAt: observedAt.Add(time.Second),
		EndClass: "non_streaming", TTFT: 50 * time.Millisecond,
	})

	rows, err := New(tx).ListAdminProviderAccounts(ctx, ListAdminProviderAccountsParams{
		TenantID: tenantID, LimitCount: 10, StatsSince: windowStart, StatsUntil: observedAt,
	})
	if err != nil {
		t.Fatalf("读取账号今日统计: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("账号数量=%d，期望 1", len(rows))
	}
	got := rows[0]
	if !got.TodayStatsWindowStart.Valid || !got.TodayStatsWindowStart.Time.Equal(windowStart) ||
		!got.TodayStatsObservedAt.Valid || !got.TodayStatsObservedAt.Time.Equal(observedAt) {
		t.Fatalf("统计时间窗=%+v..%+v，期望 %v..%v",
			got.TodayStatsWindowStart, got.TodayStatsObservedAt, windowStart, observedAt)
	}
	if got.TodayRequestCount != 3 || got.TodaySuccessCount != 2 || got.TodayFailureCount != 1 {
		t.Fatalf("今日统计=%d/%d/%d，期望请求/成功/失败=3/2/1",
			got.TodayRequestCount, got.TodaySuccessCount, got.TodayFailureCount)
	}
	if got.TodayTTFTP95MS == nil || *got.TodayTTFTP95MS != 480 {
		t.Fatalf("今日 TTFT P95=%v，期望 480ms；窗口外记录不得参与聚合", got.TodayTTFTP95MS)
	}
}

type adminTodayUsageInput struct {
	TenantID    int64
	UserID      int64
	APIKeyID    int64
	PoolGroupID int64
	AccountID   int64
	LogicalID   string
	SettledAt   time.Time
	EndClass    string
	TTFT        time.Duration
}

func insertAdminTodayUsage(t *testing.T, ctx context.Context, tx DBTX, in adminTodayUsageInput) {
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
			$1,$2,$3,$4,$5,$6,'messages','claude-test',$7,$8,$9,1,'test',
			'standard',0.01,0.01,'USD','committed',$10,$11
		) RETURNING id`,
		in.TenantID, "idem-"+in.LogicalID, "fingerprint-"+in.LogicalID,
		in.APIKeyID, in.UserID, in.LogicalID, in.PoolGroupID, in.AccountID,
		acquisitionToken, in.SettledAt, in.SettledAt.Add(time.Hour),
	).Scan(&claimID); err != nil {
		t.Fatalf("写入 claim %s: %v", in.LogicalID, err)
	}
	requestedAt := in.SettledAt.Add(-time.Second)
	firstByteAt := requestedAt.Add(in.TTFT)
	if _, err := tx.Exec(ctx, `
		INSERT INTO usage_records (
			tenant_id, claim_id, api_key_id, user_id, provider_account_id,
			acquisition_token, attempt_seq, tokens_input, tokens_output, actual_cost,
			input_cost, output_cost, end_class, usage_source, pending_reconciliation,
			requested_at, first_byte_at, settled_at, requested_model, upstream_model,
			stream, settlement_source
		) VALUES (
			$1,$2,$3,$4,$5,$6,1,10,20,0.01,0.004,0.006,$7,'reported',false,
			$8,$9,$10,'claude-test','claude-test',false,'provider_upstream'
		)`,
		in.TenantID, claimID, in.APIKeyID, in.UserID, in.AccountID, acquisitionToken,
		in.EndClass, requestedAt, firstByteAt, in.SettledAt,
	); err != nil {
		t.Fatalf("写入 usage %s: %v", in.LogicalID, err)
	}
}
