# HUAKAI Upgrade #1 — binding-aware filter

| Field | Value |
| --- | --- |
| Lane | codex |
| Time | 2026-05-08 |
| Mode | PLANNER only; no code, no migration, no business implementation |
| Owner directive | "HUAKAI Upgrade #1 — binding-aware filter" |
| Clean-room note | Did not read Sub2API source. Used only Owner-provided contrast: global round-robin/global health is insufficient for HUAKAI multi-tenant account-binding. |

## Current Observations

1. Current request flow is already layered as `Auth -> Registry -> Router.Plan -> Executor/Pool`, and router is explicitly pure: no auth import, no DB write, no credential read. Upgrade #1 should preserve that boundary by resolving bindings outside router and passing router plain binding data only. Sources: `backend/internal/router/router.go:5-15`, `backend/internal/router/router.go:22-27`.
2. Current `DefaultRouter` is L0: it reads only `ResolvedModel.PoolCandidates[0]`, emits one attempt, and sets `AttemptBudget=1`. That is not sufficient for binding-aware routing because it ignores API key/user/account binding order and fallback. Sources: `backend/internal/router/default_router.go:7-10`, `backend/internal/router/default_router.go:18-21`, `backend/internal/router/default_router.go:55-76`.
3. Registry already returns tenant-scoped `model -> pool_group` bindings as ordered `PoolCandidates` plus `BindingMetadata`, but those are model/pool bindings, not local API key/user to upstream account bindings. Sources: `backend/internal/registry/registry.go:56-60`, `backend/internal/registry/registry.go:68-82`, `backend/internal/registry/postgres_registry.go:119-180`, `backend/sql/queries/registry.sql:100-127`, `backend/sql/migrations/0008_model_registry.up.sql:160-210`.
4. Pool selector already has the right enforcement seam: `SelectionRequest` carries tenant/user/key/pool/model data; `GateChain` has typed gates; selection filters candidates, then tries sticky/routing/fresh/fallback paths. Upgrade #1 should add binding metadata and a binding gate here, not replace the selector. Sources: `backend/internal/pool/pool.go:24-38`, `backend/internal/pool/gates.go:7-18`, `backend/internal/pool/gates.go:46-52`, `backend/internal/pool/selector.go:100-140`, `backend/internal/pool/selector.go:252-267`.
5. DB account lookup is currently pool-group keyed and returns all enabled, healthy accounts in that pool. It does not know API key binding or direct provider-account binding. Sources: `backend/internal/pool/db_account_source.go:37-40`, `backend/sql/queries/pool_accounts.sql:71-97`.
6. Auth resolves `TenantID`, `APIKeyID`, and `UserID`; `api_keys` are tenant/user-scoped, but no active schema links a key to upstream account or pool. Sources: `backend/internal/auth/api_key_resolver.go:38-46`, `backend/internal/auth/api_key_resolver.go:138-142`, `backend/sql/migrations/0007_l0_inbound_auth.up.sql:51-70`.
7. Prior HUAKAI audit already identified `APIKeyBinding` as a critical missing spine: no `api_key_bindings` table, no persisted contract that key K targets pool/account set S, and usage lacks `binding_id`. Sources: `docs/reference_delta/2026-05-02/account-to-api-mainline-audit.md:37-45`, `docs/reference_delta/2026-05-02/account-to-api-mainline-audit.md:62-72`, `docs/reference_delta/2026-05-02/account-to-api-mainline-audit.md:101-124`, `docs/reference_delta/2026-05-02/account-to-api-mainline-audit.md:145-178`.
8. The synthesized Account-to-API plan already converged on additive `api_key_bindings`, `request_attempts`, and nullable `usage_records.binding_id/pool_group_id/credential_*` fields, with composite tenant FKs and no new dependency. Upgrade #1 should either depend on that spine or include its minimal binding subset after Owner approval. Sources: `docs/plans/2026-05-02-accapi-spine.md:16-29`, `docs/plans/2026-05-02-accapi-spine.md:161-175`.
9. Health state today is mostly account-global: `provider_accounts.health_state`, rate/cooldown columns, and OAuth storm/circuit budget are account/tenant scoped. The new FSM is pure and returns side effects, but persistence/wiring is not yet binding-aware. Sources: `backend/sql/migrations/0001_pool_routing.up.sql:119-149`, `backend/sql/migrations/0004_rate_limiting.up.sql:24-84`, `backend/sql/migrations/0006_upstream_credential_management.up.sql:91-124`, `backend/internal/gateway/health_fsm.go:83-92`, `backend/internal/gateway/health_fsm.go:153-185`, `backend/internal/gateway/health_fsm.go:309-323`.
10. Quota/billing flow has tenant, API key, user, pool group, and provider account fields, but not binding identity. Reserve/settle are money-path code; binding quota must be added transactionally and requires Owner approval before implementation. Sources: `backend/internal/billing/billing.go:41-55`, `backend/internal/billing/billing.go:65-90`, `backend/internal/billing/claim_gate.go:63-71`, `backend/internal/billing/claim_gate.go:143-158`, `backend/internal/billing/settler.go:84-117`, `backend/internal/billing/settler.go:123-178`, `backend/sql/migrations/0002_observability_billing.up.sql:121-190`.

