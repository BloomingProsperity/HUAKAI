# 2026-04-29 Integration Sprint Plan — make HUAKAI run end-to-end

| Field | Value |
| --- | --- |
| Owner directive | "我只知道现有借鉴项目以及是一个能跑起来的东西。你也要让我这个融合体跑起来。这是我的目的。所以你掌控全局，和codex协作。定计划执行。" |
| Trigger | Self-audit + Codex cross-validation: 4 slices of feature scaffolding, 0 lines wired, money-path can fake success |
| Bias | money-path correctness > HTTP completeness > feature breadth |
| Lane mode | Claude PM + Codex implementer + Codex reviewer (READ-ONLY for verification) |
| Driver | Don't ship until a real PostgreSQL row proves the binary works |

---

## Why this plan exists

Codex's cross-validation rejected my first plan ("wire HTTP first"). Quote: **"A running gateway that bypasses durable settlement would create false confidence."** A green `200 OK` from a chi handler that skips Tx2 is worse than a 501 — it teaches the operator to trust something that loses money silently.

So the sprint starts at the **money invariant**, not the HTTP entry. Order matters.

---

## Constraints honestly stated

- Docker is available locally (v29.1.3 verified). Real PostgreSQL via container.
- No CI exists. All verification is local until Phase F.
- AppLocker on Windows blocks Go test temp dirs unless `GOTMPDIR` is set. Already worked around in earlier sessions.
- Slice 5 (billing/obs Codex output) was quarantined to `c:/tmp/slice5-codex-orig/` because it contained 5 HIGH defects (Settler returning success on nil stores, ClaimGate not implementing promised lock order, IdempotencyKeyClientHeader ignored, no sqlc queries, etc.). Will be rewritten against real DB in Phase D, not resurrected.

---

## Phase A — Stabilize (DONE before this plan was committed)

- [x] Save Codex validation report → `docs/reviews/2026-04-29-codex-validation-of-self-audit.md`
- [x] Errata appended to self-audit (LOC, test count, TODO count, go.mod)
- [x] Quarantine slice 5 broken code (preserved in `c:/tmp/slice5-codex-orig/`, removed from repo)
- [x] Verify Docker available

---

## Phase B — Money-path foundation (next, ~2-3h)

Goal: a real PostgreSQL container running migrations + ONE Tx1/Tx2 happy-path integration test that proves a billing claim row + usage record row land atomically.

