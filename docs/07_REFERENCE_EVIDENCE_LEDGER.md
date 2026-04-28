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
