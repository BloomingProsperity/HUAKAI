# 2026-04-30 Phase C Gateway Wiring - Codex Independent Plan

| Field | Value |
|---|---|
| Owner directive | "Owner has authorized Phase C of the integration sprint: wire HTTP entry through real packages so POST /v1/chat/completions actually reserves a claim, calls upstream (mocked), forwards a stream, and settles to PostgreSQL with a real billing_ledger_claims row at status='committed'." |
| Independence note | I did not read `docs/plans/2026-04-30-phase-c-gateway-wiring-claude.md`; this is a parallel draft. |

## Scope

In:
- Replace only `POST /v1/chat/completions` 501 route in `backend/cmd/gateway/main.go` with a real HTTP path; other routes stay 501.
- Open PostgreSQL via `backend/internal/db.Open` using `HUAKAI_DATABASE_URL`, matching the existing DB factory contract (`backend/internal/db/pgconn.go:21`, `backend/internal/db/pgconn.go:34`).
- Run real Tx1 with `billing.NewClaimGate(pool).Reserve(...)`; spec requires Tx1 before upstream (`docs/specs/observability-billing.md:47`, `docs/specs/observability-billing.md:54`, `docs/specs/observability-billing.md:56`).
- Run pool selection/acquisition and write acquisition token back to the claim before upstream; spec places pool slot acquisition after Tx1 (`docs/specs/observability-billing.md:57`), and selector already writes acquisition tokens through its claim seam (`backend/internal/pool/selector.go:203`, `backend/internal/pool/selector.go:213`).
- Use mocked upstream SSE, then `gateway.StreamForwarder.Forward(...)` to forward chunks and produce a `UsageRecordDraft`; the forwarder is the existing F-GW-002 pipeline (`backend/internal/gateway/forwarder.go:29`, `backend/internal/gateway/forwarder.go:31`, `docs/specs/streaming-forwarder.md:54`, `docs/specs/streaming-forwarder.md:62`).
- Run real Tx2 with `billing.NewSettler(pool).Settle(...)`; spec requires usage record, billing event, in-flight decrement, and claim status flip in one Tx2 (`docs/specs/observability-billing.md:63`, `docs/specs/observability-billing.md:74`, `docs/specs/observability-billing.md:83`, `docs/specs/observability-billing.md:84`, `docs/specs/observability-billing.md:85`, `docs/specs/observability-billing.md:86`).
- Add an integration/smoke test that starts the handler with real PostgreSQL, seeded tenant/pool/account rows, posts a streaming chat request, reads SSE, then asserts one `billing_ledger_claims.status='committed'`, one `usage_records` row, and one `billing_events.event_type='claim_committed'`.

Out:
- No real upstream network call; use deterministic in-process upstream SSE.
- No broad OpenAI compatibility surface beyond the minimal request fields needed for billing and model selection.
- No production inbound API-key/user schema unless Owner explicitly approves a 0007 migration.
- No Redis, no admin routes, no orphan sweep worker, no retry/backoff matrix, no non-streaming response cache replay.
- No changes to `LICENSE`, secrets, auth core, billing ledger semantics, or deployment scripts.

## Owner-Flagged Auth Blocker Decision

Recommendation: **Option B, env-injected fake auth for smoke test**, implemented as an explicit `SmokeAuthResolver`, not as silent production auth.

Why:
- Current migrations have `tenants` (`backend/sql/migrations/0001_pool_routing.up.sql:15`) and scalar `api_key_id` / `user_id` columns on billing rows (`backend/sql/migrations/0002_observability_billing.up.sql:29`, `backend/sql/migrations/0002_observability_billing.up.sql:30`, `backend/sql/migrations/0002_observability_billing.up.sql:125`, `backend/sql/migrations/0002_observability_billing.up.sql:126`), but no first-class `api_keys` or `users` table in 0001..0006.
- Existing billing tests already use synthetic `apiKeyID` and `userID` after seeding a tenant (`backend/internal/billing/claim_gate_integration_test.go:38`, `backend/internal/billing/claim_gate_integration_test.go:51`, `backend/internal/billing/claim_gate_integration_test.go:52`).
- Adding `0007_api_keys_users` is a schema change, and AGENTS marks database schema as high-risk. It should be a separate Owner-approved slice with auth/security review, not hidden inside Phase C.

Concrete shape:
- Add a small gateway-local `AuthResolver` interface, plus `SmokeAuthResolver`.
- Env vars: `HUAKAI_SMOKE_BEARER_TOKEN`, `HUAKAI_SMOKE_TENANT_ID`, `HUAKAI_SMOKE_API_KEY_ID`, `HUAKAI_SMOKE_USER_ID`.
- If any smoke auth env var is missing, `/v1/chat/completions` returns `503` configuration error, not `200`.
- If `Authorization: Bearer <token>` does not match `HUAKAI_SMOKE_BEARER_TOKEN`, return `401`.
- Mark code comments and config example as `Phase C smoke auth only; replace with api_keys/users-backed resolver after Owner approves schema`.

## Success Criteria

