This file is agent-facing and authoritative.

# Project Brief

## Product

An AI Gateway + Account Hub + Admin Ops Platform for managing model providers, accounts, keys, quota, billing, routing, protocol conversion, reliability, security, observability, and operator workflows.

## Owner-Stated Goal (Authoritative, 2026-04-28)

> "驱动：赚钱（成功后项目会开源）。主要的目的是在 sub2api 的基础上搭建出全面的更好的产品。因为 sub2api 的功能底座很好，但是能接入的 API 以及模型太少了。我需要增加完善。"

This is the **North Star** for every downstream PM decision, design tradeoff, and feature priority. Translation:

- **Commercial driver**: HUAKAI is a paid SaaS (monetization is required for success criterion). Open-sourcing happens AFTER commercial validation, not before.
- **Strategy**: HUAKAI is **Sub2API plus comprehensive breadth**. Sub2API's algorithmic foundation is acknowledged as already good (relay-station / pooling / billing claim gate / sticky session) — HUAKAI adopts it, does not try to outdo it on those axes.
- **Differentiator**: HUAKAI exceeds Sub2API on **provider integration breadth** and **completeness of supported models / protocols**. Sub2API's bottleneck is too few upstream integrations; HUAKAI's advantage is solving that gap.
- **Phasing implication**: Provider adapter coverage is **promoted from "long-tail catalog work" to a first-class L1/L2 concern**. Phase 5–9 must include provider-integration milestones, not just core gateway plumbing.
- **Quality / authenticity rule**: "必须要真实" (Owner directive 2026-04-28). Inventory completion is not understanding. Every L1/L2 algorithm requires source-verified deep decomposition + reviewer-lane sign-off before implementation. Ship slow, ship real.
- **Continuous learning rule**: "我们后续的维护也主要看借鉴平台的更新，他们更新后我们吸取问题，然后自查，更新我们的产品" (Owner directive 2026-04-28). HUAKAI's maintenance phase is operationalized in [24_REFERENCE_TRACKING_POLICY.md](24_REFERENCE_TRACKING_POLICY.md) — every tracked reference's release triggers a HUAKAI self-audit cycle. The mining pass is not a one-time event; it is the start of a continuous practice.

### Owner's Two Business Models (2026-04-28 refinement)

> "我有两种营业模式，一个是通过自用基座卖 API，一个是卖 SaaS"

HUAKAI directly enables both, mapped one-to-one to the DR-002 Editions:

| Model | Edition | Customer | Revenue |
| --- | --- | --- | --- |
| **Model 1: Owner self-deploys + sells API** | Personal Edition | End-user developers / consumers | Token usage / subscriptions |
| **Model 2: Owner sells SaaS** | SaaS Edition | Other operators who want to run their own Model-1 business | SaaS subscription / per-tenant fee |

Per [DR-002 §Owner Refinement](process/decisions/DR-002-product-editions.md), Personal Edition is a deployable **commercial** product (Owner runs it to earn money); SaaS Edition is a **managed platform** (tenants pay Owner; tenants in turn run their own Model-1 business). One codebase serves both.

### Success Criteria (Refined)

A HUAKAI v1.0 release is successful when **all** are true:

1. The commercial Personal Edition ships with at least Sub2API's full algorithmic feature base — no functional regression.
2. **Owner can run Model 1 commercially**: Personal Edition has the minimum operator surface to issue API Keys, charge end-users via at least one payment surface, enforce quota / rate / concurrency, and surface usage / billing / audit to both operator and end-user.
3. Provider catalog covers materially more upstream APIs / models than Sub2API, measured by a published catalog comparison ([DR-007](process/decisions/DR-007-product-positioning-and-breadth.md)).
4. SaaS Edition activation criteria are documented and an early-access SaaS deployment is reachable; SaaS Edition supports tenant onboarding, isolation, per-tenant billing, and gives each tenant the tools to operate Model 1 themselves.
5. Open-source release happens **after** Model-1 commercial validation (paying-customer or recurring-revenue threshold to be set later by Owner).
6. Every core algorithm (selection / quota / billing claim / streaming / retry / cooldown) has a source-verified prose decomposition and HUAKAI's design strictly equals or improves upstream behavior ([22 Deep Mining Mandate](22_DEEP_MINING_MANDATE.md)).

## Product Identity (Owner-Confirmed 2026-04-28)

HUAKAI sits in the **relay-station (中转站) / quota-pooling** product category, validated by Sub2API (14.5k stars) and the broader Chinese-ecosystem relay-station product family. The defining capability is **multi-account quota pooling**: operators bring their own upstream subscription / API accounts, the platform pools them into one shared logical capacity, and end-users consume that pooled capacity through platform-issued API Keys.

Generic OpenAI-compatible gateway features (routing, streaming, retry, observability) are necessary plumbing — they are not the product. The product is the pooling abstraction plus the operator workflows around it: per-User × per-Account concurrency limits, sticky-session routing, fair token-level billing, payment surfaces for self-service top-up, and operator dashboards that make a multi-account inventory tractable.

This positioning sharpens the Phase 10+ SaaS Edition vision in [DR-002](process/decisions/DR-002-product-editions.md): the SaaS Edition is not "generic AI gateway with multi-tenancy bolted on", it is "managed relay-station with tenant isolation", competing in the space Sub2API has already validated.

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

**Decided in [DR-002](process/decisions/DR-002-product-editions.md), 2026-04-28.**

HUAKAI delivers two intentional editions in sequence, sharing one codebase:

- **Personal Edition (Phase 1-9):** Single-organization deployment. Operator and end users belong to one tenant. The relay-station model still applies in Personal Edition: a solo operator pools their own multi-account subscriptions (e.g. ChatGPT Pro + Anthropic API + Gemini Pro + Azure) into one logical capacity. SaaS-only features (payment, multi-tenant onboarding, abuse-cross-tenant tools) are gated off by configuration.
- **SaaS Distribution Edition (Phase 10+):** Multi-tenant managed-relay-station offering with tenant onboarding, isolation, per-tenant billing, cross-tenant abuse controls, payment surfaces, and compliance export. Targets the product space validated by Sub2API. Activated by Owner after Personal Edition gathers user feedback.

The schema is tenant-aware from day 1 ([DR-001](process/decisions/DR-001-multi-tenancy.md)) so the SaaS Edition adds features without migration.

## Success Criteria

- No reference feature is silently dropped.
- All risky features have safe implementation strategies.
- Admin workflows are operationally complete.
- Release readiness is based on acceptance tests, clean-room review, security review, and parity audit.
- Personal Edition reaches Phase 9 release readiness independently of SaaS Edition status.
- SaaS Edition is activated only after Personal Edition feedback validates the product direction.
