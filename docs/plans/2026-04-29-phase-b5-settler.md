# Phase B.5 — Real Tx2 Settler against PostgreSQL

| Field | Value |
| --- | --- |
| Owner directive | "继续你该做的" + integration sprint plan §B.5 |
| Author | Claude PM-Orchestrator (Opus) |
| Date | 2026-04-29 |
| Scope | One module + sql queries + integration tests; in / out clearly bounded |
| Time estimate | 60-90 min focused work |
| Blast radius | Money path code; **mitigated by integration tests against real PG before commit** |
| Pre-commit gate | All AT-OBS-004 + AT-OBS-007 + happy-path commit/abort tests PASS against dev PG; codex review --uncommitted; no fakes per truth-first |

## What's in scope

1. `backend/sql/queries/billing_settle.sql` — sqlc queries for Tx2 atomic 5-effect
2. `backend/internal/billing/settler.go` — DefaultSettler implementing the Settler interface from billing.go
3. `backend/internal/billing/settler_integration_test.go` — AT-OBS-004 atomic, AT-OBS-007 outbox same-tx, happy commit, abort path
4. Schema additive migration if needed for `cache_creation_5m_tokens` + `cache_creation_1h_tokens` (Delta D-3)

## What's NOT in scope (deferred)

- Multi-window quota auto-rollover (subscription rolling / API key rate windows) — Phase 4.5
- DLQ persistence on usage record write fail — Phase 4.5
- Async reconciliation worker for pending_reconciliation — Phase 4.5
- Outbox consumer (cross-threshold cache invalidation worker) — Phase 4.5
- Per-tenant pricing override read — Phase 4.5
- Tiered billing expression engine — Phase 5+

This Phase B.5 establishes the **money-path durable settle** that returns success only when usage_record + billing_event + claim status flip + in_flight_count decrement all commit atomically. Async/post-settle features layer on top later.

## Sub-tasks (sequential)

### B.5.1 — sql/queries/billing_settle.sql

Queries needed:
- `GetClaimForSettle :one` — SELECT claim row FOR UPDATE WHERE id=$1 AND tenant_id=$2 AND acquisition_token=$3 AND status='reserving'. Returns id + predicted_cost + provider_account_id + acquisition_token + currency + billing_policy_version. Used at start of Tx2 to verify claim is still settle-able.
- `InsertUsageRecord :one` — full row insert per `usage_records` table schema; cache + reasoning + routing_reason + end_class fields.
- `InsertBillingEvent :one` — audit-grade row insert per `billing_events` table.
- `InsertOutboxRow :one` — `scheduler_outbox` insert when cross-threshold quota signal fires.
- `DecrementInFlightCount :execrows` — UPDATE provider_accounts pa SET in_flight_count = in_flight_count - 1 FROM pool_slot_acquisitions psa WHERE psa.acquisition_token=$1 AND psa.provider_account_id=pa.id AND pa.in_flight_count > 0 (the JOIN form per fusion arch RB-2).
- `UpdateClaimCommitted :execrows` — UPDATE claim status='committed', actual_cost=$N, settled_at=NOW() WHERE id=$1 AND status='reserving'.
- `UpdateClaimAborted :execrows` — UPDATE claim status='aborted', aborted_reason=$2, settled_at=NOW() WHERE id=$1 AND status='reserving'.

### B.5.2 — internal/billing/settler.go

`DefaultSettler` impl:
- Constructor: `NewSettler(pool *pgxpool.Pool) *DefaultSettler`
- `Settle(ctx, req SettleRequest) (*SettleResult, error)`:
  1. Begin Tx (Serializable isolation)
  2. `GetClaimForSettle` — verify reserving + acquisition_token match. If not match: rollback + ErrAcquisitionTokenMismatch.
  3. `InsertUsageRecord` with bucket fields + final usage source enum + end_class.
  4. `InsertBillingEvent` audit row.
  5. (Phase B.5 simplification) Skip multi-window quota update — defer to 4.5; quota outbox row only on threshold crossing detected via simple operator-policy stub returning false in this phase.
  6. `DecrementInFlightCount` (idempotent: only if acquisition_token matches AND counter > 0).
  7. `UpdateClaimCommitted` with actual_cost.
  8. Commit Tx.
