# 2026-06-03 multi-oauth slice1 Codex plan

| Owner directive | "Implement ONLY its first slice ... VERIFY every premise against real code ... HIGH-RISK: AUTH / login / sessions." |
| --- | --- |
| Scope | Implement the gap spec's explicit Wave 1 only: provider normalization constants, pending-email sentinel, endpoint validator export, auth error/provider classification, migration CHECK widening, and focused tests. Do not implement full OAuth provider packages, pending-oauth service logic, route wiring, credential storage, or session core changes. |
| Success criteria | `cd backend && sqlc generate && go build ./... && go vet ./internal/userauth/...` pass. `go test ./internal/userauth/...` plus touched gateway HTTP tests pass. New tests fail under targeted mutations and pass after implementation. |
| Time estimate | 1-2 hours wall clock, mostly test/mutation/verification time. |
| Blast radius | Auth login/OAuth session creation and callback classification; additive schema migration affects `oauth_flow_sessions` and `social_identity_links` provider constraints. No production credential, billing, quota, or session-token persistence changes. |
| Failure modes | Provider slugs could be stored incorrectly; mitigate by normalizing `oidc:<slug>` to `oidc` in tests. Unverified provider emails could become generic 503/403 incorrectly; mitigate by sentinel and handler classification tests. Migration could break rollback; keep additive up, explicit down, and no data mutation outside CHECK widening/new tables. |
| Decision points | Owner must review before landing because this touches auth and schema. I will not add files under frozen `internal/gatewayhttp`, `internal/gateway`, or `internal/proto`; route wiring remains out of scope. |
| Pre-execution checklist | Read `docs/process/gap-specs/multi-oauth.md` fully; verify OAuthProvider interface, PKCE/nonce/state flow, provider normalization, store consumption, gateway auth error routing, current migration max; write RED tests; implement minimal code; mutation-check each required behavior; run requested checks. |

## Concrete execution order

1. Add RED tests in existing `internal/userauth` and `internal/gatewayhttp` test files for provider normalization, OIDC slug normalization, pending-email sentinel, state mismatch staying authoritative, tenant-scoped identity linking, endpoint validator export, safe provider classification, and auth reason classification.
2. Run the focused tests and confirm failures are caused by missing Wave 1 behavior.
3. Implement minimal changes in existing auth files only:
   - `internal/userauth/types.go`
   - `internal/userauth/social_login.go`
   - `internal/userauth/oauth_flow.go`
   - `internal/gatewayhttp/auth_handler.go`
4. Add migration `0081_multi_provider_oauth.{up,down}.sql` if no newer reserved migration exists. Use normalized provider names only and no credential logging/storage changes.
5. Run mutation checks by temporarily removing/changing each behavior and recording the red output, then restore the implementation.
6. Run `sqlc generate`, `go build ./...`, `go vet ./internal/userauth/...`, `go test ./internal/userauth/...`, and touched gateway HTTP tests.

## Scope correction recorded before execution

The task text suggests the first slice is likely one provider package. The read spec's explicit first slice is Wave 1 infrastructure, not a provider implementation. Implementing a provider first would still fail persistence because the current provider CHECK constraints and normalization reject non-Google/GitHub providers. This plan therefore implements Wave 1 only.
