# Codex Feature Parity Missing-Feature Audit — 2026-05-09

**Lane**: feature-parity-auditor (codex independent)  
**Trigger**: Owner 2026-05-09 quote "功能是否缺失这一点也要算进去。再派一个Agent codex"  
**Constraint**: per CLAUDE.md Feature Preservation Rule

## TL;DR

- 8 `MISSING_DISPOSITION` (HIGH) features
- 8 disposition incomplete / phase-ID weak (MED) features
- 5 nit / underspecified (LOW) features
- Verdict: **Block P-0c-A dispatch until HIGH items get explicit disposition rows or Owner re-decision.** P-0c code fixes are useful, but they do not close feature-preservation gaps.

## 1. 三向对照表 (ref → HCSF v0.4 → HUAKAI roadmap disposition)

| Ref | Feature | HCSF v0.4 / P-0c mapping | Roadmap disposition found | Rating |
|---|---|---|---|---|
| sub2api | session-hash + `previous_response_id` sticky chain | HCSF does not implement axis 1; three-directions maps sticky session to tenant segment/prefix integration (`docs/plans/2026-05-09-three-directions-synthesis.md:98`). | `F-SESSION-001` has `Implemented` disposition and L2 status (`docs/03_FEATURE_PARITY_MATRIX.md:74`); axis table says context-state code is still 10% (`docs/02_HUAKAI_FUSION_ARCHITECTURE.md:91`). | MED: disposition exists, phase coupling weak |
| sub2api | `intercept_warmup_requests` client-warmup mock | HCSF/P-0c has no capability or native route for "client warmup detected -> synthetic response". Ref behavior is explicitly inverse of proactive warmup (`docs/research/2026-05-09-source-read-sub2api-newapi.md:43-52`). | Only mentioned in three-directions as a correction, no disposition row (`docs/plans/2026-05-09-three-directions-synthesis.md:54`). | **HIGH MISSING_DISPOSITION** |
| sub2api | OAuth arbitrage core: login bootstrap / refresh / Claude Code mimicry / 5h window | HCSF/P-0c does not touch auth. `F-AUTH-005` covers refresh/cache/mimicry as target (`docs/03_FEATURE_PARITY_MATRIX.md:71`). | Bootstrap is explicitly outside current F-AUTH-005 and L0 blocker (`docs/02_HUAKAI_FUSION_ARCHITECTURE.md:120`); no separate feature ID for first login bootstrap or 5h window. | **HIGH MISSING_DISPOSITION** |
| sub2api | multi-account scheduling + sticky | `F-POOL-001` and `F-SESSION-001` cover pooling/sticky outcomes (`docs/03_FEATURE_PARITY_MATRIX.md:73-74`). | L1 provider pool identity is locked (`docs/16_PHASED_DELIVERY_PLAN.md:101-109`; `docs/04_FEATURE_LOCK.md:13-20`). | Implemented target / Open |
| sub2api | claim-gate Pattern B + 5-effect Tx2 | Not HCSF scope. | `F-OBS-001` and architecture sequence record Tx1 reserve + Tx2 five-effect settlement (`docs/03_FEATURE_PARITY_MATRIX.md:48`; `docs/02_HUAKAI_FUSION_ARCHITECTURE.md:277-285`). | Implemented Better target |
| new-api | `channel_affinity` prompt-cache fingerprint + Redis binding | HCSF/PASR plan recognizes new-api affinity and LiteLLM locality; HUAKAI delta is score blending + miss demote (`docs/plans/2026-05-09-three-directions-synthesis.md:16-24`, `docs/plans/2026-05-09-next-pivot-synthesis.md:47-55`). | No dedicated `docs/03` row names new-api affinity; likely merged into `F-SESSION-001` / `F-POOL-001`. | MED: merged outcome, evidence row weak |
| new-api | recharge order + gift card/redemption | Payment plugin and voucher roadmap exist (`docs/03_FEATURE_PARITY_MATRIX.md:43`, `docs/03_FEATURE_PARITY_MATRIX.md:75`). | Recharge/payment is an L0 commercial blocker (`docs/02_HUAKAI_FUSION_ARCHITECTURE.md:124`). | Merged Equivalent / Plugin |
| new-api | invitation / referral codes | HCSF/P-0c not relevant. | I found voucher/gift-card and payment rows, but no invitation/referral-code disposition in `docs/03/16/17`. | **HIGH MISSING_DISPOSITION** |
| new-api | admin panel + reasoning effort passthrough | HCSF has `thinking/reasoning` capability (`docs/plans/2026-05-09-hcsf-v04-implementation-synthesis.md:46`). | Admin Lite is Phase 7 (`docs/16_PHASED_DELIVERY_PLAN.md:222-240`); reasoning effort row exists (`docs/03_FEATURE_PARITY_MATRIX.md:65`). | Mandatory Roadmap + Implemented target |
| new-api | cache differential billing | HCSF accounting supports cache usage; `F-BILL-003` covers cached-prompt pricing (`docs/03_FEATURE_PARITY_MATRIX.md:63`). | RB-8 tracks 5m vs 1h cache split (`docs/02_HUAKAI_FUSION_ARCHITECTURE.md:325`). | Implemented Better target |
| new-api | 4-state failed-stream billing semantics | Issue mining says HUAKAI must distinguish client_gone / upstream_timeout / output_token_zero / upstream_5xx (`docs/research/2026-05-09-issue-mining-cross-repo.md:248-249`, `docs/research/2026-05-09-issue-mining-cross-repo.md:317`). | `F-GW-002`/`F-OBS-001` are broad, but I found no explicit 4-state billing disposition. | **HIGH MISSING_DISPOSITION** |
| one-api | tenant/channel/group quota split + priority-bucketed random | Routing/pool levels cover weighted/priority/group routing (`docs/17_FEATURE_LEVEL_MATRIX.md:29`). | `F-GROUP-001`, `F-CH-001`, `F-BILL-001` cover the business surface (`docs/03_FEATURE_PARITY_MATRIX.md:42-46`). | Merged Equivalent |
| one-api | 2-gate auto-disable | Not HCSF scope. | `F-CH-002` and RB-1 cover auto-disable and default-on correction (`docs/03_FEATURE_PARITY_MATRIX.md:45`; `docs/02_HUAKAI_FUSION_ARCHITECTURE.md:318`). | Implemented Better target |
| LiteLLM | prefix-hash cache locality pin | HCSF/PASR next pivot explicitly repairs OpenAI/Gemini cache signal ingestion (`docs/plans/2026-05-09-next-pivot-synthesis.md:20-29`). | Feature matrix should add LiteLLM/new-api evidence to PASR rows, but user outcome is covered by PASR delta (`docs/plans/2026-05-09-three-directions-synthesis.md:94-104`). | MED: evidence/disposition sync needed |
| LiteLLM | MidStreamFallbackError continuation + usage merge | HCSF marks mid-stream fallback as P-8 (`docs/plans/2026-05-09-hcsf-v04-implementation-synthesis.md:94`, `docs/plans/2026-05-09-hcsf-v04-implementation-synthesis.md:119`). | Next-pivot suggests D-0 spec skeleton after PASR repair (`docs/plans/2026-05-09-next-pivot-synthesis.md:56`). | Mandatory Roadmap P-8 |
| LiteLLM | 100+ provider normalization | HCSF 14 capability + native passthrough + Tier A/B provider split covers first slice (`docs/plans/2026-05-09-hcsf-v04-implementation-synthesis.md:38-64`, `docs/plans/2026-05-09-hcsf-v04-implementation-synthesis.md:66-80`). | Provider catalog breadth is Phase 9 exit criterion (`docs/16_PHASED_DELIVERY_PLAN.md:265-290`). | Implemented Better target |
| LiteLLM | retry hierarchy | Not HCSF P-0. | `F-GW-004` covers retry/fallback DAG and stream-safe boundary (`docs/03_FEATURE_PARITY_MATRIX.md:52`). | Implemented target |
| LiteLLM | 2.5K LOC exception normalization | Not HCSF P-0. | `F-RATE-001` A13 converts this into versioned provider error rules (`docs/03_FEATURE_PARITY_MATRIX.md:68`). | Implemented Better target |
| Portkey | bi-canonical OpenAI + Anthropic | HCSF explicitly chooses OpenAI storefront + Anthropic native side-entry + capability graph (`docs/plans/2026-05-09-hcsf-canonical-synthesis.md:66-123`). | P-2/P-4 phases implement adapters/native passthrough (`docs/plans/2026-05-09-hcsf-v04-implementation-synthesis.md:27-34`). | Implemented Better target |
| Portkey | circuit breaker hook / control-plane decision | Existing docs cover retry, health, and circuit breaker broadly (`docs/17_FEATURE_LEVEL_MATRIX.md:38`; `docs/03_FEATURE_PARITY_MATRIX.md:52`). | No row captures "control-plane breaker decision hook" specifically. | MED: merged but underspecified |
| Portkey | conditional routing with Mongo-style operators | Architecture has F-CONFIG-001 for declarative routing and asks if rule chains enter v1.0 (`docs/03_FEATURE_PARITY_MATRIX.md:89`; `docs/02_HUAKAI_FUSION_ARCHITECTURE.md:364-365`). | Disposition exists, exact DSL/operator set is not locked. | MED |
| Portkey | TransformStream + streamState | HCSF stream plan and F-GW-002 cover stream states and terminal classification (`docs/plans/2026-05-09-p0-schema-spec-synthesis.md:145-153`; `docs/03_FEATURE_PARITY_MATRIX.md:38`). | Implemented Better target. | Pass |
| Helicone | 14 wired handler chain + per-batch drains | Reverify proves 14 wired handlers, not 15 (`docs/research/2026-05-09-helicone-chain-reverify.md:26-60`). | No feature row/ID in `docs/03` maps this consumer-chain outcome. | **HIGH MISSING_DISPOSITION** |
| Helicone | DLQ + 15min timeout + `setLowerPriority` lanes + asymmetric Kafka/SQS dual-write | Reverify records DLQ, 15min timeout, priority lanes, and asymmetric dual-write (`docs/research/2026-05-09-helicone-chain-reverify.md:111-123`). | Axis 5 notes "spec name only; code 0" and L1 DLQ/orphan worker exists without feature ID (`docs/02_HUAKAI_FUSION_ARCHITECTURE.md:95`, `docs/02_HUAKAI_FUSION_ARCHITECTURE.md:134`). | **HIGH MISSING_DISPOSITION** |
| Helicone | escrow reserve -> cancel -> settle | Billing/claim-gate design is already stronger: Tx1 reserve + Tx2 settle + audit (`docs/02_HUAKAI_FUSION_ARCHITECTURE.md:277-285`; `docs/03_FEATURE_PARITY_MATRIX.md:48`). | Implemented Better target. | Pass |
| Envoy AI Gateway | TokenProvider / preRotation / OIDC->cloud STS | Source-read lane says these should become TokenProvider + enterprise roadmap (`docs/research/2026-05-09-source-read-helicone-envoy-allapihub.md:206-225`). | Three-directions says "mark Mandatory Roadmap" but no `F-*` row or phase ID exists (`docs/plans/2026-05-09-three-directions-synthesis.md:121-124`). | **HIGH MISSING_DISPOSITION** |
| Envoy AI Gateway | 7 cost enum + CEL escape | HCSF accounting supports usage; `F-BILL-001` has pricing vector and expression AST (`docs/03_FEATURE_PARITY_MATRIX.md:42`). | CEL specifically is an alternative; local AST is acceptable safe equivalent if documented. | Safe Equivalent / MED to document |
| Envoy AI Gateway | per-endpoint canonical | HCSF aligns: OpenAI chat + Anthropic messages + capability graph, and cites per-endpoint canonical as a delta (`docs/plans/2026-05-09-hcsf-v04-implementation-synthesis.md:115-121`). | Implemented Better target. | Pass |
| all-api-hub | vendor-pluggable auto-checkin scheduler | Not HCSF scope. | `F-OPS-004` plugin disposition exists (`docs/03_FEATURE_PARITY_MATRIX.md:82`). | Plugin |
| all-api-hub | verification probe registry | Source-read recommends model+token+CLI probe registry (`docs/research/2026-05-09-source-read-helicone-envoy-allapihub.md:141-142`). | `F-EXPORT-001` covers health probe before tool export, but not the three-axis registry explicitly (`docs/03_FEATURE_PARITY_MATRIX.md:83`). | MED |
| all-api-hub | credential profile + one-click CLI export | `F-EXPORT-001` covers tool-export plugin with explicit confirmation (`docs/03_FEATURE_PARITY_MATRIX.md:83`). | Plugin. | Pass |
| all-api-hub | price comparison | `F-OPS-003` covers multi-source dashboard + price comparison (`docs/03_FEATURE_PARITY_MATRIX.md:81`). | Mandatory Roadmap Phase 7+. | Pass |
| all-api-hub | channel management | `F-CH-001` and Admin Lite cover channel CRUD/admin surfaces (`docs/03_FEATURE_PARITY_MATRIX.md:44`; `docs/17_FEATURE_LEVEL_MATRIX.md:35`). | Merged Equivalent. | Pass |
| all-api-hub | redemption codes | `F-BILL-002` covers vouchers/redemption, but evidence/reference only names one-api (`docs/03_FEATURE_PARITY_MATRIX.md:43`). | User outcome covered; evidence ledger/matrix should add All API Hub. | MED |

