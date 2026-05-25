# new-api Cache-Aware Billing Buckets + Reasoning-Effort Handling

| Field | Value |
| --- | --- |
| Project | new-api |
| Feature | Cache-aware billing buckets + reasoning-effort handling |
| HUAKAI matrix row | F-BILL-003 (L3) + F-MODEL-001 (L2) |
| Lane / round | Codex specifier-lane R3 |
| Date | 2026-04-29 |
| Source license posture | AGPL-3.0 upstream mirror read for behavior evidence only; implementation identifiers and code shapes are redacted. |
| Truth-discipline | Observed regions: 19 / Inferences: 8 / Open questions: 8 |
| Companion critic | `.omc/artifacts/decomp-critic/C5-newapi-cache-reasoning.md` read first and addressed in §11 |
| Claude contamination guard | Did not read `docs/decompositions/new-api/cache-billing-reasoning-claude-draft.md`. |

## §1 WHY

This feature exists because cache tokens and reasoning controls make "one request = prompt tokens + completion tokens" materially false. The observed upstream behavior prices normal input, cache-read input, cache-write input, split cache-write windows, image input, audio input, output, tool surcharges, fixed per-call price, group ratio, model ratio, and optional expression-based policy as distinct billing signals [region-1][region-3][region-4][region-5].

Reasoning is also not one portable field. The source handles explicit effort values, model-name suffixes, provider-specific thinking payloads, budget-token style controls, "no thinking" variants, OpenRouter-style reasoning payloads, Responses-style reasoning objects, Claude-style thinking, Gemini thinking budgets and levels, DeepSeek-style thinking suffixes, and xAI suffix aliases [region-10][region-11][region-12][region-13][region-14][region-15][region-16].

For HUAKAI, the design pressure is not to copy these paths, but to preserve the user outcomes: correct billing under cache usage, correct pricing snapshots, observable requested/sent/actual reasoning state, and recovery when upstream usage is missing or late [region-1][region-6][region-16][region-17].

## §2 WHAT

S-1. The system builds a text billing summary from actual usage when available, but falls back to estimated input-only usage when actual usage is absent; this fallback is observed before quota calculation, not as a separate recovery workflow [region-1].

S-2. If computed total tokens are zero, the text path sets actual charge to zero, logs an error, skips user/channel used-quota increments, and still invokes settlement with zero actual usage [region-1].

S-3. Cache-read input is treated as a separate token bucket with its own ratio; for non-Claude-style usage, cached input is subtracted from the base input bucket before its cache ratio is applied [region-1].

S-4. Cache-write input is also treated as a separate bucket, and Claude-style usage can split cache-write tokens into short-window and long-window buckets with distinct ratios [region-1][region-3].

S-5. The log-facing cache-write total is normalized: when split windows exist it uses the split total unless the aggregate write bucket is larger; otherwise it uses the aggregate write bucket [region-1].

S-6. OpenRouter Claude-style billing is adjusted by subtracting cache-read and cache-write buckets from normal input, and the source can infer cache-write tokens from upstream cost when custom pricing is not active [region-1][region-6].

S-7. Image input and audio input are not generic prompt tokens in all cases; image input can be ratio-priced, and audio input can use a separate per-million-token price when configured [region-1].

S-8. Tool and special-call surcharges are added on top of token-derived quota for observed web search, Claude web search, file search, and image-generation call usage [region-1].

S-9. Fixed per-call pricing bypasses token-ratio calculation for the model base cost, but still adds observed tool/audio surcharges and group ratios [region-1][region-3].

S-10. The pre-consume price snapshot includes group ratio, model ratio or fixed price, completion ratio, cache ratios, image/audio ratios, and computed pre-consume quota; free-model configuration can force pre-consume to zero [region-3].

S-11. Dynamic/tiered billing is a separate pricing path: pre-consume evaluates an administrator expression with estimated input/output, freezes an expression snapshot, and settlement later re-evaluates actual usage against that frozen snapshot [region-4][region-5].

S-12. Dynamic/tiered token normalization subtracts cache, image, and audio sub-buckets only when the expression uses those variables; Claude-style usage is treated as already text-separated, while non-Claude-style usage may need subtraction [region-5].

S-13. Dynamic/tiered failure is fail-soft: if actual expression settlement fails, the system uses final pre-consumed quota or the estimated snapshot quota rather than failing the request settlement [region-5].

S-14. Pre-consume and post-settle are split across a funding source and API Key quota; settlement first adjusts funding, then adjusts API Key quota, and a later API Key quota error is logged after funding may already be committed [region-7][region-8].

