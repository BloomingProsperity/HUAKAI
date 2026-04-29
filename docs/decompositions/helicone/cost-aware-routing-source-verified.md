# Helicone cost-aware routing + custom routing R3 source-verified decomposition

| Field | Value |
| --- | --- |
| Project | helicone |
| Feature | Cheapest-provider routing + custom-rule chain routing + cost forecast + tier-driven weight vectors |
| HUAKAI row | F-ROUTE-001 (L2) + F-CONFIG-001 (L2) |
| Lane | Codex specifier-lane R3 |
| Date | 2026-04-29 |
| Source posture | Clean-room behavior decomposition only. Upstream code identifiers, file paths, schema names, and distinctive implementation structure are intentionally omitted. |
| Critic read first | Yes: `.omc/artifacts/decomp-critic/C3-helicone-cost-routing.md` |
| Truth-discipline | Observed regions: 12 / Inferences: 8 / Open questions: 9 |

## §1 WHY

The observed design pressure is multi-provider operational continuity: provider downtime, provider rate limits, regional availability, and vendor lock-in are listed as the problems that provider routing tries to reduce [region-1]. A second pressure is cost control: the docs say default routing finds providers for a requested model, selects a cheapest available provider, and uses provider credits without markup [region-2]. A third pressure is operator control: docs expose request-level provider locking, tenant-owned deployment targeting, manual fallback order, and provider exclusion as ways to override the default routing path [region-3][region-4]. A fourth pressure is accounting visibility: cost documentation says gateway usage has complete visibility for precise cost calculation, while direct provider integrations are only best-effort estimates [region-8]. A fifth pressure is spend prevention: rate-limit documentation supports request-count and cost-based limits at global, user, and custom-property scopes [region-9].

HUAKAI should treat this as a routing-policy family, not as a single "cheapest" switch. The safe local equivalent needs a typed Route policy that combines Model Registry lookup, Channel eligibility, Provider Account eligibility, effective-cost ranking, failover rules, quota and Billing Ledger constraints, and Usage Record auditability.

## §2 WHAT

S-1. Default model routing is documented as zero-configuration for the client: the client sends a logical model request and the gateway finds providers that offer that model [region-2].

S-2. The default selection step is documented as cheapest-provider-first among available providers for the requested model [region-2].

S-3. Equal-cost providers are documented as load balanced, but the accessible evidence does not show the balancing algorithm or whether state is per-process or shared [region-2].

S-4. Routing priority is documented as tenant-owned provider keys first, then managed provider keys/credits as fallback [region-2][region-4].

S-5. Managed credits are documented as a way for the gateway operator to hold provider credentials and charge at provider price with no markup [region-2][region-6].

S-6. Provider locking is documented as a request-level model hint that restricts routing to one provider and disables automatic failover to other providers [region-3].

S-7. Deployment targeting is documented as a request-level hint that routes only through a configured deployment, with regional data residency as a named use case [region-3].

S-8. Manual fallback chain routing is documented as an ordered request-level list where each item is tried in the specified order [region-3].

S-9. BYOK routing is documented as tenant-owned provider credentials being tried before managed credentials, while still preserving managed fallback when allowed [region-4].

S-10. Unknown model/provider combinations are documented as forwardable, but unknown models only route through tenant-owned deployments [region-4].

S-11. Provider exclusion is documented as a request-level hint that removes named providers from automatic routing, including multiple exclusions [region-5].

S-12. Failover is documented for provider errors, including rate-limit, authentication, context-length, timeout, and server-error status classes [region-6].

S-13. Separate retry documentation narrows automatic retry behavior to rate-limit and server-side gateway/server statuses and says other client errors are not retried, creating an observed inconsistency with provider-routing failover on authentication and context-length errors [region-6][region-10].

S-14. Cost optimization docs say gateway cost calculation is precise because gateway usage has complete model-usage visibility and uses a model registry pricing system [region-8].

S-15. Cost optimization docs say automatic optimization uses BYOK priority, cost-based routing, and fallback to the next cheapest option [region-8].

S-16. Cost visibility docs support segmentation by user tier, feature, and environment through custom properties, which is the observed basis for HUAKAI tier-driven reporting and policy inputs [region-8].

S-17. Rate-limit docs support cost-based limits using cents as the unit, plus user and custom-property segmentation [region-9].

S-18. Rate-limit docs say a rate-limited request returns a client-visible rate-limit error [region-9].

