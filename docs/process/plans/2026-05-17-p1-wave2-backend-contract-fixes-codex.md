# 2026-05-17 P1 Wave 2 Backend Contract Fixes Codex Plan

| Owner directive | "P1 wave 2 (Pools 字段 + Voucher GetBatch + Channel Health list, 3 后端 fix 顺序合做)" |
| --- | --- |
| Scope | In: Go backend handlers, stores, tests, routes, and OpenAPI contract for Pools defaults, Voucher batch detail route, and Channel Health read endpoints. Out: frontend, Rust core_gateway, LICENSE, billing core, F-PAY-001, anti-ban implementation, spec wave, reference project source, new dependencies, and new migrations unless Owner approves. |
| Success criteria | Pools create/update persist and return `top_k_default`, `capability_default`, `allow_last_resort` if existing columns are present; Voucher `GET /v1/admin/vouchers/batches/{batch_id}` is mounted and tenant scoped; Channel Health list/detail endpoints are tenant scoped and redact audit events; requested focused build/tests pass or failures are identified as pre-existing and unrelated. |
| Time estimate | 90-150 minutes wall clock depending on existing test helpers and OpenAPI layout; one Codex agent work unit. |
| Blast radius | Admin API route table, pool persistence contract, voucher admin contract, channel health admin read contract, generated/static OpenAPI schema, focused Go tests. |
| Failure modes | Pools columns may be absent, requiring stop for Owner migration approval; test DB helpers may not cover all packages, requiring narrow in-memory/fake tests; OpenAPI may be split across files, requiring careful schema alignment; route ordering may conflict with existing voucher paths, mitigated by reading current mounts before editing. |
| Decision points | Stop before adding a migration if Pools columns are missing; stop before touching high-risk auth, billing ledger, quota enforcement, production secrets, or deployment files; do not add dependencies. |
| Pre-execution checklist | 1. Read current pool store, handler, migrations, and OpenAPI schema. 2. Confirm Pools columns exist before implementation. 3. Read voucher service/handler/routes and add only the missing mount plus test. 4. Read channel health handler/store/schema and add read-side methods, handlers, routes, tests. 5. Run focused build/tests with `GOCACHE=/tmp/go-cache`. 6. Report diff stat, pass/fail list, risks, and Chinese Owner summary. |
| Concrete execution order | Task A first, then Task B, then Task C, then OpenAPI alignment and verification. |
