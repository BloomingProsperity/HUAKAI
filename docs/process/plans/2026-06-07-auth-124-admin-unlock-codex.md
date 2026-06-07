# 2026-06-07 AUTH-124 Admin Unlock Codex Plan

| Owner directive | "TASK: 模块A闭环 — 管理员解锁被锁账户(AUTH-124)。Branch fix/a-unlock. HUAKAI-internal,clean-room。加性 admin 写 + tenant 围栏 + 审计。" |
| Scope | In: userauth ClearLockout/UnlockUser, admin POST `/admin/v1/users/{id}/unlock`, cmd/gateway route wiring, OpenAPI declaration, focused unit and integration_pg tests. Out: reference source reading, destructive schema, auth core redesign, billing/quota changes. |
| Success criteria | Admin tenant_operator can unlock an existing locked user in its tenant; unknown or cross-tenant user returns 404; unauthenticated/non-admin request is rejected before mutation; audit is attempted for successful unlock; OpenAPI and chi route operations stay in sync; requested non-integration build/vet/test commands pass. |
| Time estimate | 1.5-2.5 wall-clock hours; 1 Codex implementation pass plus verification. |
| Blast radius | User auth store/service, admin user HTTP package, gateway route table, OpenAPI contract, admin audit action whitelist if Owner confirms migration. |
| Failure modes | Missing tenant predicate could unlock another tenant's user; missing admin guard could expose unlock publicly; missing audit action whitelist could make real PG unlock fail; OpenAPI omission could fail cmd/gateway consistency tests; weak tests could pass on SQL no-op. Mitigation: TDD red run, tenant-scoped SQL, preflight `AdminGetUserForTenant`, audit unit assertion, OpenAPI sync test run. |
| Decision points | Owner has explicitly authorized the non-destructive `admin_audit_events.action` CHECK migration for the unlock audit action. No further Owner sign-off is needed unless implementation uncovers destructive schema, auth-core, billing, quota, secret, or deployment changes. |
| Pre-execution checklist | Read `docs/RULES.md` Owner gate; read `internal/userauth/store.go`, `internal/userauth/service.go`, `internal/adminuserhttp/routes.go`, `cmd/gateway/routes.go`, OpenAPI admin user paths, and current admin audit constraints; write failing tests first; do not read reference source; do not commit. |

## Understanding Report

- `internal/userauth/store.go` observed `MarkLoginFailure` increments `failed_login_count` and sets `status='locked'` at threshold, scoped by `tenant_id` and `id`.
- `MarkLoginSuccess` currently clears `failed_login_count` and `locked_until` but does not change `status`, so a locked user remains blocked without a password reset or new admin unlock path.
- `ConsumePasswordResetToken` clears `failed_login_count`, clears `locked_until`, and converts locked/reset/pending statuses to active when the password reset token is consumed.
- `internal/userauth/service.go` blocks login when `status='locked'`, `failed_login_count >= threshold`, or `locked_until` is still in the future; therefore admin unlock must clear the count and locked timestamp, and change locked status to active.
- `internal/adminuserhttp/routes.go` uses `resolveTenant` for admin auth and requires `tenant_operator` with a positive `ScopeTenantID`; platform admin is intentionally rejected for tenant-scoped user reads.
- `AdminGetUserForTenant` already provides a tenant-scoped existence check and returns 404 for cross-tenant user IDs.
- `cmd/gateway/routes.go` mounts `adminuserhttp.MountRoutes` under `/admin/v1/users`; adding the route in `MountRoutes` should make gateway routing additive.
- `adminuserhttp` is not in the frozen package list, but its package comment is stale for a mutation slice and should be updated.

## Execution Order

1. Add failing unit tests in `backend/internal/adminuserhttp/routes_test.go` for `POST /admin/v1/users/{id}/unlock`:
   - happy path requires tenant_operator scope, checks existence through `AdminGetUserForTenant`, calls unlock service with scoped tenant/user, writes audit with `target_type=user`, and returns updated `status`.
   - missing admin credential returns 401 before get/unlock/audit.
   - unknown user returns 404 before unlock/audit.
2. Add failing service/store contract test in `backend/internal/userauth/service_test.go` for `UnlockUser` rejecting non-positive IDs and delegating valid tenant/user IDs.
3. Add failing integration_pg test in `backend/internal/adminuserhttp/routes_integration_test.go` named `TestPGAdminUnlockUser`:
   - seed a verified password user, drive failed login to lockout through `userauth.Authenticate`, call admin unlock, assert `status='active'`, `failed_login_count=0`, `locked_until IS NULL`, and successful password login.
4. Implement `ClearLockout(ctx, tenantID, userID)` in `backend/internal/userauth/store.go` with:
   - `WHERE tenant_id=$1 AND id=$2 AND deleted_at IS NULL`
   - `failed_login_count=0`
   - `locked_until=NULL`
   - `status=CASE WHEN status='locked' THEN 'active' ELSE status END`
   - `updated_at=NOW()`
   - `RETURNING` full user row
5. Add `ClearLockout` to `userauth.Store` and `UnlockUser(ctx, tenantID, userID)` to `userauth.Service` with `tenantID > 0` and `userID > 0` validation.
6. Extend `backend/internal/adminuserhttp/routes.go`:
   - add dependencies for `UnlockUser` and `InsertAdminAuditEvent`
   - mount `POST /{id}/unlock`
   - reuse `resolveTenant`, `pathID`, and `AdminGetUserForTenant`
   - map `ErrUserNotFound`/`pgx.ErrNoRows` to 404
   - return a compact user body including updated status
7. Wire `d.userAuth` and `d.adminQueries` into `adminuserhttp.Deps` in `backend/cmd/gateway/routes.go`; route mounting remains under `/admin/v1/users`.
8. Add OpenAPI path `/admin/v1/users/{id}/unlock` in `docs/openapi/openapi.yaml` with tag `admin-users`, `x-huakai-required-role: tenant_operator`, and 200/401/403/404/503 responses.
9. Add the Owner-authorized non-destructive migration extending `admin_audit_events_action_check` with `unlock_user`. Without this, the meaningful audit write will fail against real PostgreSQL.
10. Run red tests before implementation where practical, then run:
    - `GOCACHE=/tmp/go-build /usr/local/go/bin/go build ./...`
    - `/usr/local/go/bin/go vet ./internal/userauth/... ./internal/adminuserhttp/... ./cmd/gateway/...`
    - `/usr/local/go/bin/go test -count=1 ./internal/userauth/... ./internal/adminuserhttp/... ./cmd/gateway/...`

## Clean-Room Review

This is HUAKAI-internal implementation work. No reference project source will be read. The only upstream-alignment input used is the Owner-provided behavior phrase for admin user status mutation; local names, SQL, tests, and HTTP response shape will follow existing HUAKAI code patterns.
