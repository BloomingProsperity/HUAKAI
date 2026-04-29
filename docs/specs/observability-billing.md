# F-OBS-001: Observability + Atomic Billing Settlement

| Field | Value |
| --- | --- |
| Status | Released |
| Feature ID | F-OBS-001 (with corrected F-BILL-001 framing) |
| Specifier | Claude (PM-Orchestrator), 2026-04-28 |
| Specifier date | 2026-04-28 |
| Reviewer | Codex final reviewer-lane, 2026-04-28 (APPROVE-WITH-FIXES; 10 fixes applied this revision) |
| Review date | 2026-04-28 |
| Released date | 2026-04-28 |
| Lane mode | Option C |
| Supersedes | — |
| Superseded by | — |

## Sources

> Reference material consulted by specifier-lane only. Implementer lane MUST NOT open these.

- Sub2API — LGPL-3.0 ([E-LIC-001](../07_REFERENCE_EVIDENCE_LEDGER.md), commit `b0a2252ed19c3720e6adafde6083e64fbac2efa9`)
- Helicone — GPL-3.0-or-later ([E-LIC-007](../07_REFERENCE_EVIDENCE_LEDGER.md), commit `548832f8e763a33732ead27d8b2dcaeccc665a39`, behavior-only)
- Specifier backing artifacts: [observability-synthesis.md](../decompositions/_cross-cutting/observability-synthesis.md), [observability-source-verified.md](../decompositions/sub2api/observability-source-verified.md), [helicone observability cross-verify](../decompositions/helicone/observability-source-verified.md)

## Capability

This spec satisfies F-OBS-001 from [03_FEATURE_PARITY_MATRIX.md](../03_FEATURE_PARITY_MATRIX.md) with the corrected F-BILL-001 framing: durable money-grade billing settlement + observability layer that is **end-to-end durable** (no lossy worker-pool tier upstream of settlement), with append-only Usage Records and transactional outbox for cross-process invalidation.

## Actor

- **System** (Gateway): runs Tx1 and Tx2; produces Usage Records and Audit Events.
- **System** (Background reconciliation worker): appends adjustment rows when authoritative usage arrives late.
- **Operator**: queries the analytics layer; receives DLQ + outbox-lag alerts.
- **External Provider**: source of upstream usage data.

## Preconditions

1. Tenant context established; `tenant_id` propagated through every internal call.
2. User authenticated; API Key resolved.
3. Selected Provider Account from F-POOL-001; acquisition token present.
4. Predicted cost computed by billing policy version `N`.
5. PostgreSQL tables locked at field level per `docs/schema/observability-billing.sql` (Phase 2.1, follows this Released spec).

## Normal Path

The settlement model has **two transactions** (Tx1 reserves, Tx2 reconciles). The pipeline must be end-to-end durable: NO bounded worker pool may sit between a successful upstream response and Tx2 commit; otherwise overflow drops financial settlement.

### Tx1 — Reserve Transaction (before upstream call)

1. Compute `idempotency_key = HASH(tenant_id, api_key_id, logical_request_id, endpoint_family, normalized_payload_hash, requested_model, pooling_group_id, billing_policy_version, request_class)`.
2. Acquire row locks in fixed order (alphabetical by entity-id pair): Billing Ledger claim → User → API Key → Subscription → Provider Account quota → rate-window rows.
3. Look up or insert Billing Ledger claim by idempotency_key:
   - If existing claim with same fingerprint AND status `committed`: return cached prior response immediately. Skip upstream call.
   - If existing claim with different fingerprint: return `FINGERPRINT_CONFLICT` (409).
   - Else: insert claim row with status `reserving`, predicted_cost, attempt_seq=1.
4. Reserve quota across User / API Key / Subscription / Provider Account / rate-window (5 dimensions).
5. Commit Tx1.
6. Pool slot acquisition follows in F-POOL-001 Phase C (Pattern B placeholder writeback).

### Upstream Call

7. Gateway sends request to Provider Account; receives response (or stream).

### Tx2 — Reconcile Transaction (after upstream resolution)

8. Acquire same row locks in same fixed order.
9. Re-fetch claim row; verify status still `reserving` and acquisition_token matches.
10. Apply final cost atomically:
    - Subscription rolling-window usage (daily / weekly / monthly).
    - User balance deduction (RETURNING new_balance for cache update + low-balance signal).
    - API Key quota counter increment + status flip if exhausted (single-row atomic update).
    - API Key rate-limit windows (5h / 1d / 7d) with auto-rollover on expiry.
    - Provider Account quota multi-window (total / daily / weekly).