S-15. Funding can come from wallet or subscription according to user preference; subscription-first and wallet-first fallback are observed, and subscription pre-consume uses a request id for refund behavior [region-8][region-9].

S-16. Billing logs include billing source, billing preference, subscription pre-consume/post-delta details, reasoning effort when known, request path, conversion chain, stream status, and admin/routing metadata [region-18].

S-17. The usage structure carries prompt, completion, total, cache-read, cache-write, image, audio, reasoning-token, source, semantic, and cost-derived fields; these fields are not all guaranteed by every Provider response [region-10].

S-18. Cache usage used for routing affinity is observed separately from billing: cache-hit counters, cached-token counters, relay-format-specific rate modes, and TTL-windowed stats are maintained as observability/routing support, not as settlement truth [region-19].

S-19. OpenAI-compatible request handling accepts a requested effort field and a reasoning object, and Responses-style request handling accepts a nested reasoning object with effort [region-10][region-12].

S-20. OpenAI-compatible Provider handling parses effort suffixes on reasoning-capable model names, rewrites the upstream model to its base form, records the selected effort, and may move the effort into a Provider-specific reasoning payload [region-12][region-15].

S-21. OpenRouter-style handling enables usage inclusion by default when absent, converts a thinking suffix into a reasoning payload, removes the requested effort field after translation, and supports a Claude-style thinking payload with budget tokens [region-12].

S-22. Claude-style handling maps suffix-based effort into adaptive thinking for newer Opus-family models, uses high effort for some thinking aliases, clamps/removes sampling parameters for models observed to reject them, and maps low/medium/high requested effort to fixed thinking budgets [region-13].

S-23. Claude-style response conversion preserves thinking content as reasoning content in OpenAI-compatible responses and maps thinking deltas during streams [region-13].

S-24. Gemini-style handling supports explicit thinking budgets in the model alias, a thinking alias driven by max output tokens or requested effort, a no-thinking alias, and effort suffixes that become thinking levels [region-14][region-15].

S-25. Gemini native request handling can rewrite the billed/origin model to a no-thinking variant if the request explicitly disables thinking and a matching billing configuration exists [region-15].

S-26. DeepSeek-style handling parses limited thinking suffixes into disabled/enabled thinking, rewrites model names to their base form, and records the resulting effort when present [region-11][region-15].

S-27. xAI-style handling maps selected model suffixes to high or low requested effort and rewrites the model to its base form [region-16].

S-28. Reasoning-token accounting is partial: some response conversions copy actual reasoning-token counts into completion details, while other paths record only selected effort or convert visible thinking content [region-10][region-13][region-17].

S-29. Streaming billing depends on terminal usage when present; otherwise the system estimates usage from accumulated streamed text and tool-call count, and stream status records EOF, timeout, scanner error, client disconnect, ping failure, panic, or normal done [region-17][region-18].

S-30. Non-streaming OpenAI-compatible responses are written to the client before some parsing errors are returned to the caller, and the code comments state billing should still proceed after content has been written [region-17].

## §2-bis Lifecycle traces

1. Text request with cache read/write and actual usage: request receives a pre-consume price snapshot, upstream returns usage, billing summary separates normal input, cache read, cache write, split cache write if present, image/audio buckets if present, output, and tool surcharges; then user/channel usage is incremented and funding/API Key settlement is applied [region-1][region-3][region-7].

2. Text request with missing usage: settlement enters text post-consume with nil or zero usage, fills estimated input-only usage only for nil usage, but if total tokens resolve to zero it records zero actual charge, logs the problem, and settles actual quota as zero against the pre-consume reservation [region-1][region-7].

3. Tiered policy request: pre-consume evaluates the configured expression using estimated input/output and request context, freezes a snapshot, settlement builds actual bucket parameters from the returned usage and expression-used variables, then either charges the actual expression result or falls back to pre-consume/estimated quota if expression settlement fails [region-4][region-5].

4. Reasoning alias to Provider payload: a client-visible model alias or requested effort is parsed, the upstream model is rewritten to the base model, the Provider-specific thinking/reasoning payload is populated, the selected effort is stored for logs, and actual reasoning-token counts are only available if the Provider response supplies them [region-12][region-13][region-14][region-16][region-18].

5. Streaming request: the stream scanner records stream end status while data chunks are forwarded; if final usage arrives it is used, otherwise text accumulation is converted to estimated usage; the consume log can include stream status, enabling later reconciliation of timeout, client disconnect, or scanner error cases [region-17][region-18].

