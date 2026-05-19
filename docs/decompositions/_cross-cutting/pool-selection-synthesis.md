# Provider Account Pool Selection — Synthesis & Final Action Plan

> ⚠️ **SUPERSEDED — DO NOT USE** (2026-04-28). Codex reviewer REJECTED this synthesis (per `docs/process/reviews/2026-04-28-codex-reviewer-cycle1-cycle2-cl011.md` §4 verdict matrix). Use [pool-selection-synthesis-v2.md](pool-selection-synthesis-v2.md) regenerated from source-verified inputs. Q1..Q4 PM decisions remain valid in v2.

| Field | Value |
| --- | --- |
| Status | Action Plan (Draft, v1 — partial correction needed; see banner) |
| Author | Claude (PM-Orchestrator), synthesizing Claude's pass + Codex's pass |
| Date | 2026-04-28 |
| Mutual review | [Claude pass](pool-selection-claude.md) and [Codex pass](pool-selection-codex.md) authored independently in parallel sessions per Owner directive 2026-04-28 ("同样的事情你们都要做，然后互审对方的结果。然后给出最终的优化排版行动方案"). This file is the synthesized final action plan. |
| Becomes | The Option C strict spec for HUAKAI's Pool Selection. After reviewer-lane sign-off (CL-001..010 per [_REVIEW_CHECKLIST.md](../../specs/_REVIEW_CHECKLIST.md)), file moves to `docs/specs/pool-routing.md` and Status becomes `Released`. |
| Companion | [quota-billing-claim-gate-synthesis.md](quota-billing-claim-gate-synthesis.md) — Pool selection integrates with Quota+Billing Tx1/Tx2 per §6 below. |

## Convergence (Both Passes Agree)

These are load-bearing facts both Claude and Codex extracted independently. Treating as established truth:

1. **Three-layer selection structure** — continuation affinity → sticky session affinity → fresh pooled selection — is the correct topology; HUAKAI inherits behavior, not source.
2. **Revalidation Gate at every layer** — affinity is a hint, never authority. Stale continuation / sticky / cache snapshot must be rechecked against authoritative state before the upstream HTTP call is made.
3. **Per-request exclusion list** — once an Account fails inside this request's failover loop, it is excluded for the remainder of this logical request's retries.
4. **Strong-candidate band + randomization** — `argmax(score)` produces stampedes; HUAKAI picks uniformly at random within a top-K band.
5. **Score signals**: operator priority, current concurrency load, wait queue depth, recent error rate, recent first-token latency, health probe state, Account quota / balance headroom. Cache supplies signal values; cache is never authority for spend or admission.
6. **Tenant isolation** — `tenant_id` in every key, every lock, every cache key, every Audit Event, every routing diagnostic.
7. **Single Billing Ledger claim across Provider Account retry switches** — when failover swaps Account, the new attempt links to the *same* logical claim. Provider Account id is NEVER part of the idempotency key (consistent with Quota+Billing synthesis C3).
8. **`routing_reason` is a mandatory structured field on Usage Record** — no free-form text; fixed enum + structured payload.
9. **No fast-path / no silent downgrade** — affinity layers cannot skip revalidation; capability changes (exact-native vs safe-equivalent) must be a declared Route policy outcome, not silent.
10. **Cache is read-through hint, never source-of-truth** for admission. Authoritative state is the locked DB row at acquisition time.

## Where Codex Sharpens Claude

Reading Codex's pass after writing my own surfaced 7 sharpenings I should adopt:

