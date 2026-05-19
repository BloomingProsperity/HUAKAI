# 2026-04-30 N+5b Chat Handler Rewrite + Escape Hatch Deletion - Codex Independent Plan

| Field | Value |
| --- | --- |
| Lane | specifier, independent planner |
| Counterpart | Claude is drafting `docs/process/plans/2026-04-30-n5b-handler-rewrite-claude.md` in parallel; this plan was written without reading that file. |
| Output target | `docs/process/plans/2026-04-30-n5b-handler-rewrite-codex.md` |
| Owner directive | "Independently draft the implementation plan for N+5b - Chat handler rewrite + escape hatch deletion." |
| Scope | Rewrite `/v1/chat/completions` to resolve `model` through Registry, route through Router, reject request-body `pool_group_id`, stamp usage with registry/router snapshot, update smoke, and delete the router escape hatch. |
| Out of scope | New admin registry writer, weighted routing, multi-attempt executor loop, pricing lookup, protocol translation, new schema beyond already-added nullable `usage_records.snapshot_version`. N+5a already says cache/admin writer/weighted execution are later slices, not L0 handler work [docs/process/plans/2026-04-30-n5-model-registry.md:77-79], [docs/process/plans/2026-04-30-n5-model-registry.md:459-463]. |
| Success criteria | Existing normal tests compile; router tests no longer reference `PlanWithPoolGroupID`; new gatewayhttp tests cover the four registry error classes and removed-field rejection; smoke posts only `model` and succeeds through registry-seeded rows; `usage_records.snapshot_version` is non-null for the success path and equals the combined registry/router stamp. |
| Time estimate | 2-3 engineering hours plus test time. Add 20-40 minutes if sqlc generation or PostgreSQL smoke setup needs local repair. |
| Blast radius | Medium-high: handler request contract, router public input, generated sqlc, billing SettleRequest/usage insert, and smoke data graph. Billing changes are high-risk under project rules because they touch usage record writes, so execution should wait for synthesized Owner-approved plan even though this file is only a plan. |

## Clean-room and Evidence Discipline

No non-MIT reference source is in scope for this plan. I read HUAKAI repo files and the already-synthesized N+5a plan only. I did not read `docs/process/plans/2026-04-30-n5b-handler-rewrite-claude.md`.

All repo-state assertions below cite file path plus line range. Reference behavior claims are avoided except where the N+5a synthesized plan already records evidence-row or WebFetch-backed decisions, for example uniform model-not-available handling and snapshot stamp shape [docs/process/plans/2026-04-30-n5-model-registry.md:80-89].

## Observed Current State

1. N+5a explicitly split the work: N+5a adds schema/registry/tests while N+5b rewrites the handler and removes the escape hatch [docs/process/plans/2026-04-30-n5-model-registry.md:8-10]. The synthesized N+5a plan specifically says N+5b must resolve via Registry, reject body `pool_group_id`, return uniform 404 for client model-unavailable states, delete `PlanWithPoolGroupID` and `ExplicitPoolGroupID`, and update smoke/snapshot assertions [docs/process/plans/2026-04-30-n5-model-registry.md:449-457].

2. `PostgresRegistry.ResolveModel` already exists and is the right dependency to consume. It normalizes aliases, opens a repeatable-read read-only transaction, resolves tenant/global alias rows, checks model status, loads capabilities/bindings/snapshot version, returns `ErrTenantNoAccess` when no binding survives, and builds `Resolved` with ordered `PoolCandidates` and `SnapshotVersion` [backend/internal/registry/postgres_registry.go:58-80], [backend/internal/registry/postgres_registry.go:118-155], [backend/internal/registry/postgres_registry.go:159-180].

3. Registry error classes are already defined with the intended public mapping: `ErrUnknownModel`, `ErrModelDisabled`, and `ErrTenantNoAccess` map to 404 `model_not_available`; `ErrRegistryBackend` maps to 503 `registry_backend_error` [backend/internal/registry/errors.go:1-9]. The resolver wraps datastore failures with `ErrRegistryBackend`, so handler mapping can use `errors.Is` [backend/internal/registry/postgres_registry.go:75-77], [backend/internal/registry/postgres_registry.go:122-140].

