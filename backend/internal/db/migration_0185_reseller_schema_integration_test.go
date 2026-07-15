//go:build integration_pg

package db

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// TestMigration0185ResellerDormantSchemaRoundtrip 在隔离 schema 中验证 0185 的
// 休眠结构、旧写法兼容性、数据库约束和开发/测试回滚顺序。测试只执行迁移 SQL，
// 不接入任何运行时读取或写入口。
func TestMigration0185ResellerDormantSchemaRoundtrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, dsn(t))
	if err != nil {
		t.Fatalf("连接 PostgreSQL: %v", err)
	}
	defer conn.Close(context.Background())

	schema := fmt.Sprintf("migration_0185_%d", time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatalf("创建隔离 schema: %v", err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), "ROLLBACK")
		_, _ = conn.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE")
	}()
	if _, err := conn.Exec(ctx, "SET search_path TO "+quotedSchema+", public"); err != nil {
		t.Fatalf("设置 search_path: %v", err)
	}

	createMigration0185Fixture(t, ctx, conn)

	var existingTenantID int64
	if err := conn.QueryRow(ctx,
		"INSERT INTO tenants (name) VALUES ('pre-0185-tenant') RETURNING id",
	).Scan(&existingTenantID); err != nil {
		t.Fatalf("插入迁移前既有租户: %v", err)
	}
	if _, err := conn.Exec(ctx,
		"INSERT INTO admin_audit_events (action, target_type) VALUES ('cleanup_runtime_logs', 'runtime_logs')",
	); err != nil {
		t.Fatalf("插入迁移前审计动作: %v", err)
	}

	up := readMigration0185(t, "0185_reseller_phase1_tenant_hierarchy.up.sql")
	down := readMigration0185(t, "0185_reseller_phase1_tenant_hierarchy.down.sql")
	if _, err := conn.Exec(ctx, up); err != nil {
		t.Fatalf("首次执行 0185 up: %v", err)
	}

	// 破坏点→删掉任一默认值或错误回填既有行，本断言会因六项平台默认不完整而转红。
	t.Run("迁移前既有行获得平台默认值", func(t *testing.T) {
		var parentIsNull, multiplierIsNull bool
		var sharedPool, platformDomain, platformAnnouncement, masked bool
		err := conn.QueryRow(ctx, `
SELECT parent_tenant_id IS NULL,
       wholesale_multiplier IS NULL,
       upstream_mode = 'shared_pool',
       domain_mode = 'platform_domain',
       announcement_mode = 'platform',
       transparency_mode = 'masked'
FROM tenants
WHERE id = $1`, existingTenantID).Scan(
			&parentIsNull,
			&multiplierIsNull,
			&sharedPool,
			&platformDomain,
			&platformAnnouncement,
			&masked,
		)
		if err != nil {
			t.Fatalf("读取既有租户迁移结果: %v", err)
		}
		if !parentIsNull || !multiplierIsNull || !sharedPool || !platformDomain || !platformAnnouncement || !masked {
			t.Fatalf(
				"既有租户默认值错误: parent_null=%v multiplier_null=%v shared=%v domain=%v announcement=%v masked=%v",
				parentIsNull,
				multiplierIsNull,
				sharedPool,
				platformDomain,
				platformAnnouncement,
				masked,
			)
		}
	})

	// 破坏点→把新增列改成无默认的 NOT NULL，seed 使用的 name-only INSERT 会直接失败并转红。
	t.Run("旧name列清单仍可插入", func(t *testing.T) {
		var id int64
		if err := conn.QueryRow(ctx,
			"INSERT INTO tenants (name) VALUES ('legacy-name-only') RETURNING id",
		).Scan(&id); err != nil {
			t.Fatalf("旧 INSERT INTO tenants(name) 失败: %v", err)
		}
		if id <= 0 {
			t.Fatalf("旧 name-only INSERT 返回非法 id=%d", id)
		}
	})

	// 破坏点→新增列若强制由旧调用方提供，显式列出全部旧列的 INSERT 会失败并转红。
	t.Run("旧全列清单仍可插入", func(t *testing.T) {
		_, err := conn.Exec(ctx, `
INSERT INTO tenants (id, name, status, created_at, updated_at, deleted_at)
VALUES (9100185, 'legacy-all-columns', 'active', now(), now(), NULL)`)
		if err != nil {
			t.Fatalf("旧全列 INSERT 失败: %v", err)
		}
	})

	// 破坏点→删除租户形态 CHECK 后，根租户携带批发倍率会成功，本断言转红。
	t.Run("拒绝根租户携带批发倍率", func(t *testing.T) {
		assertMigration0185ConstraintViolation(
			t,
			ctx,
			conn,
			`INSERT INTO tenants (name, wholesale_multiplier) VALUES ('root-with-multiplier', 1.25000000)`,
			"23514",
			"tenants_reseller_shape_check",
		)
	})

	parentOne := insertMigration0185Root(t, ctx, conn, "parent-one")
	parentTwo := insertMigration0185Root(t, ctx, conn, "parent-two")

	// 破坏点→删除租户形态 CHECK 后，子租户缺少批发倍率会成功，本断言转红。
	t.Run("拒绝子租户缺少批发倍率", func(t *testing.T) {
		assertMigration0185ConstraintViolation(
			t,
			ctx,
			conn,
			`INSERT INTO tenants (name, parent_tenant_id) VALUES ('child-without-multiplier', $1)`,
			"23514",
			"tenants_reseller_shape_check",
			parentOne,
		)
	})

	// 破坏点→删掉倍率大于零条件后，零倍率子租户会成功，本断言转红。
	t.Run("拒绝子租户非正批发倍率", func(t *testing.T) {
		assertMigration0185ConstraintViolation(
			t,
			ctx,
			conn,
			`INSERT INTO tenants (name, parent_tenant_id, wholesale_multiplier)
VALUES ('child-zero-multiplier', $1, 0)`,
			"23514",
			"tenants_reseller_shape_check",
			parentOne,
		)
	})

	modeCases := []struct {
		name       string
		query      string
		constraint string
	}{
		{
			name:       "upstream_mode",
			query:      "INSERT INTO tenants (name, upstream_mode) VALUES ('bad-upstream-mode', 'invalid')",
			constraint: "tenants_upstream_mode_check",
		},
		{
			name:       "domain_mode",
			query:      "INSERT INTO tenants (name, domain_mode) VALUES ('bad-domain-mode', 'invalid')",
			constraint: "tenants_domain_mode_check",
		},
		{
			name:       "announcement_mode",
			query:      "INSERT INTO tenants (name, announcement_mode) VALUES ('bad-announcement-mode', 'invalid')",
			constraint: "tenants_announcement_mode_check",
		},
		{
			name:       "transparency_mode",
			query:      "INSERT INTO tenants (name, transparency_mode) VALUES ('bad-transparency-mode', 'invalid')",
			constraint: "tenants_transparency_mode_check",
		},
	}
	for _, tc := range modeCases {
		t.Run("拒绝非法_"+tc.name, func(t *testing.T) {
			// 破坏点→删除当前模式列的 CHECK 后，非法枚举会写入成功，本断言转红。
			assertMigration0185ConstraintViolation(t, ctx, conn, tc.query, "23514", tc.constraint)
		})
	}

	// 破坏点→删除自父 CHECK 后，自引用行会通过自引用 FK，本断言转红。
	t.Run("拒绝自父租户", func(t *testing.T) {
		assertMigration0185ConstraintViolation(
			t,
			ctx,
			conn,
			`INSERT INTO tenants (id, name, parent_tenant_id, wholesale_multiplier)
VALUES (9200185, 'self-parent', 9200185, 1)`,
			"23514",
			"tenants_parent_not_self_check",
		)
	})

	// 破坏点→删除 parent_tenant_id 外键后，不存在的父租户会被接受，本断言转红。
	t.Run("拒绝缺失父租户", func(t *testing.T) {
		assertMigration0185ConstraintViolation(
			t,
			ctx,
			conn,
			`INSERT INTO tenants (name, parent_tenant_id, wholesale_multiplier)
VALUES ('missing-parent', 9300185, 1)`,
			"23503",
			"tenants_parent_tenant_id_fkey",
		)
	})

	childForReparent := insertMigration0185Child(t, ctx, conn, "child-for-reparent", parentOne)
	// 破坏点→删除不可变触发器或把 IS DISTINCT FROM 改错后，reparent 会成功，本断言转红。
	t.Run("拒绝修改父租户", func(t *testing.T) {
		assertMigration0185ConstraintViolation(
			t,
			ctx,
			conn,
			"UPDATE tenants SET parent_tenant_id = $1 WHERE id = $2",
			"23514",
			"tenants_parent_tenant_id_immutable",
			parentTwo,
			childForReparent,
		)
	})

	// 破坏点→触发器若错误拒绝未变化赋值，这条合法 UPDATE 会失败并转红。
	t.Run("允许父租户保持不变", func(t *testing.T) {
		if _, err := conn.Exec(ctx,
			"UPDATE tenants SET parent_tenant_id = $1 WHERE id = $2",
			parentOne,
			childForReparent,
		); err != nil {
			t.Fatalf("相同 parent_tenant_id 的 UPDATE 应成功: %v", err)
		}
	})

	// 破坏点→漏建索引、交换键顺序或删掉 deleted_at 过滤条件，目录定义断言会转红。
	t.Run("父租户部分索引键序与谓词正确", func(t *testing.T) {
		var keysCorrect, predicateCorrect bool
		err := conn.QueryRow(ctx, `
SELECT pg_get_indexdef(i.indexrelid) LIKE '%(parent_tenant_id, id)%',
       pg_get_expr(i.indpred, i.indrelid) LIKE '%deleted_at IS NULL%'
FROM pg_index AS i
JOIN pg_class AS index_class ON index_class.oid = i.indexrelid
WHERE index_class.relname = 'idx_tenants_parent_active'
  AND index_class.relnamespace = current_schema()::regnamespace`).Scan(&keysCorrect, &predicateCorrect)
		if err != nil {
			t.Fatalf("读取 idx_tenants_parent_active 定义: %v", err)
		}
		if !keysCorrect || !predicateCorrect {
			t.Fatalf("父租户索引定义错误: keys_correct=%v predicate_correct=%v", keysCorrect, predicateCorrect)
		}
	})

	// 破坏点→把任一父级删除动作退化为默认 NO ACTION，confdeltype 目录断言会转红。
	t.Run("父租户与账号属主外键均为删除RESTRICT", func(t *testing.T) {
		var restrictCount int
		err := conn.QueryRow(ctx, `
SELECT count(*)
FROM pg_constraint
WHERE conname IN (
    'tenants_parent_tenant_id_fkey',
    'tenant_provider_account_allocations_owner_provider_account_fkey'
)
  AND contype = 'f'
  AND confdeltype = 'r'`).Scan(&restrictCount)
		if err != nil {
			t.Fatalf("读取外键删除动作: %v", err)
		}
		if restrictCount != 2 {
			t.Fatalf("ON DELETE RESTRICT 外键数=%d，期望 2", restrictCount)
		}
	})

	ownerTenant := insertMigration0185Root(t, ctx, conn, "allocation-owner")
	consumerOne := insertMigration0185Child(t, ctx, conn, "allocation-consumer-one", ownerTenant)
	consumerTwo := insertMigration0185Child(t, ctx, conn, "allocation-consumer-two", ownerTenant)

	// 破坏点→给 allocation 表计划外增加约束或漏掉四项中的任一项，约束集合断言会转红。
	t.Run("allocation仅包含锁定的四项约束", func(t *testing.T) {
		var exact bool
		// PG18 起 NOT NULL 以 contype='n' 计入 pg_constraint,断言业务约束数时须排除它。
		err := conn.QueryRow(ctx, `
SELECT count(*) FILTER (WHERE contype <> 'n') = 4
   AND count(*) FILTER (
           WHERE conname = 'tenant_provider_account_allocations_pkey' AND contype = 'p'
       ) = 1
   AND count(*) FILTER (
           WHERE conname = 'tenant_provider_account_allocations_provider_account_key' AND contype = 'u'
       ) = 1
   AND count(*) FILTER (
           WHERE conname = 'tenant_provider_account_allocations_owner_provider_account_fkey' AND contype = 'f'
       ) = 1
   AND count(*) FILTER (
           WHERE conname = 'tenant_provider_account_allocations_consumer_owner_check' AND contype = 'c'
       ) = 1
FROM pg_constraint
WHERE conrelid = 'tenant_provider_account_allocations'::regclass`).Scan(&exact)
		if err != nil {
			t.Fatalf("读取 allocation 约束集合: %v", err)
		}
		if !exact {
			t.Fatal("allocation 约束集合不是锁定的 PK/UNIQUE/复合 FK/CHECK 四项")
		}
	})

	duplicateAccount := insertMigration0185ProviderAccount(t, ctx, conn, ownerTenant)
	if _, err := conn.Exec(ctx, `
INSERT INTO tenant_provider_account_allocations (
    consumer_tenant_id, owner_tenant_id, provider_account_id, assigned_by_actor
) VALUES ($1, $2, $3, 'migration-test')`, consumerOne, ownerTenant, duplicateAccount); err != nil {
		t.Fatalf("插入首条专属账号分配: %v", err)
	}
	// 破坏点→删除 provider_account_id UNIQUE 后，同一账号可分给两个 consumer，本断言转红。
	t.Run("拒绝重复专属账号", func(t *testing.T) {
		assertMigration0185ConstraintViolation(
			t,
			ctx,
			conn,
			`INSERT INTO tenant_provider_account_allocations (
    consumer_tenant_id, owner_tenant_id, provider_account_id, assigned_by_actor
) VALUES ($1, $2, $3, 'migration-test')`,
			"23505",
			"tenant_provider_account_allocations_provider_account_key",
			consumerTwo,
			ownerTenant,
			duplicateAccount,
		)
	})

	sameTenantAccount := insertMigration0185ProviderAccount(t, ctx, conn, ownerTenant)
	// 破坏点→删除 consumer≠owner CHECK 后，账号可分配给其 owner 自身，本断言转红。
	t.Run("拒绝consumer等于owner", func(t *testing.T) {
		assertMigration0185ConstraintViolation(
			t,
			ctx,
			conn,
			`INSERT INTO tenant_provider_account_allocations (
    consumer_tenant_id, owner_tenant_id, provider_account_id, assigned_by_actor
) VALUES ($1, $1, $2, 'migration-test')`,
			"23514",
			"tenant_provider_account_allocations_consumer_owner_check",
			ownerTenant,
			sameTenantAccount,
		)
	})

	accountForWrongOwner := insertMigration0185ProviderAccount(t, ctx, conn, ownerTenant)
	wrongOwner := insertMigration0185Root(t, ctx, conn, "allocation-wrong-owner")
	// 破坏点→删除 owner/account 复合 FK 后，错误 owner 可冒充账号真属主，本断言转红。
	t.Run("拒绝owner非账号真属主", func(t *testing.T) {
		assertMigration0185ConstraintViolation(
			t,
			ctx,
			conn,
			`INSERT INTO tenant_provider_account_allocations (
    consumer_tenant_id, owner_tenant_id, provider_account_id, assigned_by_actor
) VALUES ($1, $2, $3, 'migration-test')`,
			"23503",
			"tenant_provider_account_allocations_owner_provider_account_fkey",
			consumerOne,
			wrongOwner,
			accountForWrongOwner,
		)
	})

	// 破坏点→0185 up 漏加任一新动作，合法审计 INSERT 会被旧 CHECK 拒绝并转红。
	t.Run("up开放五个分销商审计动作", func(t *testing.T) {
		assertMigration0185AuditActionsAllowed(t, ctx, conn)
	})

	// 破坏点→down 漏删表、触发器、索引、约束、函数或列，随后 re-up 会对象重名失败并转红。
	if _, err := conn.Exec(ctx, down); err != nil {
		t.Fatalf("执行 0185 down: %v", err)
	}
	assertMigration0185ObjectsAbsent(t, ctx, conn)

	// 破坏点→down 未恢复旧审计 CHECK 时，新动作仍可写入，本断言转红。
	assertMigration0185ConstraintViolation(
		t,
		ctx,
		conn,
		"INSERT INTO admin_audit_events (action, target_type) VALUES ('create_reseller_tenant', 'tenant')",
		"23514",
		"admin_audit_events_action_check",
	)

	// 破坏点→down 恢复了过早或缩水的白名单，0181 已有动作会被拒绝，本断言转红。
	if _, err := conn.Exec(ctx,
		"INSERT INTO admin_audit_events (action, target_type) VALUES ('cleanup_runtime_logs', 'runtime_logs')",
	); err != nil {
		t.Fatalf("down 后应保留既有 cleanup_runtime_logs 审计动作: %v", err)
	}

	// 破坏点→down 清理不完整或 up 非幂等重建时，第二次 up 会失败并转红。
	if _, err := conn.Exec(ctx, up); err != nil {
		t.Fatalf("down 后重新执行 0185 up: %v", err)
	}
	assertMigration0185ObjectsPresent(t, ctx, conn)
	assertMigration0185AuditActionsAllowed(t, ctx, conn)
}

