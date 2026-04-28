# Provider Account Pool Selection — Claude's Independent Pass

| Field | Value |
| --- | --- |
| Status | Specifier-lane draft (Claude pass, independent) |
| Author | Claude (PM-Orchestrator), specifier lane |
| Date | 2026-04-28 |
| Lane | Specifier — Option C strict spec input per [DR-000](../../decisions/DR-000-clean-room-methodology.md) carve-out for F-POOL-001 |
| Feature | [F-POOL-001](../../03_FEATURE_PARITY_MATRIX.md) (L1 MVP) — three-layer pool selection: continuation → sticky → fresh |
| Mutual review | This file is authored independently of Codex's parallel specifier pass per Owner directive 2026-04-28 ("同样的事情你们都要做"). Codex's parallel pass lives at [pool-selection-codex.md](pool-selection-codex.md). Synthesis follows after both are complete. |
| Becomes | After mutual review + reviewer-lane CL-001..010 sign-off, the synthesized version moves to `docs/specs/pool-routing.md` Status=Released; only then may implementer-lane agents cite it. |
| Source basis | Behavior described in [layered-account-selection.md](../sub2api/layered-account-selection.md), [streaming-forwarder.md](../sub2api/streaming-forwarder.md), and evidence rows E-S2A-DEEP-006/007/009, E-LM-DEEP-005/009/012, E-OAI-DEEP-004 (per [07_REFERENCE_EVIDENCE_LEDGER.md](../../07_REFERENCE_EVIDENCE_LEDGER.md)). |

## 1. Why Pool Selection Is Money-Grade

Pool selection sits between the Quota+Billing claim gate (Tx1 reserved) and the upstream HTTP call. A bad pick produces:

- **Customer-visible breakage**: continuation lost mid-conversation, surprise rate-limit, latency spike from cold Account.
- **Financial leakage**: Account chosen has insufficient balance → upstream returns 402/429 mid-stream → reconcile (Tx2) charges the customer for a partial response while the Account silently drained.
- **Stampede**: top-1 selection on the highest-scored Account creates thundering herd, which the per-Account concurrency cap catches as overflow → cascading wait-queue overflow → request shed.
- **Stale-state failure**: Account looked healthy 30 s ago but is now revoked / disabled / out-of-quota; without revalidation gate, the gateway sends a doomed request and burns one retry budget.

This is L1 MVP because Owner's Model-1 commercial launch sells API access on top of pooled upstream subscriptions. If pool selection mis-routes, the product loses customer trust faster than billing inaccuracy: one bad mid-conversation context loss is a churn event.

## 2. Algorithm — Layered Selection with Revalidation Gate

Selection runs in three layers. Each layer produces a *candidate* Provider Account; every candidate then passes through the same **Revalidation Gate** before being sent to the upstream HTTP call. Failure of the gate at any layer falls through to the next layer; failure of all three layers returns a typed `NO_ELIGIBLE_ACCOUNT` failure to the orchestrator (which may map to client 503 with a `Retry-After` hint).

### 2.1 Layer 1 — Continuation Affinity

**Trigger**: request carries an upstream-issued continuation marker (e.g. provider conversation id / session token) extracted from the client request body or headers.

**Candidate**: the Provider Account that handled the prior turn for that marker, looked up via `(tenant_id, continuation_marker) → provider_account_id` table.

**Why preserved over sticky**: continuation markers are upstream-stateful — switching Accounts mid-conversation loses provider-side context. This is not just preference; it is a correctness property visible to the client.

**TTL**: continuation entries expire on the upstream's documented session lifetime (per-Channel config), default 30 minutes; entries are deleted on natural conversation end signal where the provider exposes one.

### 2.2 Layer 2 — Sticky Session Affinity

**Trigger**: request carries a session id (typically derived from client cookie / `X-Session-Id` header / API Key + IP fingerprint).

**Candidate**: looked up via `(tenant_id, session_id, model) → provider_account_id`.

**Why model is in the key**: a single client session may span multiple models (chat, embedding, vision); each model binds independently because providers can have per-model concurrency caps and per-model balance accounting.

**TTL**: refreshed on every successful selection; default 10 minutes idle expiry.

