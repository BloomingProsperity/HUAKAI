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
	"fmt"
	"strings"
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
		Actor: "admin_token:test", ActorRole: "platform_admin", RequestID: "binding-integration", Reason: "integration",
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
	if err := r.DeletePoolBinding(ctx, DeleteBindingInput{
		ID: got.ID, TenantID: f.tenantID, Actor: "admin_token:test",
		ActorRole: "platform_admin", RequestID: "binding-delete",
	}); err != nil {
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
	if _, err := r.UpdatePoolBinding(ctx, UpdateBindingInput{
		ID:       got.ID,
		TenantID: f.otherTenantID,
		Priority: BindingField[int32]{Set: true, Value: 5},
		Actor:    "admin_token:test", ActorRole: "platform_admin", RequestID: "binding-cross-update",
	}); !errors.Is(err, ErrBindingNotFound) {
		t.Errorf("Update 跨租户 err=%v want ErrBindingNotFound", err)
	}
	if err := r.DeletePoolBinding(ctx, DeleteBindingInput{
		ID: got.ID, TenantID: f.otherTenantID, Actor: "admin_token:test",
		ActorRole: "platform_admin", RequestID: "binding-cross-delete",
	}); !errors.Is(err, ErrBindingNotFound) {
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

// 日志写入失败时，绑定行与快照版本必须同时回滚。该用例覆盖新增、更新和删除三个
// 独立事务；任一路把日志挪到提交之后，精确行状态或版本断言都会转红。
func TestBindingsAdmin_LogFailureRollsBackBindingAndSnapshot(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newFixture(t, ctx, pool)
	modelID := f.seedModel(modelOpts{canonicalID: "mb-log-" + f.suffix, providerModelID: "pm"})
	f.setSnapshot(7)
	registry := NewPostgresRegistry(pool, nil)
	baseline, err := registry.CreatePoolBinding(ctx, baseCreate(f, modelID))
	if err != nil {
		t.Fatalf("create baseline binding: %v", err)
	}

	suffix := strings.ReplaceAll(f.suffix, "-", "")
	functionName := "reject_binding_log_" + suffix
	triggerName := "reject_binding_log_trigger_" + suffix
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
CREATE FUNCTION %s() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'binding log rejected for atomicity test';
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER %s BEFORE INSERT ON admin_audit_events
FOR EACH ROW EXECUTE FUNCTION %s()`, functionName, triggerName, functionName)); err != nil {
		t.Fatalf("install reject trigger: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON admin_audit_events`, triggerName))
		_, _ = pool.Exec(c, fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, functionName))
	})

	versionBefore := readSnapVer(t, ctx, pool, f.tenantID)
	create := baseCreate(f, modelID)
	create.PoolGroupID = f.seedPoolGroup("log-create")
	if _, err := registry.CreatePoolBinding(ctx, create); err == nil {
		t.Fatal("日志失败时新增绑定必须失败")
	}
	var count int64
	if err := pool.QueryRow(ctx, `
SELECT count(*) FROM model_pool_bindings
WHERE tenant_id=$1 AND model_id=$2 AND pool_group_id=$3 AND deleted_at IS NULL`,
		f.tenantID, modelID, create.PoolGroupID,
	).Scan(&count); err != nil || count != 0 {
		t.Fatalf("新增日志失败留下绑定 count=%d err=%v", count, err)
	}
	if got := readSnapVer(t, ctx, pool, f.tenantID); got != versionBefore {
		t.Fatalf("新增日志失败仍推进快照 version=%d want %d", got, versionBefore)
	}

	if _, err := registry.UpdatePoolBinding(ctx, UpdateBindingInput{
		ID: baseline.ID, TenantID: f.tenantID,
		Enabled: BindingField[bool]{Set: true, Value: false},
		Actor:   "admin_token:test", ActorRole: "platform_admin",
		RequestID: "binding-log-rejected-update",
	}); err == nil {
		t.Fatal("日志失败时更新绑定必须失败")
	}
	got, err := registry.GetPoolBindingByID(ctx, baseline.ID, f.tenantID)
	if err != nil || !got.Enabled {
		t.Fatalf("更新日志失败留下半状态 enabled=%v err=%v", got.Enabled, err)
	}
	if current := readSnapVer(t, ctx, pool, f.tenantID); current != versionBefore {
		t.Fatalf("更新日志失败仍推进快照 version=%d want %d", current, versionBefore)
	}

	if err := registry.DeletePoolBinding(ctx, DeleteBindingInput{
		ID: baseline.ID, TenantID: f.tenantID,
		Actor: "admin_token:test", ActorRole: "platform_admin",
		RequestID: "binding-log-rejected-delete",
	}); err == nil {
		t.Fatal("日志失败时删除绑定必须失败")
	}
	if _, err := registry.GetPoolBindingByID(ctx, baseline.ID, f.tenantID); err != nil {
		t.Fatalf("删除日志失败后绑定应仍存在: %v", err)
	}
	if current := readSnapVer(t, ctx, pool, f.tenantID); current != versionBefore {
		t.Fatalf("删除日志失败仍推进快照 version=%d want %d", current, versionBefore)
	}
}

func strptr(s string) *string { return &s }
