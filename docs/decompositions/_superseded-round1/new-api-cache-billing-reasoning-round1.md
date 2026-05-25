# New API - Cache-aware billing + reasoning-effort handling

| Field | Value |
| --- | --- |
| Status | Draft |
| Reference | New API (AGPL-3.0-or-later, E-LIC-002) |
| Feature in HUAKAI matrix | F-BILL-003; F-MODEL-001 |
| Evidence ledger row | E-NAI-001; E-NAI-004 |
| Lane mode | Specifier - Option C carve-out for billing behavior under DR-000 |
| Specifier session | Codex specifier-lane, 2026-04-29 |
| Specifier date | 2026-04-29 |
| Reviewer session | TBD - must be a different reviewer-lane session |
| Reviewer date | TBD |
| Source files read | https://github.com/QuantumNous/new-api/tree/42846c692e01<br>https://github.com/QuantumNous/new-api/blob/42846c692e01/dto/openai_response.go<br>https://github.com/QuantumNous/new-api/blob/42846c692e01/dto/claude.go<br>https://github.com/QuantumNous/new-api/blob/42846c692e01/dto/gemini.go<br>https://github.com/QuantumNous/new-api/blob/42846c692e01/types/pricing.go<br>https://github.com/QuantumNous/new-api/blob/42846c692e01/service/token_counter.go<br>https://github.com/QuantumNous/new-api/blob/42846c692e01/relay/channel/gemini/relay-gemini.go<br>https://github.com/QuantumNous/new-api/blob/42846c692e01/relay/channel/openai/relay_responses.go |

## 1. WHY

New API adds two behaviors that basic quota billing does not cover. First, modern Providers report that part of the request token stream reused a prompt cache, and that reused portion has a different economic cost than fresh prompt tokens. If the gateway bills only total request tokens, repeat prompts either overcharge Users or under-recover Provider spend. Second, thinking-capable Models let the client express a reasoning tier or token budget. If the gateway normalizes away that intent, the User cannot predict latency, cost, or response depth, and the Usage Record cannot explain why a request consumed extra reasoning tokens.

Inference: the upstream design is driven by billing transparency and provider compatibility pressure, not only by quota enforcement. E-NAI-001 establishes the product claim that cached and fresh prompt tokens are priced separately. E-NAI-004 establishes the product claim that reasoning effort and thinking budget are accepted as request intent.

## 2. WHAT (algorithm in HUAKAI vocabulary)

For text generation, the gateway treats Provider usage as a vector, not a single total. It separates request tokens into fresh request tokens, cache-read request tokens, and, for Providers that charge cache creation separately, cache-write request tokens. It also keeps completion tokens separate from reasoning tokens when the upstream reports them.

On settlement, the pricing engine starts from the Model pricing context for the selected Channel and User Group. It applies the normal request-token ratio to fresh request tokens, the cache-read ratio to cache-hit tokens, the cache-write ratio to cache-creation tokens, and the completion ratio to completion tokens. For Providers that distinguish cache-write lifetime buckets, the source confirms separate ratios are available rather than forcing all cache creation into one bucket. The final request charge is then reconciled against the pre-consumed quota, and the Usage Record receives enough context to explain which token classes contributed to cost.

For reasoning effort, the gateway preserves client intent before forwarding. The source confirms three intent sources: explicit request fields for reasoning-capable OpenAI-style requests, Claude-style thinking settings with a budget, and Gemini-style thinking configuration. The README also confirms logical Model suffixes that encode high/medium/low effort or fixed thinking budgets. The gateway maps that intent into the selected Provider's native request shape where supported. If an operator enables generic header or body pass-through rules, reasoning-related upstream fields may also be preserved by configuration; HUAKAI should treat this as an explicit allow-list decision, not an uncontrolled tunnel.

## 3. INPUTS

Inputs consumed:

- Client request Model, including any HUAKAI Model alias or suffix that encodes reasoning intent.
- Client request body fields that express reasoning effort, thinking mode, or token budget.
- Optional operator pass-through policy for request headers or body fields.
- Selected User, User Group, API Key, Route, Channel, and Provider Account.
- Model pricing context: request-token ratio, completion-token ratio, cache-read ratio, cache-write ratio, cache-write lifetime ratios where supported, and any group multiplier.
- Provider response usage fields: request tokens, completion tokens, total tokens, cache-read tokens, cache-write tokens, reasoning tokens, and provider-specific usage semantics.
- Stream accumulation state when usage arrives at the end of a streamed response or has to be reconstructed from chunks.
- Pre-consumed quota and final settlement state.

State mutated:

- User or API Key quota balance through pre-consumption, refund, and final settlement.
- Usage Record fields for token class breakdown, cost context, Provider/Channel context, and reasoning-token spend.
- Operator-visible usage/cache statistics where the selected path records cache-hit counters.

## 4. FAILURE MODES HANDLED