## 2. 14 capability hosted-tools / 协议 surface 漏检

| Surface | Current mapping | Verdict |
|---|---|---|
| Anthropic `web_search` hosted tool | HCSF `tool_use/tool_result` says native trigger includes hosted tools (`docs/plans/2026-05-09-hcsf-v04-implementation-synthesis.md:45`). LiteLLM source-read shows hosted tools bypass normal function translation (`docs/research/2026-05-09-axis3-protocol-translation-litellm.md:118-121`). | LOW: covered, but P-1 schema needs hosted-tool subtype. |
| Anthropic `code_execution` | Best fit is `tool_use` with hosted execution subtype plus `data_retention`; if it creates stateful sandbox files, also intersects `file`. HCSF has `computer_use`, but code execution is not literally UI computer control (`docs/plans/2026-05-09-hcsf-v04-implementation-synthesis.md:49-57`). | LOW: no new capability required, but naming must be explicit. |
| OpenAI Skills | Official OpenAI data docs say Skills can be local execution or hosted container execution and hosted skills share container lifecycle with hosted shell. | MED: map to `tool_use` + `file` + `data_retention`; add an explicit OpenAI Skills projection row in P-1/P-2. |
| OpenAI server-side compaction / `/responses/compact` | Issue mining names Codex remote compaction as a user demand (`docs/research/2026-05-09-issue-mining-cross-repo.md:82`, `docs/research/2026-05-09-issue-mining-cross-repo.md:199`). HCSF native passthrough lists `/v1/native/openai/responses`, but no compact route/capability/disposition is named (`docs/plans/2026-05-09-hcsf-canonical-synthesis.md:115-122`). | **HIGH MISSING_DISPOSITION** unless Owner declares it covered by `/v1/native/openai/responses/*`. |
| Gemini `inline_data` | Gemini current-state audit says inlineData is lossy today (`docs/research/2026-05-09-axis3-huakai-current-state.md:90`, `docs/research/2026-05-09-axis3-huakai-current-state.md:132`). HCSF has separate `file` / `image` / `audio` / `video` nodes (`docs/plans/2026-05-09-hcsf-v04-implementation-synthesis.md:50-53`). | LOW: MIME-based split is enough; document `image/* -> image`, other binary -> file/audio/video. |
| xAI server-side tools / Remote MCP | HCSF has `mcp_server` capability (`docs/plans/2026-05-09-hcsf-v04-implementation-synthesis.md:56`). Official OpenAI MCP docs also model remote MCP as a tool entry used with hosted tools. | Pass: map to `mcp_server`; native passthrough for xAI-specific server-side tool dialects. |

