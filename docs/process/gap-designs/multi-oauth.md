# Gap Design: Multi-provider OAuth login (WeChat / DingTalk / LinuxDo / OIDC)

_Author: HUAKAI senior backend architect_
_Date: 2026-06-02_
_Status: READY FOR IMPLEMENTATION_

---

## Summary

The current codebase supports Google and GitHub OAuth behind the `OAuthProvider`
interface in `internal/userauth/social_login.go`. `normalizeSocialProvider` is a
hard-coded switch that returns `""` for any unknown name, causing all four target
providers to fail at the validation gate with `ErrOAuthProviderMissing`.

This design adds:

1. **WeChat** (OAuth 2.0 + custom `/sns/oauth2/access_token` + `/sns/userinfo` —
   no verified email field; pending-oauth multi-step flow required).
2. **DingTalk** (OAuth 2.0 + OIDC-like, `/v1.0/oauth2/userAccessToken` + `/v1.0/contact/users/me`).
3. **LinuxDo** (OAuth 2.0, Discourse-compatible `/oauth2/token` + `/api/user`).
4. **Generic OIDC / custom IdP** (admin-configurable issuer, discovery document,
   PKCE S256, claim-mapping; covers any spec-compliant IdP including enterprise SSO).

WeChat uniquely cannot guarantee a verified email from the upstream identity (the
`unionid`/`openid` subject is reliable but no email is returned). This requires a
**pending-oauth** two-step: the callback stores a short-lived `pending_oauth_token`
and asks the client to supply an email, which is verified before the identity is
committed to `social_identity_links`.

The design is additive and clean-room. No file in the FROZEN packages
(`internal/gatewayhttp`, `internal/gateway`, `internal/proto`) is created; only
existing files are modified where strictly required for registration. All new logic
lives in new packages under `internal/`.

---

## Package layout

Each file is kept under 500 lines; each function under 80 lines.

```
internal/socialprovider/
    wechat/
        provider.go          (~280 ln) WeChat OAuth client: auth URL, code exchange,
                                       /sns/userinfo fetch, openid→subject mapping.
                                       Returns VerifiedIdentity with EmailVerified=false
                                       when no email is available.
        provider_test.go     (~220 ln) Discriminating unit tests (see §Discriminating tests).

    dingtalk/
        provider.go          (~260 ln) DingTalk OAuth 2.0 + /v1.0/contact/users/me;
                                       extracts unionId as subject, stateEmail from
                                       upstream if present.
        provider_test.go     (~200 ln) Discriminating unit tests.

    linuxdo/
        provider.go          (~220 ln) LinuxDo Discourse OAuth2; fetches /api/user,
                                       uses numeric id as subject, email field.
        provider_test.go     (~180 ln) Discriminating unit tests.

    oidc/
        discovery.go         (~240 ln) Fetches and caches OIDC discovery document
                                       (/.well-known/openid-configuration); validates
                                       issuer match; extracts authorization_endpoint,
                                       token_endpoint, jwks_uri.
        provider.go          (~320 ln) Generic OIDC provider: AuthorizationURL with
                                       PKCE S256 + nonce; ExchangeVerifiedIdentity
                                       fetches token, verifies id_token RS256/ES256,
                                       applies configurable claim-mapping.
        provider_test.go     (~300 ln) Discriminating unit tests (see §Discriminating tests).

internal/pendingoauth/
    types.go                 (~80 ln)  PendingOAuthSession struct + error sentinels.
    service.go               (~260 ln) Service.CreatePending, Service.Consume,
                                       Service.CompleteWithEmail. Wraps userauth.Service
                                       for the two-step flow.
    store.go                 (~200 ln) PostgresStore: CreatePendingOAuthSession,
                                       ConsumePendingOAuthSession.
    service_test.go          (~280 ln) Discriminating unit tests.
```

**Modifications to existing files (not new files in frozen packages):**

