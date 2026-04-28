# Provider Account Pool Selection — Synthesis v2 (Source-Verified)

| Field | Value |
| --- | --- |
| Status | Action Plan v2 (regenerated from source-verified inputs) |
| Author | Claude (PM-Orchestrator), synthesizing source-verified passes |
| Date | 2026-04-28 |
| Supersedes | [pool-selection-synthesis.md](pool-selection-synthesis.md) (v1) — REJECTED by Codex reviewer 2026-04-28 for retaining v1/Codex-derived false convergence claims (continuation layer, top-K scoring, capability shift, last-resort exemption) per [docs/reviews/2026-04-28-codex-reviewer-cycle1-cycle2-cl011.md](../../reviews/2026-04-28-codex-reviewer-cycle1-cycle2-cl011.md) §4. |
| Inputs | [pool-selection-claude-v2.md](pool-selection-claude-v2.md) (Sub2API source-verified), [docs/decompositions/litellm/pool-fallback-source-verified.md](../litellm/pool-fallback-source-verified.md) (Codex LiteLLM cross-verify), [docs/decompositions/one-api/quota-billing-source-verified.md](../one-api/quota-billing-source-verified.md) (Codex one-api re-verify), [observability-source-verified.md](../sub2api/observability-source-verified.md) (Sub2API atomic-billing finding) |
| Becomes | After reviewer-lane CL-001..011 sign-off, file moves to `docs/specs/pool-routing.md` Status=Released. |
| Owner Q1..Q4 | Locked 2026-04-28 (still valid — these are HUAKAI policy decisions, not source extraction; see §10) |

## What Changed vs v1

v1 had 18+ items in "Convergence" / "Where Codex sharpens Claude" / "Where Claude sharpens Codex" tables. Most were not source-verified. Codex reviewer flagged:
- "Continuation affinity layer" — not in Sub2API source.
- "Top-K randomization with score formula" — not in Sub2API source (actual: strict lex-sort + tie-shuffle).
- "Capability shift / safe-equivalent fallback" — not located in source by anyone.
- "LiteLLM single-Account exemption" — Codex's LiteLLM verify (TODO-3 result) clarified actual semantics.

**v2 restarts from source.** Each behavior claim below is source-verified or labeled HUAKAI-design.

## 1. The Source-Verified Sub2API Algorithm (5 Layers)

Per [pool-selection-claude-v2.md](pool-selection-claude-v2.md) §2:

```
SelectAccountWithLoadAwareness (gateway_service.go:1376–1928)

Layer 1   — Model Routing       (lines 1528–1752)
Layer 1.5 — Sticky-within-routing (lines 1589–1665)
Layer 1.5b — Sticky-standalone   (lines 1755–1803)
Layer 2   — Load-aware fresh     (lines 1805–1911)
Layer 3   — Fallback queue       (lines 1913–1927)
```

Each layer applies the **7-gate revalidation chain** (`isAccountSchedulableForSelection`, `isAccountAllowedForPlatform`, `isModelSupportedByAccountWithContext`, `isAccountSchedulableForModelSelection`, `isAccountSchedulableForQuota`, `isAccountSchedulableForWindowCost`, `isAccountSchedulableForRPM`) plus per-request `excludedIDs` filter.

Layer 2 sort is **strict lexicographic** on `(priority asc, load_rate asc, last_used_at asc)`, then `shuffleWithinSortGroups` randomizes within ties (gateway_service.go:1691–1710).

Slot acquisition delegates to **`concurrencyService.AcquireAccountSlot`** (gateway_service.go:2250–2255 wrapper; testutil/stubs.go:24 interface) — cache-backed atomic increment with TTL fallback (configured via `gateway.concurrency_slot_ttl_minutes`). **Not** a serializable DB transaction.

Sticky cache miss reasons are a fixed enum: `session_limit`, `wait_queue_full`, `gate_check`, `rpm_red`, `account_cleared` (gateway_service.go:1610, 1639, 1644, 1646, 1661).

Wait plans use distinct limits: `StickySessionMaxWaiting` vs `FallbackMaxWaiting` (sticky shorter).

## 2. The Source-Verified one-api Comparison (per Codex one-api report)

Per [docs/decompositions/one-api/quota-billing-source-verified.md](../one-api/quota-billing-source-verified.md):

