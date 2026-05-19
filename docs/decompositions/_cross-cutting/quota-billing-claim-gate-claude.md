# Quota Atomic Reservation + Billing Idempotent Claim Gate (Claude pass)

| Field | Value |
| --- | --- |
| Status | Draft |
| Author | Claude (PM-Orchestrator), independent specifier-lane pass |
| Date | 2026-04-28 |
| Sources read | one-api `model/token.go` + `model/user.go` (verified URLs in [07_REFERENCE_EVIDENCE_LEDGER.md](../../07_REFERENCE_EVIDENCE_LEDGER.md) E-OAI-DEEP-007/008/009 and Codex E-OAI-DEEP-013/015) + Sub2API billing-claim-gate evidence (E-S2A-DEEP-011) |
| Cross-cutting note | This algorithm spans two references intentionally. one-api is the anti-pattern (what HUAKAI must NOT inherit). Sub2API is the design pattern HUAKAI adopts. The synthesis is HUAKAI's Option C strict design. |
| Companion file | The Codex parallel pass for the same algorithm lives at [quota-billing-claim-gate-codex.md](quota-billing-claim-gate-codex.md). The synthesized final design lives at [`docs/specs/quota-billing-claim-gate.md`](../../specs/quota-billing-claim-gate.md) (Option C strict, pending). |

## 1. WHY

Money-grade correctness is non-negotiable for Owner's **Model 1** (self-deployment + sells API to end-users; per [DR-002 §Owner Refinement](../../process/decisions/DR-002-product-editions.md)). Every billing mistake is real money lost or real user disputes. Two failure shapes are catastrophic for relay-station operators:

1. **Silent overspend**: a User exhausts their Quota but the gateway keeps approving requests because pre-call check and post-call deduct are not atomic. The relay operator pays upstream for tokens the User cannot be charged for.
2. **Double charge on retry**: a request that succeeds upstream but fails to reach the client is retried; without an idempotency key shared across attempts, the User is billed twice while only one upstream call was honored end-to-end.

Both are routine in production. Sub2API ([E-S2A-DEEP-011](../../07_REFERENCE_EVIDENCE_LEDGER.md)) addresses them with a request-fingerprint claim gate that wraps subscription / balance / API Key quota / API Key rate window / Provider Account quota in **one database transaction**, rejecting replays with conflicting fingerprints. one-api ([E-OAI-DEEP-005, E-OAI-DEEP-008, E-OAI-DEEP-013, E-OAI-DEEP-015](../../07_REFERENCE_EVIDENCE_LEDGER.md)) does NOT. HUAKAI's design pressure is "preserve money correctness across crashes, retries, races, and multi-instance deployments without becoming unusably slow." That tradeoff has a known good answer (Sub2API's approach); the work here is to capture it precisely so HUAKAI does not silently regress.

## 2. WHAT (in HUAKAI vocabulary)

The algorithm covers a single User's request from API Key resolution through final Billing Ledger commit. Step-by-step:

1. **Request envelope construction**: gateway receives the client request and assigns a `request_id` (cryptographically random, 128-bit). All subsequent steps reference this id.
2. **Idempotency key computation**: gateway computes the request fingerprint = hash(API Key id, User id, requested model, request body bytes, declared streaming preference). The `idempotency_key` is `(tenant_id, request_id)` paired with the fingerprint. Retry attempts within the same logical request reuse the same `request_id` and therefore the same `idempotency_key`.
3. **Open Reservation Transaction**: a single DB transaction opens with explicit isolation level `serializable` OR with a `SELECT ... FOR UPDATE` row lock on the rows that will be mutated. Inside the transaction:
   - Look up API Key state (active, not expired, not disabled, sufficient per-key quota). If not, abort + return clean error. No state mutated.
   - Look up User account balance. If not sufficient, abort + return clean error.
   - Look up Provider Account in the chosen Pool's eligible set; check Provider Account quota state.
   - Estimate cost: `prompt_tokens × model_input_price + reserve_buffer × model_output_price + reserve_buffer × completion_multiplier`, multiplied by User Group ratio and Channel ratio. Negative effective multipliers clamped to zero.
   - **Reserve** that estimated cost: decrement (per-key quota, User balance, Provider Account quota) by the estimate, all in this same transaction.
   - Write a Reservation row with status `reserved` carrying `request_id, idempotency_key, fingerprint, tenant_id, User id, API Key id, Provider Account id, estimated_cost, reservation_opened_at`.
   - Commit the transaction.