- **C1 — Verified commit hashes pinned**: Codex pins Sub2API at `b0a2252ed19c3720e6adafde6083e64fbac2efa9` and one-api at `8df4a2670b98266bd287c698243fff327d9748cf`, both verified 2026-04-28. Claude's pass cited evidence rows but not commits. Synthesis must inherit Codex's commit pinning for [docs/24 reference tracking policy](../../24_REFERENCE_TRACKING_POLICY.md) reproducibility.
- **C2 — Capability shift / safe-equivalent as declared Route policy**: Sub2API has a capability-shift pattern (capability not natively supported by chosen Account → fall back to safe equivalent). Claude's pass treated `CAPABILITY_MISMATCH` as a hard `next_layer` failure. HUAKAI must adopt safe-equivalent fallback **as an explicit Route policy outcome with `routing_reason = capability_shift_<from>_<to>`**, not silent. Three policy modes: `exact_capability_only`, `safe_equivalent_allowed`, `reject`.
- **C3 — Forced/administrative override (break-glass routing)**: one-api has a forced Channel pattern (operator override bypasses normal selection). Claude's pass missed this entirely. HUAKAI needs operator break-glass that **still flows through Quota+Billing reservation** and is **logged with override actor identity** in Audit Event. Override does NOT bypass Tx1/Tx2; it only short-circuits eligibility filter + scoring.
- **C4 — Wait queue is a leased intent, not a final route**: Codex articulates that a queued request, when its wait ends, must **re-enter admission** and re-validate against current authoritative state. Claude's pass had a `WAITING` enum but did not make explicit that resumed waiters re-execute the gate from scratch. Stale-state risk during wait is real (Account can become disabled / over-quota / unhealthy while request was queued).
- **C5 — Open questions for Owner**: Codex surfaces 4 platform-policy questions HUAKAI must resolve before spec Released:
  1. Forced routing visibility: tenant-operator-visible or platform-admin-only?
  2. Sticky wait budget scope: per-Route or per-Pooling-Group?
  3. Provider Account quota reserve table: same family as User/API Key, or separate Provider Account capacity ledger?
  4. Capability safe-equivalent default: opt-in or opt-out per Route?
  Synthesis preserves these as §10 below; Owner answers required before reviewer-lane sign-off.
- **C6 — `routing_reason` structured contents are richer than enum strings**: Codex enumerates the structured payload — selection layer, affinity key class (without secret material), sticky break reason, capability outcome, candidate counts by exclusion category, selected Route/Channel/Pooling Group, selected Provider Account identity, scoring policy version, signal contributions map, wait action taken, retry attempt number, per-request exclusion summary, Quota reservation identity, Billing Ledger claim identity, override actor when forced routing used. Must NOT include provider credentials, API Key secrets, raw prompts, or provider response bodies. Claude's pass had enum strings but not the structured payload. Synthesis adopts Codex's full payload spec.
- **C7 — Additional score signals**: `capability_confidence`, `fairness_debt` (long-run dispatch balance), `snapshot_freshness`. Claude's score formula had latency / error / queue / concurrency / freshness / priority / balance but missed `capability_confidence` (how reliably this Account serves the requested capability) and `fairness_debt` (running debt against fair distribution). Both are weight-able; both default to weight 0 and are tunable upward per Pool.

## Where Claude Sharpens Codex

These are Claude's pass contributions Codex's pass did not articulate:

- **L1 — Concrete 10 invariants table with enforcement mechanism** (I1..I10). Codex describes "race-window closures" prosaically; Claude's I1..I10 table is the auditable artifact: each invariant pairs with the specific primitive (serializable txn / row lock / per-request exclusion list / acquisition token) that enforces it. Synthesis adopts Claude's table verbatim.
- **L2 — 13-row typed failure taxonomy with `recovery_policy` enum**. Each failure reason maps to a recovery action (`next_layer`, `wait_with_backoff`, `client_503`, `next_layer + alert_operator`, etc.) and a Usage Record annotation enum string. Codex describes diagnostic categories abstractly; Claude's table is implementation-ready. Synthesis adopts.
- **L3 — Top-K randomization with explicit default K=3** and operator-tunable. Codex says "strong-candidate band" without a concrete K; Claude pins default. Synthesis adopts default K=3, operator-tunable per Pool, lower bound 1 (degenerate single candidate), upper bound `min(eligible_count, 10)`.
- **L4 — Acquisition token (UUID per acquire) for idempotent slot release**. Codex says "deterministic release on reconcile"; Claude's token mechanism makes double-release a verified no-op (release decrements only if token still matches AND `in_flight_count > 0`). Synthesis adopts the token.
- **L5 — Single-Account exemption opt-in flag (`allow_last_resort`)** with three guards: Pool has exactly one Account, failure was a single transient probe miss, operator has set the flag. Without flag, empty eligible set is hard `pool_exhausted` failure (no silent dispatch to `failed`-state Account). Synthesis adopts.
- **L6 — Test scenarios AT-POOL-001..010** mapped to [docs/11_ACCEPTANCE_TEST_MATRIX.md](../../11_ACCEPTANCE_TEST_MATRIX.md). Codex describes test categories abstractly; Claude's 10 numbered scenarios are implementation-ready. Synthesis adopts; Codex's additional categories (capability safe-equivalent path, forced-route audit, queue-then-reenter staleness) added as AT-POOL-011..013.
- **L7 — Pattern A vs Pattern B integration with Quota+Billing**. See §6 below — this is the one real disagreement, resolved in synthesis.

