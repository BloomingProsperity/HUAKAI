# Quota Atomic Reservation + Billing Claim Gate — Synthesis & Final Action Plan

| Field | Value |
| --- | --- |
| Status | Action Plan (Draft) |
| Author | Claude (PM-Orchestrator), synthesizing Claude's pass + Codex's pass |
| Date | 2026-04-28 |
| Mutual review | [Claude pass](quota-billing-claim-gate-claude.md) and [Codex pass](quota-billing-claim-gate-codex.md) were authored independently in parallel sessions per Owner directive 2026-04-28 ("同样的事情你们都要做，然后互审对方的结果。然后给出最终的优化排版行动方案"). This file is the synthesized final action plan. |
| Becomes | The Option C strict spec for HUAKAI's Quota+Billing core. After reviewer-lane sign-off, file moves to `docs/specs/quota-billing-claim-gate.md` and Status becomes `Released`. |

## Convergence (Both Passes Agree)

These are load-bearing facts both Claude and Codex extracted independently. Treating them as established truth:

1. **one-api is the negative reference** — its pre/post-split quota model has at least 8 concrete gaps (G1-G8 in [Claude pass §5](quota-billing-claim-gate-claude.md); restated in different phrasing in [Codex pass §5 of one-api](quota-billing-claim-gate-codex.md)).
2. **Sub2API is the positive reference for idempotency** — its single-transaction multi-dimension claim with fingerprint conflict check + archived replay guard is the topology HUAKAI adopts.
3. **Both upstream references are individually insufficient.** one-api lacks idempotency. Sub2API lacks pre-call reservation (claim is post-call only). HUAKAI is the first to combine both.
4. **Atomic primitive: PostgreSQL serializable + `SELECT FOR UPDATE` row locks** on Billing Ledger / User / API Key / subscription / Provider Account / rate-window rows. Advisory locks only for background recovery coordination.
5. **Cache is read-through hint, never source-of-truth** for spend admission. Authoritative state is the locked DB row.
6. **Tenant isolation: `tenant_id` in every key, every lock, every mutation, every cache key, every Audit Event, every recovery query.**
7. **Cross-attempt deduplication: all retries inside one logical request share the same Billing Ledger claim.** The idempotency key MUST exclude Provider Account identity (that goes into the per-attempt record, not the claim).
8. **Two transactions, not one network-spanning transaction**: reserve transaction commits before upstream; reconcile transaction commits after upstream resolution.
9. **Orphan sweep + Audit Event mandatory** for crash recovery. No quota leak on gateway crash.
10. **Usage Record lives INSIDE the reconcile transaction**, not best-effort after commit. (This is HUAKAI's improvement over Sub2API's "Usage Record is written after billing and is best-effort" pattern.)

## Where Codex Sharpens Claude

Reading Codex's pass after writing my own surfaced 7 sharpenings I should adopt:

- **C1**: Sub2API's claim is **post-call**, not pre-call. Claude's pass implied claim-first; Codex made the temporal location explicit. HUAKAI must do **claim-first AND reserve-first** (move claim to reservation transaction).
- **C2**: **Lease extension for slow upstream**. A long legitimate stream must heartbeat to keep its reservation alive; the orphan sweep ignores reservations with active leases. Claude's pass had only orphan sweep — would have killed legitimate slow requests.
- **C3**: **Idempotency key explicitly excludes Provider Account id**. Provider Account identity belongs in attempt records; the claim key has tenant_id, API Key id, logical request id, endpoint family, request payload hash, requested model, Pooling Group id, billing policy version, request class. (Sub2API's actual key includes Provider Account id; HUAKAI's must not.)
- **C4**: **Sub2API has a "trusted high-balance" shortcut path** that bypasses reservation. Claude missed this. HUAKAI must explicitly **forbid** this shortcut in the design.
- **C5**: **Image / audio paths in one-api have different (worse) charging windows than text**. HUAKAI's design must enforce the same reservation algorithm for ALL endpoint families — text, audio, image, embedding — no per-modality shortcut.
- **C6**: **Archived old claims** — Sub2API archives old claim rows so cleanup of in-flight claims doesn't accidentally allow replay of an old request. HUAKAI must adopt this archive pattern, not just `DELETE` old claims.
- **C7**: **Generated fallback request identities are not replay-stable across client retries.** If the client retries with a fresh `request_id`, HUAKAI's idempotency key changes and the claim does not deduplicate. HUAKAI must therefore: (a) require client-supplied stable request identity, OR (b) derive a deterministic identity from request body hash + API Key + tenant_id when client does not provide one.

## Where Claude Sharpens Codex

These are Claude's pass contributions Codex's pass did not articulate:

- **L1**: Concrete gap-closure mapping `G1..G8` from one-api → HUAKAI. Codex listed gaps but did not number them or map each to a specific HUAKAI design closure. The numbered map is the auditable artifact for the [docs/24 reference tracking policy](../../24_REFERENCE_TRACKING_POLICY.md) — when one-api ships a fix, HUAKAI checks the gap number directly.
- **L2**: **`pending_reconciliation` flag on Usage Record** when usage source is `inferred`. Codex's pass mentions `partial` labeling but does not articulate the late-reconciliation lifecycle.
- **L3**: **Concrete Owner-commercial framing** — HUAKAI's design pressure is mapped to Model 1 commercial launch readiness ([DR-002 §Owner Refinement](../../decisions/DR-002-product-editions.md)), not just "money correctness in general".

## The Synthesized HUAKAI Algorithm — Final

The HUAKAI Quota + Billing Claim Gate algorithm, after combining both passes:

### Atomic primitives

PostgreSQL serializable transaction with **ordered row-level locks** on:
1. Billing Ledger claim row (the gate)
2. User Quota / balance row
3. API Key row
4. Subscription quota row (when present)
5. Provider Account quota row
6. Rate-window rows (per-User × per-Model and per-API-Key)

Lock order is fixed and documented (alphabetical by entity-id pair) to prevent deadlocks under contention.

Advisory locks: ONLY for background recovery coordination (the orphan sweep). Never primary primitive.

### Idempotency key shape

`idempotency_key = HASH(tenant_id, API_Key_id, logical_request_id, endpoint_family, normalized_request_payload_hash, requested_model, pooling_group_id, billing_policy_version, request_class)`

**`logical_request_id`** is:
1. Client-supplied stable request id if present (HTTP header `Idempotency-Key` or equivalent).
2. Else: deterministic derivation from `(API_Key_id, normalized_payload_hash, tenant_id, time-window-bucket-15s)`.
3. Never: a generated random fallback (Codex C7 rule).

**Provider Account id is NOT in the key.** It lives in per-attempt records.

### Two transactions

**Reserve Transaction** (Tx1):
1. Lock rows in fixed order.
2. Look up or insert Billing Ledger claim row by `idempotency_key`.
3. If claim exists with same fingerprint AND status `committed`: return cached prior response immediately, no further mutation.
4. If claim exists with same fingerprint AND status `reserved`: another worker is in flight; return `RESERVATION_IN_PROGRESS` error.
5. If claim exists with **conflicting** fingerprint: write Audit Event `replay_conflict`, return error.
6. If no claim: insert claim row with fingerprint, status `reserved`, opened_at, lease_expires_at (+ T_lease).
7. Verify available capacity on User / API Key / subscription / Provider Account / rate-window. If insufficient: rollback, return clean error.
8. Compute estimated cost via cost engine (input/output prices × multipliers, clamped to non-negative).
9. Decrement `(per-key quota, User balance, subscription quota, Provider Account quota, rate-window counter)` by `estimated_cost`.
10. Insert per-attempt record `(claim_id, attempt_seq=1, Provider_Account_id, opened_at)`.
11. Write Audit Event.
12. Commit.

**Upstream Call** (no DB transaction):
- Streaming forwarder handles the request. Lease is heartbeat-extended on activity.
- On retry to a different Provider Account: open Tx1' that closes the old per-attempt record, releases that Provider Account's reservation, opens a new per-attempt record on the new Provider Account, **keeps the same Billing Ledger claim**.

**Reconcile Transaction** (Tx2):
1. Lock the same rows in the same order.
2. Look up the claim. Verify status is `reserved`.
3. Compute `actual_cost` from final usage record. Mark `usage_source = reported / normalized / inferred / partial`.
4. Compute `delta = actual_cost - estimated_cost`.
5. Apply delta to `(per-key quota, User balance, subscription quota, Provider Account quota, rate-window)`.
6. Insert Usage Record (inside this transaction).
7. Insert Billing Ledger entry referencing claim_id and Usage Record id.
8. Update claim row to `committed`.
9. If `usage_source = inferred`, set `Usage Record.pending_reconciliation = true`.
10. Insert Audit Event.
11. Commit.

### Orphan sweep + lease extension

- Lease default `T_lease = 60s`. Streaming requests heartbeat-extend every `T_lease/2`.
- Background sweep every `T_sweep = 30s` looks for `claim.status = reserved AND lease_expires_at < now`.
- For each: check whether upstream produced a Usage Record. If yes, run Tx2. If no, run Tx_release (release reservations, claim status → `released_orphaned`, write Audit Event with reason `crash_recovery` or `lease_expired`).

### Concurrency invariants (provable post-design)