### B.1 — Docker Compose for dev PostgreSQL
- Add `backend/docker-compose.dev.yml` with one `postgres:16-alpine` service on port 5432.
- Add `backend/Makefile` targets: `db-up`, `db-down`, `db-migrate`, `db-reset`.
- Add `backend/scripts/run-migrations.sh` that applies `sql/migrations/*.up.sql` in order via `psql` inside the container (Owner doesn't need psql on host).

### B.2 — pgxpool wiring
- Add `backend/internal/db/pgconn.go` with `func Open(ctx, dsn) (*pgxpool.Pool, error)` + health probe.
- Add config field for DSN (env: `HUAKAI_DATABASE_URL`).
- One smoke test: `go test ./internal/db -run TestPgConnect` opens pool, runs `SELECT 1`.

### B.3 — Migrations table + actual application
- Use `golang-migrate/migrate/v4` (already in indirect dep tree? — verify; if not, add as direct dep).
- Apply all 6 migrations to the dev container.
- Verify `\dt` shows ~10 tables (provider_accounts, billing_ledger_claims, usage_records, etc.).

### B.4 — REAL Tx1 ClaimGate (no fake success)
Discard the broken slice 5 ClaimGate. Rewrite from scratch with these invariants:
- `Reserve` MUST open a transaction via the injected `*pgxpool.Pool`. If pool is nil, return `ErrNotConfigured` — never silently succeed.
- Idempotency key SHA-256 over the 9 fields per spec, including `IdempotencyKeyClientHeader` (the field that Codex flagged Claude was ignoring).
- 6-row lock acquisition in alphabetical entity order via `SELECT ... FOR UPDATE`.
- Insert claim row with `status='reserving'`, return `ClaimID`.
- On fingerprint conflict (different normalized hash, same logical request id) → return `ErrFingerprintConflict`.

### B.5 — REAL Tx2 Settler (atomic 5-effect)
Same: discard broken version. Real version:
- `Settle` opens a transaction. Verifies claim status is `reserving`. Rolls back if not.
- Writes Usage Record + BillingEvent in same `BEGIN/COMMIT`.
- Decrements `provider_accounts.in_flight_count` with `WHERE acquisition_token=$1 AND in_flight_count>0`.
- Updates claim row to `committed`.
- All-or-nothing: any failure rolls everything back.

### B.6 — Integration test (the gating gate)
`backend/internal/billing/integration_test.go` with `//go:build integration_pg`:
- Setup: connect to dev PG, apply migrations to a randomly-named test database.
- AT-OBS-001 strong: same fingerprint twice → second call returns cached prior response, no second usage record row.
- AT-OBS-002 strong: same logical_request_id, different normalized_payload_hash → 409 + zero quota change.
- AT-OBS-004 strong: kill the test mid-Tx2 (force a rollback) → no partial Usage Record row visible.
- AT-OBS-014 strong: 1_000_000 × 0.0000001 → exact 0.10 round-tripped through `numeric(20,8)`.
- Teardown: drop test database.

**Phase B exit gate**: all 4 above tests pass against real PostgreSQL. If they don't, Phase C does not start. **No exception**.

---

## Phase C — Wire one chi route through real packages (~2h)

After Phase B proves money-path correctness, then HTTP. Single endpoint first.

### C.1 — main.go DI
- Read `config.example.yaml` + env into a Config struct.
- Open pgxpool.
- Construct: `db.New(pool)` → `auth.NewAntigravityTokenProvider(...)` → `pool.NewDefaultSelector(...)` with `pool.AuthCredentialGate{Provider: tokenProvider}` → `gateway.StreamForwarder{...}` → `billing.NewClaimGate(pool)` → `billing.NewSettler(pool)`.
- Pass these into chi handlers.

### C.2 — POST /v1/chat/completions handler (real)
- Resolve tenant from `Authorization: Bearer <api_key>` middleware (lookup api_keys table).
- Call `billing.ClaimGate.Reserve(...)` with computed fingerprint.
- Call `pool.Selector.Select(...)` with claim's reservation.
- For Phase C, mock the upstream call (return canned anthropic SSE).
- Pipe through `gateway.StreamForwarder.Forward(...)`.
- Call `billing.Settler.Settle(...)` with the resulting `UsageRecordDraft`.

### C.3 — End-to-end smoke test
`backend/cmd/gateway/smoke_test.go` with `//go:build smoke`:
- Boot binary in subprocess pointing at dev PG.
- POST `/v1/chat/completions` with a fixture payload + valid API key (seeded by test).
- Assert 200 + body contains streamed canonical chunks.
- Assert PostgreSQL has: 1 claim row `committed`, 1 usage_record row, 1 billing_event row, all in same tenant.
- Assert `provider_accounts.in_flight_count = 0` (decremented after settle).

**Phase C exit gate**: smoke test passes. The binary actually runs and bills correctly.

---

## Phase D — Stitch failure paths (~1-2h)

Now that the happy path works, prove the failure paths don't lose money.

- D.1 — Upstream returns 401: claim `aborted`, no usage_record row, no in_flight decrement (because increment never happened or was already rolled back).
- D.2 — Client disconnect mid-stream: forwarder's drain runs, claim still settles with `usage_source=partial`.
- D.3 — Replay attack (different fingerprint): returns 409 + audit event row written.
- D.4 — Kill the gateway process between Tx1 commit and Tx2 commit: orphan-sweep query catches it via `lease_expires_at < NOW()`. (Worker not yet implemented; just verify the query returns the expected row so the worker has something to consume in Phase E.)

**Phase D exit gate**: failure paths verified. No row in any table represents money charged when the response says no charge.

---

## Phase E — Rebuild slice 5 + slice 6 (~1-2h)

Now that the foundation is real, finish what was scaffolded:
- E.1 — Promote ClaimGate / Settler into permanent files; copy needed test patterns from quarantined `slice5-codex-orig/` only as inspiration, not as code.
- E.2 — Outbox table + cross-threshold detection (was Codex's HIGH C-001).
- E.3 — LRU dedup cache (was Codex's claim, but only useful when at least one in-process request needs it; defer if Phase B-D worked without it).
- E.4 — F-RATE-001 (slice 6) skeleton + a real per-tenant rate limit test against PG.
- E.5 — Re-run cross-review against the integrated system (not slice-by-slice).

**Phase E exit gate**: full E2E smoke + rate limit smoke + all 6 spec features have at least one strongly-asserted AT against real PG.

---

## Phase F — Honest production-readiness gate (~30min, then Owner decides)

Single document `docs/15_RELEASE_GATES.md` updated with:
- What works end-to-end (with evidence).
- What's still mocked (upstream Anthropic API in tests).
- Operational TODOs Owner must do before real deployment (DNS, TLS, secrets management, monitoring).

Then Owner decides: ship to a private VPS for personal use, or push for SaaS readiness (which is months not days).

---

## Risk register for this sprint

| Risk | Mitigation |
|---|---|
| Docker disk space full mid-migration | Run `docker system prune` before B.1; monitor |
| `golang-migrate` adds large dep tree | Vendor it via `go mod tidy`; ~5MB; acceptable |
| Real PG behavior differs from in-memory CAS (e.g. serialization failures retry) | Phase B.4 explicitly tests retry path on `40001` SQLSTATE |
| Slice 5 quarantine confuses future reader | This plan + the quarantine note in the audit are the durable record |
| AppLocker blocks pg test temp files | `GOTMPDIR` already set; verify before each phase |
| Codex hallucinates a "fix" that re-introduces fake success | Every Phase B-D PR must run the integration tests; tests fail closed |

---

## What this plan does NOT do

- It does not "finish" Phase 4 v0.1. Phase 4 v0.1 was a planning unit. The real unit going forward is "is the binary wired and correct."
- It does not promise SaaS-grade readiness. Goal is "Owner can run it and bill themselves $0.001 of Anthropic credit by the end."
- It does not add new features (no F-RATE-001 deepening, no admin UI, no quota dashboards).
- It does not replace the cross-review protocol — every Phase B-E completion still gets a Codex audit before being declared done.

---

## Execution order (concrete)

1. Now: commit Phase A (this file + audit errata + quarantine note).
2. Phase B.1 → B.6 sequentially. Each step's output committed individually.
3. Codex review of Phase B before starting Phase C.
4. Phase C → smoke test green → commit.
5. Phase D failure paths.
6. Phase E rebuild slice 5 + slice 6 + cross-review.
7. Phase F — Owner decides next horizon.

If at any point a phase exit gate fails, the next phase does NOT start. We surface to Owner.

---

## My contract to Owner

I will not commit code to `internal/billing/` or `cmd/gateway/main.go` that returns success without going through PostgreSQL. If a function cannot reach PG (because the pool isn't configured, because tests didn't seed data, because the migration didn't apply), it returns a typed error, not a 200 OK.

This is the only way "fusion 体跑起来" means a real thing.
