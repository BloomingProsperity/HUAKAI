# Codex Reviewer-Lane Report — Cycle 1+2 Source-Verified Rewrites

| Field | Value |
| --- | --- |
| Status | Reviewer-lane report |
| Reviewer | Codex reviewer-lane |
| Review date | 2026-04-28 |
| Scope | Cycle 1+2 strict-path decompositions and current pool-selection synthesis |
| Checklist | `docs/specs/_REVIEW_CHECKLIST.md` CL-001..CL-011 |
| Required source commit | Sub2API `b0a2252ed19c3720e6adafde6083e64fbac2efa9` |
| Local source checked | `.omc/reference-src/sub2api` |

## Section 1 — CL-001..CL-011 Verdict Matrix

### Review Protocol Notes

- Pre-commitment prediction 1: the v2 Claude rewrites would fix the obvious v1 hallucinations but still contain unclosed TODOs.
- Actual result: confirmed. Pool v2 has open source-verification TODOs at `pool-selection-claude-v2.md:236-244`.
- Actual result: confirmed. Streaming v2 has open TODOs at `streaming-forwarder-claude-v2.md:333-338`.
- Pre-commitment prediction 2: the current synthesis would still contain v1-derived false convergence claims.
- Actual result: confirmed. The synthesis admits this at `pool-selection-synthesis.md:3` and still repeats false claims at lines 18, 21, 34, 44, 54, 260, and 265.
- Pre-commitment prediction 3: the earlier Codex passes would fail CL-011 because they were deliberately clean-room prose without file:line citations.
- Actual result: confirmed. `pool-selection-codex.md:10` and `streaming-forwarder-codex.md:9-10` cite commits but not behavior-level file:line sources.
- Pre-commitment prediction 4: at least one v2 broad negative claim would be too wide after checking adjacent source.
- Actual result: confirmed. `streaming-forwarder-claude-v2.md:212` and `streaming-forwarder-claude-v2.md:255` say no drain exists, but `bedrock_stream.go:155-169` keeps draining after client disconnect.
- Pre-commitment prediction 5: the review checklist itself would create tension between CL-001 identifier suppression and CL-011 source citation requirements.
- Actual result: confirmed. CL-011 permits function-name citation forms, but CL-001 forbids upstream function names. I treat minimal source-location identifiers as tolerable only in reviewer/specifier evidence sections, not in implementer-facing specs.

### Artifact A — `pool-selection-claude-v2.md`

| Check | Verdict | Evidence |
| --- | --- | --- |
| CL-001 | partial | Contains upstream identifiers and code-level names beyond minimal citation, e.g. `SelectAccountWithLoadAwareness`, `tryAcquireAccountSlot`, `concurrencyService.AcquireAccountSlot`, `StickySessionMaxWaiting`, `FallbackMaxWaiting` at `pool-selection-claude-v2.md:19`, `70-81`, `84-95`. Minimal file:line citations are required by CL-011, but copied code blocks should be reduced before implementer release. |
| CL-002 | pass | No upstream database column/table/migration names were identified as source-derived schema. Local HUAKAI design names like `provider_account_id` are explicitly HUAKAI-side design claims at `pool-selection-claude-v2.md:210`. |
| CL-003 | pass | No upstream UI component, class, or dashboard layout names are present. |
| CL-004 | pass | No copied upstream documentation sentences were found. Source snippets are code evidence, not upstream docs, though they create CL-005 clean-room pressure. |
| CL-005 | partial | The file embeds source-code snippets at `pool-selection-claude-v2.md:53-64`, `71-78`, and `85-92`. For reviewer evidence this is useful; for release to implementer lane it should be paraphrased to guarantees plus file:line citations. |
| CL-006 | partial | Sub2API is cited as a verified commit at `pool-selection-claude-v2.md:11`, but the source row is not explicitly tied to `E-LIC-001`. one-api is cited at `pool-selection-claude-v2.md:139-155` without a license-tier row in the file. |
| CL-007 | pass | Lane is Option C strict for F-POOL-001 at `pool-selection-claude-v2.md:8`; account-pool routing is an Option C carve-out under the checklist. |
| CL-008 | pass | Feature ID `F-POOL-001` appears in the parity matrix at `docs/03_FEATURE_PARITY_MATRIX.md:71`; file references it at `pool-selection-claude-v2.md:9`. |
| CL-009 | pass | Open uncertainty is explicit at `pool-selection-claude-v2.md:236-244`. The TODOs are honest hold signals, not silent claims. |
| CL-010 | pass | No source URL is embedded in Normal Path / Failure Path / Acceptance sections. The file uses local paths, not upstream URLs. |
| CL-011 | partial | Sub2API line citations spot-check mostly verified. However one-api behavioral claims at `pool-selection-claude-v2.md:139-155` lack file:line citations, and TODO-dependent HUAKAI tests at `pool-selection-claude-v2.md:233-234` cannot be treated as source-verified. |

#### Artifact A Evidence Notes

- A1. The Sub2API commit header is correct: `git rev-parse HEAD` returned `b0a2252ed19c3720e6adafde6083e64fbac2efa9`.
- A2. The main function citation exists at `.omc/reference-src/sub2api/backend/internal/service/gateway_service.go:1372-1376`.
- A3. The model-routing layer exists at `.omc/reference-src/sub2api/backend/internal/service/gateway_service.go:1528-1752`.
- A4. The sticky-within-routing layer exists at `.omc/reference-src/sub2api/backend/internal/service/gateway_service.go:1588-1665`.
- A5. The sticky-standalone layer exists at `.omc/reference-src/sub2api/backend/internal/service/gateway_service.go:1754-1803`.
- A6. The load-aware/fresh path exists at `.omc/reference-src/sub2api/backend/internal/service/gateway_service.go:1805-1911`.
- A7. The fallback wait-plan path exists at `.omc/reference-src/sub2api/backend/internal/service/gateway_service.go:1913-1927`.
- A8. The code uses a strict priority/load/LRU selection shape, but `pool-selection-claude-v2.md:28` overstates Layer 2 as `strict lex-sort -> tie-shuffle`; the actual Layer 2 uses min-priority, min-load, then `selectByLRU`.
- A9. `selectByLRU` randomizes only when multiple candidates share the minimum `LastUsedAt` at `.omc/reference-src/sub2api/backend/internal/service/gateway_service.go:2635-2692`.
- A10. `shuffleWithinSortGroups` is real at `.omc/reference-src/sub2api/backend/internal/service/gateway_service.go:2718-2737`, but it is used in the routing-available path, not the fresh Layer 2 loop.
- A11. The wait-plan revalidation TODO is not only unresolved; source inspection shows the wait helper only retries slot acquisition, not schedulability gates, at `.omc/reference-src/sub2api/backend/internal/handler/gateway_helper.go:291-376`.
- A12. Handler paths call `AcquireAccountSlotWithWaitTimeout`, not the line-267 wrapper named in the v2 text: see `.omc/reference-src/sub2api/backend/internal/handler/gateway_handler_chat_completions.go:195-202` and `.omc/reference-src/sub2api/backend/internal/handler/gateway_handler.go:622-629`.
- A13. The file should convert TODO-1 into a verified negative finding: Sub2API wait-resume does not re-run the selector gates.
- A14. The file should add `E-LIC-001` and `E-LIC-004` to its provenance.
- A15. The file should cite one-api source lines or remove the one-api behavior claims from the source-verified pass.

