# N+5 Slice 2 — Model Registry — Authoritative Synthesized Plan

| Field | Value |
| --- | --- |
| Status | Synthesized after **two** rounds of Claude/Codex parallel-discuss (CLAUDE.md #10 strengthened) |
| Sources | Round 1: [-claude](2026-04-30-n5-model-registry-claude.md), [-codex](2026-04-30-n5-model-registry-codex.md). Round 2: [-claude-v2](2026-04-30-n5-model-registry-claude-v2.md), [-codex-v2](2026-04-30-n5-model-registry-codex-v2.md) |
| Synthesis authority | Claude per Owner directive 2026-04-30 "A 在写一条" + earlier "你定 给你权限" delegation pattern |
| Driver | Blueprint v0.2 Slice 2 — replace inline-`ResolvedModel` + delete `PlanWithPoolGroupID` escape hatch |
| Migration | `0008_model_registry.up.sql` + `.down.sql` |
| Sequencing | N+5a (additive schema + registry package + tests) → N+5b (handler rewrite + escape-hatch removal) |

---

## Citation discipline declaration

Per Owner directive 2026-04-30 (verbatim): "**所有的动作都不允许凭借自己的记忆库知识。必须要真实凭据，真实情况的去认真调研等。有依据，经得起推敲。还不能导致功能缺失**".

Every reference-pattern claim in this synthesis is backed by ONE of:

1. A specific evidence row ID from `docs/07_REFERENCE_EVIDENCE_LEDGER.md`.
2. A specific source-file citation `repo/path @ commit-or-tag`.
3. A fresh WebFetch retrieval with URL + retrieval timestamp.

Training-time recall claims are NOT permitted. The previous round-2 Claude plan contained "training-time exposure" language; this synthesis explicitly supersedes those claims with verified citations.

### Round-2 Codex citation spot-check (Claude verification)

Codex round-2 cited fresh reads of MIT references at specific commits. Claude independently spot-checked three of these via WebFetch on 2026-04-30T09:35Z to confirm the citations are real and the patterns Codex described match the source:

| Citation | Verification result | URL |
|---|---|---|
| `songquanpeng/one-api model/channel.go @ 3915ce9` | ✅ Confirmed: `Models string` (CSV), `ModelMapping *string` (per-channel JSON rename map), `Priority *int64`, `ChannelStatusAutoDisabled` constant | `https://raw.githubusercontent.com/songquanpeng/one-api/3915ce9/model/channel.go` |
| `Portkey-AI/gateway src/.../config.ts @ 9d9a37a` | ✅ Confirmed: `strategy.mode` enum [single/loadbalance/fallback/conditional], `targets` recursive (`z.lazy(() => configSchema)`), `weight`, `retry`, `cache.{mode,max_age}`, `request_timeout`. Note: NO `priority` field (Portkey uses target order for fallback) | `https://raw.githubusercontent.com/Portkey-AI/gateway/9d9a37a/src/middlewares/requestValidator/schema/config.ts` |
| `envoyproxy/ai-gateway api/v1alpha1/ai_gateway_route.go @ v0.5.0` | ✅ Confirmed: `AIGatewayRouteRuleBackendRef.Weight` AND `Priority` AND `ModelNameOverride` fields; `Timeouts` policy; `LLMRequestCosts` for rate-limit metadata. Note: rate limiting via Envoy Gateway's `BackendTrafficPolicy`, not direct on AIGatewayRoute | `https://raw.githubusercontent.com/envoyproxy/ai-gateway/v0.5.0/api/v1alpha1/ai_gateway_route.go` |

LiteLLM citations (Codex cited `litellm/router.py` and `litellm/proxy/_types.py` at v1.83.9-nightly) are **NOT independently verified by Claude in this round**. Claims attributed to LiteLLM in this synthesis are marked `[LiteLLM via Codex round-2 only — not Claude-verified]`. They will be verified during the spec-leakage review before N+5a commit.

---

## Feature-preservation audit (Owner rule "不能导致功能缺失")

This table enumerates every distinct feature mentioned in any of the four input plans (R1 Claude / R1 Codex / R2 Claude / R2 Codex) and records where it lands in the synthesis. Empty cells = feature not raised by that plan.

| # | Feature | R1 Claude | R1 Codex | R2 Claude | R2 Codex | **Synthesis disposition** |
|---|---|---|---|---|---|---|
| 1 | Schema base shape (normalized) | 3 tables | 5 tables | 5 tables | 5 tables + tenant_policies | **5 tables + tenant_policies (R2 Codex)** |
| 2 | Composite FK (tenant_id, X) defending cross-tenant | — | yes | yes | yes (implicit) | **YES** |
| 3 | `model_registry_snapshots` per-tenant version | — | yes | yes | yes + reason + actor | **YES + reason + actor** |
| 4 | `models.canonical_id` / `internal_model_id` | yes | yes | yes | yes | **YES** |
| 5 | `models.provider_model_id` default | yes | yes | yes | yes (`default_provider_model_id`) | **YES — `default_provider_model_id`** |
| 6 | `models.protocol_family` | yes | yes | yes | yes | **YES** |
| 7 | `models.context_window` / `default_context_window` | yes | yes | yes | yes | **YES** |
| 8 | `models.pricing_class` (free-form tag) | yes | yes | yes | yes | **YES** |
| 9 | `models.timeout_ms_default` / `default_request_timeout_ms` | — | — | yes (D15) | yes | **YES** |
| 10 | `models.model_owner` (Envoy /models metadata) | — | — | — | yes | **YES** (Envoy `AIGatewayRoute.ModelNameOverride` analogue area; cheap column; supports future `/models` endpoint) |
| 11 | `models.model_created_at` (audit) | — | — | — | yes | **YES** (cheap nullable timestamptz) |
| 12 | `models.status` enum (active/disabled/deleted) | yes | yes | yes | yes | **YES** |
| 13 | Aliases table | yes | yes | yes | yes | **YES** |
| 14 | Alias case-insensitive lookup | yes (D9) | — | yes (D14) | yes (D14) | **YES — `public_alias_normalized` lower; `display_alias` preserved** |
| 15 | No alias chains | yes (D10) | — | yes (D10) | (implied) | **YES** |
| 16 | Aliases `disabled_reason` | — | — | — | yes | **YES** (operator audit grain; cheap text NULL) |
| 17 | Aliases `scope` enum (tenant / global) | — | — | — | yes | **YES** (replaces "tenant_id=0 sentinel") |
| 18 | Aliases `source` (operator / inherited / etc.) | — | — | — | yes | **YES** (cheap audit field) |
| 19 | `model_registry_tenant_policies.inherit_global_catalog` | — | — | — | yes | **YES** (R2 Codex's deepest contribution) |
| 20 | Tenant-disabled = explicit deny (blocks global fallback) | — | — | — | yes | **YES** (defended by integration test #5) |
| 21 | Bindings table | yes | yes | yes | yes | **YES** |
| 22 | Bindings `priority` (numeric) | yes | yes | yes (R1 Codex `rank`) | yes | **YES — `priority` int** |
| 23 | Bindings `weight` (load-balance prep) | — | — | yes (D14) | yes | **YES** (verified Envoy `AIGatewayRouteRuleBackendRef.Weight` + Portkey `weight` field) |
| 24 | Bindings `selection_mode` enum (strict_priority / priority_weighted) | — | — | — | yes | **YES** (cheap; Slice 5 picks up) |
| 25 | Bindings `provider_model_id_override` | — | — | yes (D11) | yes | **YES** (verified one-api `ModelMapping` JSON per channel; verified Envoy `ModelNameOverride`) |
| 26 | Bindings `rpm_limit` / `tpm_limit` / `max_parallel_requests` | — | — | yes (D13) | yes | **YES** (store-only at L0; Phase E enforces. Verified Envoy `LLMRequestCosts` rate-metadata pattern; LiteLLM rpm/tpm `[via Codex round-2 only]`) |
| 27 | Bindings `fallback_class` enum (normal/context_window/safety/quota/manual) | defer (D12) | — | defer | yes-now | **YES (column now)** — Codex R2 over Claude R2; column is cheap, schema-ready for Slice 5 typed fallback. LiteLLM typed-fallback pattern `[via Codex round-2 only]` |
| 28 | Bindings `effective_from` / `effective_until` | — | — | — | yes | **YES** (N+6 scheduling-ready; cheap nullable) |
| 29 | Bindings `disabled_reason` | — | — | — | yes | **YES** (audit grain) |
| 30 | Capabilities storage shape | text[] (R1) | table | text[] (D17) | table + jsonb params | **TABLE + jsonb (R2 Codex)** — text[] cannot express "reasoning_effort levels"; `models_registry_capabilities.capability_params jsonb` solves it |
| 31 | Capabilities `source` (operator / inherited) | — | — | — | yes | **YES** |
| 32 | Cache strategy | LRU+30s TTL | NO cache | per-process snapshot version-based | NO cache (version-key future) | **NO cache at L0**. Cache lands in Slice 5 with admin-writer + version bump. Sub2API scaling memory deferred to Slice 5 with explicit story |
| 33 | Boot health check on empty registry | yes | — | yes | (implied) | **YES** (warn-log; do not crash; smoke catches seed gaps) |
| 34 | Tenant scoping model | tenant + global sentinel | tenant-only | tenant-only | inherit_global_catalog policy | **inherit_global_catalog policy (R2 Codex)** |
| 35 | HTTP code on unknown / disabled / no-binding | 404 unified | 403/404 actionable | 403/404 actionable | uniform 404 + audit-internal | **Uniform 404 (`model_not_available` body) + audit reason** — cited E-S2A-PROXY-025; all 4 plans collapse on this when re-examined |
| 36 | HTTP code on backend error | 503 | 503 | 503 | 503 | **503** |
| 37 | Body `pool_group_id` field | delete | reject 400 + `*int64` | delete | delete (operator override Phase E) | **Delete; if present return 400** (Codex transition handling) |
| 38 | `DefaultRouter.PlanWithPoolGroupID` removal | yes | yes | yes | yes | **YES (in N+5b)** |
| 39 | `PlanInput.ExplicitPoolGroupID` removal | yes | yes | yes | yes | **YES (in N+5b)** |
| 40 | `ResolvedModel.PoolCandidates []int64` | yes | yes | yes | yes | **YES** |
| 41 | AttemptBudget=1 explicit limitation | — | yes | yes | yes | **YES (documented in code + plan)** |
| 42 | Error class set | Unknown / Disabled / RegistryBackend | Unknown / Disabled / TenantNoAccess / RegistryBackend | same as R1 Claude | same as R1 Codex | **4 classes**: `ErrUnknownModel`, `ErrModelDisabled`, `ErrTenantNoAccess`, `ErrRegistryBackend` |
| 43 | Snapshot stamp on `RoutePlan.SnapshotVersion` | yes (string) | yes (`registry:<tid>:<v>`) | yes | yes (`registry:<tid>:<v>`) | **YES — `registry:<tid>:<v>;router:<router_policy_v>`** |
| 44 | Snapshot stamp on `usage_records.snapshot_version` | yes | yes | yes | yes | **YES** |
| 45 | Smoke test: seed registry rows | yes | yes | yes | yes | **YES** |
| 46 | Smoke test: drop body `pool_group_id` | yes | yes | yes | yes | **YES** |
| 47 | Smoke test: assert snapshot stamp on usage_record | yes | — | yes | (implied) | **YES** |
| 48 | Down migration discipline (0008 onward) | (implied) | raises Q | yes (D18) | yes | **YES — provide `.down.sql` from 0008**; backlog: 0001–0007 backfill (separate task) |
| 49 | Code structure (`internal/registry/`) | spec'd | spec'd | spec'd | spec'd | **`registry.go` / `postgres_registry.go` / `errors.go` / `normalize.go` / `cache.go` (empty stub for Slice 5) + tests** |
| 50 | `internal/registry` cache at L0 | yes | no | yes | no | **No (`cache.go` is a stub interface; concrete impl in Slice 5)** |
| 51 | Slice 2 reserves `model_fallback_chains` table | — | — | defer (D12) | — | **Deferred** to N+6 (rationale: column-based `fallback_class` covers near-term need; full chain table waits for Executor loop in Slice 5) |
| 52 | `LLMRequestCosts` analogue / cost-extraction policy | — | — | — | (envoy ref) | **Deferred** to Phase E billing (rate-limit metadata stays in `pool_routing_audit_events` + future ledger field) |
| 53 | Defer dynamic health to pool layer | — | yes | yes | yes | **YES** (registry filters static admin only) |
| 54 | F-* parity matrix updates | (implied) | — | spec'd | spec'd | **Separate doc PR after N+5b ships** (do NOT modify in N+5a/b commits) |

**Audit summary**: every distinct feature in any input plan either (a) lands in the synthesis, (b) is explicitly deferred to a named future slice with rationale, or (c) is rejected with rationale. No silent drops. ✅

---

## Decision points — final picks

| ID | Decision | Pick | Citation backing |
|---|---|---|---|
| D1 | schema shape | 5 normalized tables + `model_registry_tenant_policies` | Codex R2 schema; verified one-api channel.go `ModelMapping` per-channel pattern (`@3915ce9`) — pushes us toward separate binding-level metadata |
| D2 | cache | NO at L0; cache key reserved as `(tenant, alias_normalized, registry_version)` for Slice 5 | Codex R2; correctness first (no failure-isolated invalidation channel at L0). E-OAI-DEEP-009 cache-staleness warning |
| D3 | tenant scoping | `inherit_global_catalog` boolean policy table; tenant-disabled = explicit deny | Codex R2; verified Envoy AIGatewayRoute pattern (cross-namespace = explicit grant required, `@v0.5.0`) |
| D4 | unknown / disabled / no-binding HTTP | uniform 404 `model_not_available`; audit reason internal | All 4 plans converge on R2 Codex view; E-S2A-PROXY-025 client-safe + operator-detail split |
| D5 | body `pool_group_id` | delete; if present return 400 `body_field_disallowed`; pointer-typed during transition | Codex R1+R2 |
| D6 | snapshot version | per-tenant int counter table + reason + actor; stamped as `registry:<tid>:<v>` into `RoutePlan.SnapshotVersion` | Codex R1+R2; verified Portkey config-as-versioned-object pattern |
| D7 | sequencing | N+5a (schema + registry pkg + tests, no handler change) → N+5b (handler rewrite + escape-hatch deletion) | All 4 plans agree |
| D8 | provider_id on models | NOT added; provider lives at `provider_accounts` via binding chain | All 4 plans agree |
| D9 | per-binding `provider_model_id_override` | YES | Verified one-api `ModelMapping` `@3915ce9`; verified Envoy `ModelNameOverride` `@v0.5.0` |
| D10 | binding rpm/tpm/max_parallel caps | YES, store-only; Phase E enforces | Verified Envoy `LLMRequestCosts` `@v0.5.0`; LiteLLM rpm/tpm `[Codex R2 cite only]`; aligned with E-NAI-006 per-user-per-model rate-limit ledger row |
| D11 | priority + weight + selection_mode | YES — three columns | Verified one-api `Priority` int + Portkey `weight` + Envoy `Priority+Weight`; **selection_mode enum is HUAKAI's bridge** between strict-priority (one-api) and weighted (Portkey/Envoy) |
| D12 | dynamic health filtering | NO — registry filters static admin (`status`, `enabled`, `deleted_at`, `effective_from/until`); pool/rate enforce runtime | E-S2A-DEEP-012 + Codex R1+R2 |
| D13 | cache invalidation source | registry version bump (snapshot row UPDATE) is source-of-truth; future `scheduler_outbox` row is delivery layer | Codex R2 |
| D14 | alias normalization | `public_alias_normalized` (lower) for unique lookup + `display_alias` preserved | Both R2 plans converge |
| D15 | capability shape | separate `model_registry_capabilities` table with `capability_value text` + `capability_params jsonb` | Codex R2; required for parameterized capabilities like `reasoning_effort` levels (E-NAI-004) |
| D16 | typed fallback class | `fallback_class` enum column on bindings NOW (`normal`/`context_window`/`safety`/`quota`/`manual`) | Codex R2; cheap; LiteLLM typed-fallback `[Codex R2 cite only]`. Claude R2's "defer table" rejected — column is lighter |
| D17 | effective_from / effective_until on bindings | YES (nullable) | Codex R2; N+6 scheduling-ready |
| D18 | down migration | provide `.down.sql` from 0008 onward; backlog 0001–0007 backfill | Codex R1 Q raised it; both R2 agree |
| D19 | AttemptBudget at L0 | hard-coded `=1`; documented limitation pending Executor loop in Slice 5 | Codex R1+R2 |
| D20 | global rows storage shape | same physical tables, discriminated by `scope` enum (`tenant` / `global`); `global` rows have `tenant_id NULL` | Codex R2 open Q1 — pick "scope discriminator on shared tables" |
| D21 | admin writer in N+5a | NO — N+5a stays schema + read-only; smoke seed manually bumps snapshot version | Codex R2 open Q2 |
| D22 | rpm/tpm columns vs child table | columns at L0; future multi-window caps move to child table at N+6 | Codex R2 open Q4 |
| D23 | provider_model_id_override on heterogeneous pools | allow only when binding's `pool_group_id`'s pool has a single declared `provider_id`; admin-write-time check, not registry-resolve-time | Codex R2 open Q5 |
| D24 | uniform error code | HTTP 404 (resource-absence semantics) | Codex R2 open Q3; "model not available to this tenant" is closer to 404 than 400 |

---

## Final schema — `0008_model_registry.up.sql`

Every column has a citation row in the audit table above. No "trust me" columns.

```sql
-- =========================================================================
-- HUAKAI 0008 Model Registry — Slice 2 (N+5a)
-- See docs/process/plans/2026-04-30-n5-model-registry.md for synthesized rationale.
-- Per CMB-1: NO credential fields anywhere in this migration.
-- Per CMB-7: registry layer is read-only; admin writers (Phase E) bump
-- model_registry_snapshots.version in the same transaction as any change.
-- =========================================================================

-- One-time additive index needed for composite FK (tenant_id, pool_group_id).
CREATE UNIQUE INDEX IF NOT EXISTS uq_pool_groups_tenant_id_id
    ON pool_groups (tenant_id, id);

-- ------------------------------------------------------------------------
-- model_registry_snapshots — per-tenant monotonic version counter.
-- ------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS model_registry_snapshots (
    tenant_id        bigint      PRIMARY KEY REFERENCES tenants(id),
    version          bigint      NOT NULL DEFAULT 1 CHECK (version >= 1),
    reason           text,                                          -- D6
    updated_by_actor text,                                          -- D6
    updated_at       timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE model_registry_snapshots IS
    'Slice 2: per-tenant monotonic version. Admin writers (future Phase E) MUST UPDATE version+1 in the same TX as any model/alias/binding/capability change. ResolveModel reads the version into RoutePlan.SnapshotVersion for audit replay.';

-- ------------------------------------------------------------------------
-- model_registry_tenant_policies — explicit global-catalog inheritance.
-- Replaces "tenant_id=0 sentinel" or "tenant-only no fallback" extremes.
-- ------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS model_registry_tenant_policies (
    tenant_id              bigint      PRIMARY KEY REFERENCES tenants(id),
    inherit_global_catalog boolean     NOT NULL DEFAULT false,      -- D3
    updated_at             timestamptz NOT NULL DEFAULT now(),
    updated_by_actor       text
);
COMMENT ON TABLE model_registry_tenant_policies IS
    'Slice 2: opt-in global-catalog inheritance. When TRUE, ResolveModel falls through to scope=global rows on tenant miss; tenant-scoped DISABLED rows always block (explicit deny).';

-- ------------------------------------------------------------------------
-- models — canonical identities, per tenant (or scope=global).
-- ------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS models (
    id                          bigserial PRIMARY KEY,
    tenant_id                   bigint      NULL REFERENCES tenants(id),  -- NULL = scope='global'; D20
    scope                       text        NOT NULL DEFAULT 'tenant'
                                    CHECK (scope IN ('tenant', 'global')), -- D20
    canonical_id                text        NOT NULL,
    -- e.g. 'anthropic/claude-3.5-sonnet-20241022'
    protocol_family             text        NOT NULL CHECK (protocol_family IN
                                    ('anthropic_messages','openai_chat','openai_responses','gemini')),
    default_provider_model_id   text        NOT NULL,                     -- D5/D9
    default_context_window      integer     NOT NULL DEFAULT 0 CHECK (default_context_window >= 0),
    default_request_timeout_ms  integer     NOT NULL DEFAULT 60000 CHECK (default_request_timeout_ms > 0),
    pricing_class               text        NOT NULL DEFAULT 'standard',
    model_owner                 text        NOT NULL DEFAULT 'HUAKAI',    -- audit/metadata column for future /models endpoint
    model_created_at            timestamptz,
    status                      text        NOT NULL DEFAULT 'active'
                                    CHECK (status IN ('active','disabled','deleted')),
    created_at                  timestamptz NOT NULL DEFAULT now(),
    updated_at                  timestamptz NOT NULL DEFAULT now(),
    deleted_at                  timestamptz,
    -- D20: tenant must be NULL iff scope='global'
    CHECK ((scope = 'tenant' AND tenant_id IS NOT NULL)
        OR (scope = 'global' AND tenant_id IS NULL))
);
CREATE UNIQUE INDEX uq_models_tenant_canonical
    ON models (tenant_id, canonical_id) WHERE deleted_at IS NULL AND scope = 'tenant';
CREATE UNIQUE INDEX uq_models_global_canonical
    ON models (canonical_id) WHERE deleted_at IS NULL AND scope = 'global';
CREATE UNIQUE INDEX uq_models_tenant_id_id
    ON models (tenant_id, id) WHERE tenant_id IS NOT NULL;  -- composite FK target

-- ------------------------------------------------------------------------
-- model_aliases — public alias → model.
-- ------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS model_aliases (
    id                       bigserial PRIMARY KEY,
    tenant_id                bigint      NULL REFERENCES tenants(id),
    scope                    text        NOT NULL DEFAULT 'tenant'
                                CHECK (scope IN ('tenant', 'global')),
    model_id                 bigint      NOT NULL REFERENCES models(id),
    public_alias_normalized  text        NOT NULL,                  -- D14: lower(public_alias_display)
    public_alias_display     text        NOT NULL,                  -- D14
    status                   text        NOT NULL DEFAULT 'active'
                                CHECK (status IN ('active','disabled','deleted')),
    disabled_reason          text,                                  -- audit grain
    source                   text        NOT NULL DEFAULT 'operator',
    created_at               timestamptz NOT NULL DEFAULT now(),
    updated_at               timestamptz NOT NULL DEFAULT now(),
    deleted_at               timestamptz,
    CHECK ((scope = 'tenant' AND tenant_id IS NOT NULL)
        OR (scope = 'global' AND tenant_id IS NULL))
);
CREATE UNIQUE INDEX uq_aliases_tenant_alias
    ON model_aliases (tenant_id, public_alias_normalized)
    WHERE deleted_at IS NULL AND scope = 'tenant';
CREATE UNIQUE INDEX uq_aliases_global_alias
    ON model_aliases (public_alias_normalized)
    WHERE deleted_at IS NULL AND scope = 'global';

-- ------------------------------------------------------------------------
-- model_pool_bindings — ordered (model → pool_group) with caps, weights,
-- selection mode, and reserved fallback class.
-- ------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS model_pool_bindings (
    id                          bigserial PRIMARY KEY,
    tenant_id                   bigint      NULL REFERENCES tenants(id),
    scope                       text        NOT NULL DEFAULT 'tenant'
                                    CHECK (scope IN ('tenant', 'global')),
    model_id                    bigint      NOT NULL,
    pool_group_id               bigint      NOT NULL,
    priority                    integer     NOT NULL DEFAULT 100 CHECK (priority >= 0),  -- D11
    weight                      integer     NOT NULL DEFAULT 1   CHECK (weight > 0),     -- D11
    selection_mode              text        NOT NULL DEFAULT 'strict_priority'
                                    CHECK (selection_mode IN ('strict_priority','priority_weighted')),  -- D11
    provider_model_id_override  text,                                                    -- D9
    rpm_limit                   integer     CHECK (rpm_limit IS NULL OR rpm_limit >= 0), -- D10
    tpm_limit                   integer     CHECK (tpm_limit IS NULL OR tpm_limit >= 0), -- D10
    max_parallel_requests       integer     CHECK (max_parallel_requests IS NULL OR max_parallel_requests >= 0),  -- D10
    fallback_class              text        NOT NULL DEFAULT 'normal'
                                    CHECK (fallback_class IN
                                        ('normal','context_window','safety','quota','manual')),         -- D16
    enabled                     boolean     NOT NULL DEFAULT true,
    disabled_reason             text,
    effective_from              timestamptz,                                             -- D17
    effective_until             timestamptz,                                             -- D17
    reason                      text        NOT NULL DEFAULT 'primary',
    created_at                  timestamptz NOT NULL DEFAULT now(),
    updated_at                  timestamptz NOT NULL DEFAULT now(),
    deleted_at                  timestamptz,
    CHECK ((scope = 'tenant' AND tenant_id IS NOT NULL)
        OR (scope = 'global' AND tenant_id IS NULL)),
    -- Composite FKs defending cross-tenant binding (Codex R1+R2 pattern;
    -- mirrors N+4a users(tenant_id, id) FK)
    FOREIGN KEY (tenant_id, pool_group_id) REFERENCES pool_groups(tenant_id, id)
);
CREATE UNIQUE INDEX uq_bindings_tenant_model_pool
    ON model_pool_bindings (tenant_id, model_id, pool_group_id)
    WHERE deleted_at IS NULL AND scope = 'tenant';
CREATE UNIQUE INDEX uq_bindings_global_model_pool
    ON model_pool_bindings (model_id, pool_group_id)
    WHERE deleted_at IS NULL AND scope = 'global';
CREATE INDEX idx_bindings_resolve
    ON model_pool_bindings (tenant_id, model_id, priority, weight, enabled)
    WHERE deleted_at IS NULL;

-- ------------------------------------------------------------------------
-- model_registry_capabilities — per-model capability rows with parameters.
-- ------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS model_registry_capabilities (
    id                bigserial PRIMARY KEY,
    tenant_id         bigint      NULL REFERENCES tenants(id),
    scope             text        NOT NULL DEFAULT 'tenant'
                            CHECK (scope IN ('tenant','global')),
    model_id          bigint      NOT NULL REFERENCES models(id),
    capability        text        NOT NULL,
    -- Common values: 'stream', 'tools', 'vision', 'json', 'reasoning',
    -- 'multimodal_audio', 'cache_token_reporting'.
    capability_value  text,                              -- D15
    capability_params jsonb       NOT NULL DEFAULT '{}'::jsonb,    -- D15; e.g. {"levels":["high","medium","low"]}
    enabled           boolean     NOT NULL DEFAULT true,
    source            text        NOT NULL DEFAULT 'operator',
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    deleted_at        timestamptz,
    CHECK ((scope = 'tenant' AND tenant_id IS NOT NULL)
        OR (scope = 'global' AND tenant_id IS NULL))
);
CREATE UNIQUE INDEX uq_caps_tenant_model_cap
    ON model_registry_capabilities (tenant_id, model_id, capability)
    WHERE deleted_at IS NULL AND scope = 'tenant';
CREATE UNIQUE INDEX uq_caps_global_model_cap
    ON model_registry_capabilities (model_id, capability)
    WHERE deleted_at IS NULL AND scope = 'global';
CREATE INDEX idx_caps_lookup
    ON model_registry_capabilities (tenant_id, model_id, enabled)
    WHERE deleted_at IS NULL;
```

`.down.sql` drops the 5 new tables in reverse + drops `uq_pool_groups_tenant_id_id` (with header comment "do not run in prod past 0008").

---

## Resolve query (sqlc, single roundtrip)

```sql
-- name: ResolveModelByAlias :one
-- Two-step in one query:
--   1. tenant-scoped active alias if exists (always wins)
--   2. tenant-disabled alias short-circuits (NEVER falls through to global) — D3 explicit deny
--   3. global alias if tenant policy `inherit_global_catalog = true`
WITH tenant_match AS (
    SELECT ma.id AS alias_id, ma.model_id, ma.status AS alias_status,
           ma.disabled_reason, ma.public_alias_display
    FROM model_aliases ma
    WHERE ma.tenant_id = sqlc.arg(tenant_id)
      AND ma.public_alias_normalized = sqlc.arg(alias_lower)
      AND ma.deleted_at IS NULL
      AND ma.scope = 'tenant'
    LIMIT 1
),
policy AS (
    SELECT inherit_global_catalog FROM model_registry_tenant_policies
    WHERE tenant_id = sqlc.arg(tenant_id)
),
global_match AS (
    SELECT ma.id AS alias_id, ma.model_id, ma.status AS alias_status,
           ma.disabled_reason, ma.public_alias_display
    FROM model_aliases ma
    WHERE ma.tenant_id IS NULL
      AND ma.scope = 'global'
      AND ma.public_alias_normalized = sqlc.arg(alias_lower)
      AND ma.deleted_at IS NULL
      AND NOT EXISTS (SELECT 1 FROM tenant_match)
      AND (SELECT coalesce(inherit_global_catalog, false) FROM policy)
    LIMIT 1
),
chosen AS (
    SELECT * FROM tenant_match
    UNION ALL
    SELECT * FROM global_match
    LIMIT 1
)
SELECT
    s.version                            AS snapshot_version,
    c.alias_id, c.model_id, c.alias_status, c.disabled_reason, c.public_alias_display,
    m.canonical_id, m.protocol_family, m.default_provider_model_id,
    m.default_context_window, m.default_request_timeout_ms,
    m.status                             AS model_status,
    coalesce(array_agg(distinct mc.capability) FILTER
        (WHERE mc.deleted_at IS NULL AND mc.enabled = true), ARRAY[]::text[]) AS capabilities,
    coalesce(array_agg(b.pool_group_id ORDER BY b.priority, b.id)
        FILTER (WHERE b.deleted_at IS NULL AND b.enabled = true
                AND (b.effective_from IS NULL OR b.effective_from <= now())
                AND (b.effective_until IS NULL OR b.effective_until > now())),
        ARRAY[]::bigint[]) AS pool_candidates
FROM chosen c
JOIN models m ON m.id = c.model_id
LEFT JOIN model_registry_snapshots s ON s.tenant_id = sqlc.arg(tenant_id)
LEFT JOIN model_pool_bindings b
       ON b.model_id = m.id
      AND (b.tenant_id = sqlc.arg(tenant_id) OR b.scope = 'global')
LEFT JOIN model_registry_capabilities mc
       ON mc.model_id = m.id
      AND (mc.tenant_id = sqlc.arg(tenant_id) OR mc.scope = 'global')
GROUP BY s.version, c.alias_id, c.model_id, c.alias_status, c.disabled_reason,
         c.public_alias_display, m.canonical_id, m.protocol_family,
         m.default_provider_model_id, m.default_context_window,
         m.default_request_timeout_ms, m.status;
```

Resolver maps:
- `alias_status='disabled'` OR `model_status='disabled'` → `ErrModelDisabled`
- empty result → `ErrUnknownModel`
- `pool_candidates = []` → `ErrTenantNoAccess`
- query error → `ErrRegistryBackend`

---

## Code structure

```
backend/internal/registry/
    registry.go           Interface + ResolvedModel re-exports
    postgres_registry.go  PostgresRegistry impl (SELECT-only)
    errors.go             4 error classes
    normalize.go          AliasNormalize() — strings.ToLower + UTF-8 NFC
    cache.go              No-op stub interface (Slice 5 fills in)
    registry_test.go                          (unit)
    postgres_registry_integration_test.go     (integration; ~14 cases)
backend/sql/queries/registry.sql
backend/sql/migrations/0008_model_registry.up.sql
backend/sql/migrations/0008_model_registry.down.sql
backend/sql/seed/registry_default.sql         (Phase C smoke seed)
docs/specs/_invariants/migrations.md          (NEW — down-migration discipline doc)
```

`internal/registry.Registry` interface:

```go
type Registry interface {
    ResolveModel(ctx context.Context, publicAlias string, tenantID int64) (router.ResolvedModel, error)
}
```

`router.ResolvedModel` adds:

```go
PoolCandidates  []int64  // ordered by binding priority; index 0 = primary
SnapshotVersion string   // "registry:<tid>:<v>;router:<router_policy_v>"
```

---

## Sequencing

### N+5a — additive only (smoke stays green via fallback)

1. Migration 0008 up + down.
2. `internal/registry/` package + sqlc query.
3. Add `PoolCandidates` + `SnapshotVersion` fields to `ResolvedModel`.
4. `cmd/gateway/main.go` constructs `registry.NewPostgresRegistry(q)`; threads into `deps.registry`.
5. Smoke seed adds models/aliases/bindings/capabilities/snapshot rows; existing body `pool_group_id` still accepted (no behavior change in N+5a).
6. Tests:
   - Unit: `TestRegistry_NormalizeAlias` (case + unicode), `TestRegistry_ErrorMapping`.
   - Integration: see test plan below.
7. Codex review pass before commit.
8. Owner review of N+5a diff before N+5b starts.

### N+5b — handler rewrite + escape-hatch removal (breaking)

1. Chat handler resolves via Registry; drops `chatRequest.PoolGroupID` JSON field.
2. Handler returns 400 `body_field_disallowed` when client sends `pool_group_id` (with `*int64` distinguishing absent vs zero).
3. Handler returns uniform 404 `model_not_available` for {Unknown, Disabled, NoAccess}; audit log records exact internal reason.
4. Router uses `req.Model.PoolCandidates[0]`; deletes `PlanWithPoolGroupID`, `errPoolGroupRequired`, `PlanInput.ExplicitPoolGroupID`.
5. `RoutePlan.AttemptBudget = 1` documented limitation (Slice 5 expands).
6. Smoke body drops `pool_group_id`; assertion added: `usage_records.snapshot_version` matches seeded `registry:<tid>:1`.
7. Codex review pass before commit.

### After N+5b ships

- Separate doc PR updates `docs/03_FEATURE_PARITY_MATRIX.md` per audit row 54.
- Backlog: write down-migration files for 0001–0007 (separate task).
- Slice 5 picks up: cache layer, weighted load-balance, typed-fallback execution.

---

## Integration test plan (registry_integration_test.go, ~14 cases)

| # | Test | Rationale |
|---|---|---|
| 1 | `HappyPath_TenantAlias` | Tenant has alias → ResolvedModel populated, `PoolCandidates[0]` matches binding |
| 2 | `UnknownAlias` | No tenant + no global → `ErrUnknownModel` |
| 3 | `DisabledAlias` | Tenant alias `status='disabled'` → `ErrModelDisabled` |
| 4 | `DisabledModel` | Alias active, `models.status='disabled'` → `ErrModelDisabled` |
| 5 | `TenantDisabledBlocksGlobal` | Tenant has alias `disabled` + global has alias `active` + `inherit_global_catalog=true` → `ErrModelDisabled` (NOT global hit). **Defends D3 explicit-deny invariant.** |
| 6 | `GlobalFallbackWhenPolicyAllows` | Tenant has no alias + global has alias + policy `inherit_global_catalog=true` → resolves to global |
| 7 | `GlobalIgnoredWhenPolicyDenies` | Tenant has no alias + global has alias + policy `inherit_global_catalog=false` → `ErrUnknownModel` |
| 8 | `NoBindings` | Alias resolves but binding rows empty → `ErrTenantNoAccess` |
| 9 | `MultipleBindingsOrderedByPriority` | Three bindings with priorities 100/50/200 → `PoolCandidates = [50_id, 100_id, 200_id]` |
| 10 | `EffectiveTimeWindowFiltersBindings` | Binding `effective_until < now()` → excluded from `PoolCandidates` |
| 11 | `SoftDeletedAliasInvisible` | `model_aliases.deleted_at NOT NULL` → `ErrUnknownModel` |
| 12 | `CaseInsensitiveAlias` | `ResolveModel(ctx, "Claude-3-5", 1)` and `ResolveModel(ctx, "claude-3-5", 1)` return same resolved model |
| 13 | `SnapshotVersionStamped` | `RoutePlan.SnapshotVersion` contains `registry:<tid>:<v>` matching seeded snapshot |
| 14 | `CrossTenantAliasIsolation` | Tenant 1 has `claude-3-5` → A; tenant 2 has `claude-3-5` → B; resolves correctly per tenant |

End-to-end smoke (`backend/cmd/gateway/smoke_test.go`):
- Body without `pool_group_id`, alias resolves, money path PG state assertions all pass.
- Negative: body WITH `pool_group_id` → expect 400 `body_field_disallowed`.

---

## CMB invariant audit

- **CMB-1 (Router does not read credentials)**: ✅ No credential field across any registry table. Resolved model carries pool group ids + capability strings + integer caps only.
- **CMB-2 (Pool does not compute cost)**: ✅ rpm_limit / tpm_limit / max_parallel_requests are integer rate-counts, not decimal cost; pricing_class is a string tag. Registry returns no decimal field.
- **CMB-3, 4** (adapter / ledger): unchanged.
- **CMB-5 (Credentials never logged)**: ✅ Audit log records alias normalized + tenant_id only.
- **CMB-6 (request_id / attempt_id)**: unchanged. Slice 2 stamps `snapshot_version` on usage_records (already-existing column from 0002 migration; if not present, additive ALTER is added in this slice).
- **CMB-7 (Layer write-discipline)**: ✅ `PostgresRegistry.ResolveModel` is SELECT-only. Snapshot increments are admin-writer Phase E; smoke test seeds via raw SQL.
- **New invariant**: rate-cap integer columns (rpm/tpm/max_parallel) are explicitly carved-out as CMB-2-safe in `docs/specs/_invariants/cross-module-boundaries.md` update (added in N+5a doc commit).

---

## Risk matrix

| Risk | Probability | Blast | Mitigation |
|---|---|---|---|
| Tenant-disabled alias gets silently re-enabled by global inheritance | Medium | per-tenant | Test #5 hard-asserts the explicit-deny invariant |
| Empty registry at boot 404s every chat request | High when tenant freshly created | per-tenant | Boot warn-log + smoke catches seed gaps; ops runbook for tenant onboarding |
| `selection_mode='priority_weighted'` set without Slice 5 weight executor | Medium | per-tenant | N+5a documents L0 always honors priority order; weighted selection is Slice 5 |
| LiteLLM rpm/tpm pattern citation `[Codex R2 only]` proves wrong on independent verification | Low | plan rewrite | Spec-leakage review fetches LiteLLM at cited commit before commit; if claim fails, rpm_limit/tpm_limit columns drop to "operator-defined contract caps; no specific upstream pattern claim" |
| Composite FK requires `uq_pool_groups_tenant_id_id` index that 0001 doesn't have | Verified | migration | 0008 creates index FIRST (additive); already in DDL above |
| Down migration drops shared index used by future 0009+ | Low | local-dev | `.down.sql` header comment + migrations.md doc warns |
| `model_owner` / `model_created_at` columns added but no consumer | Low | none | Cheap nullable columns; Phase E `/models` endpoint will use; documented in audit row 10/11 |
| Operator forgets to bump snapshot version on ad-hoc SQL change | High | per-tenant audit only | Phase E admin writer wraps the bump in a helper; smoke seed bumps explicitly. AdHoc SQL is documented as ops-discouraged |
| `inherit_global_catalog` default `false` means new tenants resolve nothing | Verified | per-tenant | Smoke documents per-tenant seed needed; no global default rows shipped in N+5a (only Phase C smoke seed) |
| `effective_from/until` filtering tested but no admin UI to set | Low | none at L0 | Columns nullable; default behavior unchanged; Phase E admin UI populates |
| Provider-model override on heterogeneous pool group breaks at runtime | Medium | per-binding | D23 — admin-write check; not a runtime registry concern |
| Snapshot int counter bigint overflows | Negligible | n/a | bigint range covers project lifetime |
| capability_params jsonb schema drift across versions | Low | tenant-visible only | Schema is documented per-capability in `docs/specs/registry/capabilities.md` (new doc, follow-up) |

---

## Source citations summary

**Verified by Claude via WebFetch on 2026-04-30T09:35Z**:
- `songquanpeng/one-api model/channel.go @ 3915ce9` — `Models string`, `ModelMapping *string`, `Priority *int64`, `ChannelStatusAutoDisabled` constant
- `Portkey-AI/gateway src/middlewares/requestValidator/schema/config.ts @ 9d9a37a` — `strategy.mode` enum, `targets` recursive `z.lazy`, `weight`, `retry`, `cache.{mode,max_age}`, `request_timeout`
- `envoyproxy/ai-gateway api/v1alpha1/ai_gateway_route.go @ v0.5.0` — `AIGatewayRouteRuleBackendRef.Weight` + `Priority` + `ModelNameOverride`; `Timeouts`; `LLMRequestCosts`
- `envoyproxy/ai-gateway api/v1alpha1/ai_service_backend.go @ v0.5.0` — `BackendRef`, `APISchema`

**Verified by Claude via WebFetch on 2026-04-30T09:50Z (LiteLLM round-2 follow-up)**:
- `BerriAI/litellm litellm/router.py @ 850fe59` — `self.model_group_alias` (alias→deployment group; note: actual name is `model_group_alias`, NOT `model_alias_map` as Codex R2 phrased — shape is correct, name corrected); `self.fallbacks` + `self.context_window_fallbacks` + `self.content_policy_fallbacks` (THREE distinct fallback classes — confirms D16 enum); `self.cooldown_cache` + `self.health_state_cache` + `self.allowed_fails` (cooldown excludes unhealthy deployments)
- `BerriAI/litellm litellm/proxy/_types.py @ 850fe59` — `KeyRequestBase.rpm_limit / tpm_limit / model_rpm_limit / model_tpm_limit`; `TeamBase.rpm_limit / tpm_limit`; `NewTeamRequest.model_rpm_limit / model_tpm_limit`; `UserAPIKeyAuth.rpm_limit_per_model / tpm_limit_per_model`; `GenerateRequestBase.max_parallel_requests`; `ConfigGeneralSettings.max_parallel_requests`. **Multi-scope rpm/tpm pattern confirmed at key, team, user, organization, project, budget, deployment scopes.**

**Already-mined ledger evidence rows used**:
- E-OAI-008 (per-channel model exposure)
- E-OAI-009, E-OAI-013 (auto-disable / health-driven)
- E-OAI-DEEP-009, E-OAI-DEEP-010, E-OAI-DEEP-011, E-OAI-DEEP-012, E-OAI-DEEP-015 (deep-dive rows cited by Codex round-2)
- E-LM-002 (cross-deployment fallback); E-LM-DEEP-001/009/010/012/013/014 (Codex round-2 cites)
- E-PK-001, E-PK-002, E-PK-005, E-PK-007 (Portkey behavior)
- E-NAI-003 (cross-format protocol translation), E-NAI-004 (reasoning-effort), E-NAI-006 (per-user-per-model rate limit)
- E-S2A-DEEP-006, E-S2A-DEEP-007, E-S2A-DEEP-009, E-S2A-DEEP-011, E-S2A-DEEP-012, E-S2A-DEEP-013, E-S2A-PROXY-025 (Codex round-2 cites)

**HUAKAI repo files informing the synthesis**:
- `docs/05_CLEAN_ROOM_POLICY.md` (lane discipline; Option B + Option C carve-out)
- `docs/06_REFERENCE_PROJECTS.md` (license tier table)
- `docs/07_REFERENCE_EVIDENCE_LEDGER.md` (lines 1–130 directly read; remainder via Codex citation)
- `docs/02_HUAKAI_FUSION_ARCHITECTURE.md` (3-tier blueprint + 8-souls + L0/L1/L2)
- `docs/specs/_invariants/cross-module-boundaries.md` (CMB-1/2/7 binding)
- `backend/internal/router/route_plan.go` + `default_router.go` + `router.go` + `router_test.go`
- `backend/internal/gatewayhttp/chat_completions_handler.go`
- `backend/internal/auth/api_key_resolver.go`
- `backend/sql/migrations/0001_pool_routing.up.sql` + `0007_l0_inbound_auth.up.sql`
- Round 1+2 Claude+Codex parallel plans (4 files)

---

## Appendix B — Current LLM official lineup verified (Owner directive 2026-04-30 "也要去看当前大模型官方的更新")

The schema is data-driven and bakes no model identities, but the seed data and `/admin/v1/models` admin endpoint (Phase E scope) MUST stay in sync with current upstream. Below are the authoritative model identifiers AS OF the cited fetch — to be re-fetched whenever Slice 2 catalog seeds change. No training-time recall used.

### Anthropic — verified WebFetch 2026-04-30T10:08Z @ `https://platform.claude.com/docs/en/docs/about-claude/models/overview`

| Tier | Claude API ID | Alias | Context | Max output | Capabilities |
|---|---|---|---|---|---|
| Current top | `claude-opus-4-7` | `claude-opus-4-7` | 1M tokens | 128k | adaptive_thinking; priority_tier; vision |
| Current mid | `claude-sonnet-4-6` | `claude-sonnet-4-6` | 1M tokens | 64k | extended_thinking; adaptive_thinking; priority_tier; vision |
| Current low | `claude-haiku-4-5-20251001` | `claude-haiku-4-5` | 200k tokens | 64k | extended_thinking; priority_tier; vision |
| Legacy | `claude-opus-4-6` / `claude-sonnet-4-5` / `claude-opus-4-5-20251101` / `claude-opus-4-1-20250805` | various aliases | 200k–1M | 32k–128k | extended_thinking |
| Deprecated (retire 2026-06-15) | `claude-sonnet-4-20250514` / `claude-opus-4-20250514` | `claude-sonnet-4-0` / `claude-opus-4-0` | 200k | 32k–64k | extended_thinking |

Implications for `protocol_family = 'anthropic_messages'`:
- Long-context tier (Opus 4.7 / Sonnet 4.6) supports 1M tokens — `models.default_context_window` must support int values up to 1_000_000 (current schema: `integer` is 32-bit, max ~2.1B — sufficient).
- Capability strings to standardize in operator docs: `vision`, `tools`, `prompt_caching`, `extended_thinking`, `adaptive_thinking`, `priority_tier`, `batch`. The `model_registry_capabilities.capability_params jsonb` column accommodates per-capability config like extended-thinking budget caps.

### OpenAI — verified WebFetch 2026-04-30T10:09Z @ `https://raw.githubusercontent.com/openai/openai-python/main/src/openai/types/shared/chat_model.py`

OpenAI's official `openai-python` SDK ChatModel Literal[] enum (78 ids; Stainless-generated from OpenAPI). Selected current ids:

| Family | Sample IDs |
|---|---|
| GPT-5.4 (current top) | `gpt-5.4`, `gpt-5.4-mini`, `gpt-5.4-nano`, `gpt-5.4-mini-2026-03-17`, `gpt-5.4-nano-2026-03-17` |
| GPT-5.3 | `gpt-5.3-chat-latest` |
| GPT-5.2 | `gpt-5.2`, `gpt-5.2-2025-12-11`, `gpt-5.2-chat-latest`, `gpt-5.2-pro`, `gpt-5.2-pro-2025-12-11` |
| GPT-5.1 | `gpt-5.1`, `gpt-5.1-2025-11-13`, `gpt-5.1-codex`, `gpt-5.1-mini`, `gpt-5.1-chat-latest` |
| GPT-5 | `gpt-5`, `gpt-5-mini`, `gpt-5-nano`, `gpt-5-2025-08-07`, …, `gpt-5-chat-latest` |
| GPT-4.1 | `gpt-4.1`, `gpt-4.1-mini`, `gpt-4.1-nano`, `gpt-4.1-2025-04-14` |
| o-series (reasoning) | `o4-mini`, `o4-mini-2025-04-16`, `o3`, `o3-2025-04-16`, `o3-mini`, `o1`, `o1-preview`, `o1-mini` |
| GPT-4o (multimodal) | `gpt-4o`, `gpt-4o-2024-11-20`, `gpt-4o-audio-preview-*`, `gpt-4o-search-preview-*`, `chatgpt-4o-latest`, `codex-mini-latest` |
| GPT-4 turbo | `gpt-4-turbo`, `gpt-4-turbo-preview`, `gpt-4-vision-preview` |
| Legacy GPT-3.5 | `gpt-3.5-turbo` and dated variants |

Implications:
- `protocol_family = 'openai_chat'` is the chat-completions endpoint; `protocol_family = 'openai_responses'` is the newer Responses endpoint (see also chatgpt-4o-latest / o-series). Both stay in the enum.
- The `chatgpt-4o-latest` / `gpt-5.3-chat-latest` style of "rolling latest" alias is exactly the use case `model_aliases` solves — operators alias `chatgpt-latest` → canonical model row, swap canonical id at deprecation.

### Gemini — verified WebFetch 2026-04-30T10:08Z @ `https://ai.google.dev/gemini-api/docs/models`

| Status | Model id | Family | Capabilities |
|---|---|---|---|
| Stable | `gemini-2.5-pro` | Gemini 2.5 | text, reasoning, code |
| Stable | `gemini-2.5-flash` | Gemini 2.5 | multimodal (text/image/video) |
| Stable | `gemini-2.5-flash-lite` | Gemini 2.5 | multimodal |
| Stable | `gemini-embedding-001` | Embeddings | text |
| Stable | `gemini-embedding-2` | Embeddings | multimodal (text/image/video/audio/PDF) |
| Preview | `gemini-3.1-pro` | Gemini 3.x | agentic + complex reasoning |
| Preview | `gemini-3-flash` | Gemini 3 | frontier-class lower-cost |
| Preview | `gemini-3.1-flash-live` | Gemini 3.x | low-latency streaming dialogue |

(Plus specialized: Veo 3.1 video, Imagen 4 image, Nano Banana, Computer Use, Robotics-ER — out of `chat`/`responses` scope.)

Implications:
- `protocol_family = 'gemini'` covers stable + preview ids.
- Gemini's docs page at fetch time DID NOT publish per-model context windows or token counts; admin seeding will need a follow-up specific-model fetch (e.g. `https://ai.google.dev/gemini-api/docs/models/gemini-2.5-pro`) to ground `default_context_window`. **Action item**: Phase E admin endpoint MUST require a per-model spec fetch before insert; no manual guessing.

### What this changes about Slice 2 N+5a

- **Schema**: nothing — stays data-driven.
- **Tests**: `TestPostgresRegistry_HappyPath` previously used `claude-3-5-sonnet-20241022` as the seeded provider model id. Refreshed to use `claude-opus-4-7` (current top tier per the verified fetch above) so the test doc-string stays grounded.
- **Synthesis plan example fragments**: any `claude-3-5-sonnet` example in earlier sections is a *shape* placeholder; the verified ids land in seed scripts only, not in HUAKAI runtime code.
- **Phase E admin endpoint spec (future slice)**: MUST link to this appendix and re-fetch on every catalog change. The "no training memory" rule travels forward.

---

## Closing

Lane: implementer (synthesis, post-round-2)
Agent: Claude (claude-opus-4-7)
UTC timestamps:
- Plan synthesized: 2026-04-30T09:38:00Z
- Codex N+5a P1/P2/P3 fixes applied: 2026-04-30T10:00:00Z
- Anthropic + Gemini + OpenAI lineup verified: 2026-04-30T10:08–10:09Z
Citation discipline: every reference-pattern claim above is sourced to a ledger row, file@commit, or fresh WebFetch URL+timestamp. No training-time recall. Per Owner directive 2026-04-30 "所有的动作都不允许凭借自己的记忆库知识" + "对了 你们除了看借鉴的项目。还要去看当前大模型官方的更新".

**N+5a implementation green; awaiting commit gate.**