S-19. Public README-level evidence claims routing strategies include fastest, cheapest, reliable, latency-aware, weighted distribution, and model-weight distribution; accessible evidence does not show the configuration grammar or implementation for weight vectors [region-11].

S-20. Provider inventory evidence shows the gateway maintains an embedded provider/model catalog with provider base endpoints and supported model lists, but it does not show prices or tenant-specific Provider Account state [region-12].

## §2-bis Lifecycle traces

Trace A: cheapest default path. A client submits a logical Model without a provider lock. The gateway consults the model/provider registry, applies the documented priority of tenant-owned Provider Accounts before managed Provider Accounts, selects the cheapest available candidate, and logs cost/performance data after the provider response [region-2][region-7][region-8].

Trace B: compliance-locked path. A client or Route policy constrains the request to one provider or one deployment. The observed docs say the request only goes through that target and does not automatically fail over elsewhere, which makes retry/backoff within the same target safer than cross-provider fallback for residency-sensitive traffic [region-3][region-10].

Trace C: manual fallback path. A request carries an ordered chain. The gateway tries the first requested provider/deployment and, on observed failover-trigger statuses, advances through the chain in request-specified order; if the chain ends with a generic model request, automatic provider discovery resumes for the remaining step [region-3][region-6].

Trace D: exclusion path. A request excludes one or more providers. The gateway removes those providers from automatic routing and tries the remaining available providers; the observed evidence does not state what happens when all candidates are excluded [region-5].

Trace E: cost-control path. A request enters with a cost-based limit policy attached by user or custom property. Cost documentation says the gateway has precise model-usage visibility for gateway calls; rate-limit documentation says cost-based windows can be enforced and return a rate-limit error when exceeded [region-8][region-9].

## §3 INPUTS

Observed input inventory:

| Input | Observed role | Source |
| --- | --- | --- |
| Logical Model | Client-facing model selector used for provider discovery and default cheapest routing. | [region-2] |
| Provider hint | Request-level constraint to a named provider. | [region-3] |
| Deployment hint | Request-level constraint to a configured deployment. | [region-3] |
| Manual fallback chain | Request-level ordered list of provider/model choices. | [region-3] |
| Provider exclusion hint | Request-level deny hint for automatic routing. | [region-5] |
| Tenant-owned provider credentials | Higher-priority Provider Account source before managed credentials. | [region-2][region-4] |
| Managed provider credits | Operator-managed Provider Account source used as fallback and no-markup access. | [region-2][region-7] |
| Model Registry pricing | Cost-ranking and post-call cost calculation input. | [region-8] |
| Custom properties | Segmentation dimensions such as user tier, feature, and environment. | [region-8] |
| Rate-limit policy | Request-count or cost-window policy at global, user, or custom-property scope. | [region-9] |
| Provider/model catalog | Static catalog of providers, supported models, and base endpoints. | [region-12] |

Not observed: exact parser AST, exact price dimensions, exact effective-cost formula, equal-cost load-balance state, health-score storage, and tier-to-weight mapping.

## §4 FAILURE MODES

Observed-only failure modes:

| Failure mode | Observed behavior | HUAKAI handling requirement |
| --- | --- | --- |
| Provider outage | Gateway is positioned to fail over to another provider. | Record fallback reason on Usage Record; preserve tenant authorization before next Channel. |
| Provider rate limit | Provider-routing docs and retry docs both treat rate limits as retry/failover-worthy. | Distinguish upstream Provider Account cooldown from User quota denial. |
| Regional restriction | Regional availability is listed as a routing problem; deployment targeting is shown for data residency. | Region lock must override cheapest routing and cross-region fallback. |
| Vendor lock-in | Multi-provider routing is positioned as a flexibility/cost solution. | Route policy must support multiple Channels without exposing Provider Account details to Users. |
| Authentication error | Provider-routing docs list authentication errors as failover triggers. | HUAKAI should fail closed by default for Provider Account credential failure unless an authorized replacement exists. |
| Context-length error | Provider-routing docs list context-length errors as failover triggers. | HUAKAI should only retry to a candidate with known larger context support; otherwise fail deterministically. |
| Timeout | Provider-routing docs list timeout as a failover trigger. | Timeout fallback must record partial/unknown usage and idempotency state. |
| Server error | Provider-routing docs and retry docs both treat server-side failures as retry/failover candidates. | Apply bounded fallback and operator-visible degraded state. |
| Cost estimate gap outside gateway | Cost docs say non-gateway integrations are best-effort. | HUAKAI gateway path should store pricing context and actual usage; non-gateway paths cannot claim exact cost. |

