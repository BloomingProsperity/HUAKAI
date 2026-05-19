# 2026-04-30 N+5 Slice 2 Model Registry - Codex Round 2 Reference-Pattern Plan

| Field | Value |
| --- | --- |
| Lane | implementer, round 2 |
| Counterpart | Claude is drafting `docs/process/plans/2026-04-30-n5-model-registry-claude-v2.md` in parallel; I did not read it |
| Output | `docs/process/plans/2026-04-30-n5-model-registry-codex-v2.md` |
| Owner directive | "SECOND round that integrates reference-project feature-implementation patterns on top of the round-1 work." |
| Clean-room posture | Fresh source reads only from one-api, LiteLLM, Portkey, and Envoy AI Gateway. For sub2api/new-api/all-api-hub/helicone I used only existing evidence rows in `docs/07_REFERENCE_EVIDENCE_LEDGER.md`. |

## 0. Round-2 Position

Reference patterns change two material picks from my round-1 plan.

First, D3 should not be tenant-local only. Envoy's namespace default plus explicit cross-namespace grant pattern and Portkey's explicit inherited-target config both point to a safer middle ground: tenant-scoped resolution with an explicit, auditable global-catalog inheritance policy. No hidden sentinel tenant should silently route a tenant to global defaults, but operators do need a default catalog that new tenants can inherit without copying hundreds of rows.

Second, D4 should not expose disabled/no-binding state as a separate client HTTP status. The references split public error surfaces from operator diagnostics. HUAKAI should return one client-safe not-available error for unknown, disabled, and no-enabled-binding, while emitting precise audit reasons internally.

I keep my round-1 D2 and D6 picks: no stale TTL cache in L0, and a monotonic registry snapshot row per tenant. The reference evidence makes both stronger, not weaker. one-api's own public docs warn that cache can create freshness delay, while LiteLLM and Envoy assume active runtime config machinery that HUAKAI L0 does not yet have.

## 1. Reference-Pattern Grid

