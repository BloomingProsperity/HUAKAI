//go:build integration_pg

package accountquota_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/accountquota"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/routingsignal"
)

func TestQuotaSnapshotsAndRoutingSignalsUseSharedDatabaseFacts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openQuotaFactPool(t, ctx)
	tenantID, accountID := seedQuotaFactAccount(t, ctx, pool)
	cleanupQuotaFactTenant(t, pool, tenantID)

	store := accountquota.NewPostgresStore(pool)
	t0 := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	remainingA, remainingB := 80.0, 40.0
	if err := store.ReplaceSnapshot(ctx, accountquota.Snapshot{
		TenantID: tenantID, ProviderAccountID: accountID, Vendor: "grok",
		Source: accountquota.SourceUpstreamBilling, ObservedAt: t0, Complete: true,
		Facts: []accountquota.Fact{
			{MetricKey: "weekly", State: accountquota.StateAvailable, RemainingPercent: &remainingA},
			{MetricKey: "monthly", State: accountquota.StateAvailable, RemainingPercent: &remainingB},
		},
	}); err != nil {
		t.Fatalf("写完整快照: %v", err)
	}

	remainingA = 70
	if err := store.ReplaceSnapshot(ctx, accountquota.Snapshot{
		TenantID: tenantID, ProviderAccountID: accountID, Vendor: "grok",
		Source: accountquota.SourceUpstreamBilling, ObservedAt: t0.Add(time.Minute), Complete: false,
		Facts: []accountquota.Fact{{MetricKey: "weekly", State: accountquota.StateAvailable, RemainingPercent: &remainingA}},
	}); err != nil {
		t.Fatalf("写部分快照: %v", err)
	}
	assertQuotaFact(t, ctx, pool, tenantID, accountID, "weekly", 70)
	assertQuotaFact(t, ctx, pool, tenantID, accountID, "monthly", 40)

	if err := store.RecordFailure(ctx, accountquota.Snapshot{
		TenantID: tenantID, ProviderAccountID: accountID, Vendor: "grok",
		Source: accountquota.SourceUpstreamBilling, ObservedAt: t0.Add(2 * time.Minute),
	}, "upstream_partial_response"); err != nil {
		t.Fatalf("写失败事实: %v", err)
	}
	assertQuotaFact(t, ctx, pool, tenantID, accountID, "weekly", 70)
	var failureClass string
	if err := pool.QueryRow(ctx, `SELECT error_class FROM provider_account_quota_facts WHERE tenant_id=$1 AND provider_account_id=$2 AND metric_key='probe_status'`, tenantID, accountID).Scan(&failureClass); err != nil || failureClass != "upstream_partial_response" {
		t.Fatalf("失败事实 class=%q err=%v", failureClass, err)
	}

	remainingA = 60
	if err := store.ReplaceSnapshot(ctx, accountquota.Snapshot{
		TenantID: tenantID, ProviderAccountID: accountID, Vendor: "grok",
		Source: accountquota.SourceUpstreamBilling, ObservedAt: t0.Add(3 * time.Minute), Complete: true,
		Facts: []accountquota.Fact{{MetricKey: "weekly", State: accountquota.StateAvailable, RemainingPercent: &remainingA}},
	}); err != nil {
		t.Fatalf("再次写完整快照: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM provider_account_quota_facts WHERE tenant_id=$1 AND provider_account_id=$2 AND metric_key IN ('monthly','probe_status')`, tenantID, accountID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("完整替换后旧维度 count=%d err=%v", count, err)
	}
	adminQueries := admindb.New(pool)
	rows, err := adminQueries.ListAdminProviderAccounts(ctx, admindb.ListAdminProviderAccountsParams{TenantID: tenantID, LimitCount: 10})
	if err != nil || len(rows) != 1 {
		t.Fatalf("账号列表额度投影 len=%d err=%v", len(rows), err)
	}
	listFacts, err := accountquota.ParseView(rows[0].QuotaFacts, t0.Add(3*time.Minute))
	if err != nil || len(listFacts) != 1 || listFacts[0].MetricKey != "weekly" {
		t.Fatalf("账号列表额度事实=%+v err=%v", listFacts, err)
	}
	health, err := adminQueries.GetAdminProviderAccountHealth(ctx, admindb.GetAdminProviderAccountHealthParams{TenantID: tenantID, ID: accountID})
	if err != nil {
		t.Fatalf("健康详情额度投影: %v", err)
	}
	healthFacts, err := accountquota.ParseView([]byte(health.QuotaFacts), t0.Add(3*time.Minute))
	if err != nil || len(healthFacts) != 1 || healthFacts[0].MetricKey != "weekly" {
		t.Fatalf("健康详情额度事实=%+v err=%v", healthFacts, err)
	}

	recorder := routingsignal.NewPostgresRecorder(pool)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := recorder.RecordRoutingSignal(context.Background(), routingsignal.Observation{
				TenantID: tenantID, ProviderAccountID: accountID, Success: true,
				Latency: time.Duration(10+i) * time.Millisecond, LatencyValid: true, ObservedAt: t0.Add(time.Duration(i) * time.Second),
			}); err != nil {
				t.Errorf("并发写路由信号: %v", err)
			}
		}(i)
	}
	wg.Wait()
	var samples int64
	var success, failure, latency float64
	if err := pool.QueryRow(ctx, `SELECT sample_count, success_ewma, error_ewma, response_latency_ms_ewma FROM provider_account_routing_signals WHERE tenant_id=$1 AND provider_account_id=$2`, tenantID, accountID).Scan(&samples, &success, &failure, &latency); err != nil {
		t.Fatalf("读取路由信号: %v", err)
	}
	if samples != 20 || success != 1 || failure != 0 || latency < 10 || latency > 29 {
		t.Fatalf("路由信号 samples=%d success=%f failure=%f latency=%f", samples, success, failure, latency)
	}
}

func openQuotaFactPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL 未设置，跳过 integration_pg")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn, MaxConns: 24})
	if err != nil {
		t.Fatalf("连接测试数据库: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedQuotaFactAccount(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (int64, int64) {
	t.Helper()
	suffix := uuid.NewString()
	var tenantID, providerID, groupID, channelID, accountID int64
	queries := []struct {
		sql  string
		args []any
		dest *int64
	}{
		{`INSERT INTO tenants (name) VALUES ($1) RETURNING id`, []any{"quota-fact-" + suffix}, &tenantID},
	}
	for _, query := range queries {
		if err := pool.QueryRow(ctx, query.sql, query.args...).Scan(query.dest); err != nil {
			t.Fatalf("准备测试租户: %v", err)
		}
	}
	if err := pool.QueryRow(ctx, `INSERT INTO providers (tenant_id, code, display_name, upstream_protocol) VALUES ($1,$2,$3,'openai_chat') RETURNING id`, tenantID, "quota-"+suffix, "Quota "+suffix).Scan(&providerID); err != nil {
		t.Fatalf("准备 provider: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO pool_groups (tenant_id, name) VALUES ($1,$2) RETURNING id`, tenantID, "quota-"+suffix).Scan(&groupID); err != nil {
		t.Fatalf("准备 pool group: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1,$2,$3) RETURNING id`, tenantID, groupID, "quota-"+suffix).Scan(&channelID); err != nil {
		t.Fatalf("准备 channel: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO provider_accounts (tenant_id, provider_id, channel_id, name, account_type) VALUES ($1,$2,$3,$4,'api_key') RETURNING id`, tenantID, providerID, channelID, "quota-"+suffix).Scan(&accountID); err != nil {
		t.Fatalf("准备 provider account: %v", err)
	}
	return tenantID, accountID
}

func assertQuotaFact(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, accountID int64, metric string, want float64) {
	t.Helper()
	var got float64
	if err := pool.QueryRow(ctx, `SELECT remaining_percent::double precision FROM provider_account_quota_facts WHERE tenant_id=$1 AND provider_account_id=$2 AND metric_key=$3`, tenantID, accountID, metric).Scan(&got); err != nil || got != want {
		t.Fatalf("额度事实 %s=%f want=%f err=%v", metric, got, want, err)
	}
}

func cleanupQuotaFactTenant(t *testing.T, pool *pgxpool.Pool, tenantID int64) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, table := range []string{"provider_account_routing_signals", "provider_account_quota_facts", "provider_accounts", "channels", "pool_groups", "providers"} {
			_, _ = pool.Exec(ctx, fmt.Sprintf("DELETE FROM %s WHERE tenant_id=$1", table), tenantID)
		}
		_, _ = pool.Exec(ctx, `DELETE FROM tenants WHERE id=$1`, tenantID)
	})
}