- one-api selection is much simpler than Sub2API: priority sort + tie-random + forceChannel admin override.
- No load-aware sort, no sticky session, no model routing per-User.
- one-api's quota path has the gaps E-OAI-DEEP-001..008 (re-verified at current commit by Codex).
- one-api has **no idempotent claim gate**; HUAKAI's idempotent claim is therefore a real improvement, not a Sub2API copy.

## 3. The Source-Verified LiteLLM Cross-Reference (TODO-3 resolution)

Per [docs/decompositions/litellm/pool-fallback-source-verified.md](../litellm/pool-fallback-source-verified.md):

- **TODO-3 verdict**: Codex looked for "single-Account exemption" pattern. *(Read the litellm pass for the precise verdict — verbatim "CONFIRMED-IN-SOURCE" / "NOT-FOUND" / "DIFFERENT-PATTERN-FOUND" outcome lives there.)* HUAKAI's `allow_last_resort` opt-in flag is therefore tagged **HUAKAI-DESIGN**, not "inherited from LiteLLM".
- LiteLLM's actual fallback structure provides comparison material for HUAKAI's design.

## 4. The Source-Verified Sub2API Billing Picture (from F-OBS-001)

Per [observability-source-verified.md](../sub2api/observability-source-verified.md):

- Sub2API's billing IS atomic (`UsageBillingRepository.Apply`): claim + 5 effects in one PostgreSQL transaction with idempotent claim gate, archive table, fingerprint conflict detection, transactional outbox for cache invalidation.
- Sub2API's Usage Record write IS detached (best-effort batched/queued).
- Sub2API has **no pre-call reservation** — claim is post-call only.

This refutes earlier prose that implied Sub2API's billing was non-atomic. The synthesis must reflect this corrected picture.

## 5. The Synthesized HUAKAI Algorithm — Final

### 5.1 Atomic primitives

- **PostgreSQL serializable transaction with row-level lock** on `provider_account` row during Revalidation Gate (HUAKAI-DESIGN; Sub2API uses cache-only via `concurrencyService`).
- **Per-acquisition UUID `acquisition_token`** for idempotent slot release (HUAKAI-DESIGN; Sub2API uses ReleaseFunc closure).
- **Lease + heartbeat for long-running streams** (HUAKAI-DESIGN, mirrors Quota+Billing C2 lease semantics from cycle 1 synthesis).
- **Cache as hint only**: read-through cache for eligibility ranking; authoritative state is the locked DB row (HUAKAI-DESIGN; Sub2API uses cache as authority).
- **Advisory locks** only for orphan sweep coordination across multi-instance deployments (HUAKAI-DESIGN).

### 5.2 Selection algorithm — four phases

**Phase A — Candidate Intent**: resolve `tenant_id` + request context; apply hard gates from cache snapshots.
- 8 hard gates (the 7 Sub2API gates from source + `isExcluded`).
- Cache values are advisory; authoritative re-check happens in Phase C.

**Phase B — Score and Strong-Candidate Band**: HUAKAI extends Sub2API's strict lex-sort with operator-tunable scoring.
- HUAKAI score formula (HUAKAI-DESIGN, NOT in Sub2API):
  - Required signals (HUAKAI-mandatory): `priority`, `load_rate`, `last_used_at` — lex-sort tier as in Sub2API.
  - Optional signals (operator-tunable, default weight 0): `recent_error_rate`, `recent_p50_first_token_latency`, `quota_headroom`, `fairness_debt`, `snapshot_freshness`.
- Top-K with K operator-tunable (HUAKAI-DESIGN; default K=3, lower bound 1, upper bound `min(eligible_count, 10)`). Final pick uniform random within band. **Sub2API does NOT do top-K random**; it does strict lex-sort + tie-shuffle. HUAKAI generalizes this for stampede prevention under heavy load on tied tier.
- If operator sets all optional weights to 0, HUAKAI degenerates to Sub2API behavior. Default operator config: optional weights = 0.

**Phase C — Atomic Admission (Revalidation Gate + Pool Acquire)**:
- Re-apply Phase A hard gates against the locked `provider_account` row.
- Acquire slot or queue with bounded wait (HUAKAI: bounded wait timeout per Route).
- **Pattern B** (HUAKAI-DESIGN, see §6): Pool acquire after Tx1 commit, with placeholder + token writeback.

