# Gap spec: pricing-catalog (residual-verified)

**Status:** VERIFIED SPEC — ready for dispatch
**Date:** 2026-06-03
**Gap ID:** pricing-catalog
**Verifier:** PM residual-verification agent (sonnet-4-6)
**Source design:** `docs/process/gap-designs/pricing-catalog.md`
**Source critique:** no gap-critiques/pricing-catalog.md (critique file absent)

---

## False premises in the design (verified against real code)

### FP-1 — Migration number 0077 is stated as "next after current max 0076"

**Design claim:** "Migration 0077 is the next number after current max 0076."

**Reality:** This is correct at the time of this verification (max confirmed = 0076,
`sql/migrations/0076_user_role.up.sql`). However, the gap-roadmap explicitly warns:
> "Migration-number collision: 7 designs all claim 0077 ... Assign sequential numbers
> at implementation time (0077, 0078, …). NEVER assume 0077."

The `tiered-billing` design also claims migrations 0077-0078. **The correct migration
number must be determined at implementation time by checking the then-current max.**
The design's hardcoded "0077" is a collision risk, not a verified safe number.
Use `0080` as a placeholder in this spec (conservative; assumes tiered-billing and
one other gap land first) but the implementer MUST verify before creating the file.

### FP-2 — `GET /v1/pricing` (user-browsable catalog) does NOT already exist as a stub

**Design claim (implied):** The design proposes adding `GET /v1/pricing` as a new
user-facing endpoint. The existing routes in `cmd/gateway/routes.go:97-99` are:
- `GET /v1/pricing/rate-table`
- `GET /v1/pricing/snapshots`
- `GET /v1/pricing/snapshots/{snapshot_id}`

None of these is `GET /v1/pricing`. There is no conflict — the new path does not
collide. However the design's description of "three existing endpoints" (lines 38, 205-206)
implies these delegate to `RateTableSource.ListRateTableSnapshots` and
`RateTableSource.GetRateTable`, which is **correct** (verified:
`internal/gatewayhttp/cost_receipt_handler.go:572-608` — `NewPricingRateTableHandler`,
`NewPricingSnapshotsHandler`, `NewPricingSnapshotHandler` all use `d.RateTables`
which is `billing.RateTableSource`).

### FP-3 — `billing_pricing_versions` does NOT have a `tenant_id` column in the base migration

**Design claim:** The apply step uses `ON CONFLICT (tenant_id, version) DO NOTHING`.
The unique index `uq_pricing_tenant_version` is `ON billing_pricing_versions (tenant_id, version)`.

**Reality:** VERIFIED CORRECT. `sql/migrations/0002_observability_billing.up.sql:278-291`
confirms `billing_pricing_versions` has `tenant_id bigint NOT NULL` and
`CREATE UNIQUE INDEX uq_pricing_tenant_version ON billing_pricing_versions (tenant_id, version)`.
The `ON CONFLICT (tenant_id, version) DO NOTHING` pattern is valid.

Also confirmed: `billing_pricing_versions` already has column `is_public boolean NOT NULL`
(added by migration 0031). The design does not reference `is_public` in its new tables
or queries — no problem.

### FP-4 — `pricing_ratio_public` (design text) vs `public_ratio` (migration SQL) naming inconsistency

**Design claim:** The design uses "pricing_ratio_public" as a tenant-level setting name
in prose (section "Toggleable public ratio config") but the migration SQL uses
`public_ratio boolean NOT NULL DEFAULT false` as the column name in
`pool_group_pricing_ratios`. The handler description also uses `public_ratio` in the
JSON body example. This is an INTERNAL INCONSISTENCY in the design — the setting is
actually a per-row column in `pool_group_pricing_ratios`, not a separate tenant-level
setting table.

**Verdict:** The SQL column name `public_ratio` is correct and consistent with the
migration. The prose "pricing_ratio_public" in section 1 and section 3 is misleading
but harmless — it describes the same `public_ratio` column. No separate
`billing_settings` KV key needed. Use `public_ratio` (the column) throughout.

### FP-5 — `parseAdminCatalogPage` helper referenced as "existing adminhttp helper" is correct

**Design claim:** "ratio handlers reuse `parseAdminCatalogPage` (existing adminhttp helper)"

**Reality:** VERIFIED CORRECT. `internal/adminhttp/provider_catalog_handler.go:109-134`
exports `parseAdminCatalogPage(w, r, ident)` → `(adminCatalogPage, bool)`. The type
`adminCatalogPage` is unexported but lives in the same package. New handler files in
`internal/adminhttp` can call it directly. The tenant-isolation logic (lines 137-164)
calls `ident.CanIssueForTenant(tenantID)` which enforces `tenant_operator` scope.
This is directly reusable.