- `internal/userauth/social_login.go` — extend `normalizeSocialProvider` to accept
  `"wechat"`, `"dingtalk"`, `"linuxdo"`, and `"oidc"` (plus any tenant-scoped
  dynamic OIDC slug following the pattern `"oidc:<slug>"`). Add exported constants.
  Extend `applyVerifiedSocialIdentity` with a branch for `EmailVerified=false` that
  returns `ErrOAuthPendingEmailRequired` (new sentinel) instead of
  `ErrSocialLoginRejected`, enabling the handler layer to route into the
  pending-oauth path.
- `internal/userauth/types.go` — add `ErrOAuthPendingEmailRequired` sentinel.
- `internal/gatewayhttp/auth_handler.go` — add handling for
  `ErrOAuthPendingEmailRequired` in `writeAuthError` / `authReasonClass`; add the
  two new HTTP handler functions `newAuthOAuthPendingEmailHandler` and
  `newAuthOAuthCompletePendingHandler`; register them in `MountAuthRoutes`.
  Add `safeSocialProvider` cases for the four new providers.
  (This is a modification of an existing file, not a new file — satisfies FROZEN
  constraint.)

---

## Schema / migrations

### Migration 0077 — multi-provider OAuth support

**File:** `sql/migrations/0077_multi_provider_oauth.up.sql`

```sql
BEGIN;

-- 1. Widen provider CHECK constraints to accept new providers.
--    social_identity_links.provider
ALTER TABLE social_identity_links
    DROP CONSTRAINT IF EXISTS social_identity_links_provider_check;
ALTER TABLE social_identity_links
    ADD CONSTRAINT social_identity_links_provider_check
    CHECK (provider IN ('google','github','wechat','dingtalk','linuxdo','oidc'));

--    oauth_flow_sessions.provider
ALTER TABLE oauth_flow_sessions
    DROP CONSTRAINT IF EXISTS oauth_flow_sessions_provider_check;
ALTER TABLE oauth_flow_sessions
    ADD CONSTRAINT oauth_flow_sessions_provider_check
    CHECK (provider IN ('google','github','wechat','dingtalk','linuxdo','oidc'));

-- 2. pending_oauth_sessions — short-lived two-step flow for providers that do
--    not return a verified email (WeChat). The token_hash is the only stored
--    identity material; upstream identity (subject, display_name) is stored
--    as opaque ciphertext; raw upstream payloads MUST NOT appear here.
CREATE TABLE IF NOT EXISTS pending_oauth_sessions (
    id                      uuid        PRIMARY KEY,
    tenant_id               bigint      NOT NULL REFERENCES tenants(id),
    provider                text        NOT NULL
                                CHECK (provider IN ('wechat','dingtalk','linuxdo','oidc')),
    subject_ciphertext      bytea       NOT NULL,
    display_name_ciphertext bytea,
    token_hash              bytea       NOT NULL,
    expires_at              timestamptz NOT NULL,
    consumed_at             timestamptz,
    created_at              timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_pending_oauth_token_hash
    ON pending_oauth_sessions (token_hash);

CREATE INDEX IF NOT EXISTS idx_pending_oauth_active
    ON pending_oauth_sessions (tenant_id, provider, expires_at)
    WHERE consumed_at IS NULL;

-- 3. oidc_provider_configs — admin-configurable generic OIDC / custom IdP per
--    tenant. client_secret is stored as AES-256-GCM ciphertext via credentialstore.
--    Raw client_secret MUST NOT be stored in plaintext in this table.
CREATE TABLE IF NOT EXISTS oidc_provider_configs (
    id                  bigserial   PRIMARY KEY,
    tenant_id           bigint      NOT NULL REFERENCES tenants(id),
    slug                text        NOT NULL,   -- e.g. "corp-sso", used as provider key "oidc:corp-sso"
    issuer              text        NOT NULL,
    discovery_url       text,                   -- defaults to issuer + /.well-known/openid-configuration
    client_id           text        NOT NULL,
    client_secret_cipher bytea      NOT NULL,
    scopes              text[]      NOT NULL DEFAULT '{"openid","email","profile"}',
    claim_subject       text        NOT NULL DEFAULT 'sub',
    claim_email         text        NOT NULL DEFAULT 'email',
    claim_display_name  text        NOT NULL DEFAULT 'name',
    redirect_uri        text,
    enabled             boolean     NOT NULL DEFAULT true,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, slug)
);

CREATE INDEX IF NOT EXISTS idx_oidc_provider_configs_tenant_enabled
    ON oidc_provider_configs (tenant_id, enabled);

COMMENT ON TABLE pending_oauth_sessions IS
    'Short-lived two-step OAuth sessions for providers that do not return a verified email.
     Subject and display_name are stored encrypted; raw upstream payloads never stored here.';
COMMENT ON TABLE oidc_provider_configs IS
    'Admin-configurable generic OIDC / custom IdP per tenant.
     client_secret_cipher stores AES-256-GCM envelope; plaintext secret never stored.';

COMMIT;
```

