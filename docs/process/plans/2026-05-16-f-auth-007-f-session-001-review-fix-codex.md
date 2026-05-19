# 2026-05-16 F-AUTH-007 + F-SESSION-001 Review Fix

| Field | Value |
| --- | --- |
| Owner directive | "你是 HUAKAI 项目 codex executor lane, 任务 = F-AUTH-007 + F-SESSION-001 code review fix (4 HIGH security critical + 1 MED + AT coverage)." |
| Scope | Fix first-round scaffold security gaps in HUAKAI-owned user auth/session code: OAuth init/callback proof, session bearer middleware, atomic invite redemption, signed/persisted session tokens, lockout/reset-required enforcement, and focused AT tests. |
| Out of scope | Reference-project source reads; F-CRED-001; F-CH-002; billing; quota; `LICENSE`; production secrets; destructive migration rewrites. |
| Success criteria | OAuth callback ignores client identity claims and requires stored state/nonce/PKCE plus provider exchange; `/v1/sessions/*` uses bearer session context; invite redemption and user creation share one transaction; access session tokens are signed, stored as hashes, and validated; lockout/reset-required paths block login; targeted Go tests pass. |
| Time estimate | One Codex executor session; full production OAuth smoke with real Google/GitHub tenants remains operator-configured follow-up. |
| Blast radius | Medium/high for auth/session code: route wiring and migrations are touched, but changes remain within HUAKAI platform user auth/session boundaries. |
| Failure modes | OAuth provider misconfiguration; token verifier too test-hostile; transaction wrapper incompatibility with in-memory tests; middleware trusting body tenant/user; stale cache accepting revoked session; test-only scaffolds masking real SQL behavior. |
| Mitigations | Keep OAuth provider behind explicit config and interface; use `golang.org/x/oauth2` only; validate claims from provider verifier only; pass tenant/user through context; add DB store methods for access tokens; keep in-memory store feature-equivalent for unit tests; run focused and broad package tests. |
| Decision points | Owner/operator must supply production OAuth client ids/secrets, redirect URIs, and session HMAC secret before real deployment. Social signup policy defaults are conservative in code and can be made tenant-configurable later. |
| Pre-execution checklist | Read rules, current F-AUTH/F-SESSION specs, AT matrix rows, existing scaffold files, migrations, route wiring, Go module dependencies, and confirm no reference-project source reads. |

## Concrete Execution Order

1. Extend migration 0020/0021 with `oauth_flow_sessions`, `social_identity_links`, `invite_bindings`, and `session_tokens`.
2. Add auth flow store APIs and transaction wrapper; make registration redeem invite, create user, invite binding, and verification token inside one transaction when possible.
3. Replace body-claim social callback with OAuth init/callback flow using provider exchange plus verified provider claims.
4. Sign, hash, persist, validate, revoke, and expire platform session tokens.
5. Add bearer session middleware under `internal/auth` and wire `/v1/sessions/*`.
6. Enforce login lockout and reset-required status.
7. Add focused unit/handler tests for the requested AT rows.
8. Run `go test` for affected packages, then broader backend tests if time allows.

Source files read: docs/RULES.md; docs/specs/user-authentication.md; docs/specs/session-management.md; docs/11_ACCEPTANCE_TEST_MATRIX.md; docs/process/plans/2026-05-16-f-auth-007-f-session-001-implementation-codex.md; backend/internal/gatewayhttp/auth_handler.go; backend/internal/gatewayhttp/session_handler.go; backend/internal/userauth/social_login.go; backend/internal/userauth/service.go; backend/internal/userauth/store.go; backend/internal/userauth/types.go; backend/internal/usersession/rotation.go; backend/internal/usersession/store.go; backend/internal/usersession/types.go; backend/internal/usersession/invalidation.go; backend/internal/usersession/anomaly.go; backend/cmd/gateway/main.go; backend/sql/migrations/0020_user_authentication.up.sql; backend/sql/migrations/0021_session_management.up.sql; backend/go.mod
Lane: implementer
Agent: Codex GPT-5
UTC timestamp: 2026-05-16T08:36:31Z
