//go:build integration_pg

package proxyadmin

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

// 针对 proxyadmin 闭环的强真实 Postgres 测试。单元测试用桩 Querier;这些测试
// 针对真实 DB + 真实加密 + 真实租户过滤来证明安全属性——即单元桩触达不到的危害面。
// 运行:HUAKAI_DATABASE_URL=<gate dsn> go test -tags=integration_pg
//   -p 1 ./internal/proxyadmin/...

func openProxyPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("open PG: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedProxyTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, label string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"proxyadmin-"+label+"-"+uuid.NewString(),
	).Scan(&id); err != nil {
		t.Fatalf("seed tenant %s: %v", label, err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM proxies WHERE tenant_id=$1`, id)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id=$1`, id)
	})
	return id
}

func strptr(s string) *string { return &s }

// TestProxy_SecretEncryptedAtRest 证明代理 auth_secret 在落列之前已被加密——
// 明文凭据在 proxies 表里绝不可被读出。变异:绕过 encryptAuthSecret(存原文)→
// 该列原文等于明文 → 转红。
func TestProxy_SecretEncryptedAtRest(t *testing.T) {
	ctx := context.Background()
	pool := openProxyPool(t, ctx)
	tenantID := seedProxyTenant(t, ctx, pool, "enc")
	svc := New(admindb.New(pool), testKeys(t))

	const plaintext = "SUPER-SECRET-PROXY-CREDENTIAL-9f3a"
	p, err := svc.Create(ctx, CreateInput{
		TenantID: tenantID, Name: "enc-proxy", Protocol: "http", Host: "10.0.0.9", Port: 3128,
		AuthUsername: strptr("puser"), AuthSecret: strptr(plaintext),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	var stored *string
	if err := pool.QueryRow(ctx, `SELECT auth_secret FROM proxies WHERE id=$1 AND tenant_id=$2`, p.ID, tenantID).Scan(&stored); err != nil {
		t.Fatalf("read raw auth_secret: %v", err)
	}
	if stored == nil || *stored == "" {
		t.Fatalf("auth_secret must be persisted (encrypted), got empty/nil")
	}
	if *stored == plaintext {
		t.Fatalf("auth_secret stored as PLAINTEXT at rest — encryption bypassed")
	}
	if strings.Contains(*stored, plaintext) {
		t.Fatalf("ciphertext contains the plaintext substring — not encrypted")
	}
}

// TestProxy_CrossTenantIsolation 是核心安全测试:租户 B 绝不能看到、读取、修改
// 或删除租户 A 的代理。变异:从任一代理查询中删掉 tenant_id 谓词 → B 泄露/修改
// A 的行 → 转红。
func TestProxy_CrossTenantIsolation(t *testing.T) {
	ctx := context.Background()
	pool := openProxyPool(t, ctx)
	tenantA := seedProxyTenant(t, ctx, pool, "a")
	tenantB := seedProxyTenant(t, ctx, pool, "b")
	svc := New(admindb.New(pool), testKeys(t))

	a, err := svc.Create(ctx, CreateInput{
		TenantID: tenantA, Name: "a-proxy", Protocol: "socks5", Host: "10.0.0.1", Port: 1080,
		AuthSecret: strptr("a-secret"),
	})
	if err != nil {
		t.Fatalf("create A: %v", err)
	}

	// B 的列表绝不能含 A 的代理。
	bList, err := svc.List(ctx, tenantB)
	if err != nil {
		t.Fatalf("list B: %v", err)
	}
	for _, p := range bList {
		if p.ID == a.ID {
			t.Fatalf("cross-tenant leak: tenant B list contains tenant A proxy %d", a.ID)
		}
	}

	// B 无法读取 A 的代理。
	if _, err := svc.Get(ctx, tenantB, a.ID); err != ErrNotFound {
		t.Fatalf("cross-tenant Get must be ErrNotFound, got %v", err)
	}

	// B 删除 A 的 id 是 no-op;A 的代理存活。
	if err := svc.Delete(ctx, tenantB, a.ID); err != nil {
		t.Fatalf("cross-tenant delete should be a tenant-scoped no-op, got err %v", err)
	}
	if _, err := svc.Get(ctx, tenantA, a.ID); err != nil {
		t.Fatalf("tenant A proxy must survive B's delete attempt, got %v", err)
	}

	// B 翻转 A 的 id 状态是 no-op;A 的状态不变(仍为 active)。
	if err := svc.SetStatus(ctx, tenantB, a.ID, "disabled"); err != nil {
		t.Fatalf("cross-tenant set-status should be a no-op, got err %v", err)
	}
	got, err := svc.Get(ctx, tenantA, a.ID)
	if err != nil {
		t.Fatalf("get A after B status attempt: %v", err)
	}
	if got.Status != "active" {
		t.Fatalf("tenant B must not flip tenant A status; got %q want active", got.Status)
	}
}

// TestProxy_LifecycleAndSecretFreeReads 演练真实的 create->status->delete 状态机,
// 并证明读取结构体暴露的是非凭据字段。Proxy 类型在结构上不含凭据(无该字段),
// 故返回的结构体不可能发生泄露;本测试确认这些值经真实 DB 正确往返。
func TestProxy_LifecycleAndSecretFreeReads(t *testing.T) {
	ctx := context.Background()
	pool := openProxyPool(t, ctx)
	tenantID := seedProxyTenant(t, ctx, pool, "life")
	svc := New(admindb.New(pool), testKeys(t))

	p, err := svc.Create(ctx, CreateInput{
		TenantID: tenantID, Name: "life-proxy", Protocol: "https", Host: "proxy.internal", Port: 8443,
		AuthUsername: strptr("u"), AuthSecret: strptr("s"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.Name != "life-proxy" || p.Protocol != "https" || p.Host != "proxy.internal" || p.Port != 8443 || p.Status != "active" {
		t.Fatalf("create round-trip mismatch: %+v", p)
	}

	if err := svc.SetStatus(ctx, tenantID, p.ID, "disabled"); err != nil {
		t.Fatalf("set-status: %v", err)
	}
	got, err := svc.Get(ctx, tenantID, p.ID)
	if err != nil || got.Status != "disabled" {
		t.Fatalf("status must be disabled after SetStatus; got %+v err=%v", got, err)
	}

	if err := svc.Delete(ctx, tenantID, p.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.Get(ctx, tenantID, p.ID); err != ErrNotFound {
		t.Fatalf("soft-deleted proxy must read ErrNotFound, got %v", err)
	}
	list, err := svc.List(ctx, tenantID)
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	for _, lp := range list {
		if lp.ID == p.ID {
			t.Fatalf("soft-deleted proxy must not appear in list")
		}
	}
}
