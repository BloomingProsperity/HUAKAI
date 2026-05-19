# N+5 Slice 2 — Model Registry (Claude independent draft)

| Field | Value |
| --- | --- |
| Status | Drafted independently per CLAUDE.md #10 (parallel-draft + cross-discuss) |
| Counterpart | `docs/process/plans/2026-04-30-n5-model-registry-codex.md` (drafted in parallel; not read while authoring this) |
| Lane | specifier (Claude) |
| Driver | Blueprint v0.2 Slice 2 — replace inline-`ResolvedModel` + delete `PlanWithPoolGroupID` escape hatch |
| Predecessor | N+4a (commit `121db58`) — auth pipeline now real; chat handler still threads `pool_group_id` from request body, which is the L0-shaped hack we kill here |
| Migration | 0008 |
| Authority | Synthesis decision lives with Owner; Claude proposes |

---

## What this slice changes (one-paragraph elevator)

Right now the chat-completions handler reads `pool_group_id` straight from the JSON body, hands it to `ClaimGate.Reserve`, to `pool.Selector.Select`, and into `AttemptPlan.PoolGroupID`. The Router exists but exposes a `PlanWithPoolGroupID(...)` escape-hatch method that takes the pool group as an out-of-band argument so the handler doesn't have to lie about going through `Plan(...)`. The result: there is no point in the pipeline that translates a public model alias (e.g. `claude-3-5-sonnet`) into "which pool group serves this for this tenant", and the chat client is required to know our internal `pool_group_id` numbers — which is both (a) wrong for a public API and (b) an undocumented coupling between client and ops topology. **Slice 2 introduces a real `internal/registry` layer that owns the `(tenant, public_alias) → ResolvedModel{candidate pools, capabilities, protocol_family, snapshot_version}` mapping**, lets the Router resolve pool groups via metadata, and removes the body-side `pool_group_id` field along with `PlanWithPoolGroupID`.

---

## Goals (acceptance criteria)

1. `internal/registry.Registry` interface exists with `ResolveModel(ctx, publicAlias, tenantID) → ResolvedModel`.
2. `ResolvedModel.PoolCandidates []int64` populated from registry; Router uses it; `PlanWithPoolGroupID` deleted.
3. Chat handler stops reading `pool_group_id` from request body. The body field is removed; clients send only `model`.
4. New schema migration `0008_model_registry.up.sql` (+ `.down.sql`) defines model + alias + binding tables.
5. Phase C smoke (`backend/cmd/gateway/smoke_test.go`) seeds registry rows so the existing seeded pool group resolves through the alias `claude-3-5-sonnet-20241022` (or similar) — smoke stays green.
6. Disabled-model / unknown-alias paths return distinguishable HTTP codes from the operator's POV (audit log) but a uniform 400/404 to the client (anti-enumeration parity with auth).
7. Integration tests cover happy path, unknown alias, disabled model, tenant-scoped override, snapshot-version stamping into `usage_records`.
8. CMB-1 (no credential reads) + CMB-2 (no decimal cost) + CMB-7 (read-only) all hold; reviewer-lane evidence cited in PR.

---

## Non-goals (explicitly deferred)

- **Pricing per model** — Slice 2 does not write a price table. `pricing_class` stays free-form; F-BILL-001 pricing math is Slice 3+ scope.
- **Capability inheritance / safe-equivalent fallback** — Slice 2 returns *exact* capabilities; "safe equivalent" remapping (e.g. degrade vision-on→vision-off) is Phase E.
- **Admin model issuance API** — Operators seed models via SQL or DB tooling. A real `POST /admin/v1/models` endpoint is later phase.
- **Model deprecation lifecycle / sunset dates** — `enabled` flag only. Deprecation banners / per-tenant sunset emails are out.
- **Cache invalidation via pubsub** — In-process LRU + TTL-based snapshot version is fine for L0/L1; cross-process invalidation rides on `scheduler_outbox` later.
- **Cost-aware routing** — Router still does single-attempt plans. Cross-pool fallback enumeration is Slice 3.

