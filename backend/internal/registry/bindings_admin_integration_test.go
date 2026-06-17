//go:build integration_pg

// model_pool_bindings admin 写路径的集成测试(真 PostgreSQL,需 HUAKAI_DATABASE_URL)。
// 复用 postgres_registry_integration_test.go 的 fixture harness(同包)。
// 钉死头号不变量 + 关键安全行为(判别式见各用例注释):
//   - Create / Delete(删最后一条)都让 snapshot.version v→v+1(独立读断言)。
//   - 全局 model 可绑(正确继承谓词;naive tenant_id=$ 会误拒)。
//   - by-id 跨租户读/改/删 = ErrBindingNotFound(查询层 tenant 域,非仅靠门)。
//   - 重复 (tenant,model,pool) = ErrBindingConflict;借他租 pool = ErrPoolGroupNotFound。
//   - admin list 含 disabled 绑定(区别于路由读的 enabled 过滤)。
package registry

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func readSnapVer(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64) int64 {
	t.Helper()
	var v int64
	if err := pool.QueryRow(ctx, `SELECT version FROM model_registry_snapshots WHERE tenant_id = $1`, tenantID).Scan(&v); err != nil {
		t.Fatalf("read snapshot version: %v", err)
	}
	return v
}

func baseCreate(f *registryFixture, modelID int64) CreateBindingInput {
	return CreateBindingInput{
		TenantID: f.tenantID, ModelID: modelID, PoolGroupID: f.poolGroupID,
		Priority: 100, Weight: 1, SelectionMode: "strict_priority", FallbackClass: "normal", Enabled: true,
		Actor: "test", Reason: "integration",
	}
}

// Create 在同 Tx 内 bump snapshot.version。setSnapshot(7) → 创建 → 必须 8。
// 判别:去掉 CreatePoolBinding 里的 bumpAffectedSnapshots → 版本仍 7 → 红。
func TestBindingsAdmin_CreateBumpsSnapshot(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newFixture(t, ctx, pool)
	mid := f.seedModel(modelOpts{canonicalID: "mb-create-" + f.suffix, providerModelID: "pm"})
	f.setSnapshot(7)

	r := NewPostgresRegistry(pool, nil)
	if _, err := r.CreatePoolBinding(ctx, baseCreate(f, mid)); err != nil {
		t.Fatalf("CreatePoolBinding: %v", err)
	}
	if v := readSnapVer(t, ctx, pool, f.tenantID); v != 8 {
		t.Fatalf("snapshot version=%d want 8(7→8)", v)
	}
}

// Delete(删某租户某 model 最后一条存活绑定)仍 bump version。
// 判别:DELETE 改回用 model-id CTE(软删后命不中该行)→ 版本不变 → 红。
func TestBindingsAdmin_DeleteLastBindingBumps(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newFixture(t, ctx, pool)
	mid := f.seedModel(modelOpts{canonicalID: "mb-del-" + f.suffix, providerModelID: "pm"})
	f.setSnapshot(7)

	r := NewPostgresRegistry(pool, nil)
	got, err := r.CreatePoolBinding(ctx, baseCreate(f, mid))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	v0 := readSnapVer(t, ctx, pool, f.tenantID) // 8
	if err := r.DeletePoolBinding(ctx, got.ID, f.tenantID, "test", ""); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if v := readSnapVer(t, ctx, pool, f.tenantID); v != v0+1 {
		t.Fatalf("删后 version=%d want %d(删最后一条仍须 bump)", v, v0+1)
	}
}

// 全局 model 可绑(继承路径)。判别:谓词改成 naive tenant_id=$ → ErrModelNotBindable → 红。
func TestBindingsAdmin_GlobalModelBindable(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newFixture(t, ctx, pool)
	gmid := f.seedModel(modelOpts{scope: "global", canonicalID: "mb-global-" + f.suffix, providerModelID: "gpm"})

	r := NewPostgresRegistry(pool, nil)
	if _, err := r.CreatePoolBinding(ctx, baseCreate(f, gmid)); err != nil {
		t.Fatalf("全局 model 应可绑,却 %v", err)
	}
}

