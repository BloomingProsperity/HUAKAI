//go:build integration_pg

// TouchProviderAccountProbe 真 PG 验证:点亮 last_probe_at 死列。
//
// 背景:provider_accounts.last_probe_at(迁移 0110)读取侧齐全(健康面板回显),
// 但此前全仓零写入,该列恒 NULL。本测试种一个 last_probe_at 为 NULL 的池账号,
// 调 TouchProviderAccountProbe 后断言该列变成预期时间戳。
//
// mutation 自检:
//   - 把 UPDATE 的 SET 列从 last_probe_at 改成别的列 → after.Valid 仍为 false → red。
//   - 把 WHERE 的 id / tenant_id 写错 → 0 行受影响 → after.Valid 仍为 false → red。
//   - 跨租户调用(错误 tenant_id)→ 不应写到该账号 → 第二段断言 red。

package admin

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestTouchProviderAccountProbeWritesLastProbeAt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openAdminAuditIntegrationPool(t, ctx)
	q := New(pool)

	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	tenantID, accountID := seedAdminProviderAccountHealthGraph(t, ctx, pool, suffix)
	t.Cleanup(func() {
		cleanupAdminProviderAccountHealthGraph(t, context.Background(), pool, tenantID)
	})

	// 前置:种出的账号 last_probe_at 必须是 NULL,否则测不出"从无到有"。
	var before pgtype.Timestamptz
	if err := pool.QueryRow(ctx,
		`SELECT last_probe_at FROM provider_accounts WHERE id = $1 AND tenant_id = $2`,
		accountID, tenantID,
	).Scan(&before); err != nil {
		t.Fatalf("读取初始 last_probe_at: %v", err)
	}
	if before.Valid {
		t.Fatalf("种出的账号 last_probe_at 应为 NULL, 实得 %v", before.Time)
	}

	probedAt := time.Date(2026, 6, 24, 9, 30, 0, 0, time.UTC)
	if err := q.TouchProviderAccountProbe(ctx, TouchProviderAccountProbeParams{
		ProbedAt: pgtype.Timestamptz{Time: probedAt, Valid: true},
		ID:       accountID,
		TenantID: tenantID,
	}); err != nil {
		t.Fatalf("TouchProviderAccountProbe: %v", err)
	}

	var after pgtype.Timestamptz
	if err := pool.QueryRow(ctx,
		`SELECT last_probe_at FROM provider_accounts WHERE id = $1 AND tenant_id = $2`,
		accountID, tenantID,
	).Scan(&after); err != nil {
		t.Fatalf("读取写后 last_probe_at: %v", err)
	}
	if !after.Valid {
		t.Fatalf("last_probe_at 写后仍为 NULL(UPDATE 未命中正确列/行)")
	}
	if !after.Time.Equal(probedAt) {
		t.Fatalf("last_probe_at=%v want %v", after.Time, probedAt)
	}

	// 跨租户隔离:用错误 tenant_id 调,不应改动该账号(WHERE tenant_id 守卫)。
	wrongTenant := tenantID + 1_000_000
	if err := q.TouchProviderAccountProbe(ctx, TouchProviderAccountProbeParams{
		ProbedAt: pgtype.Timestamptz{Time: probedAt.Add(time.Hour), Valid: true},
		ID:       accountID,
		TenantID: wrongTenant,
	}); err != nil {
		t.Fatalf("跨租户 TouchProviderAccountProbe 不应报错(0 行): %v", err)
	}
	var afterCross pgtype.Timestamptz
	if err := pool.QueryRow(ctx,
		`SELECT last_probe_at FROM provider_accounts WHERE id = $1 AND tenant_id = $2`,
		accountID, tenantID,
	).Scan(&afterCross); err != nil {
		t.Fatalf("读取跨租户后 last_probe_at: %v", err)
	}
	if !afterCross.Time.Equal(probedAt) {
		t.Fatalf("跨租户调用篡改了账号 last_probe_at: got %v want unchanged %v", afterCross.Time, probedAt)
	}
}