---

## Decision points for Owner

### D1. Schema shape: normalized 3-table or denormalized 1-table?

**Option A (denormalized, 1 table `model_aliases`)**
```
model_aliases(
  id, tenant_id, public_alias, internal_model_id, provider_model_id,
  protocol_family, context_window, capabilities text[],
  pool_candidates bigint[], pricing_class, status, snapshot_version,
  created_at, updated_at, deleted_at
)
```

**Option B (normalized 3 tables: `models` + `model_aliases` + `model_pool_bindings`)**
- `models`: canonical model identities (one row per `internal_model_id`)
- `model_aliases`: tenant-scoped alias → model (many aliases → one model)
- `model_pool_bindings`: ordered (model, pool_group, priority) — the candidate list

**Trade-offs:**

| | A (denorm) | B (norm) |
|---|---|---|
| Read complexity | 1 query, no joins | 1 query with 2 joins |
| Tenant override semantics | Each tenant duplicates the row | Tenant overrides at `model_aliases` only; `models` shared |
| Pool-binding reorder cost | UPDATE one array | UPDATE many rows OR one `priority` field |
| Audit trail | Whole-row diff | Cleaner — only the changed table changes |
| Cache invalidation | Single row hash | Multi-row hash (more invalidation events) |
| Schema migration size | Smaller | Larger |
| Slice 3 cross-pool scoring | Awkward — pool list flat in array | Natural — bindings table grows columns (priority, weight) |

**Claude pick: Option B (normalized).**
Reason: Slice 3 will add per-binding scoring (priority / weight / cost-class) to drive cross-pool fallback. Squashing pool_candidates into a `bigint[]` works for Slice 2 but blocks the obvious Slice 3 evolution. The "1 query becomes 1+joins" cost is irrelevant at our QPS and is well-served by the LRU cache (D2). Audit clarity also tilts B (model rename ≠ pool reordering).

### D2. Cache: in-process LRU now, or no cache at L0?

**Option A**: no cache. Every `ResolveModel` hits Postgres.
**Option B**: in-process LRU, key = `(tenant_id, public_alias)`, TTL = 30s, capacity = 1024.
**Option C**: in-process LRU + invalidation via `scheduler_outbox` (event-driven).

**Claude pick: Option B.**
Reason: Resolve sits on the hot path of every chat request; even a 0.2 ms PG roundtrip × thousands of QPS is meaningful and feeds directly into the sub2api scaling bottleneck list (memory item: `customer-count → latency rise`). 30 s TTL is acceptable because (a) a misconfigured model self-corrects within half a minute, (b) at L0 nobody is reconfiguring on the second, (c) snapshot_version is stamped at *resolve time* into the usage record, so a stale cache hit just records the pre-change snapshot and the next resolve gets the new one. C is the eventual right answer but adds Slice 5/6 plumbing (outbox consumer wiring); deferring is honest.

### D3. Tenant-scoped override: yes or no?

**Option A**: global only. `model_aliases` has no `tenant_id`. Every tenant sees the same alias→model map.
**Option B**: tenant-scoped. `model_aliases.tenant_id NOT NULL`, every tenant owns its own aliases.
**Option C**: tenant-scoped with global fallback. Resolve queries `WHERE tenant_id = $1` first, falls back to `tenant_id = 0` (sentinel) if no match.

**Claude pick: Option C.**
Reason: Operators want to seed a "default" alias surface (`claude-3-5-sonnet` → some pool) for every new tenant without manual per-tenant rows; tenants who *do* want overrides (different pool, different fallback order) need a clean way to express it. A is rigid; B is flat-out wrong because it forces 1000-row seeds on every tenant create. C is the pattern Postgres people reach for and reads as `ORDER BY tenant_id DESC NULLS LAST LIMIT 1` (tenant-specific row wins). Reserved tenant_id = 0 is documented as the "global registry" sentinel in DR-001 update.

