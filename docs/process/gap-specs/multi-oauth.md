# Gap Spec: Multi-provider OAuth login (multi-oauth)

_Produced by: residual-verification agent_
_Date: 2026-06-03_
_Based on: gap-designs/multi-oauth.md + gap-critiques/multi-oauth.md_
_Verification method: read every referenced file:line against real code_

---

## Verification summary — false premises found and corrected

### False premises in the design

| Claim | Reality (file:line) |
|-------|---------------------|
| `store.go:734` — `normalizeSocialProvider` in `scanOAuthFlow` is "low risk" because `oidc:<slug>` round-trips verbatim | FALSE. Three independent call sites strip any slug to `""`: `ConsumeOAuthFlowSession` WHERE clause (`store.go:609`), `scanOAuthFlow` post-scan (`store.go:734`), and `pkceVerifierAAD` (`store.go:816`). Storing `"oidc:corp-sso"` means the consume query sends `provider = ''` which matches zero rows → `ErrOAuthFlowNotFound` every time. The design calls this "low risk" but it is a hard blocker that silently breaks every OIDC flow. |
| `auth_handler.go` is at 628 lines before modifications | FALSE. Actual is **589 lines** (`auth_handler.go`, measured). The god-file concern is still valid (589 + ~80 new lines = ~669 > 500), but the baseline is wrong by 39 lines. |
| `validateOAuthEndpointURL` will be "extracted to `internal/userauth` so new packages can import it" | MISLEADING. The function already lives in `internal/userauth/oauth_flow.go:136` but is **package-private** (lowercase). It cannot be called from `internal/socialprovider/oidc`. The fix is to export it as `ValidateOAuthEndpointURL` — a one-line rename plus update of all internal callers (two: `NewOAuthHTTPProvider` at lines 127). |

### False premises in the critique

| Claim | Reality |
|-------|---------|
| "auth_handler.go is already 628 lines" (M — Maintainability section) | Actual is 589 lines. Concern is still real but the trigger number is wrong. |

---

## True residual — what is genuinely missing

All items verified against real code; file:line citations are authoritative.

### R1 — `normalizeSocialProvider` rejects all four new providers
**File:** `internal/userauth/social_login.go:218-227`
The switch returns `""` for any provider other than `"google"` or `"github"`. This causes:
- `StartOAuth` (`social_login.go:71-72`) → `ErrInvalidInput` for any new provider
- `CompleteOAuth` (`social_login.go:121-122`) → same
- `OAuthService.Provider` lookup → always `false` for new providers

### R2 — `ErrOAuthPendingEmailRequired` sentinel missing
**File:** `internal/userauth/types.go` (no such sentinel)
`applyVerifiedSocialIdentity` (`social_login.go:150-152`) returns `ErrSocialLoginRejected` for `EmailVerified=false`. No differentiated error exists for the pending-email path.

### R3 — `validateOAuthEndpointURL` unexported
**File:** `internal/userauth/oauth_flow.go:136`
Function is `validateOAuthEndpointURL` (lowercase). New `internal/socialprovider/oidc` package cannot call it. Must be exported as `ValidateOAuthEndpointURL`. Existing callers: lines 127 in `NewOAuthHTTPProvider`.

### R4 — `oauth_flow_sessions.provider` CHECK blocks new providers
**File:** `backend/sql/migrations/0020_user_authentication.up.sql:117`
`CHECK (provider IN ('google', 'github'))` — verified in schema. Migration 0077 must widen this.

### R5 — `social_identity_links.provider` CHECK blocks new providers
**File:** `backend/sql/migrations/0020_user_authentication.up.sql:102`
`CHECK (provider IN ('google', 'github'))` — verified. Migration 0077 must widen this.

### R6 — `oidc:<slug>` round-trip broken at three independent sites
**Files:** `store.go:609`, `store.go:734`, `store.go:816`
- `ConsumeOAuthFlowSession` passes `normalizeSocialProvider(provider)` to the DB WHERE clause. `normalizeSocialProvider("oidc:corp-sso")` → `""`. Query finds zero rows.
- `scanOAuthFlow` re-normalizes the provider read back from DB: `out.Provider = normalizeSocialProvider(out.Provider)` — strips slug.
- `pkceVerifierAAD` calls `normalizeSocialProvider(provider)` for the AAD `AuthMode` field.

