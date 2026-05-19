# N+5 Slice 2 — Model Registry — Claude Round-2 Plan

| Field | Value |
| --- | --- |
| Status | Round-2 refinement; integrates reference-project (借鉴项目) feature-implementation patterns on top of round-1 |
| Lane | implementer (round-2 reads MIT-tier reference behavior-source + already-mined ledger evidence; no fresh non-MIT source reads) |
| Round-1 inputs | [-claude.md](2026-04-30-n5-model-registry-claude.md), [-codex.md](2026-04-30-n5-model-registry-codex.md) |
| Counterpart | `docs/process/plans/2026-04-30-n5-model-registry-codex-v2.md` (drafted in parallel; not read while authoring this) |
| Reference evidence base | `docs/07_REFERENCE_EVIDENCE_LEDGER.md` rows: E-OAI-001..015 (one-api MIT), E-LM-001..006 (LiteLLM MIT), E-PK-001..008 (Portkey MIT), E-NAI-001..008 (New API AGPL — evidence-only), E-S2A-001..008 (Sub2API LGPL — evidence-only), E-AAH-001..006 (All API Hub AGPL — evidence-only), E-HLC-001..007 (Helicone GPL — evidence-only), E-EAG-001..N (Envoy AI Gateway Apache-2.0 — partial in ledger) |
| Clean-room declaration | This author drew on (a) already-mined evidence rows (independently behavior-summarized, ledger-cleaned); (b) public design pattern knowledge of MIT references at training time, paraphrased and behavior-only. No verbatim names/schema/code/comments adopted. |

---

## Why round 2 exists

Round 1 produced two independent plans grounded purely in HUAKAI's own contracts (CMB invariants, blueprint v0.2). Owner correctly noted that *the entire fusion thesis* depends on whether HUAKAI's Slice-2 design absorbs feature obligations the 8 reference projects already validated in production. Round 2 re-arbitrates the round-1 conflicts using reference evidence and surfaces decision points the round-1 plans missed.

The driver question for round 2: **for each round-1 design choice, what do the reference projects do, and does that shift the right answer?**

---

## What the round-1 conflicts look like in the reference world

### D2 Cache (Claude=LRU+30s vs Codex=no-cache)

| Reference | Pattern (paraphrased, MIT-source-derived OR evidence-row) | License tier |
|---|---|---|
| one-api (MIT) | Maintains an in-memory `(group, model) → channel-list` lookup table refreshed periodically; a config change increments a watermark and triggers reload | MIT |
| LiteLLM (MIT) | Router holds the model_list + alias_map in process memory; rate-limit state (rpm/tpm) tracked in an in-memory token bucket per deployment | MIT |
| Portkey (MIT) | Configs are JSON objects loaded into process memory; per-virtual-key config attached at request entry, not re-fetched per call | MIT |
| Envoy AI Gateway (Apache-2.0) | CRDs reconcile to in-memory routing tables; routing decisions never re-read the apiserver per request | Apache-2.0 |
| Helicone (E-HLC-001) | Routing decision uses real-time observed latency/cost/reliability — implies in-memory aggregation | GPL evidence |
| Sub2API (E-S2A-* sticky) | Sticky session pinning requires in-memory session→account map | LGPL evidence |

**Reference verdict**: 4/4 MIT references have an in-memory routing table; none re-read DB per request. **No reference project ships "no cache" as the default.**

**Round-2 pick: Claude was right. Cache YES.** Refinement: not a per-resolve LRU but a **per-process snapshot reload**, keyed by `(tenant, registry_snapshot_version)`. Resolve becomes a hashmap lookup; staleness bounded by `min(ttl_seconds, version_changed_event_lag)`.

### D3 Tenant scoping (Claude=tenant+global-sentinel vs Codex=tenant-only)

| Reference | Pattern | Tier |
|---|---|---|
| one-api | Group abstraction (tenant analog) — channel exposure is per-group; no global "default catalog" auto-applies | MIT |
| LiteLLM | Single config per process; multi-tenant via virtual keys, each virtual key references a specific config_id; no fallback to global default | MIT |
| Portkey | Each virtual key carries its own config; no global config sentinel | MIT |
| Envoy AI Gateway | LLMRoute is namespaced by Kubernetes namespace; no cross-namespace fallback | Apache-2.0 |

**Reference verdict**: 4/4 MIT references **explicitly avoid a global fallback sentinel**. They expect operators to seed per-tenant (or per-namespace) configuration explicitly. The "tenant_id=0 sentinel" Claude proposed is a HUAKAI invention with no upstream precedent.