// by-id 跨租户读/改/删 = NotFound(tenant 域在查询层)。
// 判别:三条 by-id 查询去掉 AND tenant_id=$ → 他租能命中 → 非 NotFound → 红。
func TestBindingsAdmin_CrossTenantByIDScoped(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newFixture(t, ctx, pool)
	mid := f.seedModel(modelOpts{canonicalID: "mb-xt-" + f.suffix, providerModelID: "pm"})

	r := NewPostgresRegistry(pool, nil)
	got, err := r.CreatePoolBinding(ctx, baseCreate(f, mid))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := r.GetPoolBindingByID(ctx, got.ID, f.otherTenantID); !errors.Is(err, ErrBindingNotFound) {
		t.Errorf("Get 跨租户 err=%v want ErrBindingNotFound", err)
	}
	if _, err := r.UpdatePoolBinding(ctx, UpdateBindingInput{ID: got.ID, TenantID: f.otherTenantID, Priority: 5, Weight: 1, SelectionMode: "strict_priority", FallbackClass: "normal", Enabled: true}); !errors.Is(err, ErrBindingNotFound) {
		t.Errorf("Update 跨租户 err=%v want ErrBindingNotFound", err)
	}
	if err := r.DeletePoolBinding(ctx, got.ID, f.otherTenantID, "x", ""); !errors.Is(err, ErrBindingNotFound) {
		t.Errorf("Delete 跨租户 err=%v want ErrBindingNotFound", err)
	}
	// 正控制:原租户能读到(证明不是恒 NotFound)。
	if _, err := r.GetPoolBindingByID(ctx, got.ID, f.tenantID); err != nil {
		t.Errorf("原租户 Get err=%v want nil", err)
	}
}

// 重复三元组 = ErrBindingConflict。
func TestBindingsAdmin_UniqueConflict(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newFixture(t, ctx, pool)
	mid := f.seedModel(modelOpts{canonicalID: "mb-uniq-" + f.suffix, providerModelID: "pm"})

	r := NewPostgresRegistry(pool, nil)
	if _, err := r.CreatePoolBinding(ctx, baseCreate(f, mid)); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := r.CreatePoolBinding(ctx, baseCreate(f, mid)); !errors.Is(err, ErrBindingConflict) {
		t.Fatalf("dup err=%v want ErrBindingConflict", err)
	}
}

// 绑他租 pool = ErrPoolGroupNotFound(归属预检给 422 而非裸 FK 500)。
func TestBindingsAdmin_PoolGroupNotOwned(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newFixture(t, ctx, pool)
	mid := f.seedModel(modelOpts{canonicalID: "mb-pg-" + f.suffix, providerModelID: "pm"})
	var otherPool int64
	if err := pool.QueryRow(ctx, `INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		f.otherTenantID, "other-pg-"+f.suffix).Scan(&otherPool); err != nil {
		t.Fatalf("seed other pool: %v", err)
	}
	in := baseCreate(f, mid)
	in.PoolGroupID = otherPool
	r := NewPostgresRegistry(pool, nil)
	if _, err := r.CreatePoolBinding(ctx, in); !errors.Is(err, ErrPoolGroupNotFound) {
		t.Fatalf("err=%v want ErrPoolGroupNotFound", err)
	}
}

// admin list 含 disabled 绑定(区别于路由读的 enabled 过滤)。
// 判别:ListPoolBindingsAdmin 改用路由查询(带 enabled=true)→ disabled 不返回 → 红。
func TestBindingsAdmin_ListIncludesDisabled(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newFixture(t, ctx, pool)
	mid := f.seedModel(modelOpts{canonicalID: "mb-list-" + f.suffix, providerModelID: "pm"})

	r := NewPostgresRegistry(pool, nil)
	in := baseCreate(f, mid)
	in.Enabled = false
	in.DisabledReason = strptr("paused for test")
	got, err := r.CreatePoolBinding(ctx, in)
	if err != nil {
		t.Fatalf("create disabled: %v", err)
	}
	items, err := r.ListPoolBindingsAdmin(ctx, f.tenantID, nil, nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, b := range items {
		if b.ID == got.ID && !b.Enabled {
			found = true
		}
	}
	if !found {
		t.Fatalf("admin list 应含 disabled 绑定 #%d,实际 %d 条均未命中", got.ID, len(items))
	}
}

func strptr(s string) *string { return &s }
