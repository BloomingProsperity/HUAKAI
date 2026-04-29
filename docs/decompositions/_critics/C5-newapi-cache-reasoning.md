# Critic Review of new-api Cache-aware billing + reasoning-effort handling

| Field | Value |
| --- | --- |
| Critic | Codex critic-lane |
| Date | 2026-04-29 |
| Source files read | Withheld per CL-002 because upstream is AGPL-3.0. Independently verified against upstream GitHub source and local mirrored source for README, request/usage DTOs, relay handlers, provider adaptors, billing settlement, quota calculation, tiered billing, pricing ratios, channel affinity cache, user/token/channel quota updates, and related tests. |
| Companion specifier output | docs/decompositions/new-api/cache-billing-reasoning-source-verified.md |

## A. Coverage gaps (specifier likely missed these)
- C-001: Missing-usage billing is a fail-open money path. Source behavior shows that when upstream usage is absent or total tokens resolve to zero, the request is logged as unchargeable and actual settlement is driven with zero usage rather than a conservative estimate. HUAKAI must treat "upstream returned no usage" as a recovery workflow: estimated charge, pending reconciliation, channel quarantine, or operator approval. A spec that only says "records actual usage" is too flattering.
- C-002: Cache billing is not one discount field. The source distinguishes normal input, cache-read input, aggregate cache-write input, Claude-style split cache-write windows, image input, audio input, tool calls, group ratio, model ratio, per-call price, and optional tier expressions. The specifier must call out the exact HUAKAI billing contract categories, otherwise implementer-lane will underbuild this as "cached_tokens * rate".
- C-003: Cross-format usage semantics can double-charge or under-charge. Source behavior tags usage with semantic/source hints and treats Claude-format usage differently from OpenAI-format usage derived from Claude. If a converted response loses the semantic/source marker, cache creation and read tokens can be subtracted or added against the wrong base.
- C-004: Pre-consume and post-settle are not atomic across all ledgers. The upstream updates user/channel usage, then settles the funding source/token adjustments, and some settlement errors are only logged after partial funding movement. HUAKAI needs a transaction boundary or ledger event model, not just "pre-consume then refund".
- C-005: Subscription-first and wallet-first fallback creates multi-source settlement edge cases. The source has separate funding abstractions and idempotent subscription pre-consume records. Specifier must cover mixed funding, refund after provider failure, retry after partial settlement, and duplicate request IDs.
- C-006: Tiered billing expressions are a second billing engine. Source includes a tier-expression path with frozen pre-consume snapshots and fallback behavior when expression evaluation fails. HUAKAI cannot model this only as static ratios; the decomp needs a policy-language safety envelope, validation, and dry-run/audit mode.
- C-007: Reasoning effort is handled through several incompatible surfaces. OpenAI-style `reasoning_effort`, Responses `reasoning.effort`, Claude thinking config/output config, Gemini thinking budget/level, DeepSeek-style thinking suffixes, xAI suffixes, and OpenRouter reasoning payloads are normalized differently. A single "pass-through effort high/medium/low" requirement misses provider-specific translation and rejection paths.
- C-008: Reasoning token spend is not uniformly reported. Usage structures expose output/reasoning details for some protocols, while several adaptors only record the selected effort string or map model suffixes to budgets. HUAKAI must separate "requested effort", "sent upstream effort", "thinking budget", "actual reasoning tokens", and "visible reasoning content".
- C-009: Streaming makes final billing dependent on terminal usage events. Source handlers defer text billing until stream end or final usage assembly. Interrupted streams, scanner buffer limits, ping/timeout behavior, and clients disconnecting before final usage are all charge/reconciliation hazards.
- C-010: Channel affinity observes cache hits but is not billing truth. The source has cache-affinity metrics and cached-token rate modes. Those metrics are useful for routing, but they must not become tenant-visible billing evidence unless tied to immutable request usage records.