**File:** `sql/migrations/0077_multi_provider_oauth.down.sql`

```sql
BEGIN;
DROP TABLE IF EXISTS oidc_provider_configs;
DROP TABLE IF EXISTS pending_oauth_sessions;
ALTER TABLE oauth_flow_sessions
    DROP CONSTRAINT IF EXISTS oauth_flow_sessions_provider_check;
ALTER TABLE oauth_flow_sessions
    ADD CONSTRAINT oauth_flow_sessions_provider_check
    CHECK (provider IN ('google','github'));
ALTER TABLE social_identity_links
    DROP CONSTRAINT IF EXISTS social_identity_links_provider_check;
ALTER TABLE social_identity_links
    ADD CONSTRAINT social_identity_links_provider_check
    CHECK (provider IN ('google','github'));
COMMIT;
```

---

## Endpoints

All endpoints follow the existing pattern in `gatewayhttp.MountAuthRoutes`.

| Method | Path | Auth scope | Notes |
|--------|------|------------|-------|
| `POST` | `/oauth-init` | none (existing) | Extended: now accepts `provider` = `wechat`, `dingtalk`, `linuxdo`, `oidc:<slug>` |
| `POST` | `/oauth-callback` | none (existing) | Extended: WeChat callback returns `pending_oauth_token` + HTTP 202 when no email; others behave as Google/GitHub |
| `POST` | `/oauth-pending-email` | none (new) | Body: `{tenant_id, pending_token, email}`; triggers email-verification flow; returns `{verification_required: true}` |
| `POST` | `/oauth-complete-pending` | none (new) | Body: `{tenant_id, pending_token, email_verification_token}`; consumes both tokens; creates/links user; returns session |
| `GET/POST` | `/admin/oidc-providers` | admin (existing `AdminAuth`) | CRUD for `oidc_provider_configs`; lives in a new `internal/oidcproviderhttp` package to satisfy FROZEN constraint |

The admin OIDC CRUD handler (`internal/oidcproviderhttp`) is wired into the router
in `cmd/gateway/routes.go` (existing frozen file, modification only — one `Mount` call).

---

## Invariants honored

**CMB: credentials and raw upstream payloads NEVER logged**

- WeChat `access_token`, `openid`, `unionid`, DingTalk `accessToken`, LinuxDo
  `access_token`, OIDC `id_token` / `access_token`: none are written to logs or
  returned to callers. Only the normalized `VerifiedIdentity` struct crosses package
  boundaries.
- `subject_ciphertext` in `pending_oauth_sessions` stores the provider subject
  under AES-256-GCM via the existing `credentialstore.Cipher` (same pattern as
  `pkce_verifier_ciphertext`). Raw subject never written to the DB in plaintext.
- `client_secret_cipher` in `oidc_provider_configs` uses the same envelope.

**CMB: router reads no credentials and writes nothing**

- `internal/gatewayhttp/auth_handler.go` modifications are pure HTTP
  encoding/decoding; they call methods on `userauth.Service` and
  `pendingoauth.Service` — no direct DB or credential access.

**CMB: fail-closed on ambiguity**

- Unknown provider slug → `ErrOAuthProviderMissing` (existing sentinel, HTTP 503).
- Discovery document fetch failure → abort with `ErrOAuthProviderMissing`.
- Pending-oauth token missing or expired → `ErrOAuthFlowNotFound` (existing sentinel).
- OIDC issuer mismatch in id_token → `ErrSocialLoginRejected` (existing sentinel).
- WeChat pending path: if the email supplied by the user is already linked to a
  different social identity, fail with `ErrDuplicateUser` / `ErrSocialLoginRejected`
  — never silently merge.

