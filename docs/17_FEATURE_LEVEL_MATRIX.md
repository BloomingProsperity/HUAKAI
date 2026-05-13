This file is agent-facing and authoritative.

# Feature Level Matrix

## Purpose

This matrix turns a large product into buildable levels. It preserves all reference-derived features while making the first version small enough to complete.

## Level Definitions

| Level | Name | Meaning |
| --- | --- | --- |
| L0 | Not Started | Known feature, no local behavior yet. |
| L1 | MVP | Smallest useful version that closes the core workflow. |
| L2 | Production Usable | Safe, observable, configurable, and reliable enough for real use. |
| L3 | Reference Parity | Covers behavior and edge cases found in reference projects. |
| L4 | Better Than Reference | Improves safety, operations, UX, reliability, or extensibility beyond references. |

## Matrix

| Capability | L1 MVP | L2 Production Usable | L3 Reference Parity | L4 Better Than Reference |
| --- | --- | --- | --- | --- |
| API Key Intake | One local key can call the gateway. | Key lifecycle, disablement, owner tracking. | Key limits, search, status, audit, bulk operations. | Risk scoring, anomaly detection, guided remediation. |
| Gateway Core | Accept one OpenAI-compatible request path and forward it. | Validation, timeout, basic error normalization, request IDs. | Streaming parity, provider-specific compatibility, broad error mapping. | Adaptive compatibility layer and operator-visible protocol diagnostics. |
| Provider Account Hub | Manually configure one or more provider credentials AND group them into one Pooling Group (relay-station identity, see Provider Account Pool row). | Enable, disable, rotate, redact, inspect health state. | Cross-Pool assignment, expiration handling, balance-aware routing inside a Pool. | Automated rotation, cost-aware account selection, predictive health. |
| Provider Account Pool | One Pooling Group aggregates ≥1 Provider Accounts into one logical capacity; Routes resolve to a Pool, sticky session pins requests to one Account; per-User × per-Account concurrency caps; pool-aware Usage Records. | Per-Pool health and balance dashboard; cross-Account hot-spot detection; pool-aware billing reconciliation. | Cross-Pool fallback, balance-aware Pool selection, expiration / disablement propagation across pooled Accounts. | Automated Pool rebalancing, predictive Account exhaustion, cost-aware Pool selection. |
| Edition / Run Mode | Edition flag toggles SaaS-only features (payment, multi-tenant onboarding) off; Personal Edition still receives mandatory cost-ceiling alert. | Per-Edition feature flag matrix; deployment-time Edition lock with audit. | Live Edition switching with safe-mode fallback. | Self-tuning Edition feature exposure based on deployment signals. |
| Provider Catalog Breadth (per [DR-007](decisions/DR-007-product-positioning-and-breadth.md)) | OpenAI + Anthropic + Gemini base coverage with one Channel each. | At least 8 major providers (OpenAI / Anthropic / Gemini / Azure OpenAI / DeepSeek / Mistral / Together AI / OpenRouter or equivalent breadth) with verified per-provider acceptance tests. | Provider catalog **materially exceeds** Sub2API's catalog by Phase 9 close (target: 15+ unique upstream Providers; per-provider capability matrix published). | Self-service Provider plugin SDK so operators or community contribute new Providers without core changes; capability discovery automated. |
| Routing | Pick one available provider account. | Basic fallback and disabled-account exclusion. | Weighted routing, priority routing, model routing, group routing. | Cost, latency, health, and policy optimized routing. |
| Model Registry | Small manual model list. | Model aliases and provider availability. | Model mapping across providers and channel-level controls. | Compatibility scoring and automatic model capability discovery. |
| Protocol Conversion | Basic OpenAI-compatible forwarding. | Request and response normalization. | Multi-provider protocol adapters and compatibility edge cases. | Safer conversion diagnostics and test-generated compatibility reports. |
| Usage Logging | Record request status, model, key, provider, and estimate. | Query usage by user, key, model, provider, and time. | Token accounting, cost context, export, reconciliation support. | Drift detection, anomaly alerts, and cost attribution insights. |
| Quota Lite | Simple request or token limit. | Atomic quota checks and clear rejection path. | User, group, model, provider, and time-window quota. | Predictive quota alerts and adaptive throttling. |
| Billing | Record usage only, no money movement. | Pricing context and admin correction notes. | Balance, recharge, deduction, model price, multiplier, ledger. | Reconciliation, margin analysis, anomaly correction workflows. |
| Admin Lite | Inspect keys, accounts, routes, usage, and logs. | Search, filter, sort, pagination, and safe actions. | Full operations dashboard with audit, bulk actions, settings. | Guided incident workflows and cross-resource investigation timelines. |
| Audit Logs | Minimal admin action log for risky operations. | Structured audit events with actor, target, action, time. | Full audit trail for user, key, account, route, quota, billing. | Risk-aware audit review and compliance export. |
| Security | Redact secrets and require basic auth. | RBAC, dangerous action confirmation, secret scanning. | Abuse controls, permissioned operations, stronger policy controls. | Behavior-based abuse prevention and operator playbooks. |
| Reliability | Basic timeout and error capture. | Retry, backoff, health state, failover. | Circuit breaker, degradation state, provider/account isolation. | Self-tuning reliability policy and incident recommendations. |
| Observability | Request log and basic status. | Error trends, provider health, usage views. | Investigation path from request to user, key, route, account, usage, billing, audit. | Full incident timeline and proactive alerts. |
| Plugin System | Not required in MVP, recorded as roadmap. | Stable extension points for selected areas. | Plugin boundary for optional risky or advanced features. | Safe plugin marketplace and policy-based plugin isolation. |
| Feature Flags | Manual roadmap record. | Runtime or config-level gates for selected features. | Per-feature rollout, defaults, operator visibility. | Risk-aware staged rollout with metrics. |
| Clean-Room Evidence | Record feature source as behavior. | Link evidence to capability and test. | Full evidence coverage for reference parity. | Automated parity review checklist and evidence freshness review. |