- `Abort(ctx, claimID, reason)`:
  1. Begin Tx.
  2. `UpdateClaimAborted` (no usage_record, no billing_event for abort path; or write zero-quota usage_record per spec — TBD; pick "zero-quota usage_record + billing_event with abort flag" for audit completeness).
  3. Decrement in_flight_count if claim had acquisition_token.
  4. Commit.
- Return typed errors: ErrPoolNotConfigured, ErrAcquisitionTokenMismatch, ErrClaimNotReserving.

### B.5.3 — settler_integration_test.go (//go:build integration_pg)

Tests against real dev PG:
- **TestSettler_NilPool_ReturnsTypedError** — contract: nil pool → ErrPoolNotConfigured.
- **TestAT_OBS_004_AtomicFiveEffect** — happy path: pre-condition seed claim + pool_slot_acquisitions row + provider_account; call Settle; verify usage_records row + billing_events row + claim row status=committed + in_flight_count decremented; all in same Tx (verify by transactional rollback test variant: kill mid-Tx, ensure no partial rows visible).
- **TestAT_OBS_007_OutboxSameTx** — when outbox row generated (stub: force-true policy in test), verify it lands in scheduler_outbox and rolls back atomically with the rest if commit fails.
- **TestSettler_AbortPath** — call Abort(claimID, "test abort"); verify status=aborted, no usage_record/billing_event with non-zero cost.
- **TestSettler_AcquisitionTokenMismatch** — pass mismatched token; verify ErrAcquisitionTokenMismatch + claim row untouched.
- **TestSettler_AlreadyCommitted_NoOp** — call Settle twice; second returns ErrClaimNotReserving; no double-write.

## Pre-execution checklist

- [x] dev PG running (`docker compose -f docker-compose.dev.yml ps`)
- [x] migrations applied (schema_migrations.version=6)
- [x] Phase B.4 ClaimGate tests still PASS (regression sanity)
- [ ] Re-read `docs/specs/observability-billing.md` §Tx2 (steps 8-16) before writing
- [ ] Re-read `docs/schema/observability-billing.sql` for usage_records / billing_events / scheduler_outbox column lists

## Failure modes + mitigation

| Failure | Mitigation |
|---|---|
| sqlc field-name mismatch (e.g. ApiKeyID vs APIKeyID) | precedent from Phase B.4: regenerate sqlc; fix field references; build clean before test |
| nullable PG column scan failure (decimal.NullDecimal pattern) | precedent from Phase B.4 actual_cost; apply same pattern to other nullable money columns |
| AppLocker block on go test | precedent: use GOTMPDIR pointed to project-local .tmp/ |
| Decimal precision loss through PG numeric(20,8) | already verified in B.4 AT-OBS-014; same pattern applies |
| Serialization conflict between Tx1 and Tx2 on same claim row | both transactions SELECT FOR UPDATE; Tx2 waits for Tx1 commit; in test, Tx1 always commits before Tx2 begins |
| Scope creep (multi-window quota, async worker, outbox consumer) | Strict in-scope list above; defer everything else to 4.5 backlog |

## Codex review trigger

Before commit: `codex exec review --uncommitted --full-auto`. Address HIGH findings; document MED in commit message.

## Decision points (none expected mid-flight)

If a sub-task fails to complete in budget, stop + write status note + surface to Owner. Do NOT attempt to plow through with hacks.

## Rollback plan

If Phase B.5 lands but later proves wrong:
- Settler is a new file; revert via `git revert <SHA>` cleanly.
- Settler tests are gated behind `integration_pg` build tag — do not run in default suite; no regression risk.
- Schema migration (if needed) is additive; columns are nullable defaults; no data-loss rollback drama.

---

Starting execution now.
