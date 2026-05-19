# Codex Final Reviewer-Lane Report - F-POOL-001 Synthesis v2

| Field | Value |
| --- | --- |
| Reviewer | Codex final reviewer-lane |
| Review date | 2026-04-28 |
| Artifact reviewed | `docs/decompositions/_cross-cutting/pool-selection-synthesis-v2.md` |
| Gate | CL-001..CL-011 strict path review for F-POOL-001 |
| Verdict | APPROVE-WITH-FIXES |
| Local Sub2API source | `.omc/reference-src/sub2api` at `b0a2252ed19c3720e6adafde6083e64fbac2efa9` |
| Local one-api source | `.omc/reference-src/one-api` at `8df4a2670b98266bd287c698243fff327d9748cf` |
| Local LiteLLM source | `.omc/reference-src/litellm` at `62920a0cb29f11912edb5bacee470f1b1c044def` |

## Review Protocol Notes

- Pre-commitment prediction 1: v2 would fix the four v1 false attribution classes but leave residue in TODOs.
- Actual: mostly confirmed. Continuation, top-K, and capability-shift attributions are corrected. Last-resort is corrected in section 3 but still stale in TODO-3 and line 197.
- Pre-commitment prediction 2: at least one source line citation would be imprecise because the synthesis inherited citations from input passes.
- Actual: confirmed. The Layer 2 sort claim cites `gateway_service.go:1691-1710`, but those lines are the model-routing branch, not Layer 2.
- Pre-commitment prediction 3: one-api corrections would be easy to re-broaden because the source-verification report found false/drifted rows.
- Actual: confirmed. v2 line 53 says `E-OAI-DEEP-001..008` are quota gaps, but the one-api reverify report marks rows 001 and 004 false and 003 drifted.
- Pre-commitment prediction 4: HUAKAI design improvements would be well-labeled but one design invariant might contradict the claimed source-compatible default.
- Actual: confirmed. The top-K design says optional weights default to zero and therefore degenerates to Sub2API behavior, but default `K=3` still changes selection semantics unless the band is constrained to exact ties.
- Pre-commitment prediction 5: the regenerated synthesis would still be an action-plan artifact, not a release-ready strict spec header.
- Actual: confirmed. It lacks explicit `Feature ID`, `Lane mode`, and `Sources` fields required for the Released spec form.
- Review mode: escalated to ADVERSARIAL after the third MAJOR finding, per Critic protocol. I checked adjacent source and input reports for TODOs, one-api correction drift, force-platform semantics, and LoadRate semantics.
- Self-audit result: no CRITICAL findings survived. All MAJOR findings below have direct artifact evidence plus source or input-report evidence.
- Realist check result: severity is MAJOR rather than CRITICAL because no implementer-lane code has shipped from v2 yet, and each required fix is bounded text/spec correction.

## §1 - CL-001..011 Verdict Matrix

| Check | Verdict | One-line justification |
| --- | --- | --- |
| CL-001 | PARTIAL | v2 still carries upstream identifiers such as `SelectAccountWithLoadAwareness`, gate function names, `concurrencyService.AcquireAccountSlot`, `UsageBillingRepository.Apply`, and config names in release-facing prose. Keep citations, but move/paraphrase identifiers before Released. |
| CL-002 | PASS | No confirmed upstream database table/column/migration names are copied. `Provider Account`, `Route`, `Channel`, and `Usage Record` align with `docs/18_GLOSSARY.md` and `docs/19_DOMAIN_MODEL.md`. |
| CL-003 | PASS | No upstream UI component names, class names, or dashboard layout identifiers were found. |
| CL-004 | PASS | No upstream documentation sentence longer than the allowed common technical phrases was found. |
| CL-005 | PARTIAL | The HUAKAI four-phase algorithm is independent design, but v2 still contains implementation-shaped source layer details and one source-mismatched Layer 2 sort statement. |
| CL-006 | PARTIAL | License rows exist in `docs/07_REFERENCE_EVIDENCE_LEDGER.md` for Sub2API `E-LIC-001`, one-api `E-LIC-004`, and LiteLLM `E-LIC-005`, but v2 does not explicitly tie its sources to those rows in a `Sources` field. |
| CL-007 | FAIL | v2 does not contain a `Lane mode: Option C` field. F-POOL-001 is account-pool routing and must be Option C before release. |
| CL-008 | FAIL | v2 does not contain a `Feature ID: F-POOL-001` field, although the parity row exists at `docs/03_FEATURE_PARITY_MATRIX.md:71`. |
| CL-009 | FAIL | v2 keeps TODO-1..TODO-5 in `Open TODOs`; several are stale/resolvable and any remaining open question is a release hold signal. |
| CL-010 | PASS | No upstream source URL is embedded in implementer-relevant sections; v2 uses local paths and local doc links. |
| CL-011 | FAIL | Synthesis files are exempt from direct CL-011 only if they inherit correct citations. Spot-check found one failed citation/claim match and multiple missing or stale inherited citations. |

Detailed CL notes:

- CL-001 pressure is not license contamination by itself because most identifiers appear in evidence/provenance context.
- It is still not clean enough for a `docs/specs/*.md` Released spec because implementers should consume behavior guarantees, not upstream function names.
- CL-006 is easy to fix: add explicit source rows naming `Sub2API (E-LIC-001, LGPL-3.0)`, `one-api (E-LIC-004, MIT)`, and `LiteLLM (E-LIC-005, MIT)`.
- CL-007 and CL-008 are structural. The artifact cannot move "as-is" to `docs/specs/pool-routing.md` because the required strict spec fields are absent.
- CL-009 is the strongest release blocker. Open TODOs are acceptable in a decomposition draft; they are not acceptable in a Released strict spec.
- CL-011 fails because at least one spot-checked source citation does not support the exact claim as written.

