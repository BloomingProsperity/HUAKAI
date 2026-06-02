# 2026-06-01 S2-011 Session Refresh
| Field | Value |
| --- | --- |
| Owner directive | "Fix audit finding S2-011 to PRODUCTION QUALITY." |
| Scope | In: make `/v1/sessions/refresh` usable when the short-lived session token has expired but the refresh token is still active; preserve revoke/list bearer protection and cross-user refresh rejection when a valid caller bearer is supplied. Out: schema changes, billing/quota changes, new dependencies, broad auth redesign. |
| Success criteria | Regression test fails before the fix and passes after; expired session bearer can refresh with a valid refresh token; protected session routes still reject expired session bearers; relevant Go tests and `go build ./...` pass. |
| Time estimate | 45-75 minutes wall clock; one Codex implementation pass plus review. |
| Blast radius | Auth/session HTTP routing and `usersession.Service.Refresh` caller-identity handling. |
| Failure modes | Accidentally unprotecting revoke/list routes; accepting cross-user refresh when a valid caller bearer is present; breaking refresh replay/expiry semantics; OpenAPI mismatch. Mitigation: discriminating handler test, existing cross-user tests, targeted package tests, build. |
| Decision points | Auth-path behavior change is high-risk but explicitly authorized by Owner task. No schema, billing, quota, or dependency decision is included. |
| Pre-execution checklist | Read audit row; trace `routes.go`, `SessionMiddleware`, `session_handler.go`, `usersession.Refresh/Validate`; read cross-module invariants; read reference source for equivalent refresh handling; claim touched files; add failing test before production code. |
| New files | `backend/cmd/gateway/session_routes_test.go` in package `cmd/gateway`; this package is not frozen. |

## Concrete Execution Order

1. Add a regression test proving refresh after session-token expiry succeeds while another protected session endpoint still rejects the expired bearer.
2. Run the new test and confirm it fails on current code.
3. Split session route mounting so `/refresh` is public to the session middleware and `/revoke`/`/list` remain protected.
4. Let the refresh handler optionally use a valid bearer identity for cross-user abuse detection, but otherwise derive tenant/user from the refresh token itself.
5. Update the OpenAPI security declaration for `/v1/sessions/refresh`.
6. Run mutation check, targeted tests, full build, Codex review, commit, and push.