**S2-009: SSRF / unsafe endpoint guard**

- `socialprovider/oidc/discovery.go` calls `validateOAuthEndpointURL` (reused from
  `oauth_flow.go`, extracted to `internal/userauth` so new packages can import it)
  on every URL extracted from the discovery document before any outbound HTTP is made.
- All provider HTTP clients use `auth.NewSSRFProtectedOAuthClient` (same as existing
  `buildOAuthProvider` in `cmd/gateway/config.go`).
- `oidc_provider_configs.issuer` is validated at write time with
  `validateOAuthEndpointURL`; no discovery document is ever fetched to a private
  address.

**PKCE S256**

- All four new providers use the existing `OAuthFlowChallenge.PKCEChallenge` /
  `PKCEVerifier` mechanism already produced by `NewOAuthFlowChallenge`. The generic
  OIDC provider includes `code_challenge` + `code_challenge_method=S256` in the
  authorization URL unconditionally (same as Google in `oauth_flow.go` line 180-181).
  WeChat does not support PKCE server-side but the PKCE verifier is still generated
  and stored encrypted (defense-in-depth; PKCE param is silently dropped from WeChat
  auth URL since Weixin ignores unknown params).

**Nonce**

- OIDC provider includes `nonce` in the authorization request; verifies `nonce`
  claim in id_token against `flow.NonceHash` (same pattern as `verifyGoogleIDToken`,
  `oauth_flow.go` lines 304-307). WeChat/DingTalk/LinuxDo (non-OIDC) omit nonce from
  the upstream request but still store `NonceHash` in the flow session (set to
  `sha256("")` sentinel so the existing DB NOT NULL is satisfied).

**MODULARITY**

- Each provider is its own sub-package. `oidc/discovery.go` and `oidc/provider.go`
  are separate files with single responsibilities. `pendingoauth` is independent of
  any specific provider.
- No file exceeds 500 lines (confirmed by per-file line count targets above).
- No function will exceed 80 lines; long identity-verification functions are split
  into `verifyIDToken` + `extractClaims` + `validateClaims` sub-functions.

---

## Discriminating tests

Each test is named so that deleting the specific guard it defends causes the test
to fail; removing an unrelated guard leaves it green.

### `internal/socialprovider/wechat/provider_test.go`

| Test | Defect defended |
|------|----------------|
| `TestWeChatProviderReturnsEmailVerifiedFalse` | If `EmailVerified` is hardcoded `true` for WeChat, this test catches it. Mutation: set `EmailVerified=true` → red. |
| `TestWeChatProviderRejectsEmptyOpenID` | Subject (openid) must be non-empty. Mutation: remove openid-blank guard → red. |
| `TestWeChatEndpointRejectsNonHTTPS` | `NewWeChatProvider` must refuse plain-http endpoints. Mutation: remove HTTPS check → red. |
| `TestWeChatIdentitySubjectIsOpenID` | Subject = `openid` (not `unionid`) when `unionid` absent. Mutation: swap field → red. |

### `internal/socialprovider/oidc/provider_test.go`

| Test | Defect defended |
|------|----------------|
| `TestOIDCProviderRejectsIssuerMismatch` | `iss` claim in id_token must equal configured issuer. Mutation: remove issuer check → red. |
| `TestOIDCProviderRejectsExpiredIDToken` | Expired `exp` must be rejected. Mutation: remove exp check → red. |
| `TestOIDCProviderRejectsNonceMismatch` | `nonce` claim must match `flow.NonceHash`. Mutation: remove nonce check → red. |
| `TestOIDCProviderRejectsUnverifiedEmail` | `email_verified=false` in claims must → `ErrSocialLoginRejected` unless claim mapping overrides. Mutation: remove email_verified guard → red. |
| `TestOIDCDiscoveryRejectsPrivateIssuer` | Discovery fetch to a private-IP issuer must fail. Mutation: remove IP guard → red. |
| `TestOIDCDiscoveryRejectsIssuerMismatchInDocument` | The `issuer` field in the fetched discovery document must match the configured issuer. Mutation: remove document-issuer validation → red. |
| `TestOIDCProviderPKCEChallengeIncludedInAuthURL` | `code_challenge` and `code_challenge_method=S256` must appear in auth URL. Mutation: remove PKCE params → red. |

