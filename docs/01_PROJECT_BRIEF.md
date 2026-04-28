This file is agent-facing and authoritative.

# Project Brief

## Product

An AI Gateway + Account Hub + Admin Ops Platform for managing model providers, accounts, keys, quota, billing, routing, protocol conversion, reliability, security, observability, and operator workflows.

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

- **Personal Edition (Phase 1-9):** Single-organization deployment. Operator and end users belong to one tenant. Default for self-hosted use. Validates product-market fit and operational workflows. SaaS-only features are gated off by configuration.
- **SaaS Distribution Edition (Phase 10+):** Multi-tenant SaaS offering with tenant onboarding, isolation, per-tenant billing, cross-tenant abuse controls, and compliance export. Activated by Owner after Personal Edition gathers user feedback validating direction.

The schema is tenant-aware from day 1 ([DR-001](decisions/DR-001-multi-tenancy.md)) so the SaaS Edition adds features without migration.

## Success Criteria

- No reference feature is silently dropped.
- All risky features have safe implementation strategies.
- Admin workflows are operationally complete.
- Release readiness is based on acceptance tests, clean-room review, security review, and parity audit.
- Personal Edition reaches Phase 9 release readiness independently of SaaS Edition status.
- SaaS Edition is activated only after Personal Edition feedback validates the product direction.
