# 2026-05-19 Group C Review 3 P2 Codex
| Owner directive | "你是 HUAKAI codex executor lane, 任务 = 修 Group C codex review 3 P2." |
| Scope | In: gateway receipt routes, audit receipt validation-state migration, refund worker idempotency guard, focused tests. Out: reference reverse-proxy source, Rust, vendor/boring, proto, pool, billing/settler.go, LICENSE, secrets, auth, quota, billing ledger changes. |
| Success criteria | Slash-containing request IDs resolve receipt and verify routes; `unknown` validation state is accepted by DB schema and covered by test; refund retry after an existing mismatch-refunded receipt is idempotent and covered by test; requested Go build/test commands pass. |
| Time estimate | 45-90 minutes wall clock, one Codex implementer lane. |
| Blast radius | Gateway receipt endpoint matching and audit refund receipt writing. Migration changes one CHECK constraint on `user_cost_receipts.validation_state`. |
| Failure modes | Route regex may shadow verify route: test both GET and POST. Down migration can fail if `unknown` rows exist: document expected rollback precondition. Idempotent guard may hide real write failures: only skip when an existing mismatch-refunded receipt for the same request is observed. |
| Decision points | None expected unless implementation requires high-risk files or a new runtime dependency; both are out of scope. |
| Pre-execution checklist | Read target files only; confirm current migration numbering; implement minimal patch; run focused and requested checks; report residual risk honestly. |

Concrete execution order:
1. Read `backend/cmd/gateway/main.go`, `backend/internal/audit/refund_worker.go`, and relevant SQL migrations/tests.
2. Update receipt routes to allow slash-containing `request_id`.
3. Add migration `0036_user_cost_receipts_unknown_state` up/down.
4. Add refund idempotency guard before appending a refund receipt.
5. Add or update focused tests for all three regressions.
6. Run requested build and test commands.