### FP-6 — `modelsync.HTTPFetcher` uses API keys; `upstreamprice.HTTPFetcher` must NOT

**Design claim:** "mirrors `modelsync.HTTPFetcher` security pattern"

**Reality:** VERIFIED WITH NUANCE. `internal/modelsync/http_fetcher.go` has
`apiKey string` field and sends it in headers (lines 119-120, 169-170). The redirect
block (`CheckRedirect` returning `http.ErrUseLastResponse`) is at lines 58-62 and is
the pattern to copy. The body size cap `maxModelListBytes = 8 << 20` is at line 23.
The `upstreamprice.HTTPFetcher` MUST copy the redirect-block and size-cap pattern but
MUST NOT copy the `apiKey` field (CMB-5: no credentials logged). The design correctly
states no API key argument — this is valid.

### FP-7 — `shopspring/decimal` is already a dependency

**Design claim:** Use `shopspring/decimal` for ratio validation.
**Reality:** VERIFIED. `go.mod:10` — `github.com/shopspring/decimal v1.4.0`. No new
dependency needed.

### FP-8 — `admin.RolePlatformAdmin` constant name is correct

**Design claim:** "platform_admin only" for admin endpoints.
**Reality:** VERIFIED. `internal/admin/admin.go:54` — `RolePlatformAdmin = "platform_admin"`.
The model_sync_handler.go pattern (line 71: `if ident.Role != admin.RolePlatformAdmin`)
is the exact guard to copy.

---

## What already exists (reuse points)

| Component | Location | How to reuse |
|-----------|----------|--------------|
| Versioned rate table | `sql/migrations/0002_observability_billing.up.sql:277-291` — `billing_pricing_versions(id, tenant_id, version, pricing_data jsonb, effective_from, effective_to, created_at, created_by_actor)` + `is_public` from migration 0031 | New `upstream_price_presets.applied_version` FK target; `PostgresApplier.Apply` inserts here with `ON CONFLICT (tenant_id, version) DO NOTHING` |
| Read-only pricing endpoints | `internal/gatewayhttp/cost_receipt_handler.go:548-608` — `NewPricingRateTableHandler`, `NewPricingSnapshotsHandler`, `NewPricingSnapshotHandler` using `billing.RateTableSource` | Reuse as-is for `GET /v1/pricing/versions` and `GET /v1/pricing/versions/{version}` by re-registering same handlers at new paths, OR leave existing paths untouched |
| `RateTableSource` interface | `internal/billing/rate_table_source.go:37-41` — `GetRateTable`, `GetRateTableSnapshot`, `ListRateTableSnapshots` | `pricingcatalog.PostgresStore` can read `billing_pricing_versions` directly via the same SQL patterns |
| Redirect-block + size-cap pattern | `internal/modelsync/http_fetcher.go:58-62` (redirect block), line 23 (`maxModelListBytes = 8 << 20`) | Copy pattern verbatim into `upstreamprice.HTTPFetcher`; do NOT import modelsync (avoid circular dep) |
| `parseAdminCatalogPage` + `parseAdminCatalogTenant` | `internal/adminhttp/provider_catalog_handler.go:109-164` | Call directly from new pricing ratio and upstream handler files in same package |
| `admin.RolePlatformAdmin` guard pattern | `internal/adminhttp/model_sync_handler.go:71-73` | Copy guard pattern into ratio and upstream handlers |
| `shopspring/decimal` | `go.mod:10` — already present | Use `decimal.NewFromString(body.Ratio)` + `.IsPositive()` check |
| `writeError` / `writeAdminAuthError` / `writeAdminCatalogJSON` | `internal/adminhttp/provider_catalog_handler.go:64,70,174` | Use these existing helpers in new files |
| `billing_pricing_versions` unique index | `uq_pricing_tenant_version ON billing_pricing_versions(tenant_id, version)` | `ON CONFLICT (tenant_id, version) DO NOTHING` is valid |

---

## True residual (what is genuinely missing)

1. **Schema (migration ~0077+):** New tables `pool_group_pricing_ratios` and
   `upstream_price_presets`. Neither exists anywhere in migrations. This is genuinely new.

2. **`internal/pricingcatalog` package:** No existing package reads ratio from
   `pool_group_pricing_ratios` or computes `base × ratio`. Entirely absent. Must build.

