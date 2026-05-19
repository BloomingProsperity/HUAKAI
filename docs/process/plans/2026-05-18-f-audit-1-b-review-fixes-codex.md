# 2026-05-18 F-AUDIT-1-B Review Fixes Codex

| Field | Content |
|---|---|
| Owner directive | "你是 HUAKAI codex executor lane, 任务 = 修 F-AUDIT-1-B codex review 2 P1 + 1 P2." |
| Scope | Fix three Go backend review findings: wire successful billing settlement to receipt persistence, harden detached receipt verification canonical fields, and add explicit public scope for pricing rate-table lookup. Out of scope: reference reverse-proxy source reads, frontend, Rust, vendor/boring, control plane, new dependencies, auth-core rewrites, billing-ledger redesign, quota enforcement changes, and production secrets. |
| Success criteria | Real successful billing flow writes a receipt retrievable by `GET /v1/receipts/{request_id}`; tampering `validation_state`, `verdict`, `adjustment_refs`, or canonical hash makes verification fail; public pricing lookup only returns rows marked public; migration backfills existing pricing versions as public; requested build and targeted Go tests pass or failures are reported honestly. |
| Time estimate | One focused executor session, approximately 2-4 hours depending on existing test harness readiness. |
| Blast radius | Billing settlement completion path, audit receipt formatter/storage integration, gateway receipt verification handler, billing pricing storage queries, migration chain, and targeted backend tests. |
| Failure modes | Settlement interfaces may lack enough data to derive receipts; mitigate by reading HUAKAI settler/persister code before editing and prefer a narrow post-settle hook. Asynchronous outbox may be unavailable for this event shape; mitigate with non-blocking synchronous fallback that logs receipt failures without reversing settled usage. Schema migration may require query updates; mitigate by filtering public lookup on the new flag and backfilling existing rows. |
| Decision points | Owner has explicitly selected option A for `is_public` migration in this prompt. Stop only if the implementation would require changing auth core, billing ledger semantics, quota enforcement, real secrets, destructive migrations, or new runtime dependencies. |
| Pre-execution checklist | 1. Read HUAKAI backend billing settler and observability billing persister. 2. Read HUAKAI audit receipt formatter/storage APIs. 3. Read HUAKAI gateway receipt handler verification logic. 4. Read HUAKAI billing rate table source/storage and existing migrations. 5. Patch the smallest Go/backend SQL surface. 6. Add tests for real receipt persistence, tamper verify failure, and public pricing scope. 7. Run requested build and tests. |
| Concrete execution order | Plan artifact, source reads, receipt hook design, canonical payload hardening, pricing migration/query patch, targeted tests, build/tests, Chinese completion report. |

Clean-room note: implementer lane reads only HUAKAI source/specs in this work unit and does not read reference reverse-proxy source.
