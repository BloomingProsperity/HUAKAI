# Module A AUTH-067/068 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: use TDD for every behavior change. This plan is written for HUAKAI-internal clean-room implementation only; do not read non-HUAKAI reference source.

**Goal:** Add Discord social login and Telegram login-widget verification as additive Module A auth providers.

**Architecture:** Discord uses the existing userauth OAuth code flow and generic bearer userinfo mapper. Telegram is a separate `internal/telegramauth` package that verifies login-widget HMAC and maps the verified widget payload into `userauth.VerifiedIdentity`, then calls a new exported userauth entry that reuses the existing social identity linking and signup rules.

**Tech Stack:** Go stdlib crypto/http, existing `internal/userauth`, existing `internal/gatewayhttp` route file, PostgreSQL migrations.

---

| Owner directive | `TASK: 模块A闭环 — Discord 登录(AUTH-067)+ Telegram 登录(AUTH-068)。Branch fix/a-social. HUAKAI-internal,clean-room。全加性。` |
| --- | --- |
| Scope | In: additive Discord OAuth provider, additive Telegram widget verifier/package/handler, provider CHECK migration for `discord` and `telegram`, focused unit/integration tests, OpenAPI route doc if a new HTTP route is mounted. Out: reading reference source, destructive schema changes, quota/billing/auth-core rewrites, git commit. |
| Success criteria | Discord provider can StartOAuth/CompleteOAuth using provider `discord`; userinfo `id` becomes subject; verified email creates/links a user. Telegram widget verifier accepts correct HMAC, rejects tampering and stale `auth_date`; no email means `EmailVerified=false` and the existing pending-email rejection path is preserved. `go build`, `go vet`, and non-integration tests pass. |
| Time estimate | 60-90 minutes implementation plus local checks; integration_pg is named for PM to run because it needs `HUAKAI_DATABASE_URL`. |
| Blast radius | User auth provider normalization, OAuth provider construction, `/v1/auth` routes, provider CHECK constraints, and social identity linking. |
| Failure modes | Discord not normalized means configured provider remains missing; mitigate with StartOAuth/CompleteOAuth mutation test. Bad Discord field mapping can create wrong subject; mitigate with userinfo httptest assertion. Telegram HMAC shortcut can accept tampered params; mitigate with tamper tests. Telegram could bypass email verification; mitigate by asserting `ErrOAuthPendingEmailRequired`/202 handler path. Migration down could reject extant rows; document that down restores previous CHECK and is only safe after removing new provider rows. |
| Decision points | Owner approval is required before implementation because this is non-trivial and includes a schema CHECK migration plus a new public auth route. No high-risk auth-core rewrite, billing/quota change, secrets, or destructive migration is planned. |
| Pre-execution checklist | Confirm no reference-source read. Confirm current branch `fix/a-social`. Preserve existing untracked `TASK.md`. Do not add files under frozen `internal/gatewayhttp`, `internal/gateway`, or `internal/proto`; only additive edits to existing gatewayhttp route file. Use one migration pair `0106_social_login_discord_telegram_provider_check.{up,down}.sql`. Run tests red before production code for each behavior. |

## Understanding Report

- `internal/userauth/social_login.go` owns `SocialProvider*`, provider normalization, OAuth start/callback, and `applyVerifiedSocialIdentity`. Existing behavior rejects identities with missing or unverified email before any create/link path.
- `internal/userauth/oauth_flow.go` owns `exchangeCode` and provider dispatch. Only Google/GitHub/QQ/NodeSeek currently run after token exchange; Discord needs a new dispatch branch to the generic userinfo mapper.
- `internal/userauth/oauth_social_provider_flows.go` owns defaults and `genericUserInfoIdentity`, including dot-path `mapField`, string/bool coercion, synthetic email fallback, and bearer JSON userinfo fetch.
- Production user OAuth wiring is in `cmd/gateway/config.go::buildUserOAuthService`, not `internal/userauth/config.go`. It currently wires Google, GitHub, QQ, DingTalk, NodeSeek.
- `safeSocialProvider` is in frozen package file `internal/gatewayhttp/auth_handler.go`; adding cases inside this existing file is allowed, but no new gatewayhttp file is allowed.
- Current provider CHECK widening is `backend/sql/migrations/0081_multi_provider_oauth.{up,down}.sql`; a new 0106 migration should drop/re-add `social_identity_links_provider_check` and `oauth_flow_sessions_provider_check` with only `discord` and `telegram` added to the current set.
- `internal/telegramauth` does not exist yet, so creating that package is compatible with the frozen-package rule.

