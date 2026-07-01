//go:build integration_pg

package panelauth

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// seedUserEmail 插入带 email + role 的用户,供 admin 用户 bootstrap 的按邮箱匹配测试。
func seedUserEmail(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64, email, role string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, email, role) VALUES ($1,$2,$3) RETURNING id`,
		tenantID, email, role).Scan(&id); err != nil {
		t.Fatalf("seed user email=%q role=%q: %v", email, role, err)
	}
	return id
}

func roleOf(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, userID int64) string {
	t.Helper()
	var role string
	if err := pool.QueryRow(ctx,
		`SELECT role FROM users WHERE tenant_id = $1 AND id = $2`, tenantID, userID).Scan(&role); err != nil {
		t.Fatalf("read role: %v", err)
	}
	return role
}

func updatedAtOf(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, userID int64) time.Time {
	t.Helper()
	var ts time.Time
	if err := pool.QueryRow(ctx,
		`SELECT updated_at FROM users WHERE tenant_id = $1 AND id = $2`, tenantID, userID).Scan(&ts); err != nil {
		t.Fatalf("read updated_at: %v", err)
	}
	return ts
}

// TestPG_BootstrapAdminUser 守 admin 用户 bootstrap 在真 PG 下的全部判别不变量。
// 每个断言都对应一个具体变异会让它红,防「SQL WHERE 写错却假绿」。
func TestPG_BootstrapAdminUser(t *testing.T) {
	ctx := context.Background()
	pool := openPool(t, ctx)

	tenantA := seedTenant(t, ctx, pool, "bootstrap-a-"+t.Name())
	tenantB := seedTenant(t, ctx, pool, "bootstrap-b-"+t.Name())
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM users WHERE tenant_id IN ($1,$2)`, tenantA, tenantB)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id IN ($1,$2)`, tenantA, tenantB)
	})

	// ① 提升:tenantA 下 role=user 的匹配账号 → admin。
	// 变异:删 UPDATE 分支 → 仍是 user → RED。
	promote := seedUserEmail(t, ctx, pool, tenantA, "ops@example.test", RoleUser)
	t.Setenv(AdminBootstrapEmailEnv, "ops@example.test")
	if err := MaybeBootstrapAdminUser(ctx, pool, tenantA, nil); err != nil {
		t.Fatalf("bootstrap 提升: %v", err)
	}
	if got := roleOf(t, ctx, pool, tenantA, promote); got != RoleAdmin {
		t.Fatalf("匹配账号应被提升为 admin,得 role=%q", got)
	}

	// ② 幂等:已是 admin,再跑一次 → 仍 admin、无错,且**不重写该行**(role<>'admin' 谓词让它
	// 走 RowsAffected=0 的 skip 分支,而非再刷一遍 updated_at)。
	// 变异:删 `role <> 'admin'` 谓词 → 第二次 UPDATE 会命中并把 updated_at 刷成 now() → 下面
	// updated_at 不变的断言 RED。
	beforeUpdatedAt := updatedAtOf(t, ctx, pool, tenantA, promote)
	if err := MaybeBootstrapAdminUser(ctx, pool, tenantA, nil); err != nil {
		t.Fatalf("bootstrap 幂等重跑: %v", err)
	}
	if got := roleOf(t, ctx, pool, tenantA, promote); got != RoleAdmin {
		t.Fatalf("幂等重跑后应仍是 admin,得 role=%q", got)
	}
	if after := updatedAtOf(t, ctx, pool, tenantA, promote); !after.Equal(beforeUpdatedAt) {
		t.Fatalf("已是 admin 的账号幂等重跑不应重写 updated_at,before=%v after=%v", beforeUpdatedAt, after)
	}

	// ③ 跨租户隔离:tenantB 有同邮箱账号,以 tenantA 跑绝不能提升它。
	// 变异:删 tenant_id 谓词 → tenantB 账号被误提升 → RED(串租户越权)。
	crossEmail := "cross@example.test"
	bUser := seedUserEmail(t, ctx, pool, tenantB, crossEmail, RoleUser)
	seedUserEmail(t, ctx, pool, tenantA, "someone-else@example.test", RoleUser) // tenantA 无此邮箱
	t.Setenv(AdminBootstrapEmailEnv, crossEmail)
	if err := MaybeBootstrapAdminUser(ctx, pool, tenantA, nil); err != nil {
		t.Fatalf("bootstrap 跨租户(tenantA 无此邮箱,应 no-op): %v", err)
	}
	if got := roleOf(t, ctx, pool, tenantB, bUser); got != RoleUser {
		t.Fatalf("跨租户:tenantB 同邮箱账号绝不应被 tenantA 的 bootstrap 提升,得 role=%q", got)
	}

	// ④ 软删账号不提升。变异:删 deleted_at IS NULL 谓词 → 软删账号被提升 → RED。
	delEmail := "deleted@example.test"
	delUser := seedUserEmail(t, ctx, pool, tenantA, delEmail, RoleUser)
	if _, err := pool.Exec(ctx, `UPDATE users SET deleted_at = now() WHERE id = $1`, delUser); err != nil {
		t.Fatalf("软删账号: %v", err)
	}
	t.Setenv(AdminBootstrapEmailEnv, delEmail)
	if err := MaybeBootstrapAdminUser(ctx, pool, tenantA, nil); err != nil {
		t.Fatalf("bootstrap 软删(应 no-op): %v", err)
	}
	if got := roleOf(t, ctx, pool, tenantA, delUser); got != RoleUser {
		t.Fatalf("软删账号绝不应被提升,得 role=%q", got)
	}

	// ⑤ 大小写不敏感:env 用混合大小写,账号用小写,仍匹配提升。
	// 变异:删 lower() → 大小写不符不匹配 → 仍 user → RED。
	ciUser := seedUserEmail(t, ctx, pool, tenantA, "mixedcase@example.test", RoleUser)
	t.Setenv(AdminBootstrapEmailEnv, "MixedCase@Example.TEST")
	if err := MaybeBootstrapAdminUser(ctx, pool, tenantA, nil); err != nil {
		t.Fatalf("bootstrap 大小写: %v", err)
	}
	if got := roleOf(t, ctx, pool, tenantA, ciUser); got != RoleAdmin {
		t.Fatalf("大小写不敏感匹配应提升,得 role=%q", got)
	}

	// ⑥ 陈旧 env:无匹配账号 → 不报错、不 crash(启动韧性)。
	t.Setenv(AdminBootstrapEmailEnv, "nobody-registered@example.test")
	if err := MaybeBootstrapAdminUser(ctx, pool, tenantA, nil); err != nil {
		t.Fatalf("陈旧 env(无此账号)应 no-op 不崩,得 %v", err)
	}
}