| DX | round-1 Claude pick | round-1 Codex pick | one-api pattern | LiteLLM pattern | Portkey pattern | Envoy AI Gateway pattern | sub2api/new-api evidence | round-2 pick |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| D1 schema shape | Normalized 3-table | Normalized 5-table with snapshot/capability tables | Channel rows carry model exposure and a separate ability-like mapping from group+model to channel; channel status propagates into model eligibility (`model/channel.go`, `model/ability.go`). | A public model group maps to multiple deployments, each with provider params and model metadata (`litellm/router.py`, `litellm/proxy/_types.py`). | Config schema is recursive: single target or target tree, with per-target retry/cache/weight (`src/middlewares/requestValidator/schema/config.ts`). | Route rules contain backend refs; backend refs carry weight, priority, and optional model-name override (`API Reference`, v1alpha1). | E-S2A-DEEP-006, E-S2A-DEEP-007 | Keep normalized 5-table. Add columns for reference-derived binding behavior instead of collapsing into one alias row. |
| D2 cache | In-process LRU TTL | No cache in L0 | Public docs say memory/Redis cache can reduce DB access but causes delayed freshness; use when DB latency needs it. | Router keeps live deployment lists and cooldown state in process; this assumes a config lifecycle around the router. | Response cache is a request feature with explicit simple/semantic modes, not a registry hot-path requirement. | Declarative resources are reconciled by controller/data plane, not ad hoc TTL lookup. | E-OAI-DEEP-009, E-PK-005 | No registry result cache in L0. Add snapshot version as the future invalidation key; only add cache when admin writer emits version bumps. |
| D3 tenant scoping | Tenant-specific with global fallback | Tenant-local only | Groups constrain model/channel eligibility, but one-api is not multi-tenant in HUAKAI's DR-001 sense. | Proxy/key/team config can restrict model access; model aliases may be global in process but access is still constrained by key/team config. | Config inheritance is explicit through target nesting, not an invisible global default. | Same-namespace is default; cross-namespace backend reference requires an explicit grant. | E-NAI-006, E-S2A-DEEP-006 | Tenant-scoped first, with explicit global-catalog inheritance per tenant. No unconditional sentinel fallback. |
| D4 disabled/unknown HTTP | Uniform 404 for unknown/disabled | 400 unknown, 403 disabled/no-access | Disabled channels are excluded from eligibility; user-facing errors center on no available channel rather than exposing full internal state. | Cooldown/unhealthy deployments are excluded; caller sees availability failure while internals track cooldown reason. | Config validation can be detailed; runtime fallback/exception handling separates gateway detail from response control. | CRD status exposes operator detail, while data plane routing hides backend internals from downstream clients. | E-S2A-PROXY-025 | Uniform client not-available status for unknown, disabled, no binding; audit reason distinguishes `unknown`, `disabled`, `no_binding`. |
| D5 body pool override | Delete public body field; future gated override | Reject public field | Admin/channel override exists but bypasses normal selection and changes retry behavior; this is an operator-only hazard. | Per-request config can override behavior, but it is still a router config surface, not arbitrary client pool IDs. | Request config can select targets only when authorized by Portkey config surface. | Backend refs are operator-declared resources. | E-OAI-DEEP-011, E-S2A-DEEP-009 | Delete public body `pool_group_id`. Future override must be operator/RBAC-gated, audited, and outside public chat body. |
| D6 snapshot version | Manual string on model row | Tenant snapshot table | Config and cache freshness warnings imply operators need a visible config version boundary. | Model list changes affect routing behavior; proxy/router config is a runtime object that needs version awareness if audited. | Header/config tree can vary per request, so effective config must be traceable if reused. | CRD metadata/status and generated resources make config generation auditable. | E-S2A-DEEP-011, E-OAI-DEEP-015 | Monotonic per-tenant registry version table, stamped into route/usage records. Writers increment in same transaction as registry changes. |
| D7 sequencing | Split additive registry then handler rewrite | Split N+5a/N+5b | n/a | n/a | n/a | n/a | n/a | Keep split. Reference additions increase blast radius; additive schema+registry should land before handler body-field removal. |
| D8 provider FK on model | No provider FK | No provider FK | Same model can be exposed by multiple channels; provider-specific translation can sit at channel/binding. | Same public model group can have OpenAI, Azure, or other deployments. | Different targets under one strategy can be different providers. | Backend ref can override model name per backend. | E-S2A-DEEP-006 | No provider FK on canonical model. Provider identity belongs to pool/account/binding, not model identity. |
| D9 binding-level provider model rename | Not explicit; Claude asked whether model-level was enough | Not explicit enough | one-api supports channel-level model remapping, so a public request model can become a different upstream model per channel. | Deployments under the same public group carry their own provider params and base-model metadata. | Target configs can override request params per target. | Backend ref has an optional backend model-name override. | E-NAI-003 | Add binding-level provider-model override, falling back to model default. This is required for OpenAI-vs-Azure-vs-Anthropic naming. |
| D10 per-deployment RPM/TPM caps | Deferred | Not in v1 schema | one-api supports quota/rate concepts but not cleanly per registry binding in evidence. | Deployment params and proxy key/team types expose RPM/TPM and max parallel request concepts. | Request config has retry/cache/timeout but not durable registry caps. | QuotaPolicy supports per-model quota concepts and failover when quota is exceeded. | E-NAI-006, E-LM-DEEP-012, E-LM-DEEP-013 | Store RPM/TPM/max-parallel caps as binding metadata; enforcement lives in rate/executor, not Pool cost logic. |
| D11 priority vs weight | Priority list | Rank only | Highest-priority bucket then random among equal priority is observed. | Router can apply strategies after filtering healthy deployments. | Load-balance uses target weights; fallback uses target order and status-code policy. | Backend refs carry both weight and priority. | E-S2A-DEEP-007 | Support both: lower priority tier first, weight within tier. L0 executes deterministic ordered primary only; weighted fanout deferred. |
| D12 health-aware registry filtering | Not explicit | Registry returns active bindings only | Disabled status updates eligibility; health checks can disable channels. | Cooldown removes unhealthy deployments at routing time. | Circuit/fallback behavior belongs in runtime routing. | Endpoint picker handles dynamic endpoint health below declared backend refs. | E-S2A-DEEP-012, E-S2A-DEEP-013 | Registry filters only administrative disabled/deleted rows. Dynamic health remains Router/Pool/Rate input, not registry DB filtering. |
| D13 cache invalidation source | TTL now, outbox later | Version table now | Cache docs warn about staleness; if cache exists it needs sync frequency. | Router config object implies reload/event discipline. | Cache TTL is explicit per config. | Controller reconciliation is the event source. | E-OAI-DEEP-009 | Registry version bump is the primary invalidation source; scheduler_outbox event is a later delivery mechanism. |
| D14 alias normalization | Case-insensitive | Not explicit | Public model strings are config values; evidence does not require mixed-case distinction. | Model groups are logical identifiers; duplicate spelling would fragment deployment pools. | Config schema validates names but does not imply case-sensitive business semantics. | Model header matching should be normalized before route match. | E-NAI-003 | Store normalized alias for lookup and original alias for audit/display. |
| D15 capability and protocol envelope | Exact capabilities | Capabilities table | one-api exposes per-channel model allow-list, not full capability matrix. | Provider translation and deployment metadata require mode/base-model awareness. | Target config includes guardrails, cache, timeout, and provider-specific auth/config. | Route/backend declares schema, model ownership, body/header mutations, and request cost extraction. | E-NAI-003, E-NAI-004, E-PK-004 | Keep capability table and add capability parameters for protocol family, multimodal, tools, reasoning, and cache-token reporting. |

