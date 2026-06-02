# Feature-Tree Audit: accounts-auth

**Domain summary:** HUAKAI has a strong multi-tier auth core (API key resolver, session service, user-auth service, admin token RBAC, social OAuth) with well-designed security primitives; the primary commercial gaps are absent MFA/TOTP, no admin user-management API (explicitly deferred in code comments), no per-key scoping/IP-allowlist, no authenticated self-service password change, and no enterprise SSO (SAML/LDAP/SCIM).

Audit date: 2026-06-02. Reviewer: Claude PM-Orchestrator (read-only, no code changes). Evidence verified by direct file reads and targeted `grep` across `backend/`.

---

## Feature Table

| # | Feature | Status | Evidence (file:line or search) | Gap Note |
|---|---------|--------|-------------------------------|----------|
| **Registration & Identity** |||||
| 1 | User registration (email + password) | PRESENT | `internal/userauth/service.go:75` `Register()` | Full flow with optional invite-code enforcement |
| 2 | Email verification flow (token, consume, activate) | PRESENT | `internal/userauth/email_verify.go:75` `VerifyEmail()`; migration `0020:email_verification_tokens` | token_hash stored as bytea, consumed_at prevents replay |
| 3 | Registration modes: open / invite_required / disabled | PRESENT | `cmd/gateway/release_mode_test.go:94`; `userauth.RegistrationMode{Open,InviteRequired,Disabled}` | Production fail-closed: unset env ⇒ Disabled (S2-012) |
| 4 | Invite-code gated registration | PRESENT | `0020_user_authentication.up.sql:invite_codes`; `userauth.RegisterInput.InviteCode` | max_uses, used_count, valid_until, exhausted/disabled states |
| 5 | Social login – Google OAuth2 | PRESENT | `internal/userauth/oauth_flow.go`; `internal/userauth/social_login.go:67` `StartOAuth()` | PKCE + HTTPS-only endpoint guard; JWKS verification |
| 6 | Social login – GitHub OAuth2 | PRESENT | `internal/credentialacq/oauth.go`; `userauth.Service.StartOAuth` provider "github" | read:user + user:email scopes |
| 7 | Social identity linking (provider subject → user row) | PRESENT | `0020:social_identity_links`; PK `(tenant_id, provider, subject)` | FK to users (tenant_id, id) prevents cross-tenant binding |
| 8 | Social identity unlink / merge endpoint | MISSING | grep `unlink`, `delink`, `unmerge` → no hits in handlers | sub2api provides `/api/user/oauth` DELETE; gap |
| 9 | PKCE verifier encryption at rest | PRESENT | `0024_encrypt_pkce_verifier_at_rest.up.sql`; `userauth/store.go:754 encryptPKCEVerifier()` | AES-GCM envelope; keys from credentialKeys |
| 10 | User profile update (display_name, email) | MISSING | No `PUT /v1/users/me` or `PATCH /v1/users/me` in `routes.go`; no `UpdateProfile` in `userauth/service.go` | new-api / sub2api both expose `/api/user` PUT; gap |
| 11 | Account self-deletion | MISSING | No `DELETE /v1/users/me`; `userauth` has no `DeleteSelf()` | Compliance/GDPR risk |
| **Login & Session** |||||
| 12 | Password login | PRESENT | `internal/userauth/service.go:151` `Authenticate()` | Constant-time argon2id verify; failed-login counter increments |
| 13 | Account lockout after failed attempts | PRESENT | `internal/userauth/email_verify.go:18` `DefaultLockoutThreshold=5`; `store.go:158 MarkLoginFailure()` | `locked_until` set on threshold breach |
| 14 | Admin-side account unlock | PARTIAL | `users.locked_until` column exists; no `/admin/v1/users/{id}/unlock` endpoint found | Can be worked around by updating row directly but no API surface |
| 15 | Session token issuance (short-lived JWT, 15 min) | PRESENT | `internal/usersession/rotation.go:40` `Create()`; `SessionTTL=15m default` | HMAC-SHA256 signed payload |
| 16 | Refresh token with rotation (30-day, per-family) | PRESENT | `rotation.go:76` `Refresh()`; `MaxActiveFamilies`; token rotated on use | SHA256 hash stored; plaintext never persisted |
| 17 | Refresh-token replay detection + family revocation | PRESENT | `rotation.go`; `ErrRefreshReplay`; `FamilyStatusRevoked` on race | Concurrent request race → revoke-all-family |
| 18 | Session anomaly detection (IP + User-Agent drift) | PRESENT | `internal/usersession/anomaly.go`; `DetectDrift()` | DriftHigh triggers revocation |
| 19 | Device limit enforcement | PRESENT | `usersession.ErrDeviceLimitExceeded`; `MaxActiveFamilies`; `ErrDeviceConfirmationRequired` | Device policy configurable per Service |
| 20 | Session listing | PRESENT | `internal/gatewayhttp/session_handler.go:76` `newSessionListHandler()`; `POST /v1/sessions/list` | Returns session families for current user |
| 21 | Session revocation (single family or all) | PRESENT | `session_handler.go:97` `newSessionRevokeHandler()`; `Sessions.Revoke()` | Ownership check prevents revoking other users' sessions |
| 22 | Session me endpoint (who am I) | PRESENT | `internal/panelauthhttp/handler.go`; `GET /v1/auth/me` with SessionMiddleware | Returns panel role + user identity |
| 23 | Dedicated logout endpoint | PARTIAL | No `POST /v1/auth/logout`; revocation is via `POST /v1/sessions/revoke` (requires knowing the family ID or passing token) | Ergonomic gap: clients must call revoke; sub2api/new-api expose `/api/user/logout` as a distinct named action |
| **Password Management** |||||
| 24 | Password hashing (Argon2id) | PRESENT | `internal/userauth/password.go:32` `HashPassword()`; `argon2.IDKey(mem=64KiB×1024, iter=3, par=1, salt=16, key=32)` | constant-time verify via `subtle.ConstantTimeCompare` |
| 25 | Password reset via email token (unauthenticated) | PRESENT | `service.go:256 ResetPassword()`; `0020:password_reset_tokens`; token hash + password_version binding | Token version prevents old-token reuse after password change |
| 26 | Authenticated password change (logged-in user) | MISSING | No `POST /v1/users/me/password`; no `ChangePassword()` in `userauth/service.go` | sub2api `PUT /api/user/password`; new-api `PUT /api/user/self`; gap for SaaS UX |
| 27 | Password complexity policy (length, character classes) | PRESENT | `internal/userauth/service.go:47` `PasswordPolicy`; `DefaultPasswordPolicy()` | Configurable min-length, complexity rules |
| **API Key Management (Inbound)** |||||
| 28 | User self-service API key issuance | PRESENT | `internal/userkey/userkey.go`; `POST /v1/api-keys` via `userkeyhttp.MountUserAPIKeyRoutes()` | Plaintext returned once; prefix only stored after |
| 29 | Admin API key issuance | PRESENT | `internal/adminhttp/api_keys_handler.go`; `POST /admin/v1/api-keys/` | bcrypt hash; `hk_live_` / `hk_test_` prefixes |
| 30 | API key listing (user view) | PRESENT | `internal/userkey/userkey.go`; `GET /v1/api-keys`; prefix-only, no plaintext | paginated (50/200 per page) |
| 31 | API key revocation (user) | PRESENT | `internal/userkey/userkey.go`; `DELETE /v1/api-keys/{id}` | Ownership verified against SessionIdentity |
| 32 | API key revocation (admin) | PRESENT | `internal/adminhttp/api_keys_handler.go`; `DELETE /admin/v1/api-keys/{id}` | role-scoped: tenant_operator can only revoke own-tenant keys |
| 33 | API key expiry (expires_at) | PRESENT | `0007:api_keys.expires_at`; resolver checks `expires_at`; background sweep index exists | expiry sweep worker index present; worker not wired (Phase E) |
| 34 | API key environments (live / test) | PRESENT | `internal/userkey/userkey.go`; `EnvLive`/`EnvTest`; `hk_live_`/`hk_test_` prefix | EnvAdmin rejected for user-issued keys |
| 35 | Per-user active key cap | PRESENT | `internal/userkey/userkey.go:53` `MaxActiveKeysPerUser=20` | Advisory lock prevents race condition at cap boundary |
| 36 | API key name / label | PRESENT | `0007:api_keys.name`; `IssueRequest.Name`; `MaxNameLen=128` | |
| 37 | API key scopes / allowed models | MISSING | No `scope`, `allowed_models`, `capabilities` column in `api_keys` (grep confirmed) | OpenRouter keys carry scopes; new-api channel groups serve this; direct-key scoping absent |
| 38 | API key IP allowlist / CIDR restriction | MISSING | No `ip_allowlist`, `allowed_ips` column; no IP check in `api_key_resolver.go` | sub2api supports per-key IP binding; gap |
| 39 | Per-key rate limit / spending cap | MISSING | No `rate_limit`, `spending_limit`, `monthly_budget` on `api_keys`; only global per-IP bucket | sub2api / new-api support per-channel quota; gap for per-key monetization control |
| **Admin Auth & RBAC** |||||
| 40 | Admin token auth (bearer `hk_admin_*`) | PRESENT | `internal/admin/operator_auth.go:52` `Resolve()`; `0010:admin_tokens` | bcrypt hash; 16-char prefix indexed lookup |
| 41 | Admin RBAC: platform_admin vs tenant_operator | PRESENT | `internal/admin/admin.go:119` `CanIssueForTenant()`; `ErrAdminForbidden` on scope miss | platform_admin can issue any tenant; tenant_operator only ScopeTenantID |
| 42 | Admin bootstrap token | PRESENT | `0010:admin_tokens.bootstrap boolean`; `internal/admin` references | Allows initial admin token without existing admin |
| 43 | Admin token expiry | PRESENT | `0010:admin_tokens.expires_at`; checked in resolver | |
| 44 | Admin audit events (action log) | PRESENT | `0013_trust_chain_audit_ledger`; `admin_audit_events` table; Codex-inferred from route handlers | Credential-acquisition and pool-account actions logged |
| **Admin User Management** |||||
| 45 | Admin user list / search | MISSING | No `/admin/v1/users` in `routes.go`; `adminhttp/api_keys_handler.go:3` comment: *"later slices add /admin/v1/users"* | Explicitly deferred; sub2api / new-api have full CRUD |
| 46 | Admin user create | MISSING | Same as above; no `userauth.Service.AdminCreate()` | |
| 47 | Admin user disable / re-enable | MISSING | No `/admin/v1/users/{id}/status` endpoint; must resort to direct DB | |
| 48 | Admin user delete (soft) | MISSING | No admin route; `users.deleted_at` column exists but no API | |
| 49 | Admin user unlock (clear locked_until) | MISSING | No admin route; `locked_until` column present but no unlock API | |
| 50 | Admin role assignment (promote to admin) | PARTIAL | `0076_user_role.up.sql` adds `users.role`; `panelauth` reads it; no admin HTTP endpoint to write it | Can only set via direct DB; no `/admin/v1/users/{id}/role` |
| **Multi-Tenant** |||||
| 51 | Tenant isolation in all auth SQL queries | PRESENT | Every auth query includes `tenant_id = $N` OR composite FK `(tenant_id, id)`; `0007:FOREIGN KEY (tenant_id, user_id)` | Composite FK prevents cross-tenant user_id binding |
| 52 | Registration mode per-tenant (DB config) | MISSING | Mode loaded from env-var (`loadUserRegistrationModeFromEnv`); no per-tenant DB setting | All tenants share one mode; sub2api supports per-channel settings |
| 53 | Admin tenant management (create / list / disable tenants) | MISSING | No `/admin/v1/tenants` route; `tenants` table exists (migration 0001) but no HTTP API surface | |
| **MFA / Step-up Auth** |||||
| 54 | TOTP (Time-based OTP, authenticator app) | MISSING | No `totp`, `mfa`, `2fa`, `otp` hit anywhere in Go sources | sub2api supports TOTP (`/api/user/totp/*`); high commercial value gap |
| 55 | SMS / email OTP step-up | MISSING | No OTP delivery, no code-verify endpoint | |
| 56 | WebAuthn / FIDO2 passkey | MISSING | No `webauthn`, `fido`, `passkey` hits | |
| 57 | MFA enforcement policy (require MFA for admin) | MISSING | No MFA policy concept at all | |
| **Enterprise SSO** |||||
| 58 | SAML 2.0 SSO | MISSING | No SAML library or handler found | OpenRouter, sub2api (enterprise tier) support SAML |
| 59 | LDAP / Active Directory | MISSING | No LDAP hits | |
| 60 | SCIM user provisioning | MISSING | No SCIM endpoint or model | |
| 61 | OIDC provider (HUAKAI as IdP for third-party apps) | MISSING | HUAKAI consumes OIDC (Google/GitHub) but is not an issuer | |
| **Security Controls** |||||
| 62 | Auth endpoint rate limiting (per-IP, per-minute) | PRESENT | `cmd/gateway/rate_limit.go`; login 20/min, register 5/min, reset-password 5/min | Applied BEFORE RealIP middleware (S2-057) |
| 63 | Global rate limiting (all requests) | PRESENT | `rate_limit.go`; 180 req/180s per IP; bounded map 50K entries | Reset on overflow prevents spoofed-IP flood |
| 64 | SSRF protection on OAuth endpoints | PRESENT | `internal/userauth/oauth_flow.go`; `internal/auth/antigravity_token_provider.go:368` `NewSSRFProtectedOAuthClient()` | Rejects private/loopback/link-local/CGNAT/metadata IPs |
| 65 | Constant-time password comparison | PRESENT | `internal/userauth/password.go:57` `subtle.ConstantTimeCompare` | Prevents timing-oracle on argon2id output |
| 66 | Refresh-token storm budget (3-scope) | PRESENT | `internal/auth/storm_scope.go`; `internal/auth/storm_controller.go` | Account + endpoint + global scopes (S2-045) |
| 67 | Token plaintext never persisted / logged | PRESENT | `internal/userkey/userkey.go:87` `IssueResult.String()` redacts; `api_keys.key_hash` only | IssueResult.String() returns `<redacted>` for all fmt.* calls |
| 68 | Signed session payload (HMAC-SHA256) | PRESENT | `internal/usersession/rotation.go:241` `signPayload()`; `SigningKey []byte` | Missing key → ErrSigningKeyMissing → 503 fail-closed |
| 69 | Session signing key rotation | MISSING | `SigningKey` is a static `[]byte` injected at startup; no key-rotation mechanism or key-versioning | Old sessions cannot survive a key rotation without downtime |
| 70 | Token introspection endpoint | MISSING | No `POST /v1/auth/introspect` or equivalent | OAuth-standard capability; useful for service-to-service auth |
| **Audit & Observability** |||||
| 71 | Auth event sink (register/login/session events) | PRESENT | `internal/gatewayhttp/auth_handler.go:30` `AuthEventSink` interface; `AuthEvent{EventType,…}` | Interface-based; no-op default; durable backend pluggable |
| 72 | OAuth provider-account refresh audit trail | PRESENT | `internal/auth/audit.go`; `RefreshAuditEntry`; `WriteRefreshAudit()`; `0013:oauth_refresh_audit_events` | Outcome taxonomy: success/auth_expired/rate_limit/risk_control/etc. |
| 73 | Durable user API-key audit log | PARTIAL | `internal/userkey/userkey.go` uses `slog` structured log only; comment: *"durable user_audit_events 表升级见 RR-W5-009"* | DB-backed user auth audit table explicitly deferred (RR-W5-009) |
| 74 | Tenant SMTP email settings (admin-configurable) | PRESENT | `0025_email_settings.up.sql`; `email_settings(tenant_id, setting_key, setting_value)`; AES-GCM password envelope | Admin route: `v1/admin/email` via `MountAdminEmailSettingsRoutes()` |
| 75 | Email verification delivery | PRESENT | `internal/gatewayhttp/auth_handler.go`; `AuthEmailSender.SendVerification()` | NoopSender in tests; real SMTP injected via wiring |
| 76 | Password reset email delivery | PRESENT | `AuthEmailSender.SendPasswordReset()`; same wiring | |