## Scope

In scope for the implementation plan:

1. Binding resolution after auth and before router, using explicit key binding rows when available.
2. Router planning that orders attempts by binding contract, not only by model family/pool priority.
3. Pool selector enforcement that prevents a selected route from escaping its binding target.
4. Binding-aware health/circuit overlay so transient failures can be isolated to one binding while truly account-global failures remain account-global.
5. Binding-aware quota dimension so limits can be split by `binding_id` in addition to current tenant/user/API key/account dimensions.
6. Usage/audit/test updates so operators can prove which binding caused the route, quota decision, health state, and final upstream account.
7. No new runtime dependency; use PostgreSQL/sqlc/current in-process Go patterns only.

Out of scope unless Owner explicitly expands:

1. Reading or copying non-MIT reference implementation source.
2. UI/admin dashboard implementation.
3. Payment logic, billing ledger redesign, auth core redesign, or production deployment.
4. New external cache, Redis, queue, or distributed coordination dependency.
5. Large provider adapter rewrites.

## Success Criteria

1. A request from a key bound to pool/account set A never competes equally with unbound tenant-default accounts unless an explicit ordered fallback binding permits it.
2. A key bound directly to provider account X either selects X after model/pool compatibility checks or fails closed with an auditable binding mismatch; it does not silently fall back to unrelated accounts.
3. A key bound to pool P only expands candidates inside P and still respects tenant, model, credential, health, quota, and per-request exclusion gates.
4. Sticky routing cannot escape the active binding; stale sticky bindings outside the allowed binding target are broken with a routing reason.
5. Transient upstream errors from binding B1 can open/cool down B1 without poisoning B2 when B2 is a separate binding to the same physical account and the error is not account-global.
6. Iron-clad credential/account failures still mark the provider account globally unavailable so all bindings stop using a truly bad account.
7. Binding quota exhaustion denies only that binding unless tenant/user/API key/account quota is also exhausted.
8. New usage/audit records carry enough data to answer: tenant, user, API key, binding, pool, account, attempt, health/quota reason.
9. Existing tenant isolation guarantees are preserved through composite tenant keys and cross-tenant tests.
10. Focused unit tests and integration tests pass; no new dependency appears in module files.

## Time Estimate

Planning already done in this file.

Implementation estimate after Owner approves high-risk parts:

| Work unit | Estimate |
| --- | --- |
| Spec/docs alignment and decision capture | 1-2 hours |
| Binding resolver + router contract changes | 2-4 hours |
| Pool binding gate + DB query support | 2-4 hours |
| Binding health overlay and updater seam | 4-6 hours |
| Binding quota Tx1/Tx2 design + implementation | 6-10 hours |
| Tests and integration fixtures | 6-10 hours |
| Review/fix cycle | 2-4 hours |

Total: 2-4 focused agent-days, assuming the Account-to-API spine schema is already approved or lands as a prerequisite. Add 1-2 days if Upgrade #1 must also implement the minimal `api_key_bindings`/`usage_records.binding_id` migration.

