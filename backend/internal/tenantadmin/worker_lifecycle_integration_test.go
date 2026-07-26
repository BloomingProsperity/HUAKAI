//go:build integration_pg

package tenantadmin

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/accountmodeldiscovery"
	"github.com/BloomingProsperity/HUAKAI/internal/autolisting"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/proxyhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/quotaprobe"
	"github.com/BloomingProsperity/HUAKAI/internal/tlsfphealth"
)

// TestTenantLifecycleStopsAndRestartsAllProactiveWorkers 守住租户生命周期的主动任务合同：
// 正常租户的凭据轮换、凭据刷新、额度探测、代理探测、TLS 档案校验和账号模型保鲜都可见；
// 停用后六条链同时消失，恢复后又同时回来。
//
// 判别性：任意一个生产查询漏掉 tenants.status='active' 连接条件，停用阶段对应布尔值就会
// 变成 true；任意一个恢复条件写死或读了旧快照，恢复阶段就会保持 false。
func TestTenantLifecycleStopsAndRestartsAllProactiveWorkers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool := openTenantWorkerLifecyclePool(t, ctx)
	fixture := seedTenantWorkerLifecycleFixture(t, ctx, pool)
	syncer := &tenantWorkerSyncSpy{}

	active := readTenantWorkerVisibility(t, ctx, pool, fixture, syncer)
	assertTenantWorkerVisibility(t, "active", active, true)

	setTenantWorkerLifecycleStatus(t, ctx, pool, fixture.tenantID, "disabled")
	disabled := readTenantWorkerVisibility(t, ctx, pool, fixture, syncer)
	assertTenantWorkerVisibility(t, "disabled", disabled, false)

	setTenantWorkerLifecycleStatus(t, ctx, pool, fixture.tenantID, "active")
	recovered := readTenantWorkerVisibility(t, ctx, pool, fixture, syncer)
	assertTenantWorkerVisibility(t, "recovered", recovered, true)
}

type tenantWorkerLifecycleFixture struct {
	tenantID          int64
	poolGroupID       int64
	channelID         int64
	providerID        int64
	providerAccountID int64
	credentialID      int64
	proxyID           int64
	tlsProfileID      int64
}

type tenantWorkerVisibility struct {
	rotationDue       bool
	credentialRefresh bool
	quotaProbe        bool
	proxyProbe        bool
	tlsValidation     bool
	modelAutolisting  bool
}

type tenantWorkerSyncSpy struct {
	inputs []accountmodeldiscovery.SyncInput
}

func (s *tenantWorkerSyncSpy) Sync(_ context.Context, in accountmodeldiscovery.SyncInput) (accountmodeldiscovery.SyncResult, error) {
	s.inputs = append(s.inputs, in)
	return accountmodeldiscovery.SyncResult{InboxInvested: 1}, nil
}

func (s *tenantWorkerSyncSpy) reset() {
	s.inputs = nil
}

func (s *tenantWorkerSyncSpy) saw(tenantID, accountID int64) bool {
	for _, in := range s.inputs {
		if in.TenantID == tenantID && in.AccountID == accountID {
			return true
		}
	}
	return false
}