## §2 - v1 REJECT Finding Remediation Check

| v1 finding | v2 status | Evidence | Reviewer judgment |
| --- | --- | --- | --- |
| Continuation layer attributed to Sub2API | REMEDIATED | v2 line 16 says continuation affinity is not in Sub2API source; section 1 lists the source-backed 5-layer structure with no continuation layer. | Correct. No continuation layer remains in Sub2API KEEP. |
| Top-K scoring attributed to Sub2API | REMEDIATED | v2 line 17 says top-K randomization is not in Sub2API; lines 103-107 label the HUAKAI score formula and Top-K as HUAKAI-DESIGN. | Attribution fixed. A separate new design-consistency issue remains at line 108. |
| Capability shift attributed to Sub2API | REMEDIATED | v2 line 18 says capability shift was not located; line 175 says Q4 is a pure HUAKAI design choice, not an improvement over Sub2API. | Correct attribution. TODO-2 should be removed or closed before release. |
| Last-resort exemption attributed to LiteLLM/Sub2API | PARTIALLY-REMEDIATED | v2 lines 60-75 correctly record LiteLLM as `DIFFERENT-PATTERN-FOUND`, but line 197 still says `LiteLLM-style single-Account exemption ... until LiteLLM source confirms`, and TODO-3 line 205 says to read the already-read LiteLLM report. | The source truth is fixed in section 3 but stale release-blocking residue remains. |

Remediation summary:

- The v1 rejection was not repeated wholesale.
- v2 does not pretend continuation, top-K scoring, or capability shift are verified Sub2API behavior.
- The remaining problems are not the original four false convergence claims.
- The remaining problems are release hygiene, stale TODO closure, one mis-cited source claim, one one-api over-broad statement, and two HUAKAI design consistency defects.

## §3 - Spot-Check Log

Spot-check method:

- I selected citations across Sub2API selection, Sub2API concurrency, Sub2API billing, LiteLLM cooldown, one-api routing/billing, and v2 TODO areas.
- I used `rg -n` against the local cloned reference sources.
- Verdict meanings:
- PASS: cited source matches the claim.
- FAIL: cited source exists but does not support the exact claim as written.
- MISSING: the claim lacks a source citation adequate for release.

### Spot-check 01 - Main Sub2API selector exists