## The One Real Disagreement — Resolved

**Disagreement DIV-1**: How does Pool-slot acquisition integrate with Quota+Billing Tx1?

- **Claude (Pattern B, chosen)**: Pool acquire happens AFTER Tx1 commits the reservation. Tx1 reservation row carries `provider_account_id IS NULL` placeholder + an `acquisition_pending_token`. Pool selection runs, acquires slot, then writes the `provider_account_id` and `acquisition_token` back into the claim row in a tiny follow-up update. If Pool acquire fails, an eager compensating transaction rolls back Tx1 reservation; if compensation also fails, the orphan sweep handles cleanup.
- **Codex (Pattern A implied)**: Pool concurrency lease is acquired *inside* the same reservation boundary as Quota+Billing.

**Resolution: Pattern B is adopted.** Reasons:

1. **Lock amplification**: Pattern A holds Quota+Billing's six-row lock order across the duration of any wait queue. If `cap_concurrency` is hit and the request enters `WAITING`, it would hold Billing Ledger / User / API Key / Subscription / Provider Account / rate-window locks until slot frees. Wait can be seconds. This serializes unrelated requests on the same User / Pool.
2. **Lock-order extensibility**: Pattern A requires extending the documented six-row lock order with `provider_account` and any future per-Pool primitives. Pattern B keeps Quota+Billing's lock order frozen at six rows; Pool selection has its own single-row lock on `provider_account` only.
3. **Failure mode clarity**: In Pattern B, two distinct failure modes — quota-fail (Tx1 abort) vs pool-fail (eager compensation of Tx1) — produce two distinct typed reasons in `routing_reason`. In Pattern A, the failures are entangled.
4. **Pattern B does not weaken atomicity**: the eager compensation path is bounded (one extra serializable txn), and if compensation crashes mid-flight, the orphan sweep + lease expiry close the loop. Quota leak is bounded by `lease_ttl + sweep_interval`.

Pattern B is consistent with the Quota+Billing synthesis statement that "Provider Account id is NOT in the idempotency key" — Pool selection IS per-attempt, not per-claim, by construction.

**Caveat documented in spec**: Pattern B introduces a small window where Tx1 has committed but Pool acquire has not yet written the `provider_account_id`. If gateway crashes in this window, the orphan sweep MUST find these rows by `provider_account_id IS NULL AND created_at < now() - sweep_threshold` AND release them to free the reservation. This is one additional sweeper rule beyond what Quota+Billing synthesis specifies; spec must add it.

## The Synthesized HUAKAI Algorithm — Final

### Atomic primitives

- **PostgreSQL serializable transaction** with **row-level lock** on `provider_account` row during Revalidation Gate.
- **Per-acquisition token (UUID)** stored in slot acquisition record + claim row. Release is idempotent: decrement only if token matches and `in_flight_count > 0`.
- **Lease + heartbeat** for long-running streams: stream-active heartbeat extends lease TTL; orphan sweep ignores leased records with active heartbeat (mirrors Quota+Billing C2 lease semantics).
- **Cache is hint only**: read-through cache for eligibility ranking; authoritative state is the locked DB row.
- **Advisory locks**: only for orphan sweep coordination across multi-instance deployments.