func createMigration0185Fixture(t *testing.T, ctx context.Context, conn *pgx.Conn) {
	t.Helper()
	_, err := conn.Exec(ctx, `
CREATE TABLE tenants (
    id bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    name text NOT NULL,
    status text NOT NULL DEFAULT 'active',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);

CREATE TABLE provider_accounts (
    id bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    deleted_at timestamptz,
    CONSTRAINT provider_accounts_tenant_id_id_key UNIQUE (tenant_id, id)
);

CREATE TABLE admin_audit_events (
    id bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    action text NOT NULL,
    target_type text NOT NULL,
    CONSTRAINT admin_audit_events_action_check
        CHECK (action IN ('issue_api_key', 'cleanup_runtime_logs')),
    CONSTRAINT admin_audit_events_target_type_check
        CHECK (target_type IN ('tenant', 'provider_account', 'runtime_logs'))
);`)
	if err != nil {
		t.Fatalf("创建 0185 前置 fixture: %v", err)
	}
}

func insertMigration0185Root(
	t *testing.T,
	ctx context.Context,
	conn *pgx.Conn,
	name string,
) int64 {
	t.Helper()
	var id int64
	if err := conn.QueryRow(ctx,
		"INSERT INTO tenants (name) VALUES ($1) RETURNING id",
		name,
	).Scan(&id); err != nil {
		t.Fatalf("插入根租户 %q: %v", name, err)
	}
	return id
}