- v2 claim: `SelectAccountWithLoadAwareness (gateway_service.go:1376-1928)`.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/service/gateway_service.go:1376` defines `func (s *GatewayService) SelectAccountWithLoadAwareness(...)`.
- Grep evidence: `rg -n "SelectAccountWithLoadAwareness" .omc/reference-src/sub2api/backend/internal/service/gateway_service.go`.
- Verdict: PASS.
- Note: the cited range reaches beyond the main selector into helper definitions; acceptable for evidence, but tighter source ranges would be cleaner.

### Spot-check 02 - Five Sub2API selection layers

- v2 claim: model routing, sticky-within-routing, sticky-standalone, load-aware fresh, fallback queue.
- Grep evidence: `gateway_service.go:1528` has `Layer 1` model routing.
- Grep evidence: `gateway_service.go:1588` has sticky behavior inside the routing account range.
- Grep evidence: `gateway_service.go:1754` has `Layer 1.5` sticky session when no model routing config is present.
- Grep evidence: `gateway_service.go:1805` has `Layer 2` load-aware selection.
- Grep evidence: `gateway_service.go:1913` has `Layer 3` fallback queue.
- Verdict: PASS.
- Note: v2's `Layer 1.5b` label is synthesis vocabulary; source labels the standalone sticky path as `Layer 1.5`.

### Spot-check 03 - Seven gate chain exists

- v2 claim: the 7 gates are applied in the Sub2API selector.
- Grep evidence: `gateway_service.go:1540-1571` applies the gates in model routing candidate filtering.
- Grep evidence: `gateway_service.go:1595-1602` applies the sticky candidate gate/RPM checks.
- Grep evidence: `gateway_service.go:1815-1836` applies the gates in the load-aware candidate filter.
- Grep evidence: `rg -n "isAccountSchedulableForSelection|isAccountAllowedForPlatform|isModelSupportedByAccountWithContext|isAccountSchedulableForModelSelection|isAccountSchedulableForQuota|isAccountSchedulableForWindowCost|isAccountSchedulableForRPM" ...`.
- Verdict: PASS.
- Note: implementer-facing spec should paraphrase these as lifecycle, platform, model, quota, window-cost, and RPM gates instead of carrying upstream function names.

### Spot-check 04 - Layer 2 sort cited to `gateway_service.go:1691-1710`

- v2 claim at line 39: `Layer 2 sort is strict lexicographic ... then shuffleWithinSortGroups ... (gateway_service.go:1691-1710)`.
- Grep evidence: `gateway_service.go:1691-1710` is `sort.SliceStable(routingAvailable...)` followed by `shuffleWithinSortGroups(routingAvailable)`.
- Grep evidence: `gateway_service.go:1677` describes this as routing-available sorting, inside Layer 1 model routing.
- Grep evidence: `gateway_service.go:1879-1883` shows actual Layer 2 uses `filterByMinPriority`, `filterByMinLoadRate`, then `selectByLRU`.
- Verdict: FAIL.
- Why it matters: the source behavior is close but not identical to the citation. A release spec cannot say the cited Layer 2 line range proves a Layer 2 sort when it proves the routing branch sort.

### Spot-check 05 - Tie shuffle behavior

- v2 claim: Sub2API randomizes within tied sort groups.
- Grep evidence: `gateway_service.go:2718-2720` defines `shuffleWithinSortGroups`.
- Grep evidence: `gateway_service.go:2718` comment states grouping by priority/load/last-used, then in-group shuffle.
- Verdict: PASS.
- Note: this supports routing-branch tie shuffle. Layer 2's fresh path randomizes only inside `selectByLRU` when candidates share the minimum last-used value.

### Spot-check 06 - Slot acquisition delegates to cache-backed concurrency service

- v2 claim at line 41: slot acquisition delegates to `concurrencyService.AcquireAccountSlot`; cache-backed atomic increment with TTL fallback; not serializable DB.
- Grep evidence: `gateway_service.go:2250-2254` delegates to `s.concurrencyService.AcquireAccountSlot(ctx, accountID, maxConcurrency)`.
- Grep evidence: `concurrency_service.go:129-141` generates a request ID and calls cache `AcquireAccountSlot`.
- Grep evidence: `concurrency_cache.go:235-238` uses Redis script-backed acquisition.
- Grep evidence: `config.go:1689` sets default `gateway.concurrency_slot_ttl_minutes`.
- Verdict: PASS for the behavior, FAIL for the sub-citation `testutil/stubs.go:24 interface`.
- Note: `testutil/stubs.go:24` is a stub implementation, not the interface. Use `concurrency_service.go:17-20` for the interface.

### Spot-check 07 - Sticky cache miss enum

- v2 claim: sticky miss reasons are `session_limit`, `wait_queue_full`, `gate_check`, `rpm_red`, `account_cleared`.
- Grep evidence: `gateway_service.go:1610`, `1625`, `1639`, `1644`, `1646`, and `1661`.
- Grep evidence: `gateway_service.go:1656` logs `[StickyCacheMiss] reason=%s`.
- Verdict: PASS.
- Note: v2 correctly avoids claiming the HUAKAI structured `routing_reason` payload is inherited from Sub2API.

### Spot-check 08 - Wait plan helper does not revalidate schedulability on resume

- v2 TODO-1 asks whether `AcquireAccountSlotWithWait` revalidates on waiter resume.
- Grep evidence: `gateway_helper.go:278-282` defines `acquireSlot` as only `AcquireUserSlot` or `AcquireAccountSlot`.
- Grep evidence: `gateway_helper.go:341-349` retries acquisition and returns release function on success.
- Grep evidence: no gate function names are present in `gateway_helper.go:260-390`.
- Verdict: PASS as a verified negative finding, not as an open TODO.
- Required release action: replace TODO-1 with "Sub2API wait resume only retries slot acquisition; HUAKAI adds full revalidation on resume."

### Spot-check 09 - one-api priority bucket and random tie

- v2 claim: one-api selection is simpler: priority sort + tie-random + forced channel override.
- Grep evidence: `one-api/model/cache.go:227-254` chooses among eligible channels by priority bucket and random index.
- Grep evidence: `one-api/model/ability.go:22-50` uses max-priority filtering and random order in DB-backed path.
- Grep evidence: `one-api/middleware/distributor.go:28-59` handles specific channel context and normal selection.
- Verdict: PASS.
- Note: line 51 is acceptable as a short summary, but it should cite the source report or local line ranges directly in a Released spec.

### Spot-check 10 - one-api `E-OAI-DEEP-001..008` quota gap summary

- v2 claim at line 53: `one-api's quota path has the gaps E-OAI-DEEP-001..008`.
- Input-report evidence: `quota-billing-source-verified.md:25` marks `E-OAI-DEEP-001` FALSE.
- Input-report evidence: `quota-billing-source-verified.md:27` marks `E-OAI-DEEP-003` DRIFT.
- Input-report evidence: `quota-billing-source-verified.md:28` marks `E-OAI-DEEP-004` FALSE for text/audio.
- Input-report evidence: `quota-billing-source-verified.md:45` says rows requiring correction are `001`, `003`, `004`, `009`, and `014`.
- Verdict: FAIL.
- Why it matters: v2 re-broadens a corrected report into an inaccurate "001..008 quota gaps" statement.

### Spot-check 11 - one-api has no durable idempotent claim gate

- v2 claim: one-api has no idempotent claim gate.
- Grep evidence: `rg -n "Idempot|fingerprint|claim|dedup" .omc/reference-src/one-api` returned no matches.
- Input-report evidence: `quota-billing-source-verified.md:29` says no durable request fingerprint or claim gate was found.
- Verdict: PASS.
- Note: this is the correct one-api comparison to keep.

### Spot-check 12 - LiteLLM exactly-one-configured-deployment cooldown guard

- v2 claim: exactly-one configured deployment avoids default 429 and ordinary percentage-threshold cooldown.
- Grep evidence: `cooldown_handlers.py:194` sets `is_single_deployment_model_group`.
- Grep evidence: `cooldown_handlers.py:223-239` gates default 429/failure-rate cooldown on that flag and minimum request threshold.
- Verdict: PASS.
- Note: v2 correctly says this is not a runtime "last remaining healthy" exemption.

### Spot-check 13 - LiteLLM traffic-volume floor