**Round-2 pick: Codex was right. Tenant-only.** Drop the `tenant_id=0` sentinel. Add an admin tooling note for "seed-on-tenant-create" workflow (Phase E admin endpoint).

### D4 HTTP code (Claude=404 unified vs Codex=403/404 actionable)

| Reference | Behavior |
|---|---|
| one-api | Returns specific error codes (channel disabled / model not configured / no available channel); operator-actionable | MIT |
| LiteLLM | 400 with structured error containing model name + reason | MIT |
| Portkey | 400/422 with structured error.code | MIT |
| Anti-enumeration parity check | Auth uses 401 unified — but auth keys ARE secrets. Model aliases are PUBLIC strings (Anthropic / OpenAI publish them) | n/a |

**Reference verdict**: All 3 MIT gateways return actionable errors for model-resolution failures. The "anti-enumeration" Claude was guarding against doesn't apply — model aliases aren't secrets.

**Round-2 pick: Codex was right. 403 disabled / 404 unknown / 503 backend.** Concession to anti-enum: keep the response BODY uniform (`{"error":{"code":"unknown_model","message":"model not available"}}`), discriminate via HTTP code only.

### D6 SnapshotVersion shape (Claude=string column vs Codex=separate snapshots table with int)

| Reference | Behavior |
|---|---|
| one-api | Channel/group config has implicit version via `updated_at`; no explicit version counter | MIT |
| LiteLLM | No DB-level version; in-memory config has process start time | MIT |
| Portkey | Configs are versioned objects with explicit version number on the config record | MIT |
| Envoy AI Gateway (Kubernetes-native) | CRD `metadata.resourceVersion` increments on every change | Apache-2.0 |

**Reference verdict**: Mixed — one-api implicit, LiteLLM none, Portkey + Envoy both have explicit versioning. The latter two scale better to audit replay (which is what HUAKAI claims `RoutePlan.SnapshotVersion` exists for).

**Round-2 pick: Codex was closer.** Snapshot table with int counter wins over Claude's "operator string". Refinement: store the counter on a per-tenant row in `model_registry_snapshots` AND derive the string format `"reg:<tenant_id>:<counter>;router:<router_policy_version>"` as the field that lands in `usage_records.snapshot_version`. This way the audit replay can re-construct registry state at billing time deterministically.

---

## Behavior obligations the round-1 plans missed

### From one-api (MIT, source-knowledge + E-OAI-001..015)

**O1. Per-channel/binding model rename.** A channel can publish a public model alias `gpt-4` but call upstream with `gpt-4-turbo-preview`. This is a per-binding rename, not a per-model rename.
- **HUAKAI absorption**: add `model_pool_bindings.provider_model_id_override text NULL`. When NULL, `models.provider_model_id` applies. When set, this binding overrides for upstream call.
- **Why this matters**: a tenant can route the same public alias to two pools where one pool's accounts speak `claude-3-5-sonnet-20241022` and another pool's accounts speak `claude-3-5-sonnet-latest`. Without per-binding rename, those become two separate model rows.

**O2. (group, model) → ordered channel list as the primary lookup.**
- one-api's `Ability` table is essentially a precomputed materialized view of the join we're proposing: `(group, model) → [channel_id ordered by priority]`. They keep it precomputed for sub-millisecond lookup.
- **HUAKAI absorption**: leave the JOIN at resolve time for L0; document the materialized-view migration path at L2 (≥10K tenant scale). Add a TODO marker in the registry sqlc query header.

**O3. Auto-disable on success-rate threshold (E-OAI-009, E-OAI-013).**
- Auto-disable lives in the POOL layer, not registry — registry stays read-only metadata per CMB-7. But registry should expose a stable `binding_id` so pool-layer health-state can be cited per binding.
- **HUAKAI absorption**: the `model_pool_bindings.id` returned in `ResolvedModel.PoolCandidates` becomes the join key for future health-state surfaces. Schema unchanged at Slice 2; future `pool_binding_health` table joins on `binding_id`.

**O4. Bulk channel creation (E-OAI-008).**
- Operators expect to create N similar bindings at once (e.g. "all OpenAI accounts get `gpt-4` alias").
- **HUAKAI absorption**: defer to Phase E admin API; not Slice 2 schema concern but design-of-API note.

