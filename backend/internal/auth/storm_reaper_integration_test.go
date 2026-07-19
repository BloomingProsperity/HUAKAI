// HUAKAI · iKun
//go:build integration_pg

// storm 槽陈旧 reaper 的真 PG 判别测试: release 全败/进程崩溃留下的 in_flight 永久 +1
// (cap=1 时该账号永久无法刷新)必须被 ReapStaleSlots 归零; 新鲜行(活跃刷新)不得误伤。

package auth

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	dbauth "github.com/BloomingProsperity/HUAKAI/internal/db/auth"
)

// TestPG_StormReapStaleSlots 判别三点:
//  1. 陈旧泄漏行 (in_flight=1, last_updated_at 早于阈值) → 归零;
//  2. 新鲜行 (刚 acquire) → 不动 (误伤=活跃刷新被并发放大);
//  3. 归零后账号可再次 Acquire (自愈闭环)。
//
// mutation: ReapStaleAccountStormSlots 去掉 last_updated_at 条件 → 新鲜行也被归零 →
// 断言 2 红; 去掉整个 reaper 调用链 → 断言 1/3 红。
func TestPG_StormReapStaleSlots(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	queries := dbauth.New(pool)
	c := NewStormControllerWithSharedScopeBudget(queries, StormScopeConfig{}, nil)

	var tenantID int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"storm-reap-"+uuid.NewString()).Scan(&tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	unique := uuid.NewString()
	var providerID, poolGroupID, channelID int64
	if err := pool.QueryRow(ctx, `INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
VALUES ($1, $2, $3, 'openai_chat') RETURNING id`, tenantID, "p-"+unique, "P "+unique).Scan(&providerID); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		tenantID, "pg-"+unique).Scan(&poolGroupID); err != nil {
		t.Fatalf("seed pool group: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`,
		tenantID, poolGroupID, "ch-"+unique).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	var staleAcct, freshAcct int64
	for label, dst := range map[string]*int64{"stale": &staleAcct, "fresh": &freshAcct} {
		if err := pool.QueryRow(ctx, `
INSERT INTO provider_accounts (tenant_id, provider_id, channel_id, name, account_type)
VALUES ($1, $2, $3, $4, 'api_key') RETURNING id`,
			tenantID, providerID, channelID, "storm-"+label+"-"+unique).Scan(dst); err != nil {
			t.Fatalf("seed account %s: %v", label, err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM oauth_storm_budget WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM provider_accounts WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM channels WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM pool_groups WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM providers WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM tenants WHERE id=$1`, tenantID)
	})

	// 两账号各占一槽 (cap 默认 1); stale 的 release 永不发生 (模拟泄漏)。
	if release, outcome, err := c.Acquire(ctx, tenantID, staleAcct); err != nil || outcome != "" || release == nil {
		t.Fatalf("acquire stale: err=%v outcome=%q", err, outcome)
	}
	if release, outcome, err := c.Acquire(ctx, tenantID, freshAcct); err != nil || outcome != "" || release == nil {
		t.Fatalf("acquire fresh: err=%v outcome=%q", err, outcome)
	}
	// 泄漏坐实: stale 账号再取被拒 (cap=1 已满)。
	if _, outcome, err := c.Acquire(ctx, tenantID, staleAcct); err != nil || outcome != OutcomeStormBudgetExhausted {
		t.Fatalf("leaked slot 应拒绝再取: err=%v outcome=%q", err, outcome)
	}

	// 把 stale 行的 last_updated_at 拨旧到阈值外; fresh 保持新鲜。
	if _, err := pool.Exec(ctx, `
UPDATE oauth_storm_budget SET last_updated_at = NOW() - interval '1 hour'
WHERE tenant_id=$1 AND provider_account_id=$2`, tenantID, staleAcct); err != nil {
		t.Fatalf("age stale row: %v", err)
	}

	reaped, err := c.ReapStaleSlots(ctx, time.Now().Add(-15*time.Minute))
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if reaped < 1 {
		t.Fatalf("reaped=%d, want >=1 (陈旧泄漏未自愈)", reaped)
	}

	var staleFlight, freshFlight int32
	if err := pool.QueryRow(ctx, `SELECT current_in_flight FROM oauth_storm_budget WHERE tenant_id=$1 AND provider_account_id=$2`, tenantID, staleAcct).Scan(&staleFlight); err != nil {
		t.Fatalf("read stale: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT current_in_flight FROM oauth_storm_budget WHERE tenant_id=$1 AND provider_account_id=$2`, tenantID, freshAcct).Scan(&freshFlight); err != nil {
		t.Fatalf("read fresh: %v", err)
	}
	if staleFlight != 0 {
		t.Fatalf("stale in_flight=%d, want 0 (泄漏未归零, cap=1 即死号)", staleFlight)
	}
	if freshFlight != 1 {
		t.Fatalf("fresh in_flight=%d, want 1 (活跃刷新被误伤, 并发被放大)", freshFlight)
	}

	// 自愈闭环: stale 账号可再次取到槽。
	if release, outcome, err := c.Acquire(ctx, tenantID, staleAcct); err != nil || outcome != "" || release == nil {
		t.Fatalf("post-reap acquire: err=%v outcome=%q (自愈未闭环)", err, outcome)
	} else {
		release()
	}
}