## Blast Radius

High risk:

1. Quota enforcement and billing settlement are money-path code.
2. DB schema changes for binding IDs, binding health, or binding quota require migration and rollback discipline.
3. Routing selection changes can leak capacity across users/tenants if binding filters are wrong.
4. Health/circuit scoping errors can either over-block good bindings or under-block bad upstream accounts.

Medium risk:

1. Router and pool contract changes touch hot path request latency.
2. Cache strategy can create stale binding decisions if versioning/invalidation is weak.
3. Tests need realistic fixtures; weak stubs may hide missing SQL `WHERE tenant_id` or gate ordering defects.

Low risk:

1. Docs/spec/parity matrix updates.
2. Pure type additions that carry binding metadata without changing behavior behind a feature flag.

## Failure Modes And Mitigations

| Failure mode | Impact | Mitigation |
| --- | --- | --- |
| Implicit tenant-default fallback bypasses a strict binding | Premium/free isolation breaks | Require explicit `tenant_default` binding row or deny; record fallback in routing reason |
| Binding cache keyed without tenant/API key/version | Cross-tenant or stale binding leak | Cache key must include `tenant_id`, `api_key_id`, binding snapshot/version, model alias; fail closed on ambiguity |
| Direct provider-account binding ignores model compatibility | Requests reach account that cannot serve model | Reconcile direct account target with registry/model capability and channel/pool membership before planning |
| Sticky binding points outside current binding | Session leaks to unauthorized account | Binding gate runs before sticky success; break sticky with `sticky_broken_binding_scope` |
| Transient 429/5xx updates account-global health | One user/key pollutes all bindings | Classify transient/rate/latency errors as binding-scoped overlay by default |
| Iron-clad credential failure stays binding-local | Bad credential keeps serving other bindings | Classify invalid credential/revoked/disabled/expired/account quota as account-global hard gate |
| Binding quota checked outside Tx1/Tx2 | Double-spend or free usage under concurrency | Add binding quota reservation/settlement inside same serializable transaction path as claim/usage |
| Usage lacks binding/pool fields | Operator cannot audit binding-aware behavior | Populate `binding_id`, `pool_group_id`, and request attempt rows for all new traffic |
| Health overlay table grows unbounded | Hot path slows over time | Index `(tenant_id,binding_id,provider_account_id)` and expire/compact old success/error samples |
| Outbox/cache invalidation lags | Recently disabled binding remains usable | Versioned reads plus short max TTL; binding-disable path should synchronously bump version |

## Decision Points

| Decision | Options | Codex recommendation |
| --- | --- | --- |
| Binding owner scope | API key / user / tenant / route | L1 = API key binding because auth resolves `APIKeyID` and existing audit calls out missing key-to-account contract. Represent tenant default as explicit binding. Defer separate user-level table; user scope can be applied by creating/updating bindings for that user's keys. Route is a planner result, not the owner of the account-binding contract. |
| Binding target kinds | pool only / account only / pool + provider_account + tenant_default | Use the synthesized Account-to-API shape: `pool_group`, `provider_account`, `tenant_default`. |
| Direct account binding compatibility | Let account bypass model registry / require model+pool compatibility | Require compatibility. A direct account binding is a target constraint, not permission to bypass model capability and tenant model policy. |
| Fallback when all bound targets fail health/quota | Implicit tenant default / explicit fallback binding / deny | Explicit ordered fallback binding only; otherwise deny with `Retry-After` if recovery ETA exists. |
| Health/circuit scope | account-global only / binding-local only / hybrid | Hybrid. Binding-local for ambiguous/transient/rate/latency/circuit signals; account-global for credentials revoked, account disabled, expiry, account quota exhausted, iron-clad permanent failures. |
| Quota split scope | user only / API key / binding / route / tenant | Add optional `binding_id` quota dimension while preserving user/API key/tenant/account gates. Do not use route as the primary quota owner in L1 because route can change with router policy. |
| Hot path cache | no cache / versioned in-process cache / external cache | Start correctness-first with no cache or a small versioned in-process cache only. No external dependency. Cache only after binding versioning and invalidation are in place. |
| Cache failure behavior | fail-open from stale cache / fail-closed / last-known-good grace | Fail closed for binding and quota. Consider last-known-good only as a later Personal Edition operator override, never default SaaS behavior. |
| Schema dependency | Wait for Account-to-API 0011 / include minimal binding schema in this upgrade | Prefer prerequisite or combined Owner-approved schema slice. Binding-aware filter is not product-correct without explicit persisted binding rows. |