## MVP Scope

The first implementation should target these L1 outcomes only:

- API Key Intake.
- Gateway Core.
- Provider Account Hub.
- Routing.
- Model Registry.
- Protocol Conversion.
- Usage Logging.
- Quota Lite if it stays simple.
- Admin Lite as UI or documented admin API.
- Security basics.
- Basic observability.

## Deferred Scope

The following are preserved but should not block the first MVP:

- Full billing ledger.
- Recharge and deduction.
- Weighted routing.
- Automated account pool optimization.
- Advanced provider health automation.
- Full plugin system.
- Full dashboard parity.
- Multi-tenant organization model.
- Advanced abuse detection.
- Compliance export.

## Mapping Rule

Every feature discovered from references must receive:

- A capability row.
- A level target.
- A disposition in `docs/03_FEATURE_PARITY_MATRIX.md`.
- An acceptance test direction in `docs/11_ACCEPTANCE_TEST_MATRIX.md`.

## Practical Rule

If a feature is too large for L1, do not remove it. Assign it to L2, L3, or L4 and record the reason.

## MISSING_DISPOSITION 修补 — Codex Feature Parity Audit (2026-05-09)

来源：[docs/research/2026-05-09-codex-feature-parity-audit.md](research/2026-05-09-codex-feature-parity-audit.md) §6 — 8 条 HIGH `MISSING_DISPOSITION` 各分配 L 级与目前完成度。完整 disposition 见 [docs/03_FEATURE_PARITY_MATRIX.md](03_FEATURE_PARITY_MATRIX.md) 同名 §。

| Feature ID | Capability 归属 | L 级 | 目前完成度 | Phase 引用 | 备注 |
| --- | --- | --- | --- | --- | --- |
| [F-AUTH-006](03_FEATURE_PARITY_MATRIX.md) | Provider Account Hub | L0 Not Started | 0% | Phase 6 | OAuth 引导：登录 bootstrap + 短窗/长窗 + 客户端身份伪装 plugin；commercial blocker |
| [F-COMPAT-001](03_FEATURE_PARITY_MATRIX.md) | Gateway Core | L3 Reference Parity | 0% | Phase 5+ Personal Edition | Warm-up interception 凭据 flag plugin（默认关）；Q1 裁决 2026-05-12 维持 Plugin opt-in |
| [F-COMM-001](03_FEATURE_PARITY_MATRIX.md) | Billing | L3 Reference Parity | 0% | Phase 6+ commercial | 邀请 / 推荐子系统；Q2 裁决 2026-05-12 升 Mandatory Roadmap |
| [F-OBS-003](03_FEATURE_PARITY_MATRIX.md) | Usage Logging / Billing | L2 Production Usable | 0% | Phase 4.5 (axis 5 扩展) | 4-state 失败流计费扩展 F-OBS-001 |
| [F-OBS-004](03_FEATURE_PARITY_MATRIX.md) | Observability | L2 Production Usable | 0% | Phase 4.5 (axis 5 扩展) | 14 段异步处理器链 + 每批 drain 边界 |
| [F-OBS-005](03_FEATURE_PARITY_MATRIX.md) | Reliability / Observability | L2 Production Usable | 0% | Phase 4.5 (axis 5 扩展) | DLQ + 15 min 超时降级 + 优先级 lane + 主备非对称双写 |
| [F-CRED-001](03_FEATURE_PARITY_MATRIX.md) | Provider Account Hub / Security | L4 Better Than Reference | 0% | Phase 9+ SaaS enterprise | TokenProvider + preRotation + OIDC→cloud STS（非 K8s 表驱动） |
| [F-PROTO-003](03_FEATURE_PARITY_MATRIX.md) | Protocol Conversion | L2 Production Usable | 0% | covered by P-4 native passthrough | OpenAI 服务侧压缩透传；Q3 裁决 2026-05-12 维持 Native passthrough |