## §5 INTERFACES TO HUAKAI

Personal Edition:

- Route policy is local and single-tenant by default, but still stores tenant scope per DR-001.
- Cheapest routing may use local Provider Accounts, managed-like operator Provider Accounts, or both.
- UI can expose simple Route presets: cheapest, locked provider, ordered fallback, excluded provider set, and cost cap.
- Usage Record must include chosen Route, Channel, Provider Account, routing reason, estimated cost snapshot, final cost, and fallback attempt count.
- Billing Ledger is optional only if the operator is not monetizing; DR-002 says Personal Edition can be a commercial relay-station, so money-path support cannot be SaaS-only.

SaaS Edition:

- Every candidate Channel and Provider Account must be tenant-authorized before cost ranking.
- Request-level hints are lower authority than tenant/admin allow/deny policy.
- Tenant-owned Provider Accounts and platform-managed Provider Accounts must never be mixed without an explicit tenant policy.
- Shared state for health, cooldown, price snapshots, equal-cost rotation, and audit must be PostgreSQL-backed or explicitly coordinated per DR-006.
- Cross-tenant observability must never expose Provider Account identity or costs outside tenant/operator authorization.

## §6 RISKS

R-1. (inference, not observed) If HUAKAI copies request-string routing as the authoritative policy model, User input could override admin Route policy. Safer design: parse hints into a typed request intent, then evaluate tenant/admin Route policy first.

R-2. (inference, not observed) Authentication-error failover can mask expired or stolen Provider Account credentials. Safer design: default fail-closed, mark Provider Account `under-investigation` or `expired`, and only fallback to tenant-authorized replacements.

R-3. (inference, not observed) Context-length failover can amplify deterministic request errors. Safer design: require known context-window metadata before retrying to another Channel.

R-4. (inference, not observed) Equal public price does not mean equal tenant effective cost. Safer design: rank by effective cost including contract price, region, cache/batch dimensions, budget state, and Provider Account quota headroom.

R-5. (inference, not observed) Per-process equal-cost balancing or health state would diverge across replicas. DR-006 pushes HUAKAI toward durable decision logs and shared coordination.

R-6. (inference, not observed) Unknown model fallback into platform-managed Provider Accounts could leak tenant data to an unintended provider. Safer design: unknown Model plus no tenant-owned deployment fails closed.

R-7. (inference, not observed) Cost-based limits enforced after completion can overspend on long generations. Safer design: pre-call quota reservation using estimated max cost, then post-call reconciliation.

R-8. (inference, not observed) Tier-driven weight vectors can starve lower tiers or overuse cheap Providers if they are not audited. Safer design: store tier policy version and chosen weight vector on Usage Record.

## §7 SAFE ADAPTATION

HUAKAI should implement this as `Implemented Better` for F-ROUTE-001 and `Implemented` for F-CONFIG-001:

1. Use typed Route policies, not request-string grammar, as the source of truth.
2. Preserve request hints as optional inputs: provider lock, deployment lock, manual fallback chain, provider exclusion.
3. Enforce precedence: admin deny > tenant allow/deny > edition policy > User Group policy > request hint > cheapest/effective-cost optimizer.
4. Rank by effective cost, not public list price only.
5. Record both estimated pre-call cost and actual post-call cost on Usage Record.
6. Require fail-closed defaults for credential, compliance, unknown Model, all-candidates-excluded, and budget/quota denial.
7. Persist routing decisions, fallback attempts, cooldown state, pricing snapshots, and policy versions in PostgreSQL.
8. Expose UI wizard and config-as-code as two editors of the same Route policy artifact.
9. Add acceptance tests for parser edge cases, failover taxonomy, stale registry/price data, multi-replica balancing, and tier-weight fairness.

## §8 EVIDENCE LEDGER ROWS