## Design Outline

### 1. Binding Resolution Stage

Add a request-path stage after `Auth.Resolve` and before `Registry.ResolveModel` or before `Router.Plan`:

1. Input: `tenant_id`, `api_key_id`, `user_id`, requested public model alias, request id.
2. Lookup active bindings for `(tenant_id, api_key_id)` ordered by priority, id.
3. If no binding exists, either:
   - use an explicit `tenant_default` row if present, or
   - fail closed with `binding_not_configured`.
4. Return plain binding candidates to router:
   - `binding_id`
   - `binding_kind`
   - `pool_group_id` when target is pool/default
   - `provider_account_id` when target is direct account
   - `priority`
   - fallback policy metadata
   - binding snapshot/version

Boundary rule: this resolver can read DB; router must not. Router consumes plain structs only.

### 2. Registry And Router Reconciliation

Keep registry as the source for model visibility and model-to-pool candidates. The router should combine:

1. Registry `PoolCandidates` for the requested model.
2. Binding candidates for the API key.
3. Request features/capability requirements.

Planning rules:

1. `tenant_default`: expand to registry `PoolCandidates`.
2. `pool_group`: allow only if the pool is present in registry `PoolCandidates`; otherwise emit binding/model mismatch.
3. `provider_account`: derive the account's pool through `provider_accounts.channel_id -> channels.pool_group_id`, then require that pool to be model-compatible.
4. Deduplicate attempts by `(binding_id, pool_group_id, provider_account_id?)` while preserving binding priority.
5. Attempts should carry binding metadata to the pool selector:
   - `BindingID`
   - `BindingKind`
   - `AllowedAccountIDs` when direct account binding applies
   - `BindingSnapshotVersion`
6. `AttemptBudget` should come from binding fallback policy first, then tenant/router policy. L0 can remain single-attempt behind a feature flag, but Upgrade #1 success requires multi-binding fallback tests.

### 3. Pool Selector Binding Gate

Extend `pool.SelectionRequest` with binding fields and add a `BindingGate` early in the gate chain, after tenant and before model/credential/health:

1. For direct account binding, reject all accounts except the bound account.
2. For pool binding, reject accounts not under the selected bound pool.
3. For tenant default, allow registry-expanded pool candidates.
4. If sticky lookup returns an account outside binding scope, break sticky and continue to fresh selection.
5. Routing reason should include:
   - `binding_id`
   - `binding_kind`
   - `binding_scope`
   - `binding_filter_outcome`
   - candidate exclusion count for `binding_scope`

### 4. Binding-Aware Health And Circuit

Add a binding-health overlay separate from physical account state:

1. Physical account state remains on `provider_accounts` for global hard blocks.
2. Binding overlay is keyed by at least `(tenant_id, binding_id, provider_account_id)`.
3. Optional future refinement: include `model_alias` or `endpoint_family` if errors prove model-specific.
4. Candidate filtering checks both:
   - provider account global health/credential/quota state
   - binding overlay health/circuit state
5. Upstream error handling classifies update scope:
   - binding-local: 429, 529, ambiguous 5xx, latency/timeout, transient overload
   - account-global: revoked credential, invalid token, account expiry, operator-disabled, account quota exhausted, iron-clad permanent disable
6. `Retry-After` response on no-capacity should use the minimum recovery ETA among bound candidates, not global tenant pool recovery.
7. Health events should be auditable via request attempts or a dedicated binding-health event table.

### 5. Binding-Aware Quota

Add binding quota as an additional optional ledger dimension, not a replacement:

1. Tx1 Reserve checks tenant/API key/user quotas, account quota, and binding quota if configured.
2. Tx2 Settle charges the same binding quota dimension exactly once using the existing claim/acquisition/idempotency contract.
3. Abort releases or zero-charges consistently with existing claim behavior.
4. Binding quota keys should be `(tenant_id, binding_id, window)` with serializable row locking or equivalent existing PG locking.
5. Usage records must carry `binding_id` so quota audits can prove the charged binding.
6. Binding quota exhaustion should deny only the binding; fallback to another binding is allowed only if configured as an ordered fallback.

### 6. Cache Strategy

Correctness-first path:

1. No external cache and no new dependency.
2. If latency requires caching, use an in-process versioned read-through cache only after binding versioning exists.
3. Cache key: `(tenant_id, api_key_id, requested_model_alias_normalized, binding_snapshot_version, registry_snapshot_version)`.
4. Cache invalidation: binding/admin writes bump binding version; registry writes already have registry snapshot version pattern; scheduler outbox can notify best-effort.
5. Cache miss with DB error: fail closed (`503 binding_backend_error`) rather than using stale data.
6. Cache hit must still pass pool selector hard gates because account health/quota can change after binding resolution.

## Testing Matrix

| Area | Test | Expected result |
| --- | --- | --- |
| Binding resolver | API key has pool binding P | Returns P with binding id and priority |
| Binding resolver | API key has direct account A | Returns A plus derived pool check metadata |
| Binding resolver | No active binding and no tenant_default | Fail closed; no registry/router fallback |
| Binding resolver | Disabled binding exists | Excluded or denied according to explicit policy; never silently used |
| Cross-tenant | Tenant A key references Tenant B pool/account | Rejected by composite FK or service validation; no candidate leaks |
| Router | Registry candidates `[P1,P2]`, key bound to P2 | Plan tries P2, not P1 |
| Router | Key bound to P3 not in registry candidates | `model_not_available` or `binding_model_mismatch`, no attempt |
| Router | Multiple ordered bindings with duplicate pools | Deduplicated in deterministic order |
| Pool gate | Direct account binding A, pool also has B | Selector only chooses A |
| Pool gate | Sticky points to B but binding allows A | Sticky broken; fresh selection chooses A or no capacity |
| Health | 429 on binding B1 to account A | B1 circuit/cooldown opens; B2 to same A remains eligible if account-global state is healthy |
| Health | invalid_grant/token_revoked on account A | Account-global hard state blocks all bindings to A |
| Health | all bound candidates cooling | Response has `Retry-After` from min binding recovery ETA |
| Quota | Binding B1 exhausted, B2 available | Request can fall back to B2 only if B2 is configured fallback; B1 denial audited |
| Quota | User quota exhausted but binding quota remains | Deny due to user quota; binding is not charged |
| Quota | Binding quota concurrent reserve/settle | No double-spend under 50-100 concurrent requests |
| Billing | Idempotency replay after binding route | Same claim/binding not double-charged |
| Usage | Successful request | Usage row contains tenant, api_key, user, binding, pool, account, snapshot |
| Attempt audit | Retry across two bindings | Attempts recorded in order with binding/account/error/retry-after fields |
| Cache | Binding version bump | Old cached binding not used after version change |
| Cache | Binding DB outage on cache miss | Fail closed with 503-style backend error |
| Regression | Existing unbound L0 fixture with explicit tenant_default | Still passes through tenant default |

Minimum checks after implementation:

1. Focused unit tests: `go test ./backend/internal/router ./backend/internal/pool ./backend/internal/billing ./backend/internal/gateway ./backend/internal/auth`.
2. Integration PG tests for schema/FKs/quota/idempotency if schema lands.
3. Existing gateway smoke test with explicit binding fixture.
4. `go test ./backend/internal/...` or repo-standard equivalent.
5. `codex exec review --uncommitted --full-auto` before any commit, per project rule.

## Required Documentation Updates

