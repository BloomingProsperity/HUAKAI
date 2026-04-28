This file is agent-facing and authoritative.

# Project Brief

## Product

An AI Gateway + Account Hub + Admin Ops Platform for managing model providers, accounts, keys, quota, billing, routing, protocol conversion, reliability, security, observability, and operator workflows.

## Product Identity (Owner-Confirmed 2026-04-28)

HUAKAI sits in the **relay-station (中转站) / quota-pooling** product category, validated by Sub2API (14.5k stars) and the broader Chinese-ecosystem relay-station product family. The defining capability is **multi-account quota pooling**: operators bring their own upstream subscription / API accounts, the platform pools them into one shared logical capacity, and end-users consume that pooled capacity through platform-issued API Keys.

Generic OpenAI-compatible gateway features (routing, streaming, retry, observability) are necessary plumbing — they are not the product. The product is the pooling abstraction plus the operator workflows around it: per-User × per-Account concurrency limits, sticky-session routing, fair token-level billing, payment surfaces for self-service top-up, and operator dashboards that make a multi-account inventory tractable.

This positioning sharpens the Phase 10+ SaaS Edition vision in [DR-002](decisions/DR-002-product-editions.md): the SaaS Edition is not "generic AI gateway with multi-tenancy bolted on", it is "managed relay-station with tenant isolation", competing in the space Sub2API has already validated.

## Target

Reach full feature parity or better with Sub2API, New API, All API Hub, and similar high-star maintained open-source projects, using clean-room implementation methods compatible with an MIT project.

## Product Pillars

- Multi-provider AI gateway.
- Account and credential hub.
- Channel and route management.
- Protocol conversion and compatibility.
- Quota, rate limit, billing, and usage accounting.
- Reliability controls and fallback routing.
- Security, auditability, and abuse prevention.
- Admin operations dashboard.
- Plugin and feature-flag extensibility.
- Scenario-driven acceptance testing.

## Product Editions

**Decided in [DR-002](decisions/DR-002-product-editions.md), 2026-04-28.**

HUAKAI delivers two intentional editions in sequence, sharing one codebase:

- **Personal Edition (Phase 1-9):** Single-organization deployment. Operator and end users belong to one tenant. The relay-station model still applies in Personal Edition: a solo operator pools their own multi-account subscriptions (e.g. ChatGPT Pro + Anthropic API + Gemini Pro + Azure) into one logical capacity. SaaS-only features (payment, multi-tenant onboarding, abuse-cross-tenant tools) are gated off by configuration.
- **SaaS Distribution Edition (Phase 10+):** Multi-tenant managed-relay-station offering with tenant onboarding, isolation, per-tenant billing, cross-tenant abuse controls, payment surfaces, and compliance export. Targets the product space validated by Sub2API. Activated by Owner after Personal Edition gathers user feedback.

The schema is tenant-aware from day 1 ([DR-001](decisions/DR-001-multi-tenancy.md)) so the SaaS Edition adds features without migration.

## Success Criteria

- No reference feature is silently dropped.
- All risky features have safe implementation strategies.
- Admin workflows are operationally complete.
- Release readiness is based on acceptance tests, clean-room review, security review, and parity audit.
- Personal Edition reaches Phase 9 release readiness independently of SaaS Edition status.
- SaaS Edition is activated only after Personal Edition feedback validates the product direction.