## B. Flattering errors (looks simple, isn't)
- F-001: "Cache billing support for all supported models" hides provider semantic drift. OpenAI cached input, Anthropic cache read/write, OpenRouter cost-derived cache creation, Gemini thinking budgets, and Qwen/DeepSeek thinking controls are not equivalent.
- F-002: "Reasoning effort support" sounds like a model-name suffix trick. Source behavior shows suffix parsing, model-name rewriting, field removal, adaptive Claude behavior, budget clamps, and provider-specific unsupported-parameter workarounds.
- F-003: "Flexible billing policy configuration" hides expression safety. Tier expressions can reference different token categories, request context, and pre-consume snapshots. HUAKAI needs bounded evaluation, versioned policy snapshots, and audit replay.
- F-004: "Actual usage billing" is only as good as upstream usage. Some paths synthesize fallback usage from estimates or converted responses; some paths record zero when upstream usage is absent. The spec must require an explicit confidence/source field for every usage line.
- F-005: "Multi-database support" does not mean HUAKAI should inherit multi-database compromise. Upstream supports SQLite/MySQL/PostgreSQL; HUAKAI DR-006 chooses PostgreSQL, so HUAKAI should design stronger ledger constraints instead of copying lowest-common-denominator update patterns.
- F-006: "Thinking-to-content" is not harmless display behavior. Moving hidden reasoning into visible response text changes privacy, downstream billing perception, moderation/audit scope, and user-visible transcript semantics.

## C. Upstream's own drift
- D-001: README says cache billing covers OpenAI, Azure, DeepSeek, Claude, Qwen and all supported models; source behavior still has provider-specific usage semantics, special cases, and fallback inference. That is not universal behavior, it is a patchwork of supported-by-adaptor cases.
- D-002: README advertises Gemini suffixes like `-low`, `-medium`, and `-high`; source behavior also clamps budgets by model family, supports `-nothinking`, handles explicit budget fields, and may rewrite the priced model name to a no-thinking variant. The documentation underspecifies billing and routing consequences.
- D-003: README lists OpenAI-style reasoning suffixes; source behavior includes broader suffix sets such as `minimal`, `none`, `xhigh`, and provider-specific variants. Docs are narrower than implementation.
- D-004: Deployment docs present PostgreSQL support, but source policy and compatibility rules still preserve SQLite/MySQL behaviors. HUAKAI should not infer PostgreSQL-grade consistency from upstream's multi-database claim.
- D-005: Some channel/request settings default to filtering cost-sensitive or privacy-sensitive fields, but pass-through switches can bypass removal. README-level feature claims do not expose the governance risk of operator-enabled pass-through.

## D. Things HUAKAI should NOT copy
- N-001: Do not copy zero-usage fail-open settlement. HUAKAI should charge conservatively from a trusted estimate, mark the usage record as provisional, and reconcile or refund by operator-visible workflow.
- N-002: Do not copy mutable in-place wallet/token/channel counters as the money source of truth. HUAKAI should use PostgreSQL append-only ledger rows plus derived balances, with idempotency keys and tenant_id on every row.
- N-003: Do not copy global model-ratio maps as the primary pricing mechanism. HUAKAI needs tenant-scoped, edition-scoped, effective-dated pricing policies with version snapshots per request.
- N-004: Do not copy model-name suffixes as the public reasoning contract. HUAKAI should expose explicit request fields and normalize suffix aliases only as compatibility shims.
- N-005: Do not copy provider-specific hidden transformations without audit traces. Every HUAKAI request should record requested model, billed model, upstream model, requested effort, sent effort, and actual usage categories.
- N-006: Do not copy pass-through body behavior as an operational escape hatch. HUAKAI should make risky fields explicit policy capabilities with deny-by-default tenant controls.
- N-007: Do not copy multi-database compromises. DR-006 lets HUAKAI use PostgreSQL constraints, row locks, numeric money fields, generated summaries, and transactional outbox patterns.
- N-008: Do not copy cache-affinity observations as a billing primitive. HUAKAI should keep routing cache metrics separate from immutable billing usage evidence.