11. Cross-threshold detection: when any limit dimension crosses from "not exceeded" to "exceeded" by this increment, enqueue scheduler outbox row (cache invalidation message) **inside this transaction**.
12. Write final Usage Record into this transaction:
    - tenant_id, api_key_id, account_id, claim_id, acquisition_token, attempt_seq.
    - tokens_input, tokens_output, cache_creation_tokens, cache_read_tokens.
    - actual_cost (numeric(20, 8)).
    - usage_source ∈ { reported, normalized, inferred, partial, ambiguous }.
    - confidence_score when usage_source is `inferred`.
    - pending_reconciliation flag when usage_source ∈ { inferred, partial }.
    - routing_reason structured payload (per F-POOL-001 spec §Audit / Usage / Log Evidence).
    - end_class enum from F-GW-002 streaming taxonomy.
13. Write audit-grade billing event row in this transaction (durable, even if Usage Record async cleanup later fails).
14. Decrement Provider Account in_flight_count atomically (idempotent: only if acquisition_token matches AND in_flight_count > 0).
15. Move claim status from `reserving` to `committed` (or `aborted` on terminal upstream failure).
16. Commit Tx2.

### Async Reconciliation (out-of-band)

17. Background reconciliation worker periodically scans Usage Records with `pending_reconciliation = true`.
18. When upstream produces an authoritative usage report (e.g. provider's billing API), worker:
    - Appends a reconciliation event row.
    - Appends a paired Billing Ledger adjustment row (positive or negative) linked by `original_usage_record_id` to the immutable original.
    - Original Usage Record is NEVER mutated.
    - Original committed claim is NEVER mutated.

## Failure Path

### Failure: `TX1_QUOTA_EXHAUSTED`
- Trigger: User / API Key / Subscription / Provider Account quota predicts insufficient at Tx1.
- Observable outcome: client receives 402 Payment Required.
- Operator-visible signal: typed annotation on attempted Usage Record (status `aborted`, reason `quota_exhausted`).

### Failure: `TX1_CLAIM_RACE`
- Trigger: Concurrent attempt won the idempotency claim.
- Observable outcome: gateway re-reads the settled response and returns it (Tx1 retry within bounded budget).
- Operator-visible signal: counter increment on `claim_race_lost`.

### Failure: `TX1_FINGERPRINT_CONFLICT`
- Trigger: Same logical_request_id with different normalized_payload_hash.
- Observable outcome: client receives 409 Conflict; no charge.
- Operator-visible signal: security-grade Audit Event (possible replay attack signal).

### Failure: `TX1_LOCK_TIMEOUT`
- Trigger: Could not acquire row lock within wait budget.
- Observable outcome: client receives 503 Service Busy + Retry-After.
- Operator-visible signal: lock-wait latency metric.

### Failure: `UPSTREAM_FAIL_AFTER_TX1`
- Trigger: Upstream returns terminal error (4xx not retryable, or all retries exhausted).
- Observable outcome: Tx2 aborts the claim; reservation rolled back; no charge to customer.
- Operator-visible signal: Audit Event with end_class.

### Failure: `TX2_LOCK_TIMEOUT` / `TX2_RACE_LOST`
- Trigger: Tx2 cannot acquire locks OR concurrent reconcile won.
- Observable outcome: orphan sweep recovers (status `reserving` + age > sweep_threshold).
- Operator-visible signal: orphan-sweep counter.

### Failure: `USAGE_RECORD_WRITE_FAIL`
- Trigger: Usage Record sync fallback fails (exhausted retries or persistent storage error).
- Observable outcome: Audit-grade billing event row already committed in Tx2 → audit trail preserved; Usage Record entry persisted to durable DLQ table for later replay.
- Operator-visible signal: DLQ depth metric + alert when DLQ persists > N minutes.

### Failure: `INFERRED_RECONCILIATION_PENDING`
- Trigger: Stream ended without explicit usage frame; tokenizer-based estimate used.
- Observable outcome: Usage Record committed with `pending_reconciliation = true`.
- Operator-visible signal: dashboard widget for unreconciled records.

### Failure: `OUTBOX_CONSUMER_LAG`
- Trigger: scheduler outbox consumer dead OR backed up.
- Observable outcome: cache stays stale; subsequent Pool selection may dispatch to over-quota Account.
- Operator-visible signal: lag metric; alert when row age exceeds configured threshold (default 60s, operator-tunable).

### Failure: `BILLING_PIPELINE_DROP`
- Trigger: settlement task overflow if any bounded worker pool is placed between successful upstream and Tx2.
- HUAKAI prevention: settlement queue overflow MUST sync-fallback OR reserve-before-upstream OR fail closed. **No successful upstream response may bypass durable settlement/audit.** This is the architectural rule that prevents the Sub2API worker-pool gap from being inherited.
- Operator-visible signal: pipeline-drop counter MUST be permanently zero in healthy operation; any non-zero triggers immediate alert + automatic review.

## Operator Recovery

| Failure | Detection | Recovery |
|---|---|---|
| TX1_QUOTA_EXHAUSTED | normal customer 402 | Customer top-ups; quota auto-resets per policy. |
| TX1_LOCK_TIMEOUT (high rate) | latency dashboard | Operator scales DB; investigates contention source. |
| UPSTREAM_FAIL_AFTER_TX1 | Audit Event end_class | Standard customer support flow. |
| TX2_RACE_LOST (high rate) | orphan-sweep counter | Investigate concurrent re-attempt patterns. |
| USAGE_RECORD_WRITE_FAIL | DLQ depth + alert | Operator reviews DLQ; manual replay via admin API; storage capacity check. |
| OUTBOX_CONSUMER_LAG | lag alert | Operator restarts consumer; verify cache layer health; outbox backlog drains. |
| INFERRED_RECONCILIATION_PENDING | dashboard widget | Reconciliation worker auto-handles when authoritative report arrives. Operator may force-reconcile via admin API for stuck records. |
| BILLING_PIPELINE_DROP | non-zero counter alert | **CRITICAL** — engineer pages immediately; pipeline architecture has unintended bounded queue. |

## Audit / Usage / Log Evidence

Every Tx2 commit produces three durable artifacts in the same transaction:

1. **Billing Ledger Entry** (immutable post-commit): claim_id, status (committed | aborted), final_cost, settlement_at, billing_policy_version. Per [docs/19_DOMAIN_MODEL.md §Invariant 4](../19_DOMAIN_MODEL.md), corrections happen via paired adjustment rows, never by mutating an existing entry.
2. **Audit-grade Billing Event** (immutable): security-grade event with full request fingerprint and final disposition. Survives Usage Record async failure; serves as fallback audit trail.
3. **Usage Record** (immutable): rich analytics fields (tokens, cost, routing_reason, end_class, usage_source). Hot store retention default 90 days per tenant.

Reconciliation appends rows; never mutates existing ones.

`Storage tier split`:
- **Hot store** (PostgreSQL): claim, billing event, usage_record metadata, scheduler_outbox.
- **Cold store** (S3 or equivalent): raw request/response bodies. Per-tenant retention default 7 days.

## Acceptance Test Direction

Per [11_ACCEPTANCE_TEST_MATRIX.md](../11_ACCEPTANCE_TEST_MATRIX.md), tests AT-OBS-001..022:

Sub2API-inheritable scenarios:
- AT-OBS-001 / Idempotent replay (same fingerprint): no double charge.
- AT-OBS-002 / Replay attack (different fingerprint): 409 Conflict.
- AT-OBS-003 / Archive replay: settled-claim cache hit, no double charge.
- AT-OBS-004 / Atomic 5-effect: all-or-nothing on Tx2.
- AT-OBS-005 / Quota status flip: API Key crosses quota → status atomic with increment.
- AT-OBS-006 / Multi-window auto-rollover: 5h window expired → counter resets.
- AT-OBS-007 / Cross-threshold outbox row in same tx as quota increment.
- AT-OBS-008 / Detached billing context survives request cancellation through Tx2.
- AT-OBS-009 / Best-effort drop with sync fallback: Usage Record queue overflow path.
- AT-OBS-010 / LRU dedup: same Usage Record submitted twice within 5s.

HUAKAI-design scenarios:
- AT-OBS-011 / Pre-call Tx1 rollback on crash before upstream: no claim row, no charge.
- AT-OBS-012 / Tx1 commit + Tx2 missing: orphan sweep recovers.
- AT-OBS-013 / Tenant isolation: T1's request_id A and T2's request_id A do NOT collide.
- AT-OBS-014 / Money precision: 1M × $0.0000001 = exactly $0.10 (decimal end-to-end).
- AT-OBS-015 / Outbox lag alert: consumer dead → row > 60s → alert fires.
- AT-OBS-016 / Pending reconciliation: late authoritative usage appends adjustment row.
- AT-OBS-017 / Audit-grade billing event: Usage Record async fails → billing_event still serves audit query.
- AT-OBS-018 / DLQ persistence: Usage Record write fails AND sync fallback fails → DLQ row; replay succeeds.
- AT-OBS-019 / Hot-vs-cold split: hot store = metadata, cold store = body, per-tenant retention different.
- **AT-OBS-020 / Billing submission cannot be dropped**: settlement queue overflow must sync-fallback, reserve-before-upstream, or fail closed; no successful upstream response may bypass durable settlement/audit.
- **AT-OBS-021 / Reconciliation is append-only**: late authoritative usage appends adjustment rows; original Usage Record unchanged.
- **AT-OBS-022 / Outbox lag alert**: source-backed lag warning/rebuild present at scheduler-snapshot layer; HUAKAI metric + operator alert fire at configured threshold.

## Open Questions

None remaining at release. All four prior open questions resolved during Codex final review 2026-04-28; resolutions in [observability-synthesis.md §11](../decompositions/_cross-cutting/observability-synthesis.md).

## Implementer Notes (added by implementer lane)

> Filled by implementer after consuming the spec.

(empty until implementer-lane work begins)
