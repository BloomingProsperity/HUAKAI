# 2026-04-30 Phase C — Gateway DI wiring + first real /v1/chat/completions

| Field | Value |
| --- | --- |
| Driver | Per integration sprint plan §Phase C — wire chi → real packages, prove binary bills correctly via PG |
| Predecessor | Phase B.5 (commit fb340d2) — Tx2 Settler + Tx1 ClaimGate now both run vs real PG |
| Estimate | 2-3h |
| Blast radius | LOCAL — adds new files (config loader, auth middleware, chat-completions handler, mock-upstream emitter, smoke test); modifies cmd/gateway/main.go (wires all DI). No schema changes. No CI to break. |

---

## Why this phase

The integration sprint plan §Phase C says:

> After Phase B proves money-path correctness, then HTTP. Single endpoint first.

Until now the binary boots and returns 501 for everything. Phase C makes one endpoint actually work: `POST /v1/chat/completions` reads → reserves claim → selects pool → calls upstream (mocked) → forwards stream → settles. The smoke test asserts both HTTP correctness AND PostgreSQL row state.

Owner directive: "fusion 体跑起来" — only counts when a real PG row exists with status='committed' after a real HTTP request returns 200.

---

## Scope (in)

### C.1 — Config + DI scaffolding (~30 min)

- Add `backend/internal/config/config.go` — Config struct + loader from YAML/env. Fields:
  - `DatabaseURL`     (env: `HUAKAI_DATABASE_URL`)
  - `Listen`          (env: `HUAKAI_ADDR`, default `:8080`)
  - `MockUpstream`    (env: `HUAKAI_MOCK_UPSTREAM`, default `true` for Phase C)
  - `BillingPolicyVersion` (default `"1.0"`)
  - `RequestClass`    (default `"standard"`)
- Wire `cmd/gateway/main.go run()` to:
  1. load config
  2. open pgxpool via `db.Open(ctx, cfg.DatabaseURL)`
  3. construct `db.New(pool)`, `pool.NewDBRepository(q)`, `pool.NewDefaultSelector(repo)`, `gateway.StreamForwarder{}`, `billing.NewClaimGate(pool)`, `billing.NewSettler(pool)`
  4. inject into chi handlers via a `Deps` struct
- Keep skeleton routes for non-Phase-C endpoints; just thread Deps through.

### C.2 — POST /v1/chat/completions real handler (~60 min)

New file: `backend/internal/gateway/chat_completions_handler.go`.

Flow per spec §Tx1 → upstream → §Tx2:

1. **Auth middleware** (`backend/internal/auth/api_key_middleware.go`): parse `Authorization: Bearer <api_key>` header → SELECT api_keys + tenant_id + user_id → attach to context. Reject 401 on miss/expired.
2. **Compute idempotency**: SHA-256 over the 9 spec-mandated fields (tenant_id, api_key_id, logical_request_id, endpoint_family='chat', normalized_payload_hash, requested_model, pooling_group_id, billing_policy_version, request_class). NOT the client header.
3. **ClaimGate.Reserve** → ClaimID, fingerprint conflict → 409, idempotency hit → return cached prior response. (For Phase C, cache hit just returns 200 + empty body — full streaming-replay is Phase E.)
4. **Pool.Select** with the claim — get acquisition_token + provider_account.
5. **Mock upstream** (when `cfg.MockUpstream`): in-process generator emits 3-frame canned anthropic SSE (`message_start`, `content_block_delta`, `message_stop` w/ usage). Real upstream hookup deferred to Phase E.
6. **Forwarder.Forward** → UsageRecordDraft.
7. **Settler.Settle** with ClaimID + token + draft → claim committed.
8. Return 200 with the streamed body that already went out via `clientWriter`.

Failure rules:
- Upstream non-200 → call `Settler.Abort(tenantID, claimID, reason)`, return 502.
- Pool.Select returns no slot → return 503 + Retry-After.
- Idempotency hit → bypass Pool/Forward/Settle (claim already settled).

### C.3 — Smoke test (~45 min)

New file: `backend/cmd/gateway/smoke_test.go` with `//go:build smoke`:

- Setup: openPool against dev PG; seed tenant + api_key + provider + pool_group + channel + provider_account (matching the test fixture in settler_integration_test.go).
- Boot binary via `cmd.Start()` (compile + run subprocess) listening on a random free port. Pipe `HUAKAI_DATABASE_URL` + `HUAKAI_MOCK_UPSTREAM=true` + `HUAKAI_ADDR=:0` (or pick a free port).
- POST `/v1/chat/completions` with seeded API key + JSON body `{"model":"claude-3-haiku","messages":[{"role":"user","content":"hi"}]}`.
- Assert HTTP 200 + body contains canonical mock SSE chunks (`event: message_start`, etc.).
- Assert PostgreSQL state:
  - 1 `billing_ledger_claims` row with `status='committed'`, `actual_cost=$expected`
  - 1 `usage_records` row with non-empty `tokens_input`, `tokens_output`
  - 1 `billing_events` row with `event_type='claim_committed'`
  - `provider_accounts.in_flight_count` decremented (whatever seed value − 1)
  - 1 `pool_slot_acquisitions` row with `status='released_success'`