### D4. Disabled-model HTTP code

**Option A**: `400 Bad Request` (treat disabled as client misuse).
**Option B**: `404 Not Found` (the alias does not exist for this tenant).
**Option C**: `403 Forbidden` (the alias exists but is not authorized for this tenant).

**Claude pick: B for unknown alias + B for disabled (uniformity).**
Reason: Anti-enumeration parity with the auth layer's "all credential failures → 401". An attacker should not be able to discriminate "model X exists but is disabled for me" vs "model X does not exist" by HTTP code; both return 404 with body `{"error":{"code":"unknown_model","message":"model not available"}}`. The audit log records the truth (`registry_disabled` vs `registry_unknown`).

### D5. Should request body keep `pool_group_id` as an override?

**Option A**: delete the field outright. Clients can no longer choose a pool.
**Option B**: keep it as an authenticated-tenant-only override; if present, bypass registry alias→pool resolution and use the supplied id (still validated as a pool the tenant owns).
**Option C**: gate it behind a per-tenant capability flag (`tenant.allow_route_override = true`) that defaults to false.

**Claude pick: A for L0 (delete), with C planned for Phase E.**
Reason: `pool_group_id` in the request body was always the L0 hack. Real public APIs route via model alias only. Tenants who genuinely need override (e.g. "use this account because the other one is rate-limited") should get a real escape hatch later (header `X-HUAKAI-Pool-Override` + capability flag), but introducing it in Slice 2 means we ship two routing paths and have to test both. Removing the field hard for L0 also hard-stops a sub2api-style bad pattern from creeping in.

### D6. SnapshotVersion derivation

**Option A**: a single string column on `models` (operator bumps it manually).
**Option B**: derived hash of `(model_row, alias_row, binding_rows)` computed at resolve time.
**Option C**: monotonic per-tenant counter, `tenants.registry_version` incremented by trigger on any model/alias/binding row change.

**Claude pick: A for L0; C planned for Slice 3.**
Reason: At L0 we want `RoutePlan.SnapshotVersion` to be *stable enough* to write into `usage_records` and *visible enough* for an operator to see "what config was active at billing time". An operator-bumped string ("2026-04-30-v1") is honest and human-readable. B is correct but overengineered for L0. C is the right answer for future-proofing because Slice 3's audit replay needs deterministic ordering, but a trigger that counts every binding-table tweak is non-trivial — defer.

### D7. Sequencing — single PR or split?

**Option A**: single PR (schema + registry + chat handler + delete escape hatch + smoke seeding).
**Option B**: split N+5a (schema + registry, escape hatch still present) → N+5b (delete escape hatch + chat handler rewrite).

**Claude pick: Option B.**
Reason: N+4 splitting (auth replacement vs ledger backfill) was correct because the two sub-tasks had different blast radii. Same logic here: schema + registry is additive (existing code keeps working with the escape hatch); deleting `PlanWithPoolGroupID` and rewiring chat handler is the breaking change. Splitting gives us a clean rollback target — if smoke breaks at N+5b, revert one commit, not five. This also lets a Codex round-trip catch issues in N+5a's schema before we commit to the rewire.

### D8. Should `models` carry `provider_id` FK?

Currently `provider_accounts` lives under a `channel` under a `pool_group`, and `provider_id` lives on `provider_accounts`. If `models.provider_id NOT NULL`, we constrain "this canonical model is served by exactly one provider" — which is wrong for `gpt-4` (servable by both OpenAI and Azure OpenAI).

**Claude pick: do NOT add `models.provider_id`.**
Reason: a model is a *capability surface*; the *provider that serves it* is implicit in `model_pool_bindings → pool_groups → channels → provider_accounts.provider_id`. Forcing a single provider on `models` blocks the multi-provider case. Anti-pattern import from sub2api/portkey where "model belongs to provider" is hard-coded.

