# `new-api` — Cache-Aware Billing + Reasoning-Effort Handling (Claude deep decomposition)

| Field | Value |
| --- | --- |
| Status | Deep decomposition (Claude lane, peer to Codex R3 specifier output) |
| Reference | New API (AGPL-3.0, [E-LIC-002](../../07_REFERENCE_EVIDENCE_LEDGER.md)) |
| Feature in HUAKAI matrix | F-BILL-003 (L3) + F-MODEL-001 (L2) |
| Specifier session | Claude PM-Orchestrator (Opus), 2026-04-29 |
| Source-reading delegate | Sonnet Explore agent — read 17 source files (~40min); structured factual report retained |
| Companion artifacts | docs/decompositions/new-api/cache-billing-reasoning-source-verified.md (Codex R3), .omc/artifacts/decomp-critic/C5-newapi-cache-reasoning.md (Codex critic) |
| **Truth-discipline** | **Observed regions: 17** / **Inferences: 2** / **Open questions: 5** |

> **Lane discipline**: Independent of Codex specifier/critic outputs. Behavior claims tagged `[region-N]`; inferences explicitly marked.

> **License note**: AGPL-3.0 — implementation must be Safe Equivalent.

---

## 1. WHY (motivation)

new-api is the largest community fork of one-api, focused on adding revenue-sensitive features that the parent project lacks. Two pressures shape the cache-billing + reasoning-effort surface:

**Pressure 1 — provider cache pricing arbitrage**: Modern providers charge cached-input tokens at 10-25% of fresh-input rate. A relay station that bills users at fresh-rate while paying upstream at cache-rate captures meaningful margin under popular-prompt workload. Conversely, billing at cache-rate but paying fresh-rate erases margin. Both directions are real and silent — only a per-bucket settler catches them. new-api's contribution over one-api is **explicit 3-bucket pricing** (fresh / cache-read / cache-creation) with sub-bucket TTL split for Anthropic 5m vs 1h cache `[region-1, region-5]`.

**Pressure 2 — reasoning-token cost transparency**: Newer reasoning models (Claude extended-thinking, Gemini deep-think, OpenAI o-series) consume thinking tokens whose cost is real but whose user-visible value is a *signal*. The relay must propagate user-requested effort to upstream and faithfully reflect upstream's reasoning cost back. new-api's contribution is **effort vocabulary translation per provider** (model-name suffix `-high` or body `output_config.effort`) + per-event reasoning-token extraction `[region-7, region-8, region-9]`.

---

## 2. WHAT (algorithm in HUAKAI vocabulary)

### Sub-behaviors S-1..S-19 (observed-only)

**S-1: Cache-bucket categorization (3-bucket model)** `[region-1, region-5, region-10]`. For each upstream response, input tokens are categorized:
- **Fresh input tokens** = `prompt_tokens - cache_read_tokens - cache_creation_tokens`
- **Cache-read tokens** = explicit field from response (Anthropic `cache_read_input_tokens`; OpenAI `prompt_tokens_details.cached_tokens`)
- **Cache-creation tokens** = explicit field (Anthropic `cache_creation_input_tokens`)

**S-2: Anthropic sub-bucket TTL split** `[region-1, region-10]`. Anthropic exposes cache creation in two TTL classes:
- `cache_creation.ephemeral_5m_input_tokens` (5-minute cache) — priced at 1.0× cache-creation base
- `cache_creation.ephemeral_1h_input_tokens` (1-hour cache) — priced at **1.6×** the 5-minute rate (constant `claude_cache_creation_1h_multiplier = 1.6` derived from upstream price ratio 6/3.75)

**S-3: Per-bucket multiplier** `[region-2, region-4]`. The settler reads two ratios per (provider, model):
- `cache_ratio` (default 0.1 - 0.5 varying by model) — applied to cache-read tokens
- `cache_creation_ratio` (default 1.25) — applied to cache-creation tokens

The bills are computed as: `fresh_tokens × base_input_rate + cache_read_tokens × base_input_rate × cache_ratio + cache_creation_tokens × base_input_rate × cache_creation_ratio + output_tokens × completion_ratio`.