## 3. axis 1 / axis 5 是否被推迟 disposition 完整

Axis 1 is **not forgotten**, but it is not concretely phased inside HCSF v0.4/P-0c. The fusion architecture says context state/sticky is 10% and only schema exists (`docs/02_HUAKAI_FUSION_ARCHITECTURE.md:91`), while `F-SESSION-001` preserves sticky behavior as an `Implemented` target (`docs/03_FEATURE_PARITY_MATRIX.md:74`). That is enough to avoid HIGH, but not enough for execution sequencing. Mark MED: add explicit P-1/P-2/P-8 phase owner for session hash, previous-response binding, and migration headers.

Axis 5 is weaker. The architecture says async task implementation is 0% and only names DLQ/outbox/sweeper/token-cache invalidation (`docs/02_HUAKAI_FUSION_ARCHITECTURE.md:95`). It also lists DLQ + orphan sweep as L1 (`docs/02_HUAKAI_FUSION_ARCHITECTURE.md:134`) and later plans Helicone chain/DLQ as an action (`docs/plans/2026-05-09-three-directions-synthesis.md:135-143`), but I found no `docs/03` feature row with a valid disposition. That is HIGH for Helicone async-chain/DLQ parity.

## 4. 商业化必需 disposition (L0)

- sub2api OAuth arbitrage is a commercial blocker. Project brief says the product must enable Owner commercial Model 1, including API keys, payment, quota/rate/concurrency, usage/billing/audit (`docs/01_PROJECT_BRIEF.md:37-44`). Fusion architecture says sub2 login -> OAuth refresh-token bootstrap is L0 and not covered by F-AUTH-005 (`docs/02_HUAKAI_FUSION_ARCHITECTURE.md:120`). **HIGH until a feature row/phase exists.**
- Admin UI is preserved: L0 admin surface is missing in implementation (`docs/02_HUAKAI_FUSION_ARCHITECTURE.md:125`), but Phase 7 Admin Lite is explicit (`docs/16_PHASED_DELIVERY_PLAN.md:222-240`) and Admin Lite is in the level matrix (`docs/17_FEATURE_LEVEL_MATRIX.md:35`). No HIGH.
- `F-PAY-001` is dispositioned as Plugin with Personal Edition availability and SaaS orchestration later (`docs/03_FEATURE_PARITY_MATRIX.md:75`). No HIGH, but payment plugin shell remains L0 commercial risk.