### Artifact B — `streaming-forwarder-claude-v2.md`

| Check | Verdict | Evidence |
| --- | --- | --- |
| CL-001 | partial | Contains upstream identifiers and code snippets such as `detachStreamUpstreamContext`, `defaultMaxLineSize`, `shouldFailoverUpstreamError`, `mergeAnthropicUsage`, and `writeUsageLogBestEffort` at `streaming-forwarder-claude-v2.md:18-20`, `53-56`, `131-141`, `153-162`, `171-180`, `262`. Minimal source-location identifiers are acceptable for CL-011 evidence; copied snippets should not reach implementer-facing specs. |
| CL-002 | pass | No upstream schema column/table/migration names were found. |
| CL-003 | pass | No upstream UI component or class names were found. |
| CL-004 | pass | No copied upstream documentation sentence was found. |
| CL-005 | partial | The file contains direct source snippets at `streaming-forwarder-claude-v2.md:24-35`, `64-120`, `132-141`, `154-162`, `171-180`, and `202-205`. Evidence is readable, but implementer-lane material should retain citations and paraphrase behavior. |
| CL-006 | partial | The header cites Sub2API commit at `streaming-forwarder-claude-v2.md:11` but does not explicitly name `E-LIC-001`. |
| CL-007 | pass | Lane is Option C strict for F-GW-002 at `streaming-forwarder-claude-v2.md:8`; gateway hot path + billing reconciliation is a strict carve-out. |
| CL-008 | pass | Feature ID `F-GW-002` appears in the parity matrix at `docs/03_FEATURE_PARITY_MATRIX.md:38`; file references it at `streaming-forwarder-claude-v2.md:9`. |
| CL-009 | pass | Open uncertainty is explicit at `streaming-forwarder-claude-v2.md:333-338`. |
| CL-010 | pass | No source URL is embedded in implementer-relevant behavior sections. The file uses local paths and no upstream URLs. |
| CL-011 | fail | The cited chat-completions/responses locations mostly verify. However the broad claim `No drain after client disconnect` at `streaming-forwarder-claude-v2.md:212` and `Sub2API has no drain at all` at `streaming-forwarder-claude-v2.md:255` are contradicted by `.omc/reference-src/sub2api/backend/internal/service/bedrock_stream.go:155-169`, which continues reading after a downstream write error to preserve usage. |

#### Artifact B Evidence Notes

- B1. The Sub2API commit header is correct: `git rev-parse HEAD` returned `b0a2252ed19c3720e6adafde6083e64fbac2efa9`.
- B2. `defaultMaxLineSize = 500 * 1024 * 1024` exists at `.omc/reference-src/sub2api/backend/internal/service/gateway_service.go:46`.
- B3. The pre-stream status/failover branch exists at `.omc/reference-src/sub2api/backend/internal/service/gateway_forward_as_chat_completions.go:144-174`.
- B4. The stream-vs-buffered branch exists at `.omc/reference-src/sub2api/backend/internal/service/gateway_forward_as_chat_completions.go:183-187`.
- B5. The streaming scanner loop exists at `.omc/reference-src/sub2api/backend/internal/service/gateway_forward_as_chat_completions.go:369-456`.
- B6. The chat-completions streaming path exits immediately on write failure at `.omc/reference-src/sub2api/backend/internal/service/gateway_forward_as_chat_completions.go:397-400` and `453-454`.
- B7. `detachStreamUpstreamContext` exists and uses `context.WithoutCancel` at `.omc/reference-src/sub2api/backend/internal/service/gateway_service.go:7781-7788`.
- B8. `mergeAnthropicUsage` exists at `.omc/reference-src/sub2api/backend/internal/service/gateway_forward_as_responses.go:200-216` and is last-non-zero-wins per field.
- B9. `shouldFailoverUpstreamError` exists at `.omc/reference-src/sub2api/backend/internal/service/gateway_service.go:3669-3675`.
- B10. The buffered path reads SSE with `bufio.Scanner` and accumulates usage at `.omc/reference-src/sub2api/backend/internal/service/gateway_forward_as_chat_completions.go:212-334`.
- B11. The final `[DONE]` marker exists at `.omc/reference-src/sub2api/backend/internal/service/gateway_forward_as_chat_completions.go:480-482`.
- B12. The file's `TODO-3` at `streaming-forwarder-claude-v2.md:337` is material, not optional; the Bedrock path changes the global truth.
- B13. Bedrock source proves a drain-after-disconnect path: write error sets `clientDisconnected = true` and logs continued draining at `.omc/reference-src/sub2api/backend/internal/service/bedrock_stream.go:155-158`.
- B14. Bedrock then keeps reading events without writing to the client until stream close or interval timeout at `.omc/reference-src/sub2api/backend/internal/service/bedrock_stream.go:107-169`.
- B15. The fix is to scope the negative claim to the Anthropic chat-completions/responses path and add a separate Bedrock KEEP/AVOID entry for unbudgeted drain.

### Artifact C — `pool-selection-codex.md`

| Check | Verdict | Evidence |
| --- | --- | --- |
| CL-001 | pass | The file intentionally omits upstream implementation identifiers at `pool-selection-codex.md:10` and uses HUAKAI vocabulary. |
| CL-002 | pass | No upstream schema/table/migration names were identified. |
| CL-003 | pass | No upstream UI names were identified. |
| CL-004 | pass | No upstream documentation sentences were copied. |
| CL-005 | pass | The algorithm is written as HUAKAI design prose, not line-by-line source translation. |
| CL-006 | partial | The file cites Sub2API and one-api commits at `pool-selection-codex.md:10`, but it does not map those references to `E-LIC-001` / `E-LIC-004`. |
| CL-007 | pass | Lane mode is Option C strict at `pool-selection-codex.md:6`. |
| CL-008 | pass | Feature ID `F-POOL-001` appears at `pool-selection-codex.md:9` and in the parity matrix at `docs/03_FEATURE_PARITY_MATRIX.md:71`. |
| CL-009 | fail | The file presents unsupported source claims as facts, especially continuation affinity at `pool-selection-codex.md:29`, capability shift at `pool-selection-codex.md:35`, and strong-band scoring at `pool-selection-codex.md:33`, without marking them as source uncertainty. |
| CL-010 | pass | No source URL is embedded in implementer-relevant sections. |
| CL-011 | fail | No behavior claim has file:line source citation. Several claims are contradicted by the source-truth correction document and by Claude v2 source checks. |

#### Artifact C Evidence Notes

- C1. `pool-selection-codex.md:29` claims a continuation-affinity first layer.
- C2. Source-truth correction `docs/reviews/2026-04-28-source-truth-corrections.md:21-35` states no continuation-marker layer exists in the verified Sub2API source.
- C3. `pool-selection-codex.md:33` claims score signals, strong-candidate band, and randomization.
- C4. Source-truth correction `docs/reviews/2026-04-28-source-truth-corrections.md:36-57` states no scoring formula/top-K exists in the verified Sub2API selection path.
- C5. `pool-selection-codex.md:35` claims a Sub2API capability-shift pattern.
- C6. Source-truth correction `docs/reviews/2026-04-28-source-truth-corrections.md:82-88` marks that claim unconfirmed.
- C7. `pool-selection-codex.md:57` tells HUAKAI to keep a three-layer continuation/sticky/fresh order.
- C8. Claude v2 corrects the actual source layers to model routing, sticky-within-routing, sticky-standalone, load-aware, fallback wait at `pool-selection-claude-v2.md:21-31`.
- C9. `pool-selection-codex.md:111-115` proposes a score formula as HUAKAI design, which is acceptable as design, but it is not separated from unsupported source inheritance strongly enough.
- C10. This file is valuable as a design brainstorm but cannot be treated as a source-verified specifier pass under CL-011.