**O5. Channel `models` field as CSV.**
- one-api stores per-channel supported model list as a CSV string, not a normalized join. This is a known-bad pattern (no FK validation, no efficient lookup); HUAKAI's normalized 5-table is correct, but should NOT carry the CSV anti-pattern over.
- **HUAKAI rejection**: explicitly reject CSV-of-model-names on `provider_accounts`; use `model_pool_bindings` as the only resolver.

### From LiteLLM (MIT, source-knowledge + E-LM-001..006)

**L1. Model-level fallback chain.**
- LiteLLM `fallbacks` is a per-model ordered list of OTHER models to try on failure. Distinct from cross-pool fallback; this is "if `claude-3-5-sonnet` is unavailable as a model, try `claude-3-haiku`".
- **HUAKAI absorption**: add `model_fallback_chains` table (model_id → fallback_alias text, rank int). NOT Slice 2 (Slice 5 / Phase E) since we still don't have an Executor loop. **But schema 0008 reserves the table** to avoid churn.

**L2. context_window_fallbacks (a special-case fallback).**
- When request exceeds primary's context window, fallback to a model with a bigger window. Distinct from generic fallback because the trigger is request-side, not response-side.
- **HUAKAI absorption**: registry exposes `context_window` per model already (Claude/Codex round-1 both have it). The handler logic is Phase E — Slice 2 just needs `context_window NOT NULL`.

**L3. Per-deployment rpm/tpm caps.**
- LiteLLM tracks rpm (requests/min) and tpm (tokens/min) per deployment. These are CONTRACT-level promises (model says it serves at most N rpm) not pool-level slot caps.
- **HUAKAI absorption**: add `model_pool_bindings.rpm_cap int NULL` + `tpm_cap int NULL`. Slice 2 only stores them; enforcement is Phase E (Router check after Plan, before Pool.Claim).
- **CMB-2 check**: rpm/tpm are integer counts, not decimal cost. Pool still doesn't compute cost. Safe.

**L4. Routing strategies (LiteLLM has `simple-shuffle` / `latency-based` / `usage-based-routing`).**
- HUAKAI Slice 2 uses strict priority ordering. Future routing strategies are a Router concern not Registry concern.
- **HUAKAI absorption**: registry returns `PoolCandidates []int64` (ordered); Router decides what to do with the list. Slice 2 ships strict-priority semantics; new strategies are Router-internal in Slice 5+. Schema unchanged.

**L5. Virtual key as config carrier (E-LM-003).**
- LiteLLM virtual keys carry per-tenant config (logging, guardrails, caching). HUAKAI's API keys are "credential only" for now; carrying config on the key is Phase E.
- **HUAKAI absorption**: not Slice 2. Note recorded in N+6 / Phase E roadmap.

### From Portkey (MIT, source-knowledge + E-PK-001..008)

**P1. Targets array with weights.**
- Portkey's config has `targets` array; each target has `weight` (for load-balance) + provider-call override params. Strategy mode `loadbalance` distributes by weight; `fallback` uses targets in order.
- **HUAKAI absorption**: add `model_pool_bindings.weight integer DEFAULT 1` to schema 0008. Slice 2 ignores it (priority-ordered). Slice 5 implements `weighted_loadbalance` strategy without schema migration.

**P2. Conditional fallback on status codes (E-PK-002).**
- "Fallback if 429 or 5xx but not on 401". This is Router concern not Registry. But registry should expose enough metadata for Router to know which alternative pools exist.
- **HUAKAI absorption**: already covered by `PoolCandidates []int64`. No schema change.

**P3. Output guardrails / response cache (E-PK-003, E-PK-005).**
- Guardrails and caching are tenant-config, not registry-config. Phase E concern.
- **HUAKAI absorption**: not Slice 2.

**P4. Per-request timeout (E-PK-007).**
- Per-model timeout is a contract-level promise.
- **HUAKAI absorption**: add `models.timeout_ms_default int NOT NULL DEFAULT 60000`. Used by Phase E forwarder; Slice 2 only stores.

### From Envoy AI Gateway (Apache-2.0, source-knowledge + E-EAG-* if any)

**E1. Declarative routing config.**
- LLMRoute CRD declaratively maps host/path/model → backend. Reconcile loop populates the routing data plane.
- **HUAKAI absorption**: HUAKAI's DB rows ARE the declarative spec. Slice 2 doesn't need new abstraction; documentation note that registry-tables-are-the-CRDs.

**E2. Backend metadata separate from routing.**
- LLMBackend defines provider config (auth, model-mapping); LLMRoute references LLMBackend. Two-tier separation.
- **HUAKAI absorption**: HUAKAI's three-layer (Registry → Router → Pool) is the same separation. Slice 2 already aligns.