### Selection algorithm — four phases (Codex topology, Claude detail)

**Phase A — Candidate Intent.** Resolve `tenant_id`, User, API Key, Route, Channel, Pooling Group, requested model, endpoint family, capability requirement, session key, continuation marker, stable logical request id, attempt number, retry exclusion set. Apply hard gates from cache/snapshots:
1. Tenant match (`tenant_id` equality).
2. Lifecycle (`enabled = true`, `expires_at > now()`, `deleted_at IS NULL`).
3. Channel allow-list contains Account.
4. Account model allow-list contains requested model OR safe-equivalent permitted by Route policy.
5. Account capability satisfies request OR safe-equivalent permitted by Route policy.
6. Credential state ∈ {`valid`, `refreshing_with_grace`}.
7. Health state ∈ {`operational`, `degraded`}.
8. User Group routing policy permits.
9. Per-request exclusion list does not contain Account.
10. Forced-override: if forced Channel set by authorized operator AND override audited, eligibility filter is bypassed for this attempt only — phase B and C still run.

Outputs: provisional candidate set with provisional state snapshot. All values are advisory until Phase C.

**Phase B — Score and Strong-Candidate Band.**

```
score(candidate) =
    w_priority             * normalized(operator_priority)
  + w_continuity_affinity  * normalized(affinity_match_strength)
  + w_capability_confidence* normalized(capability_confidence)
  + w_balance              * normalized(remaining_quota_or_balance)
  + w_account_reserve_fit  * normalized(account_quota_reserve_fit_for_request)
  + w_user_reserve_fit     * normalized(user_quota_reserve_fit_for_request)
  + w_latency              * inverse_normalized(p50_first_token_latency_recent)
  + w_error_rate           * inverse_normalized(error_rate_recent)
  + w_freshness            * inverse_normalized(seconds_since_last_dispatch)
  + w_fairness_debt        * normalized(fairness_debt_score)
  + w_snapshot_freshness   * normalized(snapshot_freshness)
  - p_concurrency          * (current_in_flight / cap_concurrency)
  - p_queue                * (current_queue_depth / cap_queue)
```

All `w_*` and `p_*` are operator-set, tenant/Route/Pool-scoped, with bounded ranges, versioned defaults, and admin UI surface. Hard gates from Phase A are NOT represented as negative score — they remove candidates entirely.

**Strong-candidate band**: top-K by score where K is operator-tunable per Pool, default K=3, lower bound 1, upper bound `min(eligible_count, 10)`. Final pick is **uniform random** within the band.

Output: ranked candidate list + signal contributions map + scoring policy version + selected candidate.

**Phase C — Atomic Admission (Revalidation Gate + Pool Acquire).**

For the selected candidate (and, on retry within Phase C, for the next-ranked candidate from the band):

```
RevalidateAndAcquire(candidate, request_context, claim_row) -> Result
  txn = begin_serializable()
  row = SELECT ... FROM provider_account
        WHERE id = candidate AND tenant_id = ctx.tenant_id
        FOR UPDATE
  // re-apply all Phase A hard gates against the locked row
  if ANY hard gate fails: return typed failure (DISABLED / EXPIRED / UNHEALTHY / ...)
  if row.in_flight_count >= row.cap_concurrency:
      if row.queue_depth >= row.cap_queue_for_layer(ctx.layer): return OVER_CAPACITY
      enqueue_wait(row, ctx, lease_ttl)
      commit(txn); return WAITING
  if row.balance_or_quota < estimate_request_cost(ctx): return INSUFFICIENT_BALANCE
  acquisition_token = uuid_v4()
  UPDATE provider_account
     SET in_flight_count = in_flight_count + 1,
         last_dispatch_at = now()
     WHERE id = candidate
  // Pattern B: write back to claim row in same txn
  UPDATE billing_ledger_claim
     SET provider_account_id = candidate,
         acquisition_token = acquisition_token,
         attempt_seq = attempt_seq + 1
     WHERE claim_id = ctx.claim_id AND provider_account_id IS NULL
  if affected_rows = 0: ROLLBACK; return CLAIM_RACE  // another path won race
  commit(txn)
  return Acquired(row, acquisition_token)
```

