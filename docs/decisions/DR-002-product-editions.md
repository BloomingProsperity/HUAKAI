# DR-002: Personal Edition First, SaaS Edition After Feedback

| Field | Value |
| --- | --- |
| Status | Decided |
| Date opened | 2026-04-28 |
| Date decided | 2026-04-28 |
| Owner | Owner |
| Affected docs | docs/01_PROJECT_BRIEF.md, docs/16_PHASED_DELIVERY_PLAN.md, docs/17_FEATURE_LEVEL_MATRIX.md |
| Supersedes | — |
| Superseded by | — |

## Question

Does HUAKAI ship as a single product, or as two intentional editions delivered in sequence — Personal Edition first, SaaS Distribution Edition after Personal Edition has validated product-market fit?

## Context

- Owner has confirmed (2026-04-28) that initial use is personal / single-organization, with SaaS distribution as a planned later expansion.
- Schema decision DR-001 (tenant-aware from day 1) was made specifically so the SaaS path can be activated without a global migration.
- Without an explicit two-edition commitment, there is a risk that Phase 1-9 features either (a) over-build SaaS-only complexity prematurely, or (b) hard-code single-tenant assumptions that block SaaS later.

## Claude (PM-Orchestrator) view

- **Analysis:** Two-edition strategy with one codebase is a well-trodden pattern (e.g. Sentry, GitLab, Mattermost, Posthog). It works when the schema and core domain are SaaS-shaped from the start (DR-001 already commits to this) and feature gating is config-driven, not fork-driven. Risk concentrates in two places: feature-flag hygiene (so Personal Edition deployments are not exposed to half-built SaaS features) and Edition-specific UI surfaces (tenant management, billing UI, abuse cross-tenant tools — these only ship when SaaS is activated).
- **Recommendation:** Adopt the two-edition strategy with sequential delivery. Personal Edition is the focus through Phase 9. SaaS Edition is a post-MVP track triggered by Owner after user feedback validates the product. Both editions share one codebase; edition selection is via configuration.
- **Risks if adopted:** Feature-flag drift; Personal users may see SaaS-shaped abstractions in the schema (acceptable — `tenant_id` is invisible to them in normal use).
- **Risks if rejected:** Either premature SaaS investment or single-tenant lock-in; both are worse than the proposed sequence.
- **Confidence:** High
- **Updated:** 2026-04-28

## Codex (Reviewer) view

- **Critique of Claude's view:** No material input — strategic sequencing is an Owner call. Codex will validate at Phase 8 production hardening that no SaaS-only code path leaks into Personal Edition deployment defaults (e.g. tenant onboarding endpoints must be off by default until SaaS Edition is enabled).
- **Production / testability concerns:** Test plan must include "Personal Edition deployment" scenario with SaaS-only feature flags off; verify no hidden cross-tenant assumptions in queries.
- **License / dependency concerns:** None.
- **Recommendation:** Adopt as proposed.
- **Confidence:** Medium (will sharpen at Phase 8)
- **Updated:** 2026-04-28 (placeholder; Codex may revise)

## Gemini (UI / Ops) view

- **UI / operability impact:** Personal Edition admin UI hides tenant management entirely. SaaS Edition adds a tenant switcher, tenant onboarding wizard, per-tenant settings panel, and cross-tenant audit/abuse views. UI must check the active edition before rendering tenant-related surfaces.
- **API-shape impact:** Personal Edition exposes no `tenant_id` in any client-facing API. SaaS Edition adds tenant-scoped resource paths or tenant-resolution middleware.
- **Operator workflow concerns:** Phase 10 (SaaS) needs: tenant onboarding flow, tenant suspension flow, billing-per-tenant dashboard, abuse-across-tenants investigation tools.
- **Recommendation:** Adopt as proposed; defer SaaS UI to a Phase 10 track.
- **Confidence:** Medium (will sharpen when Phase 10 admin UI is scoped)
- **Updated:** 2026-04-28 (placeholder; Gemini may revise)

## Conflicts

- (none) — all three views align with the recommended option.

## Owner Decision

| Field | Value |
| --- | --- |
| Decision | Two editions, sequential delivery: Personal Edition (Phase 1-9), SaaS Distribution Edition (Phase 10+, post-feedback). |
| Decision date | 2026-04-28 |
| Reasoning | Currently personal use; SaaS is a real future plan but should be triggered by validated user feedback, not pre-built. Schema (DR-001) already supports both, so no migration cost. |
| Constraints attached | One codebase, no fork. Edition gated by configuration / feature flags. Personal Edition deployments must default all SaaS-only features off. |

## Propagation Checklist

- [ ] Update [01_PROJECT_BRIEF.md](../01_PROJECT_BRIEF.md) — add Product Editions section.
- [ ] Update [16_PHASED_DELIVERY_PLAN.md](../16_PHASED_DELIVERY_PLAN.md) — clarify Phase 1-9 = Personal Edition; introduce Phase 10 = SaaS Edition track.
- [ ] Update [17_FEATURE_LEVEL_MATRIX.md](../17_FEATURE_LEVEL_MATRIX.md) — annotate which capabilities are Personal vs SaaS-only at L4.
- [ ] Mark Status = Implemented when all above are done.