| Evidence ID | Feature row | Evidence summary | Disposition |
| --- | --- | --- | --- |
| E-HLC-R3-ROUTE-001 | F-ROUTE-001 | Docs observe provider discovery, BYOK priority, cheapest-first selection, equal-cost load balancing, and failover. | Implemented Better |
| E-HLC-R3-ROUTE-002 | F-ROUTE-001 | Docs observe provider lock, deployment lock, manual fallback chain, unknown-model BYOK-only routing, and provider exclusion. | Safe Equivalent |
| E-HLC-R3-ROUTE-003 | F-ROUTE-001 | Docs observe failover triggers that include rate-limit, auth, context-length, timeout, and server errors; retry docs conflict by treating most non-429 4xx as non-retryable. | Implemented Better |
| E-HLC-R3-COST-001 | F-ROUTE-001 | Cost docs observe exact gateway cost calculation via model registry visibility and best-effort non-gateway estimates. | Implemented Better |
| E-HLC-R3-CONFIG-001 | F-CONFIG-001 | README/docs observe declarative config and UI wizard surfaces for routing configuration; details of full route grammar remain open. | Implemented |
| E-HLC-R3-LIMIT-001 | F-ROUTE-001 / F-SEC-006 | Rate-limit docs observe request and cents units, user/custom-property segmentation, and 429 denial. | Implemented Better |

## §9 OPEN QUESTIONS

OQ-1. What exact implementation path parses provider locks, deployment locks, manual fallback chains, and exclusions?

OQ-2. What is the exact conflict behavior when a provider is both in a manual chain and excluded?

OQ-3. What happens when exclusions remove every candidate?

OQ-4. Is equal-cost load balancing round-robin, random, weighted, latency-aware, or another method?

OQ-5. Is health/cooldown/load-balance state process-local, shared, or durable?

OQ-6. What pricing dimensions are used before the request when output tokens are unknown?

OQ-7. How are streaming partial failures accounted and retried, if at all?

OQ-8. What is the exact implementation of weighted distribution and model-weight routing?

OQ-9. How does the cloud gateway distinguish tenant-owned Provider Account failures from managed Provider Account failures in audit logs?

## §10 SOURCE COVERAGE PROOF

| Region | Evidence read | What it contributed |
| --- | --- | --- |
| region-1 | Provider Routing docs, problem/solution section, lines 112-142 | Routing exists to address outages, rate limits, regional restrictions, and vendor lock-in. |
| region-2 | Provider Routing docs, default routing and "How It Works", lines 144-172 | Provider discovery, cheapest-first selection, equal-cost balancing, BYOK priority, managed credits fallback. |
| region-3 | Provider Routing docs, lock/deployment/manual fallback, lines 175-205 | Provider lock disables automatic failover; deployment lock targets configured deployment; manual chains are ordered. |
| region-4 | Provider Routing docs, BYOK and unknown models, lines 207-211 | Tenant-owned keys are tried first; unknown models route only through tenant-owned deployments. |
| region-5 | Provider Routing docs, provider exclusion and examples, lines 213-219 and 277-286 | Request-level provider exclusions remove named providers from automatic routing. |
| region-6 | Provider Routing docs, failover triggers and examples, lines 222-231 and 238-261 | Observed trigger list and example fallback behavior. |
| region-7 | AI Gateway Overview docs, workflow, lines 127-138 | Gateway translates/routes, provider responds, metrics/costs/errors are logged and returned. |
| region-8 | Cost Tracking docs, cost calculation and optimization, lines 119-133 and 184-212 | Gateway cost calculation precision, best-effort non-gateway estimates, model registry pricing, BYOK/cost/fallback optimization sequence. |
| region-9 | Custom Rate Limits docs, limits and cost-based policy, lines 113-180 and 238-305 | Request/cost limits, global/user/custom-property scopes, cents unit, 429 denial. |
| region-10 | Retries docs, retry mechanics and triggers, lines 100-124 and 264-275 | Retry backoff behavior and narrower retry taxonomy that conflicts with provider-routing failover docs. |
| region-11 | Gateway README, feature/config sections, lines 2-14 | Public claim of routing strategies: fastest, cheapest, reliable, weighted distribution, model-weight distribution; self-hosted config sample. |
| region-12 | Embedded provider catalog raw view, line 0 | Observed static provider/model catalog and provider base endpoints; no price or tenant state observed. |

## §11 ROUND-2 CRITIC FINDINGS

