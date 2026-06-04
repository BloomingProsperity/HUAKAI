# 2026-06-04 OAuth State Cookie And Provider Error Hardening

| Owner directive | "追加两处安全/可观测硬化...OAuth state 绑浏览器 cookie...provider 上游错误保留...不要 git commit...严禁读 /home/ubuntu/refs" |
| Scope | Append hardening on top of existing slice-2 OAuth provider work. In scope: HUAKAI-owned OAuth start/callback handlers, HUAKAI-owned OAuth provider flow code, focused discriminating tests, requested backend gates. Out of scope: commits, reference-project source reads, production credential setup, provider registration defaults beyond current slice-2 behavior. |
| Success criteria | OAuth start sets a short-lived HttpOnly/Secure/SameSite=Lax state cookie scoped to `/v1/auth/oauth-callback`; callback rejects missing/mismatched cookie state before token exchange and clears the cookie on accepted callback; QQ/DingTalk/generic provider upstream errors log provider/status/errcode/errmsg only; tests fail if cookie validation or errcode logging is removed, and fail if sensitive token/secret/code/openid values appear in logs. |
| Time estimate | 60-90 minutes wall clock; one Codex implementation pass plus verification. |
| Blast radius | Auth callback behavior: clients must preserve the state cookie between init and callback. Provider flow observability: internal logs gain sanitized provider error details. No database, billing, quota, deployment, license, or real-secret changes. |
| Failure modes | Cookie path/name mismatch could block legitimate callbacks; mitigate with tests against mounted route path. Clearing too early could break retries; clear only after cookie-state match, before server-side completion consumes state. Logging raw responses could leak secrets; log only parsed errcode/errmsg/status/provider and assert secret/token/code/openid sentinels are absent. Provider error parsing could alter success flows; keep success parsing intact and run existing provider tests. |
| Decision points | Owner/PM deep review required before real rollout because auth is high-risk. Real OAuth application credentials and production Secure-cookie deployment remain Owner-controlled. No mid-flight Owner decision is needed unless existing code requires changing auth core semantics beyond the specified additive guard. |
| Pre-execution checklist | Read `CLAUDE.md`, `AGENTS.md`, `docs/RULES.md`, and `.coordination/README.md`; confirm no live edit conflict; claim target files; read only HUAKAI-owned OAuth handler/state/provider code; write failing tests before production edits; do not add files under frozen `backend/internal/gatewayhttp`; do not read `/home/ubuntu/refs`; do not commit. |

## File Scope

- Modify existing frozen-package files only:
  - `backend/internal/gatewayhttp/auth_handler.go`
  - `backend/internal/gatewayhttp/auth_session_handler_test.go`
- Modify existing non-frozen userauth files:
  - `backend/internal/userauth/oauth_flow.go`
  - `backend/internal/userauth/oauth_social_provider_flows.go`
  - `backend/internal/userauth/oauth_provider_slice2_test.go`
- No new `gatewayhttp`, `gateway`, or `proto` files.

## Concrete Execution Order

1. Claim code/test files via `.coordination/claim.sh`.
2. Add gatewayhttp RED test for state cookie attributes, mismatch rejection before provider exchange, match success, and cookie clearing.
3. Run the focused gatewayhttp test and confirm it fails for missing cookie behavior.
4. Add userauth RED test for sanitized provider upstream error logging across QQ, DingTalk, and generic provider paths.
5. Run the focused userauth test and confirm it fails for missing errcode/errmsg logs.
6. Implement cookie helper functions in `auth_handler.go` and wire them into OAuth init/callback without replacing server-side state validation.
7. Implement provider-error parsing/logging helpers in `oauth_social_provider_flows.go` without logging access tokens, client secrets, auth codes, open IDs, raw request bodies, or raw response bodies.
8. Run focused tests until green.
9. Run the requested backend gate:
   `cd backend && (sqlc generate>/dev/null 2>&1||true) && go build ./... && go vet ./... && go test ./internal/userauth/... ./internal/gatewayhttp/... ./cmd/gateway/... 2>&1 | tail -20`
10. Leave all changes uncommitted and report file:line evidence, mutation evidence, CMB-5 self-check, clean-room self-check, and gate result in Chinese.

## Clean-Room Note

This is Codex implementer-lane work from PM-provided behavior spec plus HUAKAI-owned code. `REFERENCE PROJECTS IN SCOPE`: none for this Codex implementation pass. The explicit Owner/PM constraint for this task forbids reading `/home/ubuntu/refs`; no upstream source, tests, schemas, file structures, comments, or identifiers will be read or copied.
