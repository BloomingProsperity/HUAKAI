# 2026-05-28 S1-013 Durable Atomic Balance Hold — Claude independent draft (#10)

> Parallel-draft per CLAUDE.md #10. Written without reading the codex draft
> (`2026-05-28-s1-013-durable-hold-codex.md`). Synthesis + Owner surface happen after both exist.

## Finding

**S1-013 (ClaimGate not durable/atomic).** The pre-dispatch admission gate does not durably or
atomically reserve funds, so concurrent requests can all pass the gate and overspend.

Evidence (current code):
- `backend/internal/billing/claim_gate.go:71-174` — `Reserve()` runs Tx1 (SERIALIZABLE) but only does
  idempotency lookup + `InsertClaim(status='reserving', predicted_cost)` + COMMIT. **Spec §Tx1 step 4
  "reserve quota across 5 dimensions" is absent** — nothing checks or reserves balance.
- `backend/internal/billing/settler.go:77-252` — `Settle()` (Tx2) writes usage_record + billing_event
  + releases the pool slot + `UpdateClaimCommitted(actual_cost)`, but **never debits any user balance**;
  it even returns `SettleResult{NewUserBalance: decimal.Zero}` (settler.go:251) as a placeholder.
- `backend/sql/migrations/0002_observability_billing.up.sql:19-74` — `billing_ledger_claims` has
  `predicted_cost` / `actual_cost` / `status` / `lease_expires_at` but **no balance table and no held column anywhere.**
- The interface contracts already *anticipate* this: `ClaimGate.Reserve` doc says "reserves quota across
  5 dimensions" (billing.go:23); `SettleResult` already carries `NewUserBalance` + `APIKeyQuotaExhausted`
  (billing.go:109-110). The work is **completing a spec'd-but-deferred Phase E+ core**, not bolting on a foreign feature.

## Owner-approved design (do not re-litigate)

Durable `held` column + atomic pre-deduction, modeled on one-api/new-api preConsumeQuota but **upgraded**
(see Reference comparison). Owner sketch: `user_balances(balance, held, version)` + `quota_buckets` +
`balance_holds(keyed by claim_id)`.

## Scope

**In:**
- New migration `0060_user_balance_holds` (up + down): `user_balances`, `balance_holds`. (`quota_buckets` — see Decision D2.)
- New non-frozen package `backend/internal/balancehold`: `Reserve` / `Capture` / `Release` + their SQL, all
  operating on a caller-supplied `pgx.Tx` (so they compose into the existing Tx1/Tx2).
- Wire `Reserve` into `claim_gate.go` Tx1 (new-claim + re-reserve paths).
- Wire `Capture` into `settler.go` `Settle` (committed) and `CommitCacheHit` (cost 0), `Release` into `Abort`.
- Lease-sweep worker (new file in `billing/`, reuses `Abort`) to release holds of orphaned `reserving` claims.
- Discriminating tests (see Test plan).

**Out:**
- API-key / subscription / rate-window quota enforcement (the other 4 of the "5 dimensions") — schema may be
  staged (D2) but enforcement is a follow-up slice.
- Dynamic pricing / how `predicted_cost` is computed (unchanged; we hold whatever `ReserveRequest.PredictedCost` carries).
- Refund→balance credit (Refund currently only appends a negative billing_event; see Decision D3).
- Frozen packages `gatewayhttp/gateway/proto` — untouched (gate/settle live in `billing`).

## Success criteria

1. Two+ concurrent requests for the same user whose combined predicted cost exceeds available balance:
   exactly the affordable subset reserve successfully; the rest get a typed insufficient-funds error mapped to **HTTP 402**.
2. `held` never exceeds what is genuinely reserved; on commit, `balance` drops by **actual** cost and the hold is fully removed; on abort/cache-hit/lease-expiry the hold is released and `balance` is unchanged.
3. Capture and Release are **idempotent** (keyed by `claim_id`): a retry/double-call charges or releases at most once.
4. Balance debit happens in the **same Tx2** as usage_record/claim-commit (atomic, not async/best-effort).
5. `go build ./...` + `go test ./...` green; new tests pass the mutation check.

