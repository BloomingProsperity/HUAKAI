# 2026-06-07 AUTH-086 AUTH-140 Codex Plan

| Owner directive | "模块A闭环 — 社交解绑(AUTH-086)+ 专用 logout 端点(AUTH-140)。Branch fix/a-acct. HUAKAI-internal,clean-room。全加性。" |
| Scope | In: userauth store/service unlink behavior, self-service auth binding delete route, admin user binding delete route, controlhttp logout route, discriminating unit tests and integration_pg tests. Out: destructive schema changes, reference-source reads, commits, production secrets, billing/quota/auth-core rewrites. |
| Success criteria | `DELETE /v1/auth/account-bindings/{provider}` unlinks the caller's own social identity unless it is the last login method; admin `DELETE /admin/v1/users/{id}/account-bindings/{provider}` uses the same userauth service and tenant admin authorization; `POST /v1/auth/logout` revokes only the caller's current session family and returns `{"revoked":n}`. Requested build/vet/test commands complete or failures are recorded truthfully. |
| Time estimate | 1 focused Codex session, roughly 60-120 minutes depending on existing test wiring and build state. |
| Blast radius | Auth route behavior, userauth Store interface implementers, admin user route interface, controlhttp auth route dependencies, session revocation handler tests. Frozen `gatewayhttp` gets no new files; production route wiring is an additive `cmd/gateway/routes.go` edit. |
| Failure modes | Store interface change may break test stubs; mitigate by updating local stubs additively. Route path ambiguity may collide with existing auth route; mitigate with exact chi path tests. Last-login-method guard may be weak if it only checks `users.social_login_provider`; mitigate by counting `social_identity_links` rows by tenant/user. Logout may revoke a wrong family if body/query is trusted; mitigate by no body parsing and assertions on `SessionIdentity.FamilyID`. |
| Decision points | No destructive schema changes. No `LICENSE` change. No new runtime dependency. If implementation requires auth core, billing ledger, quota enforcement, DB schema migration, or production secret edits, stop for Owner confirmation. |
| Pre-execution checklist | Confirm branch/worktree; read relevant HUAKAI-internal code only; write failing tests before production code; keep gatewayhttp frozen rule by not adding new files; update only low/medium-risk implementation and test files; run requested commands and report `integration_pg` test names for PM. |

## Concrete Execution Order

1. Add userauth service tests for social unlink success and last-login lockout, plus memory store unlink/count support in tests only as needed.
2. Add Postgres `integration_pg` tests proving unlink deletes `social_identity_links` and lockout blocks no-password single-link users.
3. Implement `ErrLastLoginMethod`, store unlink/count helpers, and `Service.UnlinkSocialIdentity`.
4. Add self-service HTTP test for `DELETE /v1/auth/account-bindings/{provider}` using session context, then add route/handler in `controlhttp.MountAuthMeRoutes` so it is mounted under the existing session-authenticated `/v1/auth` route group.
5. Add admin HTTP test for `DELETE /admin/v1/users/{id}/account-bindings/{provider}` with tenant scope, then add route/handler in `adminuserhttp`.
6. Add controlhttp logout tests for current family revocation and no-session rejection, then implement `MountAuthMeRoutes` logout wiring.
7. Add `integration_pg` logout test creating live session tokens with `usersession.PostgresStore`, calling `POST /v1/auth/logout`, then asserting the old session and refresh token fail.
8. Run focused tests after each red/green cycle, then run the Owner-requested build/vet/test command set.

## Clean-Room Notes

This is HUAKAI-internal implementation. The task names sub2api route shapes only as product behavior alignment; no reference project source will be read or copied.
