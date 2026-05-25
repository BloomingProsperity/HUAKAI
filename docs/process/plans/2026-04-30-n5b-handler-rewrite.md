# N+5b — Chat Handler Rewrite + Escape Hatch Deletion (Synthesized Plan)

| Field | Value |
| --- | --- |
| Status | Synthesized after parallel-draft cross-discuss (CLAUDE.md #10) |
| Sources | [-claude.md](2026-04-30-n5b-handler-rewrite-claude.md) + [-codex.md](2026-04-30-n5b-handler-rewrite-codex.md) |
| Synthesis authority | Claude per Owner's pre-decided A→B→C order; per CLAUDE.md #10 the slice-internal decisions inside an approved scope can be synthesized without re-asking Owner unless conflicts surface |
| Date | 2026-05-01 |
| Predecessor | N+5a commit `ddef4e4` |
| Migration | None — pure code rewire on top of 0008 |
| Citation discipline | Owner directive 2026-04-30 "所有的动作都不允许凭借自己的记忆库知识"; every claim cites a repo file:line, an evidence ID, or a fresh fetch URL+timestamp |

---

## Conflict resolution

The two parallel drafts converged strongly. The differences:

| Topic | Claude pick | Codex pick | **Adopted** | Reason |
|---|---|---|---|---|
| D1 HTTP code mapping | 404 / 404 / 404 / 503 | 404 / 404 / 404 / 503 | **same** | Anti-enum parity with auth; matches N+5a synthesized plan §D4. |
| D2 body field rejection method | `*int64` pointer + non-nil check | Pre-parse `map[string]json.RawMessage` to detect the key, then unmarshal canonical struct | **Codex** | Pointer-only fails if a future field is shadowed; raw-key detection is unambiguous. Implementation cost equivalent. |
| D3 Snapshot wiring | Router concatenates + new `SettleRequest.SnapshotVersion` | Same | **same** | Centralizes stamp composition where `RoutePlan.SnapshotVersion` already lives ([route_plan.go:69-72]). |
| D4 Call `Router.Plan` | YES | YES | **same** | CMB-1 mandates Auth → Registry → Router → Executor call order ([cross-module-boundaries.md:29-33]). |
| D5 Sequencing | Single commit | Single commit | **same** | Coupled changes; revert is one step. |
| D6 Test seam | "Use stubs" (vague) | Define interfaces for Auth + Registry + Router + ClaimGate + Selector + Settler at handler boundary | **Codex** | Most of these are already package-level interfaces ([registry.go:28-32], [router.go:22-27], [billing.go:17-37], [pool.go:13-20]); only Auth needs a new local interface. |
| D7 (Codex-only) Use `resolved.ProviderModelID` at upstream | n/a | Open question — should ForwardRequest.Model use override now? | **YES, use it now** | Binding-level `provider_model_id_override` was N+5a's whole point (generic per-binding upstream rename pattern, evidence-mined 2026-04-30T09:35Z); not threading it makes the override a stored-but-unused column. Cost: 1 line. |
| Nil-dependency panic | (not surfaced) | Add explicit `gateway_not_configured` 503 if Registry/Router nil | **Codex** | Cheap defensive check; mirrors N+4a `ErrAuthMisconfigured` pattern. |

No material conflicts requiring Owner arbitration. D7 default-decided per "simple coding-execution choice inside Codex-approved plan" exception (CLAUDE.md #10 last paragraph).

---

## Final scope

### What changes

1. `chat_completions_handler.go`:
   - Add `Registry registry.Registry` + `Router router.Router` to `ChatHandlerDeps`.
   - Convert `Auth` field type from `*auth.APIKeyResolver` to a local `authResolver` interface (so unit tests don't need a real DB).
   - Convert `Selector *pool.DefaultSelector` to `pool.Selector` interface (already exists per [pool.go:13-20]).
   - Detect body `pool_group_id` via raw-JSON pre-parse: `map[string]json.RawMessage`. If key present → 400 `body_field_disallowed` "field removed; use model alias resolution".
   - Remove `chatRequest.PoolGroupID`. Struct retains only `model`, `messages`, `stream`.
   - Pipeline: `Auth.Resolve → raw-key reject check → json.Unmarshal canonical → Registry.ResolveModel → Router.Plan → ClaimGate.Reserve → Pool.Select → Forwarder.Forward (with resolved.ProviderModelID) → Settler.Settle (with plan.SnapshotVersion)`.
   - Map registry errors per D1.
   - On `Registry == nil` or `Router == nil` (boot-time misconfig) return 503 `gateway_not_configured`.
   - Audit reasons (registry_unknown / disabled / no_access / backend) stay SERVER-SIDE only. **No public response header.** Codex pass-2 P1 caught that exposing the reason via `X-Huakai-*` header re-leaks the enumeration signal that the uniform 404 was meant to hide. Phase E threads a structured logger.

2. `default_router.go`:
   - Delete `errPoolGroupRequired` + `PlanWithPoolGroupID`.
   - Drop `errors` import (no longer used).
   - `requestPoolGroupID(req)` becomes `if len(req.Model.PoolCandidates) > 0 { return req.Model.PoolCandidates[0] }; return 0`.
   - `Plan()` concatenates: `plan.SnapshotVersion = req.Model.SnapshotVersion + ";router:" + r.SnapshotVersion`. Empty modelStamp falls back to `"registry:unknown"` to avoid leading semicolon.

3. `route_plan.go`:
   - Remove `PlanInput.ExplicitPoolGroupID` field.
   - Update doc comments to reflect Registry-driven flow.

4. `router_test.go`:
   - Delete `TestPlanWithPoolGroupID*` cases.
   - Add `TestPlan_RequiresPoolCandidates` (empty → `no_eligible_pool`).
   - Add `TestPlan_UsesPrimaryCandidate` (multi-candidate → `Attempts[0].PoolGroupID == PoolCandidates[0]`).
   - Add `TestPlan_StampsConcatenatedSnapshot`.
   - Keep RequestID / TenantID / ProtocolFamily validation tests.

5. `billing/billing.go`:
   - Add `SnapshotVersion string` field to `SettleRequest` (additive; existing callers pass empty).

6. `sql/queries/billing_settle.sql`:
   - `InsertUsageRecord` adds `snapshot_version` to the column list and bind list. Re-run `sqlc generate`.

7. `billing/settler.go`:
   - In success path (`Settle`), pass `req.SnapshotVersion` into `InsertUsageRecordParams`. `Abort` path is OUT OF SCOPE per CMB-4 stable signature ([cross-module-boundaries.md:56-63]); deferred until DR opens the contract.

8. `cmd/gateway/main.go`:
   - Construct `*router.DefaultRouter` and add to `deps`.
   - Pass `d.modelRegistry` and `d.routePlanner` (rename from inline) into `ChatHandlerDeps`.
   - Drop the `_ = d.modelRegistry` no-op marker.

9. `gatewayhttp/chat_completions_handler_test.go` (new):
   - Stubs implement `authResolver`, `registry.Registry`, `router.Router`, `billing.ClaimGate`, `pool.Selector`, `billing.Settler`.
   - Cases: HappyPath / RegistryUnknown / RegistryDisabled / RegistryNoAccess / RegistryBackend / RejectsBodyPoolGroupID / NoStreamRejected / NilRegistry503 / NilRouter503.

10. `cmd/gateway/smoke_test.go`:
    - `seedSmokeGraph` adds `models` (with `provider_model_id` matching the existing seeded request model) + `model_aliases` (alias = the request body's model string) + `model_pool_bindings` to the seeded pool group + `model_registry_snapshots` row at `version=1`.
    - Smoke client request body changes from `{"model":"…","stream":true,"pool_group_id":N}` to `{"model":"…","stream":true}`.
    - Cleanup chain (FK-safe order, mirrors [postgres_registry_integration_test.go:103-120]): `model_pool_bindings → model_registry_capabilities → model_aliases → models → model_registry_snapshots → model_registry_tenant_policies → pool_groups → … → tenants`.
    - PG-state assertion #6 added: `usage_records.snapshot_version IS NOT NULL` AND prefix matches `registry:<tid>:`.

### What does NOT change

- Migration 0008 — no schema work in N+5b.
- Anthropic forwarder mock — Slice 5 (option C).
- `ReserveRequest` shape — Tx1 is unaware of snapshot at L0.
- `Settler.Abort` signature — protected by CMB-4 stable contract; abort-path stamping is a future DR.
- Registry package — already correct per N+5a.

---

## Final D1–D7 decisions

### D1. HTTP error code mapping

```
ErrUnknownModel    -> 404 model_not_available  (audit reason: registry_unknown)
ErrModelDisabled   -> 404 model_not_available  (audit reason: registry_disabled)
ErrTenantNoAccess  -> 404 model_not_available  (audit reason: registry_no_access)
ErrRegistryBackend -> 503 registry_backend_error  (audit reason: registry_backend)
```

The audit reason stays server-side. **No public header.** Pass-2 codex review (2026-05-01) caught that putting the internal reason on a response header lets clients distinguish unknown/disabled/no-access by reading the header, which defeats the uniform 404 anti-enumeration design. Phase E threads a structured logger; until then the reason has no L0 surfacing.

### D2. Body `pool_group_id` rejection

```go
// pre-parse to detect removed field BEFORE struct unmarshal
var keys map[string]json.RawMessage
if err := json.Unmarshal(body, &keys); err == nil {
    if _, found := keys["pool_group_id"]; found {
        writeJSONError(w, http.StatusBadRequest, "body_field_disallowed",
            "pool_group_id field removed in N+5b; the gateway resolves the pool from the model alias")
        return
    }
}
// then continue with canonical chatRequest unmarshal
```

`chatRequest` no longer has a `PoolGroupID` field at all.

### D3. Snapshot wiring

- `registry.Resolved.SnapshotVersion` = `"registry:<tid>:<v>"` (already in N+5a).
- `DefaultRouter.Plan` concatenates: `plan.SnapshotVersion = modelStamp + ";router:" + r.SnapshotVersion` where `modelStamp = req.Model.SnapshotVersion` (or `"registry:unknown"` if empty).
- New: `billing.SettleRequest.SnapshotVersion string`. Chat handler passes `plan.SnapshotVersion` here.
- `Settler.Settle` writes `req.SnapshotVersion` into `usage_records.snapshot_version`.
- Format documented in 0008 ([0008_model_registry.up.sql:248-258]).

### D4. Chat handler MUST call `Router.Plan`

Auth → Registry → Router → Reserve → Pool.Select → Forward → Settle. Bypassing Router would re-introduce the architecture violation N+5b is meant to remove.

### D5. Single commit

Atomic. Revert = one step. Smoke must be green before commit.

### D6. Test seams (handler-local interfaces)

```go
// in chat_completions_handler.go:
type authResolver interface {
    Resolve(ctx context.Context, req *http.Request) (auth.Identity, error)
}
type ChatHandlerDeps struct {
    Auth      authResolver           // was *auth.APIKeyResolver
    Registry  registry.Registry      // already an interface
    Router    router.Router          // already an interface
    ClaimGate billing.ClaimGate      // already an interface
    Selector  pool.Selector          // was *pool.DefaultSelector
    Forwarder *gateway.StreamForwarder
    Settler   billing.Settler        // already an interface
    BillingPolicyVersion string
    RequestClass         string
}
```

`Forwarder` stays concrete because the handler test injects `bytesReader` directly; refactoring the forwarder is Slice 5 work.

### D7. Use `resolved.ProviderModelID` at upstream

`gateway.ForwardRequest.Model` was previously set to `req.Model` (client's public alias). N+5b uses `resolved.ProviderModelID` (which carries the binding-level upstream-id override per N+5a [postgres_registry.go:135-138]; the per-binding upstream rename pattern was evidence-mined 2026-04-30T09:35Z). This makes the override column actually do something. Cost = 1 line.

---

## Test plan

### Unit (no DB)

`backend/internal/router/router_test.go` updates per touch list #4.

`backend/internal/gatewayhttp/chat_completions_handler_test.go` (NEW):

| Test | Setup | Assert |
|---|---|---|
| `TestHandler_HappyPath` | stub Registry: `Resolved{PublicAlias:"claude-opus-4-7", ProviderModelID:"claude-opus-4-7", PoolCandidates:[]int64{42}, SnapshotVersion:"registry:1:1"}`; stub Router: `RoutePlan{Attempts:[]{PoolGroupID:42}, SnapshotVersion:"registry:1:1;router:v0.1-phase-c"}`; stub Selector returns `AccountID:99`; stub Settler captures the `SettleRequest` for assertion | 200 + Settler called with `SnapshotVersion="registry:1:1;router:v0.1-phase-c"` + ForwardRequest.Model="claude-opus-4-7" |
| `TestHandler_RegistryUnknown` | Registry returns `ErrUnknownModel` | 404 + `model_not_available`; **no `X-Huakai-Audit-Reason` header** (pass-2 fix) |
| `TestHandler_RegistryDisabled` | Registry returns `ErrModelDisabled` | 404 + audit `registry_disabled` |
| `TestHandler_RegistryNoAccess` | Registry returns `ErrTenantNoAccess` | 404 + audit `registry_no_access` |
| `TestHandler_RegistryBackend` | Registry returns `ErrRegistryBackend` | 503 + `registry_backend_error` + audit `registry_backend` |
| `TestHandler_RejectsBodyPoolGroupID` | body includes `"pool_group_id":5` | 400 + `body_field_disallowed` |
| `TestHandler_RejectsBodyPoolGroupIDZero` | body includes `"pool_group_id":0` | 400 (raw-key detection catches it; pointer would have missed) |
| `TestHandler_NoStream` | body has `stream:false` | 400 (preserve existing) |
| `TestHandler_NilRegistry` | `ChatHandlerDeps.Registry == nil` | 503 `gateway_not_configured` |
| `TestHandler_NilRouter` | `ChatHandlerDeps.Router == nil` | 503 `gateway_not_configured` |

### Integration (`-tags=integration_pg`)

- Existing 14 registry tests stay GREEN.
- `settler_integration_test.go` extended: pass `SnapshotVersion="registry:1:7;router:v0.1-phase-c"`; assert `usage_records.snapshot_version` matches.

### Smoke (`-tags=smoke`)

`go test -tags=smoke ./cmd/gateway` — full HTTP boot + DB seed + 5 (now 6) PG state assertions. Runs after migrations applied.

---

## Risk matrix

| Risk | Trigger | Detection | Mitigation |
|---|---|---|---|
| Old client keeps sending `pool_group_id` | Pre-N+5b client | 400 `body_field_disallowed` returned | Explicit error message + release note. No silent ignore. |
| Registry binding resolves to pool group with no eligible accounts at request time | Provider accounts cooled / disabled / removed after registry read | `Pool.Select` returns `ErrNoEligibleAccount`; existing handler aborts claim and returns 503 + `Retry-After` ([chat_completions_handler.go:185-195]) | Existing path already correct; no change. Registry doesn't second-guess pool health (CMB-1). |
| Snapshot version mismatch between Resolve and Settle | Admin updates registry after Resolve commits but before Settle (race) | Stamp records the version active at Resolve time, not Settle time | Semantically correct — stamp's purpose is "what config built this plan". REPEATABLE READ TX in `ResolveModel` (N+5a P2 fix) keeps Resolve internally consistent. |
| Smoke FK cleanup order wrong | DELETE `pool_groups` before `model_pool_bindings` | FK violation at cleanup | Cleanup order: bindings → capabilities → aliases → models → snapshots → tenant_policies → pool_groups → … → tenants. |
| sqlc / generated code mismatch | `billing_settle.sql` adds column but generated code stale | `go test ./...` compile fail in `settler.go` | Run `sqlc generate` from `backend/`; include generated diff in commit. |
| Nil dependency panic | Main forgets to wire Registry/Router | Unit test asserts 503 from nil-deps; runtime would panic | Explicit nil checks at handler entry returning 503 `gateway_not_configured` before any other work. |
| Raw-key pre-parse fails on a body with comments / BOM | Unconventional client | `json.Unmarshal(body, &keys)` returns err; we currently `if err == nil` before checking keys, so an unparseable body skips the check and falls through to canonical unmarshal which then 400s on its own grounds | Acceptable — invalid JSON dies at canonical unmarshal regardless. |
| `chatRequest` struct still has `PoolGroupID` field somewhere referenced | Stale comment / shadow definition | `go build` fails; `grep PoolGroupID` on backend confirms none remain | Search `internal/`, `cmd/`, `pkg/` for `PoolGroupID` after edits. |
| Settler integration test fixture drift | Existing fixture doesn't pass SnapshotVersion | `SnapshotVersion=""` in usage_records — column is nullable so no SQL error, just stale data | Update fixture to pass an explicit version; new assertion catches it. |
| Abort path leaves `usage_records.snapshot_version` NULL | Forwarder fails after Pool acquire → handler calls Settler.Abort, signature has no snapshot field per CMB-4 | Manually inspecting `usage_records` rows after a forward error shows NULL | Documented limitation. Future DR opens `Abort` signature; abort-path stamping deferred. |
| `resolved.ProviderModelID` blank because binding has no override | Common case (override is nullable) | resolver fills `ProviderModelID` from `models.default_provider_model_id`, never empty | No mitigation needed; verified in resolver code path [postgres_registry.go:113]. |

---

## CMB compliance

| Invariant | Status | Evidence |
|---|---|---|
| **CMB-1** Router does not read credentials | ✅ | Router input is `RequestContext` + `ResolvedModel` + `RequestFeatures`; no auth/credentials field. ([cross-module-boundaries.md:99-105]) |
| **CMB-2** Pool does not compute cost | ✅ | Untouched. |
| **CMB-3** Adapter does not bypass Ledger | ✅ | Forwarder still returns `UsageRecordDraft`; Settler still owns the write. ([cross-module-boundaries.md:115-129]) |
| **CMB-4** Ledger settles only via events | ✅ | `SettleRequest` is the explicit event; new `SnapshotVersion` field is data, not behavior. |
| **CMB-5** Credentials never logged | ✅ | Audit reason field carries string tags only (`registry_disabled` etc). |
| **CMB-6** request_id present | ✅ | `middleware.GetReqID(r.Context())` threaded into `RequestContext.RequestID`. |
| **CMB-7** Layer write discipline | ✅ | Registry SELECT-only ([registry.sql:1-7]); Router emits no DB writes; Settler is the only writer; new column writes are inside existing Tx2. |

---

## Sequencing + rollback

**Single commit**:
1. Update router (delete escape hatch, rewrite Plan, add snapshot concatenation).
2. Update billing.SettleRequest + billing_settle.sql + sqlc generate.
3. Update settler.go to pass SnapshotVersion through.
4. Refactor handler deps + flow.
5. Add handler unit tests.
6. Update main.go wiring.
7. Update smoke test (seed registry rows + drop pool_group_id + new PG assertion).
8. Run unit + integration_pg + smoke tests.
9. Run `codex exec review --uncommitted --full-auto`; address findings.
10. Commit.

**Rollback**: `git revert HEAD`. Migration 0008 is additive + nullable, so it stays applied. Registry tables are unused after revert; operator can leave them or drop them.

---

## Time estimate

| Step | Estimated |
|---|---|
| Router changes | 30 min |
| Billing + sqlc | 30 min |
| Handler refactor (interfaces + raw-JSON pre-parse + flow + error mapping) | 60 min |
| New unit test file (~10 cases) | 60 min |
| Smoke seed + body update + cleanup chain | 30 min |
| Test/lint regression + codex review iteration | 60 min |
| Total | **~4.5 hours** |

(My initial 2h estimate was optimistic; Codex's 2-3h was closer; the unit test file is the long pole.)

---

## Open questions for Owner (final)

None blocking. The Codex-flagged Open Questions are resolved by this synthesis:

- ✅ Audit reason → response header now, structured log Phase E.
- ✅ SettleRequest.SnapshotVersion success-only; Abort-path stamping deferred to a DR.
- ✅ ProviderModelID flowed into ForwardRequest now (D7).

If you want any of these reopened, say so before the implementation commit.

---

Source files read (Claude this round): Codex N+5b plan (full), [route_plan.go], [default_router.go], [registry.go + postgres_registry.go + errors.go], [chat_completions_handler.go], [main.go], [smoke_test.go], [billing.go grep], [settler.go grep], [0008_model_registry.up.sql], [registry.sql], [N+5 synthesized plan], [cross-module-boundaries.md], [Makefile].
Lane: implementer (synthesis post-round-1)
Agent: Claude (claude-opus-4-7)
UTC timestamp: 2026-05-01T00:30:00Z
Citation discipline: Owner directive 2026-04-30 "所有的动作都不允许凭借自己的记忆库知识" + 2026-04-30 "对了 你们除了看借鉴的项目。还要去看当前大模型官方的更新".

**Awaiting Owner go signal before N+5b coding starts.**