**Wait-queue limit on sticky path is shorter than on fallback path** (default 2 vs 8). Rationale: sticky's value is preserving conversation context, not surviving Account overload; if sticky's chosen Account is overloaded, fall through fast rather than blocking the request indefinitely.

### 2.3 Layer 3 — Fresh Pooled Selection

**Trigger**: layers 1 and 2 produced no candidate, or both candidates failed the Revalidation Gate.

**Eligibility filter** (cheap, in-memory or index-backed):
1. `enabled = true`, `expires_at > now()`, `tenant_id` matches.
2. Channel allow-list contains this Provider Account.
3. Account's Model allow-list contains the requested model.
4. Account's transport mode supports the request (streaming vs non-streaming).
5. Account's capability flags satisfy the request (tool-use / vision / reasoning effort).
6. User Group routing policy permits this Account.
7. Per-request exclusion list (Accounts already failed during this request's failover loop) does not contain this Account.
8. Current health state is `operational` or `degraded` (not `failed` / `error` / `cooling_down`).

**Scoring** (per remaining candidate):

```
score(account) =
    w_priority    * normalized(operator_priority)
  + w_balance     * normalized(remaining_quota_or_balance)
  + w_latency     * inverse_normalized(p50_first_token_latency_recent)
  + w_error_rate  * inverse_normalized(error_rate_recent)
  + w_freshness   * inverse_normalized(seconds_since_last_dispatch)
  - p_concurrency * (current_in_flight / cap_concurrency)
  - p_queue       * (current_queue_depth / cap_queue)
```

Weights `w_*` and penalties `p_*` are operator-set, surfaced in the admin UI, defaulted explicitly (no opaque defaults), with telemetry showing dispatch distribution per Pool over a sliding window so operators can detect skew.

**Pick**: instead of `argmax(score)`, take the **top-K candidates** (default K=3), then select **uniformly at random** among them. This is Sub2API's randomized top-K selection (E-S2A-DEEP-007) and prevents stampede on the strongest Account.

If only one candidate exists in the eligible set, it is returned without randomization (degenerate case).

If zero candidates: return `NO_ELIGIBLE_ACCOUNT`. Do **not** fall back to silently picking a `failed`-state Account — this is the LiteLLM single-Account exemption ([E-LM-DEEP-005](../../07_REFERENCE_EVIDENCE_LEDGER.md)) which we adopt selectively: exemption applies only when (a) the Pool has exactly one Account, AND (b) the failure was a single transient probe miss, AND (c) operator has set `allow_last_resort = true` for this Pool. Otherwise, an empty eligible set is a hard failure.

### 2.4 Revalidation Gate (applied to every layer's candidate)

```
RevalidateAndAcquire(candidate, request_context) -> Result<Account, GateFailureReason>
  txn = begin_serializable()
  row = SELECT ... FROM provider_account
        WHERE id = candidate AND tenant_id = ctx.tenant_id
        FOR UPDATE
  if row.deleted_at IS NOT NULL OR row.enabled = false: return DISABLED
  if row.expires_at <= now(): return EXPIRED
  if row.health_state IN ('failed', 'cooling_down', 'error'): return UNHEALTHY
  if row.credential_state IN ('revoked', 'refresh_failed'): return CREDENTIAL_INVALID
  if NOT model_allowed(row, ctx.model): return MODEL_NOT_ALLOWED
  if NOT capability_satisfied(row, ctx.caps): return CAPABILITY_MISMATCH
  if row.in_flight_count >= row.cap_concurrency:
      if row.queue_depth >= row.cap_queue_for_layer(ctx.layer): return OVER_CAPACITY
      enqueue_wait(row, ctx); return WAITING (re-enter when slot frees or timeout)
  if row.balance_or_quota < estimate_request_cost(ctx): return INSUFFICIENT_BALANCE
  UPDATE provider_account
     SET in_flight_count = in_flight_count + 1,
         last_dispatch_at = now()
     WHERE id = candidate
  commit(txn)
  return Acquired(row)
```

The gate is **the single chokepoint** through which any Account, regardless of layer, must pass. Failure produces a typed reason that is logged on the Usage Record so operators can see why a sticky was broken or why a continuation degraded.

### 2.5 Slot Release

The acquired slot must be released on every exit path: successful upstream response (release in Tx2 reconcile), upstream error (release with error code), client cancellation, lease timeout, gateway crash (orphan sweep — see §3.4).

Release is idempotent: the slot row carries an `acquisition_token` (UUID) generated at acquisition; release decrements only if the token still matches and the row's `in_flight_count > 0`. Double-release is a no-op.

## 3. Concurrency Invariants

These ten properties must hold under arbitrary interleaving of concurrent requests on the same Pool, the same Account, the same User, and across multiple gateway instances:

| # | Invariant | Enforcement |
|---|-----------|-------------|
| I1 | An Account never has `in_flight_count > cap_concurrency`. | Serializable txn + row lock on `provider_account` during acquire. |
| I2 | The same `(tenant_id, session_id, model)` sticky binding is never split across two Accounts simultaneously. | Sticky lookup is read-only outside Tx1; binding write happens inside the Revalidation Gate's serializable txn. |
| I3 | A request's chosen Account never appears in its own exclusion list. | Exclusion list is per-request; revalidation gate appends on failure. |
| I4 | Continuation marker → Account mapping is consistent across retries within one logical request. | Continuation lookup is keyed on the marker, not on attempt; subsequent attempts that lose the original Account fall to sticky/fresh per §2.4 (mapping not rewritten until Tx2 reconcile confirms upstream success). |
| I5 | Slot release is idempotent and order-independent w.r.t. crash recovery. | Acquisition token + `in_flight_count > 0` precondition. |
| I6 | An Account in `cooling_down` state never receives a fresh dispatch via Layer 3. | Eligibility filter rule 8 + revalidation gate `UNHEALTHY` branch. |
| I7 | Top-K randomization never returns the same pick twice in a single failover loop. | `top-K` set is recomputed from current eligibility (which incorporates the per-request exclusion list) at each layer-3 retry. |
| I8 | Reservation holder (Quota+Billing Tx1 claim) and Pool slot acquirer are atomically linked. | Pool acquire happens inside Tx1 OR Tx1 carries the acquisition token in the claim row; both directions of failure (claim ok / pool fail, or claim fail / pool ok) produce Tx1 rollback that releases both. See §6 integration. |
| I9 | Distributed instances respect global `cap_concurrency`. | Backed by PostgreSQL row lock as authoritative; in-process semaphore is a hint. (Multi-instance case formally requires DR-006 — see §5.2.) |
| I10 | An expired sticky binding never serves a request. | Revalidation Gate's `expires_at` and `health_state` checks are per-attempt; sticky cache is read-through hint, never source-of-truth (mirrors Quota+Billing C5 from synthesis). |

## 4. Typed Failure Taxonomy

Every selection failure emits a typed reason. The taxonomy below maps to a `recovery_policy` enum used by the orchestrator's retry logic:

| Reason | Recovery Policy | Usage Record annotation |
|--------|-----------------|------------------------|
| `DISABLED` | next_layer | `account_disabled_at_revalidation` |
| `EXPIRED` | next_layer | `account_expired_at_revalidation` |
| `UNHEALTHY` | next_layer | `account_unhealthy_<state>` |
| `CREDENTIAL_INVALID` | next_layer + alert_operator | `credential_invalid_<sub_state>` |
| `MODEL_NOT_ALLOWED` | next_layer | `model_not_supported_on_account` |
| `CAPABILITY_MISMATCH` | next_layer | `capability_<flag>_missing` |
| `OVER_CAPACITY` | next_layer (sticky) / wait_with_backoff (fresh) | `over_capacity_<layer>` |
| `INSUFFICIENT_BALANCE` | next_layer + decrement_account_quota_estimate | `insufficient_balance_at_revalidation` |
| `WAITING_TIMEOUT` | next_layer | `wait_queue_timeout` |
| `STICKY_BREAK_<reason>` | next_layer | `sticky_broken_<reason>` (where reason ∈ above set) |
| `CONTINUATION_LOST` | next_layer + record_break | `continuation_account_<reason>` |
| `NO_ELIGIBLE_ACCOUNT` | client_503 + alert_operator | `pool_exhausted` |
| `EXEMPTION_DENIED` | client_503 | `last_resort_exemption_not_configured` |

Each annotation is a **fixed enum string**, not free-form text. This enables: (a) operator dashboards filtering by reason, (b) machine alerts on rate-of-`pool_exhausted`, (c) the [docs/24 reference tracking policy](../../24_REFERENCE_TRACKING_POLICY.md) self-audits to detect when a reason class spikes.

## 5. Distributed Coordination

### 5.1 Personal Edition (single-node)

In-process: `provider_account.in_flight_count` is read from PostgreSQL row + maintained in a process-local cache for fast eligibility filtering. Authoritative count is the locked DB row at acquisition time. Cache TTL ≤ 1s.

### 5.2 SaaS Edition (multi-node, Phase 10+)

Authority remains the locked DB row. Multi-node gateways race for the row lock; PostgreSQL serializes them. Process-local cache becomes stale faster (cache TTL ≤ 250ms or invalidation via NOTIFY/LISTEN channel).

If row-lock contention becomes a bottleneck, the secondary primitive is:
- **Option A**: PostgreSQL advisory lock per `(tenant_id, provider_account_id)` for fast acquisition (still authoritative).
- **Option B**: Redis with Lua atomic decrement script + periodic reconciliation against PostgreSQL.

Per [DR-006](../../decisions/DR-006-database-orm-strategy.md) Constraint 3, in-process semaphores are insufficient for SaaS. Choice between A and B deferred until SaaS Phase 10 architecture decision; both are listed here so the Personal Edition design does not foreclose either.

## 6. Integration with Quota+Billing Claim Gate (F-BILL-001)

Pool selection runs in coordination with [Quota+Billing Tx1 (Reserve)](quota-billing-claim-gate-synthesis.md). Two integration patterns are valid; HUAKAI picks Pattern B for clarity:

**Pattern A (rejected)**: Pool acquire is *inside* Tx1. Lock order extends Quota+Billing's six-row lock order with `provider_account` row. **Problem**: enforces lock order across two domains; if Pool acquire blocks (waiting on `cap_concurrency`), it holds Quota+Billing locks for the wait duration → contention amplification.

**Pattern B (chosen)**: Pool acquire happens **after Tx1 commits** the reservation, but the acquisition token is written back to the claim row in a tiny follow-up update before the upstream HTTP call. Tx1 reservation includes a `provider_account_id IS NULL` placeholder; pool selection updates this field once acquired. If pool acquire fails, Tx1 reservation is rolled back via the orphan sweep (or eagerly via a compensating transaction).

The Quota+Billing synthesis document explicitly says **"Provider Account id is NOT in the idempotency key"**; this is consistent — Pool selection is per-attempt, while the claim is per-logical-request.

## 7. Failure Modes the Algorithm Does NOT Handle

These are explicitly out-of-scope and must be tracked:

| Gap | Why out-of-scope | Remediation track |
|-----|------------------|-------------------|
| G-POOL-1 | Cross-tenant fairness when one tenant monopolizes a Pool | Per-tenant quota lives at the Quota+Billing layer, not Pool layer | Phase 6 fairness telemetry |
| G-POOL-2 | Score weight auto-tuning / A-B feedback | Adds ML/tuning subsystem, not L1 | Phase 11+ optional plugin |
| G-POOL-3 | Pool re-balance during long sticky session as Account quota depletes mid-session | Sticky preserves the binding until natural break; depletion handled by INSUFFICIENT_BALANCE → next_layer | Future: graceful sticky migration with continuation hand-over (not all upstreams support) |
| G-POOL-4 | Geographic / latency-aware routing | Per-Account latency is in score; per-region is not | Phase 9 geo-routing |
| G-POOL-5 | Operator-policy enforcement for "this Account only for these Users" | Currently via User Group → Account ACL; not a per-User rule | Phase 7 fine-grained ACL |

## 8. Test Scenarios (informs AT-POOL-001..)

Behavioral tests, language-agnostic, mapped to [docs/11_ACCEPTANCE_TEST_MATRIX.md](../../11_ACCEPTANCE_TEST_MATRIX.md):

1. **AT-POOL-001 / Layer-1 hit**: continuation marker present → Account A returned; A passes gate; observed routing reason `continuation_hit`.
2. **AT-POOL-002 / Layer-1 miss + sticky hit**: continuation Account A revalidation fails (UNHEALTHY); sticky binding to B exists → B returned; routing reason `continuation_lost_sticky_hit`; Usage Record carries `sticky_break_unhealthy`.
3. **AT-POOL-003 / Layer-1+2 miss + fresh pick**: both fail → fresh Layer 3 chooses among top-K; routing reason `fresh_topk_random`; same input over 1000 trials shows distribution within ±15% of uniform across the K.
4. **AT-POOL-004 / Stampede prevention**: 1000 concurrent requests for same model on a Pool of 5 healthy Accounts where Account A has highest score → distribution is **not** all on A; max share ≤ 50% (top-K randomization assertion).
5. **AT-POOL-005 / Single-Account exemption**: Pool has 1 Account in `failed` state, `allow_last_resort=true`, single transient probe miss → Account is dispatched with `last_resort_exemption_used` annotation; if `allow_last_resort=false` → 503 `pool_exhausted`.
6. **AT-POOL-006 / Slot release on crash**: gateway killed mid-stream → orphan sweep releases slot within configured TTL (default 60 s); released slot picked up by next request.
7. **AT-POOL-007 / Idempotent release**: simulated double-release call decrements only once; `in_flight_count` invariant preserved.
8. **AT-POOL-008 / Sticky shorter wait**: under load, sticky Account at queue depth=cap_queue_sticky → request falls through to fresh in <100 ms wall-clock; fresh Account at same depth → request waits until queue drains or timeout.
9. **AT-POOL-009 / Tenant isolation**: Tenant T1's Pool selection never picks T2's Provider Accounts; verified by injecting T2 Account ids into T1's eligibility filter and confirming rejection.
10. **AT-POOL-010 / Routing reason completeness**: every Usage Record produced under selection failure carries a non-empty `routing_reason` annotation drawn from §4 enum; no free-form text.

## 9. Reference Gap Closures (this design vs upstreams)

| Gap | Reference | This design's closure |
|-----|-----------|------------------------|
| G-REF-1 | one-api: Account selection happens before quota check (E-OAI-DEEP-004); doomed-request retry burns retry budget | Revalidation Gate runs *after* Quota+Billing Tx1 reservation; pool acquire fails fast on insufficient balance from the gate, not from upstream 402. |
| G-REF-2 | Sub2API: top-1 score-based routing under heavy load can stampede (E-S2A-DEEP-007 hints at randomization existing) | Top-K randomization explicit, K operator-tunable, default K=3. |
| G-REF-3 | LiteLLM: cooldown can starve last Account (E-LM-DEEP-005) | Single-Account exemption opt-in per Pool, with explicit operator policy flag. |
| G-REF-4 | one-api: no typed routing reason taxonomy → operator cannot see why a sticky broke | §4 typed taxonomy, fixed enum, written to Usage Record. |
| G-REF-5 | LiteLLM: in-process concurrency only (E-LM-DEEP-012) | DR-006 row-lock primary, advisory lock / Redis as scaling option. |
| G-REF-6 | New API (AGPL, behavioral observation only): no documented sticky-break attribution | §4 `sticky_break_<reason>` enums propagate to Usage Record. |

## 10. Attribution

- Source basis: behavior synthesized from existing prose decompositions ([layered-account-selection.md](../sub2api/layered-account-selection.md), [streaming-forwarder.md](../sub2api/streaming-forwarder.md)), evidence rows under [07_REFERENCE_EVIDENCE_LEDGER.md](../../07_REFERENCE_EVIDENCE_LEDGER.md) (E-S2A-DEEP-006/007/009, E-LM-DEEP-005/009/012, E-OAI-DEEP-004), and Quota+Billing synthesis ([quota-billing-claim-gate-synthesis.md](quota-billing-claim-gate-synthesis.md)).
- No upstream function name, struct field, file path, or distinctive identifier appears here. All algorithmic structure is described in HUAKAI domain language.
- This pass was authored without reading Codex's parallel pass; mutual review and synthesis follow.
- Reviewer-lane sign-off (CL-001..010 per [_REVIEW_CHECKLIST.md](../../specs/_REVIEW_CHECKLIST.md)) by a fresh agent session is required before any implementer-lane work may cite this design.
