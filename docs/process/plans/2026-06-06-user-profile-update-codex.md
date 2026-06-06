# 2026-06-06 User Profile Update

| Owner directive | "TASK: Add user self profile UPDATE (display_name) (branch fix/user-profile-update). Verified real_missing: only GET /v1/auth/me read exists, no self-update. Reach CLOSURE. Session-auth, self-scoped. No shortcuts." |
| Scope | IN: session-authenticated `PUT /v1/auth/me/profile` updating existing `users.display_name` only; echo `display_name` in `GET /v1/auth/me`; update HUAKAI-native tests and `docs/openapi/openapi.yaml`. OUT: locale/avatar columns, schema migrations, `/home/ubuntu/refs`, commits. |
| Success criteria | Unit tests prove self scope, validation, persistence through `GET /me`, auth-required behavior, and tenant scope. Build, vet, and targeted package tests pass. OpenAPI declares the new public route. |
| Time estimate | 60-90 minutes wall clock; one Codex implementation pass plus verification. |
| Blast radius | `internal/controlhttp` auth-me handler behavior, `internal/userauth` service/store interface, `cmd/gateway` route wiring, OpenAPI route consistency. |
| Failure modes | Handler trusts request body/query tenant or user IDs; mitigation: tests send spoofed IDs and assert service/store receives session identity. Store update drops tenant predicate; mitigation: service/store tests seed same user ID across tenants and assert only scoped row changes. Validation is weak; mitigation: table tests for empty, too-long, and control-character display names. OpenAPI drift; mitigation: update spec and run gateway consistency tests. |
| Decision points | None expected. High-risk items are explicitly avoided: no migration, no auth-core rewrite, no billing/quota/payment changes, no new dependency, no `LICENSE` edit. |
| Pre-execution checklist | 1. Read `docs/RULES.md`, `internal/controlhttp/panelauth_handler.go`, `internal/controlhttp/notify_handler.go`, `internal/userauth/service.go`, `internal/userauth/store.go`, `cmd/gateway/routes.go`, and `docs/openapi/openapi.yaml`. 2. Confirm `users.display_name` exists in existing migration. 3. Write failing tests before production edits. 4. Implement minimal userauth validation/update/store flow. 5. Wire `PUT /v1/auth/me/profile` beside existing GET `/me`. 6. Update OpenAPI. 7. Run requested build/vet/tests. 8. Stage `backend/` and `docs/` only; do not commit. |

## Implementation Order

1. Add failing tests in `backend/internal/userauth/service_test.go` for `UpdateProfile` validation, user scope, and tenant scope.
2. Add failing tests in `backend/internal/controlhttp/panelauth_handler_test.go` for `PUT /v1/auth/me/profile` self scope, auth required, validation mapping, and `GET /me` returning `display_name`.
3. Implement `userauth.ProfileInput`, `Store.UpdateDisplayName`, service validation, and `PostgresStore.UpdateDisplayName`.
4. Extend `AuthMeDeps` with a profile service/store dependency, add the PUT handler, and have GET read display name through `GetUserByID`.
5. Wire `cmd/gateway/routes.go` to pass `d.userAuth` into `AuthMeDeps`.
6. Update `docs/openapi/openapi.yaml` for `display_name` on GET `/v1/auth/me` and new PUT `/v1/auth/me/profile`.
7. Run formatting and the requested verification commands.
8. Run `git add backend/ docs/` and report staged scope without committing.

## Assumptions And Risks

- Owner start signal is present in the task; this Codex plan is the independent Codex plan artifact. A separate Claude parallel-draft is not available inside this session, so this plan records that limitation rather than blocking low/medium-risk implementation.
- This is HUAKAI-native implementation work only. No reference source will be read.
- Adding `UpdateDisplayName` to the existing `userauth.Store` interface is medium risk because test fakes must compile; it keeps the behavior in the existing auth service boundary and avoids frozen packages.