### Artifact D — `streaming-forwarder-codex.md`

| Check | Verdict | Evidence |
| --- | --- | --- |
| CL-001 | pass | The file mostly uses HUAKAI vocabulary and omits upstream source identifiers by design at `streaming-forwarder-codex.md:10`. |
| CL-002 | pass | No upstream schema/table/migration names were identified. |
| CL-003 | pass | No upstream UI identifiers were identified. |
| CL-004 | pass | No copied upstream documentation sentence was found. |
| CL-005 | pass | The file is a clean-room design decomposition, not a direct source translation. |
| CL-006 | pass | The file explicitly maps sources to license rows at `streaming-forwarder-codex.md:9`: Sub2API `E-LIC-001`, one-api `E-LIC-004`, New API `E-LIC-002`. |
| CL-007 | pass | Lane mode is Option C strict at `streaming-forwarder-codex.md:7`. |
| CL-008 | pass | Feature ID `F-GW-002` appears at `streaming-forwarder-codex.md:6` and in the parity matrix at `docs/03_FEATURE_PARITY_MATRIX.md:38`. |
| CL-009 | partial | PM open questions are present at `streaming-forwarder-codex.md:180-185`, but source uncertainty is not separated from HUAKAI design uncertainty. |
| CL-010 | pass | No source URL is embedded in implementer-relevant sections. |
| CL-011 | fail | No behavior claim has file:line source citation. The pass predates CL-011 and cannot be released as source-verified. |

#### Artifact D Evidence Notes

- D1. `streaming-forwarder-codex.md:23` claims downstream disconnect can trigger limited upstream drain.
- D2. That claim is partially supported by Bedrock source at `.omc/reference-src/sub2api/backend/internal/service/bedrock_stream.go:155-169`, but not by the chat-completions path.
- D3. `streaming-forwarder-codex.md:38` says downstream write failure can preserve billing evidence through bounded drain.
- D4. The "bounded" part is not shown in Bedrock source; Bedrock stops on stream close or interval timeout, not byte/time/cost drain budgets.
- D5. `streaming-forwarder-codex.md:52-54` correctly frames eight-axis timeout and drain budgets as HUAKAI improvements, but the reference basis lacks line evidence.
- D6. `streaming-forwarder-codex.md:71-74` makes New API claims without source line citations.
- D7. `streaming-forwarder-codex.md:83-86` makes one-api/New API claims without source line citations.
- D8. This file should be retained only as a design input until a CL-011 rewrite cites source lines per reference.

### Artifact E — `pool-selection-synthesis.md`

| Check | Verdict | Evidence |
| --- | --- | --- |
| CL-001 | partial | The synthesis is not a raw source evidence file, but it contains implementation-shaped names and pseudo-schema such as `provider_account_id`, `acquisition_token`, `billing_ledger_claim`, `cap_concurrency`, and `allow_last_resort` at `pool-selection-synthesis.md:62-74`, `80-84`, `131-158`, `214-232`, `261`. These appear to be HUAKAI design names, not upstream names, but they must be checked before moving to `docs/specs/`. |
| CL-002 | partial | No confirmed upstream schema names were identified, but the file is schema-like enough that a specifier should explicitly state these are HUAKAI-local names before release. |
| CL-003 | pass | No upstream UI component/class names were identified. |
| CL-004 | pass | No upstream documentation sentence was copied. |
| CL-005 | partial | The pseudocode at `pool-selection-synthesis.md:131-158` and `166-174` is HUAKAI design, not direct Sub2API translation. It is still too implementation-prescriptive for a release spec unless it is aligned with Quota+Billing schema decisions. |
| CL-006 | partial | It cites evidence rows at `pool-selection-synthesis.md:259-266`, but the stale evidence rows include now-corrected false claims. License rows exist in `docs/07_REFERENCE_EVIDENCE_LEDGER.md`, but source-truth validity is not current. |
| CL-007 | pass | The file declares it becomes the Option C strict Pool Selection spec at `pool-selection-synthesis.md:11`. |
| CL-008 | pass | It is clearly for F-POOL-001 by title and parity matrix row `docs/03_FEATURE_PARITY_MATRIX.md:71`. |
| CL-009 | fail | The banner admits partial correction is required at `pool-selection-synthesis.md:3`, but the body still presents false convergence as established truth at `pool-selection-synthesis.md:16-27`. |
| CL-010 | pass | No upstream source URL is embedded in implementer-relevant sections. |
| CL-011 | N/A | Checklist says CL-011 does not directly apply to synthesis files, but synthesis must inherit citations. This synthesis has not inherited v2 citations and still inherits uncited/false v1/Codex claims; treat as a synthesis release blocker. |

#### Artifact E Evidence Notes

- E1. The file explicitly says partial correction is required at `pool-selection-synthesis.md:3`.
- E2. It still says continuation/sticky/fresh is established truth at `pool-selection-synthesis.md:18`.
- E3. Claude v2 says there is no continuation-marker layer at `pool-selection-claude-v2.md:31`.
- E4. Source-truth correction says no continuation layer exists at `docs/reviews/2026-04-28-source-truth-corrections.md:21-35`.
- E5. It still says top-K score-band randomization is established truth at `pool-selection-synthesis.md:21`.
- E6. Claude v2 says Sub2API has strict lexicographic/min-filter selection, not top-K scoring, at `pool-selection-claude-v2.md:49-67`.
- E7. It still says Sub2API has a capability-shift pattern at `pool-selection-synthesis.md:34` and `265`.
- E8. Claude v2 explicitly marks capability shift unverified at `pool-selection-claude-v2.md:240-241`.
- E9. It still adopts `allow_last_resort` at `pool-selection-synthesis.md:54` and `261`, while Claude v2 says this is LiteLLM-specific and unverified for Sub2API at `pool-selection-claude-v2.md:192` and `241-242`.
- E10. It still cites stale evidence row E-S2A-DEEP-007 at `pool-selection-synthesis.md:260`, but the correction doc says that row's top-K/randomized-scoring content was false for the verified Sub2API source.
- E11. It is not safe to patch a few lines and release because the convergence and sharpens sections are conceptually stale.
- E12. It should be regenerated from source-verified v2 facts plus clearly labeled HUAKAI design deltas.

### Matrix Summary

| Artifact | CL-001 | CL-002 | CL-003 | CL-004 | CL-005 | CL-006 | CL-007 | CL-008 | CL-009 | CL-010 | CL-011 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| pool-selection-claude-v2 | partial | pass | pass | pass | partial | partial | pass | pass | pass | pass | partial |
| streaming-forwarder-claude-v2 | partial | pass | pass | pass | partial | partial | pass | pass | pass | pass | fail |
| pool-selection-codex | pass | pass | pass | pass | pass | partial | pass | pass | fail | pass | fail |
| streaming-forwarder-codex | pass | pass | pass | pass | pass | pass | pass | pass | partial | pass | fail |
| pool-selection-synthesis | partial | partial | pass | pass | partial | partial | pass | pass | fail | pass | N/A |