**S-4: Pricing storage (in-memory map + per-model override)** `[region-2, region-4]`. Two-tier storage:
- Global default maps: `cacheRatioMap` and `createCacheRatioMap` (loaded from JSON config at startup; hot-reloadable via admin API).
- Per-model override: each model's `Pricing` struct has `CacheRatio` and `CreateCacheRatio` pointer fields; if set, they override the global default for that model.

**S-5: OpenAI cache extraction (2-bucket only)** `[region-10, region-12]`. OpenAI does not expose cache-creation as a separate bucket. The settler:
- Reads `prompt_tokens_details.cached_tokens` for cache-read.
- Treats remaining `prompt_tokens - cached_tokens` as fresh.
- Has NO cache-creation bucket for OpenAI (no premium first-write rate).

**S-6: Non-standard cache extraction (vendor-specific fallback)** `[region-12]`. For providers like Zhipu and Moonshot that emit `cached_tokens` in non-standard locations (custom JSON shape), new-api has dedicated extractors `extractCachedTokensFromBody` and `extractMoonshotCachedTokensFromBody`. Cache detection is empirical, per-provider.

**S-7: Gemini cache silence** `[region-13]`. Gemini supports implicit caching but does NOT expose cache-read distinction in the response. new-api treats all Gemini input as fresh — cache cost absorbed by upstream's pricing, not surfaced or charged differentially.

**S-8: Reasoning-effort input shapes** `[region-9]`. Two parallel input mechanisms:
- **Model-name suffix** (legacy + DSL): `claude-opus-4-7-high`, `gpt-4-low`. Recognized suffixes for Claude: `-max -xhigh -high -medium -low -minimal`. For OpenAI: `-high -minimal -low -medium -none -xhigh`.
- **Body field**: Claude uses `output_config.effort = "high"`; OpenAI uses `reasoning.effort = "high"`.

The model-name suffix is parsed via `TrimEffortSuffix` which strips the suffix from the model id and returns the effort level.

**S-9: Claude effort transformation** `[region-7]`. Translates parsed effort to:
- `thinking.type = "adaptive"` (extended thinking enabled)
- `output_config = {"effort": "<level>"}` (passes effort through)
- For Claude Opus 4.7 specifically: `thinking.display = "summarized"` (restore visible summary) and **temperature/top_p/top_k must be nil** (Opus 4.7 rejects non-default values with HTTP 400).

**S-10: OpenAI effort transformation** `[region-8]`. Translates to `reasoning = {"effort": "<level>"}` in request body. Provider-specific behavior: some OpenAI variants ignore the field; some require model-tier variant selection.

