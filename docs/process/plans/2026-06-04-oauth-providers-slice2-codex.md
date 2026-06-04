# 2026-06-04 OAuth Providers Slice2 Codex Plan

| Owner directive | "给社交登录加 QQ、钉钉(DingTalk)、NodeSeek 三个 provider...不做微信、不做 LinuxDo...每个 provider 默认关闭...严禁读 /home/ubuntu/refs 等外部参考源码...实现+验证...不要 git commit" |
| Scope | In: backend OAuth provider registration/config, QQ/DingTalk/NodeSeek exchange flows, focused userauth/cmd gateway tests. Out: WeChat/LinuxDo wiring, DB schema changes, auth/session redesign, real credentials, commit. |
| Success criteria | QQ parses querystring and JSON token responses, strips QQ callback-wrapped openid JSON, and uses openid as subject. DingTalk exchanges code via configured POST JSON, reads user profile with access-token header, and uses unionId as subject. NodeSeek is a configurable generic OAuth2/OIDC-style provider with configurable authorize/token/userinfo URL and subject field. Unconfigured providers fail closed. Existing state/CSRF, redirect allowlist, PKCE/nonce, SSRF URL guard, social identity uniqueness, disabled-user gate, and secret encryption paths remain reused. |
| Time estimate | 2-4 hours wall clock in this Codex session, including self-review and gate commands. |
| Blast radius | Authentication/social-login path, provider configuration load, OAuth token/userinfo outbound HTTP behavior, existing Google/GitHub provider registration. |
| Failure modes | Wrong provider subject can link the wrong account; mitigate with discriminating subject tests. Missing state/SSRF guard can create account takeover or SSRF risk; mitigate by using existing StartOAuth/CompleteOAuth and endpoint URL validation tests. Logging secrets/tokens/codes would violate CMB-5; mitigate by no new token logging and scan changed code. Misconfigured providers could register half-open; mitigate by requiring client_id + client_secret + required endpoints. |
| Decision points | Stop for Owner confirmation before any DB schema change, auth/session redesign, real credential handling, new runtime dependency, production deployment, or deletion. NodeSeek endpoint/protocol uncertainty is handled as configurable provider; true production enablement remains parked for Owner OAuth app credentials and endpoint confirmation. |
| Pre-execution checklist | 1. Read CLAUDE.md and AGENTS.md. 2. Use coordination lock before editing. 3. Read HUAKAI OAuth self-code only. 4. Write failing tests before production code. 5. Keep new files out of frozen packages. 6. Run target tests and full requested gate. 7. Do not commit. |

## Clean-Room And Reference Scope

This task intentionally has no external reference-project source scope. The Owner's task text explicitly forbids reading `/home/ubuntu/refs` or other external reference source for this slice and supplies the provider behavior contract directly. This plan therefore records a project-rule conflict with the default triple-mirror research rule and follows the more specific Owner directive for this work unit.

Official provider credentials are not available in this slice. The implementation must make all three providers opt-in through configuration and leave production enablement to Owner-managed OAuth app registration.

## File Plan

- Modify `backend/internal/userauth/oauth_flow.go` for provider constants, registration/exchange routing, and shared provider exchange helpers if the existing file is the local pattern.
- Create focused files under `backend/internal/userauth/` only if needed, for example provider-specific exchange code and tests. `internal/userauth` is not listed as frozen in `AGENTS.md`.
- Modify existing `backend/cmd/gateway/config.go` for provider config/env registration only. This avoids adding files to frozen `gatewayhttp`, `gateway`, or `proto`.
- Add focused tests under existing package paths, preferably `backend/internal/userauth/*_test.go` and `backend/cmd/gateway/*_test.go`.

## Execution Order

1. Inspect `backend/internal/userauth/oauth_flow.go`, `backend/internal/userauth/social_login.go`, `backend/cmd/gateway/config.go`, and credential encryption helpers.
2. Map existing Google/GitHub provider setup, existing HTTP client/test stubs, endpoint validation, and secret storage flow.
3. Add failing userauth tests for QQ token parsing, QQ callback-wrapped openid extraction, DingTalk unionId identity, configurable NodeSeek subject extraction, missing-provider fail-closed, state mismatch rejection, identity collision rejection, disabled user rejection, and SSRF endpoint rejection using discriminating fixtures.
4. Add failing cmd/gateway config tests proving unconfigured providers do not register and configured providers register with encrypted secrets/endpoints.
5. Implement the minimum provider registration and exchange logic to pass tests while reusing existing OAuthService flow and validation primitives.
6. Run focused tests until green.
7. Run requested gate: `cd backend && (sqlc generate>/dev/null 2>&1||true) && go build ./... && go vet ./... && go test ./internal/userauth/... ./cmd/gateway/... 2>&1 | tail -20`.
8. Review changed code for CMB-5 secret/token/code logging, provider subject correctness, and clean-room scope.

## Mutation Evidence To Preserve

- Removing state validation must make the state mismatch test fail before any exchange stub is invoked.
- Taking QQ subject from nickname or DingTalk subject from openId must make subject tests fail.
- Removing endpoint validation must make the internal-address NodeSeek test fail.
- Registering providers without required credentials must make fail-closed tests fail.

## Parked For Owner

- Real QQ/DingTalk/NodeSeek OAuth app registration and client credentials.
- NodeSeek final endpoint contract and exact subject field choice if it differs from configured defaults.
- PM deep review of auth high-risk items before production enablement.