4. **Pre-flight check on idempotency**: if a row for this `idempotency_key` already exists with status `committed` or `rejected_replay`, the gateway returns the cached previous response immediately and does NOT call upstream. This is the cross-attempt deduplication.
5. **Upstream call**: the gateway dispatches the request to the chosen Provider Account through the streaming forwarder. The Reservation row is the only state held until the call resolves.
6. **Streaming + accounting** (handled by [streaming-forwarder.md](../sub2api/streaming-forwarder.md) decomposition): tokens are counted inline; usage is extracted from upstream events with source labels (`reported / normalized / inferred / partial`).
7. **Reconcile Transaction**: a second DB transaction opens. Inside it:
   - Read the Reservation row by `idempotency_key`.
   - If the row's status is already `committed` or `rejected_replay` (because a parallel reconcile beat us — should not happen but defended), abort idempotently.
   - Compute `actual_cost` from the streaming-forwarder's final usage record, multiplied by ratios.
   - **Diff**: `delta = actual_cost - estimated_cost`. May be positive (User used more than estimate) or negative (User used less than estimate; refund needed).
   - Apply the diff atomically: increment / decrement (per-key quota, User balance, Provider Account quota) by the diff.
   - Write the Usage Record (`request_id, tenant_id, User, API Key, Pool, Provider Account, actual_cost, usage_source, routing_reason`).
   - Write the Billing Ledger entry referencing `request_id` and `Usage Record id`. The entry is the source-of-truth for charging.
   - Update Reservation row to `committed`.
   - Commit the transaction.
8. **Crash recovery sweep** (background): a periodic sweep scans for Reservation rows in `reserved` state older than `T_max` (e.g. 5 minutes — longer than any reasonable upstream call). For each such row, the sweep checks if the corresponding upstream call ever produced a Usage Record. If yes, run the Reconcile Transaction. If no, **release the reservation** by reversing the initial decrements; mark the Reservation row `released_orphaned` with reason. This is the gateway-crash recovery path.
9. **Replay rejection**: any later request that recomputes the same fingerprint will hit the pre-flight check (step 4) and return the prior response — never reach upstream a second time. This is the cross-attempt deduplication contract.

## 3. INPUTS (signals consumed, state mutated)

Per request:
- API Key bytes, target Model id, request body bytes, streaming preference, requested capabilities (tool-use, vision, reasoning effort).

Per API Key state (DB read):
- key id, owning User id, key status (active/exhausted/expired/disabled), per-key quota remaining, per-key rate window state, expiration timestamp.

Per User state (DB read):
- account balance, User Group membership, low-quota notification threshold.

Per Pool / Channel / Provider Account state (DB read):
- Channel allow-list (model support), Provider Account credentials (referenced by id; never read into the algorithm), Provider Account quota state, current health (operational / degraded / failed), tenant_id binding.

Per Model state (DB read):
- input price, output price, completion multiplier, reasoning-effort budget multiplier, cache-hit pricing rule.

State mutated (in two distinct transactions):
- Reservation row (Tx1 INSERT, Tx2 UPDATE).
- API Key per-key quota counter (Tx1 decrement reserve, Tx2 reconcile diff).
- User balance (Tx1 decrement reserve, Tx2 reconcile diff).
- Provider Account quota (Tx1 decrement reserve, Tx2 reconcile diff).
- Usage Record row (Tx2 INSERT).
- Billing Ledger row (Tx2 INSERT).

Time:
- monotonic clock for reservation timeout sweep; wall clock for ledger entry timestamps.

Randomness:
- only used in the `request_id` generation; algorithm is otherwise deterministic given inputs.

## 4. FAILURE MODES HANDLED

Per the design above:
- **Concurrent over-deduct**: the Tx1 transaction with serializable isolation OR row lock makes pre-check + reserve atomic. Two concurrent requests cannot both pass pre-check; the second will retry under the lock or be rejected.
- **Crash mid-flight (after reserve, before reconcile)**: the orphan sweep (step 8) detects unreconciled reservations, checks whether the upstream call actually completed (via Usage Record presence), and either reconciles or releases the reservation atomically.
- **Retry double-charge**: Step 4's pre-flight check by `idempotency_key` rejects replays before reaching upstream and returns the prior response.
- **Stale cross-instance cache**: the algorithm requires every read inside Tx1/Tx2 to come from the DB under lock, NOT from in-memory cache. Cache is allowed for fast-path checks BEFORE Tx1, but the authoritative state inside Tx1 is the DB row read under the same lock that governs the mutation.
- **Estimate vs actual divergence**: the Reconcile Transaction (step 7) explicitly diffs estimate vs actual and applies the delta. Estimate-too-high refunds the User; estimate-too-low captures the additional cost.
- **Unlimited tokens draining User balance silently**: HUAKAI's algorithm requires every successful request to write a Usage Record AND a Billing Ledger entry, even for unlimited-quota tokens. The ledger entry is the audit anchor.

## 5. FAILURE MODES NOT HANDLED (gaps in upstream that HUAKAI MUST close)

These are the concrete one-api gaps that HUAKAI's design above closes by construction. Listing them so the gap closure is auditable:

- **G1**: one-api's pre-call check and post-call deduct are not in the same transaction. Concurrent requests slip through the gap. **Closed** by Tx1 covering both steps under lock.
- **G2**: one-api has no per-request idempotency key shared across retry attempts. Retry can double-charge. **Closed** by step 4's idempotency check.
- **G3**: one-api uses `CacheGetTokenByKey` for cross-instance reads; cache invalidation is best-effort. Stale cache approves requests that another instance already exhausted. **Closed** by mandating DB-under-lock reads inside Tx1; cache is fast-path advisory only.
- **G4**: one-api crash mid-flight permanently loses Quota (reservation never released). **Closed** by step 8's orphan sweep.
- **G5**: one-api's batch-update mode defers DB writes; concurrent requests in the same batch window all see pre-batch state and overspend collectively. **Closed** by HUAKAI banning batched DB writes for money-grade mutations (Tx1/Tx2 are synchronous and individual).
- **G6**: one-api's unlimited tokens skip key-quota mutation but still drain User balance with no key-level audit. **Closed** by HUAKAI requiring every request to produce a Usage Record + Billing Ledger entry regardless of unlimited-flag state.
- **G7**: one-api's User balance update and Usage Record write are not in the same transaction; a crash between them produces drift. **Closed** by Tx2 covering both.
- **G8**: one-api isolation level defaults to Read Committed in many DBs; phantom reads possible between check and write. **Closed** by HUAKAI Tx1/Tx2 using `serializable` OR `SELECT FOR UPDATE`.

## 6. KEEP / IMPROVE / AVOID for HUAKAI

- **KEEP**: Sub2API's idempotent fingerprint claim gate as the topology. Two-transaction pattern (reserve then reconcile).
- **KEEP**: Sub2API's transaction boundary spanning subscription + balance + API Key quota + API Key rate window + Provider Account quota in one mutation. Do NOT split.
- **KEEP**: pre-call estimate + post-call reconcile (close to one-api's two-stage model), but with the atomicity and idempotency that one-api lacks.
- **IMPROVE**: explicit isolation level OR explicit row lock — never rely on default. PostgreSQL `SELECT ... FOR UPDATE` is the HUAKAI default per [DR-006](../../process/decisions/DR-006-database.md).
- **IMPROVE**: Reservation row TTL + orphan sweep is mandatory, not optional. Without this, gateway crash = quota leak.
- **IMPROVE**: idempotency key includes `tenant_id` (DR-001 multi-tenant safety); two tenants cannot collide on identical fingerprints by accident.
- **IMPROVE**: Usage Record's `usage_source` enum (`reported / normalized / inferred / partial`, per [streaming-forwarder.md](../sub2api/streaming-forwarder.md)) is referenced in the Billing Ledger entry. When source is `inferred`, the ledger entry carries a `pending_reconciliation` flag that may be cleared if upstream reports usage out-of-band later.
- **AVOID**: any code path that reads "remaining quota" from cache to decide whether to approve. Cache is for telemetry only inside the algorithm's scope. (Cache outside this algorithm — UI dashboard, observability — is fine.)
- **AVOID**: batched DB writes for money-grade mutations. one-api's batched mode is unsafe by design and HUAKAI must not adopt it for Quota / Billing.
- **AVOID**: any "we'll just rely on the database to be atomic for two separate UPDATE statements" pattern. If two writes need to be atomic, they go in one transaction. Period.

## 7. ATTRIBUTION

- Source files Claude read: `https://raw.githubusercontent.com/songquanpeng/one-api/main/model/token.go` (full WebFetch summary, 12 numbered behaviors); `https://raw.githubusercontent.com/songquanpeng/one-api/main/model/user.go` (7 numbered behaviors). Both verified URLs.
- Sub2API claim-gate evidence drawn from Codex E-S2A-DEEP-011 specifier session (raw artifact `.omc/artifacts/ask/codex-role-codex-specifier-lane-miner-for-huakai-deep-source-decom-2026-04-28T05-18-49-050Z.md`). Claude has not personally read Sub2API billing source files; Codex has.
- This decomposition is **Claude's pass**. A parallel Codex pass exists at [quota-billing-claim-gate-codex.md](quota-billing-claim-gate-codex.md) (forthcoming in this commit batch). Cross-audit + synthesized final action plan to follow per Owner directive 2026-04-28: "同样的事情你们都要做，然后互审对方的结果。然后给出最终的优化排版行动方案."
- Specifier-lane session: Claude (this conversation), 2026-04-28.
- Reviewer-lane session: Codex (will be dispatched after Codex's parallel pass is filed; Codex must be a fresh session to perform the review).

## Review Sign-Off

| Field | Value |
| --- | --- |
| Reviewer | (pending — Codex symmetric pass first, then synthesized action plan, then reviewer-lane sign-off) |
| Review date | (pending) |
| Checks passed | (pending; CL-001..010 must all pass) |
| Notes | First cross-cutting prose decomposition (spans one-api + Sub2API). Algorithm is the load-bearing core for Owner's Model 1 commercial launch — money-grade correctness is non-negotiable. |
