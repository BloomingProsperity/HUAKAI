# DR-003: Technology Stack For Phase 2-9 Personal Edition

| Field | Value |
| --- | --- |
| Status | Decided |
| Date opened | 2026-04-28 |
| Date decided | 2026-04-28 |
| Date implemented | (pending) |
| Owner | Owner |
| Affected docs | docs/13_API_CONTRACTS.md, docs/14_UI_CONTRACTS.md, docs/16_PHASED_DELIVERY_PLAN.md, docs/19_DOMAIN_MODEL.md |
| Supersedes | — |
| Superseded by | — |

## Question

Pick one technology stack for Phase 2-9 Personal Edition implementation, with the explicit constraint that the SAME codebase must later activate the Phase 10+ SaaS Distribution Edition without rewrite ([DR-002](DR-002-product-editions.md)). The choice covers backend gateway/control plane and admin UI/operations dashboard.

## Context

- Phase 0.5 governance is complete; **56 features** mapped in [03_FEATURE_PARITY_MATRIX.md](../../03_FEATURE_PARITY_MATRIX.md) across **8 references** (one-api MIT, LiteLLM MIT, Portkey MIT, New API AGPL, Sub2API LGPL, All API Hub AGPL, Helicone GPL-3.0, Envoy AI Gateway Apache-2.0).
- **13 L1 MVP features** have acceptance test directions in [11_ACCEPTANCE_TEST_MATRIX.md](../../11_ACCEPTANCE_TEST_MATRIX.md), including AT-POOL-001, AT-MODE-001, and AT-SEC-005 added after the Codex Phase 1 audit.
- DR-001 commits to tenant-aware schema from day 1.
- DR-002 commits to Personal Edition first, SaaS in Phase 10+, both on one codebase.
- Owner-confirmed product identity (2026-04-28, [01_PROJECT_BRIEF.md §Product Identity](../../01_PROJECT_BRIEF.md)): HUAKAI is a relay-station / quota-pooling AI gateway, not a generic AI gateway. F-POOL-001 promoted to L1 MVP.
- Owner is a solo developer doing both backend and frontend.
- Mining anchors: one-api and New API are Go; LiteLLM is Python; Portkey is TypeScript; Helicone is Rust. The choice should reflect technical fit, not reference imitation, per the clean-room methodology in [05_CLEAN_ROOM_POLICY.md](../../05_CLEAN_ROOM_POLICY.md).

## Staleness Refresh

- **2026-04-28** — Context numbers refreshed from 35 features / 4 references / 10 L1 tests to 56 features / 8 references / 13 L1 tests. Relay-station product identity acknowledged; no change to recommendation. Per [docs/21 Staleness Protocol](../../21_DECISION_PROCESS.md).

## Candidate Stacks

| # | Backend | Frontend | Notes |
| --- | --- | --- | --- |
| 1 | Go (Gin / Echo / Fiber) | TypeScript (React / Vue / Svelte) with OpenAPI-generated types | Single binary, low memory, mature observability (Prometheus / OpenTelemetry / pprof). Same family as MIT anchor one-api. |
| 2 | Rust (axum / actix-web) | TypeScript with generated types | Maximal performance and safety; longer dev time, smaller solo ecosystem. |
| 3 | TypeScript on Node.js or Bun (Hono / Fastify) | TypeScript shared monorepo | Single language end-to-end; streaming/abort semantics need care; npm supply-chain audit work. |
| 4 | Python on FastAPI | TypeScript with generated types | Huge LLM ecosystem; GIL concurrency model and runtime startup are gateway-throughput risks. |

## Claude (PM-Orchestrator) view

> Edited only by Claude.

