# Provider Account Pool Selection — Synthesis v2 (Source-Verified)

| Field | Value |
| --- | --- |
| Status | Released-Inputs (2026-04-28). The implementer-facing Released spec is at [docs/specs/pool-routing.md](../../specs/pool-routing.md) Status=Released. This file is retained as the source-traceable backing artifact for spec; implementer lane MUST read the Released spec, NOT this file. |
| Feature ID | F-POOL-001 |
| Lane mode | Option C (account-pool routing carve-out per [DR-000](../../process/decisions/DR-000-clean-room-methodology.md)) |
| Author | Claude (PM-Orchestrator), synthesizing source-verified passes |
| Date | 2026-04-28 |
| Sources | Sub2API ([E-LIC-001](../../07_REFERENCE_EVIDENCE_LEDGER.md), LGPL-3.0, commit `b0a2252ed19c3720e6adafde6083e64fbac2efa9`); one-api ([E-LIC-004](../../07_REFERENCE_EVIDENCE_LEDGER.md), MIT, commit `8df4a2670b98266bd287c698243fff327d9748cf`); LiteLLM (E-LIC-005, MIT — pinned via Codex litellm cross-verify); plus [pool-selection-claude-v2.md](pool-selection-claude-v2.md), [pool-fallback-source-verified.md](../litellm/pool-fallback-source-verified.md), [quota-billing-source-verified.md](../one-api/quota-billing-source-verified.md), [observability-source-verified.md](../sub2api/observability-source-verified.md) |
| Supersedes | [pool-selection-synthesis.md](pool-selection-synthesis.md) (v1) — REJECTED by Codex reviewer 2026-04-28 |
| Becomes | After 10 Codex final-review fixes applied (this revision), file moves to `docs/specs/pool-routing.md` Status=Released. |
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

**Path-scoped sort behavior** (Codex final-review correction 2026-04-28):
- **Routing-branch path** (when group has ModelRouting config): strict lexicographic sort `(priority asc, load_rate asc, last_used_at asc)`, then shuffle ONLY inside exact tied sort groups (gateway_service.go:1691–1710 + 2718–2720 `shuffleWithinSortGroups`).
- **Layer 2 fresh path** (no ModelRouting config): three-step iterative selection — `filterByMinPriority` → `filterByMinLoadRate` → `selectByLRU` with random tie handling (gateway_service.go:1879–1883 + 2595–2637).

These are NOT identical algorithms; the routing branch sorts globally, the fresh path filters tier-by-tier.

Slot acquisition delegates to **`concurrencyService.AcquireAccountSlot`** (gateway_service.go:2250–2255 wrapper; testutil/stubs.go:24 interface) — cache-backed atomic increment with TTL fallback (configured via `gateway.concurrency_slot_ttl_minutes`). **Not** a serializable DB transaction.

Sticky cache miss reasons are a fixed enum: `session_limit`, `wait_queue_full`, `gate_check`, `rpm_red`, `account_cleared` (gateway_service.go:1610, 1639, 1644, 1646, 1661).

Wait plans use distinct limits: `StickySessionMaxWaiting` vs `FallbackMaxWaiting` (sticky shorter).

## 2. The Source-Verified one-api Comparison (per Codex one-api report)

Per [docs/decompositions/one-api/quota-billing-source-verified.md](../one-api/quota-billing-source-verified.md):

one-api provides a simpler group/model/channel selection baseline:
- highest-priority eligible bucket
- random tie choice
- specific-channel override (forceChannel)
- retry reselection after failure

one-api's quota path corrections (per Codex one-api re-verify; do NOT cite E-OAI-DEEP-001..008 as a single valid range — rows 001 and 004 marked FALSE, 003 marked DRIFT):
- **Non-atomic quota mutation**: separate operations not in one transaction.
- **Reservation/refund-based duplicate-billing mitigation** (post-call only).
- **Endpoint-specific charging windows**: text/audio/image differ.
- **No durable request fingerprint**: no idempotent claim gate.

HUAKAI's idempotent claim gate is therefore a real improvement, not a Sub2API copy.

## 3. The Source-Verified LiteLLM Cross-Reference (TODO-3 resolution)

Per [docs/decompositions/litellm/pool-fallback-source-verified.md](../litellm/pool-fallback-source-verified.md) §1:

**TODO-3 verdict: DIFFERENT-PATTERN-FOUND.**

What LiteLLM actually has (NOT "last remaining"):

