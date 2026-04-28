This file is agent-facing and authoritative.

# Feature Parity Matrix

## Purpose

This matrix tracks reference-derived features and ensures full feature parity or better. It is a living control document, not a wishlist.

## Valid Dispositions

| Disposition | Meaning |
| --- | --- |
| `Implemented` | The feature exists with equivalent behavior. |
| `Implemented Better` | The feature exists with materially stronger behavior, safety, UX, or operability. |
| `Merged Equivalent` | Multiple reference features are covered by one broader capability. |
| `Safe Equivalent` | The same user outcome is delivered with a safer clean-room design. |
| `Plugin` | The feature is supported through a plugin boundary. |
| `Feature Flag` | The feature exists but is gated for rollout, risk, or deployment policy. |
| `Mandatory Roadmap` | The feature is not implemented yet but is required before parity closure. |

Invalid dispositions: `Dropped`, `Ignored`, `Not Needed`, `Too Risky`, `License Risk`, `Out of Scope`.

## Mined Features — Phase 1 First Batch (one-api)

First mapping pass, 2026-04-28. Each row references one or more `E-OAI-*` evidence rows in [07_REFERENCE_EVIDENCE_LEDGER.md](07_REFERENCE_EVIDENCE_LEDGER.md). Capabilities map to [02_CAPABILITY_CONTRACT.md](02_CAPABILITY_CONTRACT.md). Test IDs are placeholders pending [11_ACCEPTANCE_TEST_MATRIX.md](11_ACCEPTANCE_TEST_MATRIX.md) population.