### D9. Are aliases case-sensitive?

**Option A**: case-sensitive (`Claude-3-5-Sonnet` ≠ `claude-3-5-sonnet`).
**Option B**: case-insensitive — store `lower(public_alias)` with unique index.

**Claude pick: B (case-insensitive).**
Reason: Anthropic / OpenAI public-facing model strings are by convention all-lowercase, but real-world clients send mixed-case strings. Treating them as different aliases is a footgun that produces an "unknown_model" error for a request that *should* work. Cost: one `lower()` in the index. (Audit logs preserve the as-sent casing.)

### D10. Should we support model aliasing chains?

A chain is `claude-latest` → `claude-3-5-sonnet-20241022`. Two interpretations:

**Option A**: aliases resolve to a canonical `internal_model_id` directly; no chains. `claude-latest` is a separate alias row pointing to the same `internal_model_id` as `claude-3-5-sonnet-20241022`.
**Option B**: `model_aliases.points_to_alias text` — alias-of-alias chain, walked at resolve.

**Claude pick: A (no chains).**
Reason: Chains are O(N) walks at resolve time and are the kind of feature that grows a "max chain depth" config + cycle detection. The flat model — multiple aliases pointing at the same canonical model — is sufficient and trivially indexable. If "I want to swap claude-latest's target without recreating bindings" becomes a real ops need, we add a dedicated `latest_pointers` table later.

---

## Schema (proposed for D1=B)

```sql
-- 0008_model_registry.up.sql

-- Canonical model identities. Provider-agnostic: a model can be served by N pools.
CREATE TABLE IF NOT EXISTS models (
    id                    bigserial PRIMARY KEY,
    internal_model_id     text        NOT NULL UNIQUE,
    -- e.g. 'anthropic/claude-3.5-sonnet-20241022'
    protocol_family       text        NOT NULL CHECK (protocol_family IN
                            ('anthropic_messages','openai_chat','openai_responses','gemini')),
    provider_model_id     text        NOT NULL,
    -- e.g. 'claude-3-5-sonnet-20241022' as upstream wants it
    context_window        integer     NOT NULL CHECK (context_window > 0),
    capabilities          text[]      NOT NULL DEFAULT ARRAY[]::text[],
    pricing_class         text        NOT NULL DEFAULT 'standard',
    snapshot_version      text        NOT NULL DEFAULT '2026-04-30-v1',
    enabled               boolean     NOT NULL DEFAULT true,
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now(),
    deleted_at            timestamptz
);
COMMENT ON TABLE models IS 'Slice 2: canonical model identities. internal_model_id is provider-agnostic; provider_model_id is what the upstream API expects.';

-- Tenant-scoped alias map. tenant_id = 0 is the "global registry" sentinel (D3=C).
CREATE TABLE IF NOT EXISTS model_aliases (
    id                    bigserial PRIMARY KEY,
    tenant_id             bigint      NOT NULL,    -- 0 = global; otherwise FK semantics enforced in app layer
    public_alias_lower    text        NOT NULL,    -- D9: lower-cased
    public_alias_display  text        NOT NULL,    -- as-seeded casing (audit-friendly)
    model_id              bigint      NOT NULL REFERENCES models(id),
    enabled               boolean     NOT NULL DEFAULT true,
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now(),
    deleted_at            timestamptz
);
CREATE UNIQUE INDEX uq_model_aliases_tenant_alias
    ON model_aliases (tenant_id, public_alias_lower)
    WHERE deleted_at IS NULL;
CREATE INDEX idx_model_aliases_lookup
    ON model_aliases (public_alias_lower, tenant_id)
    WHERE deleted_at IS NULL AND enabled = true;
COMMENT ON TABLE model_aliases IS 'Slice 2: (tenant, public_alias) -> model. tenant_id=0 is global sentinel; tenant-specific row wins on resolve.';

-- Ordered binding: which pool groups serve this model, in what priority order.
CREATE TABLE IF NOT EXISTS model_pool_bindings (
    id                    bigserial PRIMARY KEY,
    tenant_id             bigint      NOT NULL,    -- 0 = global; matches model_aliases.tenant_id
    model_id              bigint      NOT NULL REFERENCES models(id),
    pool_group_id         bigint      NOT NULL REFERENCES pool_groups(id),
    priority              integer     NOT NULL DEFAULT 100,
    -- lower priority = tried first; matches existing routes.match_priority semantics
    enabled               boolean     NOT NULL DEFAULT true,
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now(),
    deleted_at            timestamptz
);
CREATE UNIQUE INDEX uq_model_pool_bindings_tenant_model_pool
    ON model_pool_bindings (tenant_id, model_id, pool_group_id)
    WHERE deleted_at IS NULL;
CREATE INDEX idx_model_pool_bindings_resolve
    ON model_pool_bindings (tenant_id, model_id, priority, enabled)
    WHERE deleted_at IS NULL;
COMMENT ON TABLE model_pool_bindings IS 'Slice 2: ordered (model -> pool_group) binding. Slice 3 adds weight / cost_class columns.';
```