4. The handler still accepts `pool_group_id` in the request body and uses it in Tx1 reservation and Pool selection [backend/internal/gatewayhttp/chat_completions_handler.go:68-76], [backend/internal/gatewayhttp/chat_completions_handler.go:142-156], [backend/internal/gatewayhttp/chat_completions_handler.go:174-184]. It has no Registry or Router dependency today [backend/internal/gatewayhttp/chat_completions_handler.go:58-66].

5. `cmd/gateway/main.go` constructs `registry.NewPostgresRegistry(pgPool, nil)` but does not pass it to the chat handler; the file even marks it as "wired now; consumed by N+5b" [backend/cmd/gateway/main.go:59-73], [backend/cmd/gateway/main.go:121-128], [backend/cmd/gateway/main.go:163-173].

6. Router has the correct future shape but still carries the transitional field. `PlanInput` contains `ExplicitPoolGroupID` [backend/internal/router/router.go:29-44]. `DefaultRouter.Plan` calls `requestPoolGroupID`, which currently prefers `ExplicitPoolGroupID` and returns 0 otherwise [backend/internal/router/default_router.go:62-99]. `PlanWithPoolGroupID` and `errPoolGroupRequired` are still present [backend/internal/router/default_router.go:121-143]. Router tests still assert those escape-hatch paths [backend/internal/router/router_test.go:54-96], [backend/internal/router/router_test.go:98-130].

7. `ResolvedModel` already has `PoolCandidates []int64` and `SnapshotVersion string`, but comments still say the handler threads `ExplicitPoolGroupID` until N+5b [backend/internal/router/route_plan.go:13-40]. `RoutePlan` already has `SnapshotVersion` [backend/internal/router/route_plan.go:51-72], so the handler should not invent a parallel route stamp.

8. Migration 0008 already added the registry schema and nullable `usage_records.snapshot_version`, with the documented format `registry:<tenant_id>:<version>;router:<router_policy_version>` [backend/sql/migrations/0008_model_registry.up.sql:248-258]. The generated `InsertUsageRecord` query does not insert `snapshot_version` yet, and generated params do not have a `SnapshotVersion` field [backend/sql/queries/billing_settle.sql:28-55], [backend/internal/db/billing_settle.sql.go:172-236].

9. `billing.SettleRequest` has no snapshot field today [backend/internal/billing/billing.go:63-83]. `DefaultSettler.Settle` calls `InsertUsageRecord` inside Tx2 [backend/internal/billing/settler.go:78-118], so the cleanest success-path stamp is a new `SettleRequest.SnapshotVersion` field flowing into the SQL insert. `Abort` can also write usage records after a pool acquisition [backend/internal/billing/settler.go:241-272], but the stable CMB public contract lists `Abort(ctx, TenantID, ClaimID, Reason)` without a snapshot parameter [docs/specs/_invariants/cross-module-boundaries.md:56-63]. I would not change Abort in this slice without a DR.

10. The smoke test still posts `pool_group_id` and seeds no registry rows [backend/cmd/gateway/smoke_test.go:70-78], [backend/cmd/gateway/smoke_test.go:121-205]. Its cleanup deletes billing/pool/auth/core rows but not registry rows, because they are not seeded yet [backend/cmd/gateway/smoke_test.go:159-175]. Existing registry integration fixtures show the FK-safe cleanup order: bindings, capabilities, aliases, models, snapshots/policies, then pool/tenant rows [backend/internal/registry/postgres_registry_integration_test.go:103-120].

