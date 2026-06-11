//go:build integration_pg

package tenancy

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

func openTestPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration test")
	}
	p, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

// hideWorkingTenants 暂时软删既有工作租户(id>0)以模拟「从零部署」空库,
// cleanup 时精确恢复——只动 deleted_at,不删任何行(-p 1 顺序执行下安全)。
func hideWorkingTenants(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT id FROM tenants WHERE id > 0 AND deleted_at IS NULL`)
	if err != nil {
		t.Fatalf("snapshot working tenants: %v", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) > 0 {
		if _, err := pool.Exec(ctx, `UPDATE tenants SET deleted_at = now() WHERE id = ANY($1)`, ids); err != nil {
			t.Fatalf("hide working tenants: %v", err)
		}
	}
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if len(ids) > 0 {
			if _, err := pool.Exec(cctx, `UPDATE tenants SET deleted_at = NULL WHERE id = ANY($1)`, ids); err != nil {
				t.Errorf("restore hidden tenants: %v", err)
			}
		}
	})
}

// deleteTenant 物理删测试自己种出来的租户行(确认无 FK 依赖才可用)。
func deleteTenant(t *testing.T, pool *pgxpool.Pool, id int64) {
	t.Helper()
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := pool.Exec(cctx, `DELETE FROM tenants WHERE id = $1`, id); err != nil {
			t.Errorf("delete seeded tenant %d: %v", id, err)
		}
	})
}

// TestEnsureDefaultTenant_SeedsOnEmptyLibrary 从零部署主路径:空库(只有
// id=0 哨兵)启动后必须出现 id=1 active 工作租户,且哨兵不被动。
// MUTATION: 删掉 INSERT 分支(只留 count 短路)→ 本测试红。
func TestEnsureDefaultTenant_SeedsOnEmptyLibrary(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openTestPool(t, ctx)
	hideWorkingTenants(t, ctx, pool)
	deleteTenant(t, pool, DefaultWorkingTenantID)

	if err := EnsureDefaultTenant(ctx, pool, nil); err != nil {
		t.Fatalf("EnsureDefaultTenant: %v", err)
	}

	var status string
	var deletedAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT status, deleted_at FROM tenants WHERE id = $1`, DefaultWorkingTenantID).Scan(&status, &deletedAt); err != nil {
		t.Fatalf("query seeded tenant: %v", err)
	}
	if status != "active" || deletedAt != nil {
		t.Fatalf("seeded tenant status=%q deleted_at=%v want active/nil(auth 联查门只认 active 未软删)", status, deletedAt)
	}
	var sentinel string
	if err := pool.QueryRow(ctx, `SELECT name FROM tenants WHERE id = 0`).Scan(&sentinel); err != nil {
		t.Fatalf("id=0 哨兵被动了: %v", err)
	}
}