## Blast radius

Money path core (Tx1 admission + Tx2 settlement). A bug can: deny legitimate paid traffic (false 402),
permit overspend (held not enforced), double-charge (non-idempotent capture), or strand funds (held never
released → user can't spend). Mitigation: `CHECK (held >= 0)`, idempotent `WHERE state='held'` guards,
lease-sweep release, and the discriminating concurrency test as the gate.

## Failure modes + mitigations

| Failure | Mitigation |
|---|---|
| Concurrent reserves both read stale balance and overspend | Atomic conditional `UPDATE ... WHERE (balance-held) >= cost` (row-lock + SERIALIZABLE); 0 rows ⇒ 402. The check and the increment are one statement. |
| Crash after Reserve, before Settle ⇒ phantom hold | Lease-sweep releases holds for `reserving` claims past `lease_expires_at` (reuses `Abort`). |
| Double Capture (retry) double-debits | `balance_holds.state` transition `held→captured` guarded by `WHERE state='held'`; 0 rows ⇒ no-op. |
| Double Release pushes held negative | Same state guard + `CHECK (held >= 0)` backstop. |
| `actual_cost > predicted` (under-estimate) ⇒ balance below 0 | Allow truthful overage debt (no `CHECK balance>=0`); next reserve auto-rejects since `(balance-held) < 0 < cost`. (Decision D1.) |
| Re-reserve of an aborted claim (claim_gate.go:106-120) leaves a stale released hold | Re-reserve transitions the per-claim hold `released→held` with the new `predicted_cost` (UPSERT on `claim_id`). |
| User has no balance row | Treated as zero funds ⇒ 402 (rows must be admin-provisioned). Unlimited users — Decision D2. |
| Cache-hit claim keeps its hold (cost 0 but never released) | `CommitCacheHit` calls `Capture(claim, 0)` ⇒ releases hold, no debit. |

## Migration 0060 (DDL)

```sql
-- 0060_user_balance_holds.up.sql
-- 持久化用户余额 + 预留(held) + per-claim 预留台账, 修复 S1-013(并发超支)。
-- money 列沿用 numeric(20,8)(spec F-OBS-001 H4)。

CREATE TABLE IF NOT EXISTS user_balances (
    tenant_id   bigint        NOT NULL,
    user_id     bigint        NOT NULL,
    balance     numeric(20,8) NOT NULL DEFAULT 0,        -- 可用余额(已结算口径)
    held        numeric(20,8) NOT NULL DEFAULT 0 CHECK (held >= 0), -- 在途预留, <= balance 不强制(允许 D1 超额债)
    version     bigint        NOT NULL DEFAULT 0,        -- 乐观并发 + 审计游标
    updated_at  timestamptz   NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, user_id),
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id)  -- 跨租户安全(uq_users_tenant_id_id)
);

CREATE TABLE IF NOT EXISTS balance_holds (
    claim_id     bigint        PRIMARY KEY REFERENCES billing_ledger_claims (id),
    tenant_id    bigint        NOT NULL,
    user_id      bigint        NOT NULL,
    amount       numeric(20,8) NOT NULL CHECK (amount >= 0),   -- 当前预留额(= 该 claim 的 predicted_cost)
    captured     numeric(20,8) NOT NULL DEFAULT 0,             -- capture 时落账的 actual
    state        text          NOT NULL DEFAULT 'held'
                               CHECK (state IN ('held','captured','released')),
    created_at   timestamptz   NOT NULL DEFAULT now(),
    resolved_at  timestamptz,
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id)
);
CREATE INDEX idx_balance_holds_user_state ON balance_holds (tenant_id, user_id, state) WHERE state = 'held';
```

```sql
-- 0060_user_balance_holds.down.sql
DROP TABLE IF EXISTS balance_holds;
DROP TABLE IF EXISTS user_balances;
```

> `quota_buckets` intentionally omitted from this draft's DDL — see Decision D2.

## `balancehold` package API + SQL

```go
package balancehold

// Reserve atomically reserves cost for a claim. Returns ErrInsufficientBalance
// (caller maps to 402) when (balance - held) < cost or no balance row exists.
// Idempotent re-reserve of the same claim_id transitions released→held.
func Reserve(ctx context.Context, tx pgx.Tx, p ReserveParams) (Snapshot, error)

// Capture finalizes a held claim for actualCost: balance -= actualCost,
// held -= reservedAmount, hold→captured. Idempotent on claim_id (WHERE state='held').
func Capture(ctx context.Context, tx pgx.Tx, claimID int64, actualCost decimal.Decimal) (Snapshot, error)

// Release returns a held reservation (abort / lease expiry): held -= reservedAmount,
// balance unchanged, hold→released. Idempotent on claim_id.
func Release(ctx context.Context, tx pgx.Tx, claimID int64) (Snapshot, error)
```

Core SQL shapes (authored in `backend/sql/queries/balance_holds.sql`, generated via `sqlc generate`):

- **Reserve (atomic gate):**
  ```sql
  -- name: ReserveBalanceHold :one
  UPDATE user_balances
     SET held = held + @cost, version = version + 1, updated_at = now()
   WHERE tenant_id = @tenant_id AND user_id = @user_id
     AND (balance - held) >= @cost
  RETURNING balance, held, version;
  ```
  0 rows ⇒ `ErrInsufficientBalance`. Then `INSERT INTO balance_holds (...) VALUES (... 'held')
  ON CONFLICT (claim_id) DO UPDATE SET amount=@cost, state='held', resolved_at=NULL WHERE balance_holds.state='released'`.

- **Capture:**
  ```sql
  -- name: CaptureBalanceHold :execrows  (guard makes it idempotent)
  UPDATE balance_holds SET state='captured', captured=@actual, resolved_at=now()
   WHERE claim_id=@claim_id AND state='held';
  ```
  When 1 row updated, also `UPDATE user_balances SET balance = balance - @actual, held = held - <reserved>, version=version+1`.
  (The reserved amount is read from the hold row in the same tx.)

- **Release:** `UPDATE balance_holds SET state='released', resolved_at=now() WHERE claim_id=@claim_id AND state='held'`
  → if 1 row, `UPDATE user_balances SET held = held - <reserved>, version=version+1`.

## Tx1 integration (`claim_gate.go`)

- **New-claim path** (`claim_gate.go:145-168`, after `InsertClaim`, before `tx.Commit` at :170): call
  `balancehold.Reserve(ctx, tx, {tenant, user, claimID: inserted.ID, cost: req.PredictedCost})`. On
  `ErrInsufficientBalance` return it up; the gate caller maps to 402. Same serializable tx → reserve is atomic with the claim insert.
- **Re-reserve path** (`claim_gate.go:106-120`, after `ReReserveAbortedClaim`): the prior hold was released on
  abort; call `Reserve` again with the (possibly updated) `req.PredictedCost` — the `ON CONFLICT` transitions
  `released→held`. Insufficient ⇒ 402.
- Lock order: the conditional `UPDATE user_balances` takes the balance row lock; claim row is the insert/locked
  lookup. Order = claim → user_balances (→ quota_buckets when D2 lands), matching spec §Tx1 step 2.

## Tx2 integration (`settler.go`)

- **`Settle`** (`settler.go:232-249`, after `UpdateClaimCommitted`, before `tx.Commit` at :247): call
  `snap, _ := balancehold.Capture(ctx, tx, claim.ID, actualCost)` and set the return at :251 to
  `SettleResult{NewUserBalance: snap.Balance, ...}` (replaces the hardcoded `decimal.Zero`).
- **`CommitCacheHit`** (`settler.go:431-441`, after `UpdateClaimCommitted`): `Capture(ctx, tx, req.ClaimID, decimal.Zero)`
  — releases the reservation with zero debit.
- **`Abort`** (`settler.go:289-385`, after `UpdateClaimAbortedWithReason`, before commit at :385):
  `balancehold.Release(ctx, tx, claimID)`.
- All three reuse the caller's existing SERIALIZABLE tx → balance mutation is atomic with the rest of settlement.

## Lock ordering

Within a tx: **claim row → `user_balances` → (`quota_buckets`)**. Tx1 already holds/inserts the claim;
`ReserveBalanceHold`'s `UPDATE ... WHERE` takes the `user_balances` row lock. Both Tx1 and Tx2 are
`pgx.Serializable`. Consistent ordering across Reserve/Capture/Release avoids deadlock.

## Lease-sweep design

New file `backend/internal/billing/lease_sweep.go`: `LeaseSweeper{ pool, settler }` with `SweepOnce(ctx)`:
```sql
SELECT id, tenant_id FROM billing_ledger_claims
 WHERE status='reserving' AND lease_expires_at < now()
 ORDER BY lease_expires_at LIMIT @batch FOR UPDATE SKIP LOCKED;
```
For each, call `settler.Abort(ctx, tenant, id, "lease_expired", auditID, 0)` — which already flips the claim to
aborted, writes the audit usage_record, releases the pool slot, **and (after this change) releases the hold**.
Scheduled from the gateway bootstrap on a ticker (e.g. every `LeaseWindow/3`). Uses the existing
`idx_claims_status_lease` partial index. `FOR UPDATE SKIP LOCKED` lets multiple instances sweep safely.

## Discriminating tests (#14)

All in `backend/internal/balancehold/*_test.go` (+ one settle-path test in `billing`). Each names the exact regression it catches; each has a stated mutation that turns it red.

1. **Concurrency overspend (THE test):** `balance=10, held=0`; 5 goroutines each `Reserve(cost=3)`. Assert
   **exactly 3** succeed, 2 return `ErrInsufficientBalance`, and final `held==9, balance==10`.
   *Mutation:* drop the `(balance - held) >= cost` predicate ⇒ 5 succeed, `held==15 > balance` ⇒ red.
2. **Capture charges actual, clears hold:** reserve `predicted=5` (held=5); `Capture(actual=3)` ⇒ `balance==B-3, held==0`, hold state `captured`.
   *Mutation:* capture deducting `predicted` not `actual` ⇒ `balance==B-5` ⇒ red.
3. **Capture idempotency:** `Capture` twice ⇒ balance debited once, second call 0-rows no-op.
   *Mutation:* remove `WHERE state='held'` ⇒ double debit ⇒ red.
4. **Release idempotency + restores available:** reserve 5, `Release` twice ⇒ `held==0` once; `(balance-held)` restored.
   *Mutation:* remove state guard ⇒ `held` negative ⇒ `CHECK (held>=0)` violation / wrong value ⇒ red.
5. **Lease sweep releases orphan:** insert `reserving` claim with hold, `lease_expires_at` in past; `SweepOnce` ⇒ claim `aborted`, `held==0`.
   *Mutation:* sweeper that aborts but skips hold release ⇒ `held` stuck ⇒ red.
6. **Cache-hit releases hold (no debit):** reserve `predicted=5`; `CommitCacheHit` ⇒ `held==0, balance` unchanged.
   *Mutation:* cache-hit path missing `Capture(0)` ⇒ `held==5` stuck ⇒ red.
7. **Re-reserve after abort re-holds:** reserve 5 → abort (held=0, state released) → re-reserve 4 ⇒ `held==4`, hold state `held`.
   *Mutation:* `ON CONFLICT` guard wrong (won't transition released→held) ⇒ re-reserve fails / held wrong ⇒ red.

## Reference comparison (#15)

| Concern | one-api `@8df4a26` | new-api `@20d3e73` | HUAKAI delta (approved) | Dimension |
|---|---|---|---|---|
| Pre-request reserve | `relay/controller/helper.go:71-93`: read user quota from cache, reject if `quota-pre<0` (403), then **decrement the user quota directly** (`CacheDecreaseUserQuota` + token-level pre-consume). | `service/pre_consume_quota.go:34-76`: same shape — reject if `userQuota-pre<0` (403), then `DecreaseUserQuota` directly. | A **separate durable `held` column**; reserve adds to `held`, **`balance` untouched** until capture; atomic `(balance-held)>=cost` gate. | 架构 (storage model: held vs single mutable counter) |
| Reconcile after call | `helper.go:116-117`: `PostConsumeTokenQuota(delta = quota - preConsumed)` trues up post-call. | `pre_consume_quota.go:17-29`: on failure `ReturnPreConsumedQuota` runs `PostConsumeQuota(-pre)` **async via `gopool.Go`** (best-effort, outside request tx). | **Capture in the same Tx2** as usage_record/claim-commit (synchronous, atomic); abort/lease-sweep release. | 算法 (sync settlement vs async best-effort refund) |
| Crash safety / orphan | No explicit reserved state; a crash between debit and reconcile leaves over-debit until/if reconciled. | Same; async refund goroutine can be lost on crash. | `held` is an explicit auditable state; lease-sweep releases orphaned holds deterministically. | 生态 (auditable hold lifecycle + sweep) |
| Trusted high-balance skip | `helper.go:82-87`: if `userQuota > 100×pre`, skip pre-consume. | `pre_consume_quota.go:48-64`: `trustQuota` + token-unlimited skip. | Not adopted (weakens the guarantee under concurrency). Noted as possible D-future optimization. | — |

Source files read: `one-api/relay/controller/helper.go`, `new-api/service/pre_consume_quota.go`.
Lane = specifier. Both are production (relay/service, not tests); shas recorded above (recency: re-verify `pushed_at` < 90d at surface time per #12 first-cite check).

**Delta summary (1-2 sentences):** HUAKAI's hold = one-api/new-api's "pre-consume then reconcile" pattern,
upgraded from *immediate-debit-then-async-refund on a single quota counter* to a *separate durable `held`
reservation captured synchronously inside the settlement transaction, with deterministic lease-sweep release* —
removing the crash-window over-debit and giving an auditable reserved state both refs lack.

## Decision points for Owner

- **D1 — overage debt vs clamp:** when `actual_cost > predicted` (under-estimate), allow `balance` to go
  negative (truthful debt; next reserve auto-rejects) **[recommended]**, or clamp to 0 (hides under-charge). Both refs effectively allow debt.
- **D2 — `quota_buckets` now or later:** ship `user_balances`+`balance_holds` only this slice (fixes the money
  overspend in S1-013) and defer API-key/subscription quota tables+enforcement to a follow-up **[recommended]**,
  or include `quota_buckets` DDL now (cheap) and wire enforcement later.
- **D3 — Refund→balance credit:** `Refund` (settler.go:543) currently only appends a negative billing_event.
  With durable balance, should a refund also `balance += refund`? **[recommend yes, as a small follow-up in the same wave]** for money consistency.
- **D4 — unlimited / unprovisioned users:** missing `user_balances` row ⇒ 402 **[recommended: rows must be
  admin-provisioned]**, or auto-provision with a configurable default / `is_unlimited` flag.

## Sequencing / estimate

Migration + `balancehold` pkg + sqlc gen + Tx1/Tx2 wiring + lease-sweep + tests ≈ one focused codex session
(2-4h wall). **High-risk (schema + billing core): migration is NOT applied and code is NOT landed until Owner
approves this plan (synthesized with codex draft).** Per-commit codex review (#8) before commit; no unresolved S0/S1.