11. Registry integration coverage already exercises happy path, unknown alias, disabled alias/model, no binding, binding ordering, case-insensitive alias, effective windows, override, and snapshot stamp [backend/internal/registry/postgres_registry_integration_test.go:1-20], [backend/internal/registry/postgres_registry_integration_test.go:321-365], [backend/internal/registry/postgres_registry_integration_test.go:371-380], [backend/internal/registry/postgres_registry_integration_test.go:387-425], [backend/internal/registry/postgres_registry_integration_test.go:545-559], [backend/internal/registry/postgres_registry_integration_test.go:567-603], [backend/internal/registry/postgres_registry_integration_test.go:729-750]. N+5b needs handler-level tests, not more resolver tests by default.

12. The cross-module contract requires call order `Auth -> Registry -> Router -> Executor loop -> Pool -> Adapter -> Ledger` [docs/specs/_invariants/cross-module-boundaries.md:29-33]. It also forbids Router credential reads [docs/specs/_invariants/cross-module-boundaries.md:99-105], Adapter ledger writes [docs/specs/_invariants/cross-module-boundaries.md:115-129], and Registry/Router DB writes while Ledger owns `usage_records` writes [docs/specs/_invariants/cross-module-boundaries.md:147-158].

## Decision Points for Owner

### D1 - HTTP mapping for registry errors

Default: map `ErrUnknownModel`, `ErrModelDisabled`, and `ErrTenantNoAccess` to HTTP 404 with body code `model_not_available`; map `ErrRegistryBackend` to HTTP 503 with body code `registry_backend_error`.

Reasoning: this matches the N+5a synthesized pick for unknown/disabled/no-binding and backend failure [docs/process/plans/2026-04-30-n5-model-registry.md:80-89], and it matches the registry package's current error comments [backend/internal/registry/errors.go:1-9]. It avoids exposing alias enumeration or disabled-vs-not-owned distinctions to clients. The cost is less actionable client feedback, so the implementation should keep exact internal reason in a structured log or future audit event without putting it in the public response. If Owner wants `ErrTenantNoAccess` as 403 for legitimate operators, that is a conscious deviation from N+5a D4/D24 and should be recorded before implementation.

### D2 - Transition handling for request-body `pool_group_id`

Default: reject any JSON body containing the key `pool_group_id` with HTTP 400, body code `body_field_disallowed`, and message `field removed; use model alias resolution`.

Reasoning: N+5a explicitly picked "delete; if present return 400" [docs/process/plans/2026-04-30-n5-model-registry.md:82-83], [docs/process/plans/2026-04-30-n5-model-registry.md:113-114]. Since Go's normal `json.Unmarshal` silently ignores unknown struct fields, implementation must check raw JSON keys before unmarshalling into `chatRequest`. I would keep `chatRequest` without `PoolGroupID`, parse `map[string]json.RawMessage` only to detect the removed key, then unmarshal into the normal struct. This avoids `Decoder.DisallowUnknownFields`, which would accidentally reject other OpenAI-compatible fields that the Phase C minimal handler currently ignores.

### D3 - Snapshot stamp wiring

Default: `PostgresRegistry.ResolveModel` supplies the registry portion (`registry:<tenant_id>:<version>`), `DefaultRouter.Plan` combines it with its router policy version, and `billing.SettleRequest.SnapshotVersion` writes `RoutePlan.SnapshotVersion` into `usage_records.snapshot_version`.

Reasoning: the migration documents the combined format on the usage column [backend/sql/migrations/0008_model_registry.up.sql:248-258]. `ResolvedModel.SnapshotVersion` is explicitly the registry portion and says Router concatenates its own policy version [backend/internal/router/route_plan.go:34-39]. `RoutePlan.SnapshotVersion` is already the Router output field intended for audit replay [backend/internal/router/route_plan.go:69-72]. Therefore the handler should pass a route plan stamp, not concatenate ad hoc. The one limitation: `Abort` records written by `billing.Abort` remain nullable in N+5b because changing the stable Abort signature requires a DR [docs/specs/_invariants/cross-module-boundaries.md:56-65]. Success-path smoke must assert the stamp.

### D4 - Should the chat handler call Router.Plan?