Down migration drops the three tables in reverse order. No data backfill required because no one was reading these tables before.

### Default registry seeding strategy

A `seed.sql` lives under `backend/sql/seed/` and runs idempotently. For Phase C smoke we seed at least:

```sql
INSERT INTO models (internal_model_id, protocol_family, provider_model_id, context_window, capabilities)
VALUES ('anthropic/claude-3.5-sonnet-20241022', 'anthropic_messages', 'claude-3-5-sonnet-20241022', 200000, ARRAY['stream','tools','vision']);

INSERT INTO model_aliases (tenant_id, public_alias_lower, public_alias_display, model_id)
VALUES (0, 'claude-3-5-sonnet-20241022', 'claude-3-5-sonnet-20241022',
        (SELECT id FROM models WHERE internal_model_id = 'anthropic/claude-3.5-sonnet-20241022'));
```

The smoke test seeds a `model_pool_bindings` row mapping the seeded pool to the seeded model, so the chat post `{"model":"claude-3-5-sonnet-20241022"}` resolves end-to-end.

---

## Code structure

```
backend/internal/registry/
    registry.go          (interface + ResolvedModel re-export from router)
    postgres_registry.go (PostgresRegistry impl)
    cache.go             (LRU + TTL — D2=B)
    errors.go            (ErrUnknownModel, ErrModelDisabled, ErrRegistryBackend)
    registry_test.go     (unit: cache eviction, casing, sentinel fallback)
    postgres_registry_integration_test.go
                         (integration: happy / unknown / disabled / tenant-override)
backend/sql/queries/
    registry.sql         (sqlc: ResolveModelByAlias, ListPoolBindingsForModel)
backend/sql/migrations/
    0008_model_registry.up.sql
    0008_model_registry.down.sql
backend/sql/seed/
    registry_default.sql (Phase C smoke seed)
```

### Registry interface

```go
package registry

import (
    "context"

    "github.com/BloomingProsperity/HUAKAI/internal/router"
)

type Registry interface {
    ResolveModel(ctx context.Context, publicAlias string, tenantID int64) (router.ResolvedModel, error)
}
```

### Resolve flow (PostgresRegistry)