## Execution Order

### 1. Discord Red Tests

- Add unit coverage in `backend/internal/userauth/oauth_provider_slice2_test.go`:
  - `TestDiscordOAuthHTTPProviderUsesGenericBearerUserInfo`
  - expected red before implementation: `NewOAuthHTTPProvider` rejects or `ExchangeVerifiedIdentity` returns `ErrOAuthProviderMissing`.
- Add service-level mutation coverage in `backend/internal/userauth/service_test.go` or a new integration_pg file:
  - `TestPGDiscordCompleteOAuthLinksVerifiedEmail`
  - expected red before implementation: `StartOAuth` or `CompleteOAuth` returns `ErrOAuthProviderMissing` when provider normalization lacks `discord`.
- Add `cmd/gateway` config test:
  - update `TestBuildUserOAuthServiceRegistersConfiguredQQDingTalkAndNodeSeek` or add `TestBuildUserOAuthServiceRegistersConfiguredDiscord`
  - expected red before implementation: `svc.Provider(userauth.SocialProviderDiscord)` missing.

### 2. Discord Implementation

- Modify `backend/internal/userauth/social_login.go`:
  - add `SocialProviderDiscord = "discord"`.
  - add `discord` to `normalizeSocialProvider`.
- Modify `backend/internal/userauth/oauth_social_provider_flows.go`:
  - add Discord default authorize/token/userinfo URLs and scopes `identify`, `email`.
  - set generic field defaults: subject `id`, email `email`, verified `verified`, display name `global_name`.
  - make Discord display fallback choose `username` if `global_name` is absent; if a small helper is needed, keep it inside userauth.
- Modify `backend/internal/userauth/oauth_flow.go`:
  - dispatch `SocialProviderDiscord` to `genericUserInfoIdentity`.
- Modify `backend/cmd/gateway/config.go`:
  - add `HUAKAI_DISCORD_OAUTH_CLIENT_ID`, `HUAKAI_DISCORD_OAUTH_CLIENT_SECRET`, `HUAKAI_DISCORD_OAUTH_REDIRECT_URI`, optional endpoint overrides, and optional scopes if existing patterns support scopes.
- Modify `backend/internal/gatewayhttp/auth_handler.go`:
  - add `discord` to `safeSocialProvider`.

### 3. Telegram Red Tests

- Create `backend/internal/telegramauth/telegramauth_test.go`:
  - `TestVerifyWidgetAcceptsValidHMAC`
  - `TestVerifyWidgetRejectsTamperedParam`
  - `TestVerifyWidgetRejectsTamperedHash`
  - `TestVerifyWidgetRejectsExpiredAuthDate`
  - expected red before implementation: package/functions absent.
- Add userauth exported-entry test in `backend/internal/userauth/service_test.go`:
  - `TestApplyVerifiedIdentityRejectsTelegramSyntheticEmailPendingVerification`
  - expected red before export: no public method; after export, expected result is `ErrOAuthPendingEmailRequired`.
- Add gateway handler test in `backend/internal/gatewayhttp/auth_session_handler_test.go`:
  - `TestTelegramLoginWidgetRejectsUnsignedEmaillessIdentityWithPendingEmail`
  - expected red before implementation: route returns 404.

### 4. Telegram Implementation

