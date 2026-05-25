# Helicone - Cost-Aware Routing and Operator Rule Chains

| Field | Value |
| --- | --- |
| Status | Draft |
| Reference | Helicone AI Gateway, GPL-3.0, E-LIC-007 |
| Feature in HUAKAI matrix | F-ROUTE-001 (L2), F-CONFIG-001 (L2) |
| Evidence ledger row | E-HLC-001, E-HLC-006 |
| Specifier session | Codex specifier-lane, 2026-04-29 |
| Specifier date | 2026-04-29 |
| Reviewer session | Pending reviewer-lane |
| Reviewer date | Pending |
| Source files read | https://github.com/Helicone/ai-gateway/blob/main/Cargo.toml<br>https://raw.githubusercontent.com/Helicone/ai-gateway/main/README.md<br>https://raw.githubusercontent.com/Helicone/ai-gateway/main/ai-gateway/config/embedded/providers.yaml<br>https://docs.helicone.ai/gateway/provider-routing<br>https://docs.helicone.ai/guides/cookbooks/cost-tracking |

## 1. WHY

Operators running production AI workloads need one gateway decision to balance cost, availability, latency, compliance, and upstream ownership. The upstream pressure is avoiding vendor lock-in while using the lowest priced Provider that can satisfy the requested Model, then recovering automatically when that Provider fails or is disallowed. Helicone's public behavior makes cost a first-class routing signal: a generic Model request can be expanded into candidate Providers, sorted by current price, and tried cheapest-first. This is distinct from Sub2API's layered account selection, which focuses on session affinity, sticky Provider Account reuse, and fresh layered Provider Account choice after another axis has already been selected. Helicone's contribution for HUAKAI is the Route-level decision using Provider price and operator constraints before a Channel or Provider Account is chosen.

Inference: the custom-rule surface exists because cheapest-first alone is not enough for enterprise operations. A free User Group may optimize for lowest cost, while an enterprise User Group may value latency, region, direct Provider Account credits, or explicit Provider exclusion. The public docs show rule-like customization through Provider locks, deployment targeting, ordered fallback chains, and Provider exclusion. Source metadata also shows separate routing strategy components and embedded Provider capability configuration, confirming that routing behavior is not a single hardcoded Provider switch.

## 2. WHAT

HUAKAI should model this feature as a Route that receives a logical Model request and resolves it into an ordered candidate list of Channels. First, the gateway resolves the requested Model against the Model Registry. The registry returns Providers and deployment variants known to support the Model, plus operator-owned Provider Account deployments that may accept custom Models.

Second, the Route applies operator constraints. Constraints can include allowed Providers, excluded Providers, region or compliance requirements, User Group eligibility, request tags, and explicit Channel preference order. If the request names a single Provider or a single deployment, the candidate list is narrowed to that target and normal automatic failover outside that target is disabled unless the Route explicitly allows it.

Third, the Route scores the remaining candidates. For cost-aware mode, the primary score is estimated per-request cost from Model price data. The comparison must account for request token and completion token rates where available; if pricing is partial, HUAKAI should mark the routing reason as estimated. Equal-cost candidates can be balanced by weight, randomization, or observed health.

Fourth, the gateway tries candidates in order. A successful upstream response creates a Usage Record with selected Channel, Provider Account, Model, cost context, and routing reason. Retryable failures advance to the next candidate. Non-retryable policy failures stop the Route.

Fifth, custom-rule routing should be represented as an ordered Route chain. Each rule has match criteria and a strategy directive. A free User Group Route can prefer cheapest Channels; an enterprise User Group Route can prefer fastest healthy Channels; a regulated User Group Route can use only region-approved Channels. The UI wizard and config-as-code file should generate the same Route artifact.

## 3. INPUTS

- Client request: logical Model, optional explicit Provider or deployment target, streaming flag, and request metadata.
- User state: User, User Group, API Key, allowed Models, quota state, and policy tags.
- Route configuration: match criteria, strategy directive, Provider allow/exclude list, ordered fallback list, weights, timeout policy, and failover policy.
- Model Registry: Provider support for each Model, aliases, upstream Model names, capability flags, and pricing data.
- Channel state: enabled or paused status, allowed Models, Channel limits, health status, and Provider Account pool.
- Provider Account state: lifecycle state, upstream credential availability, region, direct-credit preference, quota exhaustion, and investigation flags.
- Runtime signals: observed latency, error rate, timeouts, rate-limit responses, and recent retry outcomes.
- Cost signals: per-token prices, Provider-declared costs when available, cached price version, markup policy if any, and missing-price status.
- Time and randomness: retry deadlines, price-cache age, health-window age, and tie-breakers.

## 4. FAILURE MODES HANDLED