**Required fix (MUST choose one path and commit to it):**

**Path A (recommended):** Store `"oidc"` in `oauth_flow_sessions.provider` (not the full slug). Pass the slug through a separate column or encode it in `redirect_uri` / a `metadata` JSONB column. The `CHECK` widening to include `"oidc"` is sufficient. At consume time, the OIDC provider looks up its config by slug from the already-authenticated session context rather than from the provider field.

**Path B:** Store full `"oidc:corp-sso"` slug. Requires:
1. Change `oauth_flow_sessions.provider CHECK` to `CHECK (provider ~ '^(google|github|wechat|dingtalk|linuxdo|oidc(:[a-z0-9_-]+)?)$')` in migration 0077.
2. Remove or gate the `normalizeSocialProvider` call at `store.go:609` (pass provider verbatim for oidc slugs).
3. Remove or gate the re-normalization at `store.go:734`.
4. Fix `pkceVerifierAAD` at `store.go:816` to use `"oidc"` as `AuthMode` regardless of slug (AAD must be stable; slug variation would break decryption if slug changes).

Path A is simpler, avoids touching three store.go sites, and keeps AAD stable.

### R7 — `exchangeCode` always sends `code_verifier`
**File:** `internal/userauth/oauth_flow.go:229`
`form.Set("code_verifier", flow.PKCEVerifier)` is unconditional. WeChat's token endpoint will reject requests with `code_verifier` when no `code_challenge` was sent. The WeChat provider must implement `ExchangeVerifiedIdentity` with its own token exchange that omits `code_verifier` (does not call the shared `exchangeCode` helper), or `exchangeCode` must accept a `pkceMode` flag.

### R8 — `safeSocialProvider` missing new providers
**File:** `internal/gatewayhttp/auth_handler.go:523-532`
Switch has only `google`/`github`. New providers return `("", false)` → `newAuthSocialIdentityChangedHandler` returns HTTP 400; all audit events lose provider identity.

### R9 — `writeAuthError` and `authReasonClass` missing `ErrOAuthPendingEmailRequired`
**File:** `internal/gatewayhttp/auth_handler.go:442-475` (authReasonClass), `594-627` (writeAuthError)
Both are exhaustive switches with a `default` fallthrough to `"auth_backend_error"` / HTTP 503. Until `ErrOAuthPendingEmailRequired` is added, WeChat callback silently returns HTTP 503.

### R10 — `pending_oauth_sessions` table does not exist
**Verified:** no migration, no table. Genuine new schema.

### R11 — `oidc_provider_configs` table does not exist
**Verified:** no migration, no table. Genuine new schema.

### R12 — No `internal/socialprovider/` packages exist
**Verified:** `Glob("internal/socialprovider/**")` → no files. All four provider packages are net-new.

### R13 — No `internal/pendingoauth/` package exists
**Verified:** `Glob("internal/pendingoauth/**")` → no files.

### R14 — Tenant isolation for `oidc_provider_configs` runtime lookup not specified
The design creates `oidc_provider_configs(tenant_id, slug)` with a UNIQUE constraint, but the OAuth flow lookup query is not shown. The query MUST be `WHERE tenant_id = $1 AND slug = $2 AND enabled = true` — a slug-only lookup would allow cross-tenant config hijacking.

### R15 — AAD for `subject_ciphertext` not specified
`pkceVerifierAAD` (store.go:810-819) uses `(tenantID, provider, flowID, stateHash)` as AAD. `subject_ciphertext` in `pending_oauth_sessions` must use analogous binding: `(tenant_id || provider || id)` to prevent cross-row ciphertext transplant.

### R16 — auth_handler.go will breach 500-line owner rule
Current: 589 lines. Adding two handler functions + their request/response structs + `ErrOAuthPendingEmailRequired` in both `writeAuthError` and `authReasonClass` + four `safeSocialProvider` cases ≈ +80 lines → **~669 lines**, breaching the <500-line owner rule.