## §3 INPUTS

Observed input inventory:

- Request identity and routing context: User, User Group, API Key, Channel, Provider Account choice, request id, origin model, upstream model, final request format, route/group selection [region-3][region-8][region-18].
- Usage categories: normal input, output, total, cache read, aggregate cache write, split short-window cache write, split long-window cache write, image input/output, audio input/output, text input/output, reasoning output tokens, tool-call counts, Provider cost hints [region-1][region-5][region-10][region-17].
- Pricing data: model ratio, fixed per-call price, completion ratio, cache read ratio, cache write ratio, split cache-write ratios, image ratio, audio ratios, group ratio, user-group special ratio, other multipliers, quota-per-unit conversion [region-1][region-3][region-4].
- Dynamic policy data: expression string, expression version, expression hash, request-derived inputs, estimated token snapshot, matched tier, estimated quota before/after group ratio [region-4][region-5].
- Reasoning controls: requested effort, nested reasoning effort, model suffix aliases, thinking enabled/disabled controls, thinking budget tokens, thinking level, no-thinking marker, visible thinking-to-content setting [region-10][region-12][region-13][region-14][region-15][region-16].
- Operational evidence: billing source, subscription plan data, stream status, cache-affinity stats, conversion/audit hints, admin reject reason [region-18][region-19].

## §4 FAILURE MODES

| ID | Observed failure mode | Observed handling | Source |
| --- | --- | --- | --- |
| FM-1 | Missing or zero total usage after upstream work | Charge becomes zero, error is logged, settlement still runs with zero actual quota | [region-1] |
| FM-2 | Dynamic expression settlement fails after pre-consume | Falls back to final pre-consume or estimated snapshot quota | [region-5] |
| FM-3 | Pre-consume funding fails after API Key quota was decremented | API Key quota rollback is attempted; rollback error is only system-logged | [region-7] |
| FM-4 | Funding settles but later API Key quota adjustment fails | Funding remains marked settled; API Key error is logged and returned | [region-7] |
| FM-5 | Stream scanner sees timeout, client disconnect, scanner error, ping failure, or panic | Stream status end reason/error is recorded and later may be logged in consume metadata | [region-17][region-18] |
| FM-6 | Provider-specific cache tokens appear in nonstandard response locations | Post-processing extracts cached tokens from alternate usage/body locations for selected Provider types | [region-17] |
| FM-7 | Claude-style thinking requested with unsupported/new model behavior | Request translation uses adaptive mode and removes or resets sampling controls for observed rejecting models | [region-13] |
| FM-8 | Gemini thinking budget is outside model-family bounds | Budget is clamped according to observed model-family min/max rules | [region-14] |
| FM-9 | OpenRouter/Claude thinking payload lacks required budget when enabled | Conversion returns an error before upstream request construction completes | [region-12] |
| FM-10 | Subscription pre-consume cannot cover request | Preference fallback may try wallet, or the request fails with insufficient quota depending on preference | [region-8][region-9] |

## §5 INTERFACES TO HUAKAI

Personal Edition:

- Expose a stable request contract with `requested_reasoning_effort`, optional compatibility aliases, and per-request usage record fields for requested effort, sent effort, thinking budget, actual reasoning tokens, visible reasoning content policy, and Provider-reported confidence/source.
- Billing UI should show a compact immutable usage breakdown: normal input, cache read, cache write, split cache write when present, output, reasoning tokens if reported, image/audio/tool buckets, and final quota/cost.
- Missing usage should not silently zero-charge. Personal Edition can use a provisional charge from trusted estimate, mark the Usage Record as provisional, and reconcile/refund once actual usage appears or operator chooses a resolution.

SaaS Edition:

- All usage and billing rows must carry tenant_id, edition, policy version, pricing snapshot, billing source, request id, Provider, Channel, Provider Account, and API Key identity.
- Tenant-scoped pricing policy must support static bucket rates and a bounded expression/policy mode, but implementation should use HUAKAI-owned policy language or declarative tiers, not copied upstream expression mechanics.
- Billing Ledger must be append-only and transactionally connected to Usage Record state. Wallet/subscription/API Key quota settlement needs idempotency keys and PostgreSQL constraints rather than mutable counters as source of truth.
- Admin Ops should separate cache-affinity observability from billing evidence. Cache-hit routing stats are useful signals, but tenant-visible billing must come from immutable usage evidence and pricing snapshots.