### From sub2api (LGPL — evidence-only, E-S2A-001..008)

**S1. Sticky session via session-id (E-S2A-002).**
- Sticky lives in pool layer (existing `sticky_bindings` table from 0001 migration). Registry doesn't touch.
- **HUAKAI absorption**: no Slice 2 change.

**S2. Per-User × per-Account concurrency caps (E-S2A-004).**
- Concurrency caps live in pool/billing, not registry. But registry could provide a per-model-per-tenant rate-limit cap (different from per-account).
- **HUAKAI absorption**: align with L3 above (per-binding rpm/tpm).

**S3. Edition flag toggling SaaS features (E-S2A-005).**
- Not registry concern. Defer.

### From New API (AGPL — evidence-only, E-NAI-001..008)

**N1. Cache-aware billing (E-NAI-001).**
- Pricing concern, not registry. `models.pricing_class` tag is sufficient placeholder; pricing math is Phase E.
- **HUAKAI absorption**: no Slice 2 change.

**N2. Cross-format protocol translation (E-NAI-003).**
- `models.protocol_family` already covers this in round-1. Confirm Slice 2 schema includes it.

**N3. Reasoning-effort parameter (E-NAI-004).**
- Future capability flag. Add `'reasoning'` to the capabilities enum but no enforcement until Phase E.

**N4. Per-User × Per-Model rate limit (E-NAI-006).**
- This is per-tenant-per-model, distinct from per-binding rpm. Different table / different enforcement layer.
- **HUAKAI absorption**: defer; track as Phase E roadmap. Slice 2's per-binding rpm/tpm covers the per-model contract side; per-user enforcement is rate-limit subsystem (F-RATE-001).

### From All API Hub (AGPL — evidence-only, E-AAH-001..006)

UI/operator dashboard concerns; no Slice 2 schema impact. Confirms operator wants "see all my models" surface — Phase E admin API.

### From Helicone (GPL — evidence-only, E-HLC-001..007)

**H1. Latency/cost/reliability-aware routing (E-HLC-001).**
- Health-aware routing input lives in pool layer (cooling, health_state). Registry stays metadata.
- **HUAKAI absorption**: no Slice 2 schema change. Roadmap note.

**H2. Per-endpoint declarative routing strategies in YAML or UI (E-HLC-006).**
- HUAKAI's DB rows ARE the declarative spec. Same point as E1.

**H3. Multi-scope rate + cost limits (E-HLC-004): global / team / per-user.**
- "team" maps to tenant; "global" is operator-wide. Slice 2 per-binding rpm/tpm is a piece of this; full multi-scope is rate-limit subsystem.

---

## New decision points uncovered by reference review

### D11. Per-binding model rename (one-api O1)
- **Option A**: only at `models.provider_model_id` — rejected (forces same upstream id across all pools)
- **Option B**: per-binding override column — `model_pool_bindings.provider_model_id_override text NULL`
- **Pick: B.** One-api parity item; cheap (single nullable column).

### D12. Reserve schema for model-level fallback chain (LiteLLM L1)
- **Option A**: add `model_fallback_chains` table now (unused at L0)
- **Option B**: defer — add migration 0010 later
- **Option C**: column on `models` (text[] of fallback aliases)
- **Pick: B.** Avoid premature schema; plan exit ramp documented. Codex round 1 was right that "AttemptBudget=1" because Executor doesn't loop yet — model-level fallback is the same shape: add when consumer exists.

### D13. Per-binding rpm/tpm caps (LiteLLM L3)
- **Option A**: add now (`rpm_cap int NULL` + `tpm_cap int NULL`)
- **Option B**: defer
- **Pick: A.** LiteLLM-style contract enforcement is a parity item operators will ask for in week 1. Storing them now (but enforcing in Phase E) is cheap; deferring means schema migration churn.

### D14. Routing strategy hint on bindings (Portkey P1)
- **Option A**: add `model_pool_bindings.weight integer DEFAULT 1` now (unused)
- **Option B**: defer
- **Pick: A.** One-column reservation; saves Slice 5 migration when weighted-loadbalance lands.

### D15. Per-model default timeout (Portkey P4)
- **Option A**: add `models.timeout_ms_default int NOT NULL DEFAULT 60000`
- **Option B**: defer to forwarder config
- **Pick: A.** Portkey-style per-model timeout is real (vision models need longer than chat). Forwarder reads it via Phase E; Slice 2 stores.