## 5. P-0 / P-0c silent drop risk

1. `ProtocolLossEntry.IsSilentDrop()` accepts any nonempty `Reason`, `Note`, `Verdict`, or `Code` (`backend/internal/proto/protocol_loss.go:73-77`). That preserves old adapters because `newLossEntry` fills v0.3 fields (`backend/internal/proto/capability_matrix.go:168-169`). It does **not** by itself let an actual empty loss entry pass, and tests prove a `Field`-only entry fails (`backend/internal/proto/envelope_test.go:118-132`). Residual risk: a `Verdict`-only entry can pass with no human-readable reason. Mark MED, not HIGH.
2. `validateProviderProjection` currently enforces loss entries for non-preserved verdicts and `NativePath` for native-required (`backend/internal/proto/envelope_validate.go:303-327`), but it does not yet require `Capability` and valid `Verdict` for preserved rows. P-0c-A explicitly plans to add those guards (`docs/plans/2026-05-09-p0c-followup-plan-synthesis.md:21-29`, `docs/plans/2026-05-09-p0c-followup-plan-synthesis.md:46-50`). MED until P-0c-A lands.
3. `type HCSF = HCSFEnvelope` is a true alias and can sunset at compile level (`backend/internal/proto/proto.go:13-19`), but OpenAI/Gemini non-streaming still return zero-value `&HCSF{}` today (`backend/internal/proto/openai_sse.go:148-155`, `backend/internal/proto/gemini_sse.go:103-110`). P-0c-C already calls this fake success out and plans a fix (`docs/plans/2026-05-09-p0c-followup-plan-synthesis.md:54-60`). This is not a hidden drop if P-0c-C executes, but it is a real current code risk.