func insertMigration0185Child(
	t *testing.T,
	ctx context.Context,
	conn *pgx.Conn,
	name string,
	parentID int64,
) int64 {
	t.Helper()
	var id int64
	if err := conn.QueryRow(ctx, `
INSERT INTO tenants (name, parent_tenant_id, wholesale_multiplier)
VALUES ($1, $2, 1.00000000)
RETURNING id`, name, parentID).Scan(&id); err != nil {
		t.Fatalf("插入子租户 %q: %v", name, err)
	}
	return id
}

func insertMigration0185ProviderAccount(
	t *testing.T,
	ctx context.Context,
	conn *pgx.Conn,
	tenantID int64,
) int64 {
	t.Helper()
	var id int64
	if err := conn.QueryRow(ctx,
		"INSERT INTO provider_accounts (tenant_id) VALUES ($1) RETURNING id",
		tenantID,
	).Scan(&id); err != nil {
		t.Fatalf("插入 provider account: %v", err)
	}
	return id
}

func assertMigration0185ConstraintViolation(
	t *testing.T,
	ctx context.Context,
	conn *pgx.Conn,
	query string,
	wantCode string,
	wantConstraint string,
	args ...any,
) {
	t.Helper()
	_, err := conn.Exec(ctx, query, args...)
	if err == nil {
		t.Fatalf("SQL 应被约束 %s 拒绝，但执行成功", wantConstraint)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("SQL 应返回 PostgreSQL 约束错误，实际为 %T: %v", err, err)
	}
	if pgErr.Code != wantCode {
		t.Fatalf("约束 %s SQLSTATE=%s，期望 %s；错误=%v", wantConstraint, pgErr.Code, wantCode, err)
	}
	if pgErr.ConstraintName != wantConstraint {
		t.Fatalf("命中约束 %q，期望 %q；错误=%v", pgErr.ConstraintName, wantConstraint, err)
	}
}