**S-11: Extended thinking budget calculation** `[region-7]`. For Claude adaptive thinking, the budget integer is computed: `thinking.budget_tokens = 0.8 * max_tokens` (hardcoded 80% of caller's max_tokens). The 80% comes from `model_setting.GetClaudeSettings().ThinkingAdapterBudgetTokensPercentage`.

**S-12: Reasoning-token extraction (per provider)** `[region-9, region-13]`:
- OpenAI: `usage.completion_tokens_details.reasoning_tokens` (numeric).
- Gemini: `metadata.thoughts_token_count` → mapped to OpenAI-shape reasoning_tokens for consistency.
- Anthropic: NO separate reasoning_tokens field — adaptive thinking cost is rolled into output_tokens (cost absorbed silently from billing perspective).

**S-13: Reasoning-token pricing** `[inferred from region-9, region-12]`. Reasoning tokens are NOT priced separately; they are billed at the same `completion_ratio` as regular output tokens. There is no `reasoning_ratio` constant. The reasoning-token field is recorded in audit but doesn't affect cost calculation.

**S-14: Pre-consumption + settlement (BillingSession lifecycle)** `[region-1, region-3]`. Billing runs in two phases:
- **Pre-consumption** (Tx1 analog): on request entry, deduct an estimated quota from user balance/subscription. This protects against overspending in long-running streaming requests.
- **Settlement** (Tx2 analog): on response completion, compute actual quota; refund difference (if pre > actual) or charge difference (if actual > pre). Net effect: user's wallet is correct after settlement.

**S-15: Settlement bucket math** `[region-15]`. `calculateTextQuotaSummary` iterates: input quota, cache-read quota (with cache_ratio), cache-creation quota (with cache_creation_ratio + 5m vs 1h split), output quota. Sums into `total_quota` in dimensionless quota units (1 quota unit = 1.0M tokens normalized via `QuotaPerUnit`).

**S-16: Quota unit normalization** `[region-16]`. All upstream prices in dollars are converted: `quota_unit = cost_in_dollars / 1_000_000 × QuotaPerUnit`. Single-currency model — no FX rate handling. Operator-configurable QuotaPerUnit lets the deploy set its own granularity.

**S-17: Tiered billing expression (versioned)** `[region-16]`. For complex pricing (e.g., 1M-token-tier discounts), new-api supports a `BillingExpr` string evaluated by `ComputeTieredQuota`. The expression is captured in a `BillingSnapshot` with `ExprVersion` integer + `ExprString` hash, allowing expression upgrades without breaking historical settlement.

**S-18: Audit row split-field structure** `[region-14]`. Single audit log row (not multiple per-bucket rows) with denormalized fields:
- `cache_tokens` (read total)
- `cache_creation_tokens` (write total)
- `cache_creation_tokens_5m`
- `cache_creation_tokens_1h`
- `reasoning_effort` (string: "high"/"medium"/etc.)
- `quota` (final deducted)
- `billing_source` ("wallet" / "subscription")
- `billing_mode` ("standard" / "tiered_expr")
- For tiered: `expr_b64` (base64-encoded expression at settlement) + `matched_tier`

**S-19: Stream-vs-buffered usage parsing** `[region-12]`. Two paths:
- Streaming: `OaiStreamHandler` accumulates usage from each SSE event via `StreamScannerHandler`. For audio models, usage is in second-to-last SSE item (provider-specific quirk).
- Buffered: `OpenaiHandler` reads entire response body, unmarshals to `OpenAITextResponse`, extracts usage struct.

For Claude streaming, `patchClaudeMessageDeltaUsageData` merges cache fields from `message_start` event into `message_delta` (the cache info appears once at start; the merger ensures the terminal frame carries complete usage).

### 2-bis Lifecycle traces (3 observed)

**L-1 Cached prompt happy path (Claude)**: User sends prompt with explicit cache breakpoint markers. Pre-consumption deducts estimate. Upstream returns response with `cache_read_input_tokens=900, cache_creation_input_tokens=100, output_tokens=200`. S-1 categorizes: 900 cache-read + 100 cache-creation + 0 fresh + 200 output. S-2 inspects 1h vs 5m split. S-3 applies multipliers (e.g., 0.1× for cache-read, 1.25× for cache-creation). S-15 sums and converts to quota units. S-14 settles: refund pre-consumed surplus or charge delta. S-18 logs audit with all bucket fields.

**L-2 Reasoning-effort high (OpenAI)**: User sends `model="o1-high"` (suffix syntax). S-8 strips suffix → effort=high; rebuilt model id = "o1". S-10 sets `reasoning.effort = "high"` in body. Upstream processes with extended thinking. Response includes `completion_tokens_details.reasoning_tokens=4500`. S-12 extracts; S-13 prices at completion rate (same as output). S-18 audit records `reasoning_effort=high, reasoning_tokens=4500`.

**L-3 Cache-ratio change mid-flight (RACE — observed gap)** `[derived from §9 Q-1]`: Operator updates `cacheRatioMap` via admin UI while requests are in flight between pre-consumption and settlement. Pre-consumed at OLD rate; settled at NEW rate. **No mechanism observed to freeze rate per-request at pre-consumption time.** Audit records the new rate, not the rate active when the request entered. This is an integrity gap relative to F-OBS-001's spec line 8 ("Released specs unchanged since 2026-04-28...").

---

## 3. INPUTS (data structures touched)

**Per-Request inputs**: model id (with optional `-effort` suffix), `output_config.effort` body field (Claude), `reasoning.effort` body field (OpenAI), max_tokens (drives 80% thinking budget for Claude), prompt content (cache breakpoint markers).

**Per-Channel state**: per-channel pricing override (in `Pricing` struct).

**Per-Model state**: per-model `CacheRatio` + `CreateCacheRatio` pointers; per-model base input/output rates; tiered expression if applicable.

**Per-Process state**: in-memory `cacheRatioMap` + `createCacheRatioMap` (hot-reloadable); `BillingSnapshot` registry; reasoning-suffix lookup table.

**Per-User state**: wallet balance, subscription, billing preference.

**Persistent state**: usage logs (one row per request with bucket fields), pricing tables (model.Pricing rows), billing snapshots (for tiered).

---

## 4. FAILURE MODES (observed-only)

| FM-id | Trigger | Observable outcome | Operator signal | Recovery | Blast radius |
|---|---|---|---|---|---|
| FM-1 | Cache ratio updated mid-request flight | Pre-consumed at old rate, settled at new — net financial drift | none | manual reconciliation if detected | per-request, magnitude depends on ratio change |
| FM-2 | Provider cache field absent (Gemini-style silence) | All input billed as fresh; missed cache-pricing opportunity | none | switch to provider-aware default if confirmed | per-tenant margin loss until detection |
| FM-3 | Vendor non-standard cache field added | Default extractor returns 0 for cached tokens; bills at fresh rate | none until manual extractor written | manual extractor implementation | per-provider until fixed |
| FM-4 | Anthropic 5m/1h split missing from response | New-api falls back to single cache_creation bucket; loses TTL premium accuracy | none | tolerate or detect via discrepancy | margin drift |
| FM-5 | Claude Opus 4.7 with non-default temperature | Upstream rejects with HTTP 400 | error to user | clear nil-out at request transformer (S-9) | per-request |
| FM-6 | Reasoning effort suffix collision with model name | Suffix stripping breaks model lookup | error or wrong model | careful suffix list maintenance | per-request |
| FM-7 | Tiered billing expression evaluation error | Settlement fails; quota not deducted, log incomplete | error log | manual settlement recovery | financial integrity |
| FM-8 | Pre-consumption estimate too low (long stream surprise) | Settlement charges large delta; user surprised by large deduction | none specifically | improve pre-consumption forecast | per-request UX |

---

## 5. INTERFACES TO HUAKAI

**Personal Edition**:
- HUAKAI's existing schema in `docs/schema/observability-billing.sql` already has `cache_creation_tokens` + `cache_read_tokens` fields on `usage_records` — structurally compatible with new-api's bucket model.
- The 3-bucket model (fresh / cache-read / cache-creation) maps directly. HUAKAI MUST add the **5m vs 1h sub-bucket** for Claude (currently absent in HUAKAI schema).
- Reasoning-token field exists in HUAKAI schema as part of usage; effort-level field needs to be added (currently not in schema).

**SaaS Edition**:
- Per-tenant pricing override: new-api's per-channel mechanism is the model. HUAKAI's `provider_accounts.pricing_override` (additive migration) lets enterprise tenants negotiate lower rates than the catalog.
- Hot-reload pricing without restarting: new-api's admin UI updates the in-memory map; HUAKAI MUST persist pricing changes to PostgreSQL for replica consistency (DR-006), but should also trigger an in-process refresh.

**Cross-feature**:
- F-OBS-001 Tx1 ClaimGate.Reserve(): pre-consumption analog (S-14). HUAKAI's claim row is the structural equivalent of new-api's pre-consumed quota.
- F-OBS-001 Tx2 Settler.Settle(): settlement (S-15). The bucket math must run **inside Tx2** with multipliers read from a versioned `billing_pricing_versions` row (already in HUAKAI schema).
- F-PROTO-002 capability matrix: reasoning-effort support cell needed per (client, upstream) pair.

---

## 6. RISKS HUAKAI MUST GUARD AGAINST

**R-1 [DR-006 PostgreSQL — pricing race (FM-1)]**: new-api's hot-reload of in-memory cache_ratio creates a TOCTOU race between pre-consumption and settlement. HUAKAI MUST capture the pricing-policy version at pre-consumption time (Tx1 records `billing_policy_version` in claim row, already in schema) and re-use it at settlement (Tx2 reads same version). NEVER read "current" policy at settlement.

**R-2 [DR-001 multi-tenant — per-tenant rate override]**: new-api's per-channel pricing override is single-tenant. In HUAKAI multi-tenant, override scope is per-tenant (each tenant can have negotiated rates). Implementation: `pricing_overrides` table keyed by (tenant_id, provider_id, model, bucket) with effective_at timestamp.

**R-3 [Anthropic 5m vs 1h sub-bucket (S-2)]**: HUAKAI schema currently has `cache_creation_tokens` as single field. To capture the 1.6× premium for 1h variant, schema MUST split into `cache_creation_5m_tokens` + `cache_creation_1h_tokens`. Additive migration; settler reads both.

**R-4 [Provider silent cache (S-7 / FM-2)]**: For providers without cache signal (Gemini), HUAKAI must NOT silently bill all input as fresh — the operator may be paying upstream cache rate without surfacing margin opportunity. Add a `cache_signal_supported` flag per provider; on "false" providers, mark Usage Record `cache_status='unknown'` with operator alert if patterns suggest hidden caching.

**R-5 [Vendor extractor brittleness (FM-3)]**: new-api's per-vendor cache extractors are hand-coded. HUAKAI's reference-tracking policy (DR-022/24) MUST include "monitor upstream cache field shape" as recurring task; integration tests against real upstreams catch new shapes.

**R-6 [Reasoning-token billing (S-13)]**: new-api bills reasoning at completion_ratio (same as output). In HUAKAI multi-tenant, some tenants may want to bill reasoning DIFFERENTLY (e.g., not pass-through to end users — operator absorbs). Make reasoning_ratio configurable per-tenant (default = completion_ratio for compatibility with new-api).

**R-7 [Suffix-vs-body effort collision (FM-6)]**: When user sends BOTH a suffix-encoded model AND a body `effort` field, which wins? new-api source must clarify; HUAKAI MUST document precedence (recommend body field wins; suffix is sugar) and warn on conflict.

**R-8 [Pre-consumption forecast accuracy (FM-8)]**: new-api's pre-consumption may underestimate long-streaming requests, causing settlement-time surprise. HUAKAI's claim row should carry `predicted_cost` honestly + Tx2 `actual_cost` separate; user-visible accounting shows both.

**R-9 [DR-002 SaaS Edition — billing snapshot durability]**: new-api's `BillingSnapshot.ExprVersion + ExprString` is a great pattern. HUAKAI should adopt for tiered billing — the expression hash is the integrity signal for historical replay.

---

## 7. SAFE ADAPTATION (concrete divergences)

1. **Pricing version pinned at Tx1 + reused at Tx2** (eliminates FM-1 race).
2. **Per-tenant pricing override** table with effective_at versioning (DR-001).
3. **Schema split**: `cache_creation_5m_tokens` + `cache_creation_1h_tokens` (additive migration).
4. **Per-provider `cache_signal_supported` flag** + Usage Record `cache_status` enum (observed / inferred / unknown / not-supported).
5. **Reasoning-token rate configurable per-tenant** (default = completion_ratio).
6. **Body-field effort wins over suffix; warn on conflict**.
7. **Adopt BillingSnapshot pattern** with ExprVersion + ExprString hash for tiered billing.
8. **Real integration tests** against current real-provider error/usage shapes (DR-022/24).
9. **Vendor cache-extractor registry** (mappable per-provider; default fallback uses standard fields).

---

## 8. EVIDENCE LEDGER ROWS (proposed)

- **E-NAI-001 (existing — promote)**: Cache-aware billing — promote to deep with 3-bucket + sub-bucket detail.
- **E-NAI-004 (existing — promote)**: Reasoning-effort handling — promote with per-provider mapping.
- **E-NAI-DEEP-NEW-1**: Anthropic 5m vs 1h cache TTL premium 1.6× `[region-1]`.
- **E-NAI-DEEP-NEW-2**: Per-bucket multiplier storage (in-memory + per-model override) `[region-2]`.
- **E-NAI-DEEP-NEW-3**: Pre-consumption + settlement lifecycle (Tx1/Tx2 analog) `[region-1, region-3]`.
- **E-NAI-DEEP-NEW-4**: BillingSnapshot ExprVersion versioning pattern `[region-16]`.
- **E-NAI-DEEP-NEW-5**: Vendor non-standard cache extractor registry `[region-12]`.

---

## 9. OPEN QUESTIONS (for synthesis)

1. **Q-1 Pricing-version pinning at pre-consumption**: source did NOT show a mechanism freezing the rate per-request. Confirm absence definitively; if absent, this is a HUAKAI-must-fix.
2. **Q-2 Suffix-vs-body precedence**: when both effort encodings present, which wins?
3. **Q-3 Cache-creation TTL detection**: how does new-api decide between 5m and 1h? Upstream signal or local heuristic?
4. **Q-4 Per-channel rate floor**: source comments mention group ratios but not channel-level overrides; confirm if Enterprise tenants can negotiate per-channel.
5. **Q-5 BillingSnapshot transition**: when upgrading ExprVersion, are old snapshots migrated or read-as-is? Affects HUAKAI's migration story.

---

## 10. SOURCE COVERAGE PROOF (Sonnet Explore agent, ~40min, 17 files)

| Region | URL | Contribution |
|---|---|---|
| region-1 | github.com/QuantumNous/new-api/fc377da/service/billing_session.go | BillingSession lifecycle, pre-consumption + settlement |
| region-2 | .../setting/ratio_setting/cache_ratio.go | cacheRatioMap + createCacheRatioMap defaults + accessors |
| region-3 | .../service/billing.go | SettleBilling helper entry |
| region-4 | .../relay/helper/price.go | ModelPriceHelper, per-bucket rate lookup, 1.6× constant |
| region-5 | .../model/pricing.go | Per-model Pricing struct with bucket override + BillingMode + BillingExpr |
| region-6 | .../relay/common/billing.go | BillingSettler interface |
| region-7 | .../relay/channel/claude/relay-claude.go | Claude effort transformer; thinking.budget + adaptive type; Opus 4.7 nil-out |
| region-8 | .../relay/channel/openai/adaptor.go | OpenAI reasoning-effort transformer |
| region-9 | .../setting/reasoning/suffix.go | TrimEffortSuffix lookup table |
| region-10 | .../dto/claude.go | ClaudeUsage struct: cache_read + cache_creation + 5m/1h split |
| region-11 | .../dto/openai_response.go | InputTokenDetails / OutputTokenDetails for OpenAI cache + reasoning |
| region-12 | .../relay/channel/openai/relay-openai.go | Stream + buffered handlers; non-standard cache extractors (Zhipu/Moonshot) |
| region-13 | .../relay/channel/gemini/relay-gemini.go | Gemini metadata.thoughts_token_count → reasoning_tokens |
| region-14 | .../service/log_info_generate.go | Audit row assembly with denormalized bucket fields |
| region-15 | .../service/text_quota.go | calculateTextQuotaSummary |
| region-16 | .../pkg/billingexpr/settle.go | Tiered billing expression engine + ExprVersion |
| region-17 | .../relay/common/relay_info.go | RelayInfo struct including ReasoningEffort field |

---

## 11. ROUND-2 CRITIC FINDINGS (C5 new-api)

> Codex critic file at `.omc/artifacts/decomp-critic/C5-newapi-cache-reasoning.md`. This Claude-deep is independent. Synthesis stage merges Codex specifier-deep + C5 critic + this Claude-deep.

---

## Owner Chinese summary

本 deep 拆解依据 Sonnet Explore agent 真读 17 个 new-api 源文件（40min），由我（Claude Opus）合成 19 个 sub-behavior + 3 个 lifecycle + 8 个 failure 模式 + 9 个 HUAKAI-fit 风险 + 9 项 safe adaptation。**核心发现**：(1) 3-bucket 缓存模型（fresh / cache-read / cache-creation）+ Anthropic 的 **5 分钟 / 1 小时 TTL 子桶**（1h 比 5m 贵 1.6 倍）——HUAKAI schema 必须分拆现有 `cache_creation_tokens` 字段（R-3）；(2) cache_ratio 全局热重载有 **TOCTOU 漏洞**（FM-1）——HUAKAI 必须在 Tx1 锁 pricing version + Tx2 复用（R-1）；(3) effort 通过模型名后缀 (`-high`) 或 body 字段双重输入——HUAKAI 必须文档化优先级（R-7）；(4) Gemini 缓存沉默不暴露读写信号——HUAKAI 该加 cache_signal_supported 标志（R-4）；(5) reasoning_tokens 与 output_tokens 同价——HUAKAI 多租户应可配置（R-6）。BillingSnapshot ExprVersion 模式值得借鉴。本文件未读 codex specifier 或 critic 输出。
