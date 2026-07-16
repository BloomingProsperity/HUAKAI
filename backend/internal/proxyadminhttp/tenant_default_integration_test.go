//go:build integration_pg

package proxyadminhttp

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/proxyadmin"
)

type tenantDefaultResolverFixture struct {
	tenantID          int64
	providerID        int64
	poolGroupID       int64
	channelID         int64
	providerAccountID int64
	proxyID           int64
}

func openTenantDefaultIntegrationPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL 未设置，跳过 integration_pg")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("打开 PostgreSQL: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedTenantDefaultResolverFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) tenantDefaultResolverFixture {
	t.Helper()
	suffix := uuid.NewString()
	var f tenantDefaultResolverFixture
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "tenant-default-"+suffix).Scan(&f.tenantID); err != nil {
		t.Fatalf("插入租户: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
		VALUES ($1, $2, $3, 'openai_chat') RETURNING id`,
		f.tenantID, "tenant-default-"+suffix, "Tenant Default "+suffix,
	).Scan(&f.providerID); err != nil {
		t.Fatalf("插入 provider: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		f.tenantID, "tenant-default-pool-"+suffix).Scan(&f.poolGroupID); err != nil {
		t.Fatalf("插入池组: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`,
		f.tenantID, f.poolGroupID, "tenant-default-channel-"+suffix).Scan(&f.channelID); err != nil {
		t.Fatalf("插入 channel: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO provider_accounts (tenant_id, provider_id, channel_id, name, account_type, credentials)
		VALUES ($1, $2, $3, $4, 'api_key', '{}'::jsonb) RETURNING id`,
		f.tenantID, f.providerID, f.channelID, "tenant-default-account-"+suffix,
	).Scan(&f.providerAccountID); err != nil {
		t.Fatalf("插入 provider account: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO proxies (tenant_id, name, protocol, host, port, status)
		VALUES ($1, $2, 'http', 'tenant-default.example', 3128, 'active') RETURNING id`,
		f.tenantID, "tenant-default-proxy-"+suffix,
	).Scan(&f.proxyID); err != nil {
		t.Fatalf("插入 proxy: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM admin_audit_events WHERE tenant_id=$1`, f.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM provider_accounts WHERE id=$1`, f.providerAccountID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM proxies WHERE id=$1`, f.proxyID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM channels WHERE id=$1`, f.channelID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM pool_groups WHERE id=$1`, f.poolGroupID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM providers WHERE id=$1`, f.providerID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM tenants WHERE id=$1`, f.tenantID)
	})
	return f
}

// TestTenantDefaultProxyWritePortFeedsResolverPostgres 通过真实 HTTP 写口、真实 PostgreSQL
// 列与既有 resolver，证明管理面写入不是死列。变异：漏 UPDATE default_proxy_id、写错租户、
// 或路由漏注入 store，resolver 都不会得到精确代理 URL，本测试转红。
func TestTenantDefaultProxyWritePortFeedsResolverPostgres(t *testing.T) {
	ctx := context.Background()
	pool := openTenantDefaultIntegrationPool(t, ctx)
	fixture := seedTenantDefaultResolverFixture(t, ctx, pool)
	d := Deps{
		Auth:           authStub{ident: tenantOperator(fixture.tenantID)},
		TenantDefaults: proxyadmin.NewPostgresTenantDefaultStore(pool),
	}
	rec := invokeTenantDefault(t, d, http.MethodPut,
		fmt.Sprintf("/admin/v1/tenants/%d/default-proxy", fixture.tenantID),
		fmt.Sprintf(`{"proxy_id":%d}`, fixture.proxyID),
	)
	assertStatus(t, rec, http.StatusOK)
	getRec := invokeTenantDefault(t, d, http.MethodGet,
		fmt.Sprintf("/admin/v1/tenants/%d/default-proxy", fixture.tenantID), "")
	assertStatus(t, getRec, http.StatusOK)
	var readBack struct {
		ProxyID *int64 `json:"proxy_id"`
	}
	decodeBody(t, getRec, &readBack)
	if readBack.ProxyID == nil || *readBack.ProxyID != fixture.proxyID {
		t.Fatalf("真实 GET proxy_id=%v want %d", readBack.ProxyID, fixture.proxyID)
	}

	var stored *int64
	if err := pool.QueryRow(ctx, `SELECT default_proxy_id FROM tenants WHERE id=$1`, fixture.tenantID).Scan(&stored); err != nil {
		t.Fatalf("读取 default_proxy_id: %v", err)
	}
	if stored == nil || *stored != fixture.proxyID {
		t.Fatalf("default_proxy_id=%v want %d", stored, fixture.proxyID)
	}

	resolved, err := provider.NewPostgresProxyResolver(pool).Resolve(ctx, fixture.providerAccountID)
	if err != nil {
		t.Fatalf("resolver 解析租户默认出口: %v", err)
	}
	if resolved == nil || resolved.Scheme != "http" || resolved.Host != "tenant-default.example:3128" {
		t.Fatalf("resolver URL=%v want http://tenant-default.example:3128", resolved)
	}

	var action, targetType, setting, after string
	var targetID *int64
	if err := pool.QueryRow(ctx, `
		SELECT action, target_type, target_id, payload->>'setting', payload->>'after_proxy_id'
		FROM admin_audit_events
		WHERE tenant_id=$1 AND request_id='req-tenant-default'
		ORDER BY id DESC LIMIT 1`, fixture.tenantID,
	).Scan(&action, &targetType, &targetID, &setting, &after); err != nil {
		t.Fatalf("读取租户默认出口审计: %v", err)
	}
	if action != "update_platform_settings" || targetType != "tenant" || targetID == nil || *targetID != fixture.tenantID ||
		setting != "default_proxy_id" || after != fmt.Sprint(fixture.proxyID) {
		t.Fatalf("审计字段漂移 action=%q target=%q targetID=%v setting=%q after=%q", action, targetType, targetID, setting, after)
	}
}