If `WAITING`, when slot frees or wait timeout: **re-enter Phase C from scratch** against current authoritative state (Codex C4 — wait is a leased intent, not a final route). Resume must re-evaluate hard gates because Account state may have changed during wait.

If failure: append candidate to per-request exclusion list, return to Phase B for next-ranked candidate. If band exhausted, drop to next layer (sticky → fresh). If all three layers exhausted, return `NO_ELIGIBLE_ACCOUNT` to the orchestrator.

**Phase D — Reconcile.**

On upstream success: in the Quota+Billing reconcile transaction (Tx2), release Pool slot atomically with Usage Record finalization:
```
txn = begin_serializable()
SELECT FROM provider_account FOR UPDATE WHERE id = ctx.account_id
if acquisition_token matches AND in_flight_count > 0:
   in_flight_count = in_flight_count - 1
// Usage Record finalization, claim status applied, etc — per Quota+Billing synthesis
commit(txn)
```

On upstream failure (retryable): release slot, mark attempt failed, append to per-request exclusion list, retry by re-entering Phase B with same logical claim id. Single Billing Ledger claim spans all retry attempts.

On upstream failure (terminal): release slot, mark attempt failed, mark claim aborted in Tx2, terminal failure to client.

On gateway crash: orphan sweep finds:
- Slots with no live heartbeat AND lease expired → release.
- Claim rows with `provider_account_id IS NULL AND created_at < now() - sweep_threshold` (the Pattern B compensation gap) → release reservation.

### Failure Taxonomy (Claude L2, expanded with Codex categories)

| Reason | Recovery Policy | Usage Record annotation |
|--------|-----------------|------------------------|
| `DISABLED` | next_layer | `account_disabled_at_revalidation` |
| `EXPIRED` | next_layer | `account_expired_at_revalidation` |
| `UNHEALTHY` | next_layer | `account_unhealthy_<state>` |
| `CREDENTIAL_INVALID` | next_layer + alert_operator | `credential_invalid_<sub_state>` |
| `MODEL_NOT_ALLOWED` | next_layer | `model_not_supported_on_account` |
| `CAPABILITY_MISMATCH` | next_layer (or capability_shift if Route policy allows) | `capability_<flag>_missing` or `capability_shift_<from>_<to>` |
| `OVER_CAPACITY` | next_layer (sticky) / wait_with_backoff (fresh) | `over_capacity_<layer>` |
| `INSUFFICIENT_BALANCE` | next_layer + decrement_account_quota_estimate | `insufficient_balance_at_revalidation` |
| `WAITING_TIMEOUT` | next_layer | `wait_queue_timeout` |
| `STICKY_BREAK_<reason>` | next_layer | `sticky_broken_<reason>` |
| `CONTINUATION_LOST` | next_layer + record_break | `continuation_account_<reason>` |
| `CLAIM_RACE` | retry_phase_b | `claim_race_lost_to_concurrent_attempt` |
| `NO_ELIGIBLE_ACCOUNT` | client_503 + alert_operator | `pool_exhausted` |
| `EXEMPTION_DENIED` | client_503 | `last_resort_exemption_not_configured` |
| `FORCED_ROUTE_UNAUTHORIZED` | client_403 + alert_security | `forced_route_authorization_failed` |

### Concurrency Invariants (Claude L1, augmented)

I1..I10 from Claude pass + I11 from Codex C4:
- I11 — A waiter resumed from queue re-enters Phase C and re-evaluates all hard gates; stale state from before-wait cannot authorize an acquisition.

### `routing_reason` Structured Payload (Codex C6)

