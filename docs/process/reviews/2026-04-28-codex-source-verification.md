# Codex Source Verification Review - Cycle 1+2 F-POOL-001 + F-GW-002
Date: 2026-04-28
Reviewer: Codex
Scope:
- `docs/decompositions/_cross-cutting/pool-selection-codex.md`
- `docs/decompositions/_cross-cutting/streaming-forwarder-codex.md`
- `docs/decompositions/_cross-cutting/pool-selection-claude-v2.md`
- `docs/decompositions/_cross-cutting/streaming-forwarder-claude-v2.md`
Local sources verified:
- Sub2API: `.omc/reference-src/sub2api/` at `b0a2252ed19c3720e6adafde6083e64fbac2efa9`
- one-api: `.omc/reference-src/one-api/` at `8df4a2670b98266bd287c698243fff327d9748cf`
- New API: `.omc/reference-src/new-api/` at `fc377dae3e3994dc4b076e678704e1c5ef7a5e90`
Pre-commitment predictions:
- Old Codex passes would fail CL-011 because they intentionally omitted file:line evidence.
- Pool-selection drift would cluster around Sub2API layer order, scoring, capability fallback, and wait semantics.
- Streaming drift would cluster around disconnect drain, usage completeness, and failure taxonomy.
- Claude v2 would be mostly source-grounded but still have scope or citation precision defects.
- Actual result: all four predictions were confirmed.
## 1. Codex Self-Verification
### 1.1 `pool-selection-codex.md` status
Verdict: DRIFT PRESENT.
The pass is not CL-011 compliant. It says at line 10: `Source paths, implementation identifiers, schema names, comments, and code structure are intentionally omitted.`
That makes the file unusable as a source-verified specifier pass without revision.
Every retained behavior claim must receive local file:line evidence or be relabeled as HUAKAI design.
### 1.2 DRIFT-POOL-CODEX-01 - Continuation affinity layer
Codex claim:
- Sub2API first tries continuation affinity from a continuation marker.
Source finding:
- I did not verify any continuation marker layer in Sub2API selection.
- `backend/internal/service/gateway_service.go:1376` defines `SelectAccountWithLoadAwareness`.
- Its inputs include session hash, requested model, excluded IDs, metadata user ID, and Sub2API user ID.
- It does not take a continuation marker.
Verified sequence:
- `gateway_service.go:1528`: model routing branch.
- `gateway_service.go:1589-1665`: sticky behavior inside model routing.
- `gateway_service.go:1755-1803`: standalone sticky path.
- `gateway_service.go:1805-1911`: load-aware selection.
- `gateway_service.go:1913-1925`: final wait-plan fallback.
Conclusion:
- DRIFT.
- Continuation affinity must be HUAKAI design or roadmap, not Sub2API KEEP.
### 1.3 DRIFT-POOL-CODEX-02 - Three-layer order
Codex claim:
- `Keep the three-layer selection order: continuation affinity, sticky session affinity, fresh pooled selection.`
Source finding:
- The source-backed order is not continuation -> sticky -> fresh pool.
- The first major layer is model routing.
- Sticky is conditional.
- Load-aware selection follows.
- Wait-plan fallback may be returned.
Evidence:
- `gateway_service.go:1528`
- `gateway_service.go:1589-1665`
- `gateway_service.go:1755-1803`
- `gateway_service.go:1805-1911`
- `gateway_service.go:1913-1925`
Conclusion:
- DRIFT.
- The synthesis must not reuse the old Codex layer ordering.
### 1.4 DRIFT-POOL-CODEX-03 - Strong-candidate scoring band
Codex claim:
- Sub2API evaluates signal scores, keeps a strong-candidate band, and randomizes within it.
Source finding:
- I did not verify weighted signal scoring or strong-band randomization.
- The routing branch sorts by priority, load rate, last-used timestamp, then shuffles equal sort groups.
- The load-aware branch filters by minimum priority, minimum load rate, then selects LRU.
Evidence:
- `gateway_service.go:1691-1710`: priority/load/last-used ordering and tie shuffle.
- `gateway_service.go:1879-1883`: min-priority, min-load, LRU sequence.
- `gateway_service.go:2595-2637`: helper functions for those choices.
- `gateway_service.go:2718-2720`: shuffle within equal sort groups.
Conclusion:
- DRIFT.
- Strong-band scoring is HUAKAI design unless another source is cited.
### 1.5 DRIFT-POOL-CODEX-04 - Capability shift / safe equivalent
Codex claim:
- Sub2API has a capability shift pattern from exact-native capability to safe equivalent.
Source finding:
- I did not verify a general capability exact/safe-equivalent fallback in Sub2API selection.
- The source verifies model mapping, platform filtering, model support checks, and channel restrictions.
- That is not the same as the claimed capability shift model.
Evidence:
- `gateway_service.go:1534-1574`
- `gateway_service.go:1808-1839`
- `gateway_service.go:3484-3505`
Conclusion:
- DRIFT.
- Capability safe-equivalent should be HUAKAI Route policy, not Sub2API KEEP.
### 1.6 DRIFT-POOL-CODEX-05 - Wait resume revalidation
Codex claim:
- Queued or busy-affinity requests should re-enter admission before provider call.
Source finding:
- Bounded wait plans are verified.
- Revalidation after wait resume was not verified.
Evidence for wait plans:
- `gateway_service.go:1630-1635`
- `gateway_service.go:1740-1745`
- `gateway_service.go:1792-1797`
- `gateway_service.go:1920-1925`
Evidence for unresolved resume behavior:
- `backend/internal/handler/gateway_handler_chat_completions.go:195-202`
- `backend/internal/handler/gateway_handler.go:386-392`
- `backend/internal/handler/gateway_helper.go:375-376`
Conclusion:
- PARTIAL DRIFT.
- Bounded wait is source-backed; wait-resume revalidation must be HUAKAI invariant until source-proven.
### 1.7 DRIFT-POOL-CODEX-06 - Diagnostics taxonomy
Codex claim:
- Sub2API reports categories such as disabled, unsupported, temporarily unhealthy, model-ineligible, and operational-limit exclusions.
Source finding:
- Diagnostics exist, but the old taxonomy is not source-exact.
- Verified buckets include excluded, unschedulable, platform-filtered, model unsupported, and model rate-limited.
Evidence:
- `gateway_service.go:3416`
- `gateway_service.go:3484-3505`
Conclusion:
- PARTIAL DRIFT.
- Replace invented category names or label them as HUAKAI taxonomy.
### 1.8 DRIFT-POOL-CODEX-07 - Live signal list
Codex claim:
- Selection considers current health, current load, first-response latency, error trend, and quota headroom.
Source finding:
- Verified: lifecycle/schedulability, platform, model support, model rate limit, quota, window cost, RPM, concurrency load, and LRU.
- Not verified as selection scoring inputs: first-response latency, error trend, and broad quota-headroom scoring.
Evidence:
- `gateway_service.go:1534-1574`
- `gateway_service.go:1763`
- `gateway_service.go:1777`
- `gateway_service.go:1808-1839`
- `gateway_service.go:1879-1883`
Conclusion:
- PARTIAL DRIFT.
- Split source-backed hard gates from HUAKAI desired scoring signals.
### 1.9 Pool-selection claims verified but uncited
These old Codex claims are broadly source-backed, but still need citations:
- Sub2API has sticky session behavior with revalidation before immediate use.
- Sub2API uses per-request excluded account IDs.
- Sub2API can return bounded wait plans.
- Sub2API uses load/concurrency information in selection.
- one-api is a simpler group/model/channel routing baseline.
- one-api supports forced channel routing.
- one-api uses priority/random channel selection.
- one-api retries by selecting another eligible channel after provider failure.
- one-api quota handling is split rather than HUAKAI-style atomic Tx.
### 1.10 `streaming-forwarder-codex.md` status
Verdict: DRIFT PRESENT.
The pass is not CL-011 compliant. It says at line 10: `Behavior only. No reference source, file paths, function names, schema names, comments, or tests are copied into this document.`
Every retained behavior claim needs a citation or a HUAKAI-design label.
### 1.11 DRIFT-GW-CODEX-01 - Disconnect drain
Codex claim:
- Downstream write failure can preserve billing evidence through bounded drain.
Source finding:
- In the inspected Sub2API Anthropic chat-completions conversion path, client disconnect stops the loop and closes upstream.
- There is no drain loop in that path.
- Other Sub2API passthrough paths do have queue/drain-like behavior.
Evidence for no drain in the Anthropic conversion path:
- `backend/internal/service/gateway_forward_as_chat_completions.go:389-399`
- `gateway_forward_as_chat_completions.go:433-455`
- `gateway_forward_as_chat_completions.go:142`
- `gateway_service.go:7781-7788`
Evidence for different passthrough paths:
- `gateway_service.go:5054`
- `gateway_service.go:5147-5163`
- `gateway_service.go:6815-6817`
- `gateway_service.go:7069-7098`
Conclusion:
- PARTIAL DRIFT.
- Drain behavior must be scoped by stream path.
### 1.12 DRIFT-GW-CODEX-02 - Bounded upstream read queues
Codex claim:
- Slow-client handling uses blocking writes and bounded upstream read queues.
Source finding:
- The Anthropic conversion path uses scanner reads and direct writes.
- I did not verify a bounded upstream queue in that path.
- Bounded event queues exist in other Sub2API passthrough paths.
- New API has a bounded scanner channel.
Evidence:
- `gateway_forward_as_chat_completions.go:223-227`
- `gateway_forward_as_chat_completions.go:369-374`
- `gateway_forward_as_chat_completions.go:389-399`
- `gateway_service.go:5054`
- `gateway_service.go:6815-6817`
- `new-api/relay/helper/stream_scanner.go:185`
Conclusion:
- PARTIAL DRIFT.
- Split the claim by reference and stream path.
### 1.13 DRIFT-GW-CODEX-03 - Raw chunk / media-like classification
Codex claim:
- Sub2API classifies SSE, chunked JSON, and raw chunk streams for media-like bodies.
Source finding:
- The verified Anthropic conversion path parses SSE-style `event:` and `data:` lines.
- I did not verify raw chunk media classification in that path.
Evidence:
- `gateway_forward_as_chat_completions.go:433-450`
Conclusion:
- PARTIAL DRIFT.
- Raw chunk behavior must get its own source citation or become HUAKAI design.
### 1.14 DRIFT-GW-CODEX-04 - Usage axes
Codex claim:
- Usage extraction covers cache, thinking/reasoning, media, and terminal usage metadata.
Source finding:
- Verified for the inspected helper: input, output, cache read, and cache creation.
- Not verified there: media/tool usage axes.
- Thinking/reasoning may exist in content conversion, but I did not verify it as a usage accumulator axis.
Evidence:
- `gateway_forward_as_responses.go:200-216`
Conclusion:
- PARTIAL DRIFT.
- Keep source-backed usage axes separate from HUAKAI's richer usage vector.
### 1.15 DRIFT-GW-CODEX-05 - Usage completeness taxonomy
Codex claim:
- Terminal markers and missing terminal evidence drive usage-complete versus usage-partial.
- Usage source states are reported / normalized / inferred / partial.
Source finding:
- Sub2API emits `[DONE]` after scanner completion in the verified path.
- I did not verify the HUAKAI usage-source taxonomy in Sub2API.
- New API has stream end reasons, but that is not identical to HUAKAI accounting state.
Evidence:
- `gateway_forward_as_chat_completions.go:480-482`
- `new-api/relay/common/stream_status.go:15-31`
Conclusion:
- DRIFT for Sub2API attribution.
- Partial external inspiration from New API only.
### 1.16 DRIFT-GW-CODEX-06 - Failure taxonomy
Codex claim:
- Rate-limit, auth, quota, overload, malformed, compatibility correction, and protocol violation drive routing/cooldown decisions.
Source finding:
- Verified failover statuses are narrower: 401, 403, 429, 529, and 5xx.
- Pre-stream upstream status handling exists.
- The broad HUAKAI failure taxonomy is not source-backed as Sub2API behavior.
Evidence:
- `gateway_service.go:3669-3674`
- `gateway_forward_as_chat_completions.go:145-174`
Conclusion:
- PARTIAL DRIFT.
- Keep verified status behavior separate from HUAKAI failure taxonomy.
### 1.17 Streaming claims verified but uncited
These old Codex claims are broadly source-backed, but still need citations:
- Sub2API parses provider stream events in the inspected conversion path.
- Sub2API captures explicit upstream usage metadata where present.
- Sub2API can return accumulated usage after client disconnect in that path.
- Sub2API detaches upstream context from request cancellation.
- one-api requests provider stream usage where supported.
- one-api scans event lines, forwards valid events, captures usage, and estimates fallback usage.
- one-api reconciles quota after response.
- New API has explicit stream end reasons.
- New API has a bounded scanner channel.
## 2. Spot-Check of Claude v2
### 2.1 Spot-check summary
Spot-check count: 14 citation/claim checks.
Result:
- 11 checks matched the claimed behavior.
- 2 checks were imprecise or over-generalized.
- 1 check exposed a path-scope gap.
### 2.2 Verified Claude v2 checks
Check 01:
- Claim: `gateway_service.go:1376-1928` contains `SelectAccountWithLoadAwareness`.
- Result: VERIFIED at `gateway_service.go:1376`.
Check 02:
- Claim: no continuation marker layer in Sub2API selection.
- Result: VERIFIED by the inputs at `gateway_service.go:1376`.
Check 03:
- Claim: model routing precedes later sticky/load-aware behavior.
- Result: VERIFIED at `gateway_service.go:1528`.
Check 04:
- Claim: sticky candidates are revalidated and can fail for exclusion, schedulability, platform/model, and rate-limit reasons.
- Result: VERIFIED at `gateway_service.go:1610`, `1625`, `1639`, `1644`, `1646`, `1656`, and `1661`.
Check 05:
- Claim: routing branch orders by priority/load/last-used and tie shuffle.
- Result: VERIFIED at `gateway_service.go:1691-1710` and `2718-2720`.
Check 06:
- Claim: load-aware branch filters by min priority, min load rate, then LRU.
- Result: VERIFIED at `gateway_service.go:1879-1883` and `2595-2637`.
Check 07:
- Claim: wait plans are returned from several busy paths.
- Result: VERIFIED at `gateway_service.go:1630-1635`, `1740-1745`, `1792-1797`, and `1920-1925`.
Check 08:
- Claim: wait-resume revalidation remains TODO/UNVERIFIED.
- Result: VERIFIED as a review statement; I also did not find that revalidation in `gateway_helper.go`.
Check 09:
- Claim: pre-stream upstream non-2xx handling can return failover error.
- Result: VERIFIED at `gateway_forward_as_chat_completions.go:145-174`.
Check 10:
- Claim: streaming branch is selected in the inspected path.
- Result: VERIFIED at `gateway_forward_as_chat_completions.go:183-187`.
Check 11:
- Claim: scanner buffer / max-line behavior exists.
- Result: VERIFIED at `gateway_service.go:46`, `gateway_forward_as_chat_completions.go:223-227`, and `369-374`.
Check 12:
- Claim: no drain after client disconnect in the Anthropic conversion path.
- Result: VERIFIED for that path at `gateway_forward_as_chat_completions.go:389-399`, `433-455`, and `142`.
Check 13:
- Claim: usage merge helper captures Anthropic-style usage blocks.
- Result: VERIFIED at `gateway_forward_as_responses.go:200-216`.
Check 14:
- Claim: failover status logic includes 401, 403, 429, 529, and 5xx.
- Result: VERIFIED at `gateway_service.go:3669-3674`.
### 2.3 CLAUDE-V2-DRIFT-POOL-01 - Slot acquisition citation precision
Claude v2 claim:
- `testutil/stubs.go:24` shows the interface as `AcquireAccountSlot(ctx, accountID, maxConcurrency, requestID)`.
Source finding:
- The behavioral direction is mostly correct, but the citation is imprecise.
- The service-level call used by `tryAcquireAccountSlot` is `concurrencyService.AcquireAccountSlot(ctx, accountID, maxConcurrency)` at `gateway_service.go:2250-2255`.
- The request ID is generated inside `service/concurrency_service.go:129-152` before the cache-level primitive is called.
Conclusion:
- CLAUDE-V2-DRIFT.
- Low product risk, but a CL-011 citation-quality defect.
Required fix:
- Cite service-level and cache-level acquisition separately.
### 2.4 CLAUDE-V2-DRIFT-GW-01 - Best-effort usage log wording
Claude v2 claim:
- Sub2API usage record creation is `fire-and-forget`.
Source finding:
- Usage logging is best-effort and non-atomic with billing.
- But I did not verify a goroutine-style fire-and-forget operation in the inspected helper.
- It is better described as best-effort, detached-context, and separate from billing.
Evidence:
- `gateway_service.go:7812-7820`
- `gateway_service.go:8015-8017`
- `gateway_service.go:8022-8038`
Conclusion:
- CLAUDE-V2-DRIFT.
- The HUAKAI conclusion still holds, but the wording should be corrected.
### 2.5 CLAUDE-V2-SCOPE-GAP-01 - Global no-drain statement
Claude v2 claim:
- `Sub2API has no drain at all`.
Source finding:
- Correct for the inspected Anthropic conversion path.
- Not correct globally across all Sub2API streaming paths.
Evidence:
- No drain in conversion path: `gateway_forward_as_chat_completions.go:389-399`, `433-455`, `142`.
- Queue/drain-like passthrough behavior: `gateway_service.go:5054`, `5147-5163`, `6815-6817`, `7069-7098`.
Conclusion:
- SCOPE GAP.
- Synthesis must say "no drain in the inspected conversion path", not "no drain in Sub2API".
### 2.6 Source-verified behavior Claude v2 missed
Missed behavior:
- Sub2API has model/channel restriction and fallback-group logic around selection.
- Evidence: `gateway_service.go:1344-1350`, `1397-1403`, `8260-8346`.
Missed behavior:
- Sub2API has legacy/no-load-batch behavior in addition to the load-aware path.
- Evidence: `gateway_service.go:1430-1470`, `1835-1857`, and later legacy selection helpers.
Missed behavior:
- Sub2API has an Antigravity single-account special case.
- Evidence: `gateway_service.go:2194-2203`.
Missed behavior:
- Sub2API passthrough stream paths can continue reading after client disconnect under queue/drain-like constraints.
- Evidence: `gateway_service.go:5054`, `5147-5163`, `6815-6817`, `7069-7098`.
Missed behavior:
- one-api retry excludes the immediately failed channel in the inspected path, not a durable full failed-set model.
- Evidence: `one-api/controller/relay.go:77-88`.
Missed behavior:
- New API stream status tracking is a useful separate reference for HUAKAI outcome taxonomy.
- Evidence: `new-api/relay/common/stream_status.go:15-31` and `new-api/relay/helper/stream_scanner.go:185`.
## 3. CL-011 / CL-011a / CL-011b Verdict Matrix
Legend:
- PASS: requirement satisfied.
- PARTIAL: useful work exists, but required rigor is incomplete.
- FAIL: requirement not satisfied.
| File | CL-011: every behavior claim has source citation | CL-011a: local clone reference | CL-011b: KEEP/IMPROVE/AVOID separation |
| --- | --- | --- | --- |
| `pool-selection-codex.md` | FAIL | PARTIAL | FAIL |
| `streaming-forwarder-codex.md` | FAIL | PARTIAL | FAIL |
| `pool-selection-claude-v2.md` | PARTIAL | PASS | PASS |
| `streaming-forwarder-claude-v2.md` | PARTIAL | PASS | PARTIAL |
`pool-selection-codex.md`:
- CL-011 FAIL: no file:line citations for behavior claims.
- CL-011a PARTIAL: cited references are locally cloned now, but the pass does not record local clone paths.
- CL-011b FAIL: KEEP blends reference behavior with unverified HUAKAI design, especially continuation affinity, capability shift, and strong-band scoring.
`streaming-forwarder-codex.md`:
- CL-011 FAIL: no file:line citations for behavior claims.
- CL-011a PARTIAL: cited references are locally cloned now, but the pass does not record local clone paths.
- CL-011b FAIL: KEEP/IMPROVE labels exist, but source behavior is blended across paths and references.
`pool-selection-claude-v2.md`:
- CL-011 PARTIAL: Sub2API lines are strong; one-api claims and one slot-acquisition boundary need better citations.
- CL-011a PASS: local clone path and pinned Sub2API commit are present.
- CL-011b PASS: unverified claims are mostly marked TODO instead of blended into KEEP.
`streaming-forwarder-claude-v2.md`:
- CL-011 PARTIAL: main Sub2API path is well cited; no-drain and usage-log wording require correction.
- CL-011a PASS: pinned Sub2API commit was locally verified.
- CL-011b PARTIAL: HUAKAI additions are mostly labeled, but global no-drain wording can pollute the separation.
## 4. Action Items
### 4.1 Fix `pool-selection-codex.md`
1. Replace old three-layer ordering with source-verified model routing / sticky / load-aware / wait-plan sequence.
2. Move continuation affinity to HUAKAI design or roadmap.
3. Move capability exact/safe-equivalent/reject to HUAKAI Route policy.
4. Replace strong-band scoring with source-backed priority/load/LRU/tie-shuffle behavior.
5. Separate verified hard gates from proposed HUAKAI scoring inputs.
6. Keep bounded wait as source-backed, but mark wait-resume revalidation as HUAKAI invariant until proven.
7. Replace invented diagnostics labels or mark them as HUAKAI taxonomy.
8. Add file:line citations for every retained behavior claim.
### 4.2 Fix `streaming-forwarder-codex.md`
1. Scope disconnect drain by stream path.
2. State that the Anthropic conversion path exits on client disconnect and closes upstream.
3. Separately document passthrough paths with queue/drain-like behavior.
4. Do not claim bounded upstream queues for every Sub2API streaming path.
5. Cite each protocol family before claiming SSE / chunked JSON / raw chunk support.
6. Narrow Sub2API usage-axis KEEP to source-backed axes.
7. Move reported/normalized/inferred/partial taxonomy to HUAKAI design.
8. Narrow Sub2API failover KEEP to verified status codes and pre-stream behavior.
### 4.3 Fix `pool-selection-claude-v2.md`
1. Correct the slot-acquisition citation.
2. Distinguish service-level slot acquisition from cache-level request-ID acquisition.
3. Add one-api line citations for group/model selection, priority/random choice, forced routing, retry exclusion, and quota handling.
4. Decide whether to include Sub2API legacy/no-load-batch behavior in synthesis.
5. Decide whether Antigravity single-account behavior matters to HUAKAI F-POOL-001.
### 4.4 Fix `streaming-forwarder-claude-v2.md`
1. Replace `fire-and-forget` with `best-effort, detached-context, non-atomic with billing`.
2. Scope "no drain" to the inspected Anthropic chat-completions/responses conversion path.
3. Add a separate note for passthrough paths with queue/drain-like behavior.
4. Add one-api streaming line citations if synthesis compares one-api directly.
5. Add New API stream-status citations if HUAKAI keeps the stream outcome enum design.
### 4.5 Synthesis update requirements
1. Do not inherit old Codex pool three-layer KEEP wording.
2. Do not inherit old Codex streaming global drain wording.
3. Label continuation affinity, strong-candidate weighted scoring, usage-source taxonomy, and Tx2 atomic settlement as HUAKAI design.
4. Preserve source-backed behavior even when it is unsafe or incomplete.
5. Explicitly distinguish reference behavior, reference gap, and HUAKAI improvement.
6. Keep risky desired capabilities as Safe Equivalent, Feature Flag, Manual First, Experimental Module, or Mandatory Roadmap.
7. Do not silently drop any feature because a source claim drifted.
8. Re-run CL-011 against the revised files before using them as synthesis inputs.
## 5. Chinese Summary For Owner
本次复核结论是：Codex 旧版两个 specifier pass 都不能作为 CL-011 合格输入直接进入 synthesis，因为它们故意省略了源码路径和行号，而且把若干 HUAKAI 设计目标误写成了 Sub2API 的 KEEP 行为。F-POOL-001 中最严重的问题是“continuation affinity / sticky / fresh pool”的三层顺序、强候选评分带、capability safe-equivalent fallback 都没有在 Sub2API 源码中验证到；F-GW-002 中最严重的问题是把断连后 drain、bounded read queue、usage 完整性分类和失败 taxonomy 写得过于全局化，实际源码显示这些行为必须按具体 stream path 分开描述。
Claude v2 的质量明显好于 v1，Sub2API 主路径多数 citation 可复核通过，但仍不能无条件放行：pool pass 的 slot acquisition citation 不够精确，one-api 部分缺少逐行引用；streaming pass 把 `writeUsageLogBestEffort` 称为 fire-and-forget 不够准确，并且“Sub2API has no drain at all”只能限定在已检查的 Anthropic conversion path，不能扩展到所有 Sub2API passthrough stream path。建议下一步先修四个 specifier pass 的 CL-011 问题，再更新 synthesis；不要删除功能，只把未验证来源的能力明确标为 HUAKAI design、Safe Equivalent、Feature Flag 或 Mandatory Roadmap。