Default: yes. Handler should call `Router.Plan` after Registry resolution and before Tx1 reservation.

Reasoning: CMB call order is Auth, Registry, Router, then executor/pool/adapter/ledger [docs/specs/_invariants/cross-module-boundaries.md:29-33]. Router already validates request id, tenant, and protocol family [backend/internal/router/default_router.go:42-60]. Skipping Router and using `resolved.PoolCandidates[0]` directly would keep smoke simpler but would preserve the current architecture violation: the handler would keep making route decisions outside the Router. N+5b's purpose is to remove that temporary shortcut.

Implementation detail: `DefaultRouter.Plan` should use `req.Model.PoolCandidates[0]`, set attempt budget to 1, and combine snapshots. If `PoolCandidates` is empty, it should return `PlanError{Code:"no_eligible_pool"}`. This is consistent with the current `RoutePlan.Attempts` contract [backend/internal/router/route_plan.go:51-62] and the registry query comment that L0 emits all candidates but Router selects index 0 [backend/sql/queries/registry.sql:100-127].

### D5 - Sequencing: one commit or split?

Default: one atomic N+5b implementation commit after the synthesized plan is approved, with internal checkpoints during development.

Reasoning: handler contract, smoke body, router carrier removal, and snapshot usage stamp are tightly coupled. A split commit that rewrites the handler while retaining body fallback would temporarily violate D2; a split commit that rejects the body field before smoke changes would break the gating smoke. One final commit keeps the repo in a single coherent post-N+5b state. Rollback is straightforward: revert the commit while leaving 0008's additive schema in place. The nullable `usage_records.snapshot_version` column is backward-compatible [backend/sql/migrations/0008_model_registry.up.sql:248-258].

If Owner insists on two commits, my fallback is:

1. N+5b1: handler uses Registry and Router, smoke posts no `pool_group_id`, success-path snapshot stamp works, but router escape-hatch symbols still exist unused.
2. N+5b2: remove `ExplicitPoolGroupID`, `PlanWithPoolGroupID`, `errPoolGroupRequired`, and old router tests/comments.

I would not put a "supports both old and new body contract" commit on the protected branch because the accepted transition behavior is reject-with-400.

### D6 - Handler testability seam

Default: introduce small gatewayhttp-local interfaces for Auth, Registry, Router, ClaimGate, Selector, and Settler only where needed for tests. Do not construct real DB-backed auth in handler unit tests.

Reasoning: current `ChatHandlerDeps.Auth` is concrete `*auth.APIKeyResolver` [backend/internal/gatewayhttp/chat_completions_handler.go:58-66]. Registry error mapping tests need to force `ResolveModel` errors without a real database, so the handler needs an interface seam. `auth.APIKeyResolver` already implements `Resolve(ctx,*http.Request)` [backend/internal/gatewayhttp/chat_completions_handler.go:96-108], and Registry/Router/ClaimGate/Settler already have package-level interfaces [backend/internal/registry/registry.go:28-32], [backend/internal/router/router.go:22-27], [backend/internal/billing/billing.go:17-37]. For `pool.Selector`, use the existing `pool.Selector` interface instead of concrete `*pool.DefaultSelector` [backend/internal/pool/pool.go:13-20].

## Touch List

1. `backend/internal/gatewayhttp/chat_completions_handler.go`
   Add Registry and Router dependencies; change Auth/Selector dependencies to interfaces; remove `chatRequest.PoolGroupID`; pre-detect removed `pool_group_id`; resolve `req.Model` via Registry using `ident.TenantID`; map registry errors; build `router.ResolvedModel`; call `Router.Plan`; require one primary attempt; use `plan.Attempts[0].PoolGroupID` in `billing.ReserveRequest.PoolingGroupID` and `pool.SelectionRequest.PoolGroupID`; pass `plan.Attempts[0].RequiredCapabilities`; pass `plan.SnapshotVersion` to `billing.SettleRequest`. Current field usage to replace is at handler lines 151 and 179 [backend/internal/gatewayhttp/chat_completions_handler.go:142-184].

