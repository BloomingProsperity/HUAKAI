# 2026-05-18 receipt sequence P1 fix

| Owner directive | "修 P1: 让 refunded receipt 跟 original 共存 (避免 UNIQUE 冲突)" |
| --- | --- |
| Scope | In: PostgreSQL migration 0033, Go receipt storage/read path, refund worker refunded receipt sequence, receipt canonical payload, focused audit tests. Out: reference project source, frontend, Rust, cost_receipt_handler.go OpenAPI sections, unrelated refactors. |
| Success criteria | Original receipt uses `receipt_sequence = 0`; refunded receipt uses `receipt_sequence = 1`; duplicate same tenant/request/sequence returns `ErrReceiptDuplicate`; `GetReceipt` returns the highest sequence; build and requested Go tests pass or failures are reported honestly. |
| Time estimate | Wall clock 45-90 minutes; agent time one implementation pass plus test triage. |
| Blast radius | `user_cost_receipts` write/read behavior, receipt signing payload compatibility for newly created v2 receipts, refund worker retry behavior, audit/gatewayhttp tests. |
| Failure modes | Migration could leave stale unique constraint in non-standard DBs; mitigation: drop known old constraint and add explicit composite unique index. Existing tests/stubs may omit the new column; mitigation: update focused fixtures only. Signature payload changes require consistent validation; mitigation: include `receipt_sequence` in canonical v2 payload and default original receipts to 0. |
| Decision points | Owner confirmation is needed only if implementation requires changing high-risk areas beyond the authorized schema migration, such as quota enforcement, billing ledger, auth core, or production secrets. |
| Pre-execution checklist | 1. Read project rules and target files. 2. Confirm migration numbering and existing unique pattern. 3. Preserve unrelated dirty worktree changes. 4. Make scoped migration/storage/worker/hash/test edits. 5. Run requested build/test commands. |
| Concrete execution order | Read `receipt_storage.go`, `receipt_storage_pgx.go`, `refund_worker.go`, `receipt_formatter.go`, migrations 0028/0032, then add 0033, patch Go code, add tests, run build and target tests. |