## Section 2 — Spot-Check Log

### Pool Selection Claude v2 — Random Citation Checks

- Claim: "Source is `SelectAccountWithLoadAwareness` at `pool-selection-claude-v2.md:19`."
- Cited source: `backend/internal/service/gateway_service.go:1376`.
- Grep/read result: Function signature exists at `.omc/reference-src/sub2api/backend/internal/service/gateway_service.go:1376`; preceding comment at lines 1372-1375 names the selection flow.
- Verdict: VERIFIED.

- Claim: "Layer 1 Model Routing starts at lines 1528-1752 at `pool-selection-claude-v2.md:25`."
- Cited source: `backend/internal/service/gateway_service.go:1528-1752`.
- Grep/read result: Line 1528 contains the Layer 1 model-routing comment; lines 1534-1574 filter route accounts by exclusion, schedulability, platform, model, quota, window cost, and RPM; line 1752 closes that section.
- Verdict: VERIFIED.

- Claim: "Sticky-within-routing is active when sticky binding exists and bound account is in routing list at `pool-selection-claude-v2.md:26`."
- Cited source: `backend/internal/service/gateway_service.go:1589-1665`.
- Grep/read result: Lines 1589-1592 require `sessionHash`, `stickyAccountID`, `containsInt64(routingAccountIDs, stickyAccountID)`, and `!isExcluded`; lines 1595-1602 revalidate gates; lines 1605-1616 try acquire and return sticky result.
- Verdict: VERIFIED.

- Claim: "Sticky-standalone runs only with no model routing config at `pool-selection-claude-v2.md:27`."
- Cited source: `backend/internal/service/gateway_service.go:1755-1803`.
- Grep/read result: Line 1755 requires `len(routingAccountIDs) == 0`, non-empty session hash, sticky account, and not excluded; lines 1765-1772 recheck gates; lines 1773-1782 acquire and return.
- Verdict: VERIFIED.

- Claim: "Layer 2 applies seven gates at `pool-selection-claude-v2.md:33-45`."
- Cited source: `backend/internal/service/gateway_service.go:1805-1839`.
- Grep/read result: Lines 1809-1838 check exclusion, schedulability, platform, model support, model selection, quota, window cost, and RPM before appending candidates.
- Verdict: VERIFIED.

- Claim: "Routing path sort is priority, load rate, LastUsedAt, then tie shuffle at `pool-selection-claude-v2.md:51-67`."
- Cited source: `backend/internal/service/gateway_service.go:1691-1710`.
- Grep/read result: Lines 1691-1708 perform priority/load/LastUsedAt stable sort; line 1710 calls `shuffleWithinSortGroups`.
- Verdict: VERIFIED.

- Claim: "Layer 2 also uses the same lex-sort by another name at `pool-selection-claude-v2.md:66-67`."
- Cited source: `backend/internal/service/gateway_service.go:1877-1910`.
- Grep/read result: Lines 1877-1883 filter by minimum priority, minimum load, and LRU; `selectByLRU` at lines 2635-2692 randomizes only when multiple candidates share minimum LastUsedAt.
- Verdict: VERIFIED WITH NUANCE.

- Claim: "Slot acquisition delegates to `concurrencyService.AcquireAccountSlot` at `pool-selection-claude-v2.md:68-81`."
- Cited source: `backend/internal/service/gateway_service.go:2250-2255`.
- Grep/read result: Lines 2250-2255 exactly delegate to `s.concurrencyService.AcquireAccountSlot` when concurrency service exists.
- Verdict: VERIFIED.

- Claim: "The cache interface has `AcquireAccountSlot(ctx, accountID, maxConcurrency, requestID)` at `pool-selection-claude-v2.md:80-81`."
- Cited source: `backend/internal/testutil/stubs.go:24`.
- Grep/read result: Stub method exists at `.omc/reference-src/sub2api/backend/internal/testutil/stubs.go:24-26`; the actual interface is clearer at `.omc/reference-src/sub2api/backend/internal/service/concurrency_service.go:20`.
- Verdict: VERIFIED WITH CITATION IMPROVEMENT NEEDED.

- Claim: "Sticky wait uses sticky timeout/max waiting; fallback wait uses fallback timeout/max waiting at `pool-selection-claude-v2.md:82-95`."
- Cited source: `backend/internal/service/gateway_service.go:1457-1470`, `1740-1745`, `1920-1925`.
- Grep/read result: Sticky wait plans use `cfg.StickySessionWaitTimeout` and `cfg.StickySessionMaxWaiting` at lines 1457-1462 and 1740-1745; fallback wait plan uses `cfg.FallbackWaitTimeout` and `cfg.FallbackMaxWaiting` at lines 1920-1925.
- Verdict: VERIFIED.

- Claim: "Sticky miss reasons are `session_limit`, `wait_queue_full`, `gate_check`, `rpm_red`, `account_cleared` at `pool-selection-claude-v2.md:96-105`."
- Cited source: `backend/internal/service/gateway_service.go:1610`, `1639`, `1644`, `1646`, `1661`.
- Grep/read result: All five reason strings exist at those lines; the log template is at lines 1656-1657 and account-cleared log at lines 1660-1662.
- Verdict: VERIFIED.

- Claim: "Session hash has three-tier derivation at `pool-selection-claude-v2.md:107-115`."
- Cited source: `backend/internal/service/gateway_service.go:648-707`.
- Grep/read result: Lines 653-657 use parsed metadata session ID; lines 660-664 hash cache-control ephemeral content; lines 666-703 build fallback from client IP, user agent, API key ID, system text, and message text.
- Verdict: VERIFIED.

- Claim: "`localExcluded` is used when session limit rejection occurs at `pool-selection-claude-v2.md:117-122`."
- Cited source: `backend/internal/service/gateway_service.go:1426-1452`.
- Grep/read result: Lines 1426-1429 copy excluded IDs; lines 1440-1443 add account ID after failed `checkAndRegisterSession`; lines 1448-1451 add account ID before waiting-plan path when session registration fails.
- Verdict: VERIFIED.

- Claim: "Failover status codes are 401, 403, 429, 529, plus 5xx at `pool-selection-claude-v2.md:123-135`."
- Cited source: `backend/internal/service/gateway_service.go:3669-3676`.
- Grep/read result: Lines 3670-3674 return true for 401/403/429/529 and `statusCode >= 500`.
- Verdict: VERIFIED.

- Claim: "Waiter re-validates on resume is unverified at `pool-selection-claude-v2.md:94-95`, `211-212`, `233-234`, `239-240`."
- Cited source: `internal/handler/gateway_helper.go:267` named by the file.
- Grep/read result: The helper at `.omc/reference-src/sub2api/backend/internal/handler/gateway_helper.go:267-281` only retries slot acquisition; `waitForSlotWithPingTimeout` at lines 291-376 only calls `AcquireAccountSlot`. Handler use at `gateway_handler.go:622-638` binds sticky after acquire but does not re-run selection gates.
- Verdict: VERIFIED NEGATIVE / TODO SHOULD BE CLOSED AS "NOT IN SOURCE".

### Streaming Forwarder Claude v2 — Random Citation Checks

