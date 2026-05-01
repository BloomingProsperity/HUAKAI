# N+5b — Chat handler rewrite + escape hatch deletion (Claude independent draft)

| Field | Value |
| --- | --- |
| Status | Drafted independently per CLAUDE.md #10 (parallel-draft + cross-discuss) |
| Counterpart | `docs/plans/2026-04-30-n5b-handler-rewrite-codex.md` (drafted in parallel; not read while authoring) |
| Lane | specifier (Claude) |
| Driver | A → B → C order picked by Owner 2026-04-30; Slice 2 finalization (consume the Registry that N+5a wired but never used) |
| Predecessor | N+5a (commit `ddef4e4`) — Registry exists, schema applied, 14 integration tests green |
| Migration | None — pure code rewire on top of 0008 |
| Authority | Owner pre-decided slice order A→B→C; synthesis arbitration of D1–D5 below still requires Owner pick per CLAUDE.md #10 |
| Citation discipline | Per Owner directive 2026-04-30 "所有的动作都不允许凭借自己的记忆库知识"; every claim points to repo file + line range or fresh fetch |

---

## What this slice changes (one paragraph)

`/v1/chat/completions` currently parses `pool_group_id` from the JSON body and threads it through `ClaimGate.Reserve` → `pool.Selector.Select` → `Settler.Settle`. The Registry built in N+5a (commit `ddef4e4`) is wired into `cmd/gateway/main.go` deps but **no caller invokes it**. Slice 2 finalization deletes the body-side `pool_group_id` field, calls `registry.PostgresRegistry.ResolveModel` to convert the public alias into a `Resolved`, threads `Resolved.PoolCandidates[0]` through the rest of the money path, deletes `DefaultRouter.PlanWithPoolGroupID` + `errPoolGroupRequired` + `PlanInput.ExplicitPoolGroupID`, and stamps `usage_records.snapshot_version` (the column added by migration 0008 that nobody is writing yet).

After this slice, customers can call HUAKAI like any public LLM API: `{"model":"claude-opus-4-7","messages":[…],"stream":true}` — no internal pool ids leaked.

---

## Goals (acceptance criteria)

1. `chat_completions_handler.go` calls `Registry.ResolveModel(ctx, req.Model, ident.TenantID)` BEFORE `ClaimGate.Reserve`.
2. `chatRequest.PoolGroupID` field is REMOVED. Body schema accepts only `{model, messages, stream}` (plus `Idempotency-Key` header).
3. The four `registry.Err*` classes map to deterministic HTTP statuses — uniform 404 for client + audit-internal reason per the synthesized plan §D4.
4. `DefaultRouter.PlanWithPoolGroupID`, `errPoolGroupRequired`, and `PlanInput.ExplicitPoolGroupID` no longer exist anywhere in the repo. `default_router.go` line 95 (`if req.ExplicitPoolGroupID != 0`) replaced with `if len(req.Model.PoolCandidates) > 0 { return req.Model.PoolCandidates[0] }`.
5. `usage_records.snapshot_version` is populated on every successful settle. Format: `registry:<tid>:<v>;router:<router_policy_v>`.
6. Smoke test (`backend/cmd/gateway/smoke_test.go`) seeds Registry rows in `seedSmokeGraph`, sends body without `pool_group_id`, and stays GREEN end-to-end (5/5 PG state assertions still pass).
7. Existing N+4a + N+5a integration tests stay GREEN (no regression).
8. CMB-1 / CMB-3 / CMB-7 still hold (auditable via reviewer-lane + codex review pass).

---

## Non-goals (explicitly deferred)