1. `docs/specs/pool-routing.md`: add binding-aware scheduler semantics to F-POOL-001/A01.
2. `docs/specs/account-to-api-spine.md` or equivalent: make binding resolution contract explicit if not already released.
3. `docs/03_FEATURE_PARITY_MATRIX.md`: ensure A01/ACCAPI binding-aware status is mapped, not silently dropped.
4. `docs/11_ACCEPTANCE_TEST_MATRIX.md`: add AT-BIND/AT-POOL/AT-QUOTA/AT-HEALTH cases above.
5. `docs/10_RISK_REGISTER.md`: add stale binding cache, binding quota double-spend, health-scope pollution risks if not present.
6. OpenAPI/admin docs only if implementation includes admin binding management; otherwise document as dependency.

## Owner Confirmation Needed Before Execution

1. Approve schema dependency: use existing Account-to-API 0011 plan as prerequisite, or include minimal binding schema in Upgrade #1.
2. Confirm L1 binding scope: API key primary + explicit tenant_default; separate user-level binding deferred.
3. Confirm direct provider-account binding must still pass model/pool compatibility.
4. Confirm health scope rule: transient binding-local, iron-clad/account lifecycle account-global.
5. Confirm binding quota is a new quota dimension in Tx1/Tx2 and may touch money-path code.
6. Confirm cache posture: fail-closed; no external dependency; versioned in-process cache only if needed.

## Source References

- Router boundary and purity: `backend/internal/router/router.go:5-15`, `backend/internal/router/router.go:22-27`
- Current L0 router behavior: `backend/internal/router/default_router.go:7-10`, `backend/internal/router/default_router.go:18-21`, `backend/internal/router/default_router.go:55-76`
- Route plan data shape: `backend/internal/router/route_plan.go:25-30`, `backend/internal/router/route_plan.go:49-60`, `backend/internal/router/route_plan.go:73-101`
- Pool request/gate/selector: `backend/internal/pool/pool.go:24-38`, `backend/internal/pool/gates.go:7-18`, `backend/internal/pool/gates.go:46-52`, `backend/internal/pool/selector.go:100-140`, `backend/internal/pool/selector.go:252-267`
- DB pool account lookup: `backend/internal/pool/db_account_source.go:37-40`, `backend/sql/queries/pool_accounts.sql:71-97`
- Auth identity and key schema: `backend/internal/auth/api_key_resolver.go:38-46`, `backend/internal/auth/api_key_resolver.go:138-142`, `backend/sql/migrations/0007_l0_inbound_auth.up.sql:51-70`
- Registry model-pool binding: `backend/internal/registry/registry.go:56-60`, `backend/internal/registry/registry.go:68-82`, `backend/internal/registry/postgres_registry.go:119-180`, `backend/sql/queries/registry.sql:100-127`, `backend/sql/migrations/0008_model_registry.up.sql:160-210`
- Account-to-API binding gap and proposed spine: `docs/reference_delta/2026-05-02/account-to-api-mainline-audit.md:37-45`, `docs/reference_delta/2026-05-02/account-to-api-mainline-audit.md:62-72`, `docs/reference_delta/2026-05-02/account-to-api-mainline-audit.md:101-124`, `docs/reference_delta/2026-05-02/account-to-api-mainline-audit.md:145-178`, `docs/plans/2026-05-02-accapi-spine.md:16-29`, `docs/plans/2026-05-02-accapi-spine.md:161-175`
- Health/rate/account state: `backend/sql/migrations/0001_pool_routing.up.sql:119-149`, `backend/sql/migrations/0004_rate_limiting.up.sql:24-84`, `backend/sql/migrations/0006_upstream_credential_management.up.sql:91-124`, `backend/internal/gateway/health_fsm.go:83-92`, `backend/internal/gateway/health_fsm.go:153-185`, `backend/internal/gateway/health_fsm.go:309-323`
- Billing/quota settlement path: `backend/internal/billing/billing.go:41-55`, `backend/internal/billing/billing.go:65-90`, `backend/internal/billing/claim_gate.go:63-71`, `backend/internal/billing/claim_gate.go:143-158`, `backend/internal/billing/settler.go:84-117`, `backend/internal/billing/settler.go:123-178`, `backend/sql/migrations/0002_observability_billing.up.sql:121-190`
- Feature parity mandate for binding-aware scheduler: `docs/03_FEATURE_PARITY_MATRIX.md:73`