- Claim: "Pre-stream failover branch runs before streaming at `streaming-forwarder-claude-v2.md:22-38`."
- Cited source: `backend/internal/service/gateway_forward_as_chat_completions.go:144-174`.
- Grep/read result: Lines 145-173 handle `resp.StatusCode >= 400`, call `shouldFailoverUpstreamError`, and return `UpstreamFailoverError` before the stream branch at lines 183-187.
- Verdict: VERIFIED.

- Claim: "Stream branch is `clientStream` split at `streaming-forwarder-claude-v2.md:39-50`."
- Cited source: `backend/internal/service/gateway_forward_as_chat_completions.go:183-187`.
- Grep/read result: Lines 183-187 branch to `handleCCStreamingFromAnthropic` or `handleCCBufferedFromAnthropic`.
- Verdict: VERIFIED.

- Claim: "Default scanner buffer is 500 MiB at `streaming-forwarder-claude-v2.md:51-59`."
- Cited source: `backend/internal/service/gateway_service.go:46`.
- Grep/read result: Line 46 defines `defaultMaxLineSize = 500 * 1024 * 1024`; config default also appears at `.omc/reference-src/sub2api/backend/internal/config/config.go:1692`.
- Verdict: VERIFIED.

- Claim: "Streaming loop uses `bufio.Scanner`, configurable MaxLineSize, event/data line parsing at `streaming-forwarder-claude-v2.md:60-120`."
- Cited source: `backend/internal/service/gateway_forward_as_chat_completions.go:369-456`.
- Grep/read result: Lines 369-374 configure scanner; lines 433-456 parse `event:` and `data:` pairs and process each event.
- Verdict: VERIFIED.

- Claim: "Per-event flush happens at `streaming-forwarder-claude-v2.md:122-127`."
- Cited source: `backend/internal/service/gateway_forward_as_chat_completions.go:429`.
- Grep/read result: Line 429 calls `c.Writer.Flush()` after chunk conversion.
- Verdict: VERIFIED.

- Claim: "No drain on chat-completions client disconnect at `streaming-forwarder-claude-v2.md:127-128`."
- Cited source: `backend/internal/service/gateway_forward_as_chat_completions.go:397-400`, `453-454`.
- Grep/read result: `writeChunk` returns true on write error at lines 397-400; `processAnthropicEvent` returning true immediately returns `resultWithUsage()` at lines 453-454.
- Verdict: VERIFIED FOR CHAT-COMPLETIONS PATH ONLY.

- Claim: "`detachStreamUpstreamContext` uses `context.WithoutCancel` at `streaming-forwarder-claude-v2.md:129-150`."
- Cited source: `backend/internal/service/gateway_service.go:7781-7789`.
- Grep/read result: Lines 7781-7788 return `context.WithoutCancel(ctx)` for stream=true.
- Verdict: VERIFIED.

- Claim: "`mergeAnthropicUsage` is last-non-zero-wins at `streaming-forwarder-claude-v2.md:151-167`."
- Cited source: `backend/internal/service/gateway_forward_as_responses.go:200-216`.
- Grep/read result: Lines 204-215 assign each usage field only if incoming value is greater than zero; no taxonomy, conflict detection, or inference exists there.
- Verdict: VERIFIED.

- Claim: "Failover codes are hard-coded at `streaming-forwarder-claude-v2.md:168-183`."
- Cited source: `backend/internal/service/gateway_service.go:3669-3676`.
- Grep/read result: Lines 3670-3674 return true for 401/403/429/529 and all 5xx.
- Verdict: VERIFIED.

- Claim: "Buffered path accumulates usage and returns 502 if no `message_start` at `streaming-forwarder-claude-v2.md:184-198`."
- Cited source: `backend/internal/service/gateway_forward_as_chat_completions.go:212-334`.
- Grep/read result: Lines 252-266 merge `message_start` and `message_delta`; lines 294-296 write 502 if `finalResp == nil`; lines 299-306 overwrite final usage with accumulated fields.
- Verdict: VERIFIED.

- Claim: "`[DONE]` is emitted at normal end at `streaming-forwarder-claude-v2.md:199-208`."
- Cited source: `backend/internal/service/gateway_forward_as_chat_completions.go:481-482`.
- Grep/read result: Lines 481-482 print `data: [DONE]\n\n` and flush.
- Verdict: VERIFIED.

- Claim: "Sub2API has no drain at all at `streaming-forwarder-claude-v2.md:211-212`, `254-255`."
- Cited source: the file cites chat-completions/responses paths, but not Bedrock.
- Grep/read result: `.omc/reference-src/sub2api/backend/internal/service/bedrock_stream.go:155-158` sets `clientDisconnected = true` and logs "continue draining for usage"; lines 163-169 keep processing until timeout/stream end.
- Verdict: DRIFT.

- Claim: "Scanner error warns and exits loop at `streaming-forwarder-claude-v2.md:219-220`, `231-232`."
- Cited source: `backend/internal/service/gateway_forward_as_chat_completions.go:285-292`, `458-465`.
- Grep/read result: Buffered path logs warn at lines 285-292; streaming path logs warn at lines 458-465, then finalizes state and writes final marker if still connected.
- Verdict: VERIFIED WITH NUANCE.

- Claim: "Usage log creation is best-effort at `streaming-forwarder-claude-v2.md:261-262`, `276-277`."
- Cited source: `backend/internal/service/gateway_service.go:7812`.
- Grep/read result: Lines 7812-7835 define `writeUsageLogBestEffort`, try `CreateBestEffort`, and fall back/log on errors.
- Verdict: VERIFIED.

- Claim: "Open TODO says Bedrock drain path is not yet checked at `streaming-forwarder-claude-v2.md:337`."
- Cited source: `backend/internal/service/bedrock_stream.go`.
- Grep/read result: Source exists and contradicts the broad no-drain conclusion; see `bedrock_stream.go:155-169`.
- Verdict: VERIFIED NEGATIVE / TODO IS MATERIAL.

## Section 3 — Findings Ranked CRITICAL / MAJOR / MINOR

### Critical Findings

1. Current pool synthesis is not releasable because it still contains known false source-truth claims while presenting them as convergence.
   - Evidence: `pool-selection-synthesis.md:3` admits "PARTIAL CORRECTION REQUIRED".
   - Evidence: `pool-selection-synthesis.md:18` still says continuation affinity -> sticky -> fresh is established truth.
   - Evidence: `docs/reviews/2026-04-28-source-truth-corrections.md:21-35` says no continuation-marker layer exists in source.
   - Evidence: `pool-selection-synthesis.md:21` still says HUAKAI picks uniformly within a top-K band as convergence.
   - Evidence: `docs/reviews/2026-04-28-source-truth-corrections.md:36-57` says no top-K or scoring formula exists in the verified Sub2API source.
   - Confidence: HIGH.
   - Why this matters: this file is marked as the action plan that becomes the Option C strict spec at `pool-selection-synthesis.md:11`; releasing it would send implementers a source-attributed algorithm that the project already knows is false.
   - Realist check: severity remains CRITICAL because it is a planning artifact that would drive implementation architecture; detection after implementation would require significant rework in selection, tests, and operator diagnostics.
   - Fix: regenerate the synthesis from `pool-selection-claude-v2.md` plus a corrected/CL-011 Codex pass. Remove the false convergence section entirely and replace it with source-truth KEEP, HUAKAI IMPROVE, and AVOID sections.

