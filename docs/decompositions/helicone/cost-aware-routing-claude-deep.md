# `helicone` — Cost-Aware Routing + Custom Rule Chain (Claude deep decomposition)

| Field | Value |
| --- | --- |
| Status | Deep decomposition — **truth-first finding: features advertised in README are NOT implemented in source** |
| Reference | Helicone AI Gateway (GPL-3.0, [E-LIC-007](../../07_REFERENCE_EVIDENCE_LEDGER.md)) |
| Feature in HUAKAI matrix | F-ROUTE-001 (L2) + F-CONFIG-001 (L2) |
| Specifier session | Claude PM-Orchestrator (Opus), 2026-04-29 |
| Source-reading delegate | Sonnet Explore agent — read 13 source files (~40min); structured factual report retained |
| Companion artifacts | docs/decompositions/helicone/cost-aware-routing-source-verified.md (Codex R3), .omc/artifacts/decomp-critic/C3-helicone-cost-routing.md (Codex critic) |
| **Truth-discipline** | **Observed regions: 13** / **Inferences: 1** / **Open questions: 5** / **Critical finding: 2 advertised features absent from source** |

> **Headline truth-first finding**: Helicone's README and public marketing list "cost-aware routing" and "custom-rule routing" as gateway capabilities. **Independent source reading by Sonnet found NEITHER feature implemented in `ai-gateway/` source.** This decomposition documents (a) what IS implemented (4 latency-based balance strategies + forced-routing header), (b) the gap between advertised and actual, and (c) what HUAKAI should learn from the gap.

> **Why this matters**: Per Owner's "保证真实 不造假" directive, a decomposition's value is its faithfulness to source, not its faithfulness to documentation. Aspirational feature claims in upstream READMEs do NOT count as evidence. This file is shorter than peers because the source supports less — by design.

---

## 1. WHY (motivation — observed-only)

The pressures that drive Helicone's actual implementation are NOT the cost-tier pressures the README advertises. The observed pressures are:

**Pressure 1 — multi-provider OpenAI-compat surface**: The gateway accepts OpenAI-shaped requests and dispatches to one of many provider backends with model-name translation. The hot path is therefore "pick a provider that supports the requested model and is up" rather than "pick the cheapest" `[region-1, region-7]`.

**Pressure 2 — latency variance smoothing**: When multiple providers offer the same model (e.g., `claude-3-5-sonnet` on Anthropic direct + AWS Bedrock + Vertex), latency varies meaningfully. The gateway's **observed** routing strategies are all latency-shaped: ProviderWeighted, BalancedLatency, ModelWeighted, ModelLatency `[region-9]`. Cost is NOT a routing dimension in source.

**Pressure 3 — operator forced-override escape hatch**: The `helicone-forced-routing` header lets operators pin a specific provider for a request. This is a path-level override, not a rule-chain — observable behavior, not advertised feature `[region-2]`.

**Pressure 4 (advertised but absent)**: The README's "cost optimization (cheapest option)" claim has NO supporting source. The `rust_decimal` crate is in `Cargo.toml` (suggesting intent) but `config/providers.rs` does NOT contain pricing fields and the four observed routing strategies do NOT take cost as input.

---

## 2. WHAT (algorithm in HUAKAI vocabulary)

### Sub-behaviors S-1..S-9 (observed-only — not 18+ as the prompt requested, because the source does not support more)

**S-1: Path classification by URL parsing** `[region-2]`. Inbound URLs are parsed into one of three RouteType: Router (named operator-config router), UnifiedApi (direct OpenAI-compat path), DirectProxy (provider-keyed proxy path). Each path triggers a different downstream service.

**S-2: Forced-routing header override** `[region-2]`. A header `helicone-forced-routing` short-circuits normal path classification, pinning a specific provider for the request. This is the gateway's only runtime rule-override mechanism.

**S-3: Provider-weighted load distribution** `[region-9]`. Static traffic-share weights per provider (e.g., 70% Anthropic / 30% Bedrock). Operator-declared in router config; weights are `Decimal` for precise distribution math.

**S-4: BalancedLatency strategy (P2C + Peak EWMA)** `[region-9, region-3]`. Power-of-two-choices: pick two providers at random; compare each's exponentially-weighted moving average of recent latency; route to the lower. Adapts to current latency conditions.

**S-5: Model-weighted strategy** `[region-9]`. Weights keyed by (provider, model) pair rather than just provider. Allows operator to declare "model-X traffic to provider-A" while "model-Y traffic split A/B".

