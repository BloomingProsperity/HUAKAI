// HUAKAI · iKun
//go:build integration_pg

package adminsessionauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/panelauth"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

// 跨层集成:真 PostgresRoleStore(读真 users.role 列)插进组合 Resolver。
// 单测里 roleStore 被 stub 掉会掩盖"真库列读回的 role 与判定不一致"这类回归
// (对标 balance_credit 归属被下游 ParseInt 成 0 的假绿),故此处用真 handler 链:
// 真 session(stub 出已验证会话,携带真 tenant/user)→ 真 RoleStore 查真库 role → 真面板判定。

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

// seedUser 插入用户;role=="" 时不显式给 role(验证迁移列默认 'user' 走 deny-by-default)。
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

// fixedSession 是一个"已验证会话"stub:只固定返回一个 ValidatedSession(携带真 tenant/user),
// 让测试聚焦于"真库 role 列 → 面板判定"这一承重层(session 校验本身另有其包的集成测试)。
type fixedSession struct {
	sess usersession.ValidatedSession
}

func (f fixedSession) Validate(context.Context, string, string, string) (usersession.ValidatedSession, error) {
	return f.sess, nil
}

func getReq() *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/admin/v1/api-keys", nil)
	r.Header.Set("Authorization", "Bearer real-session")
	return r
}

// TestPG_SessionRoleGrantsAdminOverRealStore 守跨层不变量:真库 role 列驱动 admin 判定。
// 判别:
//   - role='admin' 真行 → 授平台级 admin(Source=session/UserID 取自会话/ScopeTenantID=0)。
//   - role='user' / 列默认(不给 role)真行 → 统一反枚举 ErrAdminUnauthorized(deny-by-default)。
//   - 软删 admin 真行(deleted_at 非空)→ RoleStore 返 ErrUserNotFound → 拒(注销账号不得授权)。
//   - 跨租户:tenantA 会话读 tenantB 的 admin 用户 → RoleStore tenant 谓词过滤 → 拒(串租户越权防线)。
//
// mutation(生产码):
//   - panelauth store_postgres UserRole 去掉 `AND deleted_at IS NULL` → 软删 admin 被授权 → 该断言红。
//   - store_postgres 去掉 tenant 谓词 → 跨租户读到他租户 admin role → 跨租户断言红。
//   - resolver 把 PanelForRole 判定反向 → user/默认行被误授 admin → deny 断言红。
func TestPG_SessionRoleGrantsAdminOverRealStore(t *testing.T) {
	ctx := context.Background()
	pool := openPool(t, ctx)

	tenantA := seedTenant(t, ctx, pool, "asa-a-"+t.Name())
	tenantB := seedTenant(t, ctx, pool, "asa-b-"+t.Name())
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM users WHERE tenant_id IN ($1,$2)`, tenantA, tenantB)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id IN ($1,$2)`, tenantA, tenantB)
	})

	adminUser := seedUser(t, ctx, pool, tenantA, panelauth.RoleAdmin)
	normalUser := seedUser(t, ctx, pool, tenantA, panelauth.RoleUser)
	defaultUser := seedUser(t, ctx, pool, tenantA, "") // 列默认 'user'
	bAdmin := seedUser(t, ctx, pool, tenantB, panelauth.RoleAdmin)

	roles := panelauth.NewPostgresRoleStore(pool)
	tok := &stubToken{err: admin.ErrAdminUnauthorized}

	// 真 admin 行 → 授平台级 admin,归属取自会话。
	{
		r := New(tok, fixedSession{sess: usersession.ValidatedSession{TenantID: tenantA, UserID: adminUser}}, roles, nil, tenantA)
		id, err := r.Resolve(ctx, getReq())
		if err != nil {
			t.Fatalf("真 admin 行经 session 通道应放行,得 err=%v", err)
		}
		if id.Role != admin.RolePlatformAdmin || id.Source != admin.AdminSourceSession {
			t.Fatalf("真 admin 行应授平台级/session 源,得 role=%q source=%q", id.Role, id.Source)
		}
		if id.UserID != adminUser || id.ScopeTenantID != 0 {
			t.Fatalf("归属应取自会话(UserID=%d)且 ScopeTenantID=0,得 UserID=%d Scope=%d", adminUser, id.UserID, id.ScopeTenantID)
		}
	}

	// role='user' 真行 → 拒。
	{
		r := New(tok, fixedSession{sess: usersession.ValidatedSession{TenantID: tenantA, UserID: normalUser}}, roles, nil, tenantA)
		if _, err := r.Resolve(ctx, getReq()); err != admin.ErrAdminUnauthorized {
			t.Fatalf("真 user 行应 deny-by-default,得 err=%v", err)
		}
	}

	// 列默认(未显式给 role)真行 → 拒(证明默认 'user' 不误授)。
	{
		r := New(tok, fixedSession{sess: usersession.ValidatedSession{TenantID: tenantA, UserID: defaultUser}}, roles, nil, tenantA)
		if _, err := r.Resolve(ctx, getReq()); err != admin.ErrAdminUnauthorized {
			t.Fatalf("列默认 role 行应 deny,得 err=%v", err)
		}
	}

	// 软删 admin → RoleStore 返 ErrUserNotFound → 拒。
	{
		softAdmin := seedUser(t, ctx, pool, tenantA, panelauth.RoleAdmin)
		if _, err := pool.Exec(ctx, `UPDATE users SET deleted_at = now() WHERE id = $1`, softAdmin); err != nil {
			t.Fatalf("soft-delete: %v", err)
		}
		r := New(tok, fixedSession{sess: usersession.ValidatedSession{TenantID: tenantA, UserID: softAdmin}}, roles, nil, tenantA)
		if _, err := r.Resolve(ctx, getReq()); err != admin.ErrAdminUnauthorized {
			t.Fatalf("软删 admin 应拒,得 err=%v", err)
		}
	}

	// 跨租户:tenantA 会话拿去查 tenantB 的 admin 用户 → tenant 谓词过滤 → not found → 拒。
	{
		r := New(tok, fixedSession{sess: usersession.ValidatedSession{TenantID: tenantA, UserID: bAdmin}}, roles, nil, tenantA)
		if _, err := r.Resolve(ctx, getReq()); err != admin.ErrAdminUnauthorized {
			t.Fatalf("跨租户读他租户 admin 应拒(tenant 隔离),得 err=%v", err)
		}
	}

	// 租户停用后,其 active admin 的既有 session 也必须立即掉权。
	{
		if _, err := pool.Exec(ctx, `UPDATE tenants SET status='suspended' WHERE id=$1`, tenantA); err != nil {
			t.Fatalf("停用租户: %v", err)
		}
		r := New(tok, fixedSession{sess: usersession.ValidatedSession{TenantID: tenantA, UserID: adminUser}}, roles, nil, tenantA)
		if _, err := r.Resolve(ctx, getReq()); err != admin.ErrAdminUnauthorized {
			t.Fatalf("停用租户内的 admin session 应拒,得 err=%v", err)
		}
	}
}