2. Earlier Codex pool pass fails source-verification and must not be used as an equal source-truth input.
   - Evidence: `pool-selection-codex.md:29` asserts continuation affinity.
   - Evidence: `pool-selection-codex.md:33` asserts score signals and strong-candidate randomization.
   - Evidence: `pool-selection-codex.md:35` asserts Sub2API capability shift.
   - Evidence: `pool-selection-claude-v2.md:31`, `49-67`, and `240-241` correct these as absent or unverified.
   - Confidence: HIGH.
   - Why this matters: the current synthesis repeatedly imports these claims as "Codex sharpenings" at `pool-selection-synthesis.md:34`, `44`, and `260`.
   - Realist check: severity remains CRITICAL because the stale pass is still upstream of the current release candidate synthesis.
   - Fix: mark `pool-selection-codex.md` rejected for CL-011 release purposes; if its HUAKAI design ideas are kept, re-label them as HUAKAI design and add source citations or explicit "not in source" labels.

### Major Findings

1. Streaming Claude v2 overstates "no drain" and misses the Bedrock drain path.
   - Evidence: `streaming-forwarder-claude-v2.md:212` says "No drain after client disconnect".
   - Evidence: `streaming-forwarder-claude-v2.md:255` says "Sub2API has no drain at all".
   - Evidence: `.omc/reference-src/sub2api/backend/internal/service/bedrock_stream.go:155-158` logs client disconnect and continues draining for usage.
   - Evidence: `.omc/reference-src/sub2api/backend/internal/service/bedrock_stream.go:163-169` keeps processing after disconnect until interval timeout or stream end.
   - Confidence: HIGH.
   - Why this matters: the KEEP/IMPROVE/AVOID split changes. HUAKAI's bounded drain is still an improvement, but not because Sub2API has no drain; it improves an existing unbudgeted Bedrock drain pattern.
   - Realist check: downgraded from CRITICAL to MAJOR because the chat-completions path claims were verified and the fix is localized to scope language plus one additional Bedrock section.
   - Mitigated by: the file already flags `bedrock_stream.go` as TODO at `streaming-forwarder-claude-v2.md:337`.
   - Fix: revise all broad "Sub2API has no drain" statements to "Anthropic chat/responses path has no drain; Bedrock path drains after downstream write failure without HUAKAI-style byte/time/cost budgets." Add Bedrock citations.

2. Pool Claude v2 leaves wait-resume revalidation as TODO even though source inspection resolves it as a negative finding.
   - Evidence: `pool-selection-claude-v2.md:94-95`, `211-212`, `233-234`, and `239-240` mark wait-resume revalidation as TODO/UNVERIFIED.
   - Evidence: `.omc/reference-src/sub2api/backend/internal/handler/gateway_helper.go:291-376` only retries slot acquisition.
   - Evidence: `.omc/reference-src/sub2api/backend/internal/handler/gateway_handler.go:622-638` acquires wait slot and binds sticky session, but does not re-run schedulability/model/quota gates.
   - Confidence: HIGH.
   - Why this matters: HUAKAI should still add revalidation as an improvement, but the v2 file should not remain ambiguous before reviewer-lane sign-off.
   - Realist check: MAJOR, not CRITICAL, because v2 already labels the gap honestly and the fix is a factual update.
   - Fix: replace TODO-1 with "Verified negative: Sub2API wait-resume acquires only a slot; HUAKAI adds full revalidation on resume."

3. Pool Claude v2's one-api cross-reference lacks CL-011 file:line citations.
   - Evidence: `pool-selection-claude-v2.md:139-155` summarizes one-api selection and quota behavior.
   - Evidence: no file:line citation accompanies those claims; only source file names appear.
   - Confidence: HIGH.
   - Why this matters: CL-011 says every reference behavior claim must cite a specific source location.
   - Realist check: MAJOR because this blocks source-verification sign-off but does not undermine the Sub2API rewrite core.
   - Fix: either add line citations against `.omc/reference-src/one-api/relay/controller/text.go` and `relay/channeltype/select.go`, or remove the one-api section from the Sub2API v2 pass and leave it to a separate source-verified one-api decomposition.

4. Both Claude v2 files need explicit license-tier linkage in their provenance.
   - Evidence: `pool-selection-claude-v2.md:11` cites Sub2API commit and local clone but not `E-LIC-001`.
   - Evidence: `streaming-forwarder-claude-v2.md:11` cites Sub2API commit but not `E-LIC-001`.
   - Evidence: CL-006 at `docs/specs/_REVIEW_CHECKLIST.md:39-41` requires every source to point to an `E-LIC-NNN` row.
   - Confidence: HIGH.
   - Why this matters: license status is the control that permits behavior-only use and prevents accidental non-MIT source import.
   - Realist check: MAJOR because it blocks checklist sign-off but is easy to fix.
   - Fix: add provenance rows naming `Sub2API (E-LIC-001, LGPL-3.0)`, `one-api (E-LIC-004, MIT)` where applicable, and any other reference with commit.

5. Claude v2 files embed more upstream code than needed for clean-room release.
   - Evidence: `pool-selection-claude-v2.md:53-64`, `71-78`, `85-92`.
   - Evidence: `streaming-forwarder-claude-v2.md:24-35`, `64-120`, `132-141`, `154-162`, `171-180`, `202-205`.
   - Confidence: HIGH.
   - Why this matters: the specifier/reviewer lane can inspect source, but implementer-relevant release specs should not carry direct source code snippets from LGPL reference code.
   - Realist check: MAJOR, not CRITICAL, because these are decomposition files rather than implementation files, and no repo code was copied.
   - Fix: preserve file:line citations and replace code blocks with HUAKAI-language behavior guarantees before any downstream implementer-lane handoff.

6. Current evidence ledger still contains stale rows that contradict source-truth corrections.
   - Evidence: `docs/07_REFERENCE_EVIDENCE_LEDGER.md` grep result shows E-S2A-DEEP-006 and E-S2A-DEEP-007 still describe continuation affinity and randomized top-k selection.
   - Evidence: `docs/reviews/2026-04-28-source-truth-corrections.md:21-57` says those were hallucinated for verified Sub2API source.
   - Confidence: HIGH.
   - Why this matters: synthesis files can keep re-importing corrected falsehoods via evidence IDs even if the v2 decomposition is accurate.
   - Realist check: MAJOR because it contaminates planning artifacts, but the correction document exists and is clear.
   - Fix: add supersession notes or corrected rows to the evidence ledger before regenerating synthesis; do not silently edit history without preserving the correction trail.

7. Streaming Codex pass fails CL-011 and should not be approved as source-verified, even though some design ideas remain useful.
   - Evidence: `streaming-forwarder-codex.md:9-10` cites references and commits but no behavior file:line evidence.
   - Evidence: `streaming-forwarder-codex.md:23`, `38`, `52-54`, `71-86` contain behavior claims spanning Sub2API, one-api, and New API without line citations.
   - Confidence: HIGH.
   - Why this matters: CL-011 was added specifically to prevent second-hand prose from passing as source verification.
   - Realist check: MAJOR because it is an input pass, not the current synthesis, and can be superseded.
   - Fix: reject it for release sign-off; preserve as HUAKAI design brainstorming or rewrite with per-reference citations.