- v2 claim: traffic-volume floor protects single transient misses.
- Grep evidence: `constants.py:88-93` defines minimum request thresholds and comments that this prevents first-failure cooldown.
- Grep evidence: `cooldown_handlers.py:234-239` applies the minimum request threshold in failure-rate cooldown logic.
- Verdict: PASS.

### Spot-check 14 - LiteLLM health-check "do not remove all" safety net

- v2 claim: health-check routing plus allowed-fail policy can bypass cooldown filtering when all would be in cooldown.
- Grep evidence: `router.py:10010-10018` logs and restores pre-cooldown deployments when all are filtered and health-check routing with allowed-fail policy is active.
- Grep evidence: `router.py:10338-10342` returns all deployments if the health-check filter would exclude every candidate.
- Verdict: PASS.
- Note: v2's wording that this is not a generic last-remaining exemption is correct.

### Spot-check 15 - Sub2API atomic billing

- v2 claim: `UsageBillingRepository.Apply` performs claim plus effects in one PostgreSQL transaction.
- Grep evidence: `usage_billing_repo.go:35` begins a transaction.
- Grep evidence: `usage_billing_repo.go:45` calls `claimUsageBillingKey`.
- Grep evidence: `usage_billing_repo.go:54` applies billing effects.
- Grep evidence: `usage_billing_repo.go:68-70` inserts into `usage_billing_dedup` with conflict handling.
- Grep evidence: `usage_billing_repo.go:331` enqueues scheduler outbox inside the same transaction path.
- Verdict: PASS.

### Spot-check 16 - Usage Record detached write

- v2 claim: Sub2API Usage Record write is detached/best-effort.
- Grep evidence: `gateway_service.go:7812-7820` calls `writeUsageLogBestEffort` with detached context and optional `CreateBestEffort`.
- Grep evidence: `gateway_service.go:8023-8038` applies billing first and writes usage log afterward.
- Grep evidence: `usage_log_repo.go:262` defines `CreateBestEffort`.
- Verdict: PASS.

### Spot-check 17 - Force-platform is platform-level

- v2 claim: Sub2API `forcePlatform` is platform-level only.
- Grep evidence: `gateway_service.go:2080-2082` resolves a context value as platform and returns it as `forcePlatform`.
- Grep evidence: `gateway_service.go:2118-2120` disables mixed scheduling when `hasForcePlatform`.
- Grep evidence: `gateway_handler.go:267-270` sets `platform` from force-platform context or from API key group.
- Verdict: PASS.
- Required release action: close TODO-4 rather than leaving it open.

### Spot-check 18 - LoadRate computation

- v2 TODO-5 asks what `LoadRate` computes.
- Grep evidence: `concurrency_service.go:112-116` defines `LoadRate` as percent.
- Grep evidence: `concurrency_cache.go:416-424` computes `LoadRate = (currentConcurrency + waitingCount) * 100 / maxConcurrency`.
- Verdict: PASS as a now-verified behavior.
- Required release action: close TODO-5 and cite the formula if retained.

### Spot-check 19 - Tx2 comparison wording

- v2 claim at line 199: `Sub2API has the first two atomic, third detached` for slot release + Usage Record + claim status.
- Grep evidence: `gateway_service.go:2250-2254` shows slot acquire/release is cache/concurrency-service based, not in billing Tx2.
- Grep evidence: `gateway_service.go:7812-7820` shows Usage Record write is detached/best-effort.
- Grep evidence: `usage_billing_repo.go:35-58` shows billing claim/effects are atomic, not slot release + Usage Record.
- Verdict: FAIL.
- Required release action: rewrite the comparison. Sub2API has atomic billing claim/effects, detached Usage Record, and cache slot release. It does not have the first two elements of HUAKAI Tx2 atomicity.

## §4 - New Findings

### Major Finding 1 - Release candidate still contains open TODOs, including stale TODOs already answered by source

- Evidence: `pool-selection-synthesis-v2.md:201-207` lists TODO-1 through TODO-5.
- Evidence: TODO-3 is already resolved in the same file at `pool-selection-synthesis-v2.md:60-75`.
- Evidence: TODO-1 was verified negative in the prior review: `docs/process/reviews/2026-04-28-codex-reviewer-cycle1-cycle2-cl011.md:57-59`.
- Evidence: source confirms `gateway_helper.go:278-349` only retries slot acquisition.
- Evidence: source confirms `forcePlatform` is platform-level at `gateway_service.go:2080-2082`.
- Evidence: source confirms `LoadRate = (currentConcurrency + waitingCount) * 100 / maxConcurrency` at `concurrency_cache.go:416-424`.
- Confidence: HIGH.
- Why this matters: CL-009 says implementer lane treats Open Questions as a hold signal. A Released spec cannot preserve TODOs that are either stale or resolvable.
- Fix: close TODO-1, TODO-3, TODO-4, and TODO-5 in v2. Convert TODO-2 into either a verified negative source finding or a pure HUAKAI-design note with no release hold.

### Major Finding 2 - Layer 2 sort claim cites the wrong source behavior

- Evidence: `pool-selection-synthesis-v2.md:39` says Layer 2 sort is lexicographic and cites `gateway_service.go:1691-1710`.
- Evidence: `gateway_service.go:1691-1710` is the routing branch sort over `routingAvailable`, not the Layer 2 fresh path.
- Evidence: actual Layer 2 source is `gateway_service.go:1879-1883`, using minimum priority, minimum load, then LRU selection.
- Confidence: HIGH.
- Why this matters: this is exactly the class of CL-011 failure the v1 review was meant to prevent: a correct-sounding statement attached to the wrong source location.
- Fix: replace line 39 with path-scoped wording: routing branch uses strict priority/load/last-used sort plus tie shuffle; Layer 2 uses min-priority, min-load, then LRU with random tie handling.