3. **`internal/pricingratiohttp` package:** No `GET /v1/pricing` endpoint serving
   per-group effective prices. The three existing `/v1/pricing/*` routes are read-only
   raw rate-table lookups with no ratio logic. Must build.

4. **`internal/upstreamprice` package:** No upstream price fetch/diff/apply pipeline.
   `modelsync` fetches vendor model *catalogs* (IDs, capabilities), not pricing data.
   Must build.

5. **Admin handlers in `internal/adminhttp`:** No ratio CRUD endpoints, no upstream
   pricing sync endpoints. The `model_sync_handler.go` is for model catalog sync only.
   Must build (as new files in existing package).

6. **Route registrations:** New paths in `cmd/gateway/routes.go` (user-facing
   `GET /v1/pricing`) and `mountAdminRoutes` (admin ratio + upstream paths).

---

## First slice spec

### Scope

Highest-value, collision-free first slice: the **per-group ratio CRUD admin endpoints**
backed by the new schema. This delivers the core pricing multiplier capability without
the upstream-fetch complexity, is fully isolated in a new package + new adminhttp files,
and has a clean discriminating test surface.

### Migration

File: `sql/migrations/NNNN_pool_group_pricing_ratios.up.sql`
(where NNNN = current max migration number + 1, determined at implementation time)

```sql
BEGIN;

CREATE TABLE IF NOT EXISTS pool_group_pricing_ratios (
    id            bigserial     PRIMARY KEY,
    tenant_id     bigint        NOT NULL REFERENCES tenants(id),
    pool_group_id bigint        NOT NULL REFERENCES pool_groups(id),
    ratio         numeric(12,6) NOT NULL CHECK (ratio > 0),
    public_ratio  boolean       NOT NULL DEFAULT false,
    created_by    text          NOT NULL,
    updated_by    text          NOT NULL,
    created_at    timestamptz   NOT NULL DEFAULT now(),
    updated_at    timestamptz   NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX uq_pgpr_tenant_group
    ON pool_group_pricing_ratios (tenant_id, pool_group_id);

CREATE INDEX idx_pgpr_tenant
    ON pool_group_pricing_ratios (tenant_id);

COMMENT ON TABLE pool_group_pricing_ratios IS
    'Per pool-group pricing ratio multiplier. effective_price = base_price * ratio. '
    'ratio > 0 enforced by CHECK. public_ratio controls whether multiplier is visible to end-users.';

COMMIT;
```

Down migration (`NNNN_pool_group_pricing_ratios.down.sql`):
```sql
BEGIN;
DROP TABLE IF EXISTS pool_group_pricing_ratios;
COMMIT;
```

**Note:** `upstream_price_presets` deferred to slice 2 (upstream fetch/diff/apply).

### New files to ADD

All files under 500 lines, single responsibility.

#### `internal/pricingcatalog/catalog.go` (~110 lines)

Domain types and `Store` interface. No I/O.

```go
package pricingcatalog

import (
    "context"
    "errors"
    "github.com/shopspring/decimal"
)

var ErrNotFound = errors.New("pricingcatalog: not found")
var ErrBackend  = errors.New("pricingcatalog: backend error")

// GroupPricingRatio is one row from pool_group_pricing_ratios.
type GroupPricingRatio struct {
    ID           int64
    TenantID     int64
    PoolGroupID  int64
    Ratio        decimal.Decimal // > 0; 1.0 = base price
    PublicRatio  bool            // when false, multiplier hidden from end-users
    CreatedBy    string
    UpdatedBy    string
}

// UpsertRatioParams is the write input.
type UpsertRatioParams struct {
    TenantID    int64
    PoolGroupID int64
    Ratio       decimal.Decimal
    PublicRatio bool
    Actor       string
}

// Store is the persistence interface for pool_group_pricing_ratios.
type Store interface {
    // GetRatio returns the ratio for (tenantID, poolGroupID).
    // Returns ErrNotFound if no row exists.
    GetRatio(ctx context.Context, tenantID, poolGroupID int64) (GroupPricingRatio, error)
    // ListRatios returns all ratio rows for tenantID.
    ListRatios(ctx context.Context, tenantID int64) ([]GroupPricingRatio, error)
    // UpsertRatio creates or updates the ratio row.
    UpsertRatio(ctx context.Context, p UpsertRatioParams) (GroupPricingRatio, error)
    // DeleteRatio removes the ratio row. Returns ErrNotFound if absent.
    DeleteRatio(ctx context.Context, tenantID, poolGroupID int64) error
}
```