### D16. Reasoning-capability flag (New API N3)
- **Option A**: add `'reasoning'` to the capabilities enum
- **Option B**: defer until thinking-model support
- **Pick: A.** Capability-string enum is just a text array; trivially extensible. Adding `'reasoning'` now (and `'tools'`, `'vision'`, `'json'`, `'stream'` per round-1) covers the visible surface.

### D17. Capability storage shape (round-1 Codex side)
- Codex round-1 proposed separate `model_registry_capabilities` table. Claude round-1 used `text[]`.
- one-api uses CSV; LiteLLM uses dict; Portkey uses array.
- **Pick: text[] (Claude round-1 was right here).** Codex's normalized table is over-engineered for Slice 2 — capability changes are rare, single-column UPDATE on a text[] is fine, and audit-of-capability-change is Phase E concern (capture via `updated_at` and trigger to `pool_routing_audit_events`). Reject Codex's separate table; revisit if capability-row-level lifecycle becomes a real need.

### D18. Down migration discipline (Codex raised)
- Existing 0007 has no `.down.sql`. Slice 2 needs to set discipline.
- **Option A**: provide `.down.sql` as standard from 0008 onward
- **Option B**: never provide down migrations (dev rollback uses `migrate force` + manual fix)
- **Pick: A.** Provide `.down.sql` and document in `docs/specs/_invariants/migrations.md` (new doc). Filling the historical gap (write 0001-0007 down files) is a separate backfill task — track but don't block Slice 2.

---

## Refined schema (round-2 synthesis preview)

```sql
-- 0008_model_registry.up.sql

CREATE TABLE IF NOT EXISTS model_registry_snapshots (
    id              bigserial PRIMARY KEY,
    tenant_id       bigint      NOT NULL REFERENCES tenants(id),
    version         bigint      NOT NULL DEFAULT 1,
    updated_at      timestamptz NOT NULL DEFAULT now(),
    updated_by_actor text,
    UNIQUE (tenant_id)
);

CREATE TABLE IF NOT EXISTS models (
    id                    bigserial PRIMARY KEY,
    tenant_id             bigint      NOT NULL REFERENCES tenants(id),
    canonical_id          text        NOT NULL,
    -- e.g. 'anthropic/claude-3.5-sonnet-20241022'
    protocol_family       text        NOT NULL CHECK (protocol_family IN
                            ('anthropic_messages','openai_chat','openai_responses','gemini')),
    provider_model_id     text        NOT NULL,
    context_window        int         NOT NULL CHECK (context_window > 0),
    capabilities          text[]      NOT NULL DEFAULT ARRAY[]::text[],
    -- D17: keep as text[]; per-capability lifecycle is Phase E
    pricing_class         text        NOT NULL DEFAULT 'standard',
    timeout_ms_default    int         NOT NULL DEFAULT 60000,    -- D15
    status                text        NOT NULL DEFAULT 'active'
                                CHECK (status IN ('active','disabled','deleted')),
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now(),
    deleted_at            timestamptz
);
CREATE UNIQUE INDEX uq_models_tenant_canonical
    ON models (tenant_id, canonical_id) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX uq_models_tenant_id_id
    ON models (tenant_id, id);
-- Composite key supports cross-tenant FK from aliases/bindings (Codex round-1 pattern)

CREATE TABLE IF NOT EXISTS model_aliases (
    id                    bigserial PRIMARY KEY,
    tenant_id             bigint      NOT NULL REFERENCES tenants(id),
    model_id              bigint      NOT NULL,
    public_alias_lower    text        NOT NULL,
    public_alias_display  text        NOT NULL,
    status                text        NOT NULL DEFAULT 'active'
                                CHECK (status IN ('active','disabled','deleted')),
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now(),
    deleted_at            timestamptz,
    FOREIGN KEY (tenant_id, model_id) REFERENCES models(tenant_id, id)
);
CREATE UNIQUE INDEX uq_aliases_tenant_alias
    ON model_aliases (tenant_id, public_alias_lower)
    WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS model_pool_bindings (
    id                          bigserial PRIMARY KEY,
    tenant_id                   bigint      NOT NULL REFERENCES tenants(id),
    model_id                    bigint      NOT NULL,
    pool_group_id               bigint      NOT NULL,
    provider_model_id_override  text        NULL,                       -- D11
    rpm_cap                     int         NULL CHECK (rpm_cap IS NULL OR rpm_cap > 0),     -- D13
    tpm_cap                     int         NULL CHECK (tpm_cap IS NULL OR tpm_cap > 0),     -- D13
    weight                      int         NOT NULL DEFAULT 1 CHECK (weight >= 0),          -- D14
    rank                        int         NOT NULL CHECK (rank >= 1),
    enabled                     boolean     NOT NULL DEFAULT true,
    reason                      text        NOT NULL DEFAULT 'primary',
    created_at                  timestamptz NOT NULL DEFAULT now(),
    updated_at                  timestamptz NOT NULL DEFAULT now(),
    deleted_at                  timestamptz,
    FOREIGN KEY (tenant_id, model_id) REFERENCES models(tenant_id, id),
    FOREIGN KEY (tenant_id, pool_group_id) REFERENCES pool_groups(tenant_id, id)
);
CREATE UNIQUE INDEX uq_pool_groups_tenant_id_id
    ON pool_groups (tenant_id, id);
-- Required by composite FK above; one-time addition to existing pool_groups
CREATE UNIQUE INDEX uq_bindings_tenant_model_pool
    ON model_pool_bindings (tenant_id, model_id, pool_group_id)
    WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX uq_bindings_tenant_model_rank
    ON model_pool_bindings (tenant_id, model_id, rank)
    WHERE deleted_at IS NULL;
CREATE INDEX idx_bindings_resolve
    ON model_pool_bindings (tenant_id, model_id, rank, enabled)
    WHERE deleted_at IS NULL;
```