### Major Finding 3 - Top-K default does not actually degenerate to Sub2API behavior

- Evidence: `pool-selection-synthesis-v2.md:107` sets Top-K default `K=3` and final pick uniform random within band.
- Evidence: `pool-selection-synthesis-v2.md:108` says optional weights set to zero degenerate to Sub2API behavior.
- Evidence: Sub2API verified behavior is not top-3 random. The fresh path chooses min priority, min load, then LRU; the routing branch shuffles only exact tied sort groups.
- Confidence: HIGH.
- Why this matters: with default `K=3`, HUAKAI can pick the second or third candidate even when priority/load/last-used differ. That is not a Sub2API-compatible default.
- Fix: either set default `K=1` when optional weights are zero, or define the default band as "exact tie group only" and make broad Top-K opt-in.

### Major Finding 4 - one-api source-verification corrections were re-broadened

- Evidence: `pool-selection-synthesis-v2.md:53` says one-api quota path has gaps `E-OAI-DEEP-001..008`.
- Evidence: `quota-billing-source-verified.md:25` marks `E-OAI-DEEP-001` FALSE.
- Evidence: `quota-billing-source-verified.md:27` marks `E-OAI-DEEP-003` DRIFT.
- Evidence: `quota-billing-source-verified.md:28` marks `E-OAI-DEEP-004` FALSE for text/audio.
- Evidence: `quota-billing-source-verified.md:45-46` says corrected rows requiring ledger work include `001`, `003`, `004`, `009`, and `014`.
- Confidence: HIGH.
- Why this matters: v2 should not import source rows already corrected as false or drifted. That recreates the stale-ledger contamination mechanism from v1.
- Fix: rewrite the one-api summary to keep only verified comparison points: priority bucket/random selection, forced channel override, no durable idempotent claim gate, non-atomic quota mutation, and endpoint-specific reservation/refund differences.

### Major Finding 5 - Tx2 comparison with Sub2API is false

- Evidence: `pool-selection-synthesis-v2.md:199` says Sub2API has "the first two atomic, third detached" for "slot release + Usage Record + claim status".
- Evidence: Sub2API slot handling is cache/concurrency-service based: `gateway_service.go:2250-2254`.
- Evidence: Usage Record is detached/best-effort: `gateway_service.go:7812-7820`.
- Evidence: the atomic transaction is billing claim/effects: `usage_billing_repo.go:35-58`.
- Confidence: HIGH.
- Why this matters: the local HUAKAI improvement is stronger than the text states. The current wording falsely credits Sub2API with atomicity it does not have.
- Fix: say Sub2API has atomic billing claim/effects, detached Usage Record, and cache slot release; HUAKAI unifies slot release, Usage Record, and claim finalization in Tx2 as HUAKAI-DESIGN.

### Major Finding 6 - Release-form metadata is missing

- Evidence: `pool-selection-synthesis-v2.md:3-11` has `Status`, `Author`, `Date`, `Supersedes`, `Inputs`, `Becomes`, and `Owner Q1..Q4`.
- Evidence: it lacks `Feature ID`, `Lane mode`, and `Sources`.
- Evidence: CL-006 requires source/license rows; CL-007 requires lane mode; CL-008 requires feature ID.
- Confidence: HIGH.
- Why this matters: the artifact cannot move to `docs/specs/pool-routing.md` "as-is" and satisfy the strict spec checklist.
- Fix: add strict-spec fields before release: `Feature ID: F-POOL-001`, `Lane mode: Option C`, and `Sources` with E-LIC rows and source-verification input files.

### Major Finding 7 - CL-001 clean-room pressure remains in release-facing prose

- Evidence: `pool-selection-synthesis-v2.md:28`, `37`, `41`, `81`, `172`, and `203-207` carry upstream function/method/config names.
- Evidence: CL-001 says specs must not contain upstream function, method, handler, or configuration-constant names from non-MIT references except where citation form is necessary for verification.
- Confidence: MEDIUM.
- Why this matters: source identifiers are acceptable in reviewer evidence, but the Released implementer spec should use HUAKAI vocabulary and keep source identifiers confined to an evidence appendix if retained.
- Fix: paraphrase implementer-facing sections and keep minimal source locations only in a "Source Evidence Appendix" not consumed as implementation instruction.

### Minor Finding 1 - Sticky wait budget is stated as universally shorter

- Evidence: `pool-selection-synthesis-v2.md:45` says sticky wait limits are "sticky shorter."
- Evidence: source defaults make sticky lower than fallback, but config validation primarily enforces positive values.
- Confidence: MEDIUM.
- Why this matters: this is probably true for defaults, not necessarily a hard source invariant.
- Fix: say "defaults to shorter sticky wait budget" or "uses distinct sticky and fallback wait budgets; default sticky budget is lower."

### Minor Finding 2 - `allow_last_resort` naming is imprecise after LiteLLM correction

- Evidence: `pool-selection-synthesis-v2.md:74` correctly chooses remaining-after-cooldown semantics as HUAKAI-DESIGN.
- Evidence: `pool-selection-synthesis-v2.md:197` still says "LiteLLM-style single-Account exemption" and "until LiteLLM source confirms."
- Confidence: HIGH.
- Why this matters: the source has confirmed a different pattern. The phrase "LiteLLM-style" can mislead implementers into configured-size semantics.
- Fix: rename the HUAKAI behavior to `allow_last_healthy_after_filters` or state that `allow_last_resort` is HUAKAI remaining-after-cooldown semantics, merely inspired by LiteLLM's configured-single guard.