## 2. Behavior Obligations Missed By Round 1

### one-api obligations

1. Per-binding model rename is mandatory, not a nice-to-have. one-api has channel-level model mapping (`model/channel.go`), and the README describes request-model redirection. HUAKAI should model this as `model_registry_pool_bindings.provider_model_id_override`, with model-level default only as fallback.

2. Model eligibility is not only alias-to-model. one-api's ability mapping ties group, model, channel, status, and priority (`model/ability.go`). HUAKAI should keep alias resolution separate from binding eligibility so tenant, group, and disabled-state decisions remain auditable.

3. Priority bucket plus random/equal distribution is a real baseline. Evidence E-OAI-DEEP-010 says highest priority bucket wins and equal priority is randomized. HUAKAI should not implement pure fixed rank forever; Slice 2 can store `priority` and `weight`, while L0 executes first candidate only.

4. Channel status and ability status move together. one-api updates model eligibility when channel status changes (`model/channel.go`, `model/ability.go`). HUAKAI should ensure future admin writers update binding status and registry version atomically, not leave alias active with no route.

5. Caching is an operational choice, not a default correctness mechanism. one-api docs explicitly warn about cache-induced delay. HUAKAI should not use TTL cache as the first correctness boundary for registry changes.

### LiteLLM obligations

1. A public model group can have multiple deployments with distinct provider parameters. HUAKAI's registry cannot stop at one canonical model row plus one provider model string; binding-specific provider model override and provider metadata are required.

2. RPM/TPM and max parallel settings exist at the deployment/key/team boundary (`litellm/router.py`, `litellm/proxy/_types.py`). HUAKAI should store declarative caps on the model binding and allow user/team model caps later, then enforce them in rate/executor.

3. Fallback is typed and bounded, not just "try next". E-LM-DEEP-009 and E-LM-DEEP-010 require classified fallback branches and maximum depth. Slice 2 should store enough metadata to let Slice 3 distinguish normal fallback, context-window fallback, and safety fallback.

4. Cooldown excludes unhealthy deployments but with exceptions and policy ladders. E-LM-DEEP-001 through E-LM-DEEP-006 and E-LM-DEEP-014 imply registry must not be the sole health filter; dynamic health needs runtime state and reason codes.

5. Deployment-level concurrency semaphores are cache-like runtime state. E-LM-DEEP-012 warns that in-process semaphores do not solve multi-node coordination. HUAKAI should persist caps as metadata now but defer counters to a distributed rate/slot implementation.

### Portkey obligations

1. Routing target trees are recursive. Portkey's config schema allows nested targets with strategies and inherited behavior. HUAKAI Slice 2 need not implement recursive rule chains, but binding metadata should not block Phase E config-as-code routing.

2. Target weight is a first-class config property (`src/middlewares/requestValidator/schema/config.ts`). HUAKAI should add `weight` now even if L0 does not yet execute weighted load balancing.

3. Cache mode is explicit and separable from routing. Portkey treats cache as a request/target config with simple and semantic modes, which reinforces not mixing response cache with model registry cache.

4. Retry has attempts, status-code triggers, and retry-after behavior (`src/middlewares/requestValidator/schema/config.ts`; E-PK-001, E-PK-002). HUAKAI binding should carry fallback/retry classification labels only; executor owns attempts.

5. Request timeout is part of target policy. `request_timeout` exists in Portkey config, and F-TIMEOUT-001 already wants per-model defaults. Slice 2 should include room for model/binding timeout policy or at least not preclude it.

### Envoy AI Gateway obligations

1. Declarative route is upstream of runtime routing. Envoy AI Gateway's AIGatewayRoute combines rules and backend refs; HUAKAI's registry tables are the relational equivalent of the declarative route contract.

