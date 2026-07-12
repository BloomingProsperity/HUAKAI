//go:build integration_pg

// SummarizeProviderAccountHealth 真 PG 验证:按 (health_state, enabled) 跨整个租户池计数。
//
// mutation 自检:
//   - 去掉 WHERE tenant_id 谓词 → 会混入他租户账号 → 计数变大 → red。
//   - 去掉 deleted_at IS NULL → 软删账号被计入 → red。
//   - GROUP BY 少一维 → enabled 维丢失 → red。

package admin

import (
	"context"
	"strconv"
	"testing"
	"time"
)

func TestSummarizeProviderAccountHealthGroupsByStateAndEnabled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openAdminAuditIntegrationPool(t, ctx)
	q := New(pool)

	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	var tenantID, providerID, poolGroupID, channelID int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "hs-summary-"+suffix).Scan(&tenantID); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	// 另一个租户,用于验证租户隔离(其账号不得计入)。
	var otherTenant int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "hs-other-"+suffix).Scan(&otherTenant); err != nil {
		t.Fatalf("insert other tenant: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		for _, tid := range []int64{tenantID, otherTenant} {
			_, _ = pool.Exec(c, `DELETE FROM provider_accounts WHERE tenant_id = $1`, tid)
			_, _ = pool.Exec(c, `DELETE FROM channels WHERE tenant_id = $1`, tid)
			_, _ = pool.Exec(c, `DELETE FROM pool_groups WHERE tenant_id = $1`, tid)
			_, _ = pool.Exec(c, `DELETE FROM providers WHERE tenant_id = $1`, tid)
			_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id = $1`, tid)
		}
	})
	if err := pool.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol) VALUES ($1, $2, 'HS Provider', 'openai_chat') RETURNING id`,
		tenantID, "hs-"+suffix).Scan(&providerID); err != nil {
		t.Fatalf("insert provider: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`, tenantID, "hs-pool-"+suffix).Scan(&poolGroupID); err != nil {
		t.Fatalf("insert pool group: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`, tenantID, poolGroupID, "hs-ch-"+suffix).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}

	insert := func(name, healthState string, enabled bool, deleted bool) {
		var id int64
		if err := pool.QueryRow(ctx,
			`INSERT INTO provider_accounts (tenant_id, provider_id, channel_id, name, account_type, enabled, health_state)
			 VALUES ($1, $2, $3, $4, 'api_key', $5, $6) RETURNING id`,
			tenantID, providerID, channelID, name+"-"+suffix, enabled, healthState,
		).Scan(&id); err != nil {
			t.Fatalf("insert %s/%s: %v", name, healthState, err)
		}
		if deleted {
			if _, err := pool.Exec(ctx, `UPDATE provider_accounts SET deleted_at = now() WHERE id = $1`, id); err != nil {
				t.Fatalf("soft-delete %s: %v", name, err)
			}
		}
	}
	insert("op1", "healthy", true, false)
	insert("op2", "healthy", true, false)
	insert("opoff", "healthy", false, false) // 停用的健康账号
	insert("deg", "throttled", true, false)
	insert("gone", "revoked", true, true) // 软删,不应计入

	// 他租户账号:不得进入本租户汇总。
	var op2 int64
	_ = pool.QueryRow(ctx, `INSERT INTO providers (tenant_id, code, display_name, upstream_protocol) VALUES ($1, $2, 'X', 'openai_chat') RETURNING id`, otherTenant, "x-"+suffix).Scan(&op2)
	var pg2, ch2 int64
	_ = pool.QueryRow(ctx, `INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`, otherTenant, "xp-"+suffix).Scan(&pg2)
	_ = pool.QueryRow(ctx, `INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`, otherTenant, pg2, "xc-"+suffix).Scan(&ch2)
	_, _ = pool.Exec(ctx, `INSERT INTO provider_accounts (tenant_id, provider_id, channel_id, name, account_type, enabled, health_state) VALUES ($1,$2,$3,$4,'api_key',true,'healthy')`, otherTenant, op2, ch2, "intruder-"+suffix)

	rows, err := q.SummarizeProviderAccountHealth(ctx, tenantID)
	if err != nil {
		t.Fatalf("SummarizeProviderAccountHealth: %v", err)
	}
	got := map[string]int64{}
	var total int64
	for _, r := range rows {
		got[r.HealthState+"/"+strconv.FormatBool(r.Enabled)] = r.N
		total += r.N
	}
	// 期望:healthy/true=2, healthy/false=1, throttled/true=1;软删 failed 与他租户 intruder 不计入 → total=4
	if total != 4 {
		t.Fatalf("total=%d want 4(软删与跨租户不得计入),rows=%+v", total, rows)
	}
	if got["healthy/true"] != 2 || got["healthy/false"] != 1 || got["throttled/true"] != 1 {
		t.Fatalf("聚合错误:%+v", got)
	}
}