1. The same logical request cannot be billed twice.
2. Conflicting replay (same key, different fingerprint) writes Audit Event and returns conflict; never silently reuses.
3. Successful admission cannot make User balance / API Key quota negative (unless explicit overdraft policy enabled).
4. Provider Account quota cannot be over-selected beyond `reserved + used` limits.
5. API Key rate windows cannot miss a successful billable request.
6. Usage Record cannot exist without Billing Ledger finalization.
7. Billing Ledger finalization cannot happen without an Audit Event.
8. Retries across Provider Accounts cannot multiply charge.
9. Tenant A can never observe or mutate tenant B's quota state (verified at every lock + read).
10. Inferred usage carries `pending_reconciliation` flag and may be late-reconciled if upstream emits usage out-of-band.

### Test strategy

- **Race tests**: hundreds of concurrent requests against one User+API Key with quota for exactly N reservations; assert exactly N succeed.
- **Replay tests**: same `idempotency_key` with same fingerprint twice; assert second returns cached response, no double mutation. Same key with conflicting fingerprint; assert Audit Event + conflict error.
- **Crash tests**: kill gateway between every adjacent step pair (after-claim/before-reserve, after-reserve/before-upstream, after-upstream/before-reconcile, after-reconcile/before-cache-outbox); assert recovery converges to either committed or released_orphaned with no leak.
- **Partial-stream tests**: client disconnect mid-stream; upstream disconnect mid-stream; missing usage; retry after partial. All paths assert exactly one Billing Ledger entry per logical request.
- **Provider Account retry tests**: one logical request switches across N Provider Accounts; assert one Billing Ledger entry, N attempt records.
- **Tenant collision tests**: same `logical_request_id` in two tenants; assert no claim collision.
- **Serializable abort retry tests**: PostgreSQL aborts under serialization conflict; assert idempotent retry.
- **Lease tests**: long stream heartbeat-extends; sweep does not falsely orphan.
- **Modality tests**: same algorithm for text, image, audio, embedding. No per-modality shortcut.
- **Trusted-shortcut absence test**: high-balance User triggers no fast-path that bypasses reservation. Assert reservation occurs always.

## Action Plan (Final)

### Immediate (Phase 1 → Phase 2 transition)

1. **Move this synthesis file to `docs/specs/quota-billing-claim-gate.md`** as the Option C strict spec for HUAKAI's quota+billing core, after reviewer-lane sign-off.
2. **Reviewer-lane review of all 3 cross-cutting files** (Claude pass + Codex pass + this synthesis) per [_REVIEW_CHECKLIST.md](../specs/_REVIEW_CHECKLIST.md) CL-001..010 by a fresh agent session.
3. **Update [docs/22 mandate](../../22_DEEP_MINING_MANDATE.md)** Per-Reference Coverage Tracking: this is the first deep cross-cutting decomposition; mark it as the template for future cross-cutting algorithm dives.
4. **Add to [docs/15 release gates](../../15_RELEASE_GATES.md)**: a "Money-Grade Correctness Gate" that requires this spec released + race/crash/replay test suite green before any Phase 6 (billing surfaces) commit.

### Open follow-ups

- Open per-reference decomposition stubs for the remaining quota/billing surfaces flagged here:
  - `decompositions/one-api/quota-mutation-gaps.md` — formalize G1..G8.
  - `decompositions/sub2api/billing-claim-gate.md` — formalize Sub2API's full implementation.
  - `decompositions/sub2api/cost-engine.md` — the multiplier / clamp / pricing logic (E-S2A-DEEP-010).
- Open `docs/sessions/<id>.md` per the contamination ledger gap flagged in [Claude reviews Codex](../../reviews/2026-04-28-claude-reviews-codex-phase1.md) Subject 6.
- Open follow-up to extend the typed failure taxonomy (`docs/decompositions/sub2api/typed-failure-taxonomy.md`) with the `network_pre_response`, `network_mid_stream`, `provider_protocol_violation` categories.

### Phase 2-9 implementation guidance

When implementer-lane work begins on this algorithm:

- The implementer reads only this synthesized spec + [streaming-forwarder.md](../sub2api/streaming-forwarder.md) + [protocol-translation.md](../sub2api/protocol-translation.md). The implementer does NOT read upstream source.
- The implementer's session is recorded in `docs/sessions/<id>.md` with `lane: implementer; references_read: none; spec_consumed: this file`.
- Test suite green is the implementation completion signal, not "code is written".

## Reviewer Sign-Off

| Field | Value |
| --- | --- |
| Reviewer | (pending — must be a different agent session from both Claude and Codex who authored the parallel passes) |
| Review date | (pending) |
| Checks passed | (pending; CL-001..010 must all pass) |
| Notes | First Owner-mandated mutual-review + synthesis cycle ("同样的事情你们都要做，然后互审对方的结果。然后给出最终的优化排版行动方案" 2026-04-28). The product is this synthesized action plan, not the parallel passes alone. |