| Feature ID | Reference | Evidence ID | User Outcome | Risk | Disposition | Local Capability | Test ID | Owner | Status |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| F-GW-001 | one-api | E-OAI-001 | Client sends one OpenAI-compatible request and reaches the right upstream provider. | Mis-routing leaks request to wrong account / provider. | Implemented | Gateway core: model-driven Route → Channel → Provider Account selection. | AT-GW-001 | Claude | Open (L1 MVP) |
| F-GW-002 | one-api | E-OAI-002 | Streaming and non-streaming requests both work with consistent usage accounting. | Streaming partial-usage drift; double-billing on retry (BUG-GW-001/002). | Implemented Better | Stream and non-stream paths share one accounting engine that records partial-token state separately from final settlement. | AT-GW-002 | Claude | Open (L1 MVP) |
| F-AUTH-001 | one-api | E-OAI-003 | A user signs in via email or any of N OAuth providers. | Hardcoding providers blocks Phase 9 SSO additions. | Plugin | Pluggable auth provider abstraction; email + at least one OAuth provider in MVP; remaining providers added as plugins. | AT-AUTH-001 | Claude | Open (L1 MVP for email + 1 OAuth) |
| F-AUTH-002 | one-api | E-OAI-004 | Session survives server restart and works across multiple instances. | Default-generated secrets, secret reuse. | Implemented Better | Operator-supplied session secret required on first run (no default); horizontal-scale-ready session store. | AT-AUTH-002 | Claude | Open (L1 MVP) |
| F-KEY-001 | one-api | E-OAI-005 | Operator issues API keys to a user with expiration and per-key quota. | "Balance OK but key out" confusion. | Implemented Better | API Key lifecycle (issue / disable / rotate / expire) with per-key quota that is reconciled against account balance in a single Usage Record query path. | AT-KEY-001 | Claude | Open (L1 MVP) |
| F-BILL-001 | one-api | E-OAI-006 | Per-request cost reflects user group, model, and completion vs prompt token weighting. | Pricing drift; non-replayable historical cost. | Implemented Better | Versioned pricing context attached to every Usage Record; reprice operation operates on stored context, not live config. | AT-BILL-001 | Codex | Open (L2 deferred) |
| F-BILL-002 | one-api | E-OAI-007 | Operator issues redemption vouchers; users redeem vouchers to top up balance. | Voucher reuse, voucher expiry, audit gaps. | Mandatory Roadmap | Voucher entity with one-time redemption, expiry, audit event, and per-issue cap. Phase 6+. | AT-BILL-002 | Codex | Open (L3 Phase 6+) |
| F-CH-001 | one-api | E-OAI-008 | Operator creates and configures channels; channels expose a model allow-list; bulk creation supported. | Bulk action without per-channel audit. | Implemented | Channel CRUD + per-channel model allow-list + bulk-create with one audit event per channel + one bulk-summary audit event. | AT-CH-001 | Claude | Open (L1 MVP) |
| F-CH-002 | one-api | E-OAI-009 | Channel health is probed periodically; channels can be auto-disabled below a success threshold. | Silent auto-disable hides upstream incidents; CF-blocking misclassified. | Implemented Better | Health probe + parser that distinguishes "upstream returned non-JSON" from "upstream down"; auto-disable raises an alert and requires operator-confirm-resume. | AT-CH-002 | Codex | Open (L2 deferred) |
| F-GROUP-001 | one-api | E-OAI-010 | User Group × Channel Group resolves to differential pricing and routing eligibility. | Pricing surprises from group cross-product. | Implemented | User Group + Channel Group as first-class entities with auditable resolution rules. | AT-GROUP-001 | Claude | Open (L2 deferred) |
| F-UI-001 | one-api | E-OAI-011, E-OAI-012 | Operator customizes branding (homepage, about, top-bar) and onboarding (announcements, recharge links, initial balance). | Iframe embed XSS; initial-balance abuse via mass registration. | Mandatory Roadmap | Branding fields with sandboxed iframe; initial-balance tied to identity-verification gate. Phase 7+. | AT-UI-001 | Gemini | Open (L3 Phase 7+) |
| F-OBS-001 | one-api | E-OAI-013 | Operator dashboard shows per-request quota detail and per-channel success rate; thresholds tunable. | Silent threshold-based disabling. | Implemented Better | Dashboard + alert-on-disable + operator-confirm-resume. | AT-OBS-001 | Gemini | Open (L2 deferred) |
| F-SEC-001 | one-api | E-OAI-014 | CAPTCHA gates registration/login; per-IP rate limit gates abuse paths. | Default thresholds too loose for SaaS. | Plugin + Implemented | CAPTCHA as plugin (Turnstile + alternatives); rate limit always-on with operator-tunable thresholds; SaaS Edition tightens defaults. | AT-SEC-001 | Codex | Open (L1 MVP rate limit; L2 CAPTCHA plugin) |
| F-SEC-002 | one-api | E-OAI-015 | Privileged management API exists; first-run bootstrap credential is rotatable. | Default credentials (`root/123456`) antipattern. | Implemented Better | RBAC tier for management API; first-run bootstrap forces credential change before any other operation; never ships hardcoded password. | AT-SEC-002 | Codex | Open (L1 MVP) |
| F-GW-003 | LiteLLM | E-LM-001 | Operator can assert and verify a per-request latency target in production. | Marketing-vs-production benchmark drift. | Implemented Better | Local SLO doc + automated p95/p99 measurement under representative payload mix; SLO violation triggers alert, not silent. | AT-GW-003 | Codex | Open (L2) |
| F-GW-004 | LiteLLM, Portkey | E-LM-002, E-PK-001, E-PK-002 | Failed request transparently retries / falls over with exponential backoff and operator-visible reason on each fallback. | Retry / fallback amplifies cost; silent fallback hides upstream incidents. | Implemented | Per-request retry policy with backoff schedule; per-account spend cap; every fallback emits an operator-visible reason. | AT-GW-004 | Codex | Open (L1 MVP retry; L2 cross-deployment fallback) |
| F-TIMEOUT-001 | Portkey | E-PK-007 | Per-request timeout is operator-tunable per model. | Aggressive timeout cuts legitimate long completions. | Implemented | Per-request timeout policy with per-model defaults; operator-tunable; emitted on the Usage Record on cut. | AT-TIMEOUT-001 | Codex | Open (L1 MVP) |
| F-RBAC-001 | Portkey | E-PK-008 | RBAC roles per user / workspace / API key; revocation propagates instantly with audited "who lost access" diff. | Cascading permission changes surprise operators. | Implemented Better | RBAC engine with explicit revocation propagation guarantee; UI shows the diff after each role change. | AT-RBAC-001 | Codex | Open (L2) |
| F-CACHE-001 | Portkey | E-PK-005 | Responses can be cached, both by simple key and by semantic similarity. | Stale cache for time-sensitive queries. | Mandatory Roadmap | Simple cache at L2; semantic cache at L3+. Always with TTL and operator-invokable invalidation. | AT-CACHE-001 | Codex | Open (L2 simple; L3 semantic) |
| F-GUARD-001 | Portkey | E-PK-003 | Output is validated against operator-selected guardrail rule packs. | False positives reject valid output. | Plugin | Pluggable guardrail engine; operator chooses rule packs; bypass path requires operator role and audit. | AT-GUARD-001 | Codex | Open (L2 plugin shell; L3 first packs) |
| F-MM-001 | Portkey | E-PK-004 | Single OpenAI-compatible request shape supports text, vision, audio in/out, image generation. | Provider capability variance is large. | Mandatory Roadmap | Multi-modal request normalization. Phase 9+. Capability matrix per model required before claim. | AT-MM-001 | Codex | Open (L3 Phase 9+) |
| F-RT-001 | Portkey | E-PK-006 | Real-time WebSocket request surface is supported (OpenAI Realtime-style). | Mid-stream drop loses partial usage. | Mandatory Roadmap | WebSocket protocol surface with stream resume + partial usage settlement. Phase 9+. | AT-RT-001 | Codex | Open (L3 Phase 9+) |
| F-PROTO-001 | LiteLLM | E-LM-006 | Gateway bridges external agent/tool protocols (MCP, A2A) so any LLM can use them. | Tool execution surface = malicious-tool risk. | Plugin | External-protocol bridging behind a plugin boundary; tool execution sandboxed; protocol versions explicit. Phase 9+. | AT-PROTO-001 | Codex | Open (L3 Phase 9+) |
| F-AUTH-003 | LiteLLM | E-LM-005 | Operator can plug in enterprise SSO (SAML / OIDC) as an auth provider. | Single-tenant SSO behaves differently from SaaS multi-tenant SSO. | Plugin | SSO as additional auth-provider plugin under F-AUTH-001 abstraction; SaaS Edition (Phase 10+) defines tenant-scoped SSO. | AT-AUTH-003 | Codex | Open (L3 Personal; L4 SaaS) |
| F-TENANT-001 | LiteLLM, Portkey | E-LM-003, E-PK-008 | Per-tenant configuration travels with the credential: logging level, guardrail pack, caching policy. | Mis-scoped config leaks between tenants. | Implemented | Per-tenant config rows referenced by API Key resolution; cross-tenant tests required (DR-001 isolation tests). | AT-TENANT-001 | Codex | Open (L2 Personal default tenant; full at L4 SaaS) |
| F-SEC-003 | LiteLLM | E-LM-004 | Operator can verify the running container's image was signed by the project. | Verification optional in many deployments. | Mandatory Roadmap | Signed images + SBOM as Phase 8 production-hardening deliverable; required for SaaS Edition. | AT-SEC-003 | Codex | Open (L3 Phase 8) |

## Matrix Template

| Feature ID | Reference | Evidence ID | User Outcome | Risk | Disposition | Local Capability | Test ID | Owner | Status |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| TBD | TBD | TBD | TBD | TBD | Mandatory Roadmap | TBD | TBD | TBD | Open |

## Review Rules

- Every reference feature must appear here.
- Similar features may be merged only when the merged capability fully covers every user outcome.
- A safer equivalent must document the behavior preserved and the risk reduced.
- Mandatory roadmap items block parity claims until implemented, pluginized, or feature-flagged.