## 6. HIGH severity findings (MISSING_DISPOSITION)

1. **sub2api warmup interception safe equivalent missing**: add `F-WARMUP-001` or fold into `F-GW-002` as `Safe Equivalent`/`Feature Flag`, default off, audit synthetic responses.
2. **sub2api OAuth login bootstrap + 5h window missing**: split `F-AUTH-005` into refresh vs bootstrap or add `F-AUTH-006`; mark L0/P-0d or Mandatory Roadmap with Owner gate.
3. **new-api invitation/referral code flow missing**: payment/voucher rows do not cover invitation/referral acquisition. Add commercial-growth feature row.
4. **new-api 4-state failed-stream billing missing**: add explicit billing semantics for client_gone / upstream_timeout / output_token_zero / upstream_5xx.
5. **Helicone 14-handler consumer chain missing**: add a feature row for durable async consumer-chain outcome, or explicitly merge it into `F-ASYNC-001` with phase and acceptance tests.
6. **Helicone DLQ/priority/dual-write behavior missing**: add `F-ASYNC-001` with DLQ, timeout, priority lane, and asymmetric dual-write disposition.
7. **Envoy TokenProvider/preRotation/OIDC-to-STS missing ID**: add enterprise credential-rotation row; Safe Equivalent if HUAKAI does non-k8s rotation table instead of CRDs.
8. **OpenAI server-side compaction not explicitly classified**: either add HCSF `context_state/compaction` capability, or document native passthrough route for `/responses/compact`.