2. `backend/internal/gatewayhttp/chat_completions_handler_test.go` (new)
   Add table tests for the four registry error mappings and the removed-field 400. Use fake auth returning a tenant/user/key identity, fake registry returning each error, and no-op downstream deps because those paths exit before Reserve/Select/Settle.

3. `backend/cmd/gateway/main.go`
   Add a `router.Router`/`DefaultRouter` dependency to `deps`, pass `d.modelRegistry` and `d.routePlanner` into `NewChatCompletionsHandler`, and remove `_ = d.modelRegistry`. Main currently wires registry but not handler consumption [backend/cmd/gateway/main.go:121-128], [backend/cmd/gateway/main.go:163-173].

4. `backend/internal/router/router.go`
   Delete `PlanInput.ExplicitPoolGroupID` and rewrite comments so the plan input is only auth context, resolved model, and request features. Current temporary field and comments are at lines 29-44 [backend/internal/router/router.go:29-44].

5. `backend/internal/router/default_router.go`
   Remove `errors` import, `errPoolGroupRequired`, and `PlanWithPoolGroupID`; change `requestPoolGroupID` to return `req.Model.PoolCandidates[0]`; update the no-pool error message to model alias resolution; combine `req.Model.SnapshotVersion` with `r.SnapshotVersion`. Current escape hatch is at lines 121-143 [backend/internal/router/default_router.go:121-143].

6. `backend/internal/router/route_plan.go`
   Update comments only: remove "chat handler still threads legacy ExplicitPoolGroupID" wording and state N+5b consumes Registry-populated candidates. The fields themselves already exist [backend/internal/router/route_plan.go:13-40].

7. `backend/internal/router/router_test.go`
   Replace `PlanWithPoolGroupID` tests with `Plan` tests that set `ResolvedModel.PoolCandidates`; keep missing request id, missing tenant, and unsupported model tests; add a no-candidates test and snapshot concatenation assertion. Existing escape-hatch tests to replace are at lines 54-130 [backend/internal/router/router_test.go:54-130].

8. `backend/internal/billing/billing.go`
   Add `SnapshotVersion string` to `SettleRequest`. This is a small API addition to support the already-added nullable column, not a schema change [backend/internal/billing/billing.go:63-83].

9. `backend/sql/queries/billing_settle.sql`
   Add `snapshot_version` to `InsertUsageRecord` columns and bind list. Current query omits it [backend/sql/queries/billing_settle.sql:28-55].

10. `backend/internal/billing/settler.go`
    Pass `req.SnapshotVersion` into `InsertUsageRecordParams` in the success path. Do not change `Abort` signature in N+5b; if Owner requires abort-path snapshot stamping too, create a DR first because the CMB contract lists Abort's signature [docs/specs/_invariants/cross-module-boundaries.md:56-65].

11. `backend/internal/db/billing_settle.sql.go`, `backend/internal/db/querier.go`, possibly `backend/internal/db/models.go`
    Regenerate with `sqlc generate` from `backend/Makefile` [backend/Makefile:30-31]. The current generated InsertUsageRecord params omit `SnapshotVersion` [backend/internal/db/billing_settle.sql.go:200-236].

12. `backend/cmd/gateway/smoke_test.go`
    Extend `smokeSeed` with model/alias/binding/snapshot ids only if useful for assertions; seed `models`, `model_aliases`, `model_pool_bindings`, optional `model_registry_capabilities`, and `model_registry_snapshots`; change request body to omit `pool_group_id`; cleanup registry rows before pool/auth/tenant rows; assert success-path usage row's `snapshot_version`. Current body and seed/cleanup are at lines 70-78 and 121-205 [backend/cmd/gateway/smoke_test.go:70-78], [backend/cmd/gateway/smoke_test.go:121-205].

## Test Plan

