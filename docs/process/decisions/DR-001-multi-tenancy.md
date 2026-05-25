# DR-001: Tenant-Aware Schema From Day 1

| Field | Value |
| --- | --- |
| Status | Decided |
| Date opened | 2026-04-28 |
| Date decided | 2026-04-28 |
| Owner | Owner |
| Affected docs | docs/19_DOMAIN_MODEL.md, docs/02_CAPABILITY_CONTRACT.md, docs/13_API_CONTRACTS.md, docs/17_FEATURE_LEVEL_MATRIX.md |
| Supersedes | — |
| Superseded by | — |

## Question

Does HUAKAI commit to a tenant-aware data model from the first MVP build, or does it ship single-tenant first and add multi-tenancy later?

## Context

- Multi-tenancy decisions affect every primary table (User, API Key, Provider Account, Channel, Route, Quota, Usage Record, Billing Ledger, Audit Event).
- Adding a `tenant_id` column after data has accumulated is a global migration: cheap at 50k rows, expensive at 50M rows, near-impossible without downtime at production scale.
- HUAKAI's positioning ("AI Gateway + Account Hub + Admin Ops Platform") matches both private-deployment and SaaS product shapes; the Owner has not committed to either.
- See [19_DOMAIN_MODEL.md §Multi-Tenancy Decision Required](../../19_DOMAIN_MODEL.md) for the original options.

## Claude (PM-Orchestrator) view

- **Analysis:** The marginal cost of adding `tenant_id` to MVP schemas is small: ~5% schema complexity overhead, one default tenant constant, one query-builder convention. The cost of *not* adding it and reversing later is dominated by migration risk: multi-month effort plus an unavoidable maintenance window once the platform has paying users. Tenant-aware-from-day-1 also makes future enterprise concerns (cross-tenant audit, per-tenant feature flags, per-tenant rate limits) tractable as additive work rather than rewrites.
- **Recommendation:** Tenant-aware from day 1 with a single hard-coded default tenant in MVP; expose multi-tenant admin in Phase 9.
- **Risks if adopted:** Slightly slower L1 schema bring-up; risk of leaking the `tenant_id` abstraction into UI prematurely (mitigation: hide in UI until Phase 9).
- **Risks if rejected:** Migration pain at scale; loss of optionality for SaaS product shape.
- **Confidence:** High
- **Updated:** 2026-04-28

## Codex (Reviewer) view

- **Critique of Claude's view:** No material input expected — this is a forward-looking architectural decision with a well-understood reversibility cost. Codex will engage at Phase 2 schema review to validate isolation tests (no cross-tenant data leakage in queries, no shared cache keys missing tenant scope).
- **Production / testability concerns:** Test plan for Phase 2 must include negative tests: a User in tenant A must not be able to read keys, channels, accounts, usage, or audit rows tagged tenant B, even with crafted query parameters.
- **License / dependency concerns:** None.
- **Recommendation:** Tenant-aware from day 1.
- **Confidence:** Medium (will sharpen at Phase 2 schema review)
- **Updated:** 2026-04-28 (placeholder; Codex may revise)

## Gemini (UI / Ops) view

- **UI / operability impact:** No material input — MVP admin UI shows the single default tenant transparently (no tenant switcher, no cross-tenant filters). Multi-tenant admin UI (tenant switcher, tenant creation, per-tenant settings, cross-tenant audit views) is deferred to Phase 9 per [16_PHASED_DELIVERY_PLAN.md](../../16_PHASED_DELIVERY_PLAN.md).
- **API-shape impact:** API endpoints in MVP do not expose `tenant_id` to clients; tenancy is server-resolved from the API Key.
- **Operator workflow concerns:** None for MVP. Phase 9 must add: tenant onboarding workflow, tenant suspension workflow, cross-tenant search for ops/abuse investigation.
- **Recommendation:** Tenant-aware from day 1; UI exposure deferred to Phase 9.
- **Confidence:** Medium (will sharpen when Phase 9 admin UI is scoped)
- **Updated:** 2026-04-28 (placeholder; Gemini may revise)

## Conflicts

- (none) — all three views align with the recommended option.

## Owner Decision

| Field | Value |
| --- | --- |
| Decision | Tenant-aware from day 1 with a single default tenant in MVP. |
| Decision date | 2026-04-28 |
| Reasoning | Optionality preserved at low upfront cost; reversal cost grows fast with data volume. |
| Constraints attached | Multi-tenant UI surfaces deferred to Phase 9; tenancy is server-resolved from API Key in MVP and never exposed to clients until Phase 9. |

## Propagation Checklist

- [ ] Update [19_DOMAIN_MODEL.md](../../19_DOMAIN_MODEL.md) — replace the "Multi-Tenancy Decision Required" section with a "Decided" note pointing to this DR.
- [ ] Update [13_API_CONTRACTS.md](../../13_API_CONTRACTS.md) — note that tenancy is server-resolved and never client-exposed in MVP.
- [ ] Update [17_FEATURE_LEVEL_MATRIX.md](../../17_FEATURE_LEVEL_MATRIX.md) — confirm "multi-tenant organization model" is L4-only and represents UI/admin surface, not schema.
- [ ] Phase 2 schema review must include cross-tenant isolation tests (Codex section above).
- [ ] Mark Status = Implemented when all above are done.
