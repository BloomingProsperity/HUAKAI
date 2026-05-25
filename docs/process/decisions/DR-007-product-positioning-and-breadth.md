# DR-007: HUAKAI Product Positioning — "Sub2API Plus Breadth"

| Field | Value |
| --- | --- |
| Status | Decided |
| Date opened | 2026-04-28 |
| Date decided | 2026-04-28 |
| Owner | Owner |
| Affected docs | docs/01_PROJECT_BRIEF.md, docs/03_FEATURE_PARITY_MATRIX.md, docs/16_PHASED_DELIVERY_PLAN.md, docs/17_FEATURE_LEVEL_MATRIX.md, docs/22_DEEP_MINING_MANDATE.md |
| Supersedes | — |
| Superseded by | — |

## Question

What is HUAKAI's commercial positioning relative to Sub2API and the rest of the relay-station / AI gateway market, and how does that positioning change the priority order of L1/L2 work?

## Context

- Earlier DRs anchored the product category (DR-002 Personal then SaaS), the schema (DR-001 tenant-aware), the methodology (DR-000 Option B + Option C carve-out), and the technology stack (DR-003 Go + TS, DR-004 React, DR-005 chi, DR-006 Postgres).
- None of these DRs spelled out the **commercial goal** or what makes HUAKAI different from existing competitors.
- Owner's verbal directive 2026-04-28 (now recorded verbatim in [01_PROJECT_BRIEF.md §Owner-Stated Goal](../../01_PROJECT_BRIEF.md)): HUAKAI is "Sub2API plus broader integration", commercial first then open-source after commercial validation.

## Owner Decision

| Field | Value |
| --- | --- |
| Decision | HUAKAI's positioning is **"Sub2API plus comprehensive breadth"**. HUAKAI adopts Sub2API's algorithmic foundation (acknowledged as already good) and differentiates on provider / model / protocol integration breadth, completeness, and operational polish. Commercial SaaS first; open-source the codebase after commercial validation. |
| Decision date | 2026-04-28 |
| Reasoning | Owner directly stated this goal. Sub2API has 14.5k stars and validated market fit for the relay-station product category but is bottlenecked by limited upstream coverage; HUAKAI's commercial value lies in solving that bottleneck while keeping Sub2API's good algorithms. |
| Constraints attached | (see binding constraints below) |

### Binding Constraints

1. **Provider catalog breadth is L1/L2 work, not deferred.** Provider adapter implementation milestones are explicit in [16_PHASED_DELIVERY_PLAN.md](../../16_PHASED_DELIVERY_PLAN.md) starting Phase 5; provider adapter dives are explicit Mandated Next Dives in the [22 mandate](../../22_DEEP_MINING_MANDATE.md) per LiteLLM (100+ providers) and Portkey (250+ models, 45+ providers) inventories. Phase 9 cannot exit if provider catalog does not materially exceed Sub2API.
2. **Sub2API algorithm adoption is allowed, not required to outdo.** HUAKAI uses Sub2API's algorithmic behavior as the design floor for selection / sticky / billing claim gate / health probing. Where Sub2API's design has gaps (E-S2A-PROXY critical defects 1–5; quota race condition documented as E-OAI-DEEP-008 inheritance risk), HUAKAI MUST do strictly better. Where Sub2API's design is sound, HUAKAI MAY adopt it without trying to invent a "better" alternative.
3. **Commercial SaaS gating is required.** The SaaS Edition (Phase 10+) is the monetization vehicle. SaaS-only features (payment, multi-tenant onboarding, billing integration, abuse-cross-tenant tools) must be deliverable on the same codebase per DR-002.
4. **Open-source release follows commercial validation.** A revenue / paying-customer threshold (Owner-set, currently TBD) gates the open-source release. Until that threshold is reached the codebase is private. The MIT license declared in `LICENSE` applies to the codebase regardless of whether it is publicly distributed.
5. **Authenticity rule (Owner directive "必须要真实")**: feature breadth claims must be backed by deep decomposition and verified provider integration tests. No "we support 100+ providers" marketing without per-provider acceptance test rows.
6. **Ship slow, ship real**: timeline is bounded by the depth of decomposition + reviewer-lane sign-off, not by an external schedule. Owner accepts slower delivery as the price of authentic engineering.

## Propagation Checklist

- [x] Update [01_PROJECT_BRIEF.md](../../01_PROJECT_BRIEF.md) — Owner-Stated Goal section recorded verbatim.
- [ ] Update [03_FEATURE_PARITY_MATRIX.md](../../03_FEATURE_PARITY_MATRIX.md) — promote provider-adapter-breadth rows to L1/L2 with explicit acceptance test obligations.
- [ ] Update [16_PHASED_DELIVERY_PLAN.md](../../16_PHASED_DELIVERY_PLAN.md) — add provider-integration milestones to each phase from Phase 5 onward; Phase 9 exit criterion includes "catalog materially exceeds Sub2API".
- [ ] Update [17_FEATURE_LEVEL_MATRIX.md](../../17_FEATURE_LEVEL_MATRIX.md) — add "Provider Catalog Breadth" capability row with L1 / L2 / L3 / L4 progression.
- [ ] Update [22_DEEP_MINING_MANDATE.md](../../22_DEEP_MINING_MANDATE.md) — provider adapter dives across LiteLLM / Portkey / Sub2API are explicit Phase-1-exit blockers.
- [ ] Mark Status = Implemented when all above are done.
