# `helicone` — Cost-Aware Routing + Custom Rule Routing (Claude draft)

| Field | Value |
| --- | --- |
| Status | Draft (Claude lane parallel viewpoint to Codex T3 specifier) |
| Reference | Helicone AI Gateway (GPL-3.0, [E-LIC-007]) |
| Feature in HUAKAI matrix | F-ROUTE-001 (L2) + F-CONFIG-001 (L2) |
| Evidence anchors | E-HLC-001 (smart load balancing + cost routing), E-HLC-006 (custom-rule routing) |
| Specifier session | Claude PM-Orchestrator, parallel viewpoint, 2026-04-29 |
| Companion artifact | docs/decompositions/helicone/cost-aware-routing-source-verified.md (Codex T3) |
| Source files read | NONE directly. This draft reasons from helicone/_INVENTORY.md + the existing helicone observability-source-verified.md + HUAKAI's existing F-POOL-001 layered selection + F-OBS-001 billing surface to identify what cost-aware routing as a peer concept looks like. Codex T3 fills in algorithm. |

> **Lane discipline**: This draft is intentionally NOT source-verified. Codex T3 reads source; this Claude draft frames structural risks + HUAKAI-fit constraints. Synthesis combines.

> **License warning**: Helicone AI Gateway is GPL-3.0. HUAKAI implementation must be **Safe Equivalent** (different code, same behavior). Decomposition reading is permitted under DR-000 Option C.

## 1. WHY (motivation)

Sub2API and one-api solve "pick a healthy upstream" — a routing problem dominated by **availability + capability** signals. Helicone's distinct contribution is treating **cost** as a first-class routing input. This unlocks two operator workflows that monolithic relays cannot:

1. **Tier-driven routing**: free-tier traffic routes to the cheapest provider that satisfies the request's capability minimum; paid-tier traffic routes to the fastest or highest-quality provider regardless of cost. This converts the operator's pricing tiers into a deterministic upstream allocation policy without per-request decisioning.

2. **Cost-spike defense**: when a provider raises prices mid-month or a popular model migrates to a more expensive variant, the operator can declare a routing rule that excludes that path for tenants whose plan no longer matches the cost. The relay reroutes silently; users see the same model name, the operator's margin stays intact.

Both workflows are essential to HUAKAI's Owner-stated dual business model (sell API + sell SaaS). Without cost-as-signal, an operator who sells a "free tier" cannot enforce its economics.

## 2. WHAT (algorithm in HUAKAI vocabulary, structural; algorithm details await Codex T3)

### 2.1 Cost as a routing dimension

The Pool-selection algorithm in F-POOL-001 currently scores candidates on three axes (priority, load, last-used). Cost-aware routing introduces a **fourth dimension**: per-1K-token estimated cost for the requested model under the candidate Account's contract.

The estimated cost depends on:

- The candidate Account's pricing snapshot (per-Account multipliers may differ even within the same provider; an enterprise account negotiates lower rates than a startup account).
- The User's prompt length (input cost component) and a forecast of output length (forecast lives in the per-Route policy).
- The cache-bucket likelihood (if the Account's prompt cache has warmed for this prompt, cache-read pricing applies; if not, fresh).

The Pool ranking function receives a per-tenant policy that **chooses how cost weighs against load**: `weight_cost`, `weight_load`, `weight_latency`, `weight_priority`. A Free-tier policy might be `(0.7, 0.1, 0.1, 0.1)`; an Enterprise-tier might be `(0.0, 0.2, 0.6, 0.2)`. Selection sorts by weighted score and then breaks ties via the existing layered fallback (sticky → fresh).

### 2.2 Custom-rule routing (rule chains)

Operators declare an ordered list of named rules; the first rule that matches a request determines the routing constraints (allow-list / deny-list / pool-group selection / capability filter overrides). Rules evaluate against:

- Request attributes: model id, endpoint family, payload-byte count, has-tool-use flag, has-images flag.
- Tenant attributes: tier, group, geographic region.
- Time attributes: current hour, day-of-week, current operator-defined "peak window".
- Account attributes: provider, region, capability flags.

Rule outcomes are **deterministic and auditable** — every routing decision under rule chains stamps the matched rule's stable id into routing_reason JSON so operator can later answer "why did request X go to account Y?"

## 3. INPUTS (HUAKAI signals)

- Per-request: model id, prompt token estimate, expected output token estimate, has-tool-use, has-images.
- Per-Account: pricing snapshot (per-bucket multipliers + base rates), provider, region, last-warmed-cache-prefix-hash window.
- Per-Tenant: weight vector (cost / load / latency / priority), rule chain version, tier id.
- Per-Operator: rule-chain definitions, peak windows, peak-window weight overrides.

## 4. FAILURE MODES HANDLED

- **Stale pricing snapshot**: if a candidate Account's pricing snapshot is older than the operator's freshness threshold, the candidate is excluded from cost-aware ranking (falls back to load+latency-only score) AND the routing reason records `pricing_stale=true` so operator can refresh.
- **Rule chain that excludes all accounts**: routing reason records the matched rule's exclusion, all accounts excluded → fail with `ROUTE_RULE_NO_VIABLE_ACCOUNT` rather than picking a forbidden account silently.
- **Rule chain conflict (two rules match contradictorily)**: deterministic order resolution by chain position; second match is ignored. Audit records both matches so operator knows the second rule never fired.
- **Cost forecast drift**: actual cost diverges from forecast by >threshold → log a forecast-drift event so the per-Route output-token forecast is recalibrated.

## 5. INTERFACES TO HUAKAI

- **F-POOL-001 selector**: ranking function gains a 4th dimension; weight vector becomes part of the per-Route policy snapshot.
- **F-CONFIG-001 admin API**: rule-chain CRUD + versioning. Rule chains are versioned alongside billing-policy versions.
- **F-OBS-001 routing_reason**: schema gains `rule_id_matched`, `rule_chain_version`, `weight_vector_snapshot` fields. (Existing schema in `docs/schema/pool-routing.sql` does not have these — needs additive migration.)
- **F-BILL-003 pricing**: cost-aware routing requires a pricing snapshot per (Account, model, billing_policy_version). Pricing tables already partially exist for billing; add a snapshot read API.

## 6. RISKS HUAKAI MUST GUARD AGAINST

- **Cost-routing race-to-the-bottom**: if every tenant chooses cheapest, the cheapest provider gets DDOS'd into rate-limit; HUAKAI must surface a per-Provider concurrency cap that overrides cost weighting (existing F-POOL-001 9-gate chain handles concurrency cap, so this risk is already bounded).
- **Rule chain authoring abuse**: an operator can author a rule that excludes premium accounts during peak hours to game their own margin reporting. Rule changes need an audit log + operator review threshold (e.g. "rule changes affecting >20% of traffic require dual approval").
- **Forecast attack**: an adversarial user crafts prompts that pass the input-cost check (looks small) but balloon output-cost (asks the model to return a huge JSON). Output-cost forecasting needs an upper-cap honored by the streaming forwarder's drain budget (already in F-GW-002 §C-bis).
- **Tier inversion via SaaS multi-tenancy**: if an operator's free tier maps to cheapest provider but a SaaS tenant within that operator's plane has paid for premium, the routing must honor the SaaS tenant's tier even when the parent operator's policy says cheapest. This is the DR-001 multi-tenant isolation guarantee re-applied to cost.

## 7. SAFE ADAPTATION FOR HUAKAI (clean-room divergences)

- Helicone is GPL-3.0; HUAKAI MIT means **algorithm behavior copied is safe; line-by-line code transcription is not**. Implementer-lane MUST design ranking + rule engine from contract (this draft + Codex T3) without reading helicone source.
- Helicone is single-tier; HUAKAI's SaaS Edition has the parent-operator vs SaaS-tenant nesting (DR-002). Rule chains must be scoped at both layers (operator-global + SaaS-tenant override) and the relay merges them in deterministic order before ranking.
- Helicone's rule engine likely has a config-file or database surface; HUAKAI uses PostgreSQL via sqlc (DR-006). Rule chains live in `routing_rule_chains` table (additive migration), versioned alongside `billing_pricing_versions`.

## 8. EVIDENCE LEDGER ROWS NEEDED

- E-HLC-001 (existing): smart load balancing + cost routing — promote to deep when Codex T3 lands.
- E-HLC-006 (existing): custom-rule routing — promote similarly.
- E-HLC-NEW: peak-window rule scoping (Codex T3 may surface as separate behavior).

## 9. OPEN QUESTIONS (for Codex T3 to resolve from source)

1. Does helicone implement weight vectors per-tenant or per-route? Determines whether HUAKAI's policy column lives on `tenants` or on `routes` (or both with override semantics).
2. How does helicone forecast output-token cost when the user's prompt is the only input — is it model-fixed, percentile-based, ML?
3. Does helicone have a "conservative cost ceiling" that excludes a candidate if any pricing field is missing, or does it default-substitute the provider's published rate?
4. Are rule outcomes additive (each matching rule contributes constraints) or first-match-wins? Audit semantics differ.
5. Does helicone version rule chains independently or together with billing policy? HUAKAI's existing schema versions billing alone — needs answer to decide.

## Owner Chinese summary

本 draft 是 Claude lane 对 helicone **成本感知路由 + 自定义规则链路由**两个 GPL-3.0 项目亮点的独立结构化拆解，未读源码。与 Codex T3 配对做 synthesis 后写进 HUAKAI F-ROUTE-001。最大风险点：cost-routing race-to-the-bottom、规则链 authoring 滥用、forecast 攻击、SaaS 双层租户的 tier 反转——这些是 HUAKAI 多租户上下文产生的，helicone 单层不会有；synthesis 阶段必须把这一层风险写进 spec。法律：GPL 行为可借鉴，代码不可。