## 7. MED severity findings

- Axis 1 sticky/session is preserved but lacks concrete HCSF phase sequencing.
- new-api channel affinity and LiteLLM prefix locality should be added to the parity matrix as PASR evidence, not only synthesis narrative.
- Portkey circuit-breaker hook and conditional-routing DSL are merged conceptually but underspecified as operator-facing control-plane features.
- Envoy 7-cost-type + CEL should be documented as Safe Equivalent via HUAKAI pricing AST if CEL is intentionally not adopted.
- All API Hub verification probe registry needs explicit model/token/CLI axes in `F-EXPORT-001`.
- All API Hub redemption-code evidence should be added to `F-BILL-002`, currently one-api-only.
- `ProtocolLossEntry` permits `Verdict`-only non-silent entries; require `Reason` or `Code` for v0.4-created entries.
- P-0c-A/B/C are good fixes but do not close the feature-preservation highs above.

## 8. LOW severity findings

- Hosted tools can stay under `tool_use`, but P-1 should name `web_search`, `code_execution`, hosted MCP, and Skills as subtypes.
- Gemini `inline_data` should be MIME-dispatched to image/audio/video/file and record loss when MIME is absent.
- Native passthrough should be shown in admin UI as "full vendor risk", matching HCSF D5 guidance (`docs/plans/2026-05-09-hcsf-v04-implementation-synthesis.md:142-145`).
- `ValidateEnvelope` comment still says INV-1..12 while P-0c plans INV-13 (`backend/internal/proto/envelope_validate.go:20-33`; `docs/plans/2026-05-09-p0c-followup-plan-synthesis.md:21-28`).
- `docs/03` still has older disposition vocabulary and does not list `Manual First` / `Experimental Module`, though AGENTS.md allows them.

## 9. Recommended path

