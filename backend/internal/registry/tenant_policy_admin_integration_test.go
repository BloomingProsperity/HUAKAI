// HUAKAI · iKun
//go:build integration_pg

// model_registry_tenant_policies(inherit_global_catalog)admin 写路径的集成测试(真 PostgreSQL, 需 HUAKAI_DATABASE_URL)。
package registry

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestPG_TenantInheritGlobalPolicy 守真 PG 下: 默认(无行)读为不继承 + 首写继承=true 并 bump 快照版本 +
// 读回 + 再写翻转 + 再 bump + 幂等 + 目标租户不存在→ErrUnknownTenant。
// 判别:
//   - SetTenantInheritGlobal 漏 bump 快照 → snapshot version 不增 → 红(client 缓存失效信号丢失)。
//   - inherit 没真写/读回错 → 读回断言红。
//   - FK 违反未映射 ErrUnknownTenant → 末段红。
func TestPG_TenantInheritGlobalPolicy(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newFixture(t, ctx, pool)
	t.Cleanup(func() {
		c := context.Background()
		// 先于 fixture 删 tenant: 清掉引用 tenant 的 policy/snapshot 行(无 ON DELETE CASCADE)。
		_, _ = pool.Exec(c, `DELETE FROM model_registry_snapshots WHERE tenant_id = $1`, f.tenantID)
		_, _ = pool.Exec(c, `DELETE FROM model_registry_tenant_policies WHERE tenant_id = $1`, f.tenantID)
	})

	r := NewPostgresRegistry(pool, nil)

	// 默认: 无策略行 → 读为不继承(对齐 resolve 语义)。
	got, err := r.GetTenantPolicy(ctx, f.tenantID)
	if err != nil {
		t.Fatalf("get default policy: %v", err)
	}
	if got.InheritGlobalCatalog {
		t.Fatalf("no-policy tenant should read inherit=false, got %+v", got)
	}

	// 首写 inherit=true → 返回 true; 快照版本应 bump 到 2(首写)。
	set, err := r.SetTenantInheritGlobal(ctx, f.tenantID, true, "admin-token:11")
	if err != nil {
		t.Fatalf("set inherit true: %v", err)
	}
	if !set.InheritGlobalCatalog || set.TenantID != f.tenantID || set.UpdatedByActor != "admin-token:11" {
		t.Fatalf("set result unexpected: %+v", set)
	}
	if v := readSnapshotVersion(t, pool, f.tenantID); v != 2 {
		t.Fatalf("snapshot version after first set=%d, want 2 (bump from default)", v)
	}
	// 读回 = true(真落库)。
	if back, _ := r.GetTenantPolicy(ctx, f.tenantID); !back.InheritGlobalCatalog {
		t.Fatalf("read-back after set true should be inherit=true, got %+v", back)
	}

	// 翻转 inherit=false → 读回 false; 版本再 +1 = 3。
	if _, err := r.SetTenantInheritGlobal(ctx, f.tenantID, false, "admin-token:11"); err != nil {
		t.Fatalf("set inherit false: %v", err)
	}
	if back, _ := r.GetTenantPolicy(ctx, f.tenantID); back.InheritGlobalCatalog {
		t.Fatalf("read-back after set false should be inherit=false, got %+v", back)
	}
	if v := readSnapshotVersion(t, pool, f.tenantID); v != 3 {
		t.Fatalf("snapshot version after second set=%d, want 3", v)
	}

	// 幂等: 再设 false → 仍 ok, 版本继续 +1 = 4(每次写都 bump, 信号语义可接受)。
	if _, err := r.SetTenantInheritGlobal(ctx, f.tenantID, false, "admin-token:11"); err != nil {
		t.Fatalf("idempotent set false: %v", err)
	}
	if v := readSnapshotVersion(t, pool, f.tenantID); v != 4 {
		t.Fatalf("snapshot version after idempotent set=%d, want 4", v)
	}

	// 目标租户不存在 → FK 违反 → ErrUnknownTenant。
	if _, err := r.SetTenantInheritGlobal(ctx, 999999999, true, "admin-token:11"); !errors.Is(err, ErrUnknownTenant) {
		t.Fatalf("set for nonexistent tenant: err=%v, want ErrUnknownTenant", err)
	}
}

func readSnapshotVersion(t *testing.T, pool *pgxpool.Pool, tenantID int64) int64 {
	t.Helper()
	var v int64
	if err := pool.QueryRow(context.Background(), `SELECT version FROM model_registry_snapshots WHERE tenant_id = $1`, tenantID).Scan(&v); err != nil {
		t.Fatalf("read snapshot version: %v", err)
	}
	return v
}
