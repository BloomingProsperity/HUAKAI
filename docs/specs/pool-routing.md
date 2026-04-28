# F-POOL-001: Provider Account Pool Selection

| Field | Value |
| --- | --- |
| Status | Released |
| Feature ID | F-POOL-001 |
| Specifier | Claude (PM-Orchestrator) + Codex (independent specifier-lane), 2026-04-28 |
| Specifier date | 2026-04-28 |
| Reviewer | Codex final reviewer-lane, 2026-04-28 (APPROVE-WITH-FIXES; 10 fixes applied this revision) |
| Review date | 2026-04-28 |
| Released date | 2026-04-28 |
| Lane mode | Option C (strict carve-out per [DR-000](../decisions/DR-000-clean-room-methodology.md)) |
| Supersedes | — |
| Superseded by | — |

## Sources

> Reference material consulted by specifier-lane only. Implementer lane MUST NOT open these.

- Sub2API — LGPL-3.0 ([E-LIC-001](../07_REFERENCE_EVIDENCE_LEDGER.md))
- one-api — MIT ([E-LIC-004](../07_REFERENCE_EVIDENCE_LEDGER.md))
- LiteLLM — MIT ([E-LIC-005](../07_REFERENCE_EVIDENCE_LEDGER.md))
- Evidence ledger rows: E-S2A-DEEP-006/007/009, E-OAI-DEEP-001..008 (corrections noted; do NOT cite as unqualified valid range — see Codex one-api re-verify), E-LM-DEEP-005/009/012
- Specifier-lane backing artifacts: `docs/decompositions/_cross-cutting/pool-selection-claude-v2.md`, `docs/decompositions/_cross-cutting/pool-selection-synthesis-v2.md`, `docs/decompositions/litellm/pool-fallback-source-verified.md`, `docs/decompositions/one-api/quota-billing-source-verified.md`, `docs/decompositions/sub2api/observability-source-verified.md`

## Capability

This spec satisfies F-POOL-001 from [03_FEATURE_PARITY_MATRIX.md](../03_FEATURE_PARITY_MATRIX.md): "Layered Provider Account selection within a Pool — preserve session continuity when safe, prefer healthy capable Accounts under load, surface routing reasons to operators, and admit a request only when Quota+Billing claim and concurrency slot are both reservable."

## Actor

- **User**: triggers a request bearing API Key + optional Session affinity hints. Observes selected Provider Account via response headers (`X-Provider-Hint`, when permitted by Pool policy) and via Usage Record `routing_reason`.
- **Operator**: observes selection telemetry, configures Pool routing policy, may invoke break-glass forced routing (per Q1 below).
- **System** (Gateway): executes selection algorithm.
- **External Provider**: receives the dispatched request.

## Preconditions

1. Tenant context established; `tenant_id` present on the request and propagated through every internal call.
2. User authenticated; API Key resolved; User Group resolved.
3. Quota+Billing Reserve Transaction (Tx1) committed for this logical request, with claim row present and `provider_account_id` placeholder unset (per Pattern B integration).
4. Eligible Provider Accounts exist in the Pool for the User Group; Pool not empty after preliminary tenant + lifecycle filter.
5. Operator policy: Pool routing policy version, sticky wait budget, fallback wait budget, optional scoring weights, optional last-healthy exemption flag (`allow_last_resort`), optional broad-K randomization opt-in.

## Normal Path

Selection runs in **four phases**. Each phase produces a candidate or falls through to the next; revalidation is mandatory at every layer.

### Phase A — Candidate Intent

1. Resolve request context: `tenant_id`, User, API Key, Route, Channel, Pooling Group, requested model, endpoint family, capability requirements, session-key, continuation marker, retry exclusion set, attempt number.
2. Apply hard gates from cache-snapshot state (advisory; authoritative re-check happens in Phase C):
   - **Tenant gate**: Account `tenant_id` matches request.
   - **Lifecycle gate**: Account enabled, not expired, not soft-deleted.
   - **Channel gate**: Account in current Channel allow-list.
   - **Model-support gate**: Account supports requested model (or safe-equivalent permitted by Route policy — opt-in only, per Q4).
   - **Capability gate**: Account capability flags satisfy request (or safe-equivalent permitted, opt-in only).
   - **Credential gate**: Account credential state in {valid, refreshing-with-grace}.
   - **Health gate**: Account health state in {operational, degraded}.
   - **Group gate**: User Group routing policy permits this Account.
   - **Exclusion gate**: Account not in this request's per-attempt exclusion list.
