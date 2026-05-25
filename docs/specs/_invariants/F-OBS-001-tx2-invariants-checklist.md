# F-OBS-001 §Tx2 Invariant Checklist

| Field | Value |
| --- | --- |
| Source | docs/specs/observability-billing.md (Released 2026-04-28) §Tx2 + §Failure Path + §Audit |
| Extracted by | Sonnet Explore agent, 2026-04-29 |
| Use | Acceptance bar for any Tx2 settler implementation; cross-validation reference for Codex-produced settler.go |
| Format | Each invariant: actionable, source-grounded with spec line cite, independent (one fact each) |
| Total | 50 invariants + 13 acceptance-test mappings |

> **Truth-first**: invariants below are extracted verbatim/directly-implied from spec. Anything ambiguous marked "AMBIGUOUS — needs Owner clarification". Sonnet found no ambiguity.

---

## A. Preconditions (entering Tx2)

- **T2-INV-1**: Tenant context established and `tenant_id` propagated through every internal call; spec line 37.
- **T2-INV-2**: User authenticated and API Key resolved; spec line 38.
- **T2-INV-3**: Selected Provider Account from F-POOL-001 with acquisition token present; spec line 39.
- **T2-INV-4**: Predicted cost computed by billing policy version N; spec line 40.
- **T2-INV-5**: PostgreSQL tables locked at field level per schema (Phase 2.1); spec line 41.
- **T2-INV-6**: Tx1 commit succeeded (claim row inserted with status `reserving`, predicted_cost, attempt_seq=1); spec lines 54–56.
- **T2-INV-7**: Upstream call completed and returned (either response received or stream ended); spec line 61.

## B. Tx2 Steps Invariants (steps 8–16)

- **T2-INV-8**: Row locks acquired in fixed alphabetical order (Billing Ledger claim → User → API Key → Subscription → Provider Account quota → rate-window rows); spec line 65 (same order as Tx1, line 50).
- **T2-INV-9**: Claim row re-fetched and status verified as `reserving`; spec line 66.
- **T2-INV-10**: Acquisition token matches the one stored in claim row; spec line 66.
- **T2-INV-11**: Final cost applied atomically to Subscription rolling-window usage (daily / weekly / monthly); spec line 69.
- **T2-INV-12**: Final cost applied atomically to User balance deduction; spec line 69; verification: balance decrements correctly, RETURNING new_balance for cache update + low-balance signal.
- **T2-INV-13**: API Key quota counter incremented and status flipped to exhausted if limit reached (single-row atomic update); spec line 70.
- **T2-INV-14**: API Key rate-limit windows (5h / 1d / 7d) updated with auto-rollover on expiry; spec line 71.
- **T2-INV-15**: Provider Account quota multi-window (total / daily / weekly) updated; spec line 72.
- **T2-INV-16**: Cross-threshold detection: when any limit dimension crosses from "not exceeded" to "exceeded", scheduler outbox row enqueued inside this transaction; spec line 73.
- **T2-INV-17**: Usage Record written in same transaction with: tenant_id, api_key_id, account_id, claim_id, acquisition_token, attempt_seq; spec lines 74–75.
- **T2-INV-18**: Usage Record includes tokens_input, tokens_output, cache_creation_tokens, cache_read_tokens; spec line 76.
- **T2-INV-19**: Usage Record includes actual_cost as numeric(20, 8); spec line 77.
- **T2-INV-20**: Usage Record usage_source ∈ {reported, normalized, inferred, partial, ambiguous}; spec line 78.
- **T2-INV-21**: Usage Record confidence_score written when usage_source is `inferred`; spec line 79.
- **T2-INV-22**: Usage Record pending_reconciliation flag set when usage_source ∈ {inferred, partial}; spec line 80.
- **T2-INV-23**: Usage Record includes routing_reason structured payload (per F-POOL-001 spec §Audit / Usage / Log Evidence); spec line 81.
- **T2-INV-24**: Usage Record includes end_class enum from F-GW-002 streaming taxonomy; spec line 82.
- **T2-INV-25**: Audit-grade billing event row written in same transaction (durable, survives Usage Record async cleanup failure); spec line 83.
- **T2-INV-26**: Billing event includes claim_id, status (committed | aborted), final_cost, settlement_at, billing_policy_version; spec line 166.
- **T2-INV-27**: Provider Account in_flight_count decremented atomically (idempotent: only if acquisition_token matches AND in_flight_count > 0); spec line 84.
- **T2-INV-28**: Claim status moved from `reserving` to `committed` (or `aborted` on terminal upstream failure); spec line 85.
- **T2-INV-29**: Tx2 committed successfully; spec line 86.

## C. Failure Path Invariants

### TX2_LOCK_TIMEOUT
- **T2-INV-30**: If row lock cannot be acquired within wait budget, observable outcome is client receives 503 Service Busy + Retry-After (analogous to TX1_LOCK_TIMEOUT); spec line 117.
- **T2-INV-31**: Orphan sweep counter incremented and orphan-sweep recovers status `reserving` + age > sweep_threshold; spec line 127.

