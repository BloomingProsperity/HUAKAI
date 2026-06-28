//go:build integration_pg

package moderation

import (
	"context"
	"testing"

	dbmoderation "github.com/BloomingProsperity/HUAKAI/internal/db/moderation"
)

// 本文件真 PG 验证 Hermes mutating 工具 moderation_keyword_enable/disable 所依赖的两个手写查询
// GetModerationKeyword / SetModerationKeywordEnabled 的关键不变量:tx 内 toggle、跨租户 0 行、
// 租户隔离读、deleted_at IS NULL 不命中已删。复用 moderation_admin_integration_test.go 的
// openModerationIntegrationPool + seedModerationAPIKey(后者建租户并登记 cleanup)。

func TestSetModerationKeywordEnabled_TxTogglesAndTenantScoped(t *testing.T) {
	// 不变量:SetModerationKeywordEnabled 在事务内翻转单条关键词的 enabled,且按 tenant+id 命中;
	// 跨租户(同 id、错租户)命中 0 行,绝不误改别租户的关键词。
	// 变异检验:把 SQL 的 WHERE tenant_id=$1 去掉(只按 id 改)→ 跨租户那次会改到 1 行,
	//   下面"跨租户 0 行 + 邻租户 enabled 不变"的断言翻红。
	ctx := context.Background()
	pool := openModerationIntegrationPool(t, ctx)

	// 租户 A:用 seedModerationAPIKey 建租户(它登记了 tenant/api_key/log 等 cleanup);
	// 我们额外在该租户插一条关键词。租户 B 同理。
	a := seedModerationAPIKey(t, ctx, pool, "kw-toggle-a", "active")
	b := seedModerationAPIKey(t, ctx, pool, "kw-toggle-b", "active")
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM moderation_keywords WHERE tenant_id IN ($1,$2)`, a.tenantID, b.tenantID)
	})

	var kwID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO moderation_keywords (tenant_id, keyword, reason_code, enabled)
		 VALUES ($1, $2, $3, $4) RETURNING id`,
		a.tenantID, "block-this", "abuse_terms", true,
	).Scan(&kwID); err != nil {
		t.Fatalf("seed keyword A: %v", err)
	}

	q := dbmoderation.New(pool)

	// (1) 同租户 disable:应改 1 行,且读回 enabled=false。
	rows, err := q.SetModerationKeywordEnabled(ctx, dbmoderation.SetModerationKeywordEnabledParams{TenantID: a.tenantID, ID: kwID, Enabled: false})
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	if rows != 1 {
		t.Fatalf("disable rows=%d want 1", rows)
	}
	got, err := q.GetModerationKeyword(ctx, dbmoderation.GetModerationKeywordParams{TenantID: a.tenantID, ID: kwID})
	if err != nil {
		t.Fatalf("get after disable: %v", err)
	}
	if got.Enabled {
		t.Fatalf("after disable enabled=true want false")
	}

	// (2) 跨租户:租户 B 试图 enable 租户 A 的 kwID → 命中 0 行,且 A 的关键词仍是 disabled。
	rowsX, err := q.SetModerationKeywordEnabled(ctx, dbmoderation.SetModerationKeywordEnabledParams{TenantID: b.tenantID, ID: kwID, Enabled: true})
	if err != nil {
		t.Fatalf("cross-tenant set: %v", err)
	}
	if rowsX != 0 {
		t.Fatalf("cross-tenant rows=%d want 0 (租户 scope 必须绑死,不得误改别租户关键词)", rowsX)
	}
	gotA, err := q.GetModerationKeyword(ctx, dbmoderation.GetModerationKeywordParams{TenantID: a.tenantID, ID: kwID})
	if err != nil {
		t.Fatalf("get A after cross-tenant: %v", err)
	}
	if gotA.Enabled {
		t.Fatalf("cross-tenant set leaked into tenant A: enabled=true want false")
	}

	// (3) 同租户 enable 翻回:应改 1 行,读回 enabled=true。
	rowsE, err := q.SetModerationKeywordEnabled(ctx, dbmoderation.SetModerationKeywordEnabledParams{TenantID: a.tenantID, ID: kwID, Enabled: true})
	if err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	if rowsE != 1 {
		t.Fatalf("re-enable rows=%d want 1", rowsE)
	}
	reGot, err := q.GetModerationKeyword(ctx, dbmoderation.GetModerationKeywordParams{TenantID: a.tenantID, ID: kwID})
	if err != nil {
		t.Fatalf("get after re-enable: %v", err)
	}
	if !reGot.Enabled {
		t.Fatalf("after re-enable enabled=false want true")
	}
}