2. Backend refs support both `weight` and `priority`. HUAKAI should not force a choice between strict priority and weighted distribution; both are observed reference patterns.

3. Backend model-name override exists at the backend reference. HUAKAI should implement binding-level provider model override in Slice 2, not defer it.

4. Model metadata for `/models` can be derived from route config. Envoy API reference exposes ownership/created-at style metadata for model listing. HUAKAI should store `model_owner` and `model_created_at` or defer them explicitly as N+6/N+7 metadata.

5. QuotaPolicy shows token quota can drive failover to a different backend. HUAKAI should separate declarative limits from enforcement counters and preserve enough binding IDs for later per-backend quota failover.

## 3. Re-Arbitration Of D2/D3/D4/D6

### D2 cache

Round-2 pick: no registry result cache in L0.

I keep my round-1 pick. The deciding evidence is one-api's cache freshness warning, Portkey treating cache as an explicit request/response feature, and Envoy's controller-style reconciliation. LiteLLM can keep a live router object because its process owns router configuration and cooldown state. HUAKAI L0 only has Postgres plus smoke tests; a 30-second stale registry cache would make model-disable and binding changes observably wrong without an invalidation channel.

Implementation consequence: write `model_registry_snapshots.version`, stamp it into `RoutePlan.SnapshotVersion`, and design future cache key as `(tenant_id, alias_normalized, registry_version)`. Do not implement TTL cache until an admin writer increments the version and emits an outbox event.

### D3 tenant scoping

Round-2 pick: tenant-scoped rows with explicit global-catalog inheritance.

This changes from my round-1 tenant-local-only pick. Envoy's cross-namespace rule is the reference pattern that matters: local namespace by default, cross-namespace only when explicitly authorized. Portkey's config inheritance is also explicit and visible in the target tree. So HUAKAI should support a global catalog, but a tenant uses it only when `model_registry_tenant_policies.inherit_global_catalog = true`.

Implementation consequence: use nullable `tenant_id` for global catalog rows or a separate `catalog_scope`, but never a magic tenant id that can collide with real tenancy. The resolver first checks tenant rows; if none exist and policy allows inheritance, it checks global rows. The audit result records whether the winner was tenant-local or global-inherited.

### D4 disabled/unknown HTTP

Round-2 pick: uniform client-safe not-available response; operator audit has exact reason.

This changes from my round-1 public 403 for disabled/no-access. E-S2A-PROXY-025 already requires client-safe errors plus operator diagnostics. Envoy exposes detail in resource status, not necessarily in downstream request errors. Portkey validates config sharply for operators, but runtime fallback should not leak target internals. For HUAKAI public chat API, unknown alias, disabled model/alias, and no enabled binding should all return one `model_not_available` class.

Implementation consequence: Registry returns typed internal errors (`unknown`, `disabled`, `no_binding`, `backend`), handler maps the first three to one client status/body, and writes audit/structured log fields for the exact internal reason.

### D6 snapshot version

Round-2 pick: monotonic per-tenant registry version table, incremented by writers.

I keep my round-1 pick. Manual model strings are human-readable but insufficient once binding-level overrides, caps, weights, and global inheritance enter the design. A derived hash is attractive but brittle across SQL ordering and future columns. Envoy's declarative resources and generated status point to a config-generation boundary; HUAKAI's relational equivalent is a tenant registry version.

Implementation consequence: `model_registry_snapshots(tenant_id, version, updated_at, updated_by_actor, reason)` is authoritative. Any admin writer that changes models, aliases, capabilities, bindings, or tenant inheritance policy must bump the version in the same transaction.

## 4. New Decision Points

### D9. Provider model rename location

Default: binding-level override with model-level default.

Reason: one-api and Envoy both show that the upstream model name can depend on the target channel/backend. A model row cannot represent all provider naming differences. Store a default provider model on the model row for simple cases, but let each binding override it.

### D10. RPM/TPM cap location

Default: declarative caps on model binding; counters outside Registry and outside Pool cost logic.

Reason: LiteLLM puts RPM/TPM close to deployment configuration, while New API evidence E-NAI-006 requires per-user-per-model limits. HUAKAI should avoid dual source of truth by making registry binding caps the model-deployment source and adding user/team overlays later in rate policy. Pool must not compute cost or token budget; the executor/rate gate enforces caps before claim/forward.

### D11. Traffic split mode

Default: priority tier first, weight within the tier.

