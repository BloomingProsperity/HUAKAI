# 2026-05-27 GEM-3 R2 env secret + antigravity pause Codex plan

| Owner directive | "所有 Gemini ClientSecret 来源统一到 env var `HUAKAI_GEMINI_OAUTH_CLIENT_SECRET`; admin API 不再接受 per-request client_secret 字段; 启动时 env var 必须已设; antigravity refresh 临时 fail-closed" |
| Scope | In: existing Gemini credential acquisition wiring, Gemini built-in exchanger secret injection, admin OAuth init behavior, Gemini mode refresh antigravity pause, focused regression tests. Out: antigravity/oauth operator-config path, ChatGPT OAuth, DB schema, billing/quota/auth core, runtime dependencies, commits. |
| Success criteria | Admin Gemini start ignores request `oauth_client.client_secret`; Gemini exchanger stores and posts env-sourced secret; gateway wiring fail-fasts when `HUAKAI_GEMINI_OAUTH_CLIENT_SECRET` is empty; Gemini `AuthModeAntigravity` refresh returns `ErrFeatureDisabled` without HTTP; targeted and requested package tests pass from `backend/`; mutation self-check evidence is recorded. |
| Time estimate | 60-90 minutes wall clock, one Codex session. |
| Blast radius | Medium: OAuth acquisition and refresh paths can block Gemini credential setup/refresh if secret wiring is wrong. No DB/schema/payment/auth/quota changes. |
| Failure modes | Request secret still leaks into stored PKCE payload; startup accepts missing env; antigravity path still calls token endpoint; existing GEM-1/2 tests rely on per-request secret and need fixture updates; dirty worktree changes could be overwritten. Mitigation: TDD tests first, inspect diffs before edits, keep edits in existing files only. |
| Decision points | None expected: Owner already decided env-only secret and antigravity fail-closed. Stop only if a required change needs high-risk files (`LICENSE`, secrets, auth core, billing ledger, quota enforcement, DB schema, deployment scripts). |
| Pre-execution checklist | Read `docs/RULES.md`; inspect dirty worktree; confirm target packages; add failing tests; run red tests; implement smallest change; run focused tests; run requested regression command; run mutation self-check by temporarily reverting the guarded line(s) and proving tests fail; restore implementation; do not commit. |

## File/package scope

- Modify existing `backend/internal/credentialacq/gemini_oauth.go`: add env-sourced secret field/constructor path and force built-in profile config to use the injected secret.
- Modify existing `backend/internal/gatewayhttp/admin_credential_acquisition_handler.go`: keep schema field but do not pass request `client_secret` into Gemini OAuth start config.
- Modify existing `backend/cmd/gateway/wiring.go` and `backend/cmd/gateway/wiring_test.go`: fail-fast on missing `HUAKAI_GEMINI_OAUTH_CLIENT_SECRET` and inject it into Gemini exchangers.
- Modify existing `backend/internal/credentialworker/mode_refresh.go` and tests: replace Gemini antigravity refresh adapter with fail-closed paused adapter.
- Modify existing tests under `backend/internal/credentialacq`, `backend/internal/gatewayhttp`, and `backend/internal/credentialworker` as needed.

Package-structure check: `backend/internal/gatewayhttp` is frozen, but this plan edits an existing file only and creates no new files there. No new runtime dependencies or schema files.

## Execution order

1. Add/adjust tests for env-only Gemini admin/acquisition secret behavior and antigravity fail-closed behavior.
2. Run the focused new tests from `backend/` and confirm they fail for the intended reason.
3. Update Gemini exchanger constructors/config merge to require an injected secret and ignore caller `cfg.ClientSecret`.
4. Update admin OAuth init so Gemini built-in modes do not pass request `client_secret`.
5. Update gateway wiring to read `HUAKAI_GEMINI_OAUTH_CLIENT_SECRET`, fail-fast if blank, and pass it into the Gemini exchanger installer.
6. Replace `gemini/antigravity` mode refresh with a paused fail-closed adapter and add the roadmap decision comment.
7. Run focused tests, then the requested package regression command.
8. Perform mutation self-checks for request-secret bypass and antigravity legacy refresh restoration, then restore implementation and rerun focused tests.
