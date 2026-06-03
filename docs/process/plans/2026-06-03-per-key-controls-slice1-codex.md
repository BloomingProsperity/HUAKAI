# 2026-06-03 Per-Key Controls Slice 1 - Codex Plan

| Owner directive | "Implement the FIRST SLICE of gap `per-key-controls`. Read docs/process/gap-specs/per-key-controls.md fully; implement ONLY its first slice ... New code in NEW packages internal/userkeycontrols (+ internal/userkeycontrolshttp)." |
| Scope | In: per-key quota PUT/GET service and HTTP handlers; key group PUT/GET service and HTTP handlers; additive migration 0079 if needed; sqlc query package; route mount helper file. Out: batch revoke, reveal token, gateway hot-path `KeyGroupID`, auth resolver changes, editing `cmd/gateway/routes.go`, editing frozen packages, wiring the helper into runtime routes. |
| Success criteria | `cd backend && sqlc generate && go build ./... && go vet ./internal/userkeycontrols/... && go test ./internal/userkeycontrols/...` pass; discriminating tests have documented mutation-red evidence; cross-tenant key/group access is structurally scoped by `(tenant_id,user_id)`; committed on current branch only. |
| Time estimate | 2-4 hours wall clock in one Codex session; longer if local PostgreSQL integration dependencies are unavailable. |
| Blast radius | New schema columns and table on `api_keys`/`quota_policies`; new sqlc package; new user-key control service and HTTP package; one new route helper under `cmd/gateway`. No production route wiring in this slice. |
| Failure modes | sqlc may regenerate unrelated files: mitigate by restoring bulk generated noise and staging only requested files. Migration may conflict with future reserved numbers: use explicit Owner-reserved 0079. Tests may require PostgreSQL: report exact availability if local DB is missing. Decimal precision may be lost if converted through float: keep `decimal.Decimal` through DB bind/read. Tenant isolation may regress if queries omit tenant/user predicates: tests assert wrong-tenant writes fail. |
| Decision points | High-risk auth hot-path `KeyGroupID` propagation is intentionally not done in slice 1 because user constrained runtime wiring and frozen-package changes; PM can schedule it as integration. No new runtime dependency will be added. |
| Pre-execution checklist | 1. Read the gap spec fully. 2. Verify schema premises in migrations and sqlc config. 3. Reuse `internal/userkey` and `internal/userkeyhttp` patterns. 4. Write tests before production code. 5. Run RED checks. 6. Implement minimal slice. 7. Run required verification. 8. Mutation-verify tests. 9. Stage only requested files and commit. |

## Concrete Execution Order

1. Add migration `backend/sql/migrations/0079_api_key_controls.{up,down}.sql` for `api_key_groups`, `api_keys.key_group_id`, and `api_keys.quota_policy_id`.
2. Add `backend/sql/queries/userkey_controls.sql` and a sqlc stanza outputting `internal/db/userkeycontrols`.
3. Write `internal/userkeycontrols` tests around quota scope/id/upsert/decimal/linking/no-policy and tenant-isolated group assignment/clear.
4. Run targeted tests and confirm they fail because the package/query implementation is absent.
5. Implement `internal/userkeycontrols` with a pgx-backed service, explicit tenant/user scoping, and no bearer material reads.
6. Add `internal/userkeycontrolshttp` handler tests for missing session, invalid quota/group input, nil service, and not-found mapping.
7. Implement HTTP handlers by copying only needed local helper shapes from `internal/userkeyhttp` and marking copied helpers with `SYNC` comments.
8. Add `cmd/gateway/routes_userkeycontrols.go` with `mountUserKeyControlsRoutes(r chi.Router, d *deps)` only; do not call it from `routes.go`.
9. Run `sqlc generate`, restore unrelated generated churn, run build/vet/tests, and run mutation checks.
10. Stage only requested source/query/generated files, run per-commit review if the local CLI supports it, then commit.
