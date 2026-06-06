# 2026-06-06 vendor catalog CRUD Codex plan

| Owner directive | "TASK: Add provider/vendor catalog CRUD to HUAKAI (branch fix/vendor-catalog-crud). Verified partial: GET /admin/v1/providers is read-only; no create/update/delete of vendor catalog entries. Reach CLOSURE. Admin-gated, tenant-scoped, audited. No shortcuts." |
| --- | --- |
| Scope | Add HUAKAI-native CRUD for existing `providers` rows under `/admin/v1/providers`; update sqlc queries, `internal/adminhttp` handlers/tests, `cmd/gateway` route wiring, and `docs/openapi/openapi.yaml`. No reads from `/home/ubuntu/refs`; no migration unless new columns are added; no commit. |
| Out of scope | Brand icon/description columns, `models` catalog editing, provider account CRUD, billing/quota/auth-core changes, runtime dependencies, production deployment. |
| Success criteria | POST creates tenant-scoped provider rows; duplicate active code in same tenant returns conflict; same code in a different tenant is allowed; PUT updates display name/protocol/enabled by tenant+code; DELETE refuses active provider-account references and otherwise soft-deletes/disables; non-admin or cross-tenant mutation is rejected; admin audit row is written for successful mutations; OpenAPI includes every new public route; focused tests/build/vet run locally. |
| Time estimate | 2-4 hours wall clock; 1 Codex implementation session. |
| Blast radius | Admin provider catalog only. SQL changes are limited to new queries over existing `providers` and `provider_accounts` columns. Route wiring touches `cmd/gateway/routes.go` but not frozen packages. |
| Failure modes | Weak tests miss tenant scope; duplicate handling maps DB error to 503 instead of 409; delete leaves active account references; audit failure leaves mutation without audit; OpenAPI consistency fails; sqlc generation changes unrelated files. Mitigation: write discriminating tests first, use tenant+code WHERE clauses, guard active accounts in SQL, wrap mutation+audit in one transaction via an `internal/adminhttp` adapter, inspect generated diff. |
| Decision points | Owner confirmation would be required for a migration, new dependency, auth-core/billing/quota/schema changes, or deleting files. None are currently planned. |
| Pre-execution checklist | 1. Read `provider_catalog_handler.go`, admin provider account mutation sqlc pattern, `providers` schema, route wiring, provider-account FK. 2. Confirm no `/home/ubuntu/refs` access. 3. Confirm no frozen-package new files. 4. Write failing tests before production code. 5. Generate sqlc after query edits. 6. Update OpenAPI. 7. Run focused tests, build, and vet. |

## Target files

- Modify `backend/sql/queries/admin_provider_channel_catalog.sql`: add provider catalog mutation and guard queries.
- Regenerate `backend/internal/db/admin/*.sql.go` and `backend/internal/db/admin/querier.go` via sqlc.
- Modify `backend/internal/adminhttp/provider_catalog_handler.go`: keep list helpers and add shared request/response helpers as needed.
- Create `backend/internal/adminhttp/provider_catalog_mutation_handler.go`: mount POST/PUT/DELETE and implement admin-gated, tenant-scoped, audited handlers.
- Modify `backend/internal/adminhttp/provider_catalog_handler_test.go`: add handler-level auth, validation, audit, conflict, guard, and tenant-isolation tests with mutation comments.
- Modify `backend/internal/db/admin/provider_channel_catalog_integration_test.go`: add integration_pg SQL query tests for create/update/delete guard.
- Modify `backend/cmd/gateway/routes.go`: mount POST/PUT/DELETE provider catalog routes using an `adminhttp` store adapter with `pgPool`.
- Modify `docs/openapi/openapi.yaml`: document POST `/admin/v1/providers` and PUT/DELETE `/admin/v1/providers/{code}`.

## Execution order

1. Add failing handler tests for create/update/delete/admin auth/tenant isolation/audit behavior.
2. Add failing integration_pg query tests for tenant-scoped uniqueness, update not-found, and guarded soft delete.
3. Add sqlc queries over existing columns only: insert, update-returning, active-account guard count, soft-delete-returning.
4. Regenerate sqlc.
5. Implement `adminhttp` mutation handlers and transaction-backed audit adapter.
6. Wire routes in `cmd/gateway/routes.go`.
7. Update OpenAPI.
8. Run focused tests, then requested build/vet commands. PM owns `integration_pg` socket run; local non-integration tests and build/vet still run here.

## Clean-room note

This is HUAKAI-native implementation against local schema and local behavior tests. No non-MIT reference source will be read or copied.

## Execution note

While regenerating sqlc for the admin provider queries, the current generator also refreshed other generated packages and surfaced a compile-time type mismatch in usage analytics day-bucket projections. The support fix was limited to explicit `::timestamptz` casts in `backend/sql/queries/usage_analytics.sql` plus the existing SQL filter test expectations in `backend/internal/db/billing/sql_filters_test.go`; it does not change the bucket semantics.