// TestEnsureDefaultTenant_AdvancesSequence 承重地雷守卫:显式插 id 不推进
// bigserial 序列,不补 setval 的话下一个自动取号 INSERT 撞唯一冲突。
// 判别性关键:共享 gate 库的序列早被其它测试推过 1,必须先把序列回拨到
// 全新库状态(setval 1,false:下一个 nextval 返回 1),否则 setval 变异
// 删掉了测试照样绿(非判别 fixture,实跑变异时抓到的教训)。
// MUTATION: 删 bootstrap.go 的 setval 语句 → 本测试的自动取号 INSERT 报
// duplicate key → 红。
func TestEnsureDefaultTenant_AdvancesSequence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openTestPool(t, ctx)
	hideWorkingTenants(t, ctx, pool)
	deleteTenant(t, pool, DefaultWorkingTenantID)

	if _, err := pool.Exec(ctx, `SELECT setval(pg_get_serial_sequence('tenants','id'), 1, false)`); err != nil {
		t.Fatalf("rewind sequence to fresh-db state: %v", err)
	}
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		// 推回安全位:变异红跑(无 setval)会把序列留在撞既有行的位置,
		// 不修后续套件全炸。
		if _, err := pool.Exec(cctx, `SELECT setval(pg_get_serial_sequence('tenants','id'), GREATEST((SELECT COALESCE(MAX(id),1) FROM tenants), 1))`); err != nil {
			t.Errorf("re-advance sequence: %v", err)
		}
	})

	if err := EnsureDefaultTenant(ctx, pool, nil); err != nil {
		t.Fatalf("EnsureDefaultTenant: %v", err)
	}

	var probeID int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ('seq-probe') RETURNING id`).Scan(&probeID); err != nil {
		t.Fatalf("种子后自动取号 INSERT 失败(序列没推进?): %v", err)
	}
	deleteTenant(t, pool, probeID)
	if probeID <= DefaultWorkingTenantID {
		t.Fatalf("probe id=%d want > %d(序列必须越过种子 id)", probeID, DefaultWorkingTenantID)
	}
}

// TestEnsureDefaultTenant_IdempotentAndSkipsWhenWorkingTenantExists 幂等 +
// 越权守卫:二次调用零写入;库里已有任意工作租户(哪怕 id≠默认值)绝不再种。
// MUTATION: 删 count 短路 → 已有 id=777 工作租户时仍种 id=1 → 红。
func TestEnsureDefaultTenant_IdempotentAndSkipsWhenWorkingTenantExists(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openTestPool(t, ctx)
	hideWorkingTenants(t, ctx, pool)
	deleteTenant(t, pool, DefaultWorkingTenantID)

	if err := EnsureDefaultTenant(ctx, pool, nil); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := EnsureDefaultTenant(ctx, pool, nil); err != nil {
		t.Fatalf("second call: %v", err)
	}
	var n int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tenants WHERE id = $1`, DefaultWorkingTenantID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("seed rows=%d want 1(幂等)", n)
	}

	// 已有工作租户(非默认 id)时绝不种新的。
	if _, err := pool.Exec(ctx, `INSERT INTO tenants (id, name, status) VALUES (777, 'preexisting', 'active') ON CONFLICT (id) DO UPDATE SET deleted_at = NULL, status = 'active'`); err != nil {
		t.Fatalf("seed preexisting: %v", err)
	}
	deleteTenant(t, pool, 777)
	if _, err := pool.Exec(ctx, `DELETE FROM tenants WHERE id = $1`, DefaultWorkingTenantID); err != nil {
		t.Fatalf("clear default: %v", err)
	}
	if err := EnsureDefaultTenant(ctx, pool, nil); err != nil {
		t.Fatalf("call with preexisting tenant: %v", err)
	}
	var defaultExists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tenants WHERE id = $1)`, DefaultWorkingTenantID).Scan(&defaultExists); err != nil {
		t.Fatalf("exists: %v", err)
	}
	if defaultExists {
		t.Fatal("已有工作租户(id=777)时不得再种默认租户(越权写入守卫)")
	}
}

// TestEnsureDefaultTenant_RestoresSoftDeletedSeed 整库唯一工作租户被软删 =
// 站点变砖,钩子应复活它(Warn 级)而非撞主键。
// MUTATION: 删 restore UPDATE 分支(直接 INSERT)→ duplicate key → 红。
func TestEnsureDefaultTenant_RestoresSoftDeletedSeed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openTestPool(t, ctx)
	hideWorkingTenants(t, ctx, pool)
	deleteTenant(t, pool, DefaultWorkingTenantID)

	if _, err := pool.Exec(ctx, `INSERT INTO tenants (id, name, status, deleted_at) VALUES ($1, 'bricked', 'active', now()) ON CONFLICT (id) DO UPDATE SET deleted_at = now()`, DefaultWorkingTenantID); err != nil {
		t.Fatalf("seed soft-deleted: %v", err)
	}

	if err := EnsureDefaultTenant(ctx, pool, nil); err != nil {
		t.Fatalf("EnsureDefaultTenant: %v", err)
	}
	var deletedAt *time.Time
	var status string
	if err := pool.QueryRow(ctx, `SELECT status, deleted_at FROM tenants WHERE id = $1`, DefaultWorkingTenantID).Scan(&status, &deletedAt); err != nil {
		t.Fatalf("query: %v", err)
	}
	if deletedAt != nil || status != "active" {
		t.Fatalf("status=%q deleted_at=%v want active/nil(软删种子应复活)", status, deletedAt)
	}
}

// TestEnsureDefaultTenant_RestoreNameCollisionDoesNotCrash 评审 S3-2 守卫:
// 软删种子行的旧 name 恰与某 active 行(如 id=0 'public-pricing')同名时,
// 复活的 UPDATE 必须归一 name 以避开 uq_tenants_name,不得撞唯一冲突致启动 fatal。
// MUTATION: 复活 UPDATE 不改 name(去掉 name=$2)→ duplicate key uq_tenants_name → 红。
func TestEnsureDefaultTenant_RestoreNameCollisionDoesNotCrash(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openTestPool(t, ctx)
	hideWorkingTenants(t, ctx, pool)
	deleteTenant(t, pool, DefaultWorkingTenantID)

	// 种子 id 以软删 + 与 id=0 哨兵同名('public-pricing')存在。
	if _, err := pool.Exec(ctx,
		`INSERT INTO tenants (id, name, status, deleted_at) VALUES ($1, 'public-pricing', 'active', now()) ON CONFLICT (id) DO UPDATE SET name = 'public-pricing', deleted_at = now()`,
		DefaultWorkingTenantID); err != nil {
		t.Fatalf("seed colliding soft-deleted: %v", err)
	}

	if err := EnsureDefaultTenant(ctx, pool, nil); err != nil {
		t.Fatalf("复活与活租户同名的软删种子应归一 name 而非 fatal: %v", err)
	}
	var status, name string
	var deletedAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT status, name, deleted_at FROM tenants WHERE id = $1`, DefaultWorkingTenantID).Scan(&status, &name, &deletedAt); err != nil {
		t.Fatalf("query: %v", err)
	}
	if status != "active" || deletedAt != nil || name != "default" {
		t.Fatalf("status=%q name=%q deleted_at=%v want active/default/nil(归一名复活)", status, name, deletedAt)
	}
}

// TestEnsureDefaultTenant_EnvOverrideAndInvalidEnv env 覆盖种子 id;非法值
// fail-loud(吞成默认值会让运维以为覆盖生效)。
// MUTATION: 非法 env 静默回默认 → err==nil → 红。
func TestEnsureDefaultTenant_EnvOverrideAndInvalidEnv(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openTestPool(t, ctx)
	hideWorkingTenants(t, ctx, pool)

	t.Setenv(DefaultWorkingTenantIDEnv, "4242")
	deleteTenant(t, pool, 4242)
	if err := EnsureDefaultTenant(ctx, pool, nil); err != nil {
		t.Fatalf("env override: %v", err)
	}
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tenants WHERE id = 4242 AND deleted_at IS NULL AND status = 'active')`).Scan(&exists); err != nil {
		t.Fatalf("exists: %v", err)
	}
	if !exists {
		t.Fatal("env 覆盖的种子 id=4242 未生效")
	}

	for _, bad := range []string{"abc", "0", "-3"} {
		t.Setenv(DefaultWorkingTenantIDEnv, bad)
		if err := EnsureDefaultTenant(ctx, pool, nil); err == nil {
			t.Fatalf("%s=%q 应 fail-loud", DefaultWorkingTenantIDEnv, bad)
		}
	}
}