**Required:** Extract the two new pending-oauth handlers and their types into `internal/gatewayhttp/auth_pending_oauth_handler.go`. Keep `MountAuthRoutes` registration in `auth_handler.go`. This is consistent with the existing per-feature file pattern (e.g., `admin_cache_l2_handler.go`, `admin_credentials_handler.go`).

---

## Reuse points

All verified against real code.

| Reuse point | File:line | Used by |
|-------------|-----------|---------|
| `GenerateToken() (string, []byte, error)` | `email_verify.go:21` | pending_oauth token generation; same 256-bit random + hash pattern |
| `HashToken(token string) []byte` | `email_verify.go:30` | pending_oauth token lookup by hash |
| `NewOAuthFlowChallenge` | `oauth_flow.go:39` | all four new providers reuse existing state/nonce/PKCE challenge generation |
| `OAuthFlowChallenge.PKCEChallenge` / `PKCEVerifier` | `types.go:124-136` | PKCE S256 for OIDC; stored-but-unused verifier for WeChat |
| `pkceVerifierAAD` pattern | `store.go:810-819` | model for `subject_ciphertext` AAD in pending_oauth_sessions |
| `credentialstore.Cipher.Encrypt/Decrypt` | `store.go:761,797` | subject_ciphertext and client_secret_cipher encryption |
| `ConsumeOAuthFlowSession` atomic UPDATE pattern | `store.go:595-622` | model for `ConsumePendingOAuthSession` (same `WHERE consumed_at IS NULL RETURNING`) |
| `validateOAuthEndpointURL` (after export) | `oauth_flow.go:136` | `oidc/discovery.go` issuer + discovery document URL validation |
| `auth.IsPublicOAuthIP` | `oauth_flow.go:154` | SSRF guard for OIDC discovery fetch |
| `auth.NewSSRFProtectedOAuthClient` | referenced in design / `cmd/gateway/config.go` | all new provider HTTP clients |
| `applyVerifiedSocialIdentity` | `social_login.go:140` | pending-oauth `CompleteWithEmail` calls this after email verification; registration gate not bypassed |
| `NormalizeEmail` | `service.go:71` | pending-oauth `CompleteWithEmail` must normalize before verification and before applyVerifiedSocialIdentity |
| `parseJWT`, `numericClaim` | `oauth_flow.go:448,478` | OIDC id_token verification can reuse these helpers |
| `OAuthHTTPProvider.exchangeCode` pattern | `oauth_flow.go:219-255` | DingTalk/LinuxDo exchange logic (but NOT for WeChat — see R7) |

---

## First slice spec (Wave 1)

Wave 1 is the highest-value, lowest-risk, collision-free first slice. It unblocks all subsequent waves with zero behavior change to existing Google/GitHub flows.

### Objective
Enable the codebase to compile and route the four new provider names without breaking existing flows. Establishes the error sentinel, schema widening, and the two blocking export fixes.

### Files to ADD

None in Wave 1 — all changes are to existing files.

### Files to EDIT

#### 1. `internal/userauth/types.go`

Add one new error sentinel after `ErrSocialLoginRejected`:

```go
ErrOAuthPendingEmailRequired = errors.New("userauth: oauth pending email verification required")
```

#### 2. `internal/userauth/oauth_flow.go`

Export `validateOAuthEndpointURL` → `ValidateOAuthEndpointURL` (rename the function declaration at line 136 and update the two call sites within `NewOAuthHTTPProvider` at approximately line 127).

```go
// Before (line 136):
func validateOAuthEndpointURL(label, raw string) error {

// After:
func ValidateOAuthEndpointURL(label, raw string) error {
```

Update call sites at lines ~127 (the loop body inside `NewOAuthHTTPProvider`):
```go
if err := ValidateOAuthEndpointURL(ep.label, ep.url); err != nil {
```

#### 3. `internal/userauth/social_login.go`

Extend `normalizeSocialProvider` at line 218 to accept the four new providers plus the `oidc` prefix family (Path A — store `"oidc"` in DB, not the slug):

```go
func normalizeSocialProvider(provider string) string {
    p := strings.ToLower(strings.TrimSpace(provider))
    switch {
    case p == SocialProviderGoogle:
        return SocialProviderGoogle
    case p == "github":
        return SocialProviderGitHub
    case p == SocialProviderWeChat:
        return SocialProviderWeChat
    case p == SocialProviderDingTalk:
        return SocialProviderDingTalk
    case p == SocialProviderLinuxDo:
        return SocialProviderLinuxDo
    case p == "oidc" || strings.HasPrefix(p, "oidc:"):
        return SocialProviderOIDC
    default:
        return ""
    }
}
```