## §6 RISKS

R-1 (inference, not observed): HUAKAI DR-001 multi-tenancy makes global price maps unsafe as a primary contract; pricing must be tenant/edition scoped with effective dates and immutable request snapshots.

R-2 (inference, not observed): HUAKAI DR-006 PostgreSQL allows stronger settlement semantics than the observed mutable counter pattern; append-only ledger plus derived balances should replace mutable wallet/API Key/channel counters as financial truth.

R-3 (inference, not observed): Zero-usage fail-open is unacceptable for HUAKAI money paths; use provisional estimated billing and reconciliation queues instead.

R-4 (inference, not observed): Provider-specific reasoning transformations can create audit gaps unless HUAKAI records requested model, billed model, upstream model, requested effort, sent effort, budget, and actual reasoning-token evidence.

R-5 (inference, not observed): Thinking-to-content can expose hidden reasoning as visible transcript text; SaaS Edition needs tenant policy, privacy review, and audit events.

R-6 (inference, not observed): Dynamic pricing expressions can become an unsafe second billing engine unless bounded by validation, dry-run, versioning, explainability, and replay.

R-7 (inference, not observed): Streaming usage dependency means interrupted streams need a recovery state, not only a log status; otherwise billing can under-charge or over-refund.

R-8 (inference, not observed): Cache-affinity stats can be mistaken for billing evidence; HUAKAI should keep routing affinity and immutable billing evidence physically and semantically separate.

## §7 SAFE ADAPTATION

- Use a HUAKAI Usage Record category set: `input_text`, `input_cache_read`, `input_cache_write`, `input_cache_write_short`, `input_cache_write_long`, `input_image`, `input_audio`, `output_text`, `output_audio`, `output_image`, `output_reasoning`, `tool_web_search`, `tool_file_search`, `tool_other`, `provider_fixed_call`.
- Store `usage_source` and `usage_confidence`: provider-reported, converted-provider-reported, estimated-from-stream, estimated-from-request, operator-corrected.
- Treat missing usage as `provisional_charge_required`, not `zero_charge_success`.
- Public API should prefer explicit effort fields; model suffix aliases may exist only as compatibility shims.
- Record requested effort, normalized HUAKAI effort, Provider payload class, Provider effort/budget/level sent upstream, and actual reasoning tokens if reported.
- Keep pricing policy snapshots immutable per request. For expression-like pricing, store policy version, normalized variables used, validation result, and replay inputs.
- Enforce settlement in one PostgreSQL transaction or through an outbox-backed ledger workflow with idempotency keys.
- Keep cache-affinity hit metrics in routing observability tables, not Billing Ledger.

## §8 EVIDENCE LEDGER ROWS

Proposed clean-room ledger rows:

| Evidence ID | Source Type | Capability | Observed behavior | HUAKAI directive |
| --- | --- | --- | --- | --- |
| E-NAI-DEEP-CBR-001 | Source code (deep read) | F-BILL-003 | Text settlement separates normal input, cache read, cache write, split cache write, image/audio/tool surcharges, fixed price, group/model ratios, and dynamic expression settlement. | IMPROVE with immutable pricing snapshot and explicit usage categories. |
| E-NAI-DEEP-CBR-002 | Source code (deep read) | F-BILL-003 | Missing/zero usage can settle as zero actual quota after upstream work. | AVOID; use provisional charge and reconciliation. |
| E-NAI-DEEP-CBR-003 | Source code (deep read) | F-BILL-003 | Wallet/subscription funding and API Key quota are separate settlement surfaces with fallback and partial-settlement risk. | IMPROVE with PostgreSQL ledger and idempotency. |
| E-NAI-DEEP-CBR-004 | Source code (deep read) | F-MODEL-001 | Reasoning effort is translated across OpenAI-compatible, Responses, Claude, Gemini, DeepSeek, OpenRouter, and xAI surfaces using fields, suffixes, budgets, levels, and payload rewrites. | KEEP outcome, redesign as explicit HUAKAI contract plus compatibility shims. |
| E-NAI-DEEP-CBR-005 | Source code (deep read) | F-BILL-003 / F-MODEL-001 | Streaming billing depends on terminal usage when available and estimation/status logging otherwise. | IMPROVE with stream reconciliation states and usage confidence. |

## §9 OPEN QUESTIONS