Required fields on every Usage Record:
```
routing_reason {
  selection_layer:           one of [continuation, sticky, fresh, forced]
  affinity_key_class:        one of [continuation_marker, session_id, none]
                              // raw key VALUE never stored, only class
  sticky_break_reason:       null OR one of failure-taxonomy enum strings
  capability_outcome:        one of [exact, safe_equivalent, none_required]
  candidate_counts_by_exclusion: {
    tenant_filter: int, lifecycle: int, channel: int, model: int,
    capability: int, credential: int, health: int, group_policy: int,
    per_request_exclusion: int, scored_band: int
  }
  route_id, channel_id, pooling_group_id, provider_account_id
  scoring_policy_version:    semver
  signal_contributions:      map of signal_name -> contribution_value
  wait_action:               null OR { entered_queue_at, exited_queue_at, exit_reason }
  retry_attempt_number:      int
  per_request_exclusion_summary: list of (account_id, reason)
  quota_reservation_id, billing_ledger_claim_id
  forced_route_override_actor: null OR { operator_id, reason, audit_event_id }
}
```
Forbidden contents: provider credentials, API Key secrets, raw prompts, raw provider responses.

## Decisions — Locked Answers to the 4 Open Questions

| # | Question | Decision | Reasoning |
|---|----------|----------|-----------|
| Q1 | Forced-route visibility scope | **Personal Edition: platform-admin only (= Owner). SaaS Edition (Phase 10+): tenant-operator allowed, but bounded by audit trail, rate-limit (default ≤ 5/hour/tenant, operator-tunable up to ≤ 50/hour), and platform-admin notification on every use.** | Forced routing is a break-glass mechanism. In Personal Edition, the Owner is the platform-admin and the tenant-operator simultaneously, so the distinction is moot — keeping it platform-admin-only forces the operator to consciously context-switch into "I am bypassing safety" mode. In SaaS Edition, tenant-operators legitimately need it for stuck conversations / dedicated routing, but the rate limit + notification + audit prevent it from becoming the default routing strategy. |
| Q2 | Sticky wait budget scope | **Per-Route default; per-Pooling-Group override allowed.** | Route is the user-facing contract surface (the API endpoint family the customer hits); Pool is the operator-facing resource pool. Sticky wait is a UX property (does the customer's request feel slow?) so it lives at Route by default. Operators may need Pool-level override when a specific Pool's upstream characteristics warrant tighter or looser bounds. |
| Q3 | Provider Account quota reserve table location | **Separate Provider Account capacity ledger, NOT shared with User / API Key quota family.** | Three orthogonal lifecycles: (a) **billing-cycle-tied** — User / API Key quota is tied to billing periods and top-ups, customer-visible, refund-eligible; (b) **upstream-subscription-tied** — Provider Account capacity is tied to operator's purchased upstream plan, only operator-visible, reconciled against upstream usage data; (c) **mutation-frequency** — User quota changes per request; Provider Account capacity changes per upstream-window-tick. Mixing them would force shared schema constraints, shared audit cadence, and shared lock contention that don't match either domain. |
| Q4 | Capability safe-equivalent default | **Opt-in. Default Route policy is `exact_capability_only`. Operator must explicitly set `safe_equivalent_allowed` per Route.** | Default-deny for capability downgrade matches Owner's "money-grade correctness" identity ([01_PROJECT_BRIEF.md](../../01_PROJECT_BRIEF.md)). Silent downgrade is the failure class that erodes customer trust most — request looks served but quality changed without notice. Opt-in forces an operator action and a `routing_reason.capability_outcome = safe_equivalent` annotation on every dispatched request, making the downgrade auditable. |

These decisions are PM-Orchestrator binding; reviewer-lane CL-001..010 sign-off proceeds against this synthesis as the final spec input. Any change to these answers requires opening a new DR with documented superseding reason.

## Test Scenarios (Claude L6 + Codex categories)

AT-POOL-001..010 from Claude pass, plus:
- AT-POOL-011 / Capability safe-equivalent path: Route policy `safe_equivalent_allowed`, Account lacks exact capability but supports equivalent → dispatched with `routing_reason.capability_outcome = safe_equivalent`.
- AT-POOL-012 / Forced-route audit: forced Channel set by authorized operator → request dispatches to that Channel; Audit Event records operator id + reason; non-authorized forced override → 403.
- AT-POOL-013 / Queue-then-reenter staleness: request enters queue with Account `operational`; during wait, operator disables Account; on wait exit, Phase C revalidation catches `DISABLED`, request falls through to next candidate.
- AT-POOL-014 / Pattern B compensation: simulated crash after Tx1 commit but before Pool acquire writeback → orphan sweep within configured threshold rolls back reservation; quota not leaked.

## Reference Gap Closures

| Gap | Reference | This design's closure |
|-----|-----------|------------------------|
| G-REF-1 | one-api: Account selection before quota check (E-OAI-DEEP-004) | Phase C runs after Quota+Billing Tx1 reservation; Pool acquire fails fast on `INSUFFICIENT_BALANCE` from authoritative gate, not from upstream 402. |
| G-REF-2 | Sub2API: top-1 score-based routing risks stampede (E-S2A-DEEP-007) | Top-K randomization with default K=3, operator-tunable per Pool. |
| G-REF-3 | LiteLLM: cooldown can starve last Account (E-LM-DEEP-005) | Single-Account exemption via per-Pool `allow_last_resort` opt-in flag with three guards. |
| G-REF-4 | one-api: no typed routing reason taxonomy | Failure taxonomy as fixed enum + structured `routing_reason` payload on Usage Record. |
| G-REF-5 | LiteLLM: in-process concurrency only (E-LM-DEEP-012) | DR-006 row-lock primary; advisory lock / Redis as SaaS scaling option. |
| G-REF-6 | one-api: forced Channel override has no audit trail | Forced-route override produces Audit Event with operator id + reason; never bypasses Quota+Billing reservation. |
| G-REF-7 | Sub2API: capability shift implicit | Capability safe-equivalent declared as Route policy with explicit `routing_reason.capability_outcome` enum. |
| G-REF-8 | Both upstreams: queue counter and slot are split primitives | Pattern B with re-enter-on-resume + idempotent acquisition token closes queue/slot race. |

## Provenance

- Claude pass: independent specifier-lane authored by Claude PM-Orchestrator session 2026-04-28, basis = existing decompositions + evidence ledger rows.
- Codex pass: independent specifier-lane via `omc ask codex --agent-prompt critic` (gpt-5.5 + xhigh reasoning) 2026-04-28T07:47 UTC, raw artifact retained at `.omc/artifacts/ask/codex-proceed-begin-implementation-owner-has-issued-the-start-sign-2026-04-28T07-47-11-032Z.md` (gitignored). Codex pinned Sub2API at commit `b0a2252ed19c3720e6adafde6083e64fbac2efa9` and one-api at commit `8df4a2670b98266bd287c698243fff327d9748cf`, both verified 2026-04-28.
- Synthesis: this file, authored by Claude after both passes complete.
- Reviewer-lane sign-off: pending. Must be a different agent session than either specifier; reviewer responsible for [_REVIEW_CHECKLIST.md](../../specs/_REVIEW_CHECKLIST.md) CL-001..010 against this synthesis and the Owner answers to §10 questions, before the spec moves to `docs/specs/pool-routing.md` Status=Released.

## Review Sign-Off

| Field | Value |
| --- | --- |
| Reviewer | (pending — must be fresh agent session) |
| Review date | (pending) |
| Owner answers received | Locked 2026-04-28 (PM-Orchestrator decisions Q1..Q4 in §10) |
| Checks passed | (pending — CL-001..010) |
| Notes | Second mutual-review cycle in HUAKAI's strict-path methodology, after Quota+Billing Cycle 1. Establishes the Pattern A vs Pattern B integration choice that will recur for any future per-attempt boundary integrating with Tx1/Tx2. |