- `POST /v1/chat/completions` no longer returns 501 when DB and smoke auth env are configured.
- Request path order is: auth resolve -> parse request -> Tx1 reserve -> pool select/acquire/writeback -> mocked upstream stream -> forwarder -> Tx2 settle.
- The response is `text/event-stream`, flushes at least one streamed chunk, and ends cleanly.
- PostgreSQL assertions after the smoke request:
  - exactly one claim for the logical request, `status='committed'`;
  - the claim has non-null `provider_account_id` and `acquisition_token`;
  - exactly one `usage_records` row linked to the claim;
  - exactly one `billing_events` row linked to the claim with `event_type='claim_committed'`.
- No bounded worker queue sits between upstream success and Tx2; F-OBS-001 forbids that because it can drop financial settlement (`docs/specs/observability-billing.md:45`).
- Tx2 receives the forwarder's draft including end class and usage source; F-GW-002 requires handoff to Tx2 (`docs/specs/streaming-forwarder.md:95`, `docs/specs/streaming-forwarder.md:97`, `docs/specs/streaming-forwarder.md:100`, `docs/specs/streaming-forwarder.md:102`).
- Tests fail closed if PostgreSQL is unavailable; existing DB factory returns errors when DSN is missing or ping fails (`backend/internal/db/pgconn.go:39`, `backend/internal/db/pgconn.go:72`).

## Time Estimate

- Wall clock: 3-5 hours for implementation + tests if Phase B.4/B.5 are green locally.
- Agent time:
  - 30 min: inspect exact constructors and missing seams.
  - 60-90 min: route composition and smoke auth.
  - 60-90 min: DB-backed selector/slot seam or minimal adapter wiring.
  - 45-60 min: end-to-end integration test and assertions.
  - 30 min: review fixes and docs updates.

## Blast Radius

- Runtime startup can change from "always starts with 501 routes" to "fails closed or exposes one live route depending on DB/env wiring".
- Billing tables: `billing_ledger_claims`, `usage_records`, `billing_events`, `pool_slot_acquisitions`, and `provider_accounts` are touched by tests and the new route.
- Pool selection path may expose missing production adapters because `DefaultSelector` requires account source, slot manager, and claim writeback (`backend/internal/pool/selector.go:57`, `backend/internal/pool/selector.go:62`, `backend/internal/pool/selector.go:63`).
- Streaming path can surface adapter issues in `gateway.StreamForwarder` if mocked SSE does not match current protocol adapters.
- Config sample may gain smoke auth env documentation, but no real secrets.

## Failure Modes + Mitigations

- Missing `api_keys/users`: use explicit smoke auth resolver only; record mandatory follow-up for schema-backed auth.
- DB unavailable or migrations absent: startup/handler fails closed with typed 503; do not silently run in memory.
- Tx1 claim succeeds but pool select fails: call `Settler.Abort(...)` where possible so the claim does not remain `reserving`; if abort fails, surface 503 and log claim id.
- Pool slot acquisition exists but Tx2 fails: return 502/503 after stream failure only if headers not yet committed; otherwise log settlement failure loudly and let test fail. Do not move Tx2 to async queue.
- Client disconnect during stream: do not fully solve in Phase C; ensure forwarder result is still passed to Tx2 when `Forward` returns a draft. Full drain behavior remains Phase D per sprint plan.
- Mocked SSE usage is ambiguous: choose canned upstream events that produce nonzero usage, or manually map draft to `UsageSourceReported` only if the existing adapter supports it. Do not assert `res.X != bad`; assert exact expected `end_class` and `usage_source`.
- Idempotency conflict: map `billing.ErrFingerprintConflict` to `409`; map `billing.ErrClaimRace` to bounded retry or `409/503` with a test note if retry is deferred.
- Settlement token mismatch: treat as 500-level invariant break; `DefaultSettler` already rejects acquisition token mismatch (`backend/internal/billing/settler.go:49`, `backend/internal/billing/settler.go:52`, `backend/internal/billing/settler.go:54`).

## Decision Points Needing Owner Sign-off

- Whether to approve a future `0007_api_keys_users` migration for real inbound API key auth. My Phase C recommendation is no migration now.
- Whether smoke auth env names above are acceptable or should be replaced by config-file fields.
- Whether Phase C may add small gateway-local adapters around existing packages, or must place them in new `backend/internal/gatewayhttp` package.
- Whether failed pool selection after Tx1 should immediately abort the claim in Phase C or be left for Phase D failure-path hardening. My recommendation: abort now, because a reserving row after known no-upstream path creates false operational state.
- Whether route startup should fail the whole binary when `HUAKAI_DATABASE_URL` is missing, or allow admin/health routes to remain up. My recommendation for Phase C: live gateway route requires DB and returns 503 if route deps are not configured.

## Pre-execution Checklist

1. Confirm Phase B.4/B.5 tests pass against real PostgreSQL:
   - `go test ./backend/internal/billing -run 'TestAT_OBS_004|TestClaimGate'`