1. Which Providers in production always return actual reasoning-token counts, and which only accept requested effort without reporting actual reasoning spend?
2. Should HUAKAI expose suffix aliases publicly, or keep them migration-only?
3. What is the exact provisional-charge policy for missing usage: max estimate, request estimate, configured minimum, or operator approval?
4. Should split cache-write windows be generalized beyond Claude-style windows into named cache-write classes?
5. How should tenant-specific dynamic pricing be governed: declarative tiers only, or a bounded expression language?
6. What stream interruption states require automatic reconciliation versus operator review?
7. Should Personal Edition include subscriptions, or should subscription funding be SaaS-only?
8. Which fields are allowed to become visible transcript content when thinking-to-content is enabled?

## §10 SOURCE COVERAGE PROOF

Region-1: Text post-consume and quota summary region; contributed cache-read/write buckets, split cache-write windows, image/audio/tool surcharge handling, missing/zero usage behavior, and consume-log fields.

Region-2: Billing settler interface region; contributed the observed reserve/settle/refund contract shape without copying implementation details.

Region-3: Static price pre-consume region; contributed model ratio, fixed price, group ratio, cache ratios, image/audio ratios, and free-model pre-consume behavior.

Region-4: Dynamic billing documentation region; contributed the observed operator-facing expression concept, variable categories, request-aware pricing, versioning, and pre-consume/settlement flow.

Region-5: Dynamic settlement code region; contributed actual token normalization rules, split Claude cache variables, expression-used variable subtraction, input-length semantics, and fallback on settlement error.

Region-6: Realtime/audio quota and OpenRouter cache-create inference region; contributed audio token pricing, realtime pre-consume/post-consume, zero-token handling, and cost-derived cache-create inference.

Region-7: Billing session settlement region; contributed ordering of funding settlement, API Key quota adjustment, refund gating, reserve flow, and partial-settlement logging.

Region-8: Billing session creation region; contributed wallet/subscription preference modes, fallback behavior, and subscription pre-consume minimum.

Region-9: Funding source region; contributed wallet pre-consume/settle/refund and subscription request-id pre-consume/refund behavior.

Region-10: Request/response DTO region; contributed observed data categories for requested effort, nested reasoning, cache tokens, image/audio tokens, reasoning tokens, usage semantic/source, and Provider cost hint.

Region-11: DeepSeek adapter region; contributed suffix-to-thinking enabled/disabled translation and effort recording.

Region-12: OpenAI-compatible adapter region; contributed OpenRouter usage inclusion, reasoning payload translation, suffix parsing, upstream model rewrite, and Responses effort translation.

Region-13: Claude relay conversion region; contributed suffix/adaptive thinking handling, low/medium/high budget mapping, sampling override behavior, and thinking-to-reasoning response conversion.

Region-14: Gemini thinking region; contributed budget clamps, effort-to-budget percentages, explicit budget alias, no-thinking alias, and effort-level mapping.

Region-15: Gemini handler and suffix parser regions; contributed no-thinking priced-model rewrite, generic effort suffix vocabulary, OpenAI effort suffix vocabulary, and DeepSeek suffix vocabulary.

Region-16: xAI adapter region; contributed high/low effort suffix handling and model rewrite.

Region-17: OpenAI stream/non-stream response handling regions; contributed terminal usage extraction, fallback text-based usage estimation, alternate cached-token extraction, and billing-after-write behavior.

Region-18: Stream scanner and log-info regions; contributed stream end statuses, scanner/ping/client disconnect handling, and consume-log metadata for reasoning effort, billing source, subscription, and stream status.

Region-19: Channel-affinity usage-cache region; contributed cache-hit observability counters, relay-format-specific rate modes, TTL windows, and separation from direct settlement.

## §11 ROUND-2 CRITIC FINDINGS

