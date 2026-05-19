# DR-005: Go HTTP Framework

| Field | Value |
| --- | --- |
| Status | Decided |
| Date opened | 2026-04-28 |
| Date decided | 2026-04-28 |
| Owner | Owner |
| Affected docs | docs/13_API_CONTRACTS.md, docs/16_PHASED_DELIVERY_PLAN.md |
| Supersedes | — |
| Superseded by | — |

## Question

Which Go HTTP framework / router does HUAKAI use, given DR-003 commits to Go for the backend and Owner directive 2026-04-28 demands "core algorithms must be optimized, especially the core"?

## Context

- HUAKAI hot paths: streaming chat completions (provider → client passthrough), retry+fallback across pooled Provider Accounts, request cancellation via `context.Context`, multi-tenant request scoping, OpenAPI-derived contract.
- The product IS the gateway core; performance and cancellation correctness must NOT be hidden behind a framework abstraction.
- Phase 1 deep decomposition of one-api ([E-OAI-DEEP-001..009](../../07_REFERENCE_EVIDENCE_LEDGER.md)) shows the upstream relay is built on stdlib `net/http` semantics, which validates that path.

## Candidates

stdlib `net/http` + `chi` | Gin | Echo | Fiber (`fasthttp`-based) | Huma | Go 1.22+ stdlib `ServeMux` only

## Claude (PM-Orchestrator) view

- **Analysis:** The deciding axis is **stay close to `net/http` and `context.Context`** so streaming, cancellation, and tenant scoping are written directly against the runtime, not against framework wrappers. `chi` is the smallest router that adds idiomatic middleware composition without a runtime layer; it is MIT-licensed, ~1k LoC, and stable. Gin/Echo are heavier with their own context types. Fiber uses `fasthttp` which is INCOMPATIBLE with `net/http` middleware ecosystem and standard streaming primitives — disqualifying for a streaming gateway. Huma is OpenAPI-first which is appealing but pushes validation/reflection into the hot path. Go 1.22 stdlib `ServeMux` is enough for a small surface but lacks middleware composition idioms.
- **Recommendation:** **stdlib `net/http` + `chi`** for routing and middleware. Gateway hot-path handlers (streaming chat completions, retry/fallback, quota mutation) are written directly against `net/http` types; chi handles only routing tree and middleware composition.
- **Risks if adopted:** chi's maintenance pace must be monitored; if abandoned, swap is straightforward (chi has no deep API surface). Need discipline to keep middleware off the hot path where unnecessary.
- **Risks if rejected:** Gin/Echo bind handlers to framework context types, hiding the cancellation and streaming primitives behind another layer. Fiber is structurally wrong for streaming gateway. Stdlib-only loses composable middleware idioms.
- **Confidence:** High
- **Updated:** 2026-04-28

## Codex (Reviewer) view

> Authored via `omc ask codex --agent-prompt critic` (gpt-5.5 + xhigh) 2026-04-28.

- **Critique of Claude's view:** I agree with stdlib `net/http` + chi, but the caveat is non-negotiable: chi is routing glue, not the gateway core. The streaming chat path, cancellation propagation, retry/fallback orchestration, tenant scoping, and usage accounting must remain written against `net/http`, `context.Context`, `http.ResponseWriter`, and controlled transport code. Owner said core algorithms must be optimized; hiding them behind Gin / Echo / Fiber / Huma abstractions is the wrong trade.
- **Production / testability concerns:** The hot path is provider-to-client streaming with cancellation and fallback. Tests must cover client disconnect, upstream timeout, partial stream failure, quota reservation rollback, duplicate billing prevention, disabled account skip, tenant isolation, and race detection. Use `httptest`, fake streaming providers, context cancellation tests, and benchmarks for routing/account-selection. Fiber/fasthttp is specifically a bad fit because standard `net/http` compatibility matters more than synthetic router speed. Huma is useful for OpenAPI-heavy control APIs but risky as the primary gateway framework if it pushes validation/reflection into hot paths.
- **License / dependency concerns:** chi is MIT and small. Gin / Echo / Fiber are permissive but larger framework surfaces. Avoid framework churn after skeleton commit. Any OpenAPI helper must be audited separately; do not let OpenAPI tooling dictate runtime architecture.
- **Recommendation:** **stdlib `net/http` + chi**. Caveats: streaming gateway endpoints stay close to stdlib; chi handles route grouping/middleware only; OpenAPI generation/codegen is a separate DR/tooling choice; benchmark before adding middleware to hot routes.
- **Confidence:** High
- **Updated:** 2026-04-28

## Gemini (UI / Ops) view

> Edited only by Gemini. **No material input** — HTTP framework choice does not affect operations-dashboard UI; Gemini's view will be material for DR-004 (frontend framework, already Decided) but not here.

## Conflicts

> Synthesized by Claude PM.

No material conflicts. Claude and Codex independently converge on **stdlib `net/http` + chi**. Both views agree the framework choice is about discipline: chi is routing glue, hot paths stay on stdlib. Codex's contribution makes the test-coverage requirements explicit (client disconnect, upstream timeout, partial stream failure, quota reservation rollback, duplicate billing prevention, disabled account skip, tenant isolation, race detection) — these fold into the Owner Decision constraints.

## Owner Decision

| Field | Value |
| --- | --- |
| Decision | **stdlib `net/http` + `chi`** for routing and middleware composition. Gateway hot-path handlers written directly against `net/http` and `context.Context`; chi limited to route tree and middleware. |
| Decision date | 2026-04-28 |
| Reasoning | Owner directive "算法要优化，尤其是核心" (2026-04-28). Hot paths must not be hidden behind framework abstractions. Both PM and Codex Reviewer at High confidence on the same recommendation. |
| Constraints attached | (1) Streaming chat completion handlers, retry/fallback orchestration, quota reservation, and tenant scoping written directly on `net/http` and `context.Context`; chi limited to routing tree and middleware composition. (2) `go test -race` baseline required for hot-path tests covering: client disconnect, upstream timeout, partial stream failure, quota reservation rollback, duplicate billing prevention across retry attempts, disabled-Account skip, tenant isolation. (3) **No** Fiber / fasthttp under any circumstance (streaming compatibility). (4) Benchmark every middleware before placing it on hot routes. (5) Framework choice locked: no churn to Gin/Echo/Huma after skeleton commit. (6) Naming aligned to [18_GLOSSARY.md](../../18_GLOSSARY.md) per DR-003 Constraint 8; `golangci-lint.yml` enforces forbidden synonyms. |

## Propagation Checklist

- [ ] Update [13_API_CONTRACTS.md](../../13_API_CONTRACTS.md) — note `net/http` + chi as the runtime; OpenAPI codegen is a separate concern (server stub generation can be hand-coded or assisted; runtime stays on stdlib).
- [ ] Update [16_PHASED_DELIVERY_PLAN.md](../../16_PHASED_DELIVERY_PLAN.md) Phase 3 to require `net/http` + chi skeleton with the test-coverage scaffolding listed above.
- [ ] Mark Status = Implemented when all above are done.