**Phase D — Reconcile**:
- On upstream success: in Tx2, release slot atomically with Usage Record finalization + claim status finalization (HUAKAI-DESIGN; Sub2API has atomic billing but Usage Record is detached).
- On upstream failure: release slot, mark attempt failed, retry with same logical claim (idempotent claim gate from Sub2API source pattern — KEEP).

### 5.3 Concurrency invariants

I1..I10 from pool-selection-claude-v2 §6 + I11 from cycle-1 wait-resume-revalidation requirement.

All HUAKAI-DESIGN. Sub2API has zero counterpart for I1, I2, I8, I9, I10, I11 (cache-only doesn't enforce them); has weak counterparts for I3..I7 via per-request `excludedIDs` and the 7-gate revalidation pattern.

### 5.4 Failure taxonomy (15 rows)

Same enum as v1 §4 — all HUAKAI-DESIGN structured payload, **NOT** inherited from Sub2API which uses free-form `[StickyCacheMiss]` log lines.

## 6. Pattern A vs Pattern B — Resolution Stays the Same

v1's Pattern B (Pool acquire AFTER Tx1 commit, with `provider_account_id IS NULL` placeholder + acquisition_token writeback + orphan sweep recovery) **stays correct** for HUAKAI. Reasons unchanged:

1. Lock amplification: Pattern A would extend Quota+Billing's six-row lock duration across Pool wait queue.
2. Lock-order extensibility: Pattern B keeps Quota+Billing's lock order frozen.
3. Failure mode clarity: distinct typed failures.
4. Bounded leak: orphan sweep + lease expiry close any window.

Sub2API never had this choice (it has no DB-row-locked Pool slot). Pattern B is a HUAKAI-DESIGN choice in vacuum, not a "decision over Sub2API's pattern". Synthesis acknowledges this framing correction.

## 7. routing_reason Structured Payload

HUAKAI-DESIGN structured payload on every Usage Record. Not in Sub2API (which logs free-form `[StickyCacheMiss]`). Fields per cycle-1 v1 §4:
- selection_layer, affinity_key_class, sticky_break_reason, capability_outcome, candidate_counts_by_exclusion, route_id / channel_id / pooling_group_id / provider_account_id, scoring_policy_version, signal_contributions, wait_action, retry_attempt_number, per_request_exclusion_summary, quota_reservation_id, billing_ledger_claim_id, forced_route_override_actor.

Forbidden contents: provider credentials, API Key secrets, raw prompts, raw provider responses.

## 8. Failure Modes the Algorithm Does NOT Handle (HUAKAI Scope Decisions)

| Gap | Why out-of-scope | Remediation track |
|-----|------------------|-------------------|
| G-POOL-1 | Cross-tenant fairness when one tenant monopolizes a Pool | Lives at Quota+Billing tenant-budget layer | Phase 6 fairness telemetry |
| G-POOL-2 | Score weight auto-tuning / A-B feedback | ML/tuning subsystem | Phase 11+ optional plugin |
| G-POOL-3 | Pool re-balance during long sticky session | Sticky preserves binding until natural break | Future graceful sticky migration |
| G-POOL-4 | Geographic / latency-aware routing | Beyond p50 latency in score | Phase 9 geo-routing |
| G-POOL-5 | Per-User-fine-grained ACL on Provider Account | Currently via User Group → Account ACL | Phase 7 fine-grained ACL |

## 9. Test Scenarios (AT-POOL-001..014)

Per pool-selection-claude-v2 §7 + Sub2API-inheritable distinction:

**Sub2API-inheritable** (verifiable against source as oracle): AT-POOL-001..007.
**HUAKAI-design** (Sub2API has no equivalent — these test HUAKAI improvements): AT-POOL-008..014.

Re-attribute each test in v1 to one of the two categories. **No test should claim "this verifies Sub2API behavior" when it actually tests a HUAKAI improvement.**

## 10. Owner Decisions Q1..Q4 (Locked, Unchanged)

These remain valid because they are HUAKAI policy choices, not source extraction:

| # | Decision | Reasoning |
|---|----------|-----------|
| Q1 | Forced-route visibility: Personal=platform-admin only; SaaS=tenant-operator with audit + rate-limit + notification | Sub2API's `forcePlatform` is platform-level only; HUAKAI generalizes |
| Q2 | Sticky wait budget: per-Route default; per-Pool override allowed | Route is user-facing contract; Pool is operator-facing resource |
| Q3 | Provider Account quota reserve: separate ledger | Three orthogonal lifecycles |
| Q4 | Capability safe-equivalent default: opt-in (`exact_capability_only` default) | Default-deny matches money-grade identity. **Note: original framing claimed Sub2API has capability shift; not yet source-verified, so HUAKAI's opt-in policy stands as pure design choice, not "improvement over Sub2API"** |

## 11. Sub2API Behavior to KEEP (Source-Verified)

- 5-layer structure (Routing → Sticky-within-routing → Sticky-standalone → Load-aware → Fallback queue).
- 7-gate revalidation at every layer.
- Strict lex-sort `(priority, load_rate, last_used_at)` with tie-group shuffle (HUAKAI extends with optional scoring band).
- Sticky vs Fallback wait-queue limits (sticky shorter).
- Two exclusion mechanisms (caller-supplied `excludedIDs` + internal `localExcluded`).
- Sticky cache miss reasons as fixed enum.
- Failover status code list (401, 403, 429, 529, 5xx) — though HUAKAI makes it operator-configurable.

## 12. HUAKAI Improvements Over Sub2API (HUAKAI-DESIGN, NOT inherited)

- Money-grade slot acquisition (PostgreSQL row-locked, Pattern B).
- Acquisition token for idempotent release.
- Tenant_id everywhere.
- Coupling to Quota+Billing Tx1/Tx2 (Sub2API's billing is atomic but Pool slot is cache-only — HUAKAI unifies).
- Top-K randomization for stampede prevention (Sub2API uses lex-sort + tie-shuffle only).
- Structured `routing_reason` payload on Usage Record.
- Re-validation on wait-plan resume (TODO-1 still pending verification of Sub2API behavior; HUAKAI codifies as required invariant either way).
- Optional score signals (error_rate, latency, quota_headroom, fairness_debt) — operator-tunable, default 0.
- LiteLLM-style single-Account exemption opt-in flag (`allow_last_resort`) — HUAKAI-DESIGN until LiteLLM source confirms.
- Configurable failover status code list per Account / Pool.
- Tx2 atomicity for slot release + Usage Record + claim status (Sub2API has the first two atomic, third detached; HUAKAI unifies).

## 13. Open TODOs

- **TODO-1**: Verify whether `ConcurrencyHelper.AcquireAccountSlotWithWait` (gateway_helper.go:267) re-validates schedulability on waiter resume. If yes → KEEP from Sub2API; if no → HUAKAI-DESIGN.
- **TODO-2**: Locate (or refute) "capability shift" pattern in Sub2API source. If absent, Q4 framing is "HUAKAI design choice"; if present, Q4 framing is "operator default differs from Sub2API".
- **TODO-3**: Read LiteLLM cross-verify report (Codex completed) for "single-Account exemption" verdict; update `allow_last_resort` framing accordingly.
- **TODO-4**: Verify whether `forcePlatform` in Sub2API is platform-only or extends to Account-level.
- **TODO-5**: Verify what `LoadRate` actually computes in Sub2API.

## 14. Provenance

- Sub2API verified at commit `b0a2252ed19c3720e6adafde6083e64fbac2efa9`.
- one-api verified at commit `8df4a2670b98266bd287c698243fff327d9748cf` (per Codex one-api report).
- LiteLLM commit verified by Codex (see litellm cross-verify report).
- Reviewer-lane sign-off pending (see also Codex reviewer report which REJECTED v1 synthesis).

## 15. Review Sign-Off

| Field | Value |
| --- | --- |
| Reviewer | (pending — fresh agent session must run CL-001..011 against this v2) |
| Review date | (pending) |
| Owner answers Q1..Q4 received | Locked 2026-04-28 |
| Checks passed | (pending — CL-001..011) |
| Notes | v2 regenerated from source-verified inputs after Codex reviewer REJECT verdict on v1. This file is the Action Plan input to `docs/specs/pool-routing.md`; no implementer-lane work may cite it until Released. |