**S-6: ModelLatency strategy** `[region-9]`. Latency-optimized provider selection per-model. Same EWMA mechanism as S-4 but tracking per-model latency separately.

**S-7: GCRA-based rate limiting** `[region-10]`. Generic Cell Rate Algorithm with per-organization configurable capacity and refill frequency. Each request consumes 1 unit (no per-operation cost differentiation).

**S-8: Static model mapping (no dynamic rules)** `[region-13]`. Source-target endpoint converters in a registry — explicit static mapping (e.g., OpenAI Chat → Anthropic Messages). NOT a rule chain; matchers are fixed at config time.

**S-9: Router store with versioning** `[region-11]`. Router configs persisted with `version_id` UUID and `version` integer; soft-delete via deleted_at timestamp; per-organization scoping. Versioning is on the CONFIG schema, not on routing rules.

### Behaviors NOT observed (advertised but absent)

The following are documented as features in Helicone's README but Sonnet's source read found **no implementation**:

- **Cost-aware ranking**: no per-1K-token cost field anywhere; no cost in any of the 4 routing strategies.
- **Cheapest-provider routing**: NO routing strategy named "cheapest" or equivalent in `config/balance.rs`.
- **Custom rule chains**: no rule DSL, no rule evaluator, no rule-id stamping.
- **Tier-driven routing weight vectors**: no per-tenant routing-strategy override; rate limits are per-org but routing strategy is per-router-config.
- **Output-cost forecasting**: no token forecasting before request dispatch.

### 2-bis Lifecycle traces (2 observed, 1 marked open)

**L-1 Happy path (BalancedLatency strategy)**: Request enters → URL parsed → RouterDetails classifies as Router → MetaRouter dispatches → BalancedLatency picks 2-of-N providers via P2C → compares EWMA-tracked latency → routes to lower → provider responds → latency record updates EWMA store. End.

**L-2 Forced override**: Request enters with `helicone-forced-routing: anthropic` header → router_details captures forced provider → bypasses S-3..S-6 strategies → dispatches directly to anthropic provider service. End.

**L-3 Cost-routed selection** — moved to §9 Q-1 (cannot trace observed behavior because it does not exist in source).

---

## 3. INPUTS (observed)

**Per-Request inputs**: URL path, optional `helicone-forced-routing` header, model id (for ModelLatency/ModelWeighted), authentication context (organization_id, owner_id), API request body (pass-through to upstream).

**Per-Provider state**: latency EWMA window (in-memory), rate-limit GCRA bucket per organization, configured weights from router config.

**Per-Process state**: AppState with router config map, dispatcher service map, EWMA windows, GCRA buckets.

**Persistent state**: router config (in PostgreSQL via `store/router.rs`), API keys, provider credentials, organization mapping. NOT persisted: per-request routing decisions, latency samples (in-memory only), cost data (no schema for it).

**Configuration inputs**: balance strategy enum (Provider Weighted | BalancedLatency | ModelWeighted | ModelLatency), per-provider weights (when ProviderWeighted), per-(provider,model) weights (when ModelWeighted), GCRA capacity + refill, model-mapping registry.

---

## 4. FAILURE MODES (observed-only — only 5, not 12, because source supports only this many)

| FM-id | Trigger | Observable outcome | Operator signal | Recovery | Blast radius |
|---|---|---|---|---|---|
| FM-1 | Provider returns 5xx | Latency EWMA records the response time but no error-class tracking observed | log only | next strategy iteration may avoid via slow-EWMA | per-request |
| FM-2 | Rate limit (GCRA bucket empty) | 429 returned to client | log + metric | client retry / wait for refill | per-request |
| FM-3 | Forced provider unavailable | Forced-routing header points to dead provider; no fallback observed | error to client | manual operator change of routing target | per-request with this header |
| FM-4 | Router config update mid-traffic | Router store version bumps; in-flight requests on old version, new requests on new — no observed transition coordination | none | natural rollover | brief inconsistency window |
| FM-5 | EWMA poisoning | A single very-slow response biases EWMA against a healthy provider; no resistance to outliers observed `[inferred from region-9]` | latency metric drift | wait for EWMA decay | one provider's traffic share over a window |

---

## 5. INTERFACES TO HUAKAI