Reason: one-api's priority bucket, Portkey's weighted targets, and Envoy's priority+weight backend refs converge. L0 can emit candidates ordered by priority and stable id. Slice 3 can add deterministic weighted selection using request id as the seed so stickiness and replay remain explainable.

### D12. Dynamic health filtering

Default: Registry filters only static admin state; dynamic health is Router/Pool/Rate input.

Reason: Sub2API evidence says availability combines probe, credential, quota, runtime, and temporary failure states. LiteLLM cooldown excludes runtime-unhealthy deployments. If registry silently filters on dynamic health, HUAKAI creates split brain between registry, pool, and rate modules.

### D13. Registry invalidation event source

Default: registry version bump is the source of truth; outbox is a delivery channel.

Reason: Envoy has controller reconciliation, LiteLLM has router config reload assumptions, and one-api cache needs sync frequency. HUAKAI should model invalidation as data version first. A scheduler_outbox event can later tell processes to drop cache entries, but correctness must not depend on receiving that event.

### D14. Alias normalization

Default: normalized alias lookup plus display alias.

Reason: Public model names are operational identifiers, not intended as case-sensitive secrets. Store original display string for logs and `/models`, but use normalized lookup to avoid support incidents from casing drift.

### D15. Capability envelope

Default: capability rows with optional parameters, not a flat text array only.

Reason: New API evidence requires explicit protocol conversion loss; Portkey and Envoy expose request mutation, schema, cache, and cost metadata. HUAKAI needs capability entries that can say "tools supported", "vision supported", "reasoning effort accepted", and "cache token reporting supported", not just a comma-separated list.

## 5. Schema Impact Against Round-1 Codex 5-Table Proposal

Round 1 already proposed:

- `model_registry_snapshots`
- `model_registry_models`
- `model_registry_aliases`
- `model_registry_capabilities`
- `model_registry_pool_bindings`

Round 2 keeps those tables and adds the following deltas.

```sql
-- Explicit tenant inheritance policy. This replaces magic tenant_id=0 fallback.
CREATE TABLE IF NOT EXISTS model_registry_tenant_policies (
    tenant_id                 bigint PRIMARY KEY,
    inherit_global_catalog    boolean NOT NULL DEFAULT false,
    effective_global_version  bigint,
    updated_at                timestamptz NOT NULL DEFAULT now(),
    updated_by_actor          text,
    CONSTRAINT fk_registry_policy_snapshot
        FOREIGN KEY (tenant_id)
        REFERENCES model_registry_snapshots(tenant_id)
);

ALTER TABLE model_registry_snapshots
    ADD COLUMN IF NOT EXISTS reason text,
    ADD COLUMN IF NOT EXISTS updated_by_actor text;

ALTER TABLE model_registry_models
    ADD COLUMN IF NOT EXISTS default_provider_model_id text,
    ADD COLUMN IF NOT EXISTS model_owner text NOT NULL DEFAULT 'HUAKAI',
    ADD COLUMN IF NOT EXISTS model_created_at timestamptz,
    ADD COLUMN IF NOT EXISTS default_context_window integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS default_request_timeout_ms integer;

ALTER TABLE model_registry_aliases
    ADD COLUMN IF NOT EXISTS public_alias_normalized text,
    ADD COLUMN IF NOT EXISTS display_alias text,
    ADD COLUMN IF NOT EXISTS disabled_reason text,
    ADD COLUMN IF NOT EXISTS scope text NOT NULL DEFAULT 'tenant'
        CHECK (scope IN ('tenant', 'global')),
    ADD COLUMN IF NOT EXISTS source text NOT NULL DEFAULT 'operator';

CREATE UNIQUE INDEX IF NOT EXISTS uq_registry_alias_tenant_normalized
    ON model_registry_aliases (tenant_id, public_alias_normalized)
    WHERE deleted_at IS NULL AND scope = 'tenant';

CREATE UNIQUE INDEX IF NOT EXISTS uq_registry_alias_global_normalized
    ON model_registry_aliases (public_alias_normalized)
    WHERE deleted_at IS NULL AND scope = 'global';
```

The resolver contract for aliases is:

1. Lookup `(tenant_id, public_alias_normalized)` active row.
2. If no tenant row and `inherit_global_catalog=true`, lookup global row.
3. If a tenant disabled row exists, do not fall back to global. Tenant disable is an explicit deny.

Binding deltas:

```sql
ALTER TABLE model_registry_pool_bindings
    ADD COLUMN IF NOT EXISTS priority integer NOT NULL DEFAULT 100,
    ADD COLUMN IF NOT EXISTS weight integer NOT NULL DEFAULT 1 CHECK (weight > 0),
    ADD COLUMN IF NOT EXISTS selection_mode text NOT NULL DEFAULT 'priority_weighted'
        CHECK (selection_mode IN ('strict_priority', 'priority_weighted')),
    ADD COLUMN IF NOT EXISTS provider_model_id_override text,
    ADD COLUMN IF NOT EXISTS rpm_limit integer CHECK (rpm_limit IS NULL OR rpm_limit >= 0),
    ADD COLUMN IF NOT EXISTS tpm_limit integer CHECK (tpm_limit IS NULL OR tpm_limit >= 0),
    ADD COLUMN IF NOT EXISTS max_parallel_requests integer
        CHECK (max_parallel_requests IS NULL OR max_parallel_requests >= 0),
    ADD COLUMN IF NOT EXISTS fallback_class text NOT NULL DEFAULT 'normal'
        CHECK (fallback_class IN ('normal', 'context_window', 'safety', 'quota', 'manual')),
    ADD COLUMN IF NOT EXISTS disabled_reason text,
    ADD COLUMN IF NOT EXISTS effective_from timestamptz,
    ADD COLUMN IF NOT EXISTS effective_until timestamptz;

CREATE INDEX IF NOT EXISTS idx_registry_binding_resolve_v2
    ON model_registry_pool_bindings
        (tenant_id, alias_id, enabled, priority, weight)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_registry_binding_caps
    ON model_registry_pool_bindings
        (tenant_id, alias_id, rpm_limit, tpm_limit)
    WHERE deleted_at IS NULL
      AND (rpm_limit IS NOT NULL OR tpm_limit IS NOT NULL);
```

Capability deltas:

```sql
ALTER TABLE model_registry_capabilities
    ADD COLUMN IF NOT EXISTS capability_value text,
    ADD COLUMN IF NOT EXISTS capability_params jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN IF NOT EXISTS source text NOT NULL DEFAULT 'operator';

CREATE INDEX IF NOT EXISTS idx_registry_capability_lookup_v2
    ON model_registry_capabilities (tenant_id, model_id, capability, enabled)
    WHERE deleted_at IS NULL;
```

Snapshot writer invariant:

```sql
-- Every admin mutation that touches registry state must do this in the same transaction.
UPDATE model_registry_snapshots
SET version = version + 1,
    updated_at = now(),
    updated_by_actor = $actor,
    reason = $reason
WHERE tenant_id = $tenant_id;
```

If a global catalog row changes, admin code either bumps a global snapshot and records affected tenants, or bumps every inheriting tenant's snapshot. For L0, choose the simpler safe version: bump every inheriting tenant in the same transaction. If that becomes too expensive, move to an explicit global version in N+6.

## 6. CMB Invariant Audit

### CMB-1: Router does not read credentials

The registry returns public alias, canonical model id, provider-model override string, protocol family, capability rows, binding ids, pool group ids, priority, weight, and declarative caps. None of those are provider credentials. Router receives only `ResolvedModel` metadata and never imports auth or provider account credential code.

No violation.

### CMB-2: Resource Pool does not compute cost

The new RPM/TPM/max-parallel fields are metadata caps, not decimal cost fields. They do not say what a token costs; they say a binding should not exceed a declared request/token rate. Pool still returns a lease and does not price usage. Enforcement should happen in a future rate/executor gate before Pool claim, or in Ledger reservation for money-grade limits.

No violation, with one guardrail: do not put `estimated_cost`, `price`, `decimal`, or spend fields on Pool lease or Pool selection structs.

### CMB-7: Router writes nothing; Pool writes only slot row; Ledger writes everything else

Registry remains read-only at request time. Snapshot version increments are admin writer operations, not `ResolveModel` operations. Router plans attempts and writes no DB rows. Pool writes only slot/acquisition state. Ledger writes reserve/settle/usage/audit money-path rows.

No violation, as long as `PostgresRegistry.ResolveModel` is SELECT-only and admin mutation code lives outside `internal/registry` request path.

## 7. Risk Matrix Expansion