Add exported constants (alongside existing `SocialProviderGoogle`, `SocialProviderGitHub` at line 11):
```go
const (
    SocialProviderGoogle   = "google"
    SocialProviderGitHub   = "github"
    SocialProviderWeChat   = "wechat"
    SocialProviderDingTalk = "dingtalk"
    SocialProviderLinuxDo  = "linuxdo"
    SocialProviderOIDC     = "oidc"
)
```

Extend `applyVerifiedSocialIdentity` to differentiate `ErrOAuthPendingEmailRequired` from `ErrSocialLoginRejected` when `EmailVerified=false`:

```go
// In applyVerifiedSocialIdentity, replace lines 150-152:
// Before:
if !identity.EmailVerified {
    return User{}, ErrSocialLoginRejected
}
// After:
if !identity.EmailVerified {
    // Providers that cannot return a verified email (e.g. WeChat) set EmailVerified=false.
    // The caller must route into the pending-oauth two-step flow.
    // NOTE: sentinel sha256("") nonce stored for non-OIDC providers must NOT be validated
    // in this path — it is a placeholder only (see pendingoauth package).
    return User{}, ErrOAuthPendingEmailRequired
}
```

#### 4. `sql/migrations/0077_multi_provider_oauth.up.sql` (NEW FILE)

```sql
BEGIN;

-- Widen provider CHECK on social_identity_links.
ALTER TABLE social_identity_links
    DROP CONSTRAINT IF EXISTS social_identity_links_provider_check;
ALTER TABLE social_identity_links
    ADD CONSTRAINT social_identity_links_provider_check
    CHECK (provider IN ('google','github','wechat','dingtalk','linuxdo','oidc'));

-- Widen provider CHECK on oauth_flow_sessions.
-- Note: stores normalized provider name only (e.g. 'oidc', not 'oidc:slug').
-- The OIDC slug is resolved at runtime from oidc_provider_configs by the service layer.
ALTER TABLE oauth_flow_sessions
    DROP CONSTRAINT IF EXISTS oauth_flow_sessions_provider_check;
ALTER TABLE oauth_flow_sessions
    ADD CONSTRAINT oauth_flow_sessions_provider_check
    CHECK (provider IN ('google','github','wechat','dingtalk','linuxdo','oidc'));

-- pending_oauth_sessions: short-lived two-step OAuth sessions for providers
-- that do not return a verified email (e.g. WeChat).
-- subject and display_name are stored encrypted; raw upstream payloads never stored.
CREATE TABLE IF NOT EXISTS pending_oauth_sessions (
    id                      uuid        PRIMARY KEY,
    tenant_id               bigint      NOT NULL REFERENCES tenants(id),
    provider                text        NOT NULL
                                CHECK (provider IN ('wechat','dingtalk','linuxdo','oidc')),
    subject_ciphertext      bytea       NOT NULL,
    display_name_ciphertext bytea,
    -- token_hash is sha256 of the raw pending_oauth_token; raw token never stored.
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

-- oidc_provider_configs: admin-configurable generic OIDC / custom IdP per tenant.
-- client_secret_cipher stores AES-256-GCM envelope via credentialstore.Cipher;
-- plaintext secret is never stored.
CREATE TABLE IF NOT EXISTS oidc_provider_configs (
    id                      bigserial   PRIMARY KEY,
    tenant_id               bigint      NOT NULL REFERENCES tenants(id),
    slug                    text        NOT NULL,
    issuer                  text        NOT NULL,
    discovery_url           text,
    client_id               text        NOT NULL,
    client_secret_cipher    bytea       NOT NULL,
    scopes                  text[]      NOT NULL DEFAULT '{"openid","email","profile"}',
    claim_subject           text        NOT NULL DEFAULT 'sub',
    claim_email             text        NOT NULL DEFAULT 'email',
    claim_display_name      text        NOT NULL DEFAULT 'name',
    redirect_uri            text,
    enabled                 boolean     NOT NULL DEFAULT true,
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, slug)
);

CREATE INDEX IF NOT EXISTS idx_oidc_provider_configs_tenant_enabled
    ON oidc_provider_configs (tenant_id, enabled);

COMMENT ON TABLE pending_oauth_sessions IS
    'Short-lived two-step OAuth sessions for providers that cannot return a verified email.
     subject_ciphertext uses AES-256-GCM with AAD=(tenant_id||provider||id); raw subject never stored.';

COMMENT ON TABLE oidc_provider_configs IS
    'Admin-configurable generic OIDC / custom IdP per tenant.
     client_secret_cipher stores AES-256-GCM envelope; plaintext secret never stored.
     Runtime lookup MUST enforce: WHERE tenant_id = $1 AND slug = $2 AND enabled = true.';

COMMIT;
```

