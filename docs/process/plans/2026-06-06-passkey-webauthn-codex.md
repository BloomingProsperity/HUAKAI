# 2026-06-06 PASSKEY / WebAuthn passwordless login

| Owner directive | "Implement PASSKEY / WebAuthn passwordless login for HUAKAI (branch fix/passkey-webauthn)." |
| Scope | In: migration 0098, new `internal/passkey` service/store/types, new `internal/passkeyhttp` handlers, platform setting keys, gateway wiring, focused unit/migration tests. Out: reading external reference projects, frontend UI, `go get`, `go mod tidy`, commits, production deployment. |
| Success criteria | Passkey registration stores a tenant-scoped credential after server-side challenge validation; discoverable passkey login resolves the credential owner and mints a session through `usersession.Service.Create`; list/delete are user-owned; step-up is required for register/delete; origin/RPID are config-bound; security invariants have discriminating tests. |
| Time estimate | Wall clock: one long Codex implementation pass. Agent time: 4-8 hours equivalent due auth/security and blocked network cache risk. |
| Blast radius | Auth/session path, platform settings allow-list, gateway route wiring, database migrations. Failure can block login, weaken account takeover resistance, or create unusable WebAuthn credentials. |
| Failure modes | Missing go-webauthn transitive modules block local build: do not fetch, record exact missing modules. WebAuthn API mismatch: adapt to public `go doc` only. Step-up ambiguity: fail closed with immediate password/TOTP proof. Sign-count clone path accidentally accepted: compare parsed assertion counter to stored counter before updating. Tenant/user leakage: every store method scopes by tenant and user where applicable. |
| Decision points | Owner/PM should confirm final RP settings values per deployment and run `go mod tidy`, `integration_pg`, and mutation checks. If 2FA-disabled passwordless-only accounts need passkey rotation without password, a separate Owner-approved recovery/step-up policy is needed. |
| Pre-execution checklist | 1. Trace `Authenticate`, session minting, HTTP login, 2FA, platform settings, migration conventions. 2. Do not read `/home/ubuntu/refs` or external reference projects. 3. Keep new files outside frozen packages. 4. Write failing tests before production code where feasible. 5. Do not run `go get` or `go mod tidy`. |

## Execution Order

1. Add 0098 migration for `passkey_credentials` and `webauthn_session`, with tenant/user indexes and rollback guard.
2. Extend `platformsettings` with passkey enable/RP keys and validation for origins JSON/list.
3. Add `internal/passkey` focused files:
   - `types.go`: errors, config, input/output DTOs, WebAuthn user wrapper.
   - `store.go`: store interface plus memory and Postgres implementations.
   - `service.go`: registration/login/list/delete orchestration and sign-count guard.
   - `webauthn.go`: production adapter around `github.com/go-webauthn/webauthn`.
4. Add `internal/passkeyhttp` focused files:
   - route mount and JSON/error helpers.
   - authenticated register/list/delete handlers.
   - unauthenticated login begin/finish handler that calls `usersession.Service.Create`.
5. Wire service/deps/routes in `cmd/gateway` without adding files to frozen packages.
6. Add discriminating unit tests for challenge single-use, sign-count regression, cross-user isolation, step-up requirement, session minting path, and migration SQL shape.
7. Run allowed checks from `backend`: targeted tests, `GOCACHE=/tmp/go-build /usr/local/go/bin/go build ./...`, and `go vet ./...`; record any blocked module-cache failures.

## Assumptions

- The Owner task is the start signal and supplies the approved design; this Codex plan is the required pre-execution artifact, not a request to pause.
- Existing HUAKAI auth responses expose session tokens in JSON, not cookies; passkey login will match that shape.
- Step-up can be implemented as immediate proof in the register/delete request because no reusable recent-verification grant exists today.
- The PM-added WebAuthn dependency is trusted as an explicit project dependency; no external reference project source will be read.