8. Pool synthesis cites stale "Sub2API top-1 score-based routing risks stampede" gap.
   - Evidence: `pool-selection-synthesis.md:260` says `G-REF-2 | Sub2API: top-1 score-based routing risks stampede`.
   - Evidence: Claude v2 source checks show Sub2API uses routing-path tie shuffle and fresh-path min-priority/min-load/LRU, not a top-1 score formula, at `pool-selection-claude-v2.md:49-67`.
   - Confidence: HIGH.
   - Why this matters: the local top-K score band may still be a HUAKAI design choice, but the cited Sub2API gap is wrong.
   - Realist check: MAJOR because the local design could be valid, but the source attribution is invalid.
   - Fix: rewrite G-REF-2 as "Sub2API has limited deterministic/min-filter load-aware selection; HUAKAI adds policy-scored bands as a design improvement" or remove Sub2API attribution.

### Minor Findings

1. `pool-selection-claude-v2.md:80-81` cites `testutil/stubs.go:24` as "the interface"; the actual interface is clearer at `.omc/reference-src/sub2api/backend/internal/service/concurrency_service.go:20`.
   - Fix: cite both or replace "interface" with "stub implementation".

2. `pool-selection-claude-v2.md:28` says "score by load"; Sub2API source uses load rate as an ordering key/filter, not a weighted score.
   - Fix: say "rank/filter by load rate" instead of "score by load".

3. `streaming-forwarder-claude-v2.md:220` says scanner oversize is "impractical"; this is an interpretation, not a source fact.
   - Fix: label as reviewer inference or remove.

4. `streaming-forwarder-claude-v2.md:336` asks for actual deployed `MaxLineSize`; this cannot be source-verified from repo defaults alone.
   - Fix: move to operator/deployment research, not source-verification TODO.

5. `pool-selection-synthesis.md:273` still says reviewer is responsible for CL-001..010, but CL-011 now exists.
   - Fix: update the synthesis workflow text during regeneration to CL-001..011, while preserving CL-011 synthesis N/A nuance.

6. `pool-selection-synthesis.md:50` says "adopts Claude's table verbatim" but the current referenced Claude v2 table is not the same as v1 and has TODO-dependent invariants.
   - Fix: remove "verbatim" and rebuild invariants from v2.

7. Several files use "source-verified" in the title/header while carrying TODOs.
   - Fix: retain "source-verified rewrite" only for verified sections, or add "with open verification gaps" in status.

## Section 4 — Final Verdicts

| Artifact | Verdict | Reason |
| --- | --- | --- |
| pool-selection-claude-v2 | APPROVE-WITH-FIXES | Sub2API core citations largely verify. Fix CL-006 provenance, one-api missing citations, code-snippet clean-room pressure, and close wait-resume TODO as verified negative. |
| streaming-forwarder-claude-v2 | APPROVE-WITH-FIXES | Main chat-completions/responses citations verify. Fix the overbroad no-drain claim by adding Bedrock drain source truth, add E-LIC provenance, and reduce code snippets before implementer handoff. |
| pool-selection-synthesis (current) | REJECT | It still contains admitted partial correction and false v1/Codex-derived convergence claims. Regeneration is required. |
| pool-selection-codex | REJECT | It lacks CL-011 citations and contains false/unverified Sub2API claims now corrected by v2/source-truth review. |
| streaming-forwarder-codex | REJECT | It lacks CL-011 citations and must be treated as design brainstorming, not a source-verified specifier pass. |

### Verdict Detail — Pool Claude v2

- Release status: not ready for Released.
- Reviewer status after fixes: likely approvable.
- Blocking fix 1: convert TODO-1 into a verified negative source finding.
- Blocking fix 2: add line citations or remove one-api section.
- Blocking fix 3: add license-tier provenance.
- Blocking fix 4: scrub direct code blocks before any implementer-facing spec derivation.
- Non-blocking fix: tighten Layer 2 wording from score/tie-shuffle to min-priority/min-load/LRU with random tie only at LRU.

### Verdict Detail — Streaming Claude v2

- Release status: not ready for Released.
- Reviewer status after fixes: likely approvable.
- Blocking fix 1: scope "no drain" to chat-completions/responses path.
- Blocking fix 2: add Bedrock drain KEEP/AVOID evidence.
- Blocking fix 3: add `E-LIC-001` provenance.
- Blocking fix 4: scrub direct code blocks before implementer-facing spec derivation.
- Non-blocking fix: move deployed `MaxLineSize` question out of source-verification checklist.

### Verdict Detail — Current Pool Synthesis

- Release status: rejected.
- Reason: the body still imports stale claims despite the warning banner.
- Required action: regenerate, not patch superficially.
- The Pattern B HUAKAI design choice may be preserved.
- The Q1..Q4 PM decisions may be preserved as HUAKAI policy.
- The convergence and "where X sharpens Y" sections must be rebuilt from corrected sources.
- Evidence row references must avoid stale E-S2A-DEEP rows unless superseded.

### Verdict Detail — Earlier Codex Passes

- Release status: rejected as source-verified specifier files.
- Useful status: can remain as historical brainstorm/source-dive artifacts.
- Required action if retained as input: mark source claims superseded where contradicted.
- Required action if promoted: rewrite under CL-011 with file:line citations.
- Risk: using these as equal source truth reproduces the exact v1 failure mode CL-011 was added to prevent.

## Section 5 — Methodology Recommendation

Regenerate the synthesis files; do not patch the current pool synthesis in place. The current synthesis is structurally organized around stale "Convergence" and "Where Codex/Claude sharpens" sections that treat false v1/Codex claims as load-bearing facts. A patch would have to touch the banner, convergence list, sharpenings, failure taxonomy, reference gaps, tests, provenance, and review-signoff language. That is effectively regeneration with higher risk of missed residue. The safer method is: freeze the current file as rejected history, generate a fresh synthesis from Claude v2 plus any Codex ideas re-labeled as HUAKAI design unless CL-011 citations are added, and only then patch the evidence ledger with supersession notes. Preserve Pattern B and Q1..Q4 if still desired, but re-derive source-attributed KEEP/AVOID from verified lines only.

## Section 6 — Chinese Executive Summary For Owner

第一段：本轮我完成了 CL-001..CL-011 的 reviewer-lane 审查，并抽查了 Claude v2 两个文件各 8 条以上源码引用。结论是：Claude v2 相比 v1 已经大幅修正，Pool Selection 的 Sub2API 核心引用大多能在本地源码 `b0a2252ed19c3720e6adafde6083e64fbac2efa9` 中验证；Streaming 的 chat-completions / responses 路径引用也基本成立。但两份 v2 仍不能直接 Released：Pool 还有未关闭的源码 TODO，Streaming 对 "Sub2API 没有 drain" 的表述过宽，被 `bedrock_stream.go` 反证。

第二段：当前 `pool-selection-synthesis.md` 必须 REJECT。它虽然在第 3 行写了 partial correction required，但正文仍把 continuation layer、top-K scoring、capability shift、last-resort exemption 等已纠正或未验证的内容当成 convergence / sharpenings 继续使用。早期 `pool-selection-codex.md` 和 `streaming-forwarder-codex.md` 也不能作为 source-verified specifier pass 通过，因为没有 CL-011 文件行号引用；其中 Pool Codex 还包含已被 source-truth correction 明确推翻的主张。

