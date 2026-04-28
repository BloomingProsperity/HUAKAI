This file is agent-facing and authoritative.

# Reference Evidence Ledger

## Purpose

The ledger records what was learned from reference projects without copying protected implementation.

## License Verification Ledger

Establishing the license tier of every primary reference is a prerequisite to any other evidence row. These rows are the foundation; behavior evidence stacks on top.

| Evidence ID | Reference | Source URL | SPDX | Verified Date | Verified By | Notes |
| --- | --- | --- | --- | --- | --- | --- |
| E-LIC-001 | Sub2API | github.com/Wei-Shaw/sub2api/blob/main/LICENSE | LGPL-3.0 | 2026-04-27 | Claude (PM) | Strong copyleft. |
| E-LIC-002 | New API | github.com/QuantumNous/new-api/blob/main/LICENSE | AGPL-3.0-or-later | 2026-04-27 | Claude (PM) | Network copyleft; service distribution triggers source disclosure. Forked from MIT one-api. |
| E-LIC-003 | All API Hub | github.com/qixing-jk/all-api-hub/blob/main/LICENSE | AGPL-3.0 (+ MIT upstream portions) | 2026-04-27 | Claude (PM) | Browser extension; client-side management UI for relay stations, not a gateway. |
| E-LIC-004 | one-api | github.com/songquanpeng/one-api/blob/main/LICENSE | MIT | 2026-04-27 | Claude (PM) | Anchor reference. Safe to read freely; New API is a derivative fork. |
| E-LIC-005 | LiteLLM | github.com/BerriAI/litellm/blob/main/LICENSE | MIT | 2026-04-28 | Claude (PM) | Safe anchor. Note: `enterprise/` subdirectory has separate terms — do not read enterprise/* without separate license review. |
| E-LIC-006 | Portkey AI Gateway | github.com/Portkey-AI/gateway/blob/main/LICENSE | MIT | 2026-04-28 | Claude (PM) | Safe anchor. Copyright Portkey, Inc 2024. |
| E-LIC-007 | Helicone AI Gateway | github.com/Helicone/ai-gateway/blob/main/LICENSE | GPL-3.0-or-later | 2026-04-28 | Claude (specifier) | NOT a safe anchor despite some marketing claiming Apache-2.0. Network use does not trigger GPL distribution requirements (unlike AGPL), but binary distribution does. Read for behavior only. |
| E-LIC-008 | Envoy AI Gateway | github.com/envoyproxy/ai-gateway/blob/main/LICENSE | Apache-2.0 | 2026-04-28 | Claude (specifier) | Safe anchor (4th confirmed: one-api MIT, LiteLLM MIT, Portkey MIT, Envoy AI Gateway Apache-2.0). Built on Envoy Proxy, CNCF lineage. |

## Behavior Evidence — one-api (MIT, E-LIC-004)

First batch from one-api public README (Phase 1 kickoff, 2026-04-28). Specifier lane: Claude. License-tier-safe: source is MIT, full read permitted.

| Evidence ID | Reference | Source Type | Observed Behavior Or Scenario | Feature Candidate | Risk Notes | Clean-Room Notes | Date | Agent |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| E-OAI-001 | one-api (E-LIC-004) | Public README | Single OpenAI-compatible endpoint routes requests across multiple upstream LLM providers; client picks provider implicitly via model name or explicitly via channel hint. | Gateway core: provider-agnostic OpenAI-compatible request forwarding. | Channel-hint header is a leakage surface if exposed without auth scope. | Behavior only; no algorithm copied. | 2026-04-28 | Claude |
| E-OAI-002 | one-api (E-LIC-004) | Public README | Streaming and non-streaming responses are both supported; usage accounting differs between the two modes. | Gateway streaming + per-mode usage accounting. | Streaming partial-token usage must be tracked separately from final settlement (see BUG-GW-002). | Behavior only. | 2026-04-28 | Claude |
| E-OAI-003 | one-api (E-LIC-004) | Public README | Users authenticate via email/password, GitHub OAuth, or WeChat OAuth. | Pluggable auth provider abstraction (email + OAuth). | Hardcoding any one provider creates plugin debt; WeChat needs an auxiliary deployment. | Behavior only. | 2026-04-28 | Claude |
| E-OAI-004 | one-api (E-LIC-004) | Public README | Sessions persist across server restart; multi-machine deployments require shared session secret. | Session persistence + horizontal-scaling readiness. | Default-generated secrets are an antipattern; require operator-supplied secret on first run. | Behavior only. | 2026-04-28 | Claude |
| E-OAI-005 | one-api (E-LIC-004) | Public README | Operators issue API keys to users with configurable expiration and per-key quota; per-key quota is independent of account balance. | API key lifecycle: expire, disable, per-key quota. | Two quota sources (key vs account) cause "balance OK but key out" confusion (BUG-BILL-001 family). | Behavior only. | 2026-04-28 | Claude |
| E-OAI-006 | one-api (E-LIC-004) | Public README | Per-request cost = group multiplier × model multiplier × (prompt tokens + completion tokens × completion multiplier); completion multiplier varies per model. | Pricing engine with group / model / completion multipliers. | Multipliers must be versioned to allow safe historical reprice (BUG-BILL-001). | Behavior only; do not copy any specific multiplier value. | 2026-04-28 | Claude |
| E-OAI-007 | one-api (E-LIC-004) | Public README | Operators issue redemption vouchers; vouchers credit user balance in bulk. | Voucher / bulk top-up workflow. | Voucher reuse, expiry, and audit trail are mandatory; otherwise abuse vector. | Behavior only. | 2026-04-28 | Claude |
| E-OAI-008 | one-api (E-LIC-004) | Public README | Channels expose a configurable model allow-list; channels can be created in bulk. | Channel-as-resource with per-channel model exposure. | Bulk channel creation must produce one audit event per channel + one bulk-summary event. | Behavior only. | 2026-04-28 | Claude |
| E-OAI-009 | one-api (E-LIC-004) | Public README | Channel availability is checked periodically; balance and reachability are tracked; channels can be auto-disabled below a configured success threshold. | Channel health probes + success-rate-driven status changes. | Cloudflare blocking returns HTML where JSON expected; parser must detect and not silently mark "down". | Behavior only. | 2026-04-28 | Claude |
| E-OAI-010 | one-api (E-LIC-004) | Public README | User Groups and Channel Groups produce differential pricing per request. | Group-scoped pricing and routing eligibility. | Group cross-product must be auditable; otherwise pricing surprises. | Behavior only. | 2026-04-28 | Claude |
| E-OAI-011 | one-api (E-LIC-004) | Public README | Operators customize system branding (homepage, about page, top-bar) via HTML/Markdown or iframe embed. | Admin-customizable presentation layer. | Iframe embed introduces XSS / clickjacking surface; require sandbox attributes by default. | Behavior only. | 2026-04-28 | Claude |
| E-OAI-012 | one-api (E-LIC-004) | Public README | Operators publish announcements, configure recharge URLs, and set initial balance for newly registered users. | Operator-driven onboarding configuration. | Initial-balance abuse via repeated registration; tie to identity verification or per-identity cap. | Behavior only. | 2026-04-28 | Claude |
| E-OAI-013 | one-api (E-LIC-004) | Public README | Per-request quota detail and per-channel success-rate metrics drive an operator dashboard; auto-disable threshold is configurable. | Observability dashboard + operator-tuned auto-disable. | Silent auto-disable hides upstream incidents; require alert + operator-confirm-resume. | Behavior only. | 2026-04-28 | Claude |
| E-OAI-014 | one-api (E-LIC-004) | Public README | Cloudflare Turnstile gates registration/login; per-IP rate limit (e.g. 180 API / 60 web per 3 minutes by default) protects abuse paths. | Anti-abuse: CAPTCHA plugin + per-IP rate limit. | Default thresholds may be too generous for SaaS Edition; expose as operator-tunable. | Behavior only; specific numbers not adopted. | 2026-04-28 | Claude |
| E-OAI-015 | one-api (E-LIC-004) | Public README | Privileged management API is gated by a special "system access token"; default first-run admin credentials exist. | Privileged management API + privileged credential bootstrap. | Default-credentials antipattern (`root/123456`) is unacceptable; force-change on first login. | Behavior only. | 2026-04-28 | Claude |

## Behavior Evidence — LiteLLM (MIT, E-LIC-005)

Second batch from LiteLLM public README (Phase 1, 2026-04-28). MIT-safe; `enterprise/` subtree excluded by license note. Specifier lane: Claude.

| Evidence ID | Reference | Source Type | Observed Behavior Or Scenario | Feature Candidate | Risk Notes | Clean-Room Notes | Date | Agent |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| E-LM-001 | LiteLLM (E-LIC-005) | Public README | Gateway publishes a concrete latency-target SLO (e.g. 8ms P95 at 1k RPS) as a public commitment. | Performance budget as a published, testable SLO. | Marketing benchmarks rarely match production payload mix; SLO must be measured locally. | Behavior only; no benchmark code copied. | 2026-04-28 | Claude |
| E-LM-002 | LiteLLM (E-LIC-005) | Public README | Router falls over to alternate provider deployments (Azure / OpenAI / etc.) when a primary fails, retrying transparently. | Cross-deployment runtime fallback (more aggressive than per-channel auto-disable). | Fallback chains can silently drain low-quota accounts; require per-account spend cap and alert. | Behavior only. | 2026-04-28 | Claude |
| E-LM-003 | LiteLLM (E-LIC-005) | Public README | Virtual API keys carry per-tenant configuration: logging, guardrails, caching policy. | Per-tenant config attached to credential, not just to user. | Misattributed config can leak between tenants if scoped wrong (DR-001 isolation tests). | Behavior only. | 2026-04-28 | Claude |
| E-LM-004 | LiteLLM (E-LIC-005) | Public README | Container images are signed with Cosign and pinned to commit hashes. | Container supply-chain integrity (signed images, SBOM). | Verification optional in many deployments; must be required for SaaS Edition. | Behavior only. | 2026-04-28 | Claude |
| E-LM-005 | LiteLLM (E-LIC-005) | Public README | Enterprise tier adds SSO and dedicated SLA support. | Enterprise SSO / advanced auth as Plugin. | Single-tenant SSO behaves differently from SaaS multi-tenant SSO; design abstraction now. | Behavior only. | 2026-04-28 | Claude |
| E-LM-006 | LiteLLM (E-LIC-005) | Public README | Bridge to MCP (Model Context Protocol) tools and A2A (Agent-to-Agent) protocol. | External agent / tool protocol bridging. | Tool-execution sandboxing critical; malicious tool = execution surface. Phase 9+. | Behavior only. | 2026-04-28 | Claude |

## Behavior Evidence — Portkey AI Gateway (MIT, E-LIC-006)

Third batch from Portkey public README (Phase 1, 2026-04-28). MIT-safe. Specifier lane: Claude.

| Evidence ID | Reference | Source Type | Observed Behavior Or Scenario | Feature Candidate | Risk Notes | Clean-Room Notes | Date | Agent |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| E-PK-001 | Portkey (E-LIC-006) | Public README | Failed requests retry up to N times with exponential backoff between attempts. | Retry policy with explicit backoff schedule. | Retries can multiply cost on systematic failures; cap by spend not just by attempts (BUG-GW-001). | Behavior only. | 2026-04-28 | Claude |
| E-PK-002 | Portkey (E-LIC-006) | Public README | Fallback triggers on configurable error conditions, not just on connection failure. | Error-condition-driven fallback rules. | Wider trigger surface = more accidental fallback; require operator-visible reason on each fallback. | Behavior only. | 2026-04-28 | Claude |
| E-PK-003 | Portkey (E-LIC-006) | Public README | Output guardrails validate LLM responses against pre-built rule packs (40+ in reference). | Pluggable output-content guardrail engine. | False positives reject valid output; provide bypass path with audit. | Behavior only; no rule list copied. | 2026-04-28 | Claude |
| E-PK-004 | Portkey (E-LIC-006) | Public README | Multi-modal requests: vision input, audio input/output, image generation, all under one OpenAI-compatible signature. | Multi-modal request normalization. | Provider compatibility varies wildly; need explicit capability matrix per model. Phase 9+. | Behavior only. | 2026-04-28 | Claude |
| E-PK-005 | Portkey (E-LIC-006) | Public README | Response caching with both simple (key-based) and semantic (embedding-based) modes. | Response cache: simple + semantic. | Stale cache for time-sensitive queries; require TTL + invalidation hooks. | Behavior only. | 2026-04-28 | Claude |
| E-PK-006 | Portkey (E-LIC-006) | Public README | OpenAI Realtime API surface (WebSocket-based) is supported. | Real-time WebSocket protocol surface. | Connection drops mid-stream require resumption protocol; otherwise lost partial usage. Phase 9+. | Behavior only. | 2026-04-28 | Claude |
| E-PK-007 | Portkey (E-LIC-006) | Public README | Per-request timeout thresholds are operator-tunable. | Granular per-request timeout policy. | Aggressive timeout cuts legitimate long completions; tune per model. | Behavior only. | 2026-04-28 | Claude |
| E-PK-008 | Portkey (E-LIC-006) | Public README | RBAC: roles defined per user, workspace, and API key; revocation is instant. | RBAC with revocation propagation guarantee. | Cascading role changes can surprise operators; surface "who lost access because of this" diff. | Behavior only. | 2026-04-28 | Claude |

## Behavior Evidence — New API (AGPL-3.0, E-LIC-002)

Fourth batch from New API public README (Phase 1, 2026-04-28). **AGPL exposure starts here**: this session has read non-MIT documentation and is now in specifier-only contamination state per [05_CLEAN_ROOM_POLICY.md](05_CLEAN_ROOM_POLICY.md). All entries below are behavior descriptions only — no schema, code, function names, or verbatim quotes longer than common technical phrases.

| Evidence ID | Reference | Source Type | Observed Behavior Or Scenario | Feature Candidate | Risk Notes | Clean-Room Notes | Date | Agent |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| E-NAI-001 | New API (E-LIC-002) | Public README | Cache-aware billing distinguishes cached-prompt tokens from fresh tokens and prices them differently across multiple providers (OpenAI, Azure, DeepSeek, Anthropic, Qwen). | Differential pricing for prompt-cache hits vs misses. | Users surprised when "same prompt" charges differently; UI must surface cache hit/miss reason. | Behavior only; no pricing values copied. | 2026-04-28 | Claude (specifier) |
| E-NAI-002 | New API (E-LIC-002) | Public README | Admin and end-user UI ships natively in 5 languages (Simplified Chinese, Traditional Chinese, English, French, Japanese), not English-primary with translations bolted on. | Native multi-language UI / i18n as a first-class concern. | Translation drift across many locales; technical terms need locked glossary per language. | Behavior only. | 2026-04-28 | Claude (specifier) |
| E-NAI-003 | New API (E-LIC-002) | Public README | Cross-format protocol translation: OpenAI request ↔ Claude Messages, and Gemini → OpenAI-Compatible (text-only, function-calling not supported). | Protocol translation surface beyond single-format compatibility. | Silent semantic loss during conversion (e.g. Gemini → OpenAI dropping function-calling) must be surfaced as explicit operator-visible capability matrix. | Behavior only. | 2026-04-28 | Claude (specifier) |
| E-NAI-004 | New API (E-LIC-002) | Public README | Reasoning-effort parameter pass-through: clients select effort level (high/medium/low) for thinking-capable models, with per-request token budget. | Reasoning-effort / thinking-mode parameter normalization. | Silent truncation of "thinking" phase causes incomplete reasoning chains; must be visible in Usage Record. | Behavior only. | 2026-04-28 | Claude (specifier) |
| E-NAI-005 | New API (E-LIC-002) | Public README | Rerank-model interface (e.g. Cohere, Jina) is a distinct API surface from chat / embedding / image, not multiplexed onto a chat endpoint. | Dedicated rerank-model API surface. | Rerank response shape diverges from OpenAI; downstream tools may misparse if served via chat endpoint. | Behavior only. | 2026-04-28 | Claude (specifier) |
| E-NAI-006 | New API (E-LIC-002) | Public README | Per-user-per-model rate limiting (not just per-IP), enabling tiered access where high-tier users get higher caps for specific models. | Per-User × Per-Model rate limit (more granular than F-SEC-001 per-IP). | Misconfigured tier limits can starve low-tier users while high-tier monopolizes; need operator-visible tier diff. | Behavior only. | 2026-04-28 | Claude (specifier) |
| E-NAI-007 | New API (E-LIC-002) | Public README | OAuth identity sources include Discord, Telegram, and LinuxDo in addition to GitHub/WeChat/email — community-platform identity. | Community-platform OAuth identity sources as plugins under F-AUTH-001 abstraction. | Account recovery depends on third-party platform availability; require fallback recovery path. | Behavior only. | 2026-04-28 | Claude (specifier) |
| E-NAI-008 | New API (E-LIC-002) | Public README | External tooling (e.g. neko-api-key-tool) queries per-key remaining quota in real time. | Operator-API surface for third-party quota-introspection tools. | External tool dependency creates UX gap if abandoned; first-party UI must cover the same need. | Behavior only. | 2026-04-28 | Claude (specifier) |

## Behavior Evidence — Sub2API (LGPL-3.0, E-LIC-001)

Fifth batch from Sub2API public README (Phase 1, 2026-04-28). LGPL exposure; specifier-lane only. Sub2API positions itself as a "ride-sharing" relay platform (拼车) that pools subscription quotas — its differentiator vs generic gateways is the relay-station business model, not the core gateway.

| Evidence ID | Reference | Source Type | Observed Behavior Or Scenario | Feature Candidate | Risk Notes | Clean-Room Notes | Date | Agent |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| E-S2A-001 | Sub2API (E-LIC-001) | Public README | Multiple upstream subscription accounts are pooled into a logical capacity bucket; users access the bucket via platform-issued API Keys without seeing individual upstream accounts. | Multi-account quota pooling (the relay-station / "ride-sharing" pattern). | Upstream Terms of Service may forbid quota sharing; operator carries the legal risk. | Behavior only. | 2026-04-28 | Claude (specifier) |
| E-S2A-002 | Sub2API (E-LIC-001) | Public README | Same-session requests pin to the same upstream account via a session-id header, so multi-turn conversations preserve provider context. | Sticky session routing (session-id-aware). | Sticky routing creates uneven account load; needs operator-visible per-account distribution view. | Behavior only. | 2026-04-28 | Claude (specifier) |
| E-S2A-003 | Sub2API (E-LIC-001) | Public README | Built-in payment surfaces support Alipay, WeChat Pay, Stripe, and EasyPay aggregator — no separate payment microservice. | Built-in payment-plugin layer for self-service top-ups. | Operator inherits PCI compliance and fraud risk for every enabled provider. | Behavior only. | 2026-04-28 | Claude (specifier) |
| E-S2A-004 | Sub2API (E-LIC-001) | Public README | Two independent concurrency caps run in parallel: one per User and one per upstream Provider Account. | Per-User × per-Account concurrency limits. | Misconfigured stacking can over-block legitimate spikes or under-protect upstream accounts. | Behavior only. | 2026-04-28 | Claude (specifier) |
| E-S2A-005 | Sub2API (E-LIC-001) | Public README | An edition-mode configuration flag hides SaaS-only surfaces (payment, multi-user billing) so the same code can be deployed for internal-team use. | Edition-flag plumbing that toggles SaaS-only features off (DR-002 Personal vs SaaS in concrete form). | Edition-stripped deployments may run without payment guardrails and over-consume; cost-ceiling alert is mandatory regardless of Edition. | Behavior only. Specific upstream config name and value redacted (CL-001 sub-clause: do not carry upstream config-constant names into specs/evidence). | 2026-04-28 | Claude (specifier) |
| E-S2A-006 | Sub2API (E-LIC-001) | Public README | Optional billing-failure circuit breaker fails closed: when the billing system is unreachable, requests are denied rather than served untracked. | Fail-closed circuit breaker for billing-system unavailability. | Closed-circuit denies all users during outage; operator must have an explicit override. | Behavior only. | 2026-04-28 | Claude (specifier) |
| E-S2A-007 | Sub2API (E-LIC-001) | Public README | Operator can check, download, and apply software updates from inside the admin UI with rollback support — no terminal. | In-dashboard self-upgrade workflow. | Update path is itself an attack surface; must require operator role + audit + signed artifact (ties to F-SEC-003). | Behavior only. | 2026-04-28 | Claude (specifier) |
| E-S2A-008 | Sub2API (E-LIC-001) | Public README | Operator-configurable allow/block list governs which upstream response headers are forwarded; security-first default blocks sensitive headers. | Configurable upstream response-header filter with secure defaults. | Permissive allowlists can leak upstream auth/internal-service info; require periodic operator review. | Behavior only. | 2026-04-28 | Claude (specifier) |

## Behavior Evidence — All API Hub (AGPL-3.0, E-LIC-003)

Sixth batch from All API Hub public README (Phase 1, 2026-04-28). All API Hub is a browser extension for managing many relay-station accounts — its evidence value is operator-pain-point and cross-relay UX, NOT gateway internals.

| Evidence ID | Reference | Source Type | Observed Behavior Or Scenario | Feature Candidate | Risk Notes | Clean-Room Notes | Date | Agent |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| E-AAH-001 | All API Hub (E-LIC-003) | Public README | Operator (and end users) accumulate accounts across many relay stations and need a single dashboard to see balance / usage / health across all of them. | Cross-source operator dashboard (extends F-OPS-001). | Aggregating credentials in one extension creates a high-value target. | Behavior only; this is product-pain-point evidence, not implementation. | 2026-04-28 | Claude (specifier) |
| E-AAH-002 | All API Hub (E-LIC-003) | Public README | Many relay stations grant daily-bonus quota requiring a daily check-in click; operators want this automated and audited. | Automated daily check-in plugin (per-source). | Looks like abuse to upstream; rate-limit or human-cadence required. | Behavior only. | 2026-04-28 | Claude (specifier) |
| E-AAH-003 | All API Hub (E-LIC-003) | Public README | Same model is sold at different effective prices across relay stations; users want side-by-side comparison including token multipliers. | Cross-source price comparison view. | Comparison may be misleading if hidden fees / rate-limits differ. | Behavior only. | 2026-04-28 | Claude (specifier) |
| E-AAH-004 | All API Hub (E-LIC-003) | Public README | Users export API keys directly from the dashboard into specific downstream tools (CherryStudio, Claude Code Router, etc.) without manual paste. | One-click downstream-tool integration / key export. | "Quick export" bypasses user pause-points to review which credential is shared. | Behavior only. | 2026-04-28 | Claude (specifier) |
| E-AAH-005 | All API Hub (E-LIC-003) | Public README | Operator probes whether a key works against specific models / token cap before promoting it to production. | API Key health-probe surface (read-only). | Burst probing can look like attack; rate-limit and operator-only. | Behavior only. | 2026-04-28 | Claude (specifier) |
| E-AAH-006 | All API Hub (E-LIC-003) | Public README | Cross-device configuration sync via WebDAV with optional encryption and selective-sync rules. | Cross-device operator-config sync (Plugin). | WebDAV credential becomes a long-lived token; revocation pathway needed. | Behavior only. | 2026-04-28 | Claude (specifier) |

## Behavior Evidence — Helicone (GPL-3.0, E-LIC-007)

Seventh batch from Helicone AI Gateway public README (Phase 1, 2026-04-28). Helicone emphasizes observability + low-overhead runtime; this batch focuses on what it does that other references do not.

| Evidence ID | Reference | Source Type | Observed Behavior Or Scenario | Feature Candidate | Risk Notes | Clean-Room Notes | Date | Agent |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| E-HLC-001 | Helicone (E-LIC-007) | Public README | Routing decision uses real-time observed latency, cost, and reliability signals per provider, not just operator-set weights. | Performance-aware routing (extends F-GW-004 retry/fallback with observable signals). | Cost-optimized routing can drift to undertested providers; require operator-visible "why this provider" reason. | Behavior only. | 2026-04-28 | Claude (specifier) |
| E-HLC-002 | Helicone (E-LIC-007) | Public README | Response cache plugs into Redis or S3 backends, not just in-process. | Pluggable cache backend (extends F-CACHE-001). | Backend choice affects multi-tenant isolation; key scheme must include tenant_id. | Behavior only. | 2026-04-28 | Claude (specifier) |
| E-HLC-003 | Helicone (E-LIC-007) | Public README | Logs / metrics / traces export via OpenTelemetry, not a proprietary format. | OpenTelemetry-compliant observability export. | OTel exporter selection affects ops-tool flexibility; should be operator-configurable. | Behavior only. | 2026-04-28 | Claude (specifier) |
| E-HLC-004 | Helicone (E-LIC-007) | Public README | Rate / cost limits expressed at three scopes: global, team, per-user — both as request count and as dollar amount. | Multi-scope rate + cost limits (extends F-SEC-004 to include cost-amount caps). | Cost limits depend on accurate pricing context (F-BILL-001); silent failures under price drift. | Behavior only. | 2026-04-28 | Claude (specifier) |
| E-HLC-005 | Helicone (E-LIC-007) | Public README | Resource footprint and cold-start time are stated as design constraints with concrete numbers (memory, binary size, latency, cold start). | Explicit performance budget (extends F-GW-003 SLO concept). | Comparing against marketing numbers is misleading; budget must be measured under our own payload mix. | Behavior only; numbers not adopted. | 2026-04-28 | Claude (specifier) |
| E-HLC-006 | Helicone (E-LIC-007) | Public README | Per-endpoint routing strategies defined declaratively in YAML or via UI wizard, allowing one deployment to serve multiple distinct routing policies. | Declarative per-endpoint routing policy. | YAML drift across deployments; require config-as-code with audit. | Behavior only. | 2026-04-28 | Claude (specifier) |
| E-HLC-007 | Helicone (E-LIC-007) | Public README | Multiple deployment surfaces from the same artifact: cloud-hosted, Docker, npm package — operator picks at install time. | Multi-deployment-target packaging. | Feature gaps between targets create "feature exists in cloud but not in self-host" drift. | Behavior only. | 2026-04-28 | Claude (specifier) |

## Behavior Evidence — Envoy AI Gateway (Apache-2.0, E-LIC-008)

Eighth batch from Envoy AI Gateway public README (Phase 1, 2026-04-28). Apache-2.0 safe anchor; novel value is the enterprise / Kubernetes-native two-tier deployment pattern.

| Evidence ID | Reference | Source Type | Observed Behavior Or Scenario | Feature Candidate | Risk Notes | Clean-Room Notes | Date | Agent |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| E-EAG-001 | Envoy AI Gateway (E-LIC-008) | Public README | Two-tier deployment: an outer "Tier One" gateway handles auth + global rate limiting + identity, while an inner "Tier Two" gateway handles self-hosted model cluster access and inference endpoint selection. | Two-tier gateway architecture for separation of concerns. | Two tiers double operational complexity if not justified by scale; Personal Edition should not require two tiers. | Behavior only. | 2026-04-28 | Claude (specifier) |
| E-EAG-002 | Envoy AI Gateway (E-LIC-008) | Public README | Endpoint-picker policy chooses the optimal inference endpoint within a model cluster (e.g. picks between replica instances of the same self-hosted model). | Inference-endpoint selection policy under a Channel/Pool. | Misconfigured picker silently routes to suboptimal endpoint; need operator-visible "why this endpoint" reason. | Behavior only. | 2026-04-28 | Claude (specifier) |
| E-EAG-003 | Envoy AI Gateway (E-LIC-008) | Public README | Kubernetes-native deployment is the primary deployment surface; CNCF-aligned operational patterns (CRDs, operators) are first-class. | Kubernetes-native deployment posture. | K8s requirement excludes simpler self-hosters; Personal Edition must remain non-K8s-runnable. | Behavior only. | 2026-04-28 | Claude (specifier) |

## Evidence Template

| Evidence ID | Reference | Source Type | Observed Behavior Or Scenario | Feature Candidate | Risk Notes | Clean-Room Notes | Date | Agent |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| E-TBD | TBD | Docs/Issue/UI behavior/Release note | TBD | TBD | TBD | No code copied. | TBD | TBD |

## Source Types

- Public documentation.
- Public issue or discussion.
- Release note.
- Public demo behavior.
- Public UI behavior.
- Public API behavior.
- Security advisory or bug report.

## Rules

- Record behavior, not implementation.
- Do not paste protected source.
- Do not copy schema, comments, UI source, or file structure.
- Link or cite public evidence when possible.
- Each parity matrix row should point to at least one evidence ID.
- Every behavior evidence row must reference the license tier of its source via the corresponding E-LIC-XXX row.
- New references added to [06_REFERENCE_PROJECTS.md](06_REFERENCE_PROJECTS.md) must first receive a license verification row here before any behavior evidence is captured.