func assertMigration0185AuditActionsAllowed(t *testing.T, ctx context.Context, conn *pgx.Conn) {
	t.Helper()
	actions := []struct {
		action     string
		targetType string
	}{
		{action: "create_reseller_tenant", targetType: "tenant"},
		{action: "set_reseller_status", targetType: "tenant"},
		{action: "set_reseller_wholesale_multiplier", targetType: "tenant"},
		{action: "set_reseller_modes", targetType: "tenant"},
		{action: "set_reseller_upstream_allocation", targetType: "provider_account"},
	}
	for _, item := range actions {
		if _, err := conn.Exec(ctx,
			"INSERT INTO admin_audit_events (action, target_type) VALUES ($1, $2)",
			item.action,
			item.targetType,
		); err != nil {
			t.Fatalf("审计动作 %q 应在 0185 白名单中: %v", item.action, err)
		}
	}
	for _, item := range actions {
		if _, err := conn.Exec(ctx,
			"DELETE FROM admin_audit_events WHERE action = $1",
			item.action,
		); err != nil {
			t.Fatalf("清理审计动作 %q: %v", item.action, err)
		}
	}
}

func assertMigration0185ObjectsAbsent(t *testing.T, ctx context.Context, conn *pgx.Conn) {
	t.Helper()
	var allocationExists bool
	if err := conn.QueryRow(ctx,
		"SELECT to_regclass('tenant_provider_account_allocations') IS NOT NULL",
	).Scan(&allocationExists); err != nil {
		t.Fatalf("检查 allocation 表是否移除: %v", err)
	}
	if allocationExists {
		t.Fatal("0185 down 后 allocation 表仍存在")
	}

	var newColumnCount int
	if err := conn.QueryRow(ctx, `
SELECT count(*)
FROM information_schema.columns
WHERE table_schema = current_schema()
  AND table_name = 'tenants'
  AND column_name IN (
      'parent_tenant_id', 'upstream_mode', 'domain_mode',
      'announcement_mode', 'transparency_mode', 'wholesale_multiplier'
  )`).Scan(&newColumnCount); err != nil {
		t.Fatalf("检查 tenants 新列是否移除: %v", err)
	}
	if newColumnCount != 0 {
		t.Fatalf("0185 down 后仍有 %d 个新增 tenants 列", newColumnCount)
	}
}