1. Unit: `go test ./internal/router`
   Expected updates: `Plan` happy path uses `ResolvedModel.PoolCandidates` instead of `ExplicitPoolGroupID`; no-candidates returns `PlanError.Code == "no_eligible_pool"`; snapshot is `registry:<tid>:<v>;router:v0.1-phase-c` or equivalent stable concatenation. Existing missing request id, missing tenant, and missing protocol tests remain conceptually unchanged [backend/internal/router/router_test.go:9-52].

2. Unit: `go test ./internal/gatewayhttp`
   New table tests:
   - `ErrUnknownModel` -> 404 `model_not_available`.
   - `ErrModelDisabled` -> 404 `model_not_available`.
   - `ErrTenantNoAccess` -> 404 `model_not_available`.
   - `ErrRegistryBackend` -> 503 `registry_backend_error`.
   - JSON body containing `pool_group_id` -> 400 `body_field_disallowed`.
   These tests require handler dependency interfaces because the current handler depends on concrete auth/selector objects [backend/internal/gatewayhttp/chat_completions_handler.go:58-66].

3. Generated-code and compile check:
   Run `sqlc generate` from `backend` via `make generate`, then `go test ./...`. The Makefile documents the generate target [backend/Makefile:30-31] and normal test target [backend/Makefile:18-19].

4. Integration registry tests:
   Keep existing `integration_pg` registry tests unchanged unless compile fallout requires helper updates. They already cover resolver behavior and snapshot source [backend/internal/registry/postgres_registry_integration_test.go:1-20].

5. Smoke:
   Run `HUAKAI_DATABASE_URL=... go test -tags smoke ./cmd/gateway` after applying migrations. Update `seedSmokeGraph` to create the registry model graph before gateway start, post body `{"model":"gpt-4.1-mini","messages":[...],"stream":true}`, and assert the committed usage row contains the expected snapshot stamp. The smoke test already builds/runs the real gateway and asserts HTTP plus PostgreSQL state [backend/cmd/gateway/smoke_test.go:45-106], [backend/cmd/gateway/smoke_test.go:336-391].

6. Optional full integration:
   If dev PostgreSQL is up, run `make test-integration` from `backend`; the Makefile target uses `-tags=integration_pg` [backend/Makefile:75-76]. If Windows Smart App Control interferes, use the existing project wrapper referenced in docs rather than changing code.

## Risk Matrix

| Risk | Trigger | Detection | Mitigation |
| --- | --- | --- | --- |
| Old clients still send `pool_group_id` | Clients using the Phase C smoke-era request contract hit N+5b after deploy. | New handler unit test and smoke negative case see HTTP 400 `body_field_disallowed`; runtime logs can count this code. | Return explicit message "field removed; use model alias resolution"; release note/operator migration note; do not silently ignore because that would hide stale clients. |
| Registry binding stale at request time | Registry resolves a model to a pool group whose accounts are disabled, cooled, full, or deleted after registry read. | `Pool.Select` returns `ErrNoEligibleAccount`/`ErrNoSlotAvailable`, existing handler path aborts the claim and returns 503 with `Retry-After` [backend/internal/gatewayhttp/chat_completions_handler.go:185-195]. | Keep L0 `AttemptBudget=1`; record no-capacity in routing reason; Slice 5 multi-attempt fallback can consume additional candidates. Do not make Registry perform runtime health filtering because CMB assigns intra-pool health to Pool [docs/specs/_invariants/cross-module-boundaries.md:23-27]. |
| Snapshot mismatch between Resolve and Router stamp | Handler concatenates manually or Router overwrites registry stamp with static router version. | Router unit test asserts combined stamp; smoke queries `usage_records.snapshot_version`. | Centralize combination in `DefaultRouter.Plan`; handler only forwards `plan.SnapshotVersion`; Registry's read-only repeatable-read transaction already keeps its own version consistent with rows read [backend/internal/registry/postgres_registry.go:68-80]. |
| Smoke cleanup FK failure | Smoke adds `models`/`model_aliases`/`model_pool_bindings` rows but cleanup still deletes pool groups and tenants first. | Re-running smoke leaves rows or fails deletes; FK errors show in cleanup if made fatal locally. | Delete `model_pool_bindings` first, then capabilities, aliases, models, snapshots/policies, then existing pool/provider/auth/tenant rows. Existing registry fixture uses this ordering [backend/internal/registry/postgres_registry_integration_test.go:103-120]. |
| sqlc/generated mismatch | Query adds `snapshot_version` but generated `InsertUsageRecordParams` is stale. | `go test ./...` compile failure in `settler.go` or generated code. | Run `make generate` in `backend`; include generated `internal/db` diff in the implementation commit [backend/Makefile:30-31]. |
| Handler nil dependency panic | Main or a unit test forgets to pass Registry/Router after deps change. | Unit test for missing Registry/Router can assert 503 fail-closed; panic would fail tests. | Add explicit nil checks returning `gateway_not_configured` or `registry_not_configured` before request processing reaches Resolve/Plan. |
| Abort-path usage records lack snapshot | Forwarder fails after Pool acquisition and handler calls `Settler.Abort`, whose public signature has no snapshot parameter. | Targeted future test can inspect aborted usage rows; current N+5b success smoke will not cover it. | Keep success-path stamp in N+5b; record this as a known limitation. If Owner wants all abort usage rows stamped now, open DR to change the stable ledger Abort contract [docs/specs/_invariants/cross-module-boundaries.md:56-65]. |

