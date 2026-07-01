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

// seedUserRawEmail 用给定字面量插入 email(含 NULL / 空串 / 带空白的值),供「按邮箱匹配」的
// 边界测试。email==nil 时插 SQL NULL(users.email 可空);否则原样插入(不 trim)。
func seedUserRawEmail(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64, email *string, role string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, email, role) VALUES ($1,$2,$3) RETURNING id`,
		tenantID, email, role).Scan(&id); err != nil {
		t.Fatalf("seed raw email=%v role=%q: %v", email, role, err)
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

	// ⑦ NULL-email 账号绝不被匹配:同租户存在一个 email 为 NULL 的账号(社交/L0 无邮箱注册,
	//    users.email 可空),bootstrap 一个恰好不存在的邮箱时,NULL-email 账号绝不能被误提升。
	//    这守 WHERE 谓词没被写成会命中 NULL / 全表的形态(lower(NULL)=lower('x') 为 NULL→不命中)。
	//    变异:把 UPDATE 的 email 谓词改成 `(lower(email)=lower($2) OR email IS NULL)` 之类放宽 →
	//    NULL-email 账号被提升 → RED。
	nullEmailUser := seedUserRawEmail(t, ctx, pool, tenantA, nil, RoleUser)
	t.Setenv(AdminBootstrapEmailEnv, "no-such-registered-email@example.test")
	if err := MaybeBootstrapAdminUser(ctx, pool, tenantA, nil); err != nil {
		t.Fatalf("bootstrap(无匹配、存在 NULL-email 账号)应 no-op: %v", err)
	}
	if got := roleOf(t, ctx, pool, tenantA, nullEmailUser); got != RoleUser {
		t.Fatalf("NULL-email 账号绝不应被 bootstrap 提升,得 role=%q", got)
	}

	// ⑧ 空串-email 账号不被非空 env 匹配:一个 email='' 的账号,bootstrap 一个非空邮箱时不应命中它。
	//    (email 非空唯一约束按 email IS NOT NULL 过滤,空串仍可入库;lower('')=lower('x') 恒 false。)
	//    变异:若匹配退化成「email 为空即命中」或去掉相等比较 → 空串账号被提升 → RED。
	emptyStr := ""
	emptyEmailUser := seedUserRawEmail(t, ctx, pool, tenantA, &emptyStr, RoleUser)
	t.Setenv(AdminBootstrapEmailEnv, "definitely-not-empty@example.test")
	if err := MaybeBootstrapAdminUser(ctx, pool, tenantA, nil); err != nil {
		t.Fatalf("bootstrap(非空 env、存在空串-email 账号)应 no-op: %v", err)
	}
	if got := roleOf(t, ctx, pool, tenantA, emptyEmailUser); got != RoleUser {
		t.Fatalf("空串-email 账号不应被非空 env 提升,得 role=%q", got)
	}

	// ⑨ DB email 列带前后空白不被 trim 后的 env 匹配:UPDATE 只对 env 值 TrimSpace,不 trim 数据库列。
	//    账号 email=' spaced@example.test '(前后空白),env=trim 后的 'spaced@example.test' → 不命中。
	//    这钉住「env 侧 trim、列侧不 trim」的真实语义:运维在 env 里粘贴带空白的邮箱能匹配到干净入库的
	//    账号(⑦ 的镜像由集成层 padded-env 覆盖),但脏数据(列里带空白)不会被干净 env 误命中。
	//    变异:若给 SQL 列也加 trim(如 `lower(trim(email))=lower($2)`)→ 带空白列会命中 → 提升 → RED。
	spacedEmail := " spaced@example.test "
	spacedEmailUser := seedUserRawEmail(t, ctx, pool, tenantA, &spacedEmail, RoleUser)
	t.Setenv(AdminBootstrapEmailEnv, "spaced@example.test")
	if err := MaybeBootstrapAdminUser(ctx, pool, tenantA, nil); err != nil {
		t.Fatalf("bootstrap(env 干净、列带空白)应 no-op 不命中: %v", err)
	}
	if got := roleOf(t, ctx, pool, tenantA, spacedEmailUser); got != RoleUser {
		t.Fatalf("列 email 带前后空白不应被 trim 后的干净 env 匹配,得 role=%q", got)
	}

	// ⑩ padded-env × 干净列:与 ⑨ 对偶。env 带前后空白、列干净 → TrimSpace(env) 后命中并提升。
	//    这守生产码里 strings.TrimSpace(os.Getenv(...)) 真的在生效(而非摆设),补齐单测 ⑦ 只能
	//    间接证明的部分:此处真库真列被命中。
	//    变异:删掉生产码的 TrimSpace → env=' cleanrow@... ' 原样进 SQL,与干净列不等 → 不命中 → 仍
	//    user → RED。
	cleanRowUser := seedUserEmail(t, ctx, pool, tenantA, "cleanrow@example.test", RoleUser)
	t.Setenv(AdminBootstrapEmailEnv, "  cleanrow@example.test  ")
	if err := MaybeBootstrapAdminUser(ctx, pool, tenantA, nil); err != nil {
		t.Fatalf("bootstrap(env 带空白、列干净,应 trim 后命中): %v", err)
	}
	if got := roleOf(t, ctx, pool, tenantA, cleanRowUser); got != RoleAdmin {
		t.Fatalf("带前后空白的 env 应被 TrimSpace 后命中并提升,得 role=%q", got)
	}
}

// TestPG_BootstrapAdminUser_OnlyTargetTenantFlips 守「同邮箱存在于两个租户,以 tenantA 跑 bootstrap
// 时只有 tenantA 的行翻成 admin,tenantB 的同邮箱行保持 user」。这与主用例 ③ 的差别:③ 的 tenantA
// 侧没有匹配账号(纯隔离);此处两侧都有匹配账号,验证 UPDATE 恰好只改一行、不波及他租户。
// 变异:删 UPDATE 的 tenant_id 谓词 → 两个租户的同邮箱账号都被提升 → tenantB 断言 RED(跨租户越权)。
func TestPG_BootstrapAdminUser_OnlyTargetTenantFlips(t *testing.T) {
	ctx := context.Background()
	pool := openPool(t, ctx)

	tenantA := seedTenant(t, ctx, pool, "bootstrap-flip-a-"+t.Name())
	tenantB := seedTenant(t, ctx, pool, "bootstrap-flip-b-"+t.Name())
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM users WHERE tenant_id IN ($1,$2)`, tenantA, tenantB)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id IN ($1,$2)`, tenantA, tenantB)
	})

	const sharedEmail = "shared-ops@example.test"
	aUser := seedUserEmail(t, ctx, pool, tenantA, sharedEmail, RoleUser)
	bUser := seedUserEmail(t, ctx, pool, tenantB, sharedEmail, RoleUser)

	t.Setenv(AdminBootstrapEmailEnv, sharedEmail)
	if err := MaybeBootstrapAdminUser(ctx, pool, tenantA, nil); err != nil {
		t.Fatalf("bootstrap tenantA: %v", err)
	}
	if got := roleOf(t, ctx, pool, tenantA, aUser); got != RoleAdmin {
		t.Fatalf("目标租户 tenantA 的账号应被提升,得 role=%q", got)
	}
	if got := roleOf(t, ctx, pool, tenantB, bUser); got != RoleUser {
		t.Fatalf("非目标租户 tenantB 的同邮箱账号绝不应被提升,得 role=%q", got)
	}
}