- Create `backend/internal/telegramauth/telegramauth.go`:
  - `VerifyWidget(params map[string]string, botToken string, now time.Time, maxAge time.Duration) (userauth.VerifiedIdentity, error)`.
  - validate bot token, hash, `id`, and `auth_date`.
  - build data-check-string from all keys except `hash`, sorted lexicographically as `key=value` joined by `\n`.
  - compute secret as `sha256.Sum256([]byte(botToken))`.
  - compute HMAC-SHA256 over the data-check-string and compare provided lowercase hex hash using constant-time comparison.
  - reject stale `auth_date` when `maxAge > 0` and `now.Sub(authDate) > maxAge`.
  - return provider `telegram`, subject as Telegram `id`, synthetic email from a new exported `userauth.SyntheticOAuthEmail`, display from first/last/username, and `EmailVerified=false`.
- Modify `backend/internal/userauth/social_login.go`:
  - add `SocialProviderTelegram = "telegram"`.
  - add `telegram` to normalization.
  - export `ApplyVerifiedSocialIdentity(ctx, tenantID, identity)` as a thin wrapper around the existing unexported method.
  - export `SyntheticOAuthEmail(provider, subject)` as a wrapper or rename with local call updates.
- Modify `backend/internal/gatewayhttp/auth_handler.go`:
  - add Telegram request struct with `tenant_id`, `bot_token` not accepted from body, `params`, and `device_info`.
  - add `TelegramBotToken string` and `TelegramWidgetMaxAge time.Duration` to `AuthHandlerDeps`, or use a small verifier interface if wiring is cleaner.
  - mount `POST /telegram-login` in the existing `MountAuthRoutes`.
  - handler verifies widget with configured bot token, calls `d.Auth.ApplyVerifiedSocialIdentity`, then creates a session only if userauth returns a user. With no email, existing `ErrOAuthPendingEmailRequired` is returned and no session is created.
- Modify production wiring:
  - read `HUAKAI_TELEGRAM_LOGIN_BOT_TOKEN`.
  - set max age to a conservative default such as 24h unless an existing auth TTL pattern suggests a better value.

### 5. Migration

- Add `backend/sql/migrations/0106_social_login_discord_telegram_provider_check.up.sql`:
  - in one transaction, drop and recreate both provider CHECK constraints with current values plus `discord` and `telegram`.
- Add `backend/sql/migrations/0106_social_login_discord_telegram_provider_check.down.sql`:
  - in one transaction, restore both CHECK constraints to current values before 0106.
- Do not alter tables, columns, indexes, tenant scope, secrets, auth core, billing, or quota.

### 6. OpenAPI and Config Docs

- If `POST /v1/auth/telegram-login` is mounted, update `docs/openapi/openapi.yaml` so `TestOpenAPI_ImplementationConsistency` stays green.
- Add `HUAKAI_DISCORD_OAUTH_*` and `HUAKAI_TELEGRAM_LOGIN_BOT_TOKEN` to non-secret example config only if existing auth env examples already list social login variables; otherwise leave runtime-only to avoid example churn.

### 7. Verification Commands

Run from `backend`:

```bash
GOCACHE=/tmp/go-build /usr/local/go/bin/go test -count=1 ./internal/userauth/... ./internal/telegramauth/...
/usr/local/go/bin/go vet ./internal/userauth/... ./internal/telegramauth/...
GOCACHE=/tmp/go-build /usr/local/go/bin/go build ./...
```

Then run the Owner-requested combined command:

```bash
cd backend && GOCACHE=/tmp/go-build /usr/local/go/bin/go build ./... && /usr/local/go/bin/go vet ./internal/userauth/... ./internal/telegramauth/... && /usr/local/go/bin/go test -count=1 ./internal/userauth/... ./internal/telegramauth/... ; echo "integration_pg 由 PM 跑"
```

PM integration_pg test name to run after migration is applied:

```bash
cd backend && GOCACHE=/tmp/go-build /usr/local/go/bin/go test -tags=integration_pg -count=1 ./internal/userauth -run TestPGDiscordCompleteOAuthLinksVerifiedEmail
```

## Clean-Room Notes

- No non-HUAKAI reference source will be read.
- The task text supplies the intended behavior: Discord standard OAuth2 bearer userinfo and Telegram login-widget bot-token HMAC verification.
- Implementation must use local names and local package boundaries; no copied upstream source, schemas, comments, file structure, or tests.