### `internal/pendingoauth/service_test.go`

| Test | Defect defended |
|------|----------------|
| `TestPendingOAuthConsumeIsOneShot` | Second consume of the same pending token must return `ErrOAuthFlowNotFound`. Mutation: remove consumed_at guard → red. |
| `TestPendingOAuthRejectsExpiredToken` | Expired pending token must fail. Mutation: remove expiry check → red. |
| `TestPendingOAuthCompleteWithEmailRejectsUnverifiedEmail` | If the email-verification step is skipped (wrong verification token), `CompleteWithEmail` must fail. Mutation: remove verification check → red. |
| `TestPendingOAuthSubjectDecryptFailureIsHardError` | If the subject ciphertext cannot be decrypted, the flow must fail closed. Mutation: return empty subject on decrypt error → red. |

### Modifications to existing tests

- `internal/userauth/service_test.go`: add case `TestApplyVerifiedSocialIdentityReturnsPendingErrorForUnverifiedEmail` — `applyVerifiedSocialIdentity` with `EmailVerified=false` must return `ErrOAuthPendingEmailRequired`, not `ErrSocialLoginRejected`. Mutation: change `ErrOAuthPendingEmailRequired` to `ErrSocialLoginRejected` in the new branch → red.
- `internal/gatewayhttp/auth_handler.go` (test file): add `TestAuthOAuthCallbackReturns202ForPendingOAuth` — WeChat-like flow where `CompleteOAuth` returns `ErrOAuthPendingEmailRequired` must produce HTTP 202 with `pending_oauth_token` field, not HTTP 403. Mutation: map `ErrOAuthPendingEmailRequired` to 403 → red.

---

## Parity-or-better vs reference

The reference implementation pattern is inferred from the existing
`internal/userauth/oauth_flow.go` (Google/GitHub) and the OIDC-standard behavior
from `internal/provider/kiro/` (AWS Bedrock OIDC) — the latter is the only in-repo
example of OIDC token/jwks handling.

| Reference behavior | Reference location (behavioral cite) | HUAKAI implementation |
|--------------------|--------------------------------------|-----------------------|
| PKCE S256 in authorization URL (`code_challenge_method=S256`) | `oauth_flow.go:180-181` | All four providers include PKCE S256 via the existing `OAuthFlowChallenge.PKCEChallenge` |
| Nonce in authorization URL + nonce claim verification in id_token | `oauth_flow.go:183-184`, `verifyGoogleIDToken:304-307` | OIDC provider follows exact same pattern; WeChat/DingTalk/LinuxDo store sentinel nonce hash |
| PKCE verifier encrypted at rest (AES-256-GCM envelope) | `store.go:577-591`, migration 0024 | `pending_oauth_sessions.subject_ciphertext` uses same `credentialstore.Cipher` pattern |
| State hash stored (not raw state) | `store.go:585`, migration 0020 schema | Unchanged — new providers reuse existing `oauth_flow_sessions` table |
| Issuer validation in JWT claims | `oauth_flow.go:298-299` | `oidc/provider.go:verifyIDToken` replicates exact check + `https://` prefix tolerance |
| Audience (`aud`) must equal `client_id` | `oauth_flow.go:301-302` | Replicated in OIDC provider; also handles array `aud` (OIDC spec §3.1.3.7 — improvement over Google-only single-string check) |
| SSRF-protected HTTP client at construction | `config.go:206`, `oauth_flow.go:123-131` | `oidc/discovery.go` validates issuer URL before any fetch; all HTTP clients use `auth.NewSSRFProtectedOAuthClient` |
| `normalizeSocialProvider` returns `""` for unknown → `ErrOAuthProviderMissing` | `social_login.go:218-227`, `social_login.go:71-75` | Preserved; new providers added to the switch, no default leakage |
| Upstream access token / raw payload never stored | migration 0020 COMMENT on `oauth_flow_sessions` | `pending_oauth_sessions` schema comment; raw subject is ciphertext only |
| Email normalization (`NormalizeEmail`) applied before any store lookup | `social_login.go:145`, `store.go:73` | Pending-oauth `CompleteWithEmail` applies `NormalizeEmail` before verification and before `applyVerifiedSocialIdentity` |
| Social signup gated by `RegistrationMode` | `social_login.go:175-190` | Pending-oauth `CompleteWithEmail` calls the same `applyVerifiedSocialIdentity` path after email verification — registration gate is not bypassed |