- **Analysis:** The deciding axis is gateway runtime correctness under concurrent streaming with retry, fallback, quota reservation, and per-tenant isolation. All four candidates can theoretically deliver the feature set, but they differ sharply in how easy it is to get hard cases right and to keep the implementation auditable for SaaS-grade ops. Go gives the lowest-friction path to a single binary that streams cleanly, cancels cleanly via `context`, and tests deterministically with `go test -race`; the runtime deps for a production gateway can stay close to the standard library plus a small permissive set, which keeps both license risk and supply-chain audit work small. TypeScript is excellent for the admin UI but is a second-best choice for the gateway runtime because stream/abort propagation, multi-tenant noise-neighbor risk, and large npm transitive surfaces add hidden work. Rust is technically strongest but pays a solo-developer velocity tax that does not match Personal-Edition-first urgency. Python is productive for prototypes but the GIL plus per-request memory makes the gateway harder to scale into SaaS without later language change.
- **Recommendation:** **Go for the Phase 2-9 backend (gateway + control plane), TypeScript for the frontend with types generated from the backend's OpenAPI / JSON Schema contracts.** Frontend framework left as a follow-up DR (lighter call than language).
- **Risks if adopted:** Owner ramp on Go if not already fluent (mitigated by Go's compact spec); discipline required to keep runtime deps small (no Gin → Echo → Fiber framework churn — pick one early); OpenAPI codegen pipeline needs setup and is one more moving part.
- **Risks if rejected:** Pure TypeScript = future stream/concurrency rewrites; Python = throughput ceiling forces language change before SaaS; Rust = solo-dev burnout risk during Personal Edition.
- **Confidence:** High
- **Updated:** 2026-04-28

## Codex (Reviewer) view

> Edited only by Codex. Authored via `omc ask codex` 2026-04-28; raw artifact retained under `.omc/artifacts/ask/`.

- **Critique of Claude's view:** Independent first pass: the deciding axis should be SaaS-grade gateway correctness, not reference-project similarity or raw benchmark appeal. Solo-dev ergonomics matters, but the no-rewrite requirement makes streaming, retries, quota/billing consistency, observability, and deploy simplicity more important than sharing one language with the frontend.
- **Production / testability concerns:** Go is the easiest stack to test hard under streaming + retry + concurrency: `httptest`, `context` cancellation, `http.Server` streaming behavior, table-driven retry/fallback tests, and `go test -race` give direct coverage of the failure modes HUAKAI must survive. TypeScript is faster for UI/API iteration, but stream semantics, abort propagation, and tenant isolation under concurrent load are easier to get subtly wrong. Rust is strongest but too slow for the Owner's solo throughput; Python is productive but weaker for high-concurrency gateway confidence.
- **License / dependency concerns:** Go has the lowest practical clean-room dependency risk for the runtime gateway because a production implementation can stay close to the standard library plus a small set of permissive libraries. TypeScript/npm and Python have larger transitive dependency surfaces and more supply-chain/license audit noise. Rust is generally license-friendly too, but crate sprawl plus slower delivery makes it less attractive here.
- **Recommendation:** Choose **Go for the Phase 2-9 backend gateway/control plane, with a TypeScript frontend generated from OpenAPI/JSON Schema contracts**. Caveats: keep tenant-aware schema and request accounting from day 1; define a provider-neutral streaming abstraction before provider integrations; require deterministic tests for retry, fallback, cancellation, quota reservation/release, and concurrent usage accounting; avoid framework lock-in and keep runtime deps small; use generated frontend types instead of choosing Node only for shared typing. This is not because one reference project uses Go; it is because Go best balances solo maintainability, gateway runtime behavior, SaaS readiness, clean deployment, and license hygiene.
- **Confidence:** High
- **Updated:** 2026-04-28

## Gemini (UI / Ops) view

> Edited only by Gemini. **Section deferred by Owner pattern (per DR-000)**: admin UI work is Phase 7+ and the stack-language choice does not block UI design.

- **Status:** No Gemini input collected for this DR. Frontend framework selection (React vs Vue vs Svelte) is intentionally separated into a follow-up DR where Gemini's view will be material. The TS-frontend recommendation here only commits to the language family; the framework remains open.
- **Updated:** 2026-04-28

## Conflicts

> Synthesized by Claude PM after the three views are in.

No material conflicts. Claude and Codex independently converge on **Go backend + TypeScript frontend with OpenAPI codegen**. Codex's framing is sharper on three points that Claude PM accepts and folds into the synthesized recommendation:

1. **Deciding axis:** SaaS-grade gateway correctness, not reference-project similarity. The Go choice happens to match the MIT anchor one-api but the recommendation must NOT be defended on that basis. Defend it on streaming, retry, concurrency, cancellation, and license-hygiene grounds.
2. **Caveat list is non-negotiable:** tenant-aware schema and request accounting from day 1 (DR-001 already commits this); provider-neutral streaming abstraction defined before provider integrations; deterministic tests for retry / fallback / cancellation / quota reservation+release / concurrent usage accounting; framework lock-in avoided; runtime deps small.
3. **Frontend codegen, not Node-for-shared-typing:** the recommendation does not endorse using Node solely to share types; types are generated from the backend's OpenAPI contract. Frontend framework choice is a separate DR.

**Synthesized recommendation entering the Owner Decision:** Go (standard library + minimal permissive deps; one HTTP framework picked early and kept) for backend Phase 2-9; TypeScript for the admin/operations frontend with types generated from backend OpenAPI/JSON Schema; frontend framework chosen in a follow-up DR.

## Owner Decision

> Owner only.

| Field | Value |
| --- | --- |
| Decision | **Go** for the Phase 2-9 backend gateway and control plane; **TypeScript** for the admin / operations frontend with types generated from the backend's OpenAPI / JSON Schema contract. |
| Decision date | 2026-04-28 |
| Reasoning | Claude (PM) and Codex (Reviewer) independently converged on the same answer on streaming + retry + concurrency + cancellation + license-hygiene grounds. Owner accepts the synthesized recommendation. |
| Constraints attached | (see numbered constraints below) |

### Constraints (binding for Phase 2-9)

1. **Minimal permissive Go runtime deps.** Stay close to the standard library plus a small permissive set; pick **one** HTTP framework early and do not churn (no Gin -> Echo -> Fiber).
2. **OpenAPI / JSON Schema is the contract source of truth.** Frontend TypeScript types are GENERATED from the backend's OpenAPI artifact; no hand-written shared types. Codegen tool selected in a follow-up DR or in Phase 3 skeleton.
3. **Provider-neutral streaming abstraction lands BEFORE provider integrations.** A single internal streaming type carries OpenAI-compatible, Anthropic, Gemini, and self-hosted streaming under one shape; provider adapters convert in.
4. **Deterministic tests for the relay-station hot paths.** Retry, fallback, request cancellation, quota reservation/release, and concurrent usage accounting must each have table-driven Go tests runnable under `go test -race`.
5. **Tenant-aware schema from day 1** ([DR-001](DR-001-multi-tenancy.md)). Every primary table carries a non-null `tenant_id`; MVP uses a single hard-coded default tenant; cross-tenant isolation tests are mandatory at Phase 2 schema review.
6. **Option C strict-mode specs required for these areas before implementation** (per [DR-000](DR-000-clean-room-methodology.md) carve-out): pool-aware routing logic, billing reconciliation across pooled accounts, provider/account-health heuristics. These are the highest-AGPL-exposure feature areas; spec-leakage review against [_REVIEW_CHECKLIST.md](../../specs/_REVIEW_CHECKLIST.md) is non-negotiable before any implementer-lane work consumes them.
7. **Frontend framework deferred to DR-004.** Owner Decision here only commits to TypeScript as a language; React / Vue / Svelte choice waits for Gemini view in DR-004.
8. **Naming and code-redundancy discipline (Owner directive 2026-04-28).** Frontend and backend project naming must be standardized and clean: one consistent identifier per concept (single canonical name from [18_GLOSSARY.md](../../18_GLOSSARY.md), no synonyms in code), one consistent file/module naming convention picked early and applied repo-wide, no duplicate logic across modules, no parallel "for now / temporary / will-clean-later" abstractions. Concretely:
    - Entity names in code match the glossary exactly (e.g. `Pool` not `PoolingGroup` *and* `Pool` mixed; `ProviderAccount` not `Provider_Account` *and* `provider-account`).
    - One source of truth per concept: e.g. quota math lives in one package, called from everywhere; not reimplemented in three handlers.
    - Linter-enforceable conventions captured in `golangci-lint.yml` and the frontend ESLint config; CI fails on drift.
    - DRY applies to specs and docs too: a behavior described in one spec must be referenced from other specs, not redescribed.

## Propagation Checklist

- [ ] Open DR-004 (frontend framework: React / Vue / Svelte) — Gemini view material, not deferred.
- [ ] Open DR-005 (Go HTTP framework: Gin / Echo / Fiber / stdlib only) — narrow technical call.
- [ ] Open DR-006 (database: PostgreSQL / SQLite / MySQL) — affects Phase 3 skeleton.
- [x] Update [13_API_CONTRACTS.md](../../13_API_CONTRACTS.md) to note OpenAPI is the contract source-of-truth and frontend types are generated from it.
- [x] Update [16_PHASED_DELIVERY_PLAN.md](../../16_PHASED_DELIVERY_PLAN.md) Phase 3 deliverable list to call out Go module + OpenAPI codegen as initial scaffolding.
- [x] Update [19_DOMAIN_MODEL.md](../../19_DOMAIN_MODEL.md) §Open Questions for Phase 2 — close the language-choice question.
- [ ] Mark Status = Implemented when DR-004/005/006 are also opened.