---

## Top Missing, Ranked by Commercial Value

1. **TOTP / MFA (feature #54–57)** — Sub2api supports TOTP; every paying SaaS customer expects MFA for their admin accounts. Absence is a sales-blocker for enterprise. Recommend: TOTP via `pquerna/otp`, per-user enrollment flag, enforce-on-admin policy.

2. **Admin user management API (#45–50)** — No `/admin/v1/users` CRUD. Operators must use direct DB for create/disable/delete/unlock/role-assign. `adminhttp/api_keys_handler.go:3` calls this out explicitly. Without it the Admin Ops UI cannot manage users.

3. **Authenticated password change (#26)** — Users who know their current password cannot change it without going through the email-based reset flow. Sub2api: `PUT /api/user/password`. Gap for security hygiene and regulatory compliance.

4. **API key scopes / per-key spending cap (#37–39)** — No model allowlist, no IP restriction, no monthly-budget per key. All keys issued under a user have identical permissions. Sub2api and OpenRouter support per-key scoping; needed for partner/developer API key tiers.

5. **Session signing-key rotation (#69)** — `SigningKey` is static at startup. A compromised key requires a full restart and invalidates all active sessions simultaneously. Standard mitigations: key versioning in token header + rolling window overlap.

6. **Admin tenant management API (#53)** — Tenants can only be created via migration bootstrap. No admin HTTP API to list, create, suspend, or configure tenants. Blocks multi-tenant onboarding automation.

7. **Registration mode per-tenant DB config (#52)** — One env-var controls all tenants. Multi-tenant platforms need per-tenant open/closed/invite modes (sub2api stores per-channel config).

8. **User self-service profile update and account deletion (#10, #11)** — No `PUT /v1/users/me`, no `DELETE /v1/users/me`. GDPR Article 17 requires a deletion path. Sub2api / new-api expose `/api/user` PUT.

9. **Social identity unlink (#8)** — Users cannot remove a linked Google/GitHub account without direct DB access.

10. **Durable user auth audit log (#73)** — API key create/revoke actions are slog only. DB-backed `user_audit_events` table is explicitly deferred (RR-W5-009) but is required for compliance (SOC 2, ISO 27001 evidence).

11. **Dedicated logout endpoint (#23)** — No `POST /v1/auth/logout`. Clients must call `POST /v1/sessions/revoke`. Minor UX/spec compliance gap (OpenAPI convention; sub2api/new-api expose named logout).

12. **SAML / LDAP / SCIM enterprise SSO (#58–61)** — Not present. Lower urgency for early SaaS phase but a hard requirement for enterprise contracts.

13. **Token introspection (#70)** — Absence means service-to-service callers cannot verify session tokens without implementing HMAC verification themselves.