1. Before P-0c-A dispatch, patch `docs/03_FEATURE_PARITY_MATRIX.md` with eight HIGH rows or explicit merged-equivalent amendments.
2. Then dispatch P-0c-A/B/C as planned: M1/M2 guard strictness, round-trip coverage, version guard + fake-success fix.
3. Create one follow-up synthesis after P-0c-C: `HCSF P-1 capability subtype lock`, covering hosted tools, Skills, compaction, Gemini inline data, and MCP.
4. Treat axis 5 as the next parity-control doc update: `F-ASYNC-001` should define queue/outbox/DLQ/sweeper/priority-lane behavior and acceptance tests.

## Source files read

- `docs/research/2026-05-09-source-read-sub2api-newapi.md:16-62`, `:83-111`, `:151-185`, `:216-250`
- `docs/research/2026-05-09-source-read-oneapi-portkey-litellm.md:29-93`, `:96-153`, `:189-220`, `:250-290`
- `docs/research/2026-05-09-source-read-helicone-envoy-allapihub.md:101-146`, `:153-189`, `:192-225`, `:228-243`
- `docs/research/2026-05-09-helicone-chain-reverify.md:8-24`, `:26-60`, `:90-123`
- `docs/research/2026-05-09-axis3-protocol-translation-litellm.md:15-36`, `:93-124`, `:153-170`, `:200-217`
- `docs/research/2026-05-09-axis3-protocol-translation-portkey.md:38-55`, `:56-80`, `:81-132`
- `docs/research/2026-05-09-axis3-protocol-translation-envoy.md:88-147`, `:150-174`, `:177-224`, `:237-253`
- `docs/research/2026-05-09-axis3-huakai-current-state.md:81-103`, `:124-139`, `:182-225`, `:274-320`
- `docs/research/2026-05-09-issue-mining-cross-repo.md:33-44`, `:74-123`, `:187-218`, `:239-268`
- `docs/03_FEATURE_PARITY_MATRIX.md:9-21`, `:37-94`, `:120-156`
- `docs/02_HUAKAI_FUSION_ARCHITECTURE.md:85-139`, `:292-334`
- `docs/16_PHASED_DELIVERY_PLAN.md:1-20`, `:99-125`, `:222-290`
- `docs/17_FEATURE_LEVEL_MATRIX.md:21-43`, `:60-86`
- `docs/04_FEATURE_LOCK.md:11-38`
- `docs/01_PROJECT_BRIEF.md:35-52`
- `docs/plans/2026-05-09-hcsf-canonical-synthesis.md:66-123`
- `docs/plans/2026-05-09-hcsf-v04-implementation-synthesis.md:23-64`, `:91-122`, `:123-176`
- `docs/plans/2026-05-09-p0-schema-spec-synthesis.md:19-34`, `:154-187`, `:189-239`
- `docs/plans/2026-05-09-p0c-followup-plan-synthesis.md:9-18`, `:19-60`, `:71-119`
- `docs/plans/2026-05-09-three-directions-synthesis.md:44-104`, `:106-143`, `:160-201`
- `docs/plans/2026-05-09-next-pivot-synthesis.md:18-29`, `:47-67`, `:88-115`
- `backend/internal/proto/protocol_loss.go:16-77`
- `backend/internal/proto/proto.go:13-19`
- `backend/internal/proto/envelope.go:5-55`
- `backend/internal/proto/envelope_validate.go:20-75`, `:303-327`
- `backend/internal/proto/openai_sse.go:142-155`
- `backend/internal/proto/gemini_sse.go:97-110`
- Official OpenAI docs MCP: `https://developers.openai.com/api/docs/guides/your-data#v1responses`; `https://developers.openai.com/cookbook/examples/mcp/mcp_tool_guide`

## Tail block (per AGENTS.md template)

Source files read: HUAKAI internal docs and code listed above; no `~/refs/` upstream source read in this reviewer lane.  
Lane: feature-parity-auditor  
Agent: GPT-5 Codex  
UTC timestamp: 2026-05-10T15:23Z