### TX2_RACE_LOST
- **T2-INV-32**: If concurrent reconcile won the race, orphan sweep recovers; spec line 127.
- **T2-INV-33**: Operator-visible signal: orphan-sweep counter; spec line 127.

### USAGE_RECORD_WRITE_FAIL
- **T2-INV-34**: If Usage Record sync fallback fails (exhausted retries or persistent storage error), Audit-grade billing event row already committed in Tx2; spec line 131.
- **T2-INV-35**: Audit trail preserved even if Usage Record entry write fails; spec line 131.
- **T2-INV-36**: Usage Record entry persisted to durable DLQ table for later replay; spec line 131.
- **T2-INV-37**: Operator-visible signal: DLQ depth metric + alert when DLQ persists > N minutes; spec line 132.

### UPSTREAM_FAIL_AFTER_TX1 (affects Tx2 claim status)
- **T2-INV-38**: Upstream returns terminal error (4xx not retryable, or all retries exhausted); spec line 120.
- **T2-INV-39**: Tx2 aborts the claim (claim status set to `aborted`); spec line 85.
- **T2-INV-40**: Reservation rolled back; no charge to customer; spec line 121.
- **T2-INV-41**: Operator-visible signal: Audit Event with end_class; spec line 122.

## D. Audit / Persistence Invariants

- **T2-INV-42**: Every Tx2 commit produces three durable artifacts in the same transaction: Billing Ledger Entry, Audit-grade Billing Event, Usage Record; spec line 164.
- **T2-INV-43**: Billing Ledger Entry is immutable post-commit; corrections via paired adjustment rows, never mutation; spec line 166.
- **T2-INV-44**: Audit-grade Billing Event is immutable with full request fingerprint and final disposition; spec line 167.
- **T2-INV-45**: Usage Record is immutable and rich with analytics fields (tokens, cost, routing_reason, end_class, usage_source); spec line 168.
- **T2-INV-46**: Usage Record hot store retention default 90 days per tenant; spec line 168.
- **T2-INV-47**: Hot store (PostgreSQL) contains: claim, billing event, usage_record metadata, scheduler_outbox; spec line 173.
- **T2-INV-48**: Cold store (S3 or equivalent) contains raw request/response bodies; spec line 174.
- **T2-INV-49**: Per-tenant cold storage retention default 7 days; spec line 174.
- **T2-INV-50**: Reconciliation appends rows; never mutates existing ones; spec line 170.

---

## E. Acceptance Test Mappings

| AT-ID | Verifies which invariants |
|---|---|
| AT-OBS-001 (Idempotent replay) | T2-INV-28 + T2-INV-42–45 |
| AT-OBS-002 (Replay attack 409) | T2-INV-10 (token match check before Tx2 work) |
| AT-OBS-004 (Atomic 5-effect) | T2-INV-11 through T2-INV-28 — the FULL atomicity bar |
| AT-OBS-005 (Quota status flip) | T2-INV-13 |
| AT-OBS-006 (Multi-window rollover) | T2-INV-14 |
| AT-OBS-007 (Cross-threshold outbox same tx) | T2-INV-16 |
| AT-OBS-010 (LRU dedup) | T2-INV-17–24 (Usage Record idempotency) |
| AT-OBS-014 (Money precision) | T2-INV-19 |
| AT-OBS-015 (Outbox lag alert) | T2-INV-16 + spec line 142 |
| AT-OBS-016 (Pending reconciliation) | T2-INV-22 |
| AT-OBS-017 (Audit survives URec async fail) | T2-INV-34–35 |
| AT-OBS-018 (DLQ persistence) | T2-INV-36–37 |
| AT-OBS-020 (Billing submission can't drop) | T2-INV-29 + spec line 146 |
| AT-OBS-021 (Reconciliation append-only) | T2-INV-50 |

---

## How to use this checklist

When reviewing a Tx2 settler implementation:

1. For each T2-INV-N, find the code path that satisfies it OR the test that asserts it.
2. Mark each as: COVERED-IN-CODE / COVERED-BY-TEST / DEFERRED-PHASE-4.5 / NOT-YET / VIOLATED.
3. Phase B.5 v0.1 expected coverage profile (per plan §out-of-scope):
   - **MUST be COVERED-IN-CODE or COVERED-BY-TEST**: T2-INV-9, T2-INV-10, T2-INV-17–24, T2-INV-25–28, T2-INV-29, T2-INV-39
   - **DEFERRED-PHASE-4.5 acceptable**: T2-INV-11–15 (multi-window quota), T2-INV-30–37 (DLQ + lock-timeout + sweep), T2-INV-46–49 (retention + cold store)
   - **MUST be addressed at synthesis later**: T2-INV-16 (cross-threshold outbox; v0.1 has stub callback hook)

If anything in the MUST list is NOT covered → settler implementation is incomplete; do not commit.