| Risk | Probability | Blast radius | Mitigation |
| --- | --- | --- | --- |
| Binding-level RPM/TPM and user/team per-model caps become dual sources of truth. | medium | per-tenant | Registry stores deployment caps only; rate policy later stores user/team overlays with documented precedence: user/team lower cap wins unless operator override is explicit. |
| Global-catalog inheritance silently re-enables a model a tenant meant to block. | medium | per-tenant | Tenant disabled alias is an explicit deny and prevents fallback. Add test: tenant disabled row blocks global active row. |
| Priority+weight fields create the impression of load balancing before executor supports it. | high | per-tenant | L0 documentation and tests assert only first attempt executes. RoutePlan can include all candidates but `AttemptBudget=1` until Executor slice. |
| Binding-level provider model override can hide protocol incompatibility. | medium | per-tenant | Resolver must validate capability/protocol family against binding provider family; unsupported override returns operator-visible config error before traffic. |
| Registry version bump is forgotten by manual SQL or early admin code. | high | per-tenant | Make all admin writers use one helper transaction; add acceptance test that each mutation changes snapshot. For manual SQL, require smoke seed to update snapshot explicitly. |
| Dynamic health duplicated between registry and pool. | medium | global if shared pools | Registry filters only static state. Runtime health fields are out of registry schema for Slice 2. |
| Cache introduced later without version key. | medium | per-node/per-tenant | Future cache API must require registry version in key. Reject TTL-only cache in review. |
| Wide schema lands without admin UI support and operators must hand-edit rows. | medium | per-tenant | Provide idempotent seed SQL and a minimal operator runbook; admin API/UI remains N+6/Phase E but schema invariants are testable. |
| Binding caps violate expectation that Pool owns concurrency. | low | per-tenant | Store max-parallel metadata at binding but enforce in rate/executor. Pool continues to enforce account slot availability only. |
| Envoy-style global/route cost extraction tempts cost fields into registry. | medium | global money path | Registry stores `pricing_class` and capability flags only. Decimal pricing stays in billing policy tables later. |

## 8. Final Synthesis Recommendation

If Owner says "by-you-decide", I would ship this:

1. Keep the 5-table normalized registry from Codex v1, plus `model_registry_tenant_policies`.
2. Use explicit global-catalog inheritance, not magic tenant id 0 and not tenant-local-only duplication.
3. Add binding-level provider model override, priority, weight, RPM, TPM, max parallel, fallback class, and disabled reason.
4. Add alias normalization and display alias.
5. Keep request-time registry SELECT-only and no registry cache in L0.
6. Add `registry:<tenant_id>:<version>` snapshot stamping.
7. Handler returns one public not-available error for unknown/disabled/no-binding, with audit reason internally.
8. Delete public request-body `pool_group_id`; future operator override is separate and gated.

Concrete code structure:

```text
backend/internal/registry/
  registry.go              # interface and result structs
  postgres_registry.go     # SELECT-only resolver
  errors.go                # typed internal errors
  normalize.go             # alias normalization
  postgres_registry_test.go

backend/sql/migrations/
  0008_model_registry.up.sql
  0008_model_registry.down.sql

backend/sql/queries/
  registry.sql
```

Execution order:

1. N+5a additive schema, sqlc queries, registry package, unit/integration tests.
2. N+5a seed updates for smoke, but handler still works through old path until registry tests pass.
3. Codex review before commit.
4. N+5b handler/router rewrite, body field removal, route plan snapshot stamping, smoke update.
5. Codex review before commit.

Deferred to N+6 or Phase E:

- Weighted candidate execution.
- Typed fallback execution after partial stream output.
- Admin API/UI for registry writes.
- Distributed RPM/TPM counters.
- Response cache and semantic cache.
- Full `/models` endpoint metadata.
- Config-as-code import/export for route trees.

## 9. Feature-Parity Items For Parity Matrix

Do not modify `docs/03_FEATURE_PARITY_MATRIX.md` in this work unit. After Slice 2 ships, synthesis should update these rows:

| Capability ID | Suggested status after Slice 2 ships | Why |
| --- | --- | --- |
| F-GW-001 | Implemented | Public model string resolves to provider/pool route without client knowing internal pool group. |
| F-CH-001 | Designed, partially implemented | Per-channel/per-pool model allow-list becomes a registry binding design; full channel CRUD/admin still later. |
| F-GW-004 | Designed | Registry stores ordered/weighted candidates and fallback class, but executor fallback is not shipped in Slice 2. |
| F-TIMEOUT-001 | Designed | Binding/model timeout column or reserved schema path supports per-model/per-binding timeout. |
| F-CACHE-001 | Designed, deferred | Round 2 explicitly separates response cache from registry cache and preserves TTL/invalidation requirements. |
| F-PROTO-002 | Designed | Capability envelope and provider model override prevent silent cross-protocol loss. |
| F-MODEL-001 | Designed | Capability rows can express reasoning-effort support; actual request parameter enforcement remains later. |
| F-SEC-004 | Designed | Per-user-per-model limits need later rate policy; registry binding caps provide deployment side of the model dimension. |
| F-RATE-001 | Designed | Binding-level RPM/TPM and fallback class provide metadata needed for upstream rate cooldown/failover. |
| F-POOL-001 | Implemented foundation | Registry maps model aliases to pool groups, but intra-pool account selection remains existing Pool responsibility. |
| F-ROUTE-001 | Designed | Priority+weight plus future observed health/cost metadata prepare performance-aware routing. No dynamic cost routing yet. |
| F-CONFIG-001 | Designed | Registry schema becomes the DB-backed form of declarative model route config; YAML/UI import remains later. |
| F-ROUTE-002 | Designed | Binding to pool group preserves endpoint-picker extension point; actual endpoint picker stays Pool/Phase E. |

## 10. Open Questions For Owner/Synthesis

1. Should global catalog rows use `tenant_id NULL + scope='global'`, or should they live in a separate `model_registry_global_*` table family? I prefer nullable + scope because it keeps query shape unified, but separate tables make tenant/global distinction physically impossible to confuse.

2. Should N+5a include a minimal admin writer helper solely to enforce snapshot bumps, or should N+5a stay schema+read-only and seed SQL increments snapshots manually? I prefer helper tests if small, but it may be outside Slice 2's intended implementation size.

3. Should uniform client not-available map to HTTP 400 or 404? I prefer 404 because "not available to this tenant" is closer to resource absence and reduces enumeration. If compatibility with OpenAI-style invalid model errors matters more, use 400 with the same body for all three cases.

4. Should `rpm_limit`/`tpm_limit` be on binding columns or a child table? I prefer columns for L0 simplicity. If caps need multiple windows, move to child table in N+6.

5. Should `provider_model_id_override` be allowed when the binding targets a pool group containing heterogeneous providers? I prefer allowing it only when the pool group has a declared provider family, otherwise require per-account provider override later.

Source files read:
  HUAKAI repo: docs/process/plans/2026-04-30-n5-model-registry-claude.md; docs/process/plans/2026-04-30-n5-model-registry-codex.md; docs/07_REFERENCE_EVIDENCE_LEDGER.md; docs/06_REFERENCE_PROJECTS.md; docs/05_CLEAN_ROOM_POLICY.md; docs/02_HUAKAI_FUSION_ARCHITECTURE.md; docs/specs/_invariants/cross-module-boundaries.md; docs/03_FEATURE_PARITY_MATRIX.md
  Reference (MIT): songquanpeng/one-api model/channel.go main (latest release anchor v0.6.10 @ 3915ce9); songquanpeng/one-api model/ability.go main (latest release anchor v0.6.10 @ 3915ce9); BerriAI/litellm litellm/router.py main (latest release anchor v1.83.9-nightly @ 850fe59); BerriAI/litellm litellm/proxy/_types.py main (latest release anchor v1.83.9-nightly @ 850fe59); Portkey-AI/gateway src/middlewares/requestValidator/schema/config.ts main (latest release anchor v1.14.3 @ 9d9a37a); envoyproxy/ai-gateway docs API Reference v1alpha1 latest (latest release anchor v0.5.0 @ b40501f)
  Reference (already-mined evidence rows): E-OAI-001, E-OAI-008, E-OAI-009, E-OAI-DEEP-009, E-OAI-DEEP-010, E-OAI-DEEP-011, E-OAI-DEEP-012, E-OAI-DEEP-015, E-OAI-DEEP-016, E-LM-002, E-LM-DEEP-001, E-LM-DEEP-009, E-LM-DEEP-010, E-LM-DEEP-012, E-LM-DEEP-013, E-LM-DEEP-014, E-PK-001, E-PK-002, E-PK-005, E-PK-007, E-NAI-003, E-NAI-004, E-NAI-006, E-S2A-DEEP-006, E-S2A-DEEP-007, E-S2A-DEEP-009, E-S2A-DEEP-011, E-S2A-DEEP-012, E-S2A-DEEP-013, E-S2A-PROXY-025
Clean-room declaration: "I read MIT reference source for behavior extraction only.
  I did NOT read source from sub2api / new-api / all-api-hub / helicone.
  I did not copy verbatim names, schemas, comments, or algorithms."
Lane: implementer (round 2)
Agent: Codex
UTC timestamp: 2026-04-30T09:29:56Z