3. **Forced override**: if operator break-glass forced routing is set AND audited AND authorized, eligibility filter is bypassed for this attempt only. Phase B and C still run; routing reason carries override actor identity.

### Phase B — Score and Strong-Candidate Band

4. Apply layered selection in order:
   - **Layer 1 — Routing-config affinity**: when Group has model-routing config mapping requested model → Account list, restrict to that intersection of eligible candidates.
   - **Layer 1.5 — Sticky-within-routing**: if sticky binding exists AND bound Account is in the Layer-1 candidate set AND not excluded, prefer it. Subject to Phase C revalidation.
   - **Layer 1.5b — Sticky standalone**: when no Layer-1 routing config, and sticky binding exists AND bound Account is eligible, prefer it. Subject to Phase C revalidation.
   - **Layer 2 — Load-aware fresh selection**: from remaining eligible candidates, select via tier-by-tier filter: minimum priority bucket → minimum load-rate bucket → least-recently-used Account. Random tie handling within the final bucket.
   - **Layer 3 — Fallback queue**: when Layer 2 found candidates but no slot can be acquired, return a wait plan with the configured fallback wait budget.
5. **Top-K policy** (default compatibility mode): K = 1 unless all top candidates fall in the same exact tie group. Operator may opt in to broad Top-K randomization (K > 1 across non-tied candidates) per Pool, which requires explicit policy + verified acceptance test.
6. **Optional score signals** (operator-tunable, default weight 0): recent error rate, recent first-token latency, quota headroom, fairness debt, snapshot freshness. With all weights zero, selection is compatible-mode (matching the lifecycle/load/last-used ordering).
7. Output: ranked candidate list + selected candidate + signal contributions map.

### Phase C — Atomic Admission (Revalidation Gate + Slot Acquire)

8. Open a serializable transaction over the selected Provider Account row (row-level lock).
9. Re-apply all 9 hard gates from Phase A against the locked row.
10. If concurrency cap exhausted:
    - If wait queue depth below configured per-layer max-waiting (sticky path uses the shorter sticky budget; fresh path uses the larger fallback budget): return a wait plan; on resume, re-enter Phase C from scratch.
    - Else: return `OVER_CAPACITY` typed failure; orchestrator falls to next candidate via Phase B.
11. If quota / balance gate predicts insufficient cost reserve: return `INSUFFICIENT_BALANCE`; falls through.
12. Generate UUID acquisition token. Increment in-flight counter. Update last-dispatch timestamp.
13. Write back the acquired Account ID and acquisition token to the Quota+Billing claim row in the same transaction (Pattern B placeholder writeback). If write-back rejects (race lost to a concurrent acquisition): roll back; return `CLAIM_RACE`; retry Phase B.
14. Commit transaction. Return Acquired result.

### Phase D — Reconcile (Tx2 atomicity)