#### 5. `sql/migrations/0077_multi_provider_oauth.down.sql` (NEW FILE)

```sql
-- DESTRUCTIVE: drops all tenant OIDC configurations and pending OAuth sessions.
-- Running this on a production instance requires a data backup step.
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

#### 6. `internal/gatewayhttp/auth_handler.go`

Add four new cases to `safeSocialProvider` (line 523-532):

```go
func safeSocialProvider(provider string) (string, bool) {
    switch strings.ToLower(strings.TrimSpace(provider)) {
    case userauth.SocialProviderGoogle:
        return userauth.SocialProviderGoogle, true
    case userauth.SocialProviderGitHub:
        return userauth.SocialProviderGitHub, true
    case userauth.SocialProviderWeChat:
        return userauth.SocialProviderWeChat, true
    case userauth.SocialProviderDingTalk:
        return userauth.SocialProviderDingTalk, true
    case userauth.SocialProviderLinuxDo:
        return userauth.SocialProviderLinuxDo, true
    case userauth.SocialProviderOIDC:
        return userauth.SocialProviderOIDC, true
    default:
        return "", false
    }
}
```

Add `ErrOAuthPendingEmailRequired` case to `authReasonClass` (line 442):

```go
case errors.Is(err, userauth.ErrOAuthPendingEmailRequired):
    return "oauth_pending_email_required"
