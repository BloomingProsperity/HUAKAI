# 2026-05-19 Group C Review 1 Refund Gateway Fixes
| Owner directive | "你是 HUAKAI codex executor lane, 任务 = 修 Group C review 1 P1 + 1 P2." |
| Scope | In: backend Go audit refund worker/storage, gateway receipt routes, focused Go tests. Out: reference reverse-proxy source, frontend, Rust, vendor/boring, LICENSE, schema, auth, billing ledger, quota enforcement. |
| Success criteria | Refund DLQ retry reuses an existing refund receipt by `(request_id, refund_idempotency_key)` and does not append a second receipt; gateway receipt endpoints accept request IDs containing one slash such as `host/random-000001`; requested Go build/test commands pass or failures are reported truthfully. |
| Time estimate | 45-75 minutes wall clock; one Codex implementation lane. |
| Blast radius | Audit receipt completion path and gateway receipt route matching. A bad change could duplicate refunds, miss completion marking, or route unintended receipt paths. |
| Failure modes | Existing dirty files may contain related work: read before editing and patch against current state. Storage interface mismatch may break tests: update all in-memory/mock implementations. Catch-all route may swallow verify subroutes: keep verify registered before wildcard or use exact wildcard patterns that preserve endpoint boundaries. |
| Decision points | Stop for Owner confirmation only if implementation requires schema changes, new runtime dependencies, auth/billing/quota core changes, deleting files, or touching secrets/LICENSE. |
| Pre-execution checklist | Read current files and tests; inspect local chi v5 route docs from module cache or package tests; locate receipt storage interface implementations; add focused tests; run requested backend build and test commands. |

## Concrete Execution Order

1. Read `backend/internal/audit/refund_worker.go`, `receipt_storage.go`, `receipt_storage_pgx.go`, and current refund tests.
2. Read `backend/cmd/gateway/main.go` receipt routes and gateway/cmd tests.
3. Read local chi v5 routing documentation or tests for wildcard/catch-all behavior.
4. Add `GetByRefundIdempotency` to receipt storage and PGX implementation.
5. Update `processOnce` to lookup existing refund receipt before append and mark completed on hit.
6. Change receipt routes to support slash-bearing request IDs without letting verify accept extra suffixes.
7. Add focused P1 and P2 regression tests.
8. Run the requested Go build/test commands and report exact results.