- Teardown: kill subprocess + delete seeded tenant data.

---

## Out of scope (for this phase, not deleted)

- Real Anthropic upstream (Phase E — needs OAuth credential management wired in)
- `/v1/responses` and `/v1/messages` handlers (Phase E)
- Rate limiting middleware (F-RATE-001 — Phase E)
- Admin endpoints (Phase E.5+)
- LRU dedup cache for replay (defer per integration plan §Phase E.3)
- Outbox cross-threshold detection (already-stub callback hook stays as-is)
- Real config YAML parsing (env-only is sufficient for Phase C; YAML stub)
- DLQ + orphan sweep (Phase 4.5)

---

## Success criteria

- [ ] `go build ./cmd/gateway` succeeds
- [ ] `go test -tags=integration_pg ./...` still 100% green (regression)
- [ ] `go test -tags=smoke ./cmd/gateway/...` smoke test passes 5/5 PG state assertions
- [ ] After smoke test: `SELECT count(*) FROM billing_ledger_claims WHERE status='committed'` returns ≥ 1
- [ ] Codex per-commit cross-review on Phase C uncommitted state — all HIGH addressed before commit

## What could go wrong

| Risk | Mitigation |
|---|---|
| Mock upstream SSE format diverges from forwarder's parser → forwarder rejects | Use the same fixture pattern as forwarder_test.go; pre-test the mock against forwarder unit-test |
| Subprocess port race in smoke test | Use `:0` listen + read assigned port from process stdout, OR static high port + `-test.parallel=1` |
| AppLocker blocks compiled gateway binary in smoke test | Compile with explicit `-o backend/gateway-smoke.exe` (we proved this path works for billing.test.exe earlier today) |
| Auth middleware leak: missing api_keys schema fields | Re-check sql/migrations against the SELECT before writing handler — fail-fast on schema mismatch |
| Forwarder needs CostEstimator/UsageAccumulator state — minimal usage may produce zero cost | Phase B.5 settler accepts zero-cost; smoke test asserts existence not magnitude |
| Pool selection returns no provider account on fresh DB | Smoke setup seeds 1 healthy provider_account with `account_type='api_key'` matching mock upstream |

---

## Decision points (will surface to Owner before crossing)

1. **Mock upstream framing**: emit Anthropic Messages SSE format vs OpenAI Chat-Completions SSE. Default: Anthropic (matches `gateway.AmbiguousUsage` testing baseline). If Owner wants OpenAI first, surface and switch.
2. **Cost computation**: forwarder emits `UsageRecordDraft` with token counts; settler currently writes `actual_cost=req.ActualCost OR draft.ActualCost`. For mock upstream, hard-code `actual_cost=0.00001` per request to avoid pricing-table dependency. Surface if Owner wants a real cost-table lookup now (would expand scope to F-BILL-001 pricing module).
3. **Auth verification model**: Phase C uses raw `api_keys.token = <bearer>` SELECT (plaintext compare). If Owner wants hashed-key storage now, surface (would expand scope to bcrypt + key-issuance flow).

I will plan-pause and ask Owner before changing any of these defaults.

---

## Per-commit cross-review

Same protocol as Phase B.5: after each meaningful chunk (config+DI, handler+middleware, smoke test), run `codex exec review --uncommitted --full-auto`. Address all HIGH; document MED in commit message. Phase C is expected to be 1-2 commits depending on review feedback.

---

## Rollback plan

- If Phase C lands but smoke test reveals incorrect billing: revert Phase C commits via `git revert <SHA>`. cmd/gateway/main.go reverts to skeleton 501-everything.
- The settler/claim-gate from Phase B.5 are NOT touched here, so they survive any C revert.
- Database state from smoke test is in seeded test tenant rows; cleanup query in test teardown removes them.

---

## Execution order (concrete)

1. Write this plan ← **DONE**
2. C.1 config + main.go DI scaffold → compile-only check → commit
3. C.2 auth middleware + chat-completions handler + mock upstream → unit test handler against mocks → commit
4. C.3 smoke test → run vs dev PG → commit
5. Codex per-commit review on each — fix HIGH, document MED
6. Surface to Owner: "Phase C done — smoke test green, X commits, here's PG state proof." Owner decides Phase D start or pause.