| Critic ID | Disposition | R3 handling |
| --- | --- | --- |
| C-001 | CONFIRM-from-source | §2 S-1/S-2 and §4 FM-1 document missing/zero usage fail-open; §7 requires provisional billing. |
| C-002 | CONFIRM-from-source | §2 S-3..S-10 and §7 enumerate cache/read/write/split/image/audio/tool/fixed-price categories. |
| C-003 | CONFIRM-from-source | §2 S-6/S-12/S-17 and §7 require source/semantic/confidence markers. |
| C-004 | CONFIRM-from-source | §2 S-14 and §4 FM-3/FM-4 document non-atomic settlement surfaces. |
| C-005 | CONFIRM-from-source | §2 S-15, §3, and §5 cover wallet/subscription fallback and request-id settlement needs. |
| C-006 | CONFIRM-from-source | §2 S-11/S-13 and §6 R-6 cover dynamic pricing as second billing engine. |
| C-007 | CONFIRM-from-source | §2 S-19..S-27 cover incompatible reasoning surfaces. |
| C-008 | CONFIRM-from-source | §2 S-28 separates requested/sent/actual reasoning evidence. |
| C-009 | CONFIRM-from-source | §2 S-29 and §4 FM-5 cover terminal stream usage and interruption hazards. |
| C-010 | CONFIRM-from-source | §2 S-18 and §7 separate cache-affinity metrics from billing truth. |
| F-001 | CONFIRM-from-source | §2 S-3..S-7 and §2 S-24 show Provider semantic drift. |
| F-002 | CONFIRM-from-source | §2 S-20..S-27 documents suffix parsing, rewrites, budgets, and unsupported workarounds. |
| F-003 | CONFIRM-from-source | §2 S-11..S-13 and §6 R-6 document expression safety concerns. |
| F-004 | CONFIRM-from-source | §2 S-1/S-2/S-17 and §7 require usage source/confidence. |
| F-005 | CONFIRM-from-source | §6 R-2 applies HUAKAI DR-006 instead of copying mutable counters. |
| F-006 | CONFIRM-from-source | §6 R-5 marks thinking-to-content as privacy/audit risk. |
| D-001 | CONFIRM-from-source | §2 S-3..S-7 keeps cache support as adaptor-specific, not universal. |
| D-002 | CONFIRM-from-source | §2 S-24/S-25 includes Gemini suffixes, clamps, no-thinking pricing rewrite. |
| D-003 | CONFIRM-from-source | §2 S-20 and §10 region-15 include broader effort vocabulary. |
| D-004 | CONFIRM-from-source | §6 R-2 says HUAKAI should use PostgreSQL-grade constraints. |
| D-005 | CONFIRM-from-source | §2 S-21 and §6 R-4/R-5 cover pass-through/governance risk. |
| N-001 | CONFIRM-from-source | §7 explicitly avoids zero-usage fail-open settlement. |
| N-002 | CONFIRM-from-source | §7 requires append-only ledger and idempotency. |
| N-003 | CONFIRM-from-source | §5/§7 require tenant/edition-scoped policy snapshots. |
| N-004 | CONFIRM-from-source | §7 makes suffixes compatibility shims only. |
| N-005 | CONFIRM-from-source | §7 requires requested/billed/upstream model and reasoning audit fields. |
| N-006 | CONFIRM-from-source | §6 R-4/R-5 and §7 require explicit policy controls. |
| N-007 | CONFIRM-from-source | §6 R-2 and §7 apply PostgreSQL design. |
| N-008 | CONFIRM-from-source | §2 S-18 and §7 separate cache affinity from billing. |
| S-001 | CONFIRM-from-source | §4 FM-1 documents zero actual settlement. |
| S-002 | CONFIRM-from-source | §6 R-1 converts global pricing/cache concern into HUAKAI policy snapshot risk. |
| S-003 | CONFIRM-from-source | §2 S-10/S-22/S-24 expose budget/ratio constants as policy inputs. |
| S-004 | OPEN-question-because-source-ambiguous | This pass observed mixed errors in read regions but did not read enough global error taxonomy to make a full claim. |
| S-005 | CONFIRM-from-source | §2 S-18 and §10 region-19 cover hybrid cache reliance for affinity observability. |
| S-006 | CONFIRM-from-source | §6 R-1/R-4/R-5 covers tenant leakage/governance risks. |
| S-007 | CONFIRM-from-source | §4 FM-4 covers funding-settled/API Key-adjustment failure. |
| S-008 | CONFIRM-from-source | §2 S-14/S-15 and §5 cover separate funding/quota/usage surfaces. |

Owner 中文总结：本轮拆解的是 new-api 的 cache-aware billing buckets 与 reasoning-effort handling；真观察来自 19 个源区，覆盖计费桶、动态计费、资金结算、usage 语义、reasoning 转换、streaming usage 和 cache-affinity，合理推断只放在 HUAKAI-fit 风险与安全改造建议中；companion critic 的主要问题均已 CONFIRM-from-source 并落到 §2/§4/§7，只有全局错误 taxonomy 因本轮未充分读取全局错误体系而标为 OPEN；当前 open questions 为 8 个，不建议阻塞继续拆解，但实现前必须先确定 provisional billing、reasoning audit 字段、stream reconciliation 和 tenant-scoped pricing policy。