## CMB Compliance Check

### CMB-1 - Router does not read credentials

Compliant. The handler receives auth identity first, Registry resolves model metadata, and Router receives only `RequestContext`, `ResolvedModel`, and `RequestFeatures`. The registry SQL explicitly says it never selects provider credentials, OAuth tokens, or API key hashes [backend/sql/queries/registry.sql:1-7]. Router package comments already forbid importing auth, holding decimal cost fields, or writing DB [backend/internal/router/router.go:1-15]. N+5b should not add any `internal/auth` import to `internal/router`.

### CMB-3 - Adapter does not bypass Ledger

Compliant. The implementation leaves Adapter/Forwarder behavior unchanged: the handler calls `Forwarder.Forward` and then `Settler.Settle` with the usage draft [backend/internal/gatewayhttp/chat_completions_handler.go:218-253]. The CMB invariant says adapters return usage draft/error and the executor handles ledger interactions [docs/specs/_invariants/cross-module-boundaries.md:115-129]. N+5b only changes which pool group is planned and which snapshot string is passed into Ledger.

### CMB-7 - Registry/Router write nothing; Ledger owns usage writes

Compliant if the snapshot is written only inside Settler's existing `InsertUsageRecord` Tx2 path. Registry remains SELECT-only [backend/sql/queries/registry.sql:1-7], Router remains data-only [backend/internal/router/router.go:22-27], Pool still writes slot acquisition/in-flight state through existing selector path [backend/internal/pool/selector.go:193-214], and Ledger writes `usage_records`/`billing_events` in Tx2 [backend/internal/billing/settler.go:78-133]. Adding `snapshot_version` to the usage insert preserves the existing owner of that write.

## Pre-execution Checklist

1. Confirm synthesized plan exists and Owner approves the final N+5b path.
2. Confirm local branch contains N+5a commit `ddef4e4` or equivalent files: registry package, 0008 migration, router `PoolCandidates`, and main `modelRegistry` wiring.
3. Do not read Claude's independent draft until after this plan is finalized and ready for cross-discussion.
4. Check `git status` before implementation and avoid reverting unrelated user/agent changes.
5. Make handler/router/billing/smoke edits in one working tree change set.
6. Run `make generate` from `backend` after SQL query edits.
7. Run `go test ./internal/router ./internal/gatewayhttp ./internal/billing ./cmd/gateway`, then `go test ./...`.
8. Run smoke with `-tags smoke` against migrated PostgreSQL if `HUAKAI_DATABASE_URL` is available.
9. Stage changes and run the required per-commit Codex review before committing, per project discipline.

