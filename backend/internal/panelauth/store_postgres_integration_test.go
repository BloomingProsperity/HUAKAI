// HUAKAI · iKun
//go:build integration_pg

package panelauth

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

func openPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, name).Scan(&id); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	return id
}

// seedUser 插入一个用户; role=="" 时不显式给 role(验证迁移列默认 'user')。
func seedUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64, role string) int64 {
	t.Helper()
	var id int64
	var err error
	if role == "" {
		err = pool.QueryRow(ctx, `INSERT INTO users (tenant_id) VALUES ($1) RETURNING id`, tenantID).Scan(&id)
	} else {
		err = pool.QueryRow(ctx, `INSERT INTO users (tenant_id, role) VALUES ($1,$2) RETURNING id`, tenantID, role).Scan(&id)
	}
	if err != nil {
		t.Fatalf("seed user (role=%q): %v", role, err)
	}
	return id
}

// TestPG_UserRoleResolution 守真 PG 下 role → 面板归属 + 列默认 + 租户隔离。
// 判别:
//   - 漏 tenant 谓词 → 跨租户读到他租户用户角色 → tenant 隔离断言红(串租户=越权)。
//   - 迁移漏 DEFAULT 'user' → 不带 role 插入 NOT NULL 违反 → seed 失败红。
//   - role→panel 映射错 → admin/user 面板断言红。
func TestPG_UserRoleResolution(t *testing.T) {
	ctx := context.Background()
	pool := openPool(t, ctx)

	tenantA := seedTenant(t, ctx, pool, "panelauth-a-"+t.Name())
	tenantB := seedTenant(t, ctx, pool, "panelauth-b-"+t.Name())
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM users WHERE tenant_id IN ($1,$2)`, tenantA, tenantB)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id IN ($1,$2)`, tenantA, tenantB)
	})

	adminUser := seedUser(t, ctx, pool, tenantA, RoleAdmin)
	normalUser := seedUser(t, ctx, pool, tenantA, RoleUser)
	defaultUser := seedUser(t, ctx, pool, tenantA, "") // 不给 role, 验证列默认 'user'
	bUser := seedUser(t, ctx, pool, tenantB, RoleAdmin)

	r := NewResolver(NewPostgresRoleStore(pool))

	if p, err := r.PanelForUser(ctx, tenantA, adminUser); err != nil || p != PanelAdmin {
		t.Fatalf("admin user: panel=%q err=%v, want PanelAdmin", p, err)
	}
	if p, err := r.PanelForUser(ctx, tenantA, normalUser); err != nil || p != PanelUser {
		t.Fatalf("normal user: panel=%q err=%v, want PanelUser", p, err)
	}
	// 列默认: 不带 role 插入的用户应为 'user' → 用户面板(迁移 DEFAULT 'user' 生效)。
	if p, err := r.PanelForUser(ctx, tenantA, defaultUser); err != nil || p != PanelUser {
		t.Fatalf("default-role user: panel=%q err=%v, want PanelUser (migration DEFAULT 'user')", p, err)
	}
	// 租户隔离: tenantB 的 admin 用户绝不能用 tenantA 查到(否则串租户越权)。
	if p, err := r.PanelForUser(ctx, tenantA, bUser); !errors.Is(err, ErrUserNotFound) || p != PanelNone {
		t.Fatalf("cross-tenant lookup (tenantA reading tenantB's user): panel=%q err=%v, want PanelNone/ErrUserNotFound", p, err)
	}
	// 反向也确认 tenantB 能查到自己的(证明上面的 not-found 是隔离而非 user 真不存在)。
	if p, err := r.PanelForUser(ctx, tenantB, bUser); err != nil || p != PanelAdmin {
		t.Fatalf("tenantB own admin user: panel=%q err=%v, want PanelAdmin", p, err)
	}

	// 软删用户: 一个 role=admin 用户被软删后绝不可解析出面板(账号已注销)。
	// mutation: store_postgres.UserRole 去掉 `AND deleted_at IS NULL` → 软删 admin 仍解析出 PanelAdmin → 红。
	softDeleted := seedUser(t, ctx, pool, tenantA, RoleAdmin)
	if _, err := pool.Exec(ctx, `UPDATE users SET deleted_at = now() WHERE id = $1`, softDeleted); err != nil {
		t.Fatalf("soft-delete user: %v", err)
	}
	if p, err := r.PanelForUser(ctx, tenantA, softDeleted); !errors.Is(err, ErrUserNotFound) || p != PanelNone {
		t.Fatalf("soft-deleted admin user: panel=%q err=%v, want PanelNone/ErrUserNotFound (deleted_at IS NULL must exclude it)", p, err)
	}
}