1. Lower-case `publicAlias` (D9).
2. LRU lookup keyed `(tenantID, alias)` — return cached `ResolvedModel` if not expired.
3. Single SQL query:
   ```sql
   SELECT m.id, m.internal_model_id, m.provider_model_id, m.protocol_family,
          m.context_window, m.capabilities, m.pricing_class, m.snapshot_version,
          ma.enabled AS alias_enabled, m.enabled AS model_enabled,
          coalesce(array_agg(mpb.pool_group_id ORDER BY mpb.priority, mpb.id)
                       FILTER (WHERE mpb.enabled = true AND mpb.deleted_at IS NULL),
                   ARRAY[]::bigint[]) AS pool_candidates
   FROM model_aliases ma
   JOIN models m ON m.id = ma.model_id AND m.deleted_at IS NULL
   LEFT JOIN model_pool_bindings mpb
        ON mpb.model_id = m.id AND mpb.tenant_id = ma.tenant_id
   WHERE ma.public_alias_lower = $1
     AND ma.tenant_id IN ($2, 0)
     AND ma.deleted_at IS NULL
   GROUP BY m.id, ma.id
   ORDER BY ma.tenant_id DESC NULLS LAST   -- tenant-specific wins over global
   LIMIT 1;
   ```
4. Map `alias_enabled / model_enabled` → `ErrModelDisabled` (or success).
5. Empty result → `ErrUnknownModel`.
6. `len(pool_candidates) == 0` → `ErrUnknownModel` from a runtime POV (we have an alias but nowhere to send), with separate audit log entry `registry_no_bindings`.
7. Build `router.ResolvedModel{...}`, populate cache, return.

### Adding `PoolCandidates` to `router.ResolvedModel`

```go
// ResolvedModel — additions for Slice 2:
type ResolvedModel struct {
    // ... existing fields ...
    PoolCandidates []int64 // ordered: index 0 is primary, 1+ are fallback (Slice 3)
}
```

### Router.Plan changes

```go
// requestPoolGroupID resolves the pool group from PlanInput.
func requestPoolGroupID(req PlanInput) int64 {
    if len(req.Model.PoolCandidates) > 0 {
        return req.Model.PoolCandidates[0]
    }
    return req.ExplicitPoolGroupID  // legacy escape; removed in N+5b
}
```

After N+5b: delete the `req.ExplicitPoolGroupID` branch, delete `PlanWithPoolGroupID`, delete `errPoolGroupRequired`, delete `req.ExplicitPoolGroupID` field from `PlanInput`.

### Chat handler rewrite (N+5b)

```go
// after auth.Resolve
resolved, err := d.Registry.ResolveModel(ctx, req.Model, ident.TenantID)
switch {
case errors.Is(err, registry.ErrUnknownModel),
     errors.Is(err, registry.ErrModelDisabled):
    writeJSONError(w, http.StatusNotFound, "unknown_model", "model not available")
    return
case errors.Is(err, registry.ErrRegistryBackend):
    writeJSONError(w, http.StatusServiceUnavailable, "registry_backend_error", "registry transient failure")
    return
case err != nil:
    writeJSONError(w, http.StatusInternalServerError, "registry_unknown_error", err.Error())
    return
}

// Plan
plan, err := d.Router.Plan(ctx, router.PlanInput{
    Context: router.RequestContext{ /* ... */ },
    Model:   resolved,
    Features: featuresFromRequest(req),
})
// PoolGroupID now comes from plan.Attempts[0].PoolGroupID, derived from
// resolved.PoolCandidates[0] inside Router.
```

The body-side `pool_group_id` field is deleted from `chatRequest`.

`ChatHandlerDeps` gains `Registry registry.Registry` (interface for testability).

`cmd/gateway/main.go` constructs `registry.NewPostgresRegistry(q, registry.WithCache(...))` and threads it into deps.

---

## Smoke test changes (`backend/cmd/gateway/smoke_test.go`)

1. `seedSmokeGraph` adds three INSERTs after `pool_groups` is seeded:
   - `models` row (anthropic/claude-3.5-sonnet-20241022).
   - `model_aliases` row (tenant_id=0, alias='claude-3-5-sonnet-20241022').
   - `model_pool_bindings` row pointing the seeded model at the seeded pool group.
2. Smoke client request body changes from
   ```json
   {"model":"claude-3-5-sonnet-20241022","stream":true,"pool_group_id":42,"messages":[...]}
   ```
   to
   ```json
   {"model":"claude-3-5-sonnet-20241022","stream":true,"messages":[...]}
   ```