func openTenantWorkerLifecyclePool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL 未设置，跳过 integration_pg")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("连接 PostgreSQL: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("探测 PostgreSQL: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedTenantWorkerLifecycleFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) tenantWorkerLifecycleFixture {
	t.Helper()
	suffix := fmt.Sprintf("tenant-worker-%d", time.Now().UnixNano())
	var fixture tenantWorkerLifecycleFixture

	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		suffix,
	).Scan(&fixture.tenantID); err != nil {
		t.Fatalf("创建租户: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM account_credentials WHERE tenant_id = $1`, fixture.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM provider_accounts WHERE tenant_id = $1`, fixture.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM tls_fingerprint_profiles WHERE tenant_id = $1`, fixture.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM proxies WHERE tenant_id = $1`, fixture.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM providers WHERE tenant_id = $1`, fixture.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM channels WHERE tenant_id = $1`, fixture.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM pool_groups WHERE tenant_id = $1`, fixture.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM tenants WHERE id = $1`, fixture.tenantID)
	})

	if err := pool.QueryRow(ctx,
		`INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		fixture.tenantID, suffix+"-pool",
	).Scan(&fixture.poolGroupID); err != nil {
		t.Fatalf("创建池组: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`,
		fixture.tenantID, fixture.poolGroupID, suffix+"-channel",
	).Scan(&fixture.channelID); err != nil {
		t.Fatalf("创建渠道: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
		 VALUES ($1, $2, $3, 'anthropic_messages') RETURNING id`,
		fixture.tenantID, suffix+"-provider", suffix+"-provider",
	).Scan(&fixture.providerID); err != nil {
		t.Fatalf("创建供应商: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO proxies (tenant_id, name, protocol, host, port)
		 VALUES ($1, $2, 'http', '127.0.0.1', 18080) RETURNING id`,
		fixture.tenantID, suffix+"-proxy",
	).Scan(&fixture.proxyID); err != nil {
		t.Fatalf("创建代理: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO tls_fingerprint_profiles (tenant_id, name)
		 VALUES ($1, $2) RETURNING id`,
		fixture.tenantID, suffix+"-tls",
	).Scan(&fixture.tlsProfileID); err != nil {
		t.Fatalf("创建 TLS 档案: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO provider_accounts (tenant_id, provider_id, channel_id, name, account_type)
		 VALUES ($1, $2, $3, $4, 'oauth') RETURNING id`,
		fixture.tenantID, fixture.providerID, fixture.channelID, suffix+"-account",
	).Scan(&fixture.providerAccountID); err != nil {
		t.Fatalf("创建上游账号: %v", err)
	}

	now := time.Now().UTC()
	if err := pool.QueryRow(ctx, `
		INSERT INTO account_credentials (
			tenant_id, provider_account_id, vendor, auth_mode, state,
			encrypted_payload, key_id, nonce, aad_hash,
			refresh_before_at, created_at
		)
		VALUES ($1, $2, 'anthropic', 'claude_ai_oauth', 'active',
			$3, $4, $5, $6, $7, $8)
		RETURNING id`,
		fixture.tenantID,
		fixture.providerAccountID,
		[]byte("ciphertext-"+suffix),
		"key-"+suffix,
		[]byte("nonce-"+suffix),
		"aad-"+suffix,
		now.Add(-time.Hour),
		now.Add(-200*24*time.Hour),
	).Scan(&fixture.credentialID); err != nil {
		t.Fatalf("创建凭据: %v", err)
	}
	return fixture
}

func setTenantWorkerLifecycleStatus(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64, status string) {
	t.Helper()
	tag, err := pool.Exec(ctx, `
		UPDATE tenants
		SET status = $2,
		    status_reason = $3,
		    status_changed_at = now(),
		    status_changed_by = 'integration-test',
		    updated_at = now(),
		    version = version + 1
		WHERE id = $1`,
		tenantID, status, "主动 worker 生命周期判别测试")
	if err != nil {
		t.Fatalf("切换租户状态到 %s: %v", status, err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("切换租户状态到 %s 影响 %d 行，期望 1", status, tag.RowsAffected())
	}
}

func readTenantWorkerVisibility(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	fixture tenantWorkerLifecycleFixture,
	syncer *tenantWorkerSyncSpy,
) tenantWorkerVisibility {
	t.Helper()
	now := time.Now().UTC()
	visibility := tenantWorkerVisibility{}

	rotationRows, err := credentialworker.NewPostgresRotationStore(pool).DueForRotation(
		ctx, now.Add(-90*24*time.Hour), 100_000,
	)
	if err != nil {
		t.Fatalf("读取凭据轮换候选: %v", err)
	}
	for _, row := range rotationRows {
		if row.CredentialID == fixture.credentialID {
			visibility.rotationDue = true
			break
		}
	}

	refreshRows, err := credentialworker.NewAccountCredentialRefreshQueries(pool).ListAccountsForRefresh(
		ctx,
		dbbilling.ListAccountsForRefreshParams{
			RefreshBefore: pgtype.Timestamptz{Time: now.Add(time.Hour), Valid: true},
			LimitCount:    100_000,
		},
	)
	if err != nil {
		t.Fatalf("读取凭据刷新候选: %v", err)
	}
	for _, row := range refreshRows {
		if row.TenantID == fixture.tenantID && row.ID == fixture.providerAccountID {
			visibility.credentialRefresh = true
			break
		}
	}

	quotaRows, err := quotaprobe.NewPostgresAccountLister(pool).ListQuotaProbeAccounts(ctx)
	if err != nil {
		t.Fatalf("读取额度探测候选: %v", err)
	}
	for _, row := range quotaRows {
		if row.TenantID == fixture.tenantID && row.ProviderAccountID == fixture.providerAccountID {
			visibility.quotaProbe = true
			break
		}
	}

	proxyRows, err := proxyhealth.NewPostgresLister(pool).List(ctx)
	if err != nil {
		t.Fatalf("读取代理探测候选: %v", err)
	}
	for _, row := range proxyRows {
		if row.TenantID == fixture.tenantID && row.ID == fixture.proxyID {
			visibility.proxyProbe = true
			break
		}
	}

	tlsRows, err := tlsfphealth.NewPostgresLister(pool).ListActive(ctx)
	if err != nil {
		t.Fatalf("读取 TLS 档案校验候选: %v", err)
	}
	for _, row := range tlsRows {
		if row.TenantID == fixture.tenantID && row.ID == fixture.tlsProfileID {
			visibility.tlsValidation = true
			break
		}
	}

	syncer.reset()
	if _, err := autolisting.NewAccountRefresher(pool, syncer).RefreshReversedAccounts(ctx); err != nil {
		t.Fatalf("执行账号模型保鲜扫描: %v", err)
	}
	visibility.modelAutolisting = syncer.saw(fixture.tenantID, fixture.providerAccountID)
	return visibility
}

func assertTenantWorkerVisibility(t *testing.T, phase string, got tenantWorkerVisibility, want bool) {
	t.Helper()
	checks := []struct {
		name string
		got  bool
	}{
		{name: "credential_rotation", got: got.rotationDue},
		{name: "credential_refresh", got: got.credentialRefresh},
		{name: "quota_probe", got: got.quotaProbe},
		{name: "proxy_probe", got: got.proxyProbe},
		{name: "tls_validation", got: got.tlsValidation},
		{name: "model_autolisting", got: got.modelAutolisting},
	}
	for _, check := range checks {
		if check.got != want {
			t.Errorf("%s 阶段 %s 可见性=%t，期望 %t；租户生命周期过滤未闭环", phase, check.name, check.got, want)
		}
	}
}
