# DR-004: Frontend Framework

| Field | Value |
| --- | --- |
| Status | Decided |
| Date opened | 2026-04-28 |
| Date decided | 2026-04-28 |
| Owner | Owner |
| Affected docs | docs/14_UI_CONTRACTS.md, docs/16_PHASED_DELIVERY_PLAN.md |
| Supersedes | — |
| Superseded by | — |

## Question

Which TypeScript frontend framework does HUAKAI use for the admin / operations dashboard, given DR-003 already commits to TypeScript with types generated from the backend's OpenAPI?

## Context

- HUAKAI admin dashboard surfaces (per [14_UI_CONTRACTS.md](../14_UI_CONTRACTS.md)): Pools, Provider Accounts, Routes, Channels, Users, API Keys, Quota, Billing, Usage Logs, Audit Logs, Health checks, Plugins, Settings.
- Volume of UI work: heavy on tables, forms, datagrids, charts, virtualized log views, drill-down detail panels.
- Solo developer; OpenAPI codegen handles types; SSR is not a requirement (admin dashboard, not public-facing).
- Owner directive 2026-04-28: "naming and code-redundancy discipline" ([DR-003 Constraint 8](DR-003-technology-stack.md)) — framework choice must support clean conventions and lint enforcement.

## Candidates

React + Vite | Vue 3 + Vite | Svelte / SvelteKit | SolidJS | Angular

## Claude (PM-Orchestrator) view

- **Analysis:** All four open-source candidates can build the dashboard. The deciding axis is **operations-dashboard component leverage**: tables (sortable, filterable, paginated, virtualized), forms (validation, conditional rendering), charts, drill-down inspectors, audit-log streams. React has the deepest ecosystem here — TanStack Table/Query/Virtual, Recharts, Mantine, plus mature OpenAPI codegen targets. Vue 3 is close but smaller component pool for admin-specific patterns. Svelte and Solid are smaller and faster but require more custom UI work, which is exactly what a solo dev should avoid. Angular is too heavy for solo.
- **Recommendation:** **React + Vite + TanStack Router + Tailwind CSS**, with **TanStack Query / Table / Virtual** as the default UI data layer. Frontend OpenAPI client and types are generated, not hand-written.
- **Risks if adopted:** Bundle size larger than Svelte/Solid; dependency footprint requires careful lint enforcement to satisfy DR-003 Constraint 8.
- **Risks if rejected:** Vue/Svelte/Solid all add custom-UI work that compounds for a solo dev; Angular adds learning-curve and ceremony.
- **Confidence:** High
- **Updated:** 2026-04-28

## Codex (Reviewer) view

> Authored via `omc ask codex --agent-prompt critic` (gpt-5.5 + xhigh) 2026-04-28; raw artifact retained under `.omc/artifacts/ask/` (gitignored).

- **Critique of Claude's view:** React + Vite + TanStack Router is the correct default, but the framing should not be "popular frontend choice." The real reason is operations-dashboard leverage: tables, forms, charts, virtualized logs, API-key management, audit views, health drilldowns, and OpenAPI-generated TypeScript all have the deepest ecosystem in React. Vue / Svelte / Solid are viable but increase custom UI integration work. Angular is too heavy for a solo developer.
- **Production / testability concerns:** Frontend choice does not optimize gateway hot paths directly, but it determines whether operators can SEE and TUNE core algorithms: route reason, fallback chain, account health, quota reservation/release, billing reconciliation, p95/p99 latency, retry outcomes must all be visible. Require Playwright coverage for Pools, Accounts, Routes, Quota, Usage Logs, Audit Logs, and Health views. Require generated API clients/types only; no hand-written duplicate DTOs.
- **License / dependency concerns:** React / Vite / TanStack / Tailwind are permissive-compatible. Main risk is datagrid licensing creep: MUI X Pro/Premium and AG Grid Enterprise are commercial; Community/MIT tiers are acceptable but must be pinned intentionally. Prefer TanStack Table/Virtual first; use commercial grids only behind explicit Owner approval.
- **Recommendation:** **React + Vite + TanStack Router + Tailwind**, with TanStack Query / Table / Virtual as default UI data layer. Caveat: no Next.js unless SSR becomes a real requirement; admin ops dashboard does not need SSR.
- **Confidence:** High
- **Updated:** 2026-04-28

## Gemini (UI / Ops) view

> Edited only by Gemini. Section deferred — Gemini CLI is installed but Owner has not authenticated it yet (2026-04-28). Per [DR-000](DR-000-clean-room-methodology.md) Owner pattern, framework can be decided without Gemini view since all named candidates (React / Vue / Svelte / Solid) are mature TypeScript options that satisfy the dashboard requirements; framework choice does not foreclose UI-design directions Gemini would normally weigh in on.

## Conflicts

> Synthesized by Claude PM after the views are in.

No material conflicts. Claude and Codex independently converge on **React + Vite + TanStack Router + Tailwind, with TanStack Query/Table/Virtual as data layer**. Codex's contribution sharpens two operational requirements that fold into the Owner Decision constraints:

1. **Playwright coverage** for the operations-critical UI surfaces (Pools / Accounts / Routes / Quota / Usage Logs / Audit Logs / Health) — these are exactly the views that let operators see core-algorithm behavior the Owner has insisted on optimizing.
2. **Datagrid licensing discipline** — prefer TanStack Table/Virtual; commercial grids (MUI X Pro/Premium, AG Grid Enterprise) only behind explicit Owner approval.

## Owner Decision

| Field | Value |
| --- | --- |
| Decision | **React + Vite + TanStack Router + Tailwind CSS**, with **TanStack Query / Table / Virtual** as the default UI data layer; OpenAPI codegen for types and clients; no Next.js. |
| Decision date | 2026-04-28 |
| Reasoning | Owner directive "开干" + "算法要优化，尤其是核心" (2026-04-28). PM and Codex Reviewer independently picked this stack at High confidence; the operations-dashboard leverage of React's ecosystem reduces solo-dev custom-UI work and lets operators see the core algorithm behavior the Owner needs to tune. |
| Constraints attached | (1) Playwright e2e coverage required for Pools, Accounts, Routes, Quota, Usage Logs, Audit Logs, and Health views before Phase 2 sign-off. (2) Datagrid licensing: TanStack Table/Virtual default; commercial grids (MUI X Pro/Premium, AG Grid Enterprise) require explicit Owner approval per project. (3) No hand-written shared types between backend and frontend; types are codegen'd from OpenAPI per DR-003 Constraint 2. (4) Naming aligned to [18_GLOSSARY.md](../18_GLOSSARY.md) per DR-003 Constraint 8; ESLint config enforces forbidden synonyms. |

## Propagation Checklist

- [ ] Update [14_UI_CONTRACTS.md](../14_UI_CONTRACTS.md) to note React + TanStack stack as the implementation surface.
- [ ] Update [16_PHASED_DELIVERY_PLAN.md](../16_PHASED_DELIVERY_PLAN.md) Phase 3 deliverables to call out Vite + TanStack Router + Tailwind + Playwright in the frontend skeleton.
- [ ] Mark Status = Implemented when all above are done.