## E. Smells found
- S-001: Fail-open path + evidence: no or zero upstream usage can lead to zero actual settlement after a successful provider call.
- S-002: Hidden global state + evidence: pricing ratios, cache ratios, billing modes, channel caches, tokenizer caches, and affinity caches are maintained through global maps/caches rather than tenant-scoped immutable policy snapshots.
- S-003: Magic constants without robust operator framing + evidence: default pre-consume quota, reasoning budget percentages, Gemini budget clamps, cache ratios, and channel affinity TTLs all materially affect billing/routing.
- S-004: Inconsistent error taxonomy + evidence: billing, upstream, quota, validation, and settlement errors are represented through a mix of typed gateway errors, string matching, local wrappers, and log-only branches.
- S-005: Single point of failure / volatile cache risk + evidence: channel selection and affinity state rely on hybrid or in-memory cache layers; Redis improves distribution but local cache behavior still matters during cold start, cache flush, or Redis outage.
- S-006: Tenant data leakage potential + evidence: pass-through request controls can expose privacy/cost-sensitive upstream fields, and global pricing/config maps need strict tenant scoping in HUAKAI.
- S-007: Partial settlement risk + evidence: funding settlement can complete before token adjustment; failure afterward is logged and settlement is marked to avoid refunding already-committed funds.
- S-008: Billing source ambiguity + evidence: wallet, subscription, token quota, user used quota, channel used quota, and consume logs are maintained as related but separate state surfaces.

## F. Synthesis recommendations
- Top-3 things specifier MUST address before this decomp can be cited by implementer-lane: define the HUAKAI usage-category contract for normal input, cache read, cache write, split cache write, output, reasoning, image/audio/tool calls; define zero/missing usage recovery and provisional billing; define cross-format semantic/source markers so converted usage cannot be double-counted.
- Top-3 things specifier MUST address before this decomp can be cited by implementer-lane: document requested-vs-sent-vs-actual reasoning fields; require stream interruption billing behavior; require idempotent settlement tests for wallet, subscription, token quota, and channel/user aggregates.
- Top-3 things specifier MUST address before this decomp can be cited by implementer-lane: include policy-version snapshots for pricing/tiered billing; add operator-visible reconciliation queues; add fail-closed handling for unsupported or risky pass-through billing fields.
- Top-3 HUAKAI-specific divergences this decomp must call out: PostgreSQL append-only ledger instead of mutable counters as source of truth; tenant/edition-scoped pricing and reasoning policy instead of global maps; explicit compatibility shims instead of model-name suffixes as the core API.
- Top-3 HUAKAI-specific divergences this decomp must call out: route cache-affinity metrics into routing observability only; keep billing evidence immutable and request-scoped; make "thinking-to-content" a tenant policy with audit logging and privacy review.
- Top-3 HUAKAI-specific divergences this decomp must call out: fail-closed or provisional-charge semantics for missing usage; transaction/outbox settlement for multi-source funding; separate clean-room implementation from AGPL source patterns.

## Owner Chinese summary (1 paragraph)
本次 critic-lane 独立阅读 new-api 的 AGPL 源码与文档后认为：这个特性不能被简化成“cache token 打折 + reasoning_effort 透传”。最高风险是上游 usage 缺失时存在 0 实际结算的 fail-open 钱路径，其次是 cache read/cache write/Claude 5m/1h 写入/跨协议 usage 语义会导致双扣或少扣，再其次是 reasoning effort 在 OpenAI、Responses、Claude、Gemini、DeepSeek、xAI 等路径里并不是同一个字段。是否阻塞下一 slice：如果 specifier 输出没有补齐“缺失 usage 恢复、不可变账本、跨协议 usage 语义、requested/sent/actual reasoning 记录、tenant/edition-scoped pricing policy”这些点，应阻塞 implementer-lane 引用；Owner 最优先要求补测零 usage、流中断、订阅+钱包混合结算、cache write split、reasoning 后缀与显式字段冲突。