15. On upstream success: in the Quota+Billing Reconcile Transaction (Tx2), atomically:
    - Decrement in-flight counter on the Provider Account (idempotent: only if acquisition token matches).
    - Write final Usage Record carrying full structured `routing_reason` payload (this is HUAKAI's atomicity improvement over Sub2API which writes Usage Record best-effort outside billing transaction).
    - Move Quota+Billing claim row status from `reserving` to `committed`.
    - Update User / API Key / Account quota counters from Usage Record values.
    - Emit Audit Event.
16. On upstream failure (retryable): release slot, mark attempt failed, append Account to per-request exclusion list, retry Phase B with same logical claim id.
17. On upstream failure (terminal): release slot, mark attempt failed, abort claim in Tx2 with zero usage charge; return terminal failure to client.

## Failure Path

### Failure: `DISABLED` / `EXPIRED` / `UNHEALTHY` at Phase C revalidation

- **Trigger**: Phase A cache-snapshot indicated eligible, but Phase C row-locked re-check shows lifecycle / health changed since cache.
- **Observable outcome**: candidate falls through to next-ranked candidate or next layer.
- **Operator-visible signal**: Usage Record `routing_reason.candidate_counts_by_exclusion[lifecycle/health]` increments; if all candidates exhaust → `pool_exhausted` annotation + alert.

### Failure: `MODEL_NOT_ALLOWED` / `CAPABILITY_MISMATCH`

- **Trigger**: requested model or capability not in Account's allow set, AND Route policy is `exact_capability_only` (default).
- **Observable outcome**: candidate falls through.
- **Operator-visible signal**: typed annotation `model_not_supported_on_account` or `capability_<flag>_missing`. If `safe_equivalent_allowed` policy active, dispatch may proceed with `routing_reason.capability_outcome = safe_equivalent_<from>_<to>`.

### Failure: `OVER_CAPACITY`

- **Trigger**: in-flight counter at cap; wait queue full per-layer.
- **Observable outcome**: sticky path → next layer; fresh path → wait or next candidate.
- **Operator-visible signal**: `over_capacity_<layer>` annotation; sticky-break-reason if layer was sticky.

### Failure: `INSUFFICIENT_BALANCE`

- **Trigger**: Account balance / quota predicts insufficient for request cost estimate.
- **Observable outcome**: candidate excluded; falls to next.
- **Operator-visible signal**: `insufficient_balance_at_revalidation`; Account quota estimate decremented.

### Failure: `CLAIM_RACE`

- **Trigger**: Pattern B placeholder writeback rejected because concurrent attempt won the race.
- **Observable outcome**: re-enter Phase B; new candidate selected.
- **Operator-visible signal**: `claim_race_lost_to_concurrent_attempt`.

### Failure: `STICKY_BREAK_<reason>`

- **Trigger**: sticky binding existed but Phase C revalidation failed for one of the typed reasons.
- **Observable outcome**: sticky binding is broken; falls to next layer.
- **Operator-visible signal**: `sticky_broken_<reason>` annotation. Sticky reason enum: `session_limit`, `wait_queue_full`, `gate_check`, `rpm_red`, `account_cleared`.

### Failure: `NO_ELIGIBLE_ACCOUNT`

- **Trigger**: all four layers exhausted; no candidate passes Phase C.
- **Observable outcome**: client receives 503 with `Retry-After` derived from earliest expected Account recovery time.
- **Operator-visible signal**: `pool_exhausted` annotation + operator alert.

### Failure: `EXEMPTION_DENIED`

- **Trigger**: empty eligible set; `allow_last_resort` flag NOT set on this Pool.
- **Observable outcome**: client receives 503.
- **Operator-visible signal**: `last_resort_exemption_not_configured`.

### Failure: `FORCED_ROUTE_UNAUTHORIZED`

- **Trigger**: forced routing requested but actor not authorized (Personal: not platform-admin; SaaS: tenant-operator over rate-limit or unaudited).
- **Observable outcome**: client receives 403.
- **Operator-visible signal**: `forced_route_authorization_failed` + security alert.

## Operator Recovery

| Failure | Detection | Recovery |
|---|---|---|
| `DISABLED` / `EXPIRED` | Account state field + alert | Operator re-enables Account, refreshes credentials, or extends expiry per upstream. |
| `UNHEALTHY` | Health state field + cooldown timer | Auto-clear when cooldown elapses; manual override available via Account admin. |
| `pool_exhausted` | Spike in `pool_exhausted` annotations | Add Provider Accounts; raise per-Account `cap_concurrency`; reduce User Group quota tier. |
| `CLAIM_RACE` (high rate) | Surge in `claim_race_lost_to_concurrent_attempt` | Indicates Pattern B contention; check whether retry-storm is happening upstream of selection; tune per-attempt jitter. |
| `forced_route_authorization_failed` (security alert) | Security audit feed | Verify forced-route configuration; review actor authorization; rate-limit if abuse. |
| `last_resort_exemption_not_configured` | Pool-level alert | If single-Account Pool is intentional, set `allow_last_resort = true`; else add capacity. |

## Audit / Usage / Log Evidence

Every Usage Record carries a structured `routing_reason` payload with these fields (none of these are upstream-derived names; all are HUAKAI domain vocabulary):

```
routing_reason {
  selection_layer            ∈ { routing_affinity, sticky_within_routing, sticky_standalone, fresh, forced }
  affinity_key_class         ∈ { continuation_marker, session_id, none }      // class only, never the key value
  sticky_break_reason        ∈ { null, <STICKY_BREAK_* enum> }
  capability_outcome         ∈ { exact, safe_equivalent, none_required }
  candidate_counts_by_exclusion {
    tenant_filter, lifecycle, channel, model, capability, credential, health,
    group_policy, per_request_exclusion, scored_band
  }
  route_id, channel_id, pooling_group_id, provider_account_id
  scoring_policy_version     // semver
  signal_contributions       // map(signal_name -> contribution_value)
  wait_action                ∈ { null, { entered_queue_at, exited_queue_at, exit_reason } }
  retry_attempt_number
  per_request_exclusion_summary  // list of (account_id, reason)
  quota_reservation_id, billing_ledger_claim_id
  forced_route_override_actor   ∈ { null, { operator_id, reason, audit_event_id } }
}
```

**Forbidden contents**: provider credentials, API Key secrets, raw prompt bodies, raw provider response bodies, sticky session ID raw values.

Audit Event rows for forced routing carry the override actor identity and the reason text supplied by the operator. The Audit Event MUST be retained per the [Reference Tracking Policy](../24_REFERENCE_TRACKING_POLICY.md) retention class for security-grade events.

## Acceptance Test Direction

Per [11_ACCEPTANCE_TEST_MATRIX.md](../11_ACCEPTANCE_TEST_MATRIX.md), tests are split between Sub2API-inheritable (verify behavior matches what Sub2API does) and HUAKAI-design (verify HUAKAI improvements that have no Sub2API equivalent).

Sub2API-inheritable scenarios:

- AT-POOL-001 / Layer-1 routing-config hit
- AT-POOL-002 / Sticky-within-routing hit with revalidation
- AT-POOL-003 / Sticky-standalone hit (no routing config)
- AT-POOL-004 / Layer 2 fresh tier-by-tier filter (priority → load → LRU)
- AT-POOL-005 / Sticky cache miss reasons enum coverage (5 reasons)
- AT-POOL-006 / Per-request exclusion list honored on retry
- AT-POOL-007 / Sticky shorter wait budget vs Fallback longer budget

HUAKAI-design scenarios (no Sub2API equivalent):

- AT-POOL-008 / Pattern B placeholder writeback + orphan sweep recovery
- AT-POOL-009 / Acquisition-token idempotent slot release (double-release no-op)
- AT-POOL-010 / Tenant isolation (cross-tenant Account never selected)
- AT-POOL-011 / Routing reason structured payload schema-conformant
- AT-POOL-012 / Wait-plan resume re-validates Phase C gates
- AT-POOL-013 / Default Top-K compatibility mode (K=1 unless tie group)
- AT-POOL-014 / Broad Top-K (K>1) opt-in flag, distribution within ±15% of uniform
- AT-POOL-015 / `allow_last_resort` opt-in semantics (remaining-after-cooldown == 1)
- AT-POOL-016 / Forced route audit + actor authorization (Personal vs SaaS)
- AT-POOL-017 / Capability safe-equivalent opt-in (Q4 default-deny)
- AT-POOL-018 / `CLAIM_RACE` retry without double-charge
- AT-POOL-019 / Tx2 atomicity for slot release + Usage Record + claim status

## Open Questions

None remaining at release. All five prior open questions resolved during Codex final review 2026-04-28; resolutions persisted in [pool-selection-synthesis-v2.md §13](../decompositions/_cross-cutting/pool-selection-synthesis-v2.md).

## Owner Decisions Locked at Release

| # | Decision | Reasoning |
|---|----------|-----------|
| Q1 | Forced-route visibility: Personal Edition = platform-admin only; SaaS Edition (Phase 10+) = tenant-operator with audit trail + rate-limit (default ≤5/hour/tenant) + platform-admin notification. | Forced routing is break-glass; default visibility scoped to highest-trust actor per edition. |
| Q2 | Sticky wait budget scope: per-Route default; per-Pool override allowed. | Route is user-facing UX surface; Pool is operator resource scope. |
| Q3 | Provider Account quota reserve: separate ledger from User / API Key quota. | Three orthogonal lifecycles + mutation frequency mismatch. |
| Q4 | Capability safe-equivalent default: opt-in (`exact_capability_only` default). | Default-deny matches money-grade correctness identity. |

Any change to these requires opening a new DR with documented superseding reason.

## Implementer Notes (added by implementer lane)

> This section is filled by the implementer after consuming the spec, NOT by the specifier. Notes here record local design choices, dependencies, and deviations.

(empty until implementer-lane work begins)