2. Confirm migrations 0001..0006 are applied and `schema_migrations` is clean.
3. Confirm no first-class `api_keys` or `users` table exists in current schema; do not design production auth in this slice.
4. Identify current protocol adapter needed by `StreamForwarder` for a canned OpenAI-style chat SSE stream.
5. Verify pool test helper behavior is not reused in production code; any adapter added for Phase C must be real DB-backed or explicitly smoke-only.
6. Draft route-level error mapping before implementation: 401 auth, 400 bad request, 409 idempotency conflict, 429/503 no capacity/config, 500 invariant break.
7. Plan docs/tests updates before code: smoke env docs, smoke test fixture, exact DB assertions.

## Concrete Execution Order

1. Add a small route dependency struct near gateway entry:
   - `ClaimGate billing.ClaimGate`
   - `Settler billing.Settler`
   - `Selector pool.Selector`
   - `Forwarder *gateway.StreamForwarder`
   - `AuthResolver`
   - `UpstreamFactory` returning an `io.Reader` for mocked SSE.
2. Change `mountRoutes` to accept dependencies and wire only `/v1/chat/completions`; leave all other routes as `notImplemented`.
3. In `run`, read `HUAKAI_DATABASE_URL`, open `db.Open`, create `billing.NewClaimGate`, `billing.NewSettler`, `db.New(pool)`, and route dependencies.
4. Add smoke auth resolver:
   - parse bearer token;
   - compare with `HUAKAI_SMOKE_BEARER_TOKEN`;
   - return tenant/api-key/user ids from env.
5. Add request parsing:
   - accept `model`, `messages`, `stream`;
   - require `stream=true` for Phase C or return 400 with clear error;
   - compute normalized payload hash from canonical JSON bytes;
   - logical request id from `Idempotency-Key` header if present, else request id.
6. Call `ClaimGate.Reserve` with:
   - endpoint family `chat`;
   - request class `stream`;
   - billing policy version from env/config default `1.0`;
   - predicted cost deterministic small decimal, e.g. `0.01000000`.
7. If Tx1 returns idempotency hit, return `409` or cached replay only if actual cached response exists. Do not fake replay bytes.
8. Select pool account with `pool.SelectionRequest{TenantID, UserID, APIKeyID, RequestedModel, EndpointFamily:"chat", ClaimID}`.
9. Ensure selector uses a DB-backed slot manager and claim writeback:
   - if current repo lacks production `SlotManager`, implement the smallest DB-backed adapter that inserts `pool_slot_acquisitions` and increments `provider_accounts.in_flight_count`;
   - implement claim writeback adapter using `db.WriteAcquisitionToken`.
10. Build mocked upstream SSE after selection. It should include enough usage signal for deterministic `UsageRecordDraft`.
11. Call `Forwarder.Forward` with request context for downstream cancellation, then immediately call `Settler.Settle` synchronously. Do not enqueue settlement.
12. On upstream/forwarding terminal failures before a valid draft, call `Settler.Abort` with reason; map client response carefully if headers are already committed.
13. Add integration test under gateway command/internal package:
   - seed tenant, pool group, channel, provider account, and any required slot rows;
   - configure smoke auth env;
   - use `httptest.Server`;
   - POST `/v1/chat/completions`;
   - read SSE body;
   - assert DB exact rows and values.
14. Update `backend/config.example.yaml` or a short docs note with smoke auth envs, clearly marked non-production.
15. Run focused tests, then `go test ./backend/internal/billing ./backend/internal/gateway ./backend/internal/pool ./backend/cmd/gateway`.
16. Stage changes and run required `codex exec review --uncommitted --full-auto` before any commit, per AGENTS per-commit discipline.

## Rollback Plan

- Revert the `/v1/chat/completions` route to the existing 501 handler if live route wiring destabilizes startup.
- Remove only the new gateway route dependency files/tests/docs; leave Phase B billing and existing pool/forwarder packages untouched.
- If a smoke test leaves DB rows, clean by tenant id in test cleanup order: `usage_records`, `billing_events`, `pool_slot_acquisitions`, `billing_ledger_claims`, `provider_accounts`, `channels`, `pool_groups`, `providers`, `tenants`.
- If a migration is accidentally proposed during execution, stop before applying it; schema work requires Owner confirmation.
- If Tx2 settlement is flaky, do not bypass it to make HTTP green. Restore 501 or keep route disabled behind env until Tx2 is reliable.

## Assumptions and Risks

- Assumption: Phase B.4/B.5 have landed as described; `DefaultClaimGate` and `DefaultSettler` are the intended real packages.
- Assumption: mocked upstream is acceptable for Phase C, but DB settlement must be real.
- Risk: current `billing.go` comments still mention TODO skeleton (`backend/internal/billing/billing.go:92`), so implementer should trust concrete `claim_gate.go` and `settler.go` behavior over stale comment text.
- Risk: API contract says every gateway request produces a Usage Record (`docs/specs/api-contract.md:98`); failed auth/config requests should be documented as pre-gateway admission failures, not billed gateway requests.
- Clean-room risk: none expected; this plan uses only local specs and local code paths, no external reference implementation source.
- Security risk: smoke bearer auth is not production-grade. It must fail closed, be visibly named smoke-only, and be replaced by schema-backed auth before release status.
