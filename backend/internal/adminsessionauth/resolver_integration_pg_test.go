//go:build integration_pg

package adminsessionauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

type pgScopeFixture struct {
	rootA      int64
	resellerA  int64
	directA    int64
	grandA     int64
	siblingA   int64
	rootB      int64
	resellerB  int64
	rootAdmin  int64
	childAdmin int64
	bAdmin     int64
}

func TestPGRootAndChildSessionIdentityChain(t *testing.T) {
	ctx := context.Background()
	pool := openIsolatedScopePool(t, ctx)
	fixture := seedScopeFixture(t, ctx, pool)
	store := NewPostgresIdentityStore(pool)
	token := &stubToken{err: admin.ErrAdminUnauthorized}

	rootResolver := New(token, fixedSession{sess: usersession.ValidatedSession{
		TenantID: fixture.rootA, UserID: fixture.rootAdmin,
	}}, store, nil)
	rootIdentity, err := rootResolver.Resolve(ctx, getReq())
	if err != nil {
		t.Fatalf("根 admin session 解析失败: %v", err)
	}
	if !rootIdentity.IsPlatformWide() || rootIdentity.Role != admin.RolePlatformAdmin || rootIdentity.ScopeTenantID() != 0 {
		t.Fatalf("破坏点→根 session 不再保持平台语义时本断言转红: role=%q scope=%d platform=%v",
			rootIdentity.Role, rootIdentity.ScopeTenantID(), rootIdentity.IsPlatformWide())
	}
	for _, target := range []int64{fixture.rootA, fixture.grandA, fixture.rootB, fixture.resellerB} {
		if err := rootIdentity.CanActOnTenant(target); err != nil {
			t.Fatalf("破坏点→平台根不再全域时 target=%d 断言转红: %v", target, err)
		}
	}

	childResolver := New(token, fixedSession{sess: usersession.ValidatedSession{
		TenantID: fixture.resellerA, UserID: fixture.childAdmin,
	}}, store, nil)
	childIdentity, err := childResolver.Resolve(ctx, getReq())
	if err != nil {
		t.Fatalf("子租户 admin session 解析失败: %v", err)
	}
	if childIdentity.Role != admin.RoleTenantOperator || childIdentity.ScopeTenantID() != fixture.resellerA || childIdentity.IsPlatformWide() {
		t.Fatalf("破坏点→session resolver 恢复旧全权返回时字段断言转红: role=%q scope=%d platform=%v",
			childIdentity.Role, childIdentity.ScopeTenantID(), childIdentity.IsPlatformWide())
	}
	for _, target := range []int64{fixture.resellerA, fixture.directA, fixture.grandA} {
		if err := childIdentity.CanActOnTenant(target); err != nil {
			t.Fatalf("破坏点→递归 CTE 退化为单层时孙级 target=%d 断言转红: %v", target, err)
		}
	}
	for _, target := range []int64{fixture.rootA, fixture.siblingA, fixture.rootB, fixture.resellerB, 999999999} {
		if err := childIdentity.CanActOnTenant(target); !errors.Is(err, admin.ErrAdminForbidden) {
			t.Fatalf("破坏点→删除非子树拒绝分支或恢复旧全权路径时 target=%d 断言转红: %v", target, err)
		}
	}
	if err := childIdentity.CanAccessProviderAccountControlPlane(); !errors.Is(err, admin.ErrAdminForbidden) {
		t.Fatalf("破坏点→删除分销商凭证控制面守卫时本断言转红: %v", err)
	}
}

