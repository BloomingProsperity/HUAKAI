# 2026-05-18 F-AUDIT-1-B P2 #3 rate table tenant scope

| Field | Content |
|---|---|
| Owner directive | "修 F-AUDIT-1-B P2 #3: rate_table public lookup tenant 隔离 (避免跨 tenant 串数据)." |
| Scope | In: HUAKAI Go backend pricing rate-table storage/query path, billing pricing migration, focused billing tests. Out: frontend, Rust, `vendor/boring`, reference reverse-proxy source, `cost_receipt_handler.go`, `billing/settler.go`, `observability/billing_persister_handler.go`, new dependencies. |
| Success criteria | `billing_pricing_versions` has explicit `is_public`; public lookup filters `is_public = true`; duplicate public version labels are prevented; private tenant rows with the same version do not affect public lookup; requested Go build and billing tests pass or failures are reported honestly. |
| Time estimate | One focused executor session, roughly 45-90 minutes including build/test. |
| Blast radius | Billing pricing schema and lookup behavior. Existing old rows are backfilled public through the default requested by Owner. No auth, quota, billing ledger, receipt handler, frontend, Rust, or deployment changes. |
| Failure modes | Existing sqlc/generated code may require manual sync if `sqlc` is unavailable; mitigate by reading generated patterns before editing. Partial index creation may fail if existing DBs already contain duplicate public `(version)` rows after default-true backfill; record as Owner confirmation risk if discovered. Tests may depend on fake storage instead of Postgres; mitigate with the narrowest existing package test style. |
| Decision points | Owner already specified the schema change and UNIQUE-public option. Stop only if existing migrations/data model make `DEFAULT true` or partial unique index impossible without destructive data cleanup. |
| Pre-execution checklist | 1. Read pricing source and storage files. 2. Read existing SQL queries and migrations for naming/order. 3. Add migration `0030_billing_pricing_versions_is_public`. 4. Update public lookup SQL/generated query path. 5. Add focused test for public-only lookup. 6. Run requested build/test commands. 7. Report files, PASS/FAIL, risks, and source files read. |
| Concrete execution order | Internal source reads, migration add, query/code update, test add, formatting, build/test verification, final Chinese summary. |

Notes:

- Clean-room constraint: this plan and implementation read only HUAKAI internal files; no non-MIT reference project source is in scope.
- Risk record: database schema change is high-risk by default, but this exact migration is explicitly requested in the Owner directive for this lane.