- Provider omits cache fields: the request falls back to fresh-token billing for the reported request tokens; cache discount is not invented.
- Provider reports cache-read tokens in a provider-specific location: the gateway maps the signal into a normalized cache-hit accounting field before settlement.
- Provider reports cache-write tokens with finer lifetime categories: the gateway can preserve separate creation buckets and price them independently.
- Streamed response delays usage until the terminal event: settlement waits for actual usage and reconciles against the pre-consumed amount.
- Reasoning-capable request uses a Provider that expects a different request shape: the gateway converts the intent to the Provider-native shape when the selected Provider supports it.
- Reasoning effort appears in a compatibility path: source evidence shows the compatibility layer is designed to preserve explicit zero values and non-default optional parameters, reducing silent loss of client intent.

## 5. INTERFACES TO HUAKAI

- Model Registry: must represent whether a Model supports reasoning effort, thinking budget, cache-read billing, and cache-write billing.
- Route and Channel policy: must decide whether a Provider Account may receive reasoning-related pass-through fields.
- Pricing engine: must accept token-class inputs and version the ratios used at request time.
- Usage Record: must store fresh request tokens, cache-read tokens, cache-write tokens, completion tokens, reasoning tokens, pricing-version reference, and settlement result.
- Billing Ledger: should receive one append-only charge entry tied to the Usage Record, not recompute from mutable pricing tables.
- Admin Ops UI: must show why a repeated prompt was cheaper or more expensive, and whether reasoning effort increased spend.

## 6. RISKS

- Clean-room risk: New API is AGPL-3.0; implementation must be Safe Equivalent and must not reuse upstream names, field layouts, or calculation structure.
- Billing risk: a zero or missing cache field can mean "no cache hit" or "Provider did not report"; HUAKAI must distinguish unknown from verified zero where possible.
- Rounding risk: token-class math can drift if each class is rounded separately before summing.
- Abuse risk: uncontrolled pass-through could let a User request high reasoning spend on a low-tier API Key.
- Audit risk: if cache-hit discount appears only in quota math and not in the Usage Record, support cannot explain bills.

## 7. SAFE ADAPTATION FOR HUAKAI

- **KEEP**: Treat cache-read request tokens as a first-class billing input, not as a display-only usage detail.
- **KEEP**: Preserve reasoning effort and thinking budget as User intent across protocol conversion when the selected Provider supports it.
- **IMPROVE**: Store a pricing snapshot on the Usage Record and append a Billing Ledger entry so historical bills never depend on current ratios.
- **IMPROVE**: Add an explicit `cache_status` concept with `reported-hit`, `reported-zero`, and `not-reported` states to avoid false transparency.
- **IMPROVE**: Gate reasoning effort by API Key, User Group, and Model policy before forwarding, with operator-visible rejection reasons.
- **AVOID**: Do not copy upstream price constants, source structure, names, or provider-specific DTO layouts.
- **AVOID**: Do not allow broad request header/body pass-through for reasoning controls without a Channel allow-list and Audit Event.

## 8. EVIDENCE LEDGER ROWS

- E-LIC-002: New API license is AGPL-3.0-or-later, so this decomposition is specifier-only.
- E-NAI-001: README-level evidence for cache-aware billing across multiple Providers; source dive confirms token-class and ratio surfaces exist.
- E-NAI-004: README-level evidence for reasoning effort and thinking budget; source dive confirms request/usage surfaces exist for OpenAI-style, Claude-style, and Gemini-style reasoning paths.
- F-BILL-003: HUAKAI should classify as Safe Equivalent / Implemented Better in Phase 6+.
- F-MODEL-001: HUAKAI should classify as Implemented with policy-gated pass-through and separate reasoning-token accounting.

## 9. OPEN QUESTIONS

- Should HUAKAI expose cache-write lifetime buckets in the public Usage Record, or keep them operator-only while showing a simpler cache-write total to Users?
- Should fixed thinking budgets be allowed from Model alias suffixes, request body fields, or both?
- What is HUAKAI's default policy when a Provider supports reasoning effort but the User Group is not entitled to high effort: downgrade, reject, or require explicit paid override?
- Does the Phase 6 Billing Ledger need separate line items per token class, or one line item with a structured pricing snapshot?

## Review Sign-Off

| Field | Value |
| --- | --- |
| Reviewer | TBD - fresh reviewer-lane session required |
| Review date | TBD |
| Checks passed | Pending CL-001 through CL-010 |
| Notes | Behavioral sections avoid upstream function names, struct names, package names, and implementation paths; source URLs are confined to the metadata block per Owner instruction. |

中文总结：本文件拆解了 New API 相比 one-api 更突出的缓存感知计费与 reasoning-effort/思考预算传递：关键差异是它不是只按 prompt/completion 总 token 扣费，而是把缓存命中、缓存创建、推理 token 等信号纳入费用解释；与已有 sub2api 拆解相比，本次重点不是账号池或流式转发，而是 Provider 返回的 usage 细分如何进入定价与 Usage Record。HUAKAI 应吸收“缓存命中单独计费、reasoning 意图不丢失、Usage Record 可解释”的产品能力，但实现必须走 Safe Equivalent，价格快照、Billing Ledger、权限门禁和可审计 pass-through 都要比上游更严格。