// TestPG_SessionAdminWriteMethodStillDeniedOverRealStore 守:即便真库 role='admin',
// 写方法经 session 通道仍被只读 gate 拒(灰度期写路径物理上够不到)。
// 用真 RoleStore 确保 gate 拒发生在"真 admin 已确认"之后,而非因 role 判定顺带拒。
// mutation:删 resolver 的 isReadOnlyMethod gate → 真 admin 的 POST 被放行 → 断言红。
func TestPG_SessionAdminWriteMethodStillDeniedOverRealStore(t *testing.T) {
	ctx := context.Background()
	pool := openPool(t, ctx)
	tenant := seedTenant(t, ctx, pool, "asa-w-"+t.Name())
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM users WHERE tenant_id = $1`, tenant)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id = $1`, tenant)
	})
	adminUser := seedUser(t, ctx, pool, tenant, panelauth.RoleAdmin)
	roles := panelauth.NewPostgresRoleStore(pool)
	tok := &stubToken{err: admin.ErrAdminUnauthorized}
	r := New(tok, fixedSession{sess: usersession.ValidatedSession{TenantID: tenant, UserID: adminUser}}, roles, nil, tenant)

	// 先证真 admin 的 GET 放行(基线,排除是 role 判定拒的干扰)。
	if _, err := r.Resolve(ctx, getReq()); err != nil {
		t.Fatalf("真 admin GET 基线应放行,得 %v", err)
	}
	// 再证同一真 admin 的写方法被 gate 拒。
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		wr := httptest.NewRequest(m, "/admin/v1/api-keys", nil)
		wr.Header.Set("Authorization", "Bearer real-session")
		if _, err := r.Resolve(ctx, wr); err != admin.ErrAdminUnauthorized {
			t.Fatalf("真 admin 的 %s 应被只读 gate 拒,得 %v", m, err)
		}
	}
}
