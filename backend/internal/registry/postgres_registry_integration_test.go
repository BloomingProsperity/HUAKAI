//go:build integration_pg

// Slice 2 integration tests for PostgresRegistry against real
// PostgreSQL. Validates the 14 original cases enumerated in
// docs/process/plans/2026-04-30-n5-model-registry.md §"Test Plan":
//
//   1.  HappyPath
//   2.  UnknownAlias
//   3.  DisabledAlias
//   4.  DisabledModel
//   5.  TenantDisabledBlocksGlobal  (D3 explicit-deny invariant)
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
// fixture helpers
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
	// pool group owned by tenant; reused as binding target.
	poolGroupID int64
	// Auxiliary tenant (used by cross-tenant probe assertions, if any).
	otherTenantID int64
	// IDs created by helpers, batched for cleanup.
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
	// Tenant-scoped rows: bindings always tenant-scoped.
	_, _ = f.pool.Exec(c, `DELETE FROM model_pool_bindings WHERE tenant_id = $1`, f.tenantID)
	// Capabilities/aliases/models: tenant-scoped + opt-in scope='global'
	// rows seeded by tests that exercise inheritance (test/).
	_, _ = f.pool.Exec(c, `DELETE FROM model_registry_capabilities WHERE tenant_id = $1`, f.tenantID)
	_, _ = f.pool.Exec(c, `DELETE FROM model_aliases WHERE tenant_id = $1`, f.tenantID)
	_, _ = f.pool.Exec(c, `DELETE FROM models WHERE tenant_id = $1`, f.tenantID)
	if f.createdGlobalRows {
		// Inheritance tests seed scope='global' rows with public_alias
		// values that include f.suffix; clean those by foreign-key chain.
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
	scope            string // "tenant" or "global"; defaults "tenant"
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
	scope                 string // defaults "tenant"
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
	// Bindings are ALWAYS tenant-scoped.
	// modelID may point at a global-scope model — that is the inheritance
	// path: tenant T sets up its own binding to a globally-shared model.
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
// Test 1 — HappyPath
// -----------------------------------------------------------------------------

func TestPostgresRegistry_HappyPath(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newFixture(t, ctx, pool)

	// Current top-tier Anthropic model per verified WebFetch 2026-04-30T10:08Z
	// @ https://platform.claude.com/docs/en/docs/about-claude/models/overview.
	// See docs/process/plans/2026-04-30-n5-model-registry.md Appendix B.
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
// Test 2 — UnknownAlias
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
// Test 3 — DisabledAlias
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
// Test 4 — DisabledModel (alias active, model disabled)
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
// Test 5 — TenantDisabledBlocksGlobal (D3 explicit-deny invariant)
// Tenant has DISABLED alias for "claude-x".
// Global has ACTIVE alias for "claude-x".
// inherit_global_catalog = TRUE.
// MUST return ErrModelDisabled, never fall through to global.
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
	// Tenant-scoped binding to the global model — present so that IF the
	// explicit-deny invariant were broken, fallback would actually succeed
	// (sharpening the assertion). Bindings are tenant-scoped.
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
// Test 6 — InheritGlobalActive
// Tenant has NO row.
// Global has ACTIVE row.
// inherit = TRUE.
// MUST resolve via global path.
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
	// Tenant-scoped binding pointing at the GLOBAL model — this is the
	// inheritance shape: the global catalog is shared at the model/alias
	// layer, but every tenant declares its own routing target.
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
// Test 7 — InheritOff
// No tenant row, no policy row (or inherit=false), MUST be ErrUnknownModel
// even if global has the alias.
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
	// Tenant binding seeded to make the assertion sharper: even with a
	// route ready to go, no-inherit must short-circuit at alias lookup.
	f.seedBinding(bindingOpts{
		modelID: gmid, poolGroupID: f.poolGroupID, priority: 100,
	})
	// No setInheritPolicy → policy row absent → resolver treats as false.

	r := NewPostgresRegistry(pool, nil)
	_, err := r.ResolveModel(ctx, "claude-z", f.tenantID)
	if !errors.Is(err, ErrUnknownModel) {
		t.Fatalf("err = %v; want ErrUnknownModel", err)
	}
}

// -----------------------------------------------------------------------------
// Test 8 — NoBindings
// -----------------------------------------------------------------------------

func TestPostgresRegistry_NoBindings(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newFixture(t, ctx, pool)

	mid := f.seedModel(modelOpts{})
	f.seedAlias(aliasOpts{modelID: mid, publicAliasNormalized: "naked"})
	// No binding seeded.

	r := NewPostgresRegistry(pool, nil)
	_, err := r.ResolveModel(ctx, "naked", f.tenantID)
	if !errors.Is(err, ErrTenantNoAccess) {
		t.Fatalf("err = %v; want ErrTenantNoAccess", err)
	}
}

// -----------------------------------------------------------------------------
// Test 9 — MultipleBindingsOrdered
// Three bindings with priorities 200 / 50 / 100.
// PoolCandidates should be [50, 100, 200] (priority asc, then id asc).
// -----------------------------------------------------------------------------

func TestPostgresRegistry_MultipleBindingsOrdered(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newFixture(t, ctx, pool)

	// Need three pool groups owned by the same tenant.
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
// Test 10 — CaseInsensitive
// Alias seeded as "Claude-3-5-Sonnet" (display) / normalized lower.
// Resolve with "CLAUDE-3-5-SONNET" must match.
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
// Test 11 — SoftDeletedAliasInvisible
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
// Test 12 — EffectiveTimeWindow
// Binding effective_until in the past → excluded → ErrTenantNoAccess.
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
// Test 13 — ProviderModelOverrideOnPrimary
// Primary binding has provider_model_id_override → ResolvedModel.ProviderModelID
// reflects the override, not the canonical model default.
// (Mirrors one-api ModelMapping behavior per
// model/channel.go @ 3915ce9 — verified WebFetch 2026-04-30T09:35Z.)
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
	// LATERAL aggregation uses array_agg(DISTINCT sw ORDER BY sw) -> sorted ascending.
	wantWords := []string{"banned", "secret"}
	if !reflect.DeepEqual(binding.SensitiveWords, wantWords) {
		t.Fatalf("SensitiveWords=%v want %v", binding.SensitiveWords, wantWords)
	}
}

// -----------------------------------------------------------------------------
// Test 14 — SnapshotVersionStamp
// snapshot row version = 42 → SnapshotVersion contains ":42".
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
// Test 15 — ListModelsDiscoverySurface
// ListModels returns only aliases the tenant can actually route: active alias,
// active model, enabled in-window binding, and explicit global inheritance.
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