func TestGetModerationKeyword_TenantIsolationAndSoftDelete(t *testing.T) {
	// 不变量:GetModerationKeyword 按 tenant+id 隔离读,且 deleted_at IS NULL —— 跨租户读不到、
	// 已软删的关键词读不到。
	// 变异检验:
	//   - 去掉 SQL 的 AND deleted_at IS NULL → "软删后应读不到"的断言翻红(软删行被读出);
	//   - 去掉 WHERE tenant_id=$1 → "跨租户读应失败"的断言翻红。
	ctx := context.Background()
	pool := openModerationIntegrationPool(t, ctx)
	a := seedModerationAPIKey(t, ctx, pool, "kw-iso-a", "active")
	b := seedModerationAPIKey(t, ctx, pool, "kw-iso-b", "active")
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM moderation_keywords WHERE tenant_id IN ($1,$2)`, a.tenantID, b.tenantID)
	})

	var kwID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO moderation_keywords (tenant_id, keyword, reason_code, enabled)
		 VALUES ($1, $2, $3, $4) RETURNING id`,
		a.tenantID, "isolated-word", "keyword_match", true,
	).Scan(&kwID); err != nil {
		t.Fatalf("seed keyword: %v", err)
	}

	q := dbmoderation.New(pool)

	// 租户 A 能读到自己的关键词,字段正确。
	row, err := q.GetModerationKeyword(ctx, dbmoderation.GetModerationKeywordParams{TenantID: a.tenantID, ID: kwID})
	if err != nil {
		t.Fatalf("get own: %v", err)
	}
	if row.TenantID != a.tenantID || row.Keyword != "isolated-word" || row.ReasonCode != "keyword_match" || !row.Enabled {
		t.Fatalf("own keyword row mismatch: %+v", row)
	}

	// 跨租户:租户 B 用同 id 读 → 必须读不到(pgx.ErrNoRows)。
	if _, err := q.GetModerationKeyword(ctx, dbmoderation.GetModerationKeywordParams{TenantID: b.tenantID, ID: kwID}); err == nil {
		t.Fatalf("cross-tenant get returned a row; want no-rows error (租户隔离)")
	}

	// 软删后:同租户也应读不到(deleted_at IS NULL 过滤)。用既有的 SoftDeleteModerationKeyword。
	delRows, err := q.SoftDeleteModerationKeyword(ctx, dbmoderation.SoftDeleteModerationKeywordParams{TenantID: a.tenantID, ID: kwID})
	if err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	if delRows != 1 {
		t.Fatalf("soft delete rows=%d want 1", delRows)
	}
	if _, err := q.GetModerationKeyword(ctx, dbmoderation.GetModerationKeywordParams{TenantID: a.tenantID, ID: kwID}); err == nil {
		t.Fatalf("get after soft delete returned a row; want no-rows error (deleted_at IS NULL 过滤)")
	}
	// 已软删的关键词也不可被 SetModerationKeywordEnabled 翻动(0 行)。
	setRows, err := q.SetModerationKeywordEnabled(ctx, dbmoderation.SetModerationKeywordEnabledParams{TenantID: a.tenantID, ID: kwID, Enabled: true})
	if err != nil {
		t.Fatalf("set on soft-deleted: %v", err)
	}
	if setRows != 0 {
		t.Fatalf("set on soft-deleted rows=%d want 0 (已删关键词不可翻动)", setRows)
	}
}
