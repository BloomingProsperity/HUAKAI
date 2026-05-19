# 2026-04-30 N+5 Slice 2 Model Registry - Codex Independent Plan

| Field | Value |
| --- | --- |
| Lane | specifier (independent planner) |
| Output | `docs/process/plans/2026-04-30-n5-model-registry-codex.md` |
| Counterpart | Claude may draft `2026-04-30-n5-model-registry-claude.md` in parallel; I did not read it |
| Work unit | Replace request-body `pool_group_id` routing with Registry -> Router model resolution |
| Clean-room state | HUAKAI repo files only; no non-MIT reference source read |

## 0. Current State Observed

N+4 L0 auth is already present as `0007_l0_inbound_auth.up.sql`, `auth.APIKeyResolver`, and table-backed auth wiring in `cmd/gateway/main.go`. The latest migration style is still forward-only `.up.sql`; there is no `0007_l0_inbound_auth.down.sql`, so the requested `0008_model_registry.down.sql` needs an Owner decision on whether down files become standard or are local rollback aids only.

Router already has `ResolvedModel` and `RoutePlan.SnapshotVersion`, but `ResolvedModel` has no pool-candidate field. `DefaultRouter.Plan` still depends on `PlanInput.ExplicitPoolGroupID`, and `PlanWithPoolGroupID` remains as a Phase C escape hatch. The chat handler currently bypasses Router entirely: it parses `pool_group_id` from JSON, passes it to `billing.ReserveRequest.PoolingGroupID`, then passes it to `pool.SelectionRequest.PoolGroupID`. The smoke test also posts `pool_group_id` in the body. Slice 2 should remove that client-controlled routing path.

CMB binding: Registry is read-only, Registry feeds Router metadata only, Router does not read credentials, Pool does not compute cost, and the request order is Auth -> Registry -> Router -> Pool/Adapter/Ledger.

## 1. Schema Migration `0008_model_registry`

### Default Pick: Lightly Normalized

I recommend normalized tables rather than one denormalized `model_aliases` table:

1. `model_registry_snapshots`
2. `model_registry_models`
3. `model_registry_aliases`
4. `model_registry_capabilities`
5. `model_registry_pool_bindings`

Why: aliases, capabilities, and ordered pool candidates have different lifecycles. Two aliases may point to the same canonical model but use different pool orderings. A separate binding table also lets Router emit future fallback attempts without parsing arrays.

### Tenant Scoping

Default: tenant-scoped rows only, no global fallback in the request hot path.

Rationale: existing schema style follows DR-001 tenant-owned business tables; tenant-local rows avoid accidental global fallback after a tenant disables or narrows access. A future admin/template job can copy catalog defaults into a tenant, but `ResolveModel` should query one tenant only.

### Proposed Shape

Use existing migration conventions: `bigserial`, non-null `tenant_id`, status/enabled, timestamps, soft delete, partial indexes.

Core fields:

```sql
model_registry_snapshots(
  id, tenant_id, version bigint not null default 1, updated_at, updated_by_actor,
  unique(tenant_id)
)

model_registry_models(
  id, tenant_id, canonical_model_id, provider_model_id, protocol_family,
  context_window int default 0, pricing_class text default 'standard',
  status text check in ('active','disabled','deleted'), timestamps, deleted_at
)

model_registry_aliases(
  id, tenant_id, model_id, public_alias,
  status text check in ('active','disabled','deleted'), timestamps, deleted_at
)

model_registry_capabilities(
  id, tenant_id, model_id, capability, enabled boolean default true,
  created_at, deleted_at
)

model_registry_pool_bindings(
  id, tenant_id, alias_id, pool_group_id, rank int check(rank >= 1),
  enabled boolean default true, reason text default 'primary',
  timestamps, deleted_at
)
```

To prevent cross-tenant binding, add composite uniqueness and FKs:

```sql
CREATE UNIQUE INDEX uq_pool_groups_tenant_id_id ON pool_groups (tenant_id, id);
CREATE UNIQUE INDEX uq_registry_models_tenant_id_id ON model_registry_models (tenant_id, id);
CREATE UNIQUE INDEX uq_registry_aliases_tenant_id_id ON model_registry_aliases (tenant_id, id);
```

Then FK `(tenant_id, model_id)`, `(tenant_id, alias_id)`, and `(tenant_id, pool_group_id)` to the matching composite keys.

Hot-path indexes:

```sql
CREATE UNIQUE INDEX uq_registry_alias_tenant_public
  ON model_registry_aliases (tenant_id, public_alias)
  WHERE deleted_at IS NULL;

CREATE INDEX idx_registry_alias_hot
  ON model_registry_aliases (tenant_id, public_alias, status)
  WHERE deleted_at IS NULL;

CREATE INDEX idx_registry_binding_alias_rank
  ON model_registry_pool_bindings (tenant_id, alias_id, rank)
  WHERE deleted_at IS NULL AND enabled = true;

CREATE UNIQUE INDEX uq_registry_binding_rank
  ON model_registry_pool_bindings (tenant_id, alias_id, rank)
  WHERE deleted_at IS NULL;

CREATE UNIQUE INDEX uq_registry_capability
  ON model_registry_capabilities (tenant_id, model_id, capability)
  WHERE deleted_at IS NULL;
```

Down migration should drop only new registry tables. If it also drops `uq_pool_groups_tenant_id_id`, it must be clearly marked as local rollback only because that index may become useful to later migrations.

### Smoke Seed Strategy

Do not seed production rows in the migration. In `seedSmokeGraph`, after creating the tenant and pool group:

1. insert snapshot version `1`
2. insert model for `gpt-4.1-mini` with canonical id such as `openai/gpt-4.1-mini`, provider model id `gpt-4.1-mini`, protocol `openai_chat`
3. insert capability `stream`
4. insert alias `gpt-4.1-mini`
5. insert rank-1 pool binding to the seeded pool group

Then remove `pool_group_id` from the smoke request body.

## 2. `backend/internal/registry`

Public interface:

```go
type Registry interface {
    ResolveModel(ctx context.Context, publicAlias string, tenantID int64) (router.ResolvedModel, error)
}
```

`PostgresRegistry` wraps `db.Querier` or `*db.Queries`. It may import `internal/router`; Router must not import Registry.

Add to `router.ResolvedModel`:

```go
PoolCandidates []int64
SnapshotVersion string
```

`PoolCandidates` replaces `ExplicitPoolGroupID`. `SnapshotVersion` lets Router stamp `RoutePlan.SnapshotVersion` without DB reads.

### Resolve Query

One sqlc query should fetch alias, model metadata, capabilities, ordered pool candidates, and snapshot version. Do not filter disabled status in SQL, because Go needs to distinguish unknown from disabled.

Logical query behavior:

1. `WHERE aliases.tenant_id = $1 AND aliases.public_alias = $2 AND aliases.deleted_at IS NULL`
2. join model by same tenant
3. join tenant snapshot
4. left join capabilities and pool bindings
5. aggregate capabilities and pool IDs ordered by binding rank

Error classes:

| Case | Error | HTTP default |
| --- | --- | --- |
| no alias row | `ErrUnknownModel` | 400 `unknown_model` |
| alias/model disabled | `ErrModelDisabled` | 403 `model_disabled` |
| active alias but no enabled pool candidates | `ErrTenantNoAccess` | 403 `model_not_allowed` |
| query/backend error | `ErrRegistryBackend` | 503 `registry_backend_error` |

D4 default is 403 for disabled/no-access because the caller is authenticated and an actionable message helps operators. Owner may choose generic 400 for less model-enumeration signal.

### Cache

D2 default: no cache for L0. One indexed SELECT is easier to reason about than stale per-process cache. Add LRU/TTL only after an invalidation path exists. If Owner chooses cache now, eviction/TTL/no-cross-tenant-hit tests should be unit-only; integration tests should not depend on cache timing.

### Snapshot Version

`ResolveModel` returns `SnapshotVersion = "model-registry:<tenant_id>:<version>"`. `DefaultRouter.Plan` stamps `RoutePlan.SnapshotVersion` as:

```text
model-registry:<tenant_id>:<version>;router:<router_policy_version>
```

If snapshot is empty, Router should fail closed with `missing_snapshot_version`. Future admin writers must increment `model_registry_snapshots.version` in the same transaction as any registry change. Registry itself stays SELECT-only.

## 3. Router Changes

Delete:

1. `PlanInput.ExplicitPoolGroupID`
2. `DefaultRouter.PlanWithPoolGroupID`
3. `errPoolGroupRequired`
4. tests built around the escape hatch

New `Plan` behavior:

1. validate `RequestID`, `TenantID`, `ProtocolFamily`, and `SnapshotVersion`
2. require `ResolvedModel.PoolCandidates` non-empty
3. emit `AttemptPlan` entries in candidate order
4. set `AttemptBudget` to `1` for this slice unless Owner explicitly includes executor fallback
5. keep `RequiredCapabilities` mapping from request features

Important limitation: Router may emit multiple attempts, but the current chat handler can still execute only the first attempt until the Executor slice. Do not claim real fallback execution in Slice 2.

Router tests to add/update:

1. missing request ID
2. missing tenant
3. missing protocol family
4. missing snapshot version
5. empty pool candidates
6. happy path preserves candidate order in attempts
7. snapshot stamp is non-empty and includes registry version

## 4. Chat Handler Wiring

Add dependencies:

```go
Registry registry.Registry
Router router.Router
```

`cmd/gateway/main.go` wires `registry.NewPostgresRegistry(q)` and `router.NewDefaultRouter()`.

Handler flow:

1. Auth resolves tenant/user/API key.
2. Parse body and require `stream=true`.
3. Reject `pool_group_id` if present. Use `*int64` temporarily if needed to distinguish absent from zero.
4. Resolve `req.Model` through Registry using `ident.TenantID`.
5. Build `router.RequestContext` with `middleware.GetReqID(r.Context())`, tenant ID, user ID, and API key ID.
6. Call `Router.Plan`.
7. Use `plan.Attempts[0].PoolGroupID` for current `Reserve` and `Pool.Select`.
8. Keep `RequestedModel` as the public alias for current ledger compatibility; use `ProviderModelID` where upstream/mock model ID is needed.
9. Preserve abort/settle behavior.

D5 default: refuse body `pool_group_id`. A client should not choose internal pool groups. Future forced-route must be operator-only, RBAC-gated, and audited, not a public chat field.

## 5. Backwards Compatibility And Smoke

The Phase C smoke request changes from:

```json
{"model":"gpt-4.1-mini","messages":[{"role":"user","content":"hi"}],"stream":true,"pool_group_id":123}
```

to:

```json
{"model":"gpt-4.1-mini","messages":[{"role":"user","content":"hi"}],"stream":true}
```

Add smoke assertions:

1. registry alias resolves to the seeded pool group
2. committed claim `pooling_group_id` equals the planned pool group
3. existing five PG-state assertions still pass

Cleanup registry rows before deleting pool groups: bindings, capabilities, aliases, models, snapshot.

## 6. Owner Decision Points

| ID | Default | Why |
| --- | --- | --- |
| D1 schema | normalized tables | supports same canonical model under multiple aliases and ordered pool candidates |
| D2 cache | no cache in L0 | avoids stale multi-instance resolution without invalidation |
| D3 tenant scoping | tenant-local only | avoids global fallback and cross-tenant surprises |
| D4 disabled HTTP | 403 for disabled/no-access | authenticated users get actionable model-state errors |
| D5 body override | reject `pool_group_id` | prevents client-controlled internal routing |
| D6 snapshot | explicit tenant snapshot table | stable audit/replay stamp |
| D7 sequencing | split N+5a/N+5b | isolates additive registry from handler money-path changes |
| D8 down migration | provide requested down file but discuss discipline | repo currently uses up-only migration files |

## 7. Risk Matrix

| Risk | Breakage | Mitigation |
| --- | --- | --- |
| two aliases map same canonical id | reporting may confuse public alias vs canonical model | allow it; keep public alias as requested model until schema stores canonical separately |
| tenant has no entry | every request for that alias fails | return `ErrUnknownModel` before Reserve; integration test no claim created |
| active alias has no pool | model appears enabled but cannot route | return `ErrTenantNoAccess`; test no Reserve |
| registry empty at boot | gateway starts but model requests fail | no fallback to body pool ID; smoke catches seed omissions |
| concurrent snapshot versions | requests may carry version 1 and 2 simultaneously | acceptable; each RoutePlan stores its version; query is one statement snapshot |
| cross-tenant pool binding | request reaches wrong tenant pool | composite FKs and tenant filters |
| registry backend outage | valid client sees wrong error | map to 503 and do not Reserve |
| multiple candidates but no executor loop | fallback appears available but is not executed | set AttemptBudget=1 and document limitation |

## 8. CMB Compliance