func assertMigration0185ObjectsPresent(t *testing.T, ctx context.Context, conn *pgx.Conn) {
	t.Helper()
	var allocationExists bool
	if err := conn.QueryRow(ctx,
		"SELECT to_regclass('tenant_provider_account_allocations') IS NOT NULL",
	).Scan(&allocationExists); err != nil {
		t.Fatalf("检查 allocation 表是否重建: %v", err)
	}
	if !allocationExists {
		t.Fatal("0185 re-up 后 allocation 表不存在")
	}

	var newColumnCount int
	if err := conn.QueryRow(ctx, `
SELECT count(*)
FROM information_schema.columns
WHERE table_schema = current_schema()
  AND table_name = 'tenants'
  AND column_name IN (
      'parent_tenant_id', 'upstream_mode', 'domain_mode',
      'announcement_mode', 'transparency_mode', 'wholesale_multiplier'
  )`).Scan(&newColumnCount); err != nil {
		t.Fatalf("检查 tenants 新列是否重建: %v", err)
	}
	if newColumnCount != 6 {
		t.Fatalf("0185 re-up 后 tenants 新列数=%d，期望 6", newColumnCount)
	}
}

func readMigration0185(t *testing.T, name string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller 失败")
	}
	path := filepath.Join(filepath.Dir(file), "..", "..", "sql", "migrations", name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 migration %s: %v", name, err)
	}
	return string(raw)
}