### What's Missing

- No explicit strict spec header fields for `Feature ID`, `Lane mode`, and `Sources`.
- No closed disposition for TODO-2 capability shift.
- No direct replacement text in v2 for the verified negative wait-resume source result.
- No direct source citation for the corrected one-api comparison in the synthesis itself.
- No acceptance-test clarification for HUAKAI default Top-K behavior when optional weights are zero.
- No clear boundary between evidence-only upstream identifiers and implementer-facing HUAKAI behavior names.

### Multi-Perspective Notes

- Executor perspective: an implementer following line 108 could build default Top-K randomization and believe it matches Sub2API defaults. It does not.
- Stakeholder perspective: v2 is materially better than v1, but shipping it as the first Released spec with TODOs would weaken the strict-path precedent.
- Skeptic perspective: the document still trusts input-pass summaries too much in the one-api section. The source-verification report already contains corrections; v2 must not summarize them as an unqualified range.
- Security perspective: no new credential or prompt leakage risk was found in the proposed `routing_reason` forbidden-content list.
- Ops perspective: the HUAKAI improvements are directionally sound, but the default Top-K behavior must be operationally explicit because it changes load distribution and incident debugging.
- New-hire perspective: the artifact is not yet self-contained as a strict spec. It reads like a synthesis memo with references, not an executable spec.

## §5 - Open TODOs in v2

| TODO | v2 text | Release judgment | Required action |
| --- | --- | --- | --- |
| TODO-1 | Verify whether wait resume revalidates schedulability. | Blocks release as written, but source now resolves it as negative. | Replace with verified negative: Sub2API wait helper retries only slot acquisition; HUAKAI adds revalidation on resume. Cite `gateway_helper.go:278-349`. |
| TODO-2 | Locate or refute capability shift pattern in Sub2API. | Blocks release if left open. | Either complete source verification or remove as a source question and state Q4 is pure HUAKAI design with no Sub2API claim. |
| TODO-3 | Read LiteLLM cross-verify report for single-Account exemption verdict. | Stale; blocks release because the same file already consumed the report. | Delete TODO-3. Keep section 3's `DIFFERENT-PATTERN-FOUND` result and correct line 197. |
| TODO-4 | Verify whether `forcePlatform` is platform-only or Account-level. | Blocks release as written, but source resolves it. | Close as platform-level only. Cite `gateway_service.go:2080-2082` and `gateway_handler.go:267-270`. |
| TODO-5 | Verify what `LoadRate` computes. | Blocks release as written, but source resolves it. | Close with formula `(currentConcurrency + waitingCount) * 100 / maxConcurrency`; cite `concurrency_cache.go:416-424`. |

TODO conclusion:

- No open TODO is acceptable in the Released spec.
- TODO-1, TODO-3, TODO-4, and TODO-5 should be closed immediately from already available evidence.
- TODO-2 can remain only if the final verdict is not Released. For Released, it must be either source-verified or explicitly converted into a HUAKAI-design statement with no open source dependency.

## §6 - FINAL VERDICT

Verdict: APPROVE-WITH-FIXES.

Meaning:

- Do not move `pool-selection-synthesis-v2.md` to `docs/specs/pool-routing.md` Status=Released as-is.
- The v1 REJECT blockers are substantially remediated.
- The remaining fixes are bounded enough that I am not issuing REJECT.
- If these fixes are not applied exactly, the verdict downgrades to REJECT because CL-009 and CL-011 would still fail.

### Required Fixes Before Released

1. Add strict spec metadata near `pool-selection-synthesis-v2.md:3-11`.
   - Replacement/addition:
   - `Feature ID | F-POOL-001`
   - `Lane mode | Option C`
   - `Sources | Sub2API (E-LIC-001, LGPL-3.0, commit b0a2252...), one-api (E-LIC-004, MIT, commit 8df4...), LiteLLM (E-LIC-005, MIT, commit 62920...), plus listed source-verification input files.`

2. Replace the Layer 2 sort statement at `pool-selection-synthesis-v2.md:39`.
   - Recommended replacement:
   - `Routing-branch available Accounts use strict ordering by priority, load rate, and last-used timestamp, with shuffle only inside exact tied sort groups (gateway_service.go:1691-1710, 2718-2720). The fresh Layer 2 path uses min-priority, min-load-rate, then LRU selection with random tie handling (gateway_service.go:1879-1883, 2595-2637).`

3. Replace the one-api range statement at `pool-selection-synthesis-v2.md:51-54`.
   - Recommended replacement:
   - `one-api provides a simpler group/model/channel selection baseline: highest-priority eligible bucket, random tie choice, specific-channel override, and retry reselection. Its current source does not provide a durable idempotent claim gate. Corrected one-api gaps for HUAKAI are non-atomic quota mutation, reservation/refund-based duplicate-billing mitigation, endpoint-specific charging windows, and lack of durable request fingerprint; do not cite E-OAI-DEEP-001..008 as a single valid quota-gap range.`

4. Fix Top-K default semantics at `pool-selection-synthesis-v2.md:103-108`.
   - Recommended replacement:
   - `Default compatibility mode uses the Sub2API-equivalent lexicographic/tie-group behavior. Broad Top-K randomization is opt-in by policy; when optional weights are all zero, K defaults to 1 unless all candidates are in the same exact tie group.`