#### `internal/pricingcatalog/postgres_store.go` (~160 lines)

Implements `Store` against `pool_group_pricing_ratios`. Uses raw `pgxpool.Pool` (same
pattern as `billing/rate_table_source.go`). No sqlc file needed for first slice.

Key query signatures (verified column names from migration above):

```go
// GetRatio:
// SELECT id, tenant_id, pool_group_id, ratio, public_ratio, created_by, updated_by, created_at, updated_at
// FROM pool_group_pricing_ratios
// WHERE tenant_id = $1 AND pool_group_id = $2

// ListRatios:
// SELECT id, tenant_id, pool_group_id, ratio, public_ratio, created_by, updated_by, created_at, updated_at
// FROM pool_group_pricing_ratios
// WHERE tenant_id = $1
// ORDER BY pool_group_id ASC

// UpsertRatio:
// INSERT INTO pool_group_pricing_ratios
//   (tenant_id, pool_group_id, ratio, public_ratio, created_by, updated_by)
// VALUES ($1, $2, $3, $4, $5, $5)
// ON CONFLICT (tenant_id, pool_group_id) DO UPDATE
//   SET ratio = EXCLUDED.ratio,
//       public_ratio = EXCLUDED.public_ratio,
//       updated_by = EXCLUDED.updated_by,
//       updated_at = now()
// RETURNING id, tenant_id, pool_group_id, ratio, public_ratio, created_by, updated_by, created_at, updated_at

// DeleteRatio:
// DELETE FROM pool_group_pricing_ratios
// WHERE tenant_id = $1 AND pool_group_id = $2
// RETURNING id
```

#### `internal/pricingcatalog/postgres_store_test.go` (~130 lines)

Unit tests against a `Store` stub (no DB).

#### `internal/adminhttp/pricing_ratio_handler.go` (~190 lines)

Admin endpoints for ratio CRUD. Platform-admin only for write; tenant_operator read.

Routes (mounted by `cmd/gateway/routes.go`):
- `GET  /admin/v1/pricing/ratios` — list ratios for resolved tenant
- `GET  /admin/v1/pricing/ratios/{pool_group_id}` — get one ratio
- `PUT  /admin/v1/pricing/ratios/{pool_group_id}` — upsert ratio
- `DELETE /admin/v1/pricing/ratios/{pool_group_id}` — delete ratio

Auth guard pattern (from `model_sync_handler.go:71`):
```go
if ident.Role != admin.RolePlatformAdmin {
    writeAdminError(w, admin.ErrAdminForbidden)
    return
}
```
Tenant resolution via `parseAdminCatalogPage(w, r, ident)`.

Ratio validation:
```go
ratio, err := decimal.NewFromString(body.Ratio)
if err != nil || !ratio.IsPositive() {
    writeError(w, http.StatusBadRequest, "invalid_ratio", "ratio must be a positive decimal")
    return
}
```

#### `internal/adminhttp/pricing_ratio_handler_test.go` (~140 lines)

Discriminating unit tests (see discriminating tests section).

### Existing files to EDIT

#### `cmd/gateway/routes.go`

Add inside `mountAdminRoutes`, after the existing `admin/v1/model-sync` block:

```go
r.Route("/admin/v1/pricing/ratios", func(r chi.Router) {
    adminhttp.MountPricingRatioRoutes(r, adminhttp.AdminPricingRatioDeps{
        Auth:  d.adminAuth,
        Store: d.pricingRatioStore,
    })
})
```

Add `d.pricingRatioStore pricingcatalog.Store` to `deps` struct in `wiring.go`, wired
via `pricingcatalog.NewPostgresStore(pgPool)`.

**Note:** `cmd/gateway/routes.go` is not in the frozen list. Edits to this file are
permitted. `internal/gatewayhttp`, `internal/gateway`, `internal/proto` are frozen for
NEW files; editing `routes.go` (which lives in `cmd/gateway`, not those packages) is OK.

#### `cmd/gateway/wiring.go`

Add `pricingRatioStore pricingcatalog.Store` field to `deps` and wire it.

---

## Discriminating tests

Each test must go RED if the specific named defect is introduced.

### `internal/pricingcatalog` (store stub tests)