**Personal Edition**:
- HUAKAI's `pool.DefaultSelector` already implements layered-affinity (sticky → fresh) which is a stronger pattern than Helicone's strategies (which are flat). Helicone's BalancedLatency P2C is one signal worth adopting INTO HUAKAI's existing fresh-tier ranking — as an additional dimension (priority + load + last-used + latency-EWMA).
- Forced-routing header (S-2) maps to HUAKAI's `RoutingLayerForced` already in `routing_reason.go`.

**SaaS Edition**:
- Helicone's per-org GCRA rate-limit (S-7) maps to HUAKAI's F-RATE-001 per-tenant rate limiting; Helicone's bucket-per-org pattern is structurally compatible.
- Static model mapping (S-8) maps to HUAKAI's F-PROTO-002 capability matrix; HUAKAI's matrix is more expressive (per (client, upstream, feature) cells) than Helicone's pairwise endpoint converters.

**Cross-feature**:
- Cost-as-routing-input (the unimplemented feature): HUAKAI should implement this as F-ROUTE-001's actual contribution — adding cost as a 4th ranking dimension in F-POOL-001 §3 fresh tier. Helicone's gap is HUAKAI's opportunity.
- Custom rule chains: HUAKAI's existing `routing_rule_chains` proposal in F-CONFIG-001 has no upstream precedent in working code; HUAKAI is creating, not borrowing.

---

## 6. RISKS HUAKAI MUST GUARD AGAINST

**R-1 [Documentation-source drift in evidence ledger]**: Helicone's README claims features that don't exist in source. Multiple HUAKAI documents reference helicone as a "cost-routing reference". This is **incorrect citation** — the reference does not implement the cited feature. **Mitigation**: every E-HLC-* row in `07_REFERENCE_EVIDENCE_LEDGER.md` must be re-classified as "advertised" vs "source-confirmed". Synthesis stage should down-grade or remove rows that cite features absent from source.

**R-2 [DR-001 multi-tenant — tenant-aware routing]**: Helicone's routing strategies are per-router-config, not per-tenant. In HUAKAI multi-tenant, routing strategy MUST be per-tenant overridable. Strategies and weight vectors live on tenant scope, not router-instance scope.

**R-3 [DR-006 PostgreSQL — EWMA persistence]**: Helicone's latency EWMA is in-memory only. On replica restart, all latency knowledge is lost. HUAKAI's PostgreSQL-backed `provider_accounts.last_used_at` is a coarser signal but consistent across replicas. **Mitigation**: if HUAKAI adopts EWMA, persist sample windows in a small `latency_samples` table (or accept reset-on-restart with a fast warm-up).

**R-4 [Forced-routing header authorization]**: Helicone's `helicone-forced-routing` header is unauthenticated within an authenticated request (no observed gate beyond auth). In HUAKAI, forced-routing has a per-tenant operator-authorization model (see F-POOL-001 AT-016). Don't inherit Helicone's open header.

**R-5 [Truth-discipline at synthesis stage]**: Codex's R3 specifier output for helicone may have written content based on README rather than source (the original critic flagged this risk; this Sonnet pass confirmed). When synthesizing, the **Sonnet-grounded truth** in this Claude-deep file must override any speculative Codex content.

---

## 7. SAFE ADAPTATION (concrete divergences)

1. **Implement the unimplemented**: cost as routing input, custom rule chains. HUAKAI's F-ROUTE-001 / F-CONFIG-001 have NO upstream blueprint; design fresh from product requirements (DR-001 multi-tenant, DR-002 dual editions).
2. **Adopt P2C + EWMA latency** as a 4th ranking dimension in F-POOL-001 §3 fresh tier (priority + load + last-used + latency-EWMA).
3. **Per-tenant routing strategy** instead of per-router-config.
4. **PostgreSQL-shared latency sample table** (or accept restart-warm-up); avoid Helicone's in-memory-only EWMA.
5. **Authenticated forced-routing** with per-tenant operator role + audit row (F-POOL-001 AT-016).
6. **Re-classify E-HLC-* evidence rows** as "advertised" vs "source-verified"; do NOT cite advertised features as observed evidence.

---

## 8. EVIDENCE LEDGER ROWS (proposed reclassification)