func TestPGSessionIdentityRejectsInactiveAndCrossTenantSubjects(t *testing.T) {
	ctx := context.Background()
	pool := openIsolatedScopePool(t, ctx)
	fixture := seedScopeFixture(t, ctx, pool)
	store := NewPostgresIdentityStore(pool)
	token := &stubToken{err: admin.ErrAdminUnauthorized}
	resolve := func(tenantID, userID int64) error {
		resolver := New(token, fixedSession{sess: usersession.ValidatedSession{
			TenantID: tenantID, UserID: userID,
		}}, store, nil)
		_, err := resolver.Resolve(ctx, getReq())
		return err
	}

	if _, err := pool.Exec(ctx, `UPDATE tenants SET status='suspended' WHERE id=$1`, fixture.resellerA); err != nil {
		t.Fatalf("停用子租户: %v", err)
	}
	if err := resolve(fixture.resellerA, fixture.childAdmin); !errors.Is(err, admin.ErrAdminUnauthorized) {
		t.Fatalf("破坏点→session 身份查询漏掉 tenant.status 时本断言转红: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE tenants SET status='active' WHERE id=$1`, fixture.resellerA); err != nil {
		t.Fatalf("恢复子租户: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET status='suspended' WHERE id=$1`, fixture.childAdmin); err != nil {
		t.Fatalf("停用子租户管理员: %v", err)
	}
	if err := resolve(fixture.resellerA, fixture.childAdmin); !errors.Is(err, admin.ErrAdminUnauthorized) {
		t.Fatalf("破坏点→session 身份查询漏掉 user.status 时本断言转红: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET status='active' WHERE id=$1`, fixture.childAdmin); err != nil {
		t.Fatalf("恢复子租户管理员: %v", err)
	}
	if err := resolve(fixture.resellerA, fixture.bAdmin); !errors.Is(err, admin.ErrAdminUnauthorized) {
		t.Fatalf("破坏点→删除 user+tenant 精确谓词时跨租户断言转红: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET deleted_at=now() WHERE id=$1`, fixture.childAdmin); err != nil {
		t.Fatalf("软删子租户管理员: %v", err)
	}
	if err := resolve(fixture.resellerA, fixture.childAdmin); !errors.Is(err, admin.ErrAdminUnauthorized) {
		t.Fatalf("破坏点→session 身份查询漏掉 user.deleted_at 时本断言转红: %v", err)
	}
}

func TestPGScopedAdminTokenLoadsRecursiveSubtree(t *testing.T) {
	ctx := context.Background()
	pool := openIsolatedScopePool(t, ctx)
	fixture := seedScopeFixture(t, ctx, pool)
	plaintext := "hk_admin_slice2_scope_token_0000000001"
	hash, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("生成 token hash: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO admin_tokens (name,key_hash,key_prefix,role,scope_tenant_id,status)
VALUES ($1,$2,$3,'tenant_operator',$4,'active')`,
		"slice2-child", string(hash), plaintext[:admin.PrefixLen], fixture.resellerA); err != nil {
		t.Fatalf("插入 scoped admin token: %v", err)
	}

	resolver := admin.NewAdminResolver(admindb.New(pool))
	request := httptest.NewRequest(http.MethodGet, "/admin/v1/api-keys", nil)
	request.Header.Set("Authorization", "Bearer "+plaintext)
	identity, err := resolver.Resolve(ctx, request)
	if err != nil {
		t.Fatalf("解析 scoped admin token: %v", err)
	}
	if identity.Role != admin.RoleTenantOperator || identity.ScopeTenantID() != fixture.resellerA {
		t.Fatalf("破坏点→token resolver 恢复旧全权/丢 scope 时字段断言转红: role=%q scope=%d",
			identity.Role, identity.ScopeTenantID())
	}
	if err := identity.CanActOnTenant(fixture.grandA); err != nil {
		t.Fatalf("破坏点→递归 CTE 改成只查一层时孙级 token 用例转红: %v", err)
	}
	for _, target := range []int64{fixture.siblingA, fixture.rootA, fixture.resellerB} {
		if err := identity.CanActOnTenant(target); !errors.Is(err, admin.ErrAdminForbidden) {
			t.Fatalf("破坏点→token resolver 恢复全权路径时 target=%d 断言转红: %v", target, err)
		}
	}
}

func TestPGTenantScopeRejectsDepthOverflowAndCycle(t *testing.T) {
	ctx := context.Background()
	pool := openIsolatedScopePool(t, ctx)
	root := insertTenant(t, ctx, pool, "depth-root", nil)
	deepRoot := insertTenant(t, ctx, pool, "depth-child-0", &root)
	parent := deepRoot
	for index := 1; index <= int(admin.MaxTenantScopeDepth)+1; index++ {
		parent = insertTenant(t, ctx, pool, fmt.Sprintf("depth-child-%d", index), &parent)
	}
	deepAdmin := insertAdminUser(t, ctx, pool, deepRoot)
	store := NewPostgresIdentityStore(pool)
	if _, err := store.ResolveActiveAdminIdentity(ctx, deepRoot, deepAdmin); !errors.Is(err, admin.ErrAdminUnauthorized) {
		t.Fatalf("破坏点→删除递归深度上限或超限拒绝时本断言转红: %v", err)
	}

	cycleRoot := insertTenant(t, ctx, pool, "cycle-root", &root)
	cycleChild := insertTenant(t, ctx, pool, "cycle-child", &cycleRoot)
	if _, err := pool.Exec(ctx, `UPDATE tenants SET parent_tenant_id=$1 WHERE id=$2`, cycleChild, cycleRoot); err != nil {
		t.Fatalf("构造测试环: %v", err)
	}
	cycleAdmin := insertAdminUser(t, ctx, pool, cycleRoot)
	if _, err := store.ResolveActiveAdminIdentity(ctx, cycleRoot, cycleAdmin); !errors.Is(err, admin.ErrAdminUnauthorized) {
		t.Fatalf("破坏点→删除递归环检测时本断言转红: %v", err)
	}
}

func TestPGTenantScopeRecursiveStepUsesParentIndex(t *testing.T) {
	ctx := context.Background()
	pool := openIsolatedScopePool(t, ctx)
	fixture := seedScopeFixture(t, ctx, pool)
	if _, err := pool.Exec(ctx, `SET enable_seqscan=off`); err != nil {
		t.Fatalf("关闭顺序扫描: %v", err)
	}
	rows, err := pool.Query(ctx, `EXPLAIN (COSTS OFF)
WITH RECURSIVE tenant_scope AS (
    SELECT t.id, 0::integer AS depth, ARRAY[t.id]::bigint[] AS visited_ids,
           false AS cycle_detected,
           (t.parent_tenant_id IS NOT NULL) AS scope_root_is_child
    FROM tenants t
    WHERE t.id=$1 AND t.deleted_at IS NULL AND t.status='active'
    UNION
    SELECT child.id, parent.depth+1, parent.visited_ids || child.id,
           child.id = ANY(parent.visited_ids),
           parent.scope_root_is_child
    FROM tenant_scope parent
    JOIN tenants child ON child.parent_tenant_id=parent.id
                      AND child.deleted_at IS NULL AND child.status='active'
    WHERE parent.depth < 33 AND NOT parent.cycle_detected
)
SELECT id,depth,cycle_detected,scope_root_is_child FROM tenant_scope ORDER BY depth,id`, fixture.resellerA)
	if err != nil {
		t.Fatalf("解释递归查询: %v", err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("读取执行计划: %v", err)
		}
		plan.WriteString(line)
		plan.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("遍历执行计划: %v", err)
	}
	if !strings.Contains(plan.String(), "idx_tenants_parent_active") {
		t.Fatalf("破坏点→递归 JOIN 不再利用 parent_active 索引时本断言转红:\n%s", plan.String())
	}
}

func TestPGSessionAdminWriteMethodStillDeniedOverRealStore(t *testing.T) {
	ctx := context.Background()
	pool := openIsolatedScopePool(t, ctx)
	fixture := seedScopeFixture(t, ctx, pool)
	resolver := New(&stubToken{err: admin.ErrAdminUnauthorized}, fixedSession{sess: usersession.ValidatedSession{
		TenantID: fixture.rootA, UserID: fixture.rootAdmin,
	}}, NewPostgresIdentityStore(pool), nil)
	if _, err := resolver.Resolve(ctx, getReq()); err != nil {
		t.Fatalf("真 root admin GET 基线应放行: %v", err)
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		request := httptest.NewRequest(method, "/admin/v1/api-keys", nil)
		request.Header.Set("Authorization", "Bearer real-session")
		if _, err := resolver.Resolve(ctx, request); !errors.Is(err, admin.ErrAdminUnauthorized) {
			t.Fatalf("破坏点→删除 session 写分级时 method=%s 断言转红: %v", method, err)
		}
	}
}

func openIsolatedScopePool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL 未设置，跳过 integration_pg")
	}
	adminConn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("连接维护库: %v", err)
	}
	schema := fmt.Sprintf("slice2_auth_%d", time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := adminConn.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		_ = adminConn.Close(ctx)
		t.Fatalf("创建隔离 schema: %v", err)
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		_ = adminConn.Close(ctx)
		t.Fatalf("解析数据库连接串: %v", err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	config.MaxConns = 4
	config.MinConns = 0
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		_, _ = adminConn.Exec(ctx, "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE")
		_ = adminConn.Close(ctx)
		t.Fatalf("创建隔离连接池: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = adminConn.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE")
		_ = adminConn.Close(context.Background())
	})
	createScopeTables(t, ctx, pool)
	return pool
}

func createScopeTables(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(ctx, `
CREATE TABLE tenants (
    id bigserial PRIMARY KEY,
    name text NOT NULL UNIQUE,
    status text NOT NULL DEFAULT 'active',
    parent_tenant_id bigint REFERENCES tenants(id),
    deleted_at timestamptz
);
CREATE INDEX idx_tenants_parent_active ON tenants(parent_tenant_id,id) WHERE deleted_at IS NULL;
CREATE TABLE users (
    id bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id),
    role text NOT NULL DEFAULT 'user',
    status text NOT NULL DEFAULT 'active',
    deleted_at timestamptz
);
CREATE TABLE admin_tokens (
    id bigserial PRIMARY KEY,
    name text NOT NULL,
    key_hash text NOT NULL,
    key_prefix text NOT NULL,
    role text NOT NULL,
    scope_tenant_id bigint REFERENCES tenants(id),
    bootstrap boolean NOT NULL DEFAULT false,
    status text NOT NULL DEFAULT 'active',
    expires_at timestamptz,
    deleted_at timestamptz
);
CREATE INDEX idx_admin_tokens_prefix ON admin_tokens(key_prefix) WHERE deleted_at IS NULL AND status='active';`)
	if err != nil {
		t.Fatalf("创建隔离测试表: %v", err)
	}
}

func seedScopeFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) pgScopeFixture {
	t.Helper()
	var fixture pgScopeFixture
	fixture.rootA = insertTenant(t, ctx, pool, "root-a", nil)
	fixture.resellerA = insertTenant(t, ctx, pool, "reseller-a", &fixture.rootA)
	fixture.directA = insertTenant(t, ctx, pool, "direct-a", &fixture.resellerA)
	fixture.grandA = insertTenant(t, ctx, pool, "grand-a", &fixture.directA)
	fixture.siblingA = insertTenant(t, ctx, pool, "sibling-a", &fixture.rootA)
	fixture.rootB = insertTenant(t, ctx, pool, "root-b", nil)
	fixture.resellerB = insertTenant(t, ctx, pool, "reseller-b", &fixture.rootB)
	fixture.rootAdmin = insertAdminUser(t, ctx, pool, fixture.rootA)
	fixture.childAdmin = insertAdminUser(t, ctx, pool, fixture.resellerA)
	fixture.bAdmin = insertAdminUser(t, ctx, pool, fixture.resellerB)
	return fixture
}

func insertTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string, parentID *int64) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants(name,parent_tenant_id) VALUES($1,$2) RETURNING id`, name, parentID).Scan(&id); err != nil {
		t.Fatalf("插入租户 %s: %v", name, err)
	}
	return id
}

func insertAdminUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx, `INSERT INTO users(tenant_id,role) VALUES($1,'admin') RETURNING id`, tenantID).Scan(&id); err != nil {
		t.Fatalf("插入 tenant=%d admin: %v", tenantID, err)
	}
	return id
}

type fixedSession struct {
	sess usersession.ValidatedSession
}

func (f fixedSession) Validate(context.Context, string, string, string) (usersession.ValidatedSession, error) {
	return f.sess, nil
}

func getReq() *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/admin/v1/api-keys", nil)
	request.Header.Set("Authorization", "Bearer real-session")
	return request
}