| Finding | Disposition | R3 handling |
| --- | --- | --- |
| C-001 | CONFIRM-from-source | §2 S-1..S-5 and §2-bis Trace A document discovery, BYOK priority, managed fallback, cheapest selection, equal-cost balancing, and failover. |
| C-002 | CONFIRM-from-source | §2 S-12..S-13 and §4 separate broad provider-routing triggers from safer HUAKAI taxonomy. |
| C-003 | CONFIRM-from-source | §2 S-6..S-11 and §9 OQ-2/OQ-3 preserve lock/exclusion conflict questions. |
| C-004 | CONFIRM-from-source | §2 S-10 and §7 fail-closed unknown Model behavior. |
| C-005 | CONFIRM-from-source | §6 R-4/R-7 and §9 OQ-6 cover estimated vs actual cost uncertainty. |
| C-006 | OPEN-question-because-source-ambiguous | Public docs do not show stale registry/price/health recovery; §9 keeps it open and §7 requires durable audit. |
| C-007 | OPEN-question-because-source-ambiguous | Public docs do not show state sharing; §6 R-5 marks the HUAKAI risk as inference. |
| C-008 | CONFIRM-from-source | §2 S-6..S-11 documents the compact request grammar and §7 requires parser acceptance tests. |
| F-001 | CONFIRM-from-source | §1 and §2 describe cost routing as multi-input, not static sort only. |
| F-002 | CONFIRM-from-source | §5 adds explicit Personal/SaaS tenant/account/channel requirements. |
| F-003 | OPEN-question-because-source-ambiguous | Source shows ordered fallback but not idempotency/streaming side effects; §9 OQ-7 keeps this open. |
| F-004 | CONFIRM-from-source | §6 R-4 rejects equal public price as sufficient effective cost. |
| F-005 | CONFIRM-from-source | §7 sets admin/tenant policy above request hints. |
| D-001 | CONFIRM-from-source | Metadata records license drift posture; source details are redacted. |
| D-002 | CONFIRM-from-source | §10 separates README/config evidence from docs and does not cite config samples as stable full contract. |
| D-003 | CONFIRM-from-source | §2 S-6 and §2-bis Trace B call automatic failover conditional. |
| D-004 | CONFIRM-from-source | §4 and §6 R-2 require fail-closed credential handling for HUAKAI. |
| D-005 | CONFIRM-from-source | §4 and §6 R-3 restrict context-length fallback. |
| N-001 | CONFIRM-from-source | §7 requires typed Route policy instead of request-string source of truth. |
| N-002 | CONFIRM-from-source | §7 precedence makes admin deny stronger than user hints. |
| N-003 | CONFIRM-from-source | §7 fails closed on credential errors by default. |
| N-004 | CONFIRM-from-source | §7 ranks by effective cost. |
| N-005 | CONFIRM-from-source | §7 persists shared state in PostgreSQL. |
| N-006 | CONFIRM-from-source | §5 and §7 keep quota/billing enforcement independent of provider routing. |
| N-007 | CONFIRM-from-source | §7 separates request hints, tenant policy, health, and operator overrides. |
| S-001 | CONFIRM-from-source | §2 S-12..S-13 and §4 split observed error classes into HUAKAI-safe outcomes. |
| S-002 | CONFIRM-from-source | §7 fail-closed rules cover managed fallback boundaries. |
| S-003 | OPEN-question-because-source-ambiguous | §9 OQ-5 keeps hidden state unresolved. |
| S-004 | CONFIRM-from-source | §5 SaaS rules require pre-authorized candidates. |
| S-005 | OPEN-question-because-source-ambiguous | No source-observed thresholds are claimed. |
| S-006 | CONFIRM-from-source | §10 pins evidence to page regions and avoids treating drifted docs as one stable contract. |
| Synthesis recommendations | CONFIRM-from-source | §7 and §8 convert recommendations into HUAKAI-safe adaptation and ledger rows. |

Owner 中文总结：本轮拆解的是 Helicone 成本感知路由、请求级自定义链路、成本可见性和权重/策略配置对 HUAKAI F-ROUTE-001 与 F-CONFIG-001 的 clean-room 行为合同；真实观察来自 12 个公开文档/配置证据区域，主要覆盖默认 cheapest-first、BYOK 优先、managed fallback、provider lock、deployment lock、manual fallback、provider exclusion、unknown model BYOK-only、failover trigger、精确/估算成本、cost-based rate limit 和 README 级 weighted strategy 声明；合理推断集中在 HUAKAI 风险区，共 8 条，均标记为 inference；critic 的 C/F/D/N/S 类发现已逐项 CONFIRM 或 OPEN 处置，没有 REFUTE；当前 open question 9 个，主要是实现内部 parser、equal-cost balancing、共享健康状态、streaming partial retry 和权重向量细节，不能从可读 source 诚实断言。
