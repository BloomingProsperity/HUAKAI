# 2026-04-30 Phase C — Gateway Wiring (synthesized)

| Field | Value |
| --- | --- |
| Owner directive | "fusion 体跑起来" — only counts when a real PG row exists with status='committed' after a real HTTP request returns 200 |
| Synthesis source | Parallel-draft per AGENTS.md §Plan Cross-Review: [-claude.md](2026-04-30-phase-c-gateway-wiring-claude.md) + [-codex.md](2026-04-30-phase-c-gateway-wiring-codex.md). Owner delegated synthesis decision to Claude. |
| Decision authority | Claude (this synthesis), per Owner directive "你们讨论后由你进行决定最终的方案进行下去" |
| Estimate | 3-5h (per Codex; Claude's 2-3h was unrealistic given selector-adapter gap caught by Codex) |
| Predecessor | Phase B.5 (commit `fb340d2`) — Tx1 ClaimGate + Tx2 Settler vs real PG |

---

## What the diff revealed

### Where both plans agreed (kept verbatim)

- Only `POST /v1/chat/completions` becomes real this phase; other routes remain 501.
- Real Tx1 → upstream → real Tx2 in a strictly synchronous pipeline. **NO** queueing of settlement (F-OBS-001 forbids it).
- Mocked upstream SSE in-process; no real network call to Anthropic/OpenAI.
- Smoke test asserts BOTH HTTP correctness AND PostgreSQL row state.
- Fail-closed: missing DB / config → 503, never 200.
- No `0007_api_keys_users` schema migration this phase (defer to Phase E with security review).

### Where they conflicted — adopted Codex's choice

| Conflict | Claude's pick | Codex's pick | **Decision** |
|---|---|---|---|
| Inbound auth | plaintext `SELECT api_keys.token = $1` | env-resolver `HUAKAI_SMOKE_BEARER_TOKEN` + smoke-only labels | **Codex**: `api_keys` table doesn't exist; Claude's path was schema-fiction |
| Selector wiring | "just construct DefaultSelector(repo)" | needs real DB-backed `SlotManager` + `pool.ClaimGate` adapters | **Codex**: verified — current code has only nil/mem `SlotManager`; this is real new production code |
| Idempotency hit | return 200 + empty body | 409 on conflict; replay ONLY with real cached body | **Codex**: never fake replay bytes (truth-first) |
| Stream constraint | unspecified | require `stream=true`; else 400 | **Codex**: simplifies Phase C handler; non-stream path is Phase E |
| Pool-fail-after-Tx1 | unspecified | call `Settler.Abort()` | **Codex**: prevents zombie reserving rows |
| Time estimate | 2-3h | 3-5h | **Codex**: realistic given adapter work |

### Where Claude caught what Codex missed (added on top of Codex's base)

- **AppLocker workaround**: gateway binary must compile to a known-allowed path (proven path: `backend/billing.test.exe`-style direct `-o`); standard `go run` will be blocked.
- **Forwarder fixture reuse**: mock SSE generator should mirror the exact event shapes from `backend/internal/gateway/forwarder_test.go` to avoid parser-mismatch surprises.
- **Concrete PG state assertions** (5 checks): `billing_ledger_claims.status='committed'`, `usage_records` linked row, `billing_events.event_type='claim_committed'`, `provider_accounts.in_flight_count` decremented, `pool_slot_acquisitions.status='released_success'`.

---

## Final synthesized scope

### In scope

#### C.1 — Config + DI (~30-45 min)

- New file `backend/internal/config/config.go`:
  - `Config` struct read from env (no YAML parsing this phase)
  - Fields: `DatabaseURL`, `Listen`, `BillingPolicyVersion="1.0"`, `RequestClass="standard"`, smoke-auth fields (`SmokeBearerToken`, `SmokeTenantID`, `SmokeAPIKeyID`, `SmokeUserID`)
  - `Load()` returns `(*Config, error)`; missing required env returns typed error
- Wire `cmd/gateway/main.go run()`:
  1. `cfg, err := config.Load(ctx)`
  2. `pool, err := db.Open(ctx, db.PoolConfig{DSN: cfg.DatabaseURL})`
  3. `q := db.New(pool)`
  4. construct selector with adapters (see C.2 — a DB-backed `SlotManager` + DB-backed `pool.ClaimGate` adapter must be added)
  5. construct `gateway.StreamForwarder{}`, `billing.NewClaimGate(pool)`, `billing.NewSettler(pool)`
  6. `Deps` struct passed to `mountRoutes(router, deps, logger)`

#### C.2 — Selector adapters (REAL production code, not mocks) (~60-90 min)

Codex's discovery: production wiring requires two adapters that don't exist yet.

- New file `backend/internal/pool/db_slot_manager.go`:
  - `type DBSlotManager struct { q *db.Queries }`
  - Implements `pool.SlotManager` interface
  - `Acquire(ctx, account, req)` → INSERT into `pool_slot_acquisitions` + UPDATE `provider_accounts.in_flight_count + 1` in same Tx; returns `*AcquireResult{Token, Reason, ...}`
  - `Release(ctx, token, reason)` → idempotent CTE flip 'acquired'→'released_*' + decrement (matches `ReleaseSlotAndDecrementInFlight` shape from settler.go)
  - Phase B.5 already has `ReleaseSlotAndDecrementInFlight` query; adapter calls it
- New file `backend/internal/pool/db_claim_gate.go`:
  - `type DBClaimGate struct { q *db.Queries }`
  - Implements `pool.ClaimGate` interface (the seam for selector → claim writeback)
  - `WriteAcquisition(ctx, claimID, accountID, token)` → calls `q.WriteAcquisitionToken(...)`
  - On `rowCount=0` → returns `pool.ErrClaimRace`
- One unit test per adapter against real PG (vector inserts + reads).

#### C.3 — chat-completions handler (~60-90 min)

New file `backend/internal/gateway/chat_completions_handler.go`. Flow per spec §Tx1 → upstream → §Tx2:

1. **Smoke auth resolver** (new file `backend/internal/auth/smoke_resolver.go`):
   - Parse `Authorization: Bearer <token>` header
   - Compare `token == cfg.SmokeBearerToken`; on mismatch → 401
   - On match, attach tenant/api_key/user IDs from env to context
   - **Comment header**: `// Phase C smoke auth only; replace with api_keys/users-backed resolver after Owner approves 0007 schema in Phase E`
2. **Request validation**:
   - JSON parse `{model, messages, stream}`
   - Require `stream == true`; else 400 `{"error":"non-streaming responses are Phase E scope"}`
   - Compute `normalized_payload_hash` = SHA-256 over canonical JSON of (model + messages)
3. **ClaimGate.Reserve** with the 9 spec-mandated fingerprint fields
   - `tenant_id, api_key_id, logical_request_id, endpoint_family='chat', normalized_payload_hash, requested_model, pooling_group_id, billing_policy_version, request_class`
   - `IdempotencyKeyClientHeader` recorded but NOT in hash
   - On `ErrFingerprintConflict` → 409
   - On `IdempotencyHit && CachedPriorResponse != nil` → return cached bytes (Phase C v0.1 likely no cache hits since no replay path); on hit-but-no-cache → 409 with explicit "replay-without-cache" code
4. **Pool.Select** with `SelectionRequest{TenantID, UserID, APIKeyID, RequestedModel, EndpointFamily:"chat", ClaimID}`
   - On `ErrNoEligibleAccount` → call `Settler.Abort(tenantID, claimID, "pool_no_capacity")` then return 503 + Retry-After
   - On select success: token written back to claim row by selector's `ClaimGate.WriteAcquisition`
5. **Mocked upstream SSE generator** (new file `backend/internal/gateway/mock_upstream.go`):
   - Emit canonical OpenAI chat-completions SSE that the forwarder accepts
   - Include enough usage signal for forwarder to produce non-zero `tokens_input`/`tokens_output`
   - Verify the exact event shapes against `forwarder_test.go` fixtures before integrating
6. **Forwarder.Forward** with `ctx`, mock upstream `io.Reader`, `http.ResponseWriter`, `ForwardRequest`
   - On forwarder error before draft → call `Settler.Abort(tenantID, claimID, reason)`; if response headers already sent, log loudly + close stream
7. **Settler.Settle** synchronously with the resulting `UsageRecordDraft`
   - On error → log + return 500-class status if headers not yet sent
   - On success → stream end-of-stream sentinel; HTTP 200 already in flight

Error mapping (per Codex):
| Error | HTTP | Body |
|---|---|---|
| auth fail | 401 | `{"error":"unauthorized"}` |
| missing config env | 503 | `{"error":"gateway not configured"}` |
| `stream=false` | 400 | `{"error":"non-streaming Phase E"}` |
| `ErrFingerprintConflict` | 409 | `{"error":"idempotency conflict"}` |
| `ErrNoEligibleAccount` | 503 | `{"error":"no capacity"}`, header `Retry-After: 5` |
| upstream / forwarder fail | 502 | (only if headers not sent) |
| settler error | 500 | (logged) |

#### C.4 — Smoke test (~45-60 min)

New file `backend/cmd/gateway/smoke_test.go` with `//go:build smoke`:

- Setup:
  - openPool against dev PG
  - seed: `tenants` row + provider + pool_group + channel + provider_account (in_flight_count=2)
  - synthetic apiKeyID = tenantID*100 + 1 (matches integration_pg pattern)
- Boot binary:
  - compile to `backend/gateway-smoke.exe` (AppLocker-allowlisted path; matches today's Phase B.5 workaround)
  - subprocess listening on a free port (use `:0` then read assigned port from log line)
  - env: `HUAKAI_DATABASE_URL`, `HUAKAI_SMOKE_BEARER_TOKEN=phase-c-smoke`, `HUAKAI_SMOKE_TENANT_ID=<seeded>`, etc.
- Request:
  - POST `/v1/chat/completions` with `Authorization: Bearer phase-c-smoke`
  - Body: `{"model":"gpt-4.1-mini","messages":[{"role":"user","content":"hi"}],"stream":true}`
- Assertions:
  - HTTP 200, `Content-Type: text/event-stream`, ≥1 SSE event consumed
  - PG state (5 checks):
    1. `SELECT count(*) FROM billing_ledger_claims WHERE tenant_id=$1 AND status='committed'` = 1
    2. `SELECT count(*) FROM usage_records WHERE claim_id=$1` = 1
    3. `SELECT count(*) FROM billing_events WHERE claim_id=$1 AND event_type='claim_committed'` = 1
    4. `SELECT in_flight_count FROM provider_accounts WHERE id=$1` = 1 (decremented from 2)
    5. `SELECT count(*) FROM pool_slot_acquisitions WHERE acquisition_token=$1 AND status='released_success'` = 1
- Teardown: SIGTERM subprocess + DELETE in tenant cleanup order (per Codex's cleanup list).

### Out of scope (deferred, not deleted)

- Real Anthropic/OpenAI upstream call (Phase E, needs F-AUTH-005 wired)
- `0007_api_keys_users` migration + bcrypt + key-issuance flow (Phase E, security review)
- `/v1/responses`, `/v1/messages` handlers (Phase E)
- Rate limiting middleware F-RATE-001 (Phase E)
- Admin endpoints (Phase E.5+)
- LRU dedup cache for replay (Phase E.3)
- Outbox cross-threshold detection (callback hook stays as-is)
- Real config YAML parsing (env-only this phase)
- DLQ + orphan sweep (Phase 4.5)
- Non-streaming response path (Phase E)

---

## Success criteria

- [ ] `go build ./cmd/gateway` succeeds
- [ ] `go test -tags=integration_pg ./...` still 100% green (no regression on Phase B.5 work)
- [ ] New unit tests (DBSlotManager, DBClaimGate adapters) pass against real PG
- [ ] `go test -tags=smoke ./cmd/gateway/...` smoke test passes 5/5 PG state assertions + HTTP 200 + SSE event
- [ ] Per parallel-plan rule (CLAUDE.md #10) and per-commit rule (CLAUDE.md #8) — Codex review on each commit; HIGH addressed; MED documented in commit message

---

## Failure modes + mitigations

| Risk | Mitigation |
|---|---|
| `pool_slot_acquisitions` adapter SQL drifts from `ReleaseSlotAndDecrementInFlight` shape | Reuse the exact CTE structure from settler.go's release query |
| Mock upstream SSE divergence from forwarder parser | Re-use exact fixture pattern from `forwarder_test.go`; pre-test against `Forward()` in unit test before integration |
| AppLocker blocks compiled gateway binary | Compile to `backend/gateway-smoke.exe` (proven allowed path today) |
| Subprocess port race | Binary writes "listening on :PORT" to stdout; smoke test parses it before sending request |
| Idempotency hit returns no cache → handler picks wrong status | 409 with code "replay_without_cache" (not 200, not 500); document for Phase E LRU work |
| Selector returns slot but Tx2 fails | Cannot revert mid-stream if headers sent; log + return 500-class; smoke test asserts no orphan rows after teardown |
| Pool select fail abort racing with Tx1 row insert | Settler.Abort uses tenant-scoped FOR UPDATE (Phase B.5 P1 fix); race window doesn't escape tenant boundary |

---

## Rollback plan

- If Phase C lands but smoke test reveals incorrect billing: `git revert <SHA>`. cmd/gateway/main.go reverts to skeleton. Selector adapters (C.2) are new files; safe to revert without touching B.5.
- DB state from smoke test: cleanup query `DELETE FROM <table> WHERE tenant_id=$1` per the seeded tenant.
- If new selector adapter has a bug landed in production: revert just `db_slot_manager.go` / `db_claim_gate.go`; selector falls back to `nilSlotManager` (route returns no-capacity 503 instead of incorrect billing).

---

## Per-commit cross-review

Standard protocol per CLAUDE.md #8:
- C.1 (config + main.go DI) → `codex exec review --uncommitted --full-auto` → fix HIGH → commit
- C.2 (selector adapters) → review → commit
- C.3 (handler + smoke auth + mock upstream) → review → commit
- C.4 (smoke test) → review → commit

Expected: 4 commits.

---

## Decision points (recorded — no Owner re-confirm needed unless surprise)

1. **Auth model**: Option B (smoke env resolver) — adopted per Codex.
2. **Mock SSE format**: OpenAI chat-completions style (matches endpoint name + forwarder's primary adapter). If forwarder rejects, fall back to Anthropic — surface only if both fail.
3. **Cost computation**: forwarder produces `actual_cost` from token counts via `CostEstimator{}` default (zero acceptable; settler accepts zero per Phase B.5).
4. **Stream-only**: required this phase. Non-stream path = 400.
5. **Pool-fail-after-Tx1**: always `Settler.Abort()` before returning to client.

---

## Concrete execution order

1. ✅ Plan written ← **THIS FILE**
2. Run codex per-commit review on this synthesized plan? **No** — already cross-validated via parallel-draft. Proceed.
3. **C.1**: config.go + main.go DI scaffold; compile-only check; codex review; commit
4. **C.2**: db_slot_manager.go + db_claim_gate.go + 2 unit tests vs PG; codex review; commit
5. **C.3**: smoke_resolver.go + chat_completions_handler.go + mock_upstream.go + handler unit test (mocked deps); codex review; commit
6. **C.4**: smoke_test.go vs real PG; codex review; commit
7. Surface to Owner: 4 commits, smoke green, here's PG row proof; Owner decides Phase D start.

---

## Assumptions logged

- Phase B.4/B.5 verified green (commit `fb340d2`). If anything regresses, stop.
- Migrations 0001..0006 applied to dev PG. If not, B.1 ran already so this is given.
- AppLocker workaround for compiled test binaries continues to work.
- Codex per-commit review tool (`codex exec review`) continues to function.
- Owner won't change auth approach mid-Phase-C; if Owner says "actually do 0007 schema now" → stop, write 0007 plan, surface.
