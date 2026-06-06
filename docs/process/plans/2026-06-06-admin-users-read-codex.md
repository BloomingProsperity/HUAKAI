# 2026-06-06 Admin Users Read Codex Plan
| Owner directive | "Add admin USER READ endpoints to HUAKAI (branch fix/admin-users-read). Verified real_missing: no /admin/v1/users at all. Reach CLOSURE for the READ-ONLY slice only." |
| Scope | In: `GET /admin/v1/users`, `GET /admin/v1/users/{id}`, `GET /admin/v1/users/{id}/balance-history`, tenant-scoped and admin-authenticated. Out: any user mutation route or balance/role/status/quota write. |
| Success criteria | Routes exist in runtime and OpenAPI, handlers return only the authenticated admin tenant's users/history, pagination and query filter are deterministic, no mutation route is registered, build/vet/targeted tests pass or any blocker is reported honestly. |
| Time estimate | 2-4 hours wall clock; one Codex implementation pass plus verification. |
| Blast radius | New package `backend/internal/adminuserhttp`, admin sqlc query surface, `cmd/gateway/routes.go`, OpenAPI admin path declarations, and focused tests. Frozen packages receive no new files. |
| Failure modes | Cross-tenant leak if SQL or handler omits tenant/user filter; mitigate with integration_pg tests and explicit mutation comments. Global sqlc regen churn; mitigate by adding one query file and checking generated diff. Accidental write surface; mitigate by registering only GET and testing PATCH/POST/DELETE miss. OpenAPI drift; mitigate with route/spec consistency test. |
| Decision points | No Owner sign-off needed unless implementation would require schema changes, auth core changes, billing/quota mutation, new runtime dependencies, or reading reference source. |
| Pre-execution checklist | Confirm worktree branch and dirty state; read admin auth/handler patterns; confirm user/balance/billing table shape from HUAKAI migrations; write failing tests first; add query file and minimal generated code; mount routes; update OpenAPI; run requested checks; stage `backend/` and `docs/` without committing. |

## Execution Order
1. Add tests for tenant-scoped list/detail/history, pagination cap/offset, auth required, and no mutation routes.
2. Add `sql/queries/admin_users.sql` and sqlc-generated admin query methods.
3. Implement `internal/adminuserhttp` read-only handlers with local pagination parsing, tenant derivation from admin identity, and JSON response helpers.
4. Mount `GET /admin/v1/users`, `GET /admin/v1/users/{id}`, and `GET /admin/v1/users/{id}/balance-history` in `cmd/gateway/routes.go`.
5. Add OpenAPI path and schema entries for the three GET routes only.
6. Run targeted red/green tests and final build/vet/unit verification.