- **E-HLC-001 (existing — RECLASSIFY)**: "Smart load balancing + cost routing" — the load-balancing half is source-verified (4 strategies); the cost-routing half is advertised-only. Split into two rows.
- **E-HLC-002 (existing)**: Semantic cache — out of scope for this decomposition; status unknown in source.
- **E-HLC-006 (existing — RECLASSIFY)**: Custom-rule routing — advertised-only; no source. Mark as "advertised, not implemented".
- **E-HLC-DEEP-NEW-1**: P2C + EWMA latency-balanced routing strategy `[region-9]` — actually implemented, worth deep evidence.
- **E-HLC-DEEP-NEW-2**: GCRA rate limiting per org `[region-10]` — implemented; relevant to F-RATE-001.
- **E-HLC-DEEP-NEW-3**: Static endpoint converter registry `[region-13]` — relevant to F-PROTO-002 contrast.

---

## 9. OPEN QUESTIONS

1. **Q-1 Cost data origin (advertised feature)**: where is the cost source data supposed to live? README implies the gateway has it; source says no. Likely a control-plane responsibility delegated to Helicone's hosted service (not in `ai-gateway/` repo). Synthesis: confirm this is platform-only feature, not OSS-gateway feature.
2. **Q-2 `rust_decimal` dependency intent**: imported but unused in routing layer; was cost-aware routing planned-and-removed, planned-and-not-shipped, or used in a non-routing module not surveyed?
3. **Q-3 EWMA decay parameters**: window size, decay rate — operator-tunable or hardcoded?
4. **Q-4 P2C tie-breaking**: when two providers' EWMA are exactly equal, what's the deterministic order?
5. **Q-5 Semantic cache integration with routing**: Helicone advertises semantic cache (E-HLC-002); does cache-hit short-circuit routing entirely, or is cache an additional signal to the strategies? Out of scope here — flagged for separate decomposition.

---

## 10. SOURCE COVERAGE PROOF (Sonnet Explore agent, ~40min, 13 files)

| Region | URL | Contribution |
|---|---|---|
| region-1 | github.com/Helicone/ai-gateway/main/ai-gateway/src/router/mod.rs | Router submodule list (no cost or rule modules present) |
| region-2 | .../src/router/router_details.rs | Path classification + forced-routing header handling |
| region-3 | .../src/router/meta.rs | Three service paths dispatcher |
| region-4 | .../src/router/unified_api.rs | Direct OpenAI-compat path |
| region-5 | .../src/router/direct.rs | Provider-keyed dispatcher |
| region-6 | .../src/config/providers.rs | Provider config: model + URL + version (no pricing fields) |
| region-7 | .../src/dispatcher/bedrock_client.rs | AWS Bedrock dispatcher (no cost lookup) |
| region-8 | .../src/config/router.rs | RouterConfig schema (no rule declarations) |
| region-9 | .../src/config/balance.rs | 4 routing strategies — all latency / weight, no cost |
| region-10 | .../src/config/rate_limit.rs | GCRA rate limiting per org |
| region-11 | .../src/store/router.rs | Router config persistence with version_id + soft-delete |
| region-12 | .../src/metrics/mod.rs | Metrics: cache/error/auth — no routing-decision tracking |
| region-13 | .../src/middleware/mapper/registry.rs | Static endpoint converter registry |

---

## 11. ROUND-2 CRITIC FINDINGS (C3 helicone)

> Codex critic file at `.omc/artifacts/decomp-critic/C3-helicone-cost-routing.md`. This Claude-deep is written without reading C3. Synthesis stage merges Codex specifier-deep + C3 critic + this Claude-deep. **Critical**: synthesis must reconcile Codex's R3 specifier output (which may describe advertised behavior) against this Claude-deep's source-grounded reality.

---

## Owner Chinese summary

**头号发现 — truth-first 协议第一次抓到造假风险**：Helicone 的 README 公开宣传"成本路由 + 自定义规则链"，**但 Sonnet 真读 13 个源文件后确认这两个功能在 ai-gateway/ 源码里根本不存在**。源里只有 4 个延迟路由策略（ProviderWeighted / BalancedLatency / ModelWeighted / ModelLatency）+ 强制路由 header + 静态模型映射，无 per-token 成本表，无规则评估器。`rust_decimal` 依赖被声明却未被路由层使用。**这意味着 HUAKAI 既有的 E-HLC-001/006 evidence 引用项需要重分类**——区分"宣传中"和"源码确认"。HUAKAI 的 F-ROUTE-001 / F-CONFIG-001 因此没有真上游 blueprint，必须按多租户 + 双版本的产品需求自己设计。本 deep 拆解只记 9 个 sub-behavior（不是 prompt 要求的 18+），因为源码就这么多——truth-first 高于篇幅。本文件未读 codex specifier 或 critic 输出。