3. Cleanup adds `DELETE FROM model_pool_bindings; DELETE FROM model_aliases; DELETE FROM models;` before the existing pool-cascade.
4. PG-state assertions stay the same (Settler-side rows). Add one new assertion: `usage_records.snapshot_version = '2026-04-30-v1'` (the seeded snapshot).

---

## Risk matrix

| Risk | Trigger | Detection | Mitigation |
|---|---|---|---|
| Two aliases map to same `internal_model_id` (intentional via D10=A) | Seed accident | Operator dashboard shows duplicate routes for one model | OK by design — no mitigation needed |
| Tenant has no `model_aliases` row + global sentinel missing the alias | Fresh tenant + un-seeded global | `ErrUnknownModel` 404 to client; audit row `registry_no_alias` | Document the seed-on-tenant-create runbook; add Phase E admin endpoint |
| Registry table empty at boot | Migration ran but seed didn't | EVERY chat request 404s; smoke fails | Boot-time health check rejects empty `models` count when `cfg.Env != "test"` |
| Snapshot version mismatch between two concurrent requests | Operator updates `models.snapshot_version` mid-traffic | Two adjacent `usage_records` rows have different `snapshot_version` strings | Acceptable — that's audit-correct behavior. Document in OPS runbook |
| Cache hit returns stale `pool_candidates` after pool reorder | LRU TTL not yet expired | `usage_records.pool_group_id` doesn't match latest binding row | TTL bound (30 s); Slice 5 wires `scheduler_outbox` for invalidation |
| `tenant_id=0` sentinel collision with a real tenant | Another part of the codebase creates tenant id 0 | FK / unique constraint conflict on first attempt; loud error | Insert a guard row `INSERT INTO tenants (id, name) VALUES (0, '_global_registry') ON CONFLICT DO NOTHING` in 0008.up.sql |
| Bcrypt-style fanout DOS via empty `pool_candidates` | Attacker probes valid alias with no bindings | Wasted DB roundtrip per attempt | Bound: alias resolve → 404 immediately; no per-attempt cost amplification |
| Multiple pool candidates ordered wrong | Operator forgets to set priority | Slice 3 fallback picks the wrong primary | Default priority=100; bindings without explicit priority warn-log on first use |

---

## CMB compliance check

- **CMB-1 (Router does not read credentials)**: Registry returns `ResolvedModel` containing pool_group_ids and capability strings only. No credentials field. No outbound network call. ✅
- **CMB-2 (Pool does not compute cost)**: Untouched; Registry contributes `pricing_class` (string tag), but pricing math still lives in Ledger Phase E. ✅
- **CMB-3, 4** (adapter / ledger boundaries): Unchanged; Slice 2 doesn't touch adapter or settler. ✅
- **CMB-5 (Credentials never logged)**: Registry has no credential field. Audit log records alias only. ✅
- **CMB-6 (request_id / attempt_id present)**: Unchanged. Slice 2 adds `snapshot_version` stamping into existing `usage_records` row but doesn't add new ID columns (Slice 3 work). ✅
- **CMB-7 (Layer write-discipline)**: Registry is read-only — no INSERT / UPDATE / DELETE in `internal/registry`. Seed data goes via SQL migration, not via registry code. ✅

---

## Test plan

### Unit (no DB)
- `TestRegistry_LowerCasesAlias` — `ResolveModel(ctx, "Claude-3-5", 1)` and `ResolveModel(ctx, "claude-3-5", 1)` hit same cache key.
- `TestRegistry_CacheEviction` — fill LRU past capacity, oldest evicted.
- `TestRegistry_TTLExpiry` — sleep past TTL, second call re-queries.

