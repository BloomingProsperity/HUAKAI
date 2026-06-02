# Gap design: Pricing catalog — per-group ratios + upstream preset sync

Status: **DRAFT**
Date: 2026-06-03
Author: HUAKAI backend architect (subagent)
Gap ID: pricing-catalog

---

## Summary

HUAKAI already stores versioned pricing tables in `billing_pricing_versions` and
exposes read-only endpoints via `billing.RateTableSource`. What is missing:

1. **Per-group ratio multipliers**: each `pool_groups` row can carry a
   `pricing_ratio` (a decimal multiplier, e.g. `1.5` = 150 % of base price) so
   different capacity groups charge different markups. Users browsing the pricing
   catalog see effective prices (base × group ratio) for the group(s) they have
   access to.

2. **Upstream preset sync**: an admin-triggered (or scheduled) job fetches raw
   model pricing from public upstream sources (models.dev JSON feed; basellm
   price sheet) and computes a diff against the current local table. The admin
   can then apply the diff to create a new `billing_pricing_versions` row. No
   credentials are used or logged; only the public HTTP response is stored.

3. **Toggleable public ratio config**: a per-tenant setting
   (`pricing_ratio_public`) controls whether the per-group ratio is visible to
   end-users or hidden (operators show only the effective final price without
   disclosing the multiplier).

This design adds three new packages (`pricingcatalog`, `pricingratiohttp`,
`upstreamprice`) and one new migration (0077).

### What is NOT in scope

- Dynamic tiered billing expressions (New API `billingexpr`) — deferred to
  `F-BILL-TIER-001`.
- Per-user or per-key ratio overrides — deferred.
- Writing to `billing_ledger_claims` or any Tx1/Tx2 path — this design is
  catalog-read + admin-write only.

---

## Package layout

New packages live under `C:\HUAKAI\repo\backend\internal\`.
All hand-written files are kept under 500 lines; functions under 80 lines.

### `internal/pricingcatalog` — domain types and read service

| File | Responsibility | Est. lines |
|---|---|---|
| `catalog.go` | `Service` interface + `ModelPrice` / `GroupPrice` / `CatalogPage` types; `ErrNotFound`, `ErrBackend` sentinels | ~120 |
| `postgres_store.go` | `PostgresStore` — reads `billing_pricing_versions` joined to `pool_group_pricing_ratios`; implements `Store` interface | ~180 |
| `postgres_store_test.go` | Discriminating unit tests against `Store` interface stub | ~150 |

**Total: 3 files, all < 500 lines.**

Responsibility boundary: this package is SELECT-only. It never touches
credentials, claim rows, or billing settlement paths.

### `internal/pricingratiohttp` — user-browsable catalog HTTP handlers

| File | Responsibility | Est. lines |
|---|---|---|
| `handler.go` | `GET /v1/pricing` handler — resolves caller identity, reads tenant's effective prices (base × ratio) for accessible groups; honours `pricing_ratio_public` setting | ~180 |
| `handler_test.go` | Discriminating tests: unauthenticated → 401; ratio hidden → effective price only; ratio visible → multiplier field present; backend error → 503 | ~140 |
| `types.go` | Request/response JSON structs (`PricingListResponse`, `ModelPriceItem`); no domain logic | ~60 |

**Total: 3 files, all < 500 lines.**

### `internal/upstreamprice` — upstream preset fetch + diff + apply

| File | Responsibility | Est. lines |
|---|---|---|
| `fetcher.go` | `Fetcher` interface; `HTTPFetcher` — fetches models.dev and/or basellm JSON over HTTPS; no API key required; redirect-block (mirrors `modelsync.HTTPFetcher` security pattern); body capped at 4 MiB | ~180 |
| `fetcher_test.go` | Discriminating tests: HTTP 404 → error; redirect → blocked; oversized → error; valid JSON → parsed | ~120 |
| `diff.go` | `Diff(upstream, local []ModelPriceRow) DiffResult` — pure function: computes added/changed/unchanged; never writes | ~100 |
| `diff_test.go` | Discriminating tests: empty upstream → all removed; price change detected; no-change is unchanged; new model flagged added | ~80 |
| `apply.go` | `Applier` interface; `PostgresApplier.Apply(ctx, DiffResult, actor, version) error` — inserts new `billing_pricing_versions` row inside a single transaction; idempotent via `ON CONFLICT (tenant_id, version) DO NOTHING` | ~140 |
| `apply_test.go` | Discriminating tests: apply creates new version row; duplicate version is no-op; actor is recorded | ~100 |
| `types.go` | `ModelPriceRow`, `DiffResult`, `DiffEntry` value types; upstream source enum | ~60 |

**Total: 7 files, all < 500 lines.**

### `internal/adminhttp` — new handlers mounted into existing package (modify existing)

Per the FROZEN rule only `internal/adminhttp` may be modified (not created).
Two new handler files are added:

| File | Responsibility | Est. lines |
|---|---|---|
| `pricing_ratio_handler.go` | Admin endpoints: `GET/PUT /admin/pricing/ratios/{pool_group_id}` — read + set per-group ratio; `platform_admin` only; uses `shopspring/decimal` for validation; writes to `pool_group_pricing_ratios` via `upstreamprice.PostgresApplier`-style store | ~200 |
| `pricing_ratio_handler_test.go` | Discriminating tests: non-admin → 403; invalid decimal → 400; zero ratio → 400 (fail-closed); valid upsert → 200 | ~160 |
| `pricing_upstream_handler.go` | Admin endpoints: `POST /admin/pricing/upstream/fetch` (fetch+diff, dry-run, returns diff); `POST /admin/pricing/upstream/apply` (apply diff to new version); `platform_admin` only | ~220 |
| `pricing_upstream_handler_test.go` | Discriminating tests: fetch error → 502; diff-only returns diff without write; apply creates version; duplicate version → 409; upstream payload never logged | ~170 |

**Total: 4 new files in existing package, all < 500 lines.**

### Summary count

| Package | New files | Max file lines |
|---|---|---|
| `pricingcatalog` | 3 | 180 |
| `pricingratiohttp` | 3 | 180 |
| `upstreamprice` | 7 | 180 |
| `adminhttp` (modified) | +4 files | 220 |

No file exceeds 500 lines. No file mixes more than one responsibility.

---

## Schema / migrations

### Migration 0077 — `pool_group_pricing_ratios`

File: `sql/migrations/0077_pool_group_pricing_ratios.up.sql`

```sql
BEGIN;