- **"Exactly-one-configured-deployment" exemption**: when a logical model group has exactly one configured deployment, default 429 cooldown and ordinary percentage-threshold cooldown skip it. Evidence: `router_utils/cooldown_handlers.py:190-194, 223-239`.
- **Traffic-volume floor**: protects single transient misses from triggering cooldown when traffic is below a minimum threshold. Evidence: `constants.py:88-93`.
- **`APIConnectionError` ignored** by cooldown eligibility. Evidence: `router_utils/cooldown_handlers.py:57-63`.
- **Health-check "do-not-remove-all" safety net**: a separate path bypasses cooldown filtering when health-check routing plus allowed-fail policy would otherwise put every deployment in cooldown. Evidence: `router.py:9536-9547, 10010-10018`. **NOT** a "last-remaining" exemption — it's an emergency safety valve.

What LiteLLM does NOT have:
- A generic "last remaining healthy Account in a small Pool" exemption that checks runtime-remaining size.
- The exemption is on **configured** size (1 deployment), not **remaining after cooldown** size.

Implications for HUAKAI:
- HUAKAI's `allow_last_resort` opt-in flag (proposed in §12) is **HUAKAI-DESIGN** if it triggers on remaining-after-cooldown == 1; it is **LITELLM-INSPIRED** if it triggers on configured == 1. Synthesis chooses **remaining-after-cooldown semantics** (HUAKAI-DESIGN) because that's the operationally useful case for relay-station: at runtime, all-but-one healthy is more common than configured-as-single.
- HUAKAI **inherits** LiteLLM's traffic-volume floor pattern + APIConnectionError exclusion + health-check "do-not-remove-all" safety net. These are LITELLM-VERIFIED behaviors HUAKAI should replicate.

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
- Top-K policy (HUAKAI-DESIGN, Codex final-review correction 2026-04-28):
  - **Default compatibility mode**: K=1 unless all top candidates are in the same exact tie group, matching Sub2API's "lex-sort + tie-shuffle" behavior. Operator gets Sub2API-equivalent routing by default; no surprising routing drift.
  - **Broad Top-K randomization** (K>1 across non-tied candidates) is **opt-in by policy** per Pool, requiring explicit operator config + acceptance-test scenario AT-POOL-004 to verify expected stampede-prevention behavior.
  - This avoids the trap where default K=3 picks the second or third candidate when priority/load/last-used differ — which is NOT Sub2API-compatible.
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
- Re-validation on wait-plan resume (HUAKAI-DESIGN, Codex final-review verified 2026-04-28). Sub2API source confirmed negative: `gateway_helper.go:278-349` shows wait resume retries slot acquisition only and does NOT rerun the schedulability gate chain. HUAKAI requires full Phase C revalidation on resume.
- Optional score signals (error_rate, latency, quota_headroom, fairness_debt) — operator-tunable, default 0.
- HUAKAI last-healthy opt-in flag (HUAKAI-DESIGN): remaining-after-cooldown semantics, inspired by but not identical to LiteLLM's configured-single-deployment guard. LiteLLM source confirms a different pattern (configured-size-1, not remaining-after-cooldown), per `pool-fallback-source-verified.md` §1.
- Configurable failover status code list per Account / Pool.
- Tx2 atomicity for slot release + Usage Record + claim status (HUAKAI-DESIGN). Sub2API has atomic billing claim/effects, detached Usage Record write, and cache-based slot release; HUAKAI unifies these three effects into one Tx2.

## 13. Verified Source Resolutions

(Previously TODO-1..TODO-5 in pre-Released drafts. All 5 closed via Codex final review 2026-04-28; no open source dependencies remain. Per CL-009, a Released spec carries no open questions.)

- **Wait-resume revalidation**: VERIFIED NEGATIVE. Sub2API wait helper retries slot acquisition only; does NOT rerun the full schedulability gate chain before returning the slot. HUAKAI requires full Phase C revalidation on resume — this is a HUAKAI-DESIGN improvement, not Sub2API-inherited. Evidence: `internal/handler/gateway_helper.go:278-349`.
- **Capability safe-equivalent (Q4)**: HUAKAI-DESIGN ONLY. No Sub2API source behavior is claimed for capability shift in this spec. Q4 default-deny is a pure HUAKAI policy choice with no source dependency.
- **LiteLLM single-Account exemption**: RESOLVED via §3 above. DIFFERENT-PATTERN-FOUND. HUAKAI's `allow_last_resort` (remaining-after-cooldown semantics) is HUAKAI-DESIGN, distinct from LiteLLM's configured-single-deployment exemption.
- **forcePlatform scope**: VERIFIED. Sub2API's force-platform is platform-level context, NOT Account-level forcing. Evidence: `gateway_service.go:2080-2082` + `gateway_handler.go:267-270`. HUAKAI's tenant-level forced-route override is HUAKAI-DESIGN.
- **LoadRate computation**: VERIFIED. Sub2API computes `LoadRate = (currentConcurrency + waitingCount) * 100 / maxConcurrency`. Evidence: `concurrency_cache.go:416-424`. HUAKAI inherits this formula for compatibility mode.

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
