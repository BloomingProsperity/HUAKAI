# 2026-05-19 provider session reversal assertions

| Field | Content |
|---|---|
| Owner directive | "加强 provider session 反转测试断言 (audit list MED)" |
| Scope | In: `backend/internal/provider/{cursor,copilot,gemini,antigravity,kiro,windsurf}` session adapter tests and low-risk test helpers if needed. Out: reference reverse-proxy source, frontend, Rust, `vendor/boring`, audit, billing, proto, pool, auth core, DB schema, production secrets. |
| Success criteria | Inventory session adapters; add stronger assertions for endpoint, headers, and body encoding where implemented; cover expired-session and upstream-5xx handling as implemented or mark unimplemented vendors with honest skips; run requested backend build and provider/transport race tests. |
| Time estimate | 1-2 wall-clock hours, one Codex executor pass. |
| Blast radius | Low to medium: Go test-only changes should not alter runtime behavior. Risk is false confidence if tests assert placeholders as real vendor behavior. Mitigation: label placeholder/spec gaps explicitly and skip behavior that is not implemented. |
| Failure modes | Existing adapters may only build requests and may not execute upstream calls, so `httptest.Server` response handling/DLQ assertions may be impossible without runtime dispatcher code. Mitigation: add request-construction assertions now, add skip tests for reauth/DLQ paths with precise missing implementation notes. |
| Decision points | Owner confirmation needed only if implementation must change runtime adapter behavior, add dependencies, touch quota/billing/pool/DLQ production code, or read non-MIT reference source. |
| Pre-execution checklist | 1. Do not read sub2api/new-api/other reference reverse-proxy source. 2. Read HUAKAI session adapter code/tests and relevant `docs/specs/`. 3. Preserve unrelated worktree change in `backend/internal/adminhttp/api_keys_handler.go`. 4. Add focused tests only under allowed provider/transport scope. 5. Run requested build/test commands with `GOCACHE=/tmp/go-cache`. |
| Concrete execution order | 1. Inspect six session adapters and existing tests. 2. Inspect HUAKAI specs/plans that define vendor session scaffold status. 3. Add table-driven request assertion helpers/tests per vendor. 4. Add explicit skip tests for expired-session reauth and 5xx DLQ when runtime support is absent. 5. Run `gofmt`, build, and targeted race tests. 6. Report coverage and residual gaps in Chinese with source files read. |

Note: this is the Codex independent plan. No reference project source will be read for this task.
