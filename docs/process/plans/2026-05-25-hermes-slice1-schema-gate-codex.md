# 2026-05-25 Hermes Slice 1 Schema Gate Codex Fix

| Field | Plan |
| --- | --- |
| Owner directive | "Round 1 修复 Hermes Slice 1.0 schema gate 的 3 个 S1 findings" |
| Scope | In: migration `0057_hermes_phase1_slice1_core.up.sql`, query `backend/sql/queries/hermes.sql`, and sqlc-generated Hermes bindings. Out: PG round-trip tests, commits, staging, unrelated schema cleanup. |
| Success criteria | Audit tenant and actor FKs use `ON DELETE RESTRICT`; audit correlation lookup is tenant-scoped and backed by a tenant/correlation index; Hermes profiles include nullable `pool_group_id` with tenant-scoped pool-group FK and profile-kind consistency check; `sqlc generate` and `go test ./internal/db/hermes/` complete successfully. |
| Time estimate | 15-25 minutes wall clock; one Codex repair pass. |
| Blast radius | Hermes migration DDL and generated Hermes db bindings. Incorrect edits could break sqlc generation or the Hermes db package compile. |
| Failure modes | SQLC may reject the new FK or nullable column mapping; mitigation: regenerate from SQL and fix generated compile errors only in source SQL/schema. Existing local changes may already be staged; mitigation: do not run `git add` or commit. |
| Decision points | No additional Owner decision expected because the Owner specified exact S1 fixes. Stop if a required fix touches auth core, billing ledger, quota enforcement, secrets, `LICENSE`, or destructive migration behavior outside `0057`. |
| Pre-execution checklist | Confirm current SQL shape; patch only specified migration/query; run `cd backend && sqlc generate`; run `go test ./internal/db/hermes/`; report summary and `git diff --stat` without staging or committing. |