-- Per-group pricing ratio multiplier.
-- A ratio of 1.000000 means base price; 1.500000 means 150% of base.
-- Stored separately from pool_groups to keep the pool schema orthogonal to billing.
-- CMB-2 carve-out: decimal pricing lives here, not in registry/router.
CREATE TABLE IF NOT EXISTS pool_group_pricing_ratios (
    id              bigserial       PRIMARY KEY,
    tenant_id       bigint          NOT NULL REFERENCES tenants(id),
    pool_group_id   bigint          NOT NULL REFERENCES pool_groups(id),
    -- Ratio must be > 0; 0 is explicitly rejected (fail-closed: a zero ratio
    -- would silently make all model prices zero).
    ratio           numeric(12,6)   NOT NULL
                        CHECK (ratio > 0),
    -- public_ratio: when false, the ratio multiplier is hidden from user-facing
    -- catalog responses; only the effective (base × ratio) price is shown.
    public_ratio    boolean         NOT NULL DEFAULT false,
    created_by      text            NOT NULL,
    updated_by      text            NOT NULL,
    created_at      timestamptz     NOT NULL DEFAULT now(),
    updated_at      timestamptz     NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX uq_pgpr_tenant_group
    ON pool_group_pricing_ratios (tenant_id, pool_group_id);

CREATE INDEX idx_pgpr_tenant
    ON pool_group_pricing_ratios (tenant_id);

COMMENT ON TABLE pool_group_pricing_ratios IS
    'Per pool-group pricing ratio multiplier. effective_price = base_price × ratio. '
    'CMB-2: decimal money lives here, not in pool_groups or registry. '
    'ratio > 0 enforced by CHECK — fail-closed, never zero.';

-- Upstream preset cache table: stores the last-fetched raw upstream price sheet
-- as an immutable append-only log. Raw payload is stored for audit and diff replay.
-- CMB invariant: upstream payloads NEVER contain provider credentials;
-- sources are public HTTP endpoints only.
CREATE TABLE IF NOT EXISTS upstream_price_presets (
    id              bigserial       PRIMARY KEY,
    tenant_id       bigint          NOT NULL REFERENCES tenants(id),
    source          text            NOT NULL
                        CHECK (source IN ('models_dev', 'basellm', 'manual')),
    fetched_at      timestamptz     NOT NULL DEFAULT now(),
    -- Raw JSON payload from public upstream; bounded to 4 MiB at fetch time.
    raw_payload     jsonb           NOT NULL,
    -- SHA-256 hex of raw_payload for dedup and diff anchoring.
    payload_hash    text            NOT NULL,
    fetch_actor     text            NOT NULL,
    -- Which billing_pricing_versions row was created from this preset (NULL = not yet applied).
    applied_version text
);

CREATE INDEX idx_upp_tenant_source_fetched
    ON upstream_price_presets (tenant_id, source, fetched_at DESC);

COMMENT ON TABLE upstream_price_presets IS
    'Append-only log of upstream price fetches. '
    'CMB invariant: raw_payload comes from public HTTP only; no credentials stored or logged.';

COMMIT;
```

Down migration (`0077_pool_group_pricing_ratios.down.sql`):

```sql
BEGIN;
DROP TABLE IF EXISTS upstream_price_presets;
DROP TABLE IF EXISTS pool_group_pricing_ratios;
COMMIT;
```

---

## Endpoints

All paths below are relative to the API root. Auth scope column shows the
minimum admin token role required.

### User-facing

| Method | Path | Auth | Description |
|---|---|---|---|
| `GET` | `/v1/pricing` | Bearer (any valid API key) | Return per-model effective prices for the calling tenant's accessible pool groups. If `public_ratio=false` for a group, only the effective price is shown; the multiplier field is omitted. |
| `GET` | `/v1/pricing/versions` | Bearer (any valid API key) | List public `billing_pricing_versions` snapshots (delegates to existing `RateTableSource.ListRateTableSnapshots`). |
| `GET` | `/v1/pricing/versions/{version}` | Bearer (any valid API key) | Fetch one public pricing version by label (delegates to `RateTableSource.GetRateTable`). |

### Admin-facing

| Method | Path | Auth scope | Description |
|---|---|---|---|
| `GET` | `/admin/pricing/ratios` | `platform_admin` or `tenant_operator` (scoped) | List all `pool_group_pricing_ratios` rows for the resolved tenant. |
| `PUT` | `/admin/pricing/ratios/{pool_group_id}` | `platform_admin` | Upsert ratio for one pool group. Body: `{"ratio":"1.25","public_ratio":false}`. Validates ratio > 0 via `shopspring/decimal`; rejects ratio = 0 (fail-closed). |
| `DELETE` | `/admin/pricing/ratios/{pool_group_id}` | `platform_admin` | Remove ratio row (group reverts to implicit 1.0). |
| `POST` | `/admin/pricing/upstream/fetch` | `platform_admin` | Fetch upstream price sheet from `source` (`models_dev` or `basellm`), compute diff against current local version, return `DiffResult`. **Dry-run only** — nothing is written. |
| `POST` | `/admin/pricing/upstream/apply` | `platform_admin` | Apply a previously-fetched preset (by `preset_id`) to a new `billing_pricing_versions` row. Requires `version` string in body. Idempotent: duplicate version label → 409. |
| `GET` | `/admin/pricing/upstream/presets` | `platform_admin` | List `upstream_price_presets` rows (most-recent-first, paginated). |

---

## Invariants honored

### CMB invariants

| Invariant | How honored |
|---|---|
| **Credentials never logged** | `upstreamprice.HTTPFetcher` fetches public endpoints only (no API key argument); `raw_payload` stored in `upstream_price_presets` comes from public HTTP. The fetcher has no `apiKey` field. |
| **Raw upstream payloads never logged** | Handler logs only `preset_id`, `source`, `payload_hash`, and `fetch_actor`. The `raw_payload` column is never included in log lines. |
| **Router reads no credentials and writes nothing** | No package in this design is imported from `internal/gateway`, `internal/gatewayhttp`, or `internal/router`. |
| **Fail-closed on ambiguity** | `ratio > 0` CHECK constraint + application-layer validation. If ratio row is missing, effective ratio defaults to `1.0` (base price unchanged) — never zero. |

### Money-path invariants

| Invariant | How honored |
|---|---|
| **`shopspring/decimal` for all money** | `ratio` field validated as `decimal.Decimal`; `ModelPrice.InputMicroUSD` etc. are `decimal.Decimal`. No `float64` in money paths. |
| **`billing_pricing_versions` is append-only** | `PostgresApplier.Apply` only `INSERT`s new rows with `ON CONFLICT DO NOTHING`. Existing versions are never mutated. |
| **Tx1/Tx2 reserve+settle unaffected** | This design never touches `billing_ledger_claims`, `billing_events`, or `usage_records`. The ratio multiplier is a catalog concern; actual settlement cost computation is a separate Phase E item (`F-BILL-SNAPSHOT-001`). |
| **Schema changes via numbered migration** | Migration 0077 is the next number after current max 0076. |

### Modularity invariants

- Each new package has a single responsibility (catalog read / HTTP presentation / upstream fetch / diff / apply).
- No god-files: largest file is `pricing_upstream_handler.go` at ~220 lines.
- `upstreamprice.Diff` is a pure function with no I/O — independently testable.

---

## Discriminating tests

Each test must fail if the specific defect it defends is introduced.

### `pricingcatalog`

| Test | Defect it defends |
|---|---|
| `TestPostgresStore_ReturnsMissingRatioAs1` | If `PostgresStore` returns ratio=0 when no `pool_group_pricing_ratios` row exists, effective price would be zero — silent zeroing of all prices. |
| `TestPostgresStore_TenantIsolation` | If tenant_id filter is missing from query, tenant A's prices leak to tenant B. |
| `TestPostgresStore_BackendErrorPropagated` | If DB error is swallowed, caller would silently serve stale or zero prices. |

### `pricingratiohttp`

| Test | Defect it defends |
|---|---|
| `TestHandler_UnauthenticatedIs401` | If auth check is skipped, unauthenticated callers can read pricing catalog. |
| `TestHandler_HiddenRatioOmitsMultiplierField` | If `public_ratio=false` is ignored, the markup multiplier leaks to end-users. |
| `TestHandler_VisibleRatioIncludesMultiplierField` | If `public_ratio=true` is treated as false, admin-configured transparency is silently suppressed. |
| `TestHandler_BackendErrorIs503` | If backend error produces 200 with empty list, client cannot distinguish empty catalog from unavailability. |

### `upstreamprice`

| Test | Defect it defends |
|---|---|
| `TestHTTPFetcher_BlocksRedirect` | If redirect is followed, a compromised upstream can exfiltrate context via 3xx chain. |
| `TestHTTPFetcher_OversizedBodyReturnsError` | If body size is uncapped, a malicious upstream can OOM the server. |
| `TestHTTPFetcher_Non2xxReturnsError` | If 404/502 are silently treated as empty, the caller applies a diff against an empty list and removes all prices. |
| `TestDiff_PriceChangeDetected` | If diff compares by model ID only and ignores price fields, changed prices are silently classified as unchanged. |
| `TestDiff_NewModelFlaggedAdded` | If a model present upstream but absent locally is not flagged, admin never sees new model prices. |
| `TestDiff_EmptyUpstreamFlagsAllRemoved` | If an empty upstream response (e.g. HTTP body `{}`) produces a zero-length diff, all local prices would be silently deleted on apply. |
| `TestApplier_DuplicateVersionIsNoOp` | If `ON CONFLICT` is absent, re-applying the same version corrupts the append-only history invariant. |
| `TestApplier_ActorRecorded` | If `fetch_actor` is not written, the audit trail for who applied the preset is lost. |

### `adminhttp` pricing ratio handler

| Test | Defect it defends |
|---|---|
| `TestPricingRatioHandler_NonAdminIs403` | If role check is missing, any authenticated caller can set arbitrary markup ratios. |
| `TestPricingRatioHandler_ZeroRatioIs400` | If zero is accepted, the catalog would silently price all models at $0. |
| `TestPricingRatioHandler_NegativeRatioIs400` | If negative is accepted, effective prices become negative — silently corrupting downstream billing. |
| `TestPricingRatioHandler_InvalidDecimalIs400` | If `shopspring/decimal` parse error is swallowed, a string like `"1.2.3"` would be treated as zero or 1. |

### `adminhttp` upstream handler

| Test | Defect it defends |
|---|---|
| `TestPricingUpstreamFetchHandler_FetchErrorIs502` | If upstream fetch failure returns 200 with empty diff, admin silently applies a blank preset. |
| `TestPricingUpstreamFetchHandler_DoesNotWrite` | If fetch handler writes to DB, a dry-run call accidentally creates a version row. |
| `TestPricingUpstreamApplyHandler_DuplicateVersionIs409` | If idempotency check is missing, re-apply creates duplicate version rows. |
| `TestPricingUpstreamHandler_RawPayloadNotInLogs` | If `raw_payload` is logged at INFO, upstream model price data is leaked to log aggregation (potential IP/commercial leakage). |

---

## Parity-or-better vs reference

| Reference behavior | Reference location | HUAKAI design | Better or parity |
|---|---|---|---|
| Billing snapshot freezes `group_ratio` at pre-consume time | `new-api/pkg/billingexpr/types.go:36` — `GroupRatio` field in `BillingSnapshot` | HUAKAI stores ratio in `pool_group_pricing_ratios`; Phase E (`F-BILL-SNAPSHOT-001`) will freeze it into the claim at Tx1 time. This design provides the ratio source; snapshot freeze is the next gap. | Parity (source) + gap acknowledged |
| Admin-triggered upstream pricing sync with apply | `sub2api backend/internal/service/pricing_service` — `SyncAndCachePrices` syncs from upstream config | HUAKAI splits fetch/diff/apply into separate idempotent steps with an append-only audit table (`upstream_price_presets`). Admin sees the diff before applying. | Better: explicit diff step + append-only audit trail; Sub2API has no diff preview |
| Per-group ratio multiplier applied to base price | `new-api/pkg/billingexpr/settle.go:25` — `applyGroupRatio` multiplies settled quota by group ratio | HUAKAI exposes effective price (base × ratio) in `GET /v1/pricing`; the multiplier field is toggleable via `public_ratio` | Parity on computation; better on operator transparency control |
| Redirect block on HTTP fetcher | `modelsync/http_fetcher.go:58-61` (HUAKAI's own modelsync pattern) | `upstreamprice.HTTPFetcher` copies the exact same `ErrUseLastResponse` redirect block | Parity |
| Body size cap on upstream fetch | `modelsync/http_fetcher.go:273` — `maxModelListBytes = 8 << 20` | `upstreamprice.HTTPFetcher` uses 4 MiB cap (tighter; pricing JSON is smaller than model list) | Better: tighter cap |
| Append-only pricing version history | `billing_pricing_versions` (HUAKAI existing) | `PostgresApplier` only inserts with `ON CONFLICT DO NOTHING`; never updates | Parity (existing invariant preserved) |

---

## Effort

**M** (medium)

Breakdown:
- Migration 0077: 0.5 day
- `pricingcatalog` package (store + service): 1 day
- `pricingratiohttp` handler + tests: 1 day
- `upstreamprice` fetcher + diff + apply + tests: 1.5 days
- `adminhttp` pricing handlers + tests: 1 day
- Integration wiring (chi router mounts, DI): 0.5 day

Total: ~5.5 engineering days.

---

## Risks

| Risk | Severity | Mitigation |
|---|---|---|
| **Upstream source instability**: models.dev or basellm change their JSON schema | Medium | `upstreamprice.HTTPFetcher` returns a typed parse error; apply step is gated on successful parse; admin sees the error before anything is written |
| **Ratio multiplied into settlement before F-BILL-SNAPSHOT-001**: if billing code reads ratio live during settlement, a mid-flight ratio change causes TOCTOU | High | This design explicitly does NOT wire ratio into settlement. The `TODO(F-BILL-SNAPSHOT-001)` comment in `pricingcatalog/catalog.go` documents the freeze requirement. Settlement reads `billing_pricing_versions`, not `pool_group_pricing_ratios`, until snapshot-freeze is built. |
| **Zero ratio slipping through**: if CHECK constraint is not applied (e.g. replica lag), ratio=0 silently zeros prices | High | Application-layer guard in handler (`ratio > 0` via decimal comparison) + DB-level `CHECK (ratio > 0)`. Both must pass. |
| **Migration 0077 runs before 0076 on some envs**: ordering gap | Low | Numbered migrations are sequential; golang-migrate enforces order. No gap between 0076 and 0077. |
| **`upstream_price_presets.raw_payload` grows unbounded**: frequent fetches accumulate large JSONB rows | Medium | Admin list endpoint is paginated; a periodic cleanup job (out of scope for this gap, noted as follow-up) should purge rows older than N days. Column comment notes this risk. |
| **`tenant_operator` reading ratios of other tenants**: misconfigured auth scope | Medium | `parseAdminCatalogPage` (existing adminhttp helper) already enforces tenant scope for `tenant_operator`; ratio handlers reuse this helper. |