- **Real Anthropic upstream** — still mock; Slice 5 (option C in Owner's order).
- **Capability filtering at Router** — Router currently builds `RequiredCapabilities` from `RequestFeatures`; whether the Pool actually filters by capability is Phase E.
- **Per-model rate limits** — `model_pool_bindings.rpm_limit/tpm_limit` columns are stored but not enforced. Phase E rate gate.
- **Weighted load-balance** — `selection_mode='priority_weighted'` is honored as `strict_priority` at L0. Slice 5.
- **Multi-attempt fallback** — `RoutePlan.Attempts` only ever has 1 entry until Executor lands.
- **Registry cache** — D2 says no L0 cache. Slice 5 + outbox invalidation.

---

## Decision points for Owner

### D1. HTTP status mapping for the 4 registry error classes

The synthesized N+5a plan landed on **uniform 404 for client + audit-internal reason**. I retain this without challenge — anti-enumeration parity with auth's uniform 401 (codex N+4a P3 finding). Final mapping:

| Registry error | Client HTTP | Client `error.code` | Server audit reason |
|---|---|---|---|
| `ErrUnknownModel` | 404 | `model_not_available` | `registry_unknown` |
| `ErrModelDisabled` | 404 | `model_not_available` | `registry_disabled` |
| `ErrTenantNoAccess` | 404 | `model_not_available` | `registry_no_access` |
| `ErrRegistryBackend` | 503 | `registry_backend_error` | `registry_backend` |

Rationale: 404 vs 403 was debated (Codex round-1 picked 403 for actionable; round-2 converged with Claude on uniform 404). Anthropic / OpenAI public model IDs are not secrets, but **whether a tenant has access to a specific model** is enumeration signal — surfacing 403 vs 404 gives an attacker access-vector-by-status-code. Cost of uniform 404 = ops loses one debugging affordance, mitigated by the audit-internal reason field.

**Claude pick: same as synthesized plan (D4 carry-forward). No new decision.**

### D2. Body `pool_group_id` transition handling

**Option A**: silently ignore (back-compat lenient).
**Option B**: reject with HTTP 400 + clear error message ("pool_group_id field removed; use model alias resolution").
**Option C**: log warning + ignore.

**Claude pick: Option B.** Reasoning:
- HUAKAI has zero external customers right now (per blueprint v0.2 — pre-L0). Blast radius of "old client breaks" = 0.
- We control the smoke test (the only known caller). Updating it is part of this slice.
- Lenient parsing is the kind of decision that creates 5-year technical debt; we should set the API contract while costs are still zero.
- Detection requires `*int64` (pointer) not `int64` (value zero). Implementation cost ~3 lines.

### D3. Snapshot stamp wiring

The Registry returns `Resolved.SnapshotVersion = "registry:<tid>:<v>"`. `DefaultRouter` already stamps `RoutePlan.SnapshotVersion = "v0.1-phase-c"` (literal, see `default_router.go:29`). The `usage_records.snapshot_version` column wants the FULL stamp.

**Option A**: Chat handler concatenates: `fmt.Sprintf("%s;router:%s", resolved.SnapshotVersion, plan.SnapshotVersion)`. Settler receives via new field.
**Option B**: Router accepts `Resolved.SnapshotVersion` as input and emits the concatenated version on `RoutePlan`. Chat handler reads only `plan.SnapshotVersion`.
**Option C**: Settler receives both separately and concatenates internally.

**Claude pick: Option B.** Reasoning:
- Router is the natural producer of the final stamp — it already owns `RoutePlan.SnapshotVersion`.
- Chat handler stays thin: it forwards strings around, doesn't compose them.
- Settler stays focused on Tx2 invariants — adding it a "concat two strings" responsibility would muddy CMB-4 (Ledger commits are event-driven, not derived).
- Concrete shape: `DefaultRouter.Plan` reads `req.Model.SnapshotVersion`; emits `RoutePlan.SnapshotVersion = req.Model.SnapshotVersion + ";router:" + r.SnapshotVersion`. Empty input falls back to just `"router:..."` (no panics).

### D4. Should chat handler call `Router.Plan` or skip Router?

Currently chat handler **does NOT call Router**. It builds `pool.SelectionRequest` directly from `req.PoolGroupID`.

**Option A**: skip Router; chat handler reads `resolved.PoolCandidates[0]` and passes to Selector. Router stays unused.
**Option B**: call `Router.Plan(ctx, PlanInput{Context, Model: routerResolvedModel, Features})`; chat handler uses `plan.Attempts[0].PoolGroupID`.

**Claude pick: Option B.** Reasoning:
- CMB-1 says the call order is `Auth → Registry → Router → (Executor loop: Pool → Adapter → Ledger)`. Skipping Router establishes a precedent that's hard to undo.
- Router currently does 4 things even at L0: validates RequestID, validates TenantID, validates ProtocolFamily, stamps SnapshotVersion. All four are real value, not vestigial.
- Once `PlanWithPoolGroupID` is gone, `Plan` is the only entry — calling it is the entire point.
- Cost: ~5 lines in chat handler.

### D5. Sequencing — single PR or split?

**Option A**: single commit. Atomic; clear rollback target.
**Option B**: split into N+5b1 (handler rewrite, escape hatch still alive) → N+5b2 (delete escape hatch + smoke change).

**Claude pick: Option A.** Reasoning:
- N+4a/b split was justified by different blast radii (auth replacement vs ledger backfill). N+5a/b split was justified by additive vs breaking. Inside N+5b, the changes form one logical unit: every step requires every other step to compile.
- If smoke breaks, single-commit revert is cleaner than two-commit unwind.
- Codex review cycle stays one round.

### D6 (new). Should Settler's INSERT on `usage_records` carry the snapshot version, or should Tx1 (Reserve) pre-stamp `billing_ledger_claims`?

The schema column added in 0008 is `usage_records.snapshot_version` only. `billing_ledger_claims` doesn't have a snapshot field (it has `billing_policy_version` instead). For audit replay you need to know "what registry/router config produced this CLAIM" not just "this usage record" — but in HUAKAI's design, the claim and the usage record are 1:1 per attempt anyway, so the snapshot on usage_records is sufficient.

**Claude pick: keep N+5b scope narrow — only `usage_records.snapshot_version` is populated.** Adding the column to `billing_ledger_claims` would be migration 0009, out of scope. Document the design choice in the commit message.

---

## Touch list

| File | Change | Lines |
|---|---|---|
| `backend/internal/router/route_plan.go` | Remove `PlanInput.ExplicitPoolGroupID` field. | -1 |
| `backend/internal/router/default_router.go` | Delete `PlanWithPoolGroupID`, `errPoolGroupRequired`. Rewrite `requestPoolGroupID` to use `req.Model.PoolCandidates[0]`. Rewrite `Plan` to require `PoolCandidates` non-empty + emit concatenated SnapshotVersion. | ~-25, +10 |
| `backend/internal/router/router_test.go` | Drop `TestPlanWithPoolGroupID*` tests; add `TestPlan_RequiresPoolCandidates` + `TestPlan_StampsConcatenatedSnapshot`. | ~-30, +30 |
| `backend/internal/registry/registry.go` | (no change — Resolved already carries SnapshotVersion + PoolCandidates) | 0 |
| `backend/internal/gatewayhttp/chat_completions_handler.go` | Add `Registry registry.Registry` + `Router router.Router` to `ChatHandlerDeps`. Drop `chatRequest.PoolGroupID` (replace with `*int64` for transition rejection). Insert Registry resolve + Router.Plan calls between Auth and Reserve. Map 4 registry errors. Pass SnapshotVersion to Settle. | ~+60 |
| `backend/internal/gatewayhttp/chat_completions_handler_test.go` | New file: 4 error mappings + happy path with stub Registry + Router. | ~+250 |
| `backend/cmd/gateway/main.go` | Pass `d.modelRegistry` + new `d.router` (or construct inline) into `ChatHandlerDeps`. | ~+5 |
| `backend/internal/billing/billing.go` | Add `SnapshotVersion string` to `SettleRequest`. (`ReserveRequest` unchanged — Tx1 doesn't know snapshot yet at L0.) | ~+1 |
| `backend/internal/billing/settler.go` | Thread `req.SnapshotVersion` into `usage_records` INSERT; pass through `coalesceString` so empty stays empty (column is nullable). | ~+3 |
| `backend/sql/queries/billing_settle.sql` | Update INSERT to include `snapshot_version` column. Re-run sqlc. | +2 |
| `backend/internal/db/billing_settle.sql.go` | sqlc regenerated. | (auto) |
| `backend/internal/billing/settler_integration_test.go` | Add SnapshotVersion to settle fixture; add new assertion that the row carries it. | ~+10 |
| `backend/cmd/gateway/smoke_test.go` | `seedSmokeGraph`: insert into `models` + `model_aliases` + `model_pool_bindings` (+ optional `model_registry_snapshots` row at version 1 for stamp determinism). Smoke client body drops `pool_group_id`. Cleanup chain adds DELETE of the registry rows in FK-safe order. PG-state assertion #6 added: `usage_records.snapshot_version IS NOT NULL`. | ~+50 |

Total: roughly **+400 lines / -60 lines**. ~80% is the test file for the chat handler (which is currently untested).

---

## Code shape sketches

### Chat handler resolve flow (new code)

```go
// after auth.Resolve(...)

// Slice 2 finalization (N+5b): replace body pool_group_id with Registry resolution.
resolved, err := d.Registry.ResolveModel(ctx, req.Model, ident.TenantID)
switch {
case errors.Is(err, registry.ErrRegistryBackend):
    writeJSONError(w, http.StatusServiceUnavailable, "registry_backend_error", "registry transient failure")
    return
case errors.Is(err, registry.ErrUnknownModel),
     errors.Is(err, registry.ErrModelDisabled),
     errors.Is(err, registry.ErrTenantNoAccess):
    // Uniform 404 anti-enum (D1). Audit reason logged via response header at L0;
    // structured logger lands in Phase E.
    w.Header().Set("X-Huakai-Audit-Reason", auditReasonFor(err))
    writeJSONError(w, http.StatusNotFound, "model_not_available", "model not available")
    return
case err != nil:
    writeJSONError(w, http.StatusInternalServerError, "registry_unknown_error", err.Error())
    return
}

// Build router input from registry output.
plan, err := d.Router.Plan(ctx, router.PlanInput{
    Context: router.RequestContext{
        TenantID:  ident.TenantID,
        UserID:    ident.UserID,
        APIKeyID:  ident.APIKeyID,
        RequestID: middleware.GetReqID(r.Context()),
    },
    Model: router.ResolvedModel{
        PublicAlias:     resolved.PublicAlias,
        InternalModelID: resolved.CanonicalModelID,
        ProviderModelID: resolved.ProviderModelID,
        ContextWindow:   resolved.ContextWindow,
        Capabilities:    resolved.Capabilities,
        PricingClass:    resolved.PricingClass,
        ProtocolFamily:  resolved.ProtocolFamily,
        PoolCandidates:  resolved.PoolCandidates,
        SnapshotVersion: resolved.SnapshotVersion,
    },
    Features: router.RequestFeatures{Stream: req.Stream},
})
if err != nil {
    writeJSONError(w, http.StatusInternalServerError, "router_plan_error", err.Error())
    return
}
attempt := plan.Attempts[0] // L0 always single-attempt
poolGroupID := attempt.PoolGroupID
```

### chatRequest with `*int64` for transition

```go
type chatRequest struct {
    Model       string        `json:"model"`
    Messages    []chatMessage `json:"messages"`
    Stream      bool          `json:"stream"`
    PoolGroupID *int64        `json:"pool_group_id,omitempty"` // N+5b: REMOVED — present = 400
}

// inside handler, after json.Unmarshal:
if req.PoolGroupID != nil {
    writeJSONError(w, http.StatusBadRequest, "pool_group_id_removed",
        "pool_group_id field removed in N+5b; the gateway resolves the pool from the model alias")
    return
}
```

### Router stamping concatenated snapshot

```go
// in DefaultRouter.Plan:
modelStamp := req.Model.SnapshotVersion
if modelStamp == "" {
    modelStamp = "registry:unknown"
}
return RoutePlan{
    Attempts: []AttemptPlan{{ /* ... */ }},
    AttemptBudget: 1,
    SnapshotVersion: modelStamp + ";router:" + r.SnapshotVersion,
    // ...
}, nil
```

### `requestPoolGroupID` rewrite

```go
func requestPoolGroupID(req PlanInput) int64 {
    if len(req.Model.PoolCandidates) > 0 {
        return req.Model.PoolCandidates[0]
    }
    return 0
}
```

The `if poolGroupID == 0 { return PlanError{Code:"no_eligible_pool"} }` guard stays in `Plan` — but now triggers on Registry returning empty PoolCandidates (which the Registry already prevents via `ErrTenantNoAccess`, so it's defense-in-depth).

---

## Test plan

### Unit tests (no DB) — add `chat_completions_handler_test.go`

| Test | Setup | Assert |
|---|---|---|
| `TestHandler_HappyPath` | stub Registry returns Resolved with `PoolCandidates=[42]`; stub Router returns Attempts[0]={PoolGroupID:42}; stub Selector returns AccountID=99; stub Settler succeeds | 200 + Content-Type: text/event-stream + Settler called with SnapshotVersion non-empty |
| `TestHandler_RegistryUnknownModel` | stub returns `ErrUnknownModel` | 404 + body `{"error":{"code":"model_not_available", ...}}` + X-Huakai-Audit-Reason: registry_unknown |
| `TestHandler_RegistryDisabled` | stub returns `ErrModelDisabled` | 404 + audit reason `registry_disabled` |
| `TestHandler_RegistryNoAccess` | stub returns `ErrTenantNoAccess` | 404 + audit reason `registry_no_access` |
| `TestHandler_RegistryBackend` | stub returns `ErrRegistryBackend` | 503 + body `registry_backend_error` |
| `TestHandler_RejectsBodyPoolGroupID` | request body includes `"pool_group_id": 5` | 400 + body `pool_group_id_removed` |
| `TestHandler_NoStreamRejected` | (preserve existing behavior) `stream=false` | 400 |

Stub Registry/Router/Selector/Settler implementations live in this test file; no DB needed.

### Router unit tests — update `router_test.go`

| Test | Status |
|---|---|
| `TestPlanWithPoolGroupID*` | DELETED |
| `TestPlan_RequiresPoolCandidates` | NEW: empty `PoolCandidates` → `no_eligible_pool` |
| `TestPlan_UsesPrimaryCandidate` | NEW: `PoolCandidates=[7,9,11]` → `Attempts[0].PoolGroupID == 7` |
| `TestPlan_StampsConcatenatedSnapshot` | NEW: `req.Model.SnapshotVersion="registry:42:5"` → `plan.SnapshotVersion="registry:42:5;router:v0.1-phase-c"` |
| existing validation tests (RequestID / TenantID / ProtocolFamily) | KEEP |

### Settler integration test — extend `settler_integration_test.go`

Add SnapshotVersion to settle fixture; assert `usage_records.snapshot_version` matches the fixture.

### Smoke test changes

`seedSmokeGraph` adds (after the existing pool group + provider account seeds):

```go
// Slice 2 (N+5b): seed Registry rows so the smoke chat alias resolves end-to-end.
var modelID int64
require.NoError(pool.QueryRow(ctx,
    `INSERT INTO models (tenant_id, scope, canonical_id, protocol_family,
                         default_provider_model_id, status)
     VALUES ($1, 'tenant', $2, 'anthropic_messages', 'claude-opus-4-7', 'active')
     RETURNING id`,
    s.tenantID, "anthropic/claude-opus-4-7-"+s.suffix,
).Scan(&modelID))

require.NoError(pool.Exec(ctx,
    `INSERT INTO model_aliases (tenant_id, scope, model_id, public_alias_normalized, public_alias_display, status)
     VALUES ($1, 'tenant', $2, 'claude-opus-4-7', 'claude-opus-4-7', 'active')`,
    s.tenantID, modelID))

require.NoError(pool.Exec(ctx,
    `INSERT INTO model_pool_bindings (tenant_id, model_id, pool_group_id, priority, enabled)
     VALUES ($1, $2, $3, 100, true)`,
    s.tenantID, modelID, s.poolGroupID))

require.NoError(pool.Exec(ctx,
    `INSERT INTO model_registry_snapshots (tenant_id, version) VALUES ($1, 1)
     ON CONFLICT (tenant_id) DO UPDATE SET version=1`,
    s.tenantID))
```

Smoke client body changes from
```json
{"model":"…","messages":[…],"stream":true,"pool_group_id":42}
```
to
```json
{"model":"claude-opus-4-7","messages":[…],"stream":true}
```

Cleanup chain adds (before pool_groups DELETE):
```sql
DELETE FROM model_pool_bindings WHERE tenant_id = $1;
DELETE FROM model_aliases WHERE tenant_id = $1;
DELETE FROM models WHERE tenant_id = $1;
DELETE FROM model_registry_snapshots WHERE tenant_id = $1;
```

PG-state assertion #6 added: `usage_records.snapshot_version IS NOT NULL` AND prefix-matches `registry:<tid>:`.

---

## Risk matrix

| Risk | Trigger | Detection | Mitigation |
|---|---|---|---|
| Old client still sends `pool_group_id` | Pre-N+5b client not updated | 400 with `pool_group_id_removed` | Fail-fast 400 with explicit message; no external customers yet so blast=0 |
| Registry returns binding to pool group with no eligible accounts at request time | Binding seeded but provider_accounts disabled/cooled-down all | Pool.Select returns `ErrNoEligibleAccount` → 503 `no_capacity` | Existing pool path handles it; Registry doesn't second-guess pool health (CMB-1 + CMB-7) |
| Snapshot version mismatch between Registry resolve and Settler insert | Admin updates registry mid-request (theoretically prevented by N+5a P2 REPEATABLE READ TX inside ResolveModel) | usage_records row carries the version Resolve READ, not the version active at Settle TIME — semantically correct | None needed; the stamp's purpose is "what config built this plan", not "what config is live now" |
| Smoke FK cleanup order wrong | DELETE pool_groups before model_pool_bindings | FK violation surfaced as test failure with exact constraint name | DELETE bindings → aliases → models → snapshot → pool_groups; verified locally before commit |
| Router skips Plan call (D4=A regression) | Refactor accidentally bypasses Plan | Plan-side validators (RequestID, TenantID, ProtocolFamily) stop firing | NEW unit test `TestHandler_HappyPath` asserts a stub Router.Plan was called with non-empty RequestID; CMB-1 reviewer-lane gate |
| `chatRequest.PoolGroupID = *int64` parses 0 as `nil` (Go json default) | Client sends `"pool_group_id": 0` | parses to `*int64{0}` not `nil`, triggers our 400 | json.Number or pointer is correct; verified via TestHandler_RejectsBodyPoolGroupID with explicit `0` value |
| Settler integration test fixture no longer covers all paths after adding SnapshotVersion | Drift between unit and integration tests | CI fails on integration | Extend the existing fixture; don't introduce a parallel one |
| Concatenation produces unexpected suffix on missing inputs | Registry returns Resolved but SnapshotVersion empty (legacy edge) | Stamp = `;router:v0.1-phase-c` (leading semicolon) | Router fallback: `if modelStamp == "" { modelStamp = "registry:unknown" }` |

---

## CMB compliance

- **CMB-1 (Router does not read credentials)**: Router gets `ResolvedModel` only. Registry produces it. No `auth.GetAccessToken` call from chat handler that the Router consumes. ✅
- **CMB-3 (Adapter does not bypass Ledger)**: Forwarder still returns `UsageRecordDraft`; Settler still owns the ledger write. Unchanged. ✅
- **CMB-4 (Ledger settles only via events)**: SettleRequest is the explicit event. Adding `SnapshotVersion` field doesn't change the event-driven invariant. ✅
- **CMB-5 (Credentials never logged)**: Audit reason field carries `registry_disabled` etc — string tags only, no credential field. ✅
- **CMB-6 (request_id present)**: chi middleware sets request_id pre-auth; chat handler reads via `middleware.GetReqID`. ✅
- **CMB-7 (Layer write discipline)**: Registry stays read-only (REPEATABLE READ TX in N+5a, untouched). Router stays write-free (it returns RoutePlan, doesn't insert). Settler is the only writer, behavior preserved. ✅

---

## Sequencing

**Single commit** (D5=A). Touch list compiles only after every change is in place; no half-state where smoke could be expected to pass.

Codex review pass on uncommitted changes BEFORE git commit (per CLAUDE.md #8 per-commit codex review).

If smoke test fails at integration time, single revert (`git revert HEAD`) restores N+5a state cleanly — schema unchanged, registry rows can be DELETE'd by any operator.

---

## Estimated effort

- ~2 hours coding
- ~30 min running smoke + integration regression
- ~10 min codex review iteration
- ~5 min commit + Owner surface

Total: **~3 hours**.

---

## What I'd ask Codex in synthesis

1. Is `*int64` reliably distinguishable from absent vs 0 across `json.Unmarshal` paths I haven't tested? Unit test #6 covers the explicit-zero case but I'd appreciate a different angle.
2. Should the audit reason live in a response header (`X-Huakai-Audit-Reason`) or in a structured log? L0 has no logger threaded into the handler; I went with header for now. Codex may have a stronger pattern.
3. Is there any reason to keep `PlanInput.ExplicitPoolGroupID` for backward compat with future Slice-3 forced-route override? My read: Slice 3 should add a NEW field (e.g. `ForcedPoolGroupID`) with explicit RBAC, not resurrect the old one. Codex may differ.
4. Should N+5b also stamp `billing_ledger_claims.snapshot_version`? I said no (out of scope — needs migration 0009). Codex may want both stamped together for audit completeness.

---

Source files read: backend/internal/router/route_plan.go, backend/internal/router/default_router.go, backend/internal/registry/{registry,postgres_registry,errors,normalize}.go, backend/internal/gatewayhttp/chat_completions_handler.go, backend/cmd/gateway/main.go, backend/cmd/gateway/smoke_test.go, backend/internal/billing/billing.go (lines 47–80 grep'd), backend/internal/billing/settler.go (lines 113–269 grep'd), backend/sql/migrations/0008_model_registry.up.sql, docs/plans/2026-04-30-n5-model-registry.md, docs/specs/_invariants/cross-module-boundaries.md, CLAUDE.md.
Lane: specifier
Agent: Claude (claude-opus-4-7)
UTC timestamp: 2026-04-30T10:30:00Z