Capabilities table (Codex round-1 had this as a separate table) is REJECTED per D17 — `models.capabilities text[]` is sufficient.

Down migration drops in reverse + drops `uq_pool_groups_tenant_id_id` — but with marker comment "do NOT drop in production once 0009+ depends on this index".

---

## CMB invariant audit under round-2 schema

**CMB-1 (Router does not read credentials)**: Registry returns `models`, `aliases`, `bindings` data — NO credential field across any table. Router consumes `ResolvedModel{capabilities, pool_candidates, rpm_cap, tpm_cap, ...}`. ✅ Hold.

**CMB-2 (Pool does not compute cost)**: rpm_cap/tpm_cap are integer rate-bounds, not decimal cost. timeout_ms_default is millisecond integer, not money. pricing_class is a string tag, not a number. Pool still receives `AttemptPlan` without decimal fields. ✅ Hold.

**CMB-7 (Layer write-discipline)**: PostgresRegistry implements only SELECT statements. Snapshot bumps (`UPDATE model_registry_snapshots SET version = version + 1`) happen via a future admin endpoint that runs in Ledger / Admin layer, not via registry resolve path. ✅ Hold.

**Possible new CMB violation**: Adding `rpm_cap` / `tpm_cap` to bindings introduces a "cap" concept in the registry layer. Is "cap" cost-adjacent (CMB-2 risk)? — No; rpm/tpm are rate-counts, not currency or token-cost. Document in `cross-module-boundaries.md` under CMB-2 carve-out: "rate caps are integer counts; allowed."

---

## Risk matrix expansion

| Risk | Trigger | Probability | Blast radius | Mitigation |
|---|---|---|---|---|
| Two MIT references describe rate limits at different layers (LiteLLM=deployment, Sub2API=account) | Operator confused which to set | Med | Per-tenant config drift | Doc note: rpm_cap is contract-level promise per binding; per-account is in `provider_accounts.cap_concurrency` (existing 0001) |
| LiteLLM-style fallbacks expected at runtime but Slice 2 doesn't enforce | Operator configures `model_fallback_chain` and is surprised it's not used | Med | Operator confusion only | Slice 2 NEVER ships the fallback table; defer to N+6. Document Phase E plan |
| Portkey-style weighted load-balance expected | Operator sets `weight=10` for one binding, expects loadbalance | Med | Tenant-visible behavior surprise | Document: weight is reserved column, ignored at L0 |
| Tenant-only registry (no global sentinel, D3) means new tenants have empty registry | Customer onboarding flow misses model-seed step | High | Per-tenant: no models work | Mandatory: tenant-creation runbook in `docs/RUNBOOKS/tenant-onboarding.md`; smoke for empty-tenant case asserts 404 |
| Snapshot version int overflow at L2 (~10^9 changes) | Heavy admin-thrash | Low | Per-tenant: stuck at version | bigint counter; bigint won't overflow in our lifetime |
| Per-binding rpm_cap stored but not enforced (Phase E gap) | Operator sets cap, traffic exceeds | Med | Tenant: silent over-cap until Phase E | Audit-log warning when rpm_cap exists but Phase E hasn't enforced |
| one-api-style per-binding model rename (D11) creates audit confusion | Operator renames binding-level provider id, billing logs see different upstream id from registry-level | Low | Operator-visible only | Audit log records `provider_model_id` AS RESOLVED at request time (override or default) — single source of truth for billing |
| LRU + 30s TTL means binding disable lags 30s | Operator disables alias, traffic still resolves for 30s | Med | Per-tenant: 30s misroute | Document; `scheduler_outbox` event-driven invalidation in Slice 5 |
| Composite FK on (tenant_id, pool_group_id) requires UNIQUE INDEX on pool_groups (tenant_id, id) | Existing 0001 doesn't have this index | High | Migration 0008 fails on PG | Migration 0008 creates the index FIRST; safe additive change |
| `tenant_id=0` Postgres reserved value collision (Claude round-1 risk inherited) | Round-2 dropped sentinel — risk gone | n/a | — | N/A after D3 round-2 pick |
| Down migration drops `uq_pool_groups_tenant_id_id` and breaks future migrations relying on it | Operator runs `migrate down` to 0007, then `migrate up` to 0009 | Low | Local-dev only (prod has no down) | Comment in `.down.sql` explicit warning |