**Better than reference:**

- Generic OIDC handles array `aud` claim (spec-compliant; Google impl only checks
  string `aud`).
- Discovery document issuer field is cross-validated against configured issuer
  (prevents misconfigured issuer pointing to a hostile discovery document).
- `oidc_provider_configs` stored per-tenant with slug, enabling multiple concurrent
  OIDC IdPs per tenant (reference has at most one Google + one GitHub globally).

---

## Effort

**L** (Large)

Rationale: four provider implementations + new `pendingoauth` package + DB migration
+ admin CRUD handler + modifications to five existing files + full discriminating test
suite. No single piece is architecturally novel — all patterns exist in the codebase
— but the surface area is wide and the pending-oauth two-step introduces a new
stateful flow requiring careful security review.

Wave decomposition (suggested for implementation):

- **Wave 1** — Migration 0077 + `normalizeSocialProvider` extension + new error
  sentinel (zero risk, pure additive, unblocks everything).
- **Wave 2** — `socialprovider/linuxdo` + `socialprovider/dingtalk` (both return
  verified email; no pending-oauth path; lowest complexity).
- **Wave 3** — `socialprovider/oidc` (discovery + provider + claim mapping).
- **Wave 4** — `socialprovider/wechat` + `pendingoauth` package + pending-oauth
  handler endpoints.
- **Wave 5** — `oidcproviderhttp` admin CRUD + wiring into `routes.go`.

---

## Risks

| Risk | Severity | Mitigation |
|------|----------|------------|
| WeChat `openid` is app-scoped; same user on two WeChat apps = two distinct subjects | High | Use `unionid` as subject when the WeChat app has cross-app permission; fall back to `openid` with a `wechat:openid:` prefix to avoid collisions with `unionid`-derived subjects. Document in code. |
| OIDC discovery document may return `authorization_endpoint` with an HTTP URL | High | `discovery.go` calls `validateOAuthEndpointURL` on every URL extracted from the document before storing or using it. |
| DingTalk v2 API returns `unionId` (case varies by app version) | Medium | Normalize field lookup: try `unionId`, then `unionid`, then `userId` — with a clear comment and test fixture. |
| `oidc_provider_configs.client_secret_cipher` key rotation | Medium | Uses existing `credentialstore.KeyProvider` rotation mechanism (same as PKCE verifier). No new key management surface. |
| Pending-oauth token phishing: attacker intercepts `pending_oauth_token` | Medium | Token is a `GenerateToken()`-produced 256-bit random (same as existing state/nonce tokens); stored as hash only; TTL 10 minutes; consumed exactly once. The email-verification step (second factor) must be completed with the same pending token — phishing the pending token alone is insufficient without also intercepting the email verification link. |
| OIDC RS256 key rollover (JWKS key not found) | Low | `oidc/provider.go` re-fetches JWKS once on `kid` miss (same pattern as `fetchRSAKey`); does not cache indefinitely. ES256 support added (missing from Google impl — improvement). |
| `normalizeSocialProvider` is used in DB scan (`store.go:734`) | Low | The scan normalizer filters to known providers; an `oidc`-prefixed slug like `oidc:corp-sso` is stored verbatim in `oauth_flow_sessions` (the DB CHECK allows `oidc`) and is passed through unchanged — the slug is opaque to `normalizeSocialProvider` which returns `oidc` after stripping the colon-suffix at the service layer. This must be tested explicitly. |