CMB-1: Registry query returns model metadata and pool group IDs only. It must not select `provider_accounts.credentials`, OAuth token columns, API key hashes, or plaintext secrets. Router consumes only `ResolvedModel`.

CMB-2: Registry returns `PricingClass` as a tag, not a decimal. Pool still receives `AttemptPlan` and does not compute cost.

CMB-7: `PostgresRegistry.ResolveModel` is SELECT-only. Router remains pure and DB-free. Request-time writes stay in Ledger reserve/settle and Pool slot/account updates.

## 9. Test Plan

Registry integration tests:

1. happy path resolves metadata, capabilities, ordered pool candidates, snapshot
2. unknown alias -> `ErrUnknownModel`
3. disabled alias/model -> `ErrModelDisabled`
4. no enabled pool binding -> `ErrTenantNoAccess`
5. two tenants use same public alias with different pools
6. two aliases map same canonical model
7. disabled binding excluded
8. cross-tenant binding rejected or never returned

Router unit tests:

1. validation failures above
2. attempts follow candidate order
3. snapshot version is required and stamped
4. required capabilities remain stable

Handler tests:

1. registry errors map to 400/403/503 and do not call Reserve
2. body `pool_group_id` returns 400
3. happy path uses `plan.Attempts[0].PoolGroupID` for Reserve and Pool.Select
4. chi request ID is passed to Router

Cache tests only if D2 changes to cache-enabled: LRU eviction, TTL expiry, no cross-tenant hit.

Smoke: run `go test -tags smoke ./cmd/gateway` after migrations and seed updates.

## 10. Sequencing

Default split:

N+5a:

1. migration up/down
2. sqlc query
3. generated db code
4. `internal/registry`
5. registry integration tests

N+5b:

1. router contract changes
2. delete `PlanWithPoolGroupID`
3. handler/main wiring
4. reject body pool override
5. smoke seed/body update
6. smoke and targeted tests

Single PR is acceptable only if Owner prioritizes speed. Even then, keep two commits and run `codex exec review --uncommitted --full-auto` before commit.

## 11. Acceptance Criteria

1. `ResolveModel(ctx, "gpt-4.1-mini", tenantID)` returns populated `router.ResolvedModel`.
2. `ResolvedModel.PoolCandidates` contains the seeded pool group.
3. `RoutePlan.SnapshotVersion` is non-empty and registry-derived.
4. `PlanWithPoolGroupID` and `ExplicitPoolGroupID` are gone.
5. Chat handler no longer accepts request-body pool routing.
6. Smoke passes without `pool_group_id`.
7. No Registry or Router code reads credentials.
8. Unknown/disabled/no-access/backend errors have deterministic HTTP mapping.

## 12. Non-Shrinkage / Security

No feature is dropped. The temporary client-side pool override becomes a safe equivalent: tenant-scoped alias resolution to ordered pool candidates. Future forced route remains possible as an audited operator feature.

Security improves because clients stop choosing internal pool IDs. Clean-room risk is low because no upstream source was read and the schema follows HUAKAI's existing tenant-scoped style.

Source files read: .agents/skills/pm-orchestrator/SKILL.md; .agents/skills/api-gateway-risk-review/SKILL.md; docs/01_PROJECT_BRIEF.md; docs/03_FEATURE_PARITY_MATRIX.md; docs/10_RISK_REGISTER.md; docs/12_AGENT_WORKFLOW.md; docs/specs/_invariants/cross-module-boundaries.md; docs/02_HUAKAI_FUSION_ARCHITECTURE.md; docs/process/plans/2026-04-30-n4-l0-minimum.md; docs/process/plans/2026-04-30-n4-l0-minimum-codex.md; backend/internal/router/route_plan.go; backend/internal/router/router.go; backend/internal/router/default_router.go; backend/internal/router/router_test.go; backend/internal/gatewayhttp/chat_completions_handler.go; backend/cmd/gateway/main.go; backend/cmd/gateway/smoke_test.go; backend/internal/billing/billing.go; backend/internal/auth/api_key_resolver.go; backend/sqlc.yaml; backend/sql/migrations/0001_pool_routing.up.sql; backend/sql/migrations/0002_observability_billing.up.sql; backend/sql/migrations/0006_upstream_credential_management.up.sql; backend/sql/migrations/0007_l0_inbound_auth.up.sql; backend/sql/queries/pool_accounts.sql
Lane: specifier
Agent: Codex
UTC timestamp: 2026-04-30T09:01:03Z
