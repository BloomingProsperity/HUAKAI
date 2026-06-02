// HUAKAI · iKun
//go:build integration_pg

package routeadmin

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
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

func seedPoolGroup(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64, name string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx, `INSERT INTO pool_groups (tenant_id, name) VALUES ($1,$2) RETURNING id`, tenantID, name).Scan(&id); err != nil {
		t.Fatalf("seed pool_group: %v", err)
	}
	return id
}

// TestPG_RouteCRUD 守真 PG 下 create→get→list→softdelete 全链 + 租户隔离 + 重名 + 坏 FK + 软删排除。
// 判别:
//   - List 漏 tenant 谓词 → 另租户 route 泄入 → tenant 隔离断言红(串租户配置=安全)。
//   - 软删谓词写错(漏 deleted_at) → 删后仍出现在 List → 红。
//   - 唯一索引/FK 错误映射丢失 → 期望的 ErrDuplicateName/ErrPoolGroupNotFound 落空 → 红。
func TestPG_RouteCRUD(t *testing.T) {
	ctx := context.Background()
	pool := openPool(t, ctx)
	sfx := uuid.NewString()

	tenantA := seedTenant(t, ctx, pool, "ra-"+sfx)
	tenantB := seedTenant(t, ctx, pool, "rb-"+sfx)
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM routes WHERE tenant_id IN ($1,$2)`, tenantA, tenantB)
		_, _ = pool.Exec(c, `DELETE FROM pool_groups WHERE tenant_id IN ($1,$2)`, tenantA, tenantB)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id IN ($1,$2)`, tenantA, tenantB)
	})
	pgA := seedPoolGroup(t, ctx, pool, tenantA, "pgA-"+sfx)
	pgB := seedPoolGroup(t, ctx, pool, tenantB, "pgB-"+sfx)

	store := NewPostgresStore(pool)
	svc := NewService(store, nil)

	// create
	created, err := svc.Create(ctx, CreateInput{TenantID: tenantA, Name: "premium-claude-" + sfx, UserGroupMatch: "premium", ModelPatternMatch: "claude-*", PoolGroupID: pgA, AdminID: 1})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == 0 || created.MatchPriority != 100 || !created.Enabled {
		t.Fatalf("created route unexpected: %+v (want id!=0, prio=100 default, enabled)", created)
	}

	// get
	got, err := svc.Get(ctx, tenantA, created.ID)
	if err != nil || got.ID != created.ID || got.PoolGroupID != pgA {
		t.Fatalf("get: route=%+v err=%v", got, err)
	}

	// tenant B 也建一条(用 pgB), 双向强化隔离: A 列表不含 B 的, B 列表不含 A 的。
	bRoute, err := svc.Create(ctx, CreateInput{TenantID: tenantB, Name: "b-route-" + sfx, UserGroupMatch: "premium", ModelPatternMatch: "*", PoolGroupID: pgB, AdminID: 1})
	if err != nil {
		t.Fatalf("create tenant B route: %v", err)
	}
	la, err := svc.List(ctx, tenantA)
	if err != nil || len(la) != 1 || la[0].ID != created.ID {
		t.Fatalf("list A: %+v err=%v, want exactly tenant A's route", la, err)
	}
	lb, err := svc.List(ctx, tenantB)
	if err != nil || len(lb) != 1 || lb[0].ID != bRoute.ID {
		t.Fatalf("list B: %+v err=%v, want exactly tenant B's route (cross-tenant isolation)", lb, err)
	}

	// 重名(同租户未软删) → ErrDuplicateName
	if _, err := svc.Create(ctx, CreateInput{TenantID: tenantA, Name: "premium-claude-" + sfx, UserGroupMatch: "premium", ModelPatternMatch: "*", PoolGroupID: pgA, AdminID: 1}); !errors.Is(err, ErrDuplicateName) {
		t.Fatalf("dup name: err=%v, want ErrDuplicateName", err)
	}

	// 坏 pool_group(不存在的 id) → ErrPoolGroupNotFound
	if _, err := svc.Create(ctx, CreateInput{TenantID: tenantA, Name: "bad-fk-" + sfx, UserGroupMatch: "premium", ModelPatternMatch: "*", PoolGroupID: 999999999, AdminID: 1}); !errors.Is(err, ErrPoolGroupNotFound) {
		t.Fatalf("bad pool_group: err=%v, want ErrPoolGroupNotFound", err)
	}

	// 跨租户(S1 retro): tenant A 引用属于 tenant B 的 pgB → 拒, 绝不建越租户路由。
	// mutation: Create 退回无 WHERE EXISTS 的裸 INSERT(只靠单列 FK)→ 此处插入成功 → 红。
	if _, err := svc.Create(ctx, CreateInput{TenantID: tenantA, Name: "cross-tenant-" + sfx, UserGroupMatch: "premium", ModelPatternMatch: "*", PoolGroupID: pgB, AdminID: 1}); !errors.Is(err, ErrPoolGroupNotFound) {
		t.Fatalf("cross-tenant pool_group: err=%v, want ErrPoolGroupNotFound (pgB 属 tenantB)", err)
	}

	// 软删 → List 排除 + Get not found
	if _, err := svc.Delete(ctx, tenantA, created.ID, 2); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if la2, _ := svc.List(ctx, tenantA); len(la2) != 0 {
		t.Fatalf("after soft-delete list A = %d, want 0", len(la2))
	}
	if _, err := svc.Get(ctx, tenantA, created.ID); !errors.Is(err, ErrRouteNotFound) {
		t.Fatalf("get after delete: err=%v, want ErrRouteNotFound", err)
	}
	// 软删后同名可再建(uq 索引带 WHERE deleted_at IS NULL)
	if _, err := svc.Create(ctx, CreateInput{TenantID: tenantA, Name: "premium-claude-" + sfx, UserGroupMatch: "premium", ModelPatternMatch: "claude-*", PoolGroupID: pgA, AdminID: 1}); err != nil {
		t.Fatalf("recreate same name after soft-delete: %v (unique index should be partial on deleted_at IS NULL)", err)
	}
}