| Test name | Defect it defends |
|-----------|-------------------|
| `TestPostgresStore_ReturnsMissingRatioErrNotFound` | If `GetRatio` returns a zero-value `GroupPricingRatio` instead of `ErrNotFound` when no row exists, callers default to ratio=0 and silently zero all prices |
| `TestPostgresStore_TenantIsolation` | If `tenant_id` filter is omitted from `ListRatios` SQL, tenant A's ratios are visible to tenant B |
| `TestPostgresStore_UpsertIdempotent` | If `ON CONFLICT DO UPDATE` is replaced with plain `INSERT`, a second upsert for the same (tenant, group) panics rather than updating |
| `TestPostgresStore_BackendErrorPropagated` | If DB error is swallowed and `ErrBackend` is not returned, caller serves stale or zero-value prices without knowing |

### `internal/adminhttp/pricing_ratio_handler.go` (unit tests with stub Store)

| Test name | Defect it defends |
|-----------|-------------------|
| `TestPricingRatioHandler_NonAdminIs403` | If `RolePlatformAdmin` check is removed, any authenticated admin token (including `tenant_operator`) can set arbitrary ratios |
| `TestPricingRatioHandler_ZeroRatioIs400` | If `ratio.IsPositive()` check is removed, `ratio=0` is accepted and catalog prices silently become $0 |
| `TestPricingRatioHandler_NegativeRatioIs400` | If negativity check is skipped, `ratio=-1.5` is accepted and effective prices become negative, corrupting downstream billing |
| `TestPricingRatioHandler_InvalidDecimalStringIs400` | If `decimal.NewFromString` error is swallowed, `"abc"` or `"1.2.3"` is parsed as zero ratio |
| `TestPricingRatioHandler_DeleteReturns404WhenMissing` | If `ErrNotFound` from `DeleteRatio` is not mapped to 404, DELETE of non-existent group returns 200, masking operator errors |
| `TestPricingRatioHandler_TenantIsolationViaParseAdminCatalogPage` | If `parseAdminCatalogPage` call is bypassed, a `platform_admin` with no `tenant_id` query param returns 200 with empty list instead of 400 |

---

## Risk classification

- **riskClass:** `schema` — requires a new migration (`pool_group_pricing_ratios`).
  The first slice writes to a new table only; no existing tables are modified.
  No money path (billing_ledger_claims / settler / Tx1/Tx2) is touched.
  No credentials are logged or selected (CMB-5 satisfied).
  No changes to `internal/{gatewayhttp,gateway,proto,router}` packages (CMB-7 satisfied).

---

## Parallelizability

**parallelizable: true**

The first slice (ratio CRUD) creates:
- New package `internal/pricingcatalog` (no shared files with any in-flight gap)
- Two new files in `internal/adminhttp` (no in-flight gap touches adminhttp)
- Edits to `cmd/gateway/routes.go` and `wiring.go` (shared files; collision possible
  if another gap is simultaneously editing routes.go)

If another gap is concurrently editing `routes.go` / `wiring.go`, the route-mount
lines are the only potential collision. An isolated worktree resolves this cleanly.
All new packages (`pricingcatalog`, future `pricingratiohttp`, `upstreamprice`) are
isolated from all other in-flight gaps.

---

## Slice 2 (deferred, not in first slice)

- Migration for `upstream_price_presets`
- `internal/upstreamprice` (fetcher + diff + apply)
- `internal/pricingratiohttp` (`GET /v1/pricing` user-browsable endpoint)
- Admin upstream fetch/apply handlers in `internal/adminhttp`

---

## Parity verification

| Reference behavior | Source | HUAKAI design | Status |
|--------------------|--------|---------------|--------|
| Per-group ratio multiplier | `new-api/pkg/billingexpr/settle.go:25` — `applyGroupRatio` | `pool_group_pricing_ratios.ratio`; applied in `pricingcatalog` effective price | Parity |
| Append-only pricing version history | `billing_pricing_versions` existing | `PostgresApplier` (slice 2) only INSERTs; existing invariant preserved | Parity |
| Redirect-block on HTTP fetcher | `internal/modelsync/http_fetcher.go:58-62` | `upstreamprice.HTTPFetcher` (slice 2) copies exact pattern | Parity |
| Body size cap | `internal/modelsync/http_fetcher.go:23` — 8 MiB | `upstreamprice.HTTPFetcher` uses 4 MiB (tighter) | Better |

---

## Effort

**S** (small) — first slice only:
- Migration: 0.25 day
- `pricingcatalog` package: 0.75 day
- `adminhttp` pricing ratio handler + tests: 0.75 day
- Route wiring: 0.25 day
**Total first slice: ~2 days**

Full design (all slices): ~5 days as stated in design.