第三段：建议 Owner 允许 Claude/Gemini 后续不要在现有 synthesis 上小修小补，而是重新生成 synthesis：以 Claude v2 的已验证事实为 source-truth，把 Codex 旧 pass 中有价值的内容只作为 HUAKAI design idea，除非补齐 CL-011 引用；同时更新 evidence ledger 的 supersession 说明，避免 stale E-S2A-DEEP 行继续污染后续规格。没有发现需要删除功能的情况；风险是 attribution 和 clean-room 交付风险，不是功能缩水。需要 Owner 确认的主要点是：是否接受 "regenerate synthesis instead of patch in place" 作为本轮返工策略。

## Appendix A — Self-Audit And Realist Check

- Self-audit item 1: CRITICAL finding on synthesis false convergence has hard evidence from synthesis lines and source-truth correction lines.
- Self-audit result: keep scored.
- Refutable by missing context: no, because the synthesis itself admits partial correction and the correction doc lists the false claims.
- Preference vs flaw: flaw.

- Self-audit item 2: CRITICAL finding on Codex pool pass as source truth has hard evidence from file lines and v2 corrections.
- Self-audit result: keep scored.
- Refutable by missing context: no, because CL-011 requires citations and the file does not contain them.
- Preference vs flaw: flaw.

- Self-audit item 3: MAJOR finding on Bedrock drain path has hard source evidence.
- Self-audit result: keep scored.
- Refutable by missing context: no, but scope matters.
- Preference vs flaw: flaw.
- Realist recalibration: downgraded from CRITICAL to MAJOR because the false claim is localized and the file already has TODO-3.

- Self-audit item 4: MAJOR finding on v2 code snippets has checklist evidence but some process ambiguity.
- Self-audit result: keep as MAJOR, not CRITICAL.
- Refutable by missing context: partially, because specifier-lane evidence files may carry more source detail than implementer specs.
- Preference vs flaw: flaw under clean-room release gate, not style.

- Self-audit item 5: CL-001 partial statuses for source identifiers could be disputed because CL-011 allows function-name citation forms.
- Self-audit result: keep as partial, not fail.
- Refutable by missing context: yes, because project may treat specifier evidence sections differently.
- Preference vs flaw: release hygiene flaw, not source-truth defect.

## Appendix B — Dependency Audit

- Dependency 1: Reviewer-lane sign-off depends on local Sub2API clone at the pinned commit.
- Status: satisfied.
- Evidence: `git -C .omc/reference-src/sub2api rev-parse HEAD` returned `b0a2252ed19c3720e6adafde6083e64fbac2efa9`.

- Dependency 2: F-POOL-001 must exist in parity matrix.
- Status: satisfied.
- Evidence: `docs/03_FEATURE_PARITY_MATRIX.md:71`.

- Dependency 3: F-GW-002 must exist in parity matrix.
- Status: satisfied.
- Evidence: `docs/03_FEATURE_PARITY_MATRIX.md:38`.

- Dependency 4: Sub2API license tier must exist.
- Status: satisfied in ledger, not fully linked in Claude v2 files.
- Evidence: `docs/07_REFERENCE_EVIDENCE_LEDGER.md` contains `E-LIC-001 | Sub2API | LGPL-3.0`.

- Dependency 5: Earlier synthesis must not be sent to implementer lane until partial correction is resolved.
- Status: not satisfied.
- Evidence: `pool-selection-synthesis.md:3`, `273-283`.

## Appendix C — Ambiguity Risks

- Ambiguity: `pool-selection-claude-v2.md:250` says upstream identifiers appear only because this is a specifier-lane file.
- Interpretation A: reviewer accepts identifiers in decomposition evidence sections.
- Interpretation B: CL-001 applies literally to every decomposition file.
- Risk if wrong: either unnecessary rewrite churn, or contaminated implementer-facing material.
- Reviewer position: accept minimal citations for reviewer evidence, but scrub code snippets and unnecessary function names before release.

- Ambiguity: `streaming-forwarder-claude-v2.md:212` says no drain after client disconnect.
- Interpretation A: scoped to the chat-completions path under discussion.
- Interpretation B: global Sub2API behavior.
- Risk if wrong: HUAKAI loses a real Bedrock behavior lesson and misattributes bounded drain as purely novel.
- Reviewer position: current wording reads global in sections 2 and 4; fix required.

- Ambiguity: `pool-selection-synthesis.md:245` says PM decisions are binding.
- Interpretation A: Q1..Q4 policy choices survive source-truth correction.
- Interpretation B: the whole synthesis is binding despite correction banner.
- Risk if wrong: implementer follows false convergence claims.
- Reviewer position: Q1..Q4 can survive, but the synthesis is rejected.

## Appendix D — What Is Genuinely Solid

- Claude v2 pool rewrite correctly removes continuation-marker inheritance as a Sub2API source fact.
- Claude v2 pool rewrite correctly replaces score/top-K claims with priority/load/LRU ordering for the verified Sub2API path.
- Claude v2 pool rewrite correctly identifies cache-backed slot accounting as a Sub2API weakness and HUAKAI's row-locked acquisition as a design improvement.
- Claude v2 streaming rewrite correctly fixes the scanner default from 1 MiB to 500 MiB.
- Claude v2 streaming rewrite correctly identifies no mid-stream failover hook in the chat-completions streaming path.
- Claude v2 streaming rewrite correctly identifies `mergeAnthropicUsage` as last-non-zero-wins without reconciliation taxonomy.
- The current synthesis's Pattern B rationale remains a plausible HUAKAI design decision, but it must be detached from stale Sub2API attribution.

## Appendix E — Required Fix Checklist Before Re-Review

- Pool Claude v2: add `E-LIC-001` and `E-LIC-004` provenance.
- Pool Claude v2: add one-api file:line citations or remove one-api section.
- Pool Claude v2: replace wait-resume TODO with verified negative evidence from `gateway_helper.go` and handler paths.
- Pool Claude v2: reduce upstream code blocks to source-location citations plus behavior summaries.
- Pool Claude v2: tighten Layer 2 wording around min-priority/min-load/LRU and random tie behavior.
- Streaming Claude v2: add `E-LIC-001` provenance.
- Streaming Claude v2: add Bedrock source section with `bedrock_stream.go:155-169`.
- Streaming Claude v2: replace "Sub2API has no drain at all" with path-scoped truth.
- Streaming Claude v2: reduce upstream code blocks to behavior summaries.
- Streaming Claude v2: classify `MaxLineSize` deployed value as deployment research, not source TODO.
- Pool Codex: mark as superseded/rejected for source-verified release.
- Streaming Codex: mark as superseded/rejected for source-verified release.
- Evidence ledger: add supersession/correction rows for stale E-S2A-DEEP continuation/top-K/drain claims.
- Pool synthesis: regenerate from corrected inputs.
- Pool synthesis: update review-signoff language to CL-001..CL-011.
- Pool synthesis: preserve Q1..Q4 only as HUAKAI policy, not source convergence.
- Pool synthesis: preserve Pattern B only as HUAKAI design, not Sub2API inheritance.
- Pool synthesis: remove false G-REF-2 and G-REF-7 wording.
- Pool synthesis: rebuild tests from verified source facts and HUAKAI-design labels.
- Re-review: spot-check at least 8 citations again after regeneration.