### Integration (real PG, table-driven)
- `HappyPath` — seeded alias → ResolvedModel populated, `PoolCandidates[0]` matches binding.
- `UnknownAlias` — alias not in registry → `ErrUnknownModel`.
- `DisabledAlias` — `model_aliases.enabled=false` → `ErrModelDisabled`.
- `DisabledModel` — `models.enabled=false`, alias active → `ErrModelDisabled`.
- `TenantOverridesGlobal` — same alias has tenant=0 + tenant=1 rows pointing at different models; tenant 1 resolve returns tenant-1 row.
- `OnlyGlobalAvailable` — alias only in tenant=0; tenant 1 resolve returns global.
- `NoBindings` — alias resolves but `model_pool_bindings` empty → `ErrUnknownModel` (operator-runtime perspective: we can't route it).
- `MultipleBindingsOrdered` — three bindings priorities 100/50/200; `PoolCandidates = [50_id, 100_id, 200_id]`.
- `SoftDeletedAliasInvisible` — `deleted_at NOT NULL` → `ErrUnknownModel`.
- `SnapshotVersionStamped` — resolve writes `snapshot_version` into `RoutePlan`; chat handler integration test verifies it lands on `usage_records`.

### End-to-end smoke
- `TestSmokeChatCompletions` — body without `pool_group_id`, model alias resolves, money path PG state assertions all pass.

---

## Sequencing (D7=B split)

### N+5a — registry layer + schema, escape hatch still present
- Migration 0008 (up + down).
- Seed script.
- `internal/registry` package + tests.
- `cmd/gateway/main.go` constructs Registry and threads into deps; no caller uses it yet.
- `PlanInput.Model.PoolCandidates` field added to router; populated by Registry but not yet used by `requestPoolGroupID`.
- Commit. Codex review pass. Smoke still green (handler still uses body `pool_group_id`).

### N+5b — delete escape hatch + chat handler rewrite
- Chat handler resolves via Registry, drops `chatRequest.PoolGroupID`.
- Router uses `req.Model.PoolCandidates[0]`; deletes `PlanWithPoolGroupID`, `errPoolGroupRequired`, `PlanInput.ExplicitPoolGroupID`.
- Smoke test seeds registry + drops body field.
- All integration tests updated.
- Commit. Codex review pass. Smoke green.

### Why the split

If N+5a's schema design is wrong (Codex catches in review), only one revert. If N+5b's handler rewrite breaks smoke, again one revert; N+5a's cache + tests are still useful work. Same logic as N+4a/b.

---

## Time + blast radius

| Phase | Estimated work | Blast |
|---|---|---|
| N+5a | ~3 hours | Schema + new package; existing path untouched |
| N+5b | ~2 hours | Chat handler rewrite + escape hatch deletion; narrow but breaking |

Owner gate between a and b. Codex parallel discussion at start (this round) and at synthesis decisions inside b (model wire format).

---

## What I'd ask Codex in synthesis

1. Is the `tenant_id=0` global sentinel a sound pattern, or should we have an explicit `models.is_global` flag instead? (Postgres-style vs application-style.)
2. Is the JOIN-with-COALESCE-array_agg query competitive with two roundtrips at L1 scale?
3. Should Registry vs Auth share the same backend-error class (`ErrAuthBackend`-style) or stay independent? Affects HTTP code mapping consistency.
4. Should the `models` table have `provider_model_id` at all, or should it move to `model_pool_bindings` so two pools serving the same model can use different upstream-id strings?

These are the spots where I'm not sure my pick is the right one. I want them surfaced to Owner side-by-side with Codex's choices.

---

Source files read: backend/internal/router/route_plan.go, backend/internal/router/default_router.go, backend/internal/gatewayhttp/chat_completions_handler.go, backend/sql/migrations/0001_pool_routing.up.sql, backend/sql/migrations/0007_l0_inbound_auth.up.sql, docs/specs/_invariants/cross-module-boundaries.md, docs/process/plans/2026-04-30-n4-l0-minimum.md.
Lane: specifier
Agent: Claude (claude-opus-4-7)
UTC timestamp: 2026-04-30T08:55:00Z