5. Replace the stale LiteLLM last-resort residue at `pool-selection-synthesis-v2.md:197` and delete TODO-3 at line 205.
   - Recommended replacement:
   - `HUAKAI last-healthy opt-in flag (HUAKAI-DESIGN): remaining-after-cooldown semantics, inspired by but not identical to LiteLLM's configured-single-deployment guard. LiteLLM source confirms a different pattern, not a broad last-remaining exemption.`

6. Replace the Tx2 comparison at `pool-selection-synthesis-v2.md:199`.
   - Recommended replacement:
   - `Tx2 atomicity for slot release + Usage Record + claim status is HUAKAI-DESIGN. Sub2API has atomic billing claim/effects, detached Usage Record write, and cache-based slot release; HUAKAI unifies these effects.`

7. Close TODO-1 at `pool-selection-synthesis-v2.md:203`.
   - Recommended replacement:
   - `Verified negative: Sub2API wait resume retries slot acquisition only; it does not rerun the full schedulability gate chain before returning the slot. HUAKAI requires full Phase C revalidation on resume. Evidence: internal/handler/gateway_helper.go:278-349.`

8. Close TODO-4 and TODO-5 at `pool-selection-synthesis-v2.md:206-207`.
   - Recommended replacement:
   - `forcePlatform verified: Sub2API force-platform is platform-level context, not Account-level forcing (gateway_service.go:2080-2082; gateway_handler.go:267-270).`
   - `LoadRate verified: (currentConcurrency + waitingCount) * 100 / maxConcurrency (concurrency_cache.go:416-424).`

9. Resolve TODO-2 at `pool-selection-synthesis-v2.md:204`.
   - Recommended replacement if no source is found:
   - `Capability safe-equivalent behavior is HUAKAI-DESIGN only. No Sub2API source behavior is claimed for capability shift in this spec.`
   - If source is found, add exact file:line evidence and update Q4 framing.

10. Reduce CL-001 identifier exposure across `pool-selection-synthesis-v2.md:28`, `37`, `41`, `81`, `172`, and `203-207`.
   - Recommended method:
   - Keep source identifiers only in a source-evidence appendix.
   - In implementer-facing sections, use HUAKAI vocabulary: lifecycle gate, platform gate, model-support gate, quota gate, window-cost gate, RPM gate, cache-backed slot acquire, forced platform override.

### Realist Check

- Finding 1 stays MAJOR: open TODOs block release, but the fixes are text-only and most have source answers.
- Finding 2 stays MAJOR: a failed line citation is a CL-011 release blocker, but the algorithm direction is recoverable.
- Finding 3 stays MAJOR: default Top-K semantics can cause real routing behavior drift, but it is corrected by one policy sentence and acceptance-test alignment.
- Finding 4 stays MAJOR: stale one-api correction drift can contaminate future specs, but the source-verification report already contains the corrected text.
- Finding 5 stays MAJOR: false atomicity attribution affects design justification, but it does not require changing the HUAKAI design direction.
- Finding 6 stays MAJOR: missing metadata blocks release but is mechanical.
- Finding 7 is MAJOR/PARTIAL: CL-001 pressure is real for Released specs, mitigated by keeping source identifiers in evidence-only appendices.

### Upgrade Conditions

- Upgrade to APPROVE-FOR-RELEASED after all 10 fixes are applied and TODO-2 is closed or relabeled as HUAKAI-DESIGN.
- No further broad regeneration is required if fixes are made precisely.
- If the fixes reveal new source claims, rerun at least 8 citation spot-checks.
- If the author chooses to keep any TODO in the release artifact, final verdict becomes REJECT.

## Appendix A - Assumptions, Pre-Mortem, and Dependency Audit

### Key Assumptions Extracted

| Assumption | Rating | Evidence / concern |
| --- | --- | --- |
| v2 is intended to become the first Released strict spec for F-POOL-001. | VERIFIED | v2 line 10 says it moves to `docs/specs/pool-routing.md` after sign-off. |
| F-POOL-001 is an Option C carve-out. | VERIFIED | Checklist CL-007 covers account-pool routing; parity row exists at `docs/03_FEATURE_PARITY_MATRIX.md:71`. |
| v2 is not allowed to silently drop any reference-derived feature. | VERIFIED | AGENTS.md and project non-negotiables require explicit disposition. |
| v2 can preserve HUAKAI design improvements not found in source if labeled. | VERIFIED | CL-011 permits HUAKAI design improvements when clearly labeled. |
| Open TODOs are acceptable in a Released spec. | FRAGILE / FALSE | CL-009 says Open Questions are a hold signal for implementer lane. |
| Top-K default with zero optional weights is Sub2API-compatible. | FRAGILE / FALSE | Default `K=3` changes selection unless constrained to exact ties. |
| one-api rows `E-OAI-DEEP-001..008` can be summarized as current quota gaps. | FRAGILE / FALSE | Codex one-api reverify marks 001 and 004 false and 003 drifted. |
| `forcePlatform` might be Account-level. | RESOLVED FALSE | Source resolves it as platform-level context. |
| `LoadRate` is still unknown. | RESOLVED FALSE | Source computes current concurrency plus waiting count over max concurrency. |
| LiteLLM source has not confirmed the single-Account exemption. | RESOLVED FALSE | Source confirmed a different configured-single pattern. |

### Pre-Mortem

Assume v2 was executed exactly as written and failed. Specific failure scenarios:

1. Implementer builds default `K=3` broad Top-K randomization and later tests fail because source-inherited Sub2API behavior expected strict min-priority/min-load/LRU.
   - Covered by plan? No.
   - Finding: Major Finding 3.

2. Implementer treats `E-OAI-DEEP-001..008` as valid current one-api source gaps and re-imports false claims into a Released spec.
   - Covered by plan? No.
   - Finding: Major Finding 4.

3. Implementer assumes Sub2API Tx2 already atomically releases slot plus writes Usage Record, and designs HUAKAI comparison around a false baseline.
   - Covered by plan? No.
   - Finding: Major Finding 5.

4. Release manager moves v2 to `docs/specs/pool-routing.md` without adding `Feature ID`, `Lane mode`, or `Sources`, causing checklist non-compliance after the move.
   - Covered by plan? No.
   - Finding: Major Finding 6.

5. Implementer sees TODO-1 and delays implementation despite source already proving a verified negative.
   - Covered by plan? Partially, but stale.
   - Finding: Major Finding 1.

6. Implementer treats `allow_last_resort` as LiteLLM configured-single semantics instead of HUAKAI remaining-after-cooldown semantics.
   - Covered by plan? Partially in section 3, contradicted by line 197.
   - Finding: Minor Finding 2.

7. Clean-room reviewer later blocks release because upstream function and config identifiers remained in implementer-facing sections.
   - Covered by plan? No.
   - Finding: Major Finding 7.

### Dependency Audit

| Dependency | Status | Notes |
| --- | --- | --- |
| Local Sub2API clone exists and matches pinned commit. | PASS | `git rev-parse HEAD` returned `b0a2252ed19c3720e6adafde6083e64fbac2efa9`. |
| Local one-api clone exists and matches pinned commit. | PASS | `git rev-parse HEAD` returned `8df4a2670b98266bd287c698243fff327d9748cf`. |
| Local LiteLLM clone exists and matches pinned commit. | PASS | `git rev-parse HEAD` returned `62920a0cb29f11912edb5bacee470f1b1c044def`. |
| License ledger has Sub2API row. | PASS | `E-LIC-001` exists and marks Sub2API LGPL-3.0. |
| License ledger has one-api row. | PASS | `E-LIC-004` exists and marks one-api MIT. |
| License ledger has LiteLLM row. | PASS | `E-LIC-005` exists and marks LiteLLM MIT. |
| Parity matrix has F-POOL-001 row. | PASS | Row found at `docs/03_FEATURE_PARITY_MATRIX.md:71`. |
| v2 itself links source references to E-LIC rows. | PARTIAL | Ledger has rows, but v2 lacks explicit `Sources` field. |
| v2 has all strict spec release metadata. | FAIL | Missing `Feature ID` and `Lane mode`. |
| v2 has no release-hold TODOs. | FAIL | TODO-1..TODO-5 remain. |

### Ambiguity Risks

- `Top-K with K operator-tunable ... default K=3` can mean broad random pick among top three candidates.
- `If optional weights are 0, HUAKAI degenerates to Sub2API behavior` can mean source-compatible default.
- Risk: both cannot be true unless K collapses to 1 or exact ties only.

- `one-api's quota path has the gaps E-OAI-DEEP-001..008` can mean every row in that range is current source truth.
- It can also mean the historical area of investigation contained quota-related gaps.
- Risk: implementers re-import rows that Codex already marked false/drifted.

- `LiteLLM-style single-Account exemption` can mean configured-single guard.
- It can also mean runtime last-healthy-after-filters guard.
- Risk: product policy differs materially under incident conditions.

- `Sub2API has the first two atomic, third detached` lacks an obvious referent.
- It could refer to billing effects, Usage Record, or slot release.
- Risk: the comparison teaches the wrong baseline.

### Self-Audit

- Major Finding 1 confidence: HIGH. Could author refute with context? No, TODO lines are present and source evidence closes several.
- Major Finding 2 confidence: HIGH. Could author refute with context? Only by redefining "Layer 2" to include routing branch, which v2 does not do.
- Major Finding 3 confidence: HIGH. Could author refute with context? Yes only if "band" means exact tie group; that is not written.
- Major Finding 4 confidence: HIGH. Could author refute with context? No, input report explicitly marks rows false/drifted.
- Major Finding 5 confidence: HIGH. Could author refute with context? No, source clearly separates cache slot release, detached Usage Record, and atomic billing.
- Major Finding 6 confidence: HIGH. Could author refute with context? No, missing fields are visible.
- Major Finding 7 confidence: MEDIUM. Could author refute with process context? Partially, because evidence files can carry source identifiers. Kept as release-facing cleanup, not contamination finding.

## §7 - Owner-Facing Chinese Summary

最终结论：`pool-selection-synthesis-v2.md` 可以 `APPROVE-WITH-FIXES`，但不能 as-is 移到 `docs/specs/pool-routing.md` 并标为 Released。

HUAKAI 还不能把 F-POOL-001 作为第一个 Released spec 直接发出；必须先关闭 TODO、补齐 Option C / Feature ID / Sources 字段，并修正 Layer 2 引用、one-api 纠偏、Top-K 默认语义和 Tx2 对 Sub2API 的错误比较。

最重要的 blocker 是 CL-009/CL-011：v2 仍保留 TODO，而且至少一个源码行号引用不支持原句。

好消息是 v1 的四个核心误归因已经大体修掉了；剩下的问题是小范围、可验证、可一次性修正的 release gate 问题，不需要删除任何功能，也没有发现新的 clean-room 功能缩水风险。