- Provider outage: timeout or server error advances to the next eligible candidate and records retry context.
- Provider rate limit: upstream rate-limit status tries the next candidate if the Route allows failover.
- Authentication failure for one Provider Account: upstream auth error triggers failover when another eligible Channel exists.
- Context or capability mismatch: unsupported candidates are removed; an empty list returns no eligible Channel.
- Operator Provider exclusion: excluded Providers are removed before scoring and preserved in the routing reason.
- Explicit Provider or deployment lock: broad automatic failover is skipped unless the Route allows it.
- Equal cost among candidates: equal-cost candidates are load balanced rather than selected by arbitrary order.

## 5. INTERFACES TO HUAKAI

- Model Registry supplies Model-to-Provider capability and price data.
- Route engine consumes User Group, request tags, and candidates, then emits an ordered Channel plan.
- Channel selection chooses the Provider Account inside the selected Channel.
- Quota reservation must happen before upstream spend; final Usage Record reconciliation must use actual usage when available.
- Usage Record stores selected Channel, Provider Account, price version, strategy, retry count, and routing reason.
- Admin Ops UI exposes Route rules, strategy mode, Provider exclusions, fallback order, price freshness, and "why this Channel" explanations.
- Config-as-code loader validates Route artifacts and produces Audit Events on reload or wizard-generated changes.

## 6. RISKS

- GPL contamination risk: Helicone source may verify behavior, but HUAKAI must not copy code, internal identifiers, schemas, tests, or structure.
- Price drift risk: stale Model Registry prices can route traffic to a Provider that is no longer cheapest or undercharge a User.
- Capability drift risk: a Provider may advertise a Model but lack a requested capability, causing expensive retry chains.
- Cost-only risk: cheapest-first routing can concentrate traffic on undertested Providers.
- Tenant leakage risk: custom properties, User Group matches, and Provider exclusions must not expose another tenant's Provider Account inventory.
- Audit risk: UI wizard changes and config-as-code reloads can diverge unless both produce the same Route artifact and Audit Event.
- Billing risk: estimated routing cost is not final spend; final Usage Record settlement must not rely solely on pre-call estimates.

## 7. SAFE ADAPTATION FOR HUAKAI

- **KEEP**: Treat cost as a first-class Route score, not only as a dashboard metric after the request.
- **KEEP**: Allow explicit Provider/deployment locks, ordered fallback chains, and Provider exclusion as operator controls.
- **KEEP**: Load balance equal-cost candidates instead of relying on a fixed order.
- **IMPROVE**: Require a routing reason on every Usage Record so operators can see whether cost, health, policy, or explicit lock drove selection.
- **IMPROVE**: Add price-versioning and freshness checks; stale or missing prices should degrade to a safe policy rather than silently claiming "cheapest."
- **IMPROVE**: Separate estimated pre-call cost from final billing settlement, with reconciliation when provider-reported usage arrives late or is missing.
- **IMPROVE**: Make UI wizard and config-as-code share one validated Route artifact with Audit Events and rollback.
- **AVOID**: Copying GPL routing implementation modules, config structure, internal names, comments, or tests.
- **AVOID**: Letting cost optimization override compliance, User Group policy, disabled Channel status, quota reservation, or Provider Account health.

## 8. EVIDENCE LEDGER ROWS

- E-HLC-001: Smart routing uses latency, cost, reliability, provider uptime, and rate-limit awareness; relevant to F-ROUTE-001.
- E-HLC-006: Declarative per-endpoint routing policy and UI wizard behavior; relevant to F-CONFIG-001.
- E-LIC-007: Helicone AI Gateway is GPL-3.0 for HUAKAI policy purposes; use behavior-only source verification and Safe Equivalent implementation.

## 9. OPEN QUESTIONS

- Does HUAKAI want cost-aware routing to use estimated prompt size before sending the request, historical average completion size, or both?
- Should "cheapest" be hard cheapest, cheapest among healthy Channels, or cheapest within an operator-defined reliability floor?
- What price staleness threshold should force a Route into warning, fallback, or manual approval?
- Should client-specified Provider locks be allowed for all Users, or only for User Groups granted explicit Provider-targeting permission?
- Should equal-cost balancing be random, weighted, latency-aware, or Provider Account quota-aware?
- How should Admin Ops display a failed candidate chain without exposing sensitive Provider Account details across tenants?

## Owner Summary

本次拆解的是 Helicone AI Gateway 的 cheapest-provider routing 与 custom-rule routing：它和已有 sub2api/layered-account-selection.md 的关键差异是，sub2api 关注已进入账号选择阶段后的 sticky / fresh / layered Provider Account 选择，而 Helicone 的独特价值是把 cost 作为 Route 选择的一等信号，并允许操作员通过 Provider 锁定、排除、显式 fallback chain、部署目标和策略配置改变候选 Channel 顺序。HUAKAI 应吸收的是 cost-aware Route score、等价成本负载均衡、策略化 fallback、配置与 UI 同源、Usage Record 中的 routing reason、price version/freshness 和最终计费 reconciliation；不能吸收的是 GPL 源码、内部命名、结构或测试，后续实现必须是 Safe Equivalent，没有功能缩水。