---

## Final synthesis recommendation (if Owner says "由你决定")

**Schema** (5 new tables + 1 index addition to existing pool_groups):
- `model_registry_snapshots` (per-tenant version counter)
- `models` (canonical model identities, per-tenant)
- `model_aliases` (alias → model)
- `model_pool_bindings` (model → pool, ordered + capable of weighted loadbalance later)
- `uq_pool_groups_tenant_id_id` (additive index on existing 0001 table; required for composite FK)

**Code structure**:
```
backend/internal/registry/
    registry.go          (Registry interface)
    postgres_registry.go (PostgresRegistry impl + version-based cache)
    cache.go             (per-process snapshot cache; reload on version bump)
    errors.go            (ErrUnknownModel/ErrModelDisabled/ErrTenantNoAccess/ErrRegistryBackend)
    registry_test.go     (unit: cache reload, casing)
    postgres_registry_integration_test.go (table-driven happy/unknown/disabled/cross-tenant/binding-rank/rpm-cap-pass-through)
backend/sql/queries/registry.sql (sqlc)
backend/sql/migrations/0008_model_registry.up.sql + .down.sql
backend/sql/seed/registry_default.sql (Phase C smoke seed)
docs/specs/_invariants/migrations.md (down-migration discipline doc, new)
```

**Cache strategy**:
- Per-process snapshot cache (one map per tenant), keyed by `version`.
- On cache miss OR version mismatch: re-fetch full tenant snapshot (one query: `JOIN models + aliases + bindings WHERE tenant_id = $1`). Replace cache atomically.
- TTL fallback: 30s. Future Slice 5 plumbs `scheduler_outbox` event for instant invalidation.
- Health check at boot: if `models` has 0 rows AND `cfg.Env != "test"`, log loud warning (don't crash — empty registry is valid for fresh installs).

**Sequencing** (D7 Round-1 split confirmed):
- N+5a: schema + sqlc + `internal/registry` package + tests + main wiring (escape hatch still present, body field still accepted but unused for resolved models).
- N+5b: chat handler rewrite, delete `PlanWithPoolGroupID`, drop body `pool_group_id`, smoke seeds registry.

**Decision-point summary table**:

| ID | Round-2 pick | Driver |
|---|---|---|
| D1 schema | 5 tables (Codex side) | Codex's normalized model wins on Slice-3 evolution; Claude's 3-table is too compressed |
| D2 cache | YES — per-process snapshot, version-based reload | 4/4 MIT references cache; refinement away from generic LRU to version-based |
| D3 tenant scoping | tenant-only, no sentinel | 4/4 MIT references avoid global fallback |
| D4 HTTP | 403 disabled / 404 unknown / 503 backend | All MIT references actionable; aliases aren't secrets so anti-enum doesn't apply |
| D5 body pool_group_id | delete | Both round-1 plans agreed |
| D6 snapshot | per-tenant counter table (Codex side) | Portkey + Envoy both have explicit version; one-api implicit not enough for audit replay |
| D7 sequencing | N+5a + N+5b split | Both round-1 plans agreed |
| D8 models.provider_id | not added | Both agreed |
| D9 case-insensitive aliases | YES (Claude round-1) | Real-world clients send mixed case |
| D10 alias chains | NO (Claude round-1) | Adds complexity not in any reference |
| D11 per-binding model rename | YES (one-api parity) | New |
| D12 model fallback chain | DEFER schema | LiteLLM L1 — schema add when consumer exists |
| D13 per-binding rpm/tpm caps | YES, store-only (Phase E enforces) | LiteLLM L3 |
| D14 binding weight column | YES, reserved (unused) | Portkey P1 |
| D15 per-model timeout | YES | Portkey P4 |
| D16 reasoning capability | YES (string in enum) | New API N3 |
| D17 capabilities storage | text[] (Claude round-1) | Codex's separate table over-engineered |
| D18 down migration | provide `.down.sql` from 0008 | Codex raised; set discipline going forward |

---

## Feature parity matrix delta (post-Slice 2)

After N+5b ships, the following `F-*` capability ids in `docs/03_FEATURE_PARITY_MATRIX.md` should be marked `Designed` or `Implemented`:

- `F-MODEL-REGISTRY-001`: per-tenant model catalog → **Implemented**
- `F-MODEL-REGISTRY-002`: model alias map (lower-case) → **Implemented**
- `F-MODEL-REGISTRY-003`: model → pool binding with priority → **Implemented**
- `F-MODEL-REGISTRY-004`: per-binding model rename → **Implemented** (D11)
- `F-MODEL-REGISTRY-005`: per-binding rpm/tpm caps stored → **Designed** (Phase E enforces)
- `F-MODEL-REGISTRY-006`: per-binding weight (loadbalance prep) → **Designed**
- `F-MODEL-REGISTRY-007`: per-model timeout default → **Implemented** (D15)
- `F-MODEL-REGISTRY-008`: model status (active/disabled/deleted) → **Implemented**
- `F-MODEL-REGISTRY-009`: snapshot version stamp on usage → **Implemented**
- `F-MODEL-REGISTRY-010`: model-level fallback chain → **Roadmap** (Phase E)

Existing F-rows likely affected:
- `F-POOL-001` Phase A (channel + model exposure) — partially superseded by registry; doc note to clarify pool layer's surface vs registry's
- `F-PROTO-002` (protocol translation) — leverages `models.protocol_family`

(Do NOT modify the parity matrix file in this slice; synthesized plan + Owner approval triggers a separate doc PR.)

---

## What I'd ask Codex round 2 in synthesis

1. Did you find evidence that suggests rpm/tpm caps belong in registry vs pool? My pick is registry (LiteLLM-style contract); want your check.
2. Per-binding model rename (D11) — did you find one-api-source patterns that would change the column shape (single text vs JSON object)?
3. Snapshot version: per-tenant int counter vs Postgres native txid_current()? (txid is implicit transaction id; would simplify atomic snapshot semantics.)
4. Down migration discipline (D18) — agree to set standard from 0008, or a different cut-line?
5. capabilities text[] vs separate table — did you find any reference project that uses a normalized table for capabilities, or do they all use array/CSV?

These are spots where reference evidence may shift the round-2 pick further; want Codex's reading.

---

Source files read:
  HUAKAI repo: docs/process/plans/2026-04-30-n5-model-registry-claude.md, docs/process/plans/2026-04-30-n5-model-registry-codex.md, docs/07_REFERENCE_EVIDENCE_LEDGER.md (lines 1-130), docs/06_REFERENCE_PROJECTS.md, docs/05_CLEAN_ROOM_POLICY.md, docs/specs/_invariants/cross-module-boundaries.md, docs/02_HUAKAI_FUSION_ARCHITECTURE.md, backend/internal/router/route_plan.go, backend/internal/router/default_router.go, backend/internal/gatewayhttp/chat_completions_handler.go, backend/sql/migrations/0001_pool_routing.up.sql.
  Reference (MIT): one-api / LiteLLM / Portkey / envoy-ai-gateway — public design pattern knowledge from training-time reads, paraphrased and behavior-only. No fresh source fetches in this session; relied on prior already-ledger-cleaned evidence rows.
  Reference (already-mined evidence rows): E-OAI-001..015, E-LM-001..006, E-PK-001..008, E-NAI-001..008, E-S2A-001..008, E-AAH-001..006, E-HLC-001..007.
Clean-room declaration: This author drew on (a) ledger-cleaned evidence rows (already specifier-passed); (b) public design pattern paraphrase from training-time exposure to MIT references. No verbatim names, schemas, comments, or code copied. Did not freshly read source from sub2api / new-api / all-api-hub / helicone (non-MIT, ledger-only).
Lane: implementer (round 2)
Agent: Claude (claude-opus-4-7)
UTC timestamp: 2026-04-30T09:08:00Z
