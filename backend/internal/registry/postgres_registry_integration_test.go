//go:build integration_pg

// PostgresRegistry 针对真实 PostgreSQL 的集成测试，覆盖 14 个原始用例：
//
//   1.  HappyPath
//   2.  UnknownAlias
//   3.  DisabledAlias
//   4.  DisabledModel
//   5.  TenantDisabledBlocksGlobal  (D3 显式拒绝不变量)
//   6.  InheritGlobalActive
//   7.  InheritOff
//   8.  NoBindings
//   9.  MultipleBindingsOrdered
//   10. CaseInsensitive
//   11. SoftDeletedAliasInvisible
//   12. EffectiveTimeWindow
//   13. ProviderModelOverrideOnPrimary
//   14. SnapshotVersionStamp
//   15. ListModelsDiscoverySurface

package registry

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

// -----------------------------------------------------------------------------
// fixture 辅助函数
// -----------------------------------------------------------------------------

func openIntegrationPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration test")
	}
	p, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

type registryFixture struct {
	t        *testing.T
	ctx      context.Context
	pool     *pgxpool.Pool
	suffix   string
	tenantID int64
	// 租户拥有的 pool group;复用为 binding 目标。
	poolGroupID int64
	// 辅助租户(若有跨租户探测断言则会用到)。
	otherTenantID int64
	// 由辅助函数创建的 ID,批量记录以便清理。
	modelIDs          []int64
	aliasIDs          []int64
	bindingIDs        []int64
	capIDs            []int64
	createdGlobalRows bool
}

func newFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) *registryFixture {
	t.Helper()
	f := &registryFixture{
		t:      t,
		ctx:    ctx,
		pool:   pool,
		suffix: uuid.NewString(),
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"reg-tenant-"+f.suffix,
	).Scan(&f.tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"reg-other-"+f.suffix,
	).Scan(&f.otherTenantID); err != nil {
		t.Fatalf("seed other tenant: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		f.tenantID, "default-"+f.suffix,
	).Scan(&f.poolGroupID); err != nil {
		t.Fatalf("seed pool_group: %v", err)
	}
	t.Cleanup(f.cleanup)
	return f
}

func (f *registryFixture) cleanup() {
	c := context.Background()
	// 租户作用域的行:binding 始终是租户作用域。
	_, _ = f.pool.Exec(c, `DELETE FROM model_pool_bindings WHERE tenant_id = $1`, f.tenantID)
	// Capabilities/aliases/models:租户作用域 + 由演练继承的测试主动播种的
	// scope='global' 行(test/)。
	_, _ = f.pool.Exec(c, `DELETE FROM model_registry_capabilities WHERE tenant_id = $1`, f.tenantID)
	_, _ = f.pool.Exec(c, `DELETE FROM model_aliases WHERE tenant_id = $1`, f.tenantID)
	_, _ = f.pool.Exec(c, `DELETE FROM models WHERE tenant_id = $1`, f.tenantID)
	if f.createdGlobalRows {
		// 继承测试播种的 scope='global' 行带有包含 f.suffix 的 public_alias
		// 值;沿外键链清理这些行。
		_, _ = f.pool.Exec(c, `DELETE FROM model_registry_capabilities WHERE scope = 'global' AND model_id IN (SELECT id FROM models WHERE scope = 'global' AND canonical_id LIKE '%' || $1 || '%')`, f.suffix)
		_, _ = f.pool.Exec(c, `DELETE FROM model_aliases WHERE scope = 'global' AND public_alias_normalized LIKE 'claude-%' AND model_id IN (SELECT id FROM models WHERE scope = 'global' AND canonical_id LIKE '%' || $1 || '%')`, f.suffix)
		_, _ = f.pool.Exec(c, `DELETE FROM models WHERE scope = 'global' AND canonical_id LIKE '%' || $1 || '%'`, f.suffix)
	}
	_, _ = f.pool.Exec(c, `DELETE FROM model_registry_snapshots WHERE tenant_id = $1`, f.tenantID)
	_, _ = f.pool.Exec(c, `DELETE FROM model_registry_tenant_policies WHERE tenant_id = $1`, f.tenantID)
	_, _ = f.pool.Exec(c, `DELETE FROM channels WHERE tenant_id IN ($1, $2)`, f.tenantID, f.otherTenantID)
	_, _ = f.pool.Exec(c, `DELETE FROM pool_groups WHERE tenant_id IN ($1, $2)`, f.tenantID, f.otherTenantID)
	_, _ = f.pool.Exec(c, `DELETE FROM tenants WHERE id IN ($1, $2)`, f.tenantID, f.otherTenantID)
}

type modelOpts struct {
	scope            string // "tenant" 或 "global";默认 "tenant"
	canonicalID      string
	providerModelID  string
	protocolFamily   string
	contextWindow    int
	pricingClass     string
	status           string
	requestTimeoutMS int
}

func (f *registryFixture) seedModel(o modelOpts) int64 {
	f.t.Helper()
	if o.scope == "" {
		o.scope = "tenant"
	}
	if o.canonicalID == "" {
		o.canonicalID = "anthropic/claude-test-" + f.suffix
	}
	if o.providerModelID == "" {
		o.providerModelID = "claude-test-" + f.suffix
	}
	if o.protocolFamily == "" {
		o.protocolFamily = "anthropic_messages"
	}
	if o.contextWindow == 0 {
		o.contextWindow = 200000
	}
	if o.pricingClass == "" {
		o.pricingClass = "standard"
	}
	if o.status == "" {
		o.status = "active"
	}
	if o.requestTimeoutMS == 0 {
		o.requestTimeoutMS = 60000
	}
	var tenantArg interface{}
	if o.scope == "global" {
		tenantArg = nil
		f.createdGlobalRows = true
	} else {
		tenantArg = f.tenantID
	}
	var id int64
	if err := f.pool.QueryRow(f.ctx,
		`INSERT INTO models (tenant_id, scope, canonical_id, protocol_family,
		                     default_provider_model_id, default_context_window,
		                     default_request_timeout_ms, pricing_class, status)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
		tenantArg, o.scope, o.canonicalID, o.protocolFamily,
		o.providerModelID, o.contextWindow, o.requestTimeoutMS, o.pricingClass, o.status,
	).Scan(&id); err != nil {
		f.t.Fatalf("seed model: %v", err)
	}
	f.modelIDs = append(f.modelIDs, id)
	return id
}

type aliasOpts struct {
	scope                 string // 默认 "tenant"
	modelID               int64
	publicAliasNormalized string
	publicAliasDisplay    string
	status                string
	deletedAt             *time.Time
}

func (f *registryFixture) seedAlias(o aliasOpts) int64 {
	f.t.Helper()
	if o.scope == "" {
		o.scope = "tenant"
	}
	if o.publicAliasDisplay == "" {
		o.publicAliasDisplay = o.publicAliasNormalized
	}
	if o.status == "" {
		o.status = "active"
	}
	var tenantArg interface{}
	if o.scope == "global" {
		tenantArg = nil
		f.createdGlobalRows = true
	} else {
		tenantArg = f.tenantID
	}
	var id int64
	if err := f.pool.QueryRow(f.ctx,
		`INSERT INTO model_aliases
		   (tenant_id, scope, model_id, public_alias_normalized, public_alias_display, status, deleted_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		tenantArg, o.scope, o.modelID, o.publicAliasNormalized, o.publicAliasDisplay, o.status, o.deletedAt,
	).Scan(&id); err != nil {
		f.t.Fatalf("seed alias: %v", err)
	}
	f.aliasIDs = append(f.aliasIDs, id)
	return id
}

type bindingOpts struct {
	// binding 始终是租户作用域。
	// modelID 可以指向 global 作用域的 model —— 这就是继承路径:
	// 租户 T 为一个全局共享的 model 建立自己的 binding。
	modelID               int64
	poolGroupID           int64
	priority              int32
	weight                int32
	selectionMode         string
	providerModelOverride *string
	rpmLimit              *int32
	tpmLimit              *int32
	maxParallel           *int32
	fallbackClass         string
	enabled               *bool
	effectiveFrom         *time.Time
	effectiveUntil        *time.Time
	reason                string
}

func (f *registryFixture) seedBinding(o bindingOpts) int64 {
	f.t.Helper()
	if o.weight == 0 {
		o.weight = 1
	}
	if o.selectionMode == "" {
		o.selectionMode = "strict_priority"
	}
	if o.fallbackClass == "" {
		o.fallbackClass = "normal"
	}
	if o.reason == "" {
		o.reason = "primary"
	}
	enabled := true
	if o.enabled != nil {
		enabled = *o.enabled
	}
	var id int64
	if err := f.pool.QueryRow(f.ctx,
		`INSERT INTO model_pool_bindings
		   (tenant_id, model_id, pool_group_id, priority, weight, selection_mode,
		    provider_model_id_override, rpm_limit, tpm_limit, max_parallel_requests,
		    fallback_class, enabled, effective_from, effective_until, reason)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15) RETURNING id`,
		f.tenantID, o.modelID, o.poolGroupID, o.priority, o.weight, o.selectionMode,
		o.providerModelOverride, o.rpmLimit, o.tpmLimit, o.maxParallel,
		o.fallbackClass, enabled, o.effectiveFrom, o.effectiveUntil, o.reason,
	).Scan(&id); err != nil {
		f.t.Fatalf("seed binding: %v", err)
	}
	f.bindingIDs = append(f.bindingIDs, id)
	return id
}

func (f *registryFixture) seedCapability(modelID int64, capability string) int64 {
	f.t.Helper()
	var id int64
	if err := f.pool.QueryRow(f.ctx,
		`INSERT INTO model_registry_capabilities (tenant_id, scope, model_id, capability)
		 VALUES ($1, 'tenant', $2, $3) RETURNING id`,
		f.tenantID, modelID, capability,
	).Scan(&id); err != nil {
		f.t.Fatalf("seed capability: %v", err)
	}
	f.capIDs = append(f.capIDs, id)
	return id
}

func (f *registryFixture) setInheritPolicy(inherit bool) {
	f.t.Helper()
	if _, err := f.pool.Exec(f.ctx,
		`INSERT INTO model_registry_tenant_policies (tenant_id, inherit_global_catalog)
		 VALUES ($1, $2)
		 ON CONFLICT (tenant_id) DO UPDATE SET inherit_global_catalog = EXCLUDED.inherit_global_catalog`,
		f.tenantID, inherit,
	); err != nil {
		f.t.Fatalf("set policy: %v", err)
	}
}

func (f *registryFixture) setSnapshot(version int64) {
	f.t.Helper()
	if _, err := f.pool.Exec(f.ctx,
		`INSERT INTO model_registry_snapshots (tenant_id, version)
		 VALUES ($1, $2)
		 ON CONFLICT (tenant_id) DO UPDATE SET version = EXCLUDED.version`,
		f.tenantID, version,
	); err != nil {
		f.t.Fatalf("set snapshot: %v", err)
	}
}

// -----------------------------------------------------------------------------
// 测试 1 — HappyPath
// -----------------------------------------------------------------------------

func TestPostgresRegistry_HappyPath(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newFixture(t, ctx, pool)

	// 根据 2026-04-30T10:08Z 已核实的 WebFetch 得到的当前 Anthropic 顶级 model
	// @ https://platform.claude.com/docs/en/docs/about-claude/models/overview。
	mid := f.seedModel(modelOpts{
		canonicalID:     "anthropic/claude-opus-4-7-" + f.suffix,
		providerModelID: "claude-opus-4-7",
	})
	f.seedAlias(aliasOpts{
		modelID:               mid,
		publicAliasNormalized: "claude-opus-4-7",
		publicAliasDisplay:    "Claude-Opus-4-7",
	})
	f.seedBinding(bindingOpts{modelID: mid, poolGroupID: f.poolGroupID, priority: 100})
	f.seedCapability(mid, "stream")
	f.seedCapability(mid, "tools")

	r := NewPostgresRegistry(pool, nil)
	got, err := r.ResolveModel(ctx, "claude-opus-4-7", f.tenantID)
	if err != nil {
		t.Fatalf("ResolveModel: %v", err)
	}
	if got.PublicAlias != "Claude-Opus-4-7" {
		t.Errorf("PublicAlias = %q; want display casing", got.PublicAlias)
	}
	if got.ProviderModelID != "claude-opus-4-7" {
		t.Errorf("ProviderModelID = %q", got.ProviderModelID)
	}
	if got.ProtocolFamily != "anthropic_messages" {
		t.Errorf("ProtocolFamily = %q", got.ProtocolFamily)
	}
	if len(got.PoolCandidates) != 1 || got.PoolCandidates[0] != f.poolGroupID {
		t.Errorf("PoolCandidates = %v; want [%d]", got.PoolCandidates, f.poolGroupID)
	}
	if len(got.Capabilities) != 2 {
		t.Errorf("Capabilities = %v; want 2 entries", got.Capabilities)
	}
	if got.SnapshotVersion == "" {
		t.Errorf("SnapshotVersion empty")
	}
}

// -----------------------------------------------------------------------------
// 测试 2 — UnknownAlias
// -----------------------------------------------------------------------------

func TestPostgresRegistry_UnknownAlias(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newFixture(t, ctx, pool)

	r := NewPostgresRegistry(pool, nil)
	_, err := r.ResolveModel(ctx, "no-such-model", f.tenantID)
	if !errors.Is(err, ErrUnknownModel) {
		t.Fatalf("err = %v; want ErrUnknownModel", err)
	}
}

// -----------------------------------------------------------------------------
// 测试 3 — DisabledAlias
// -----------------------------------------------------------------------------

func TestPostgresRegistry_DisabledAlias(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newFixture(t, ctx, pool)

	mid := f.seedModel(modelOpts{})
	f.seedAlias(aliasOpts{
		modelID:               mid,
		publicAliasNormalized: "alpha",
		status:                "disabled",
	})
	f.seedBinding(bindingOpts{modelID: mid, poolGroupID: f.poolGroupID, priority: 100})

	r := NewPostgresRegistry(pool, nil)
	_, err := r.ResolveModel(ctx, "alpha", f.tenantID)
	if !errors.Is(err, ErrModelDisabled) {
		t.Fatalf("err = %v; want ErrModelDisabled", err)
	}
}

// -----------------------------------------------------------------------------
// 测试 4 — DisabledModel(alias 启用,model 禁用)
// -----------------------------------------------------------------------------

func TestPostgresRegistry_DisabledModel(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newFixture(t, ctx, pool)

	mid := f.seedModel(modelOpts{status: "disabled"})
	f.seedAlias(aliasOpts{modelID: mid, publicAliasNormalized: "beta"})
	f.seedBinding(bindingOpts{modelID: mid, poolGroupID: f.poolGroupID, priority: 100})

	r := NewPostgresRegistry(pool, nil)
	_, err := r.ResolveModel(ctx, "beta", f.tenantID)
	if !errors.Is(err, ErrModelDisabled) {
		t.Fatalf("err = %v; want ErrModelDisabled", err)
	}
}

// -----------------------------------------------------------------------------
// 测试 5 — TenantDisabledBlocksGlobal(D3 显式拒绝不变量)
// 租户对 "claude-x" 有 DISABLED 的 alias。
// 全局对 "claude-x" 有 ACTIVE 的 alias。
// inherit_global_catalog = TRUE。
// 必须返回 ErrModelDisabled,绝不能回落到全局。
// -----------------------------------------------------------------------------

func TestPostgresRegistry_TenantDisabledBlocksGlobal(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newFixture(t, ctx, pool)

	tenantMid := f.seedModel(modelOpts{
		canonicalID: "tenant/x-" + f.suffix,
	})
	f.seedAlias(aliasOpts{
		modelID:               tenantMid,
		publicAliasNormalized: "claude-x",
		status:                "disabled",
	})

	globalMid := f.seedModel(modelOpts{
		scope:       "global",
		canonicalID: "global/x-" + f.suffix,
	})
	f.seedAlias(aliasOpts{
		scope:                 "global",
		modelID:               globalMid,
		publicAliasNormalized: "claude-x",
	})
	// 指向全局 model 的租户作用域 binding —— 故意存在,这样【如果】
	// 显式拒绝不变量被破坏,回落就会真的成功(让断言更锐利)。
	// binding 是租户作用域。
	f.seedBinding(bindingOpts{
		modelID: globalMid, poolGroupID: f.poolGroupID, priority: 100,
	})
	f.setInheritPolicy(true)

	r := NewPostgresRegistry(pool, nil)
	_, err := r.ResolveModel(ctx, "claude-x", f.tenantID)
	if !errors.Is(err, ErrModelDisabled) {
		t.Fatalf("err = %v; tenant-disabled MUST block global fallback (D3 explicit-deny)", err)
	}
}

// -----------------------------------------------------------------------------
// 测试 6 — InheritGlobalActive
// 租户没有任何行。
// 全局有 ACTIVE 的行。
// inherit = TRUE。
// 必须通过全局路径解析成功。
// -----------------------------------------------------------------------------

func TestPostgresRegistry_InheritGlobalActive(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newFixture(t, ctx, pool)

	gmid := f.seedModel(modelOpts{scope: "global", canonicalID: "global/y-" + f.suffix})
	f.seedAlias(aliasOpts{
		scope:                 "global",
		modelID:               gmid,
		publicAliasNormalized: "claude-y",
	})
	// 指向全局 model 的租户作用域 binding —— 这就是继承的形态:
	// 全局目录在 model/alias 层共享,但每个租户声明自己的路由目标。
	f.seedBinding(bindingOpts{
		modelID: gmid, poolGroupID: f.poolGroupID, priority: 100,
	})
	f.setInheritPolicy(true)

	r := NewPostgresRegistry(pool, nil)
	got, err := r.ResolveModel(ctx, "claude-y", f.tenantID)
	if err != nil {
		t.Fatalf("ResolveModel: %v", err)
	}
	if len(got.PoolCandidates) != 1 || got.PoolCandidates[0] != f.poolGroupID {
		t.Errorf("PoolCandidates = %v; want [%d]", got.PoolCandidates, f.poolGroupID)
	}
}

// -----------------------------------------------------------------------------
// 测试 7 — InheritOff
// 没有租户行,也没有 policy 行(或 inherit=false),即使全局有该 alias,
// 也必须返回 ErrUnknownModel。
// -----------------------------------------------------------------------------

func TestPostgresRegistry_InheritOff(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newFixture(t, ctx, pool)

	gmid := f.seedModel(modelOpts{scope: "global", canonicalID: "global/z-" + f.suffix})
	f.seedAlias(aliasOpts{
		scope:                 "global",
		modelID:               gmid,
		publicAliasNormalized: "claude-z",
	})
	// 播种租户 binding 让断言更锐利:即使路由已就绪,
	// no-inherit 也必须在 alias 查找处短路。
	f.seedBinding(bindingOpts{
		modelID: gmid, poolGroupID: f.poolGroupID, priority: 100,
	})
	// 不调用 setInheritPolicy → policy 行缺失 → resolver 视为 false。

	r := NewPostgresRegistry(pool, nil)
	_, err := r.ResolveModel(ctx, "claude-z", f.tenantID)
	if !errors.Is(err, ErrUnknownModel) {
		t.Fatalf("err = %v; want ErrUnknownModel", err)
	}
}

// -----------------------------------------------------------------------------
// 测试 8 — NoBindings
// -----------------------------------------------------------------------------

func TestPostgresRegistry_NoBindings(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newFixture(t, ctx, pool)

	mid := f.seedModel(modelOpts{})
	f.seedAlias(aliasOpts{modelID: mid, publicAliasNormalized: "naked"})
	// 不播种任何 binding。

	r := NewPostgresRegistry(pool, nil)
	_, err := r.ResolveModel(ctx, "naked", f.tenantID)
	if !errors.Is(err, ErrTenantNoAccess) {
		t.Fatalf("err = %v; want ErrTenantNoAccess", err)
	}
}

// -----------------------------------------------------------------------------
// 测试 9 — MultipleBindingsOrdered
// 三个 binding,优先级分别为 200 / 50 / 100。
// PoolCandidates 应为 [50, 100, 200](优先级升序,再按 id 升序)。
// -----------------------------------------------------------------------------

func TestPostgresRegistry_MultipleBindingsOrdered(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newFixture(t, ctx, pool)

	// 需要同一租户拥有的三个 pool group。
	var pg2, pg3 int64
	if err := pool.QueryRow(ctx, `INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		f.tenantID, "pg2-"+f.suffix).Scan(&pg2); err != nil {
		t.Fatalf("pg2: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		f.tenantID, "pg3-"+f.suffix).Scan(&pg3); err != nil {
		t.Fatalf("pg3: %v", err)
	}

	mid := f.seedModel(modelOpts{})
	f.seedAlias(aliasOpts{modelID: mid, publicAliasNormalized: "ordered"})
	f.seedBinding(bindingOpts{modelID: mid, poolGroupID: f.poolGroupID, priority: 200})
	f.seedBinding(bindingOpts{modelID: mid, poolGroupID: pg2, priority: 50})
	f.seedBinding(bindingOpts{modelID: mid, poolGroupID: pg3, priority: 100})

	r := NewPostgresRegistry(pool, nil)
	got, err := r.ResolveModel(ctx, "ordered", f.tenantID)
	if err != nil {
		t.Fatalf("ResolveModel: %v", err)
	}
	want := []int64{pg2, pg3, f.poolGroupID}
	if len(got.PoolCandidates) != 3 {
		t.Fatalf("PoolCandidates len = %d; want 3", len(got.PoolCandidates))
	}
	for i := range want {
		if got.PoolCandidates[i] != want[i] {
			t.Errorf("PoolCandidates[%d] = %d; want %d", i, got.PoolCandidates[i], want[i])
		}
	}
}

func TestPostgresRegistry_SkipsDisabledOrDeletedPoolGroups(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newFixture(t, ctx, pool)

	var disabledPG, deletedPG, activePG int64
	if err := pool.QueryRow(ctx, `INSERT INTO pool_groups (tenant_id, name, enabled) VALUES ($1, $2, false) RETURNING id`,
		f.tenantID, "pg-disabled-"+f.suffix).Scan(&disabledPG); err != nil {
		t.Fatalf("disabled pool group: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO pool_groups (tenant_id, name, deleted_at) VALUES ($1, $2, NOW()) RETURNING id`,
		f.tenantID, "pg-deleted-"+f.suffix).Scan(&deletedPG); err != nil {
		t.Fatalf("deleted pool group: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		f.tenantID, "pg-active-"+f.suffix).Scan(&activePG); err != nil {
		t.Fatalf("active pool group: %v", err)
	}

	mid := f.seedModel(modelOpts{})
	f.seedAlias(aliasOpts{modelID: mid, publicAliasNormalized: "pool-lifecycle"})
	f.seedBinding(bindingOpts{modelID: mid, poolGroupID: disabledPG, priority: 10})
	f.seedBinding(bindingOpts{modelID: mid, poolGroupID: deletedPG, priority: 20})
	f.seedBinding(bindingOpts{modelID: mid, poolGroupID: activePG, priority: 30})

	r := NewPostgresRegistry(pool, nil)
	got, err := r.ResolveModel(ctx, "pool-lifecycle", f.tenantID)
	if err != nil {
		t.Fatalf("ResolveModel: %v", err)
	}
	if len(got.PoolCandidates) != 1 || got.PoolCandidates[0] != activePG {
		t.Fatalf("PoolCandidates=%v; want only active pool group %d", got.PoolCandidates, activePG)
	}
}

// -----------------------------------------------------------------------------
// 测试 10 — CaseInsensitive
// alias 以 "Claude-3-5-Sonnet"(display)/ 小写 normalized 播种。
// 用 "CLAUDE-3-5-SONNET" 解析必须能匹配。
// -----------------------------------------------------------------------------

func TestPostgresRegistry_CaseInsensitive(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newFixture(t, ctx, pool)

	mid := f.seedModel(modelOpts{})
	f.seedAlias(aliasOpts{
		modelID:               mid,
		publicAliasNormalized: "claude-3-5-sonnet",
		publicAliasDisplay:    "Claude-3-5-Sonnet",
	})
	f.seedBinding(bindingOpts{modelID: mid, poolGroupID: f.poolGroupID, priority: 100})

	r := NewPostgresRegistry(pool, nil)
	got, err := r.ResolveModel(ctx, "  CLAUDE-3-5-SONNET  ", f.tenantID)
	if err != nil {
		t.Fatalf("ResolveModel: %v", err)
	}
	if got.PublicAlias != "Claude-3-5-Sonnet" {
		t.Errorf("PublicAlias = %q; want display casing preserved", got.PublicAlias)
	}
}

// -----------------------------------------------------------------------------
// 测试 11 — SoftDeletedAliasInvisible
// -----------------------------------------------------------------------------

func TestPostgresRegistry_SoftDeletedAliasInvisible(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newFixture(t, ctx, pool)

	now := time.Now().UTC()
	mid := f.seedModel(modelOpts{})
	f.seedAlias(aliasOpts{
		modelID:               mid,
		publicAliasNormalized: "ghost",
		deletedAt:             &now,
	})
	f.seedBinding(bindingOpts{modelID: mid, poolGroupID: f.poolGroupID, priority: 100})

	r := NewPostgresRegistry(pool, nil)
	_, err := r.ResolveModel(ctx, "ghost", f.tenantID)
	if !errors.Is(err, ErrUnknownModel) {
		t.Fatalf("err = %v; want ErrUnknownModel (soft-deleted alias must be invisible)", err)
	}
}

// -----------------------------------------------------------------------------
// 测试 12 — EffectiveTimeWindow
// binding 的 effective_until 在过去 → 被排除 → ErrTenantNoAccess。
// -----------------------------------------------------------------------------

func TestPostgresRegistry_EffectiveTimeWindow(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newFixture(t, ctx, pool)

	mid := f.seedModel(modelOpts{})
	f.seedAlias(aliasOpts{modelID: mid, publicAliasNormalized: "expired"})
	past := time.Now().UTC().Add(-time.Hour)
	f.seedBinding(bindingOpts{
		modelID: mid, poolGroupID: f.poolGroupID, priority: 100,
		effectiveUntil: &past,
	})

	r := NewPostgresRegistry(pool, nil)
	_, err := r.ResolveModel(ctx, "expired", f.tenantID)
	if !errors.Is(err, ErrTenantNoAccess) {
		t.Fatalf("err = %v; want ErrTenantNoAccess (binding effective_until in past)", err)
	}
}

// -----------------------------------------------------------------------------
// 测试 13 — ProviderModelOverrideOnPrimary
// 主 binding 带有 provider_model_id_override → ResolvedModel.ProviderModelID
// 反映该 override,而非 canonical model 的默认值。
// 该用例验证 HUAKAI 自身的主绑定模型覆盖合同。
// -----------------------------------------------------------------------------

func TestPostgresRegistry_ProviderModelOverrideOnPrimary(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newFixture(t, ctx, pool)

	mid := f.seedModel(modelOpts{
		providerModelID: "claude-3-5-sonnet-20241022",
	})
	f.seedAlias(aliasOpts{modelID: mid, publicAliasNormalized: "remap"})
	override := "claude-3-5-sonnet-latest"
	f.seedBinding(bindingOpts{
		modelID: mid, poolGroupID: f.poolGroupID, priority: 100,
		providerModelOverride: &override,
	})

	r := NewPostgresRegistry(pool, nil)
	got, err := r.ResolveModel(ctx, "remap", f.tenantID)
	if err != nil {
		t.Fatalf("ResolveModel: %v", err)
	}
	if got.ProviderModelID != "claude-3-5-sonnet-latest" {
		t.Errorf("ProviderModelID = %q; want %q (binding override on primary)", got.ProviderModelID, "claude-3-5-sonnet-latest")
	}
	if len(got.BindingMetadata) != 1 {
		t.Fatalf("BindingMetadata len = %d; want 1", len(got.BindingMetadata))
	}
	if got.BindingMetadata[0].ProviderModelIDOverride == nil ||
		*got.BindingMetadata[0].ProviderModelIDOverride != "claude-3-5-sonnet-latest" {
		t.Errorf("BindingMetadata override not preserved")
	}
}

func TestPostgresRegistry_ChannelBodyParamGateMetadata(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newFixture(t, ctx, pool)

	mid := f.seedModel(modelOpts{})
	f.seedAlias(aliasOpts{modelID: mid, publicAliasNormalized: "body-gate"})
	f.seedBinding(bindingOpts{modelID: mid, poolGroupID: f.poolGroupID, priority: 100})
	if _, err := pool.Exec(ctx,
		`INSERT INTO channels (tenant_id, pool_group_id, name, body_param_strips, param_override)
		 VALUES ($1, $2, $3, ARRAY['service_tier','stream_options.include_obfuscation']::text[], '{"temperature":0}'::jsonb)`,
		f.tenantID, f.poolGroupID, "body-gate-"+f.suffix,
	); err != nil {
		t.Fatalf("seed channel body-param gate: %v", err)
	}

	r := NewPostgresRegistry(pool, nil)
	got, err := r.ResolveModel(ctx, "body-gate", f.tenantID)
	if err != nil {
		t.Fatalf("ResolveModel: %v", err)
	}
	if len(got.BindingMetadata) != 1 {
		t.Fatalf("BindingMetadata len=%d want 1", len(got.BindingMetadata))
	}
	binding := got.BindingMetadata[0]
	wantStrips := []string{"service_tier", "stream_options.include_obfuscation"}
	if !reflect.DeepEqual(binding.BodyParamStrips, wantStrips) {
		t.Fatalf("BodyParamStrips=%v want %v", binding.BodyParamStrips, wantStrips)
	}
	if string(binding.ParamOverride["temperature"]) != "0" {
		asJSON, _ := json.Marshal(binding.ParamOverride)
		t.Fatalf("ParamOverride=%s want temperature=0", asJSON)
	}
}

func TestPostgresRegistry_ChannelSensitiveWordsMetadata(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newFixture(t, ctx, pool)

	mid := f.seedModel(modelOpts{})
	f.seedAlias(aliasOpts{modelID: mid, publicAliasNormalized: "cloak-gate"})
	f.seedBinding(bindingOpts{modelID: mid, poolGroupID: f.poolGroupID, priority: 100})
	if _, err := pool.Exec(ctx,
		`INSERT INTO channels (tenant_id, pool_group_id, name, sensitive_words)
		 VALUES ($1, $2, $3, ARRAY['secret','banned']::text[])`,
		f.tenantID, f.poolGroupID, "cloak-gate-"+f.suffix,
	); err != nil {
		t.Fatalf("seed channel sensitive words: %v", err)
	}

	r := NewPostgresRegistry(pool, nil)
	got, err := r.ResolveModel(ctx, "cloak-gate", f.tenantID)
	if err != nil {
		t.Fatalf("ResolveModel: %v", err)
	}
	if len(got.BindingMetadata) != 1 {
		t.Fatalf("BindingMetadata len=%d want 1", len(got.BindingMetadata))
	}
	binding := got.BindingMetadata[0]
	// LATERAL 聚合使用 array_agg(DISTINCT sw ORDER BY sw) -> 升序排列。
	wantWords := []string{"banned", "secret"}
	if !reflect.DeepEqual(binding.SensitiveWords, wantWords) {
		t.Fatalf("SensitiveWords=%v want %v", binding.SensitiveWords, wantWords)
	}
}

// -----------------------------------------------------------------------------
// 测试 14 — SnapshotVersionStamp
// snapshot 行 version = 42 → SnapshotVersion 包含 ":42"。
// -----------------------------------------------------------------------------

func TestPostgresRegistry_SnapshotVersionStamp(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newFixture(t, ctx, pool)

	mid := f.seedModel(modelOpts{})
	f.seedAlias(aliasOpts{modelID: mid, publicAliasNormalized: "stamp"})
	f.seedBinding(bindingOpts{modelID: mid, poolGroupID: f.poolGroupID, priority: 100})
	f.setSnapshot(42)

	r := NewPostgresRegistry(pool, nil)
	got, err := r.ResolveModel(ctx, "stamp", f.tenantID)
	if err != nil {
		t.Fatalf("ResolveModel: %v", err)
	}
	wantSuffix := ":42"
	if got.SnapshotVersion == "" {
		t.Fatalf("SnapshotVersion empty")
	}
	if got.SnapshotVersion[len(got.SnapshotVersion)-len(wantSuffix):] != wantSuffix {
		t.Errorf("SnapshotVersion = %q; want suffix %q", got.SnapshotVersion, wantSuffix)
	}
}

// -----------------------------------------------------------------------------
// 测试 15 — ListModelsDiscoverySurface
// ListModels 仅返回租户真正可路由的 alias:启用的 alias、启用的 model、
// 启用且在时间窗内的 binding,以及显式的全局继承。
// -----------------------------------------------------------------------------

func TestPostgresRegistry_ListModelsDiscoverySurface(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newFixture(t, ctx, pool)

	visibleTenant := f.seedModel(modelOpts{
		canonicalID:     "openai/gpt-visible-" + f.suffix,
		providerModelID: "gpt-visible",
		protocolFamily:  "openai_chat",
		contextWindow:   128000,
	})
	f.seedAlias(aliasOpts{
		modelID:               visibleTenant,
		publicAliasNormalized: "gpt-visible-" + f.suffix,
		publicAliasDisplay:    "gpt-visible-" + f.suffix,
	})
	f.seedBinding(bindingOpts{modelID: visibleTenant, poolGroupID: f.poolGroupID})

	unbound := f.seedModel(modelOpts{canonicalID: "openai/unbound-" + f.suffix, protocolFamily: "openai_chat"})
	f.seedAlias(aliasOpts{
		modelID:               unbound,
		publicAliasNormalized: "unbound-" + f.suffix,
		publicAliasDisplay:    "unbound-" + f.suffix,
	})

	disabled := f.seedModel(modelOpts{canonicalID: "openai/disabled-" + f.suffix, protocolFamily: "openai_chat"})
	f.seedAlias(aliasOpts{
		modelID:               disabled,
		publicAliasNormalized: "disabled-" + f.suffix,
		publicAliasDisplay:    "disabled-" + f.suffix,
		status:                "disabled",
	})
	f.seedBinding(bindingOpts{modelID: disabled, poolGroupID: f.poolGroupID})

	globalVisible := f.seedModel(modelOpts{
		scope:           "global",
		canonicalID:     "anthropic/claude-global-visible-" + f.suffix,
		providerModelID: "claude-global-visible",
		contextWindow:   200001,
	})
	f.seedAlias(aliasOpts{
		scope:                 "global",
		modelID:               globalVisible,
		publicAliasNormalized: "claude-global-visible-" + f.suffix,
		publicAliasDisplay:    "claude-global-visible-" + f.suffix,
	})
	f.seedBinding(bindingOpts{modelID: globalVisible, poolGroupID: f.poolGroupID})

	globalShadowed := f.seedModel(modelOpts{
		scope:           "global",
		canonicalID:     "anthropic/claude-shadowed-" + f.suffix,
		providerModelID: "claude-shadowed",
	})
	f.seedAlias(aliasOpts{
		scope:                 "global",
		modelID:               globalShadowed,
		publicAliasNormalized: "claude-shadowed-" + f.suffix,
		publicAliasDisplay:    "claude-shadowed-" + f.suffix,
	})
	f.seedAlias(aliasOpts{
		modelID:               visibleTenant,
		publicAliasNormalized: "claude-shadowed-" + f.suffix,
		publicAliasDisplay:    "claude-shadowed-" + f.suffix,
		status:                "disabled",
	})
	f.seedBinding(bindingOpts{modelID: globalShadowed, poolGroupID: f.poolGroupID})
	f.setInheritPolicy(true)

	r := NewPostgresRegistry(pool, nil)
	got, err := r.ListModels(ctx, f.tenantID)
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	modelsByID := make(map[string]ListedModel, len(got))
	for _, model := range got {
		modelsByID[model.ID] = model
		if model.CreatedAt.IsZero() {
			t.Fatalf("model %q has zero created time", model.ID)
		}
	}

	for _, want := range []string{"claude-global-visible-" + f.suffix, "gpt-visible-" + f.suffix} {
		if _, ok := modelsByID[want]; !ok {
			t.Fatalf("ListModels ids=%v missing %q", modelsByID, want)
		}
	}
	for _, notWant := range []string{"unbound-" + f.suffix, "disabled-" + f.suffix, "claude-shadowed-" + f.suffix} {
		if _, ok := modelsByID[notWant]; ok {
			t.Fatalf("ListModels ids=%v unexpectedly includes %q", modelsByID, notWant)
		}
	}
	tenantModel := modelsByID["gpt-visible-"+f.suffix]
	if tenantModel.CanonicalID != "openai/gpt-visible-"+f.suffix || tenantModel.ContextWindow != 128000 {
		t.Fatalf("tenant model projection=%+v want canonical id and context window from tenant UNION arm", tenantModel)
	}
	globalModel := modelsByID["claude-global-visible-"+f.suffix]
	if globalModel.CanonicalID != "anthropic/claude-global-visible-"+f.suffix || globalModel.ContextWindow != 200001 {
		t.Fatalf("global model projection=%+v want canonical id and context window from global UNION arm", globalModel)
	}
}

func TestPostgresRegistry_UpdateModelCapabilitiesPersistsIntoListModels(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newFixture(t, ctx, pool)

	modelID := f.seedModel(modelOpts{
		canonicalID:     "openai/capability-visible-" + f.suffix,
		providerModelID: "capability-visible",
		protocolFamily:  "openai_chat",
		contextWindow:   128000,
	})
	f.seedAlias(aliasOpts{
		modelID:               modelID,
		publicAliasNormalized: "capability-visible-" + f.suffix,
		publicAliasDisplay:    "capability-visible-" + f.suffix,
	})
	f.seedBinding(bindingOpts{modelID: modelID, poolGroupID: f.poolGroupID})

	maxOutput := 8192
	r := NewPostgresRegistry(pool, nil)
	if _, err := r.UpdateModelCapabilities(ctx, UpdateModelCapabilitiesParams{
		ModelID:         modelID,
		Capabilities:    map[string]bool{"function_calling": true, "tool_choice": true, "vision": true},
		MaxOutputTokens: &maxOutput,
		ModelMode:       ptrString("chat"),
	}); err != nil {
		t.Fatalf("UpdateModelCapabilities: %v", err)
	}

	got, err := r.ListModels(ctx, f.tenantID)
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	var visible ListedModel
	for _, model := range got {
		if model.ID == "capability-visible-"+f.suffix {
			visible = model
			break
		}
	}
	if visible.ID == "" {
		t.Fatalf("ListModels missing updated model; got=%+v", got)
	}
	if !visible.Capabilities["vision"] || !visible.Capabilities["function_calling"] || !visible.Capabilities["tool_choice"] {
		t.Fatalf("capabilities=%+v want persisted descriptor map", visible.Capabilities)
	}
	if visible.MaxOutputTokens == nil || *visible.MaxOutputTokens != 8192 {
		t.Fatalf("max_output_tokens=%v want 8192", visible.MaxOutputTokens)
	}
	if visible.Mode != "chat" {
		t.Fatalf("mode=%q want chat", visible.Mode)
	}
}
