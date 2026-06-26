//go:build integration_pg

// riskoverviewhttp 的 PostgresStore 真 PG 集成测试。重点证两件事(handler 单测覆盖鉴权层,
// 这里覆盖**数据查询层**):
//   - 4 个 COUNT 各自只数符合条件的行(status/state/ip_blacklist 过滤正确,非匹配行不计入);
//   - **跨租户隔离**:租户 A 的 Overview 绝不计入租户 B 的封禁/告警(WHERE tenant_id 真生效),
//     这是 auditexporthttp 那次 IDOR S0 在数据层的对应防线。
package riskoverviewhttp

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

func openPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL 未设置;跳过集成测试")
	}
	p, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

// signalCounts 描述要给某租户 seed 的各风控信号行数。
type signalCounts struct {
	disabledKeys      int
	firingAlerts      int
	disabledUsers     int
	ipBlacklistedKeys int
	// 噪声行(不应被任何 COUNT 计入):活跃 key + 活跃 user + 已 resolved 告警 + 无黑名单 key。
	activeKeys      int
	activeUsers     int
	resolvedAlerts  int
	cleanKeys       int
}

func seedTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, c signalCounts) int64 {
	t.Helper()
	suffix := uuid.NewString()
	var tenantID int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "risk-"+suffix).Scan(&tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM alert_events WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM alert_rules WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM api_keys WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM users WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM tenants WHERE id=$1`, tenantID)
	})

	seedUser := func(status string) int64 {
		var uid int64
		if err := pool.QueryRow(ctx, `INSERT INTO users (tenant_id, display_name) VALUES ($1,$2) RETURNING id`,
			tenantID, "u-"+uuid.NewString()).Scan(&uid); err != nil {
			t.Fatalf("seed user: %v", err)
		}
		if status != "active" {
			if _, err := pool.Exec(ctx, `UPDATE users SET status=$2 WHERE id=$1`, uid, status); err != nil {
				t.Fatalf("set user status: %v", err)
			}
		}
		return uid
	}
	// 一个承载所有 key 的 user(active,避免污染 disabledUsers 计数——故单独建一个 active user)。
	keyOwner := seedUser("active")
	seedKey := func(status string, ipBlacklist *string) {
		ks := uuid.NewString()
		var kid int64
		if err := pool.QueryRow(ctx,
			`INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status, expires_at)
			 VALUES ($1,$2,$3,$4,$5,$6,NULL) RETURNING id`,
			tenantID, keyOwner, "k-"+ks, "hash-"+ks, ks[:12], status,
		).Scan(&kid); err != nil {
			t.Fatalf("seed api_key: %v", err)
		}
		if ipBlacklist != nil {
			if _, err := pool.Exec(ctx, `UPDATE api_keys SET ip_blacklist=$2 WHERE id=$1`, kid, *ipBlacklist); err != nil {
				t.Fatalf("set ip_blacklist: %v", err)
			}
		}
	}
	seedAlert := func(state string) {
		var ruleID int64
		if err := pool.QueryRow(ctx,
			`INSERT INTO alert_rules (tenant_id, name, metric, comparator, threshold, severity, window_seconds)
			 VALUES ($1,$2,'usage.error_rate','gt',0.5,'warning',60) RETURNING id`,
			tenantID, "rule-"+uuid.NewString(),
		).Scan(&ruleID); err != nil {
			t.Fatalf("seed alert_rule: %v", err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO alert_events (tenant_id, rule_id, state, observed_value) VALUES ($1,$2,$3,0.9)`,
			tenantID, ruleID, state,
		); err != nil {
			t.Fatalf("seed alert_event: %v", err)
		}
	}

	bl := "10.0.0.0/8"
	for i := 0; i < c.disabledKeys; i++ {
		seedKey("disabled", nil)
	}
	for i := 0; i < c.ipBlacklistedKeys; i++ {
		seedKey("active", &bl)
	}
	for i := 0; i < c.activeKeys; i++ {
		seedKey("active", nil)
	}
	for i := 0; i < c.cleanKeys; i++ {
		seedKey("active", nil)
	}
	for i := 0; i < c.disabledUsers; i++ {
		seedUser("disabled")
	}
	for i := 0; i < c.activeUsers; i++ {
		seedUser("active")
	}
	for i := 0; i < c.firingAlerts; i++ {
		seedAlert("firing")
	}
	for i := 0; i < c.resolvedAlerts; i++ {
		seedAlert("resolved")
	}
	return tenantID
}

func TestPostgresOverviewCountsAndTenantIsolation(t *testing.T) {
	ctx := context.Background()
	pool := openPool(t, ctx)
	store := NewPostgresStore(pool)

	// 租户 A:每个信号 1 行 + 各类噪声行(噪声绝不应被计入)。
	tenantA := seedTenant(t, ctx, pool, signalCounts{
		disabledKeys: 1, firingAlerts: 1, disabledUsers: 1, ipBlacklistedKeys: 1,
		activeKeys: 2, activeUsers: 2, resolvedAlerts: 2, cleanKeys: 2,
	})
	// 租户 B:故意用**不同**计数(2),以证 A 的查询绝不会把 B 的行算进来。
	tenantB := seedTenant(t, ctx, pool, signalCounts{
		disabledKeys: 2, firingAlerts: 2, disabledUsers: 2, ipBlacklistedKeys: 2,
	})

	ovA, err := store.Overview(ctx, tenantA)
	if err != nil {
		t.Fatalf("Overview(A): %v", err)
	}
	// A 必须各为 1:噪声行(活跃 key/user、resolved 告警、无黑名单 key)被过滤,B 的行被租户隔离挡住。
	if ovA != (Overview{DisabledKeys: 1, FiringAlerts: 1, DisabledUsers: 1, IPBlacklistedKeys: 1}) {
		t.Fatalf("租户 A Overview=%+v,期望全 1(噪声与他租户均不计入)", ovA)
	}

	ovB, err := store.Overview(ctx, tenantB)
	if err != nil {
		t.Fatalf("Overview(B): %v", err)
	}
	if ovB != (Overview{DisabledKeys: 2, FiringAlerts: 2, DisabledUsers: 2, IPBlacklistedKeys: 2}) {
		t.Fatalf("租户 B Overview=%+v,期望全 2", ovB)
	}
}