## Concrete Execution Order

1. Update router first: remove `ExplicitPoolGroupID`, make `DefaultRouter.Plan` consume `PoolCandidates[0]`, combine snapshots, update router tests.
2. Update billing stamp plumbing: add `SettleRequest.SnapshotVersion`, update `billing_settle.sql`, run sqlc generation, pass the field into success-path `InsertUsageRecord`.
3. Update handler dependencies and flow: auth -> raw body removed-field check -> unmarshal -> Registry.ResolveModel -> Router.Plan -> Reserve -> Select -> Forward -> Settle.
4. Update `cmd/gateway/main.go` dependency injection.
5. Add gatewayhttp unit tests for registry errors and removed field.
6. Update smoke seed, request body, cleanup, and snapshot assertion.
7. Run tests in the sequence above.
8. If smoke fails after registry resolve but before Pool.Select, inspect seeded alias/model/binding/snapshot rows first; if Pool.Select fails, inspect channel/provider_account membership under the chosen pool group.

## Rollback Story

Rollback is a code revert of the N+5b commit. Keep migration 0008 applied because it is additive and nullable for `usage_records.snapshot_version` [backend/sql/migrations/0008_model_registry.up.sql:248-258]. Reverting code restores the prior handler body contract and escape hatch while leaving unused registry tables/column in the database. If the failure is only smoke seed cleanup, patch smoke cleanup forward rather than rolling back handler behavior.

## Open Questions for Cross-discussion

1. Should exact registry failure reason be written only to structured logs in N+5b, or should N+5b also add a small audit event now? Current handler deps have no logger/audit writer [backend/internal/gatewayhttp/chat_completions_handler.go:58-66], while `cmd/gateway/main.go` has a logger available at route mount [backend/cmd/gateway/main.go:163-200].
2. Should `SettleRequest.SnapshotVersion` cover only successful settle in N+5b, or does Owner want a DR to change `Abort` so post-acquire abort usage records are stamped too [backend/internal/billing/settler.go:241-272], [docs/specs/_invariants/cross-module-boundaries.md:56-65]?
3. Should the handler pass `resolved.ProviderModelID` to the mock upstream/ForwardRequest now, or leave model-wire semantics unchanged until protocol translation work? N+5b's mandatory requirement is pool-group selection replacement, not provider-model rewrite.

Source files read: docs/01_PROJECT_BRIEF.md; docs/03_FEATURE_PARITY_MATRIX.md; docs/10_RISK_REGISTER.md; docs/12_AGENT_WORKFLOW.md; docs/process/plans/2026-04-30-n5-model-registry.md; docs/specs/_invariants/cross-module-boundaries.md; backend/internal/registry/registry.go; backend/internal/registry/postgres_registry.go; backend/internal/registry/errors.go; backend/internal/registry/normalize.go; backend/internal/registry/cache.go; backend/internal/registry/postgres_registry_integration_test.go; backend/sql/migrations/0008_model_registry.up.sql; backend/sql/queries/registry.sql; backend/internal/router/router.go; backend/internal/router/route_plan.go; backend/internal/router/default_router.go; backend/internal/router/router_test.go; backend/internal/gatewayhttp/chat_completions_handler.go; backend/cmd/gateway/main.go; backend/cmd/gateway/smoke_test.go; backend/internal/billing/billing.go; backend/internal/billing/settler.go; backend/sql/queries/billing_settle.sql; backend/internal/db/billing_settle.sql.go; backend/internal/pool/pool.go; backend/internal/pool/selector.go; backend/internal/pool/routing_reason.go; backend/sqlc.yaml; backend/go.mod; backend/Makefile; C:/HUAKAI/repo/.agents/skills/pm-orchestrator/SKILL.md
Lane: specifier
Agent: Codex
UTC timestamp: 2026-04-30T16:02:05Z