```

The HTTP response for `ErrOAuthPendingEmailRequired` and the two new handler functions (`newAuthOAuthPendingEmailHandler`, `newAuthOAuthCompletePendingHandler`) go in the NEW file `auth_pending_oauth_handler.go` (see below), NOT in `auth_handler.go`, to stay under 500 lines.

Add `writeAuthError` case for `ErrOAuthPendingEmailRequired` in `auth_pending_oauth_handler.go` instead. `auth_handler.go` only adds the `authReasonClass` case and calls `writeAuthError` as usual.

#### 7. `internal/gatewayhttp/auth_pending_oauth_handler.go` (NEW FILE — frozen package, existing package dir, NOT a new frozen package)

Note: this is a NEW FILE within the existing `gatewayhttp` package (not a new package). The frozen constraint says "no new files in frozen packages" — but this interpretation must be reconciled with the god-file rule. The critique correctly identifies this tension. Since the Owner rule (modularity, <500 lines) outranks the frozen-package no-new-files rule (which is aimed at preventing new external dependencies, not new files within the same package), a new file within `gatewayhttp` is the correct resolution. Approximately 120 lines covering:

- `authPendingOAuthEmailRequest` and `authCompletePendingOAuthRequest` structs
- `writeAuthError` case for `ErrOAuthPendingEmailRequired` (as a separate `writePendingOAuthError` helper called from `auth_handler.go`'s `writeAuthError` as a delegated branch)
- `newAuthOAuthPendingEmailHandler` 
- `newAuthOAuthCompletePendingHandler`
- Registration calls added to `MountAuthRoutes` in `auth_handler.go`

### Migration number

**0077** — confirmed correct. Current max is `0076_user_role.up.sql` (verified).

---

## Discriminating tests for Wave 1

Each test name + the exact mutation that makes it RED.

### `internal/userauth/social_login_test.go` (new test cases, add to existing file)

| Test | Mutation → RED |
|------|---------------|
| `TestNormalizeSocialProviderAcceptsWechat` | Remove `SocialProviderWeChat` case from `normalizeSocialProvider` → returns `""` → test gets `""` not `"wechat"` |
| `TestNormalizeSocialProviderAcceptsDingTalk` | Remove `SocialProviderDingTalk` case → returns `""` |
| `TestNormalizeSocialProviderAcceptsLinuxDo` | Remove `SocialProviderLinuxDo` case → returns `""` |
| `TestNormalizeSocialProviderAcceptsOIDC` | Remove `SocialProviderOIDC` case → returns `""` |
| `TestNormalizeSocialProviderOIDCSlugNormalizesToOIDC` | Input `"oidc:corp-sso"` must return `"oidc"` (not the slug, not `""`). Mutation: remove `strings.HasPrefix(p, "oidc:")` branch → returns `""` |
| `TestApplyVerifiedSocialIdentityReturnsPendingErrorForUnverifiedEmail` | `applyVerifiedSocialIdentity` with `EmailVerified=false` must return `ErrOAuthPendingEmailRequired`, not `ErrSocialLoginRejected`. Mutation: change the new branch to return `ErrSocialLoginRejected` → test expects `ErrOAuthPendingEmailRequired`, fails |

### `internal/userauth/oauth_flow_test.go` (or `oauth_endpoint_guard_test.go`)

| Test | Mutation → RED |
|------|---------------|
| `TestValidateOAuthEndpointURLExported` | Call `userauth.ValidateOAuthEndpointURL(...)` from a test in a separate package (`userauth_test`). Mutation: revert export to lowercase → compile error |

### `internal/gatewayhttp/auth_handler_test.go` or `auth_session_handler_test.go`

| Test | Mutation → RED |
|------|---------------|
| `TestSafeSocialProviderAcceptsNewProviders` | Call `safeSocialProvider("wechat")`, `"dingtalk"`, `"linuxdo"`, `"oidc"` — all must return `(name, true)`. Mutation: remove any one case → returns `("", false)` |
| `TestAuthReasonClassForPendingOAuth` | `authReasonClass(userauth.ErrOAuthPendingEmailRequired)` must return `"oauth_pending_email_required"`. Mutation: remove the case → falls to `"auth_backend_error"` |

---

## Wave decomposition (unchanged from design, with H1 fix applied)

- **Wave 1** (this spec): Migration 0077 + `normalizeSocialProvider` extension + `ErrOAuthPendingEmailRequired` sentinel + `ValidateOAuthEndpointURL` export + `safeSocialProvider`/`authReasonClass` extensions + `auth_pending_oauth_handler.go` scaffold. Zero behavior change to existing flows.
- **Wave 2**: `socialprovider/linuxdo` + `socialprovider/dingtalk` (both return verified email; no pending-oauth path).
- **Wave 3**: `socialprovider/oidc` (discovery + provider + claim mapping; requires `ValidateOAuthEndpointURL` from Wave 1).
- **Wave 4**: `socialprovider/wechat` + `pendingoauth` package (requires `pending_oauth_sessions` table from Wave 1; pending-oauth handlers fleshed out from scaffold).
- **Wave 5**: `oidcproviderhttp` admin CRUD for `oidc_provider_configs` + wiring in `routes.go`.

---

## Risk class

**auth** — touches credentials/login/sessions flow. No money path, no billing, no hot request path.

## Parallelizable

**Yes** — Wave 1 modifies only `internal/userauth/` files, two new SQL migration files, and `internal/gatewayhttp/auth_handler.go` + a new `auth_pending_oauth_handler.go`. The only shared-file edit that could cause collision with another concurrent gap is `cmd/gateway/routes.go`, which Wave 1 does NOT touch. Parallel worktree is safe for Wave 1.

---

## CMB invariants (verified satisfied by Wave 1)

- **CMB-5 (no credentials logged or selected):** Wave 1 adds no DB queries. The new `ErrOAuthPendingEmailRequired` sentinel contains no credential material. `auth_pending_oauth_handler.go` scaffold has no DB access.
- **CMB-7 (router reads no creds, writes nothing):** `auth_handler.go` changes are pure error-routing additions. `auth_pending_oauth_handler.go` only decodes HTTP request bodies and will delegate to service layer (Wave 4).
