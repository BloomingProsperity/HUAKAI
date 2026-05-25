# F-CRED-001 / F-AUTH-005 Feature Preservation Review (Sonnet lane)

| Field | Value |
|---|---|
| Reviewer | Claude Sonnet 4.6 (independent sonnet lane) |
| UTC | 2026-05-15T17:30:00Z |
| Lane | READ-ONLY reviewer — no plan or code modified |
| Codex plan exists | F-CRED-001-codex: MISSING (only claude plan present) |

---

## Scope (files read)

**HUAKAI plans:**
- `docs/process/plans/2026-05-15-f-cred-001-acquisition-claude.md`
- `docs/process/plans/2026-05-15-f-auth-005-credential-mgmt-claude.md`
- `docs/process/plans/2026-05-15-f-auth-005-credential-mgmt-codex.md`

**HUAKAI committed code (Round 2-B):**
- `backend/internal/credentialstore/*.go` — crypto, types, postgres_store
- `backend/internal/credentialworker/*.go` — scheduler, refresher, mode_refresh, adapters
- `backend/internal/gatewayhttp/admin_credentials_handler.go`

**sub2api source (read-only, specifier lane):**
- `service/openai_oauth_service.go`
- `service/gemini_oauth_service.go`
- `service/antigravity_oauth_service.go`
- `service/token_refresher.go`
- `service/gemini_token_refresher.go`
- `repository/claude_oauth_service.go`
- `repository/refresh_token_cache.go`
- `service/auth_oauth_email_flow.go`

---

## sub2api Function Inventory

### OpenAIOAuthService (8 exported methods)
1. `GenerateAuthURL(ctx, proxyID, redirectURI, platform) (*OpenAIAuthURLResult, error)` — PKCE + state, platform-aware clientID selection
2. `ExchangeCode(ctx, *OpenAIExchangeCodeInput) (*OpenAITokenInfo, error)` — code→token + ID-token parse + enrichment
3. `RefreshToken(ctx, refreshToken, proxyURL) (*OpenAITokenInfo, error)` — delegates to #4
4. `RefreshTokenWithClientID(ctx, refreshToken, proxyURL, clientID) (*OpenAITokenInfo, error)` — multi-clientID support (ChatGPT vs Codex CLI)
5. `enrichTokenInfo(ctx, tokenInfo, proxyURL)` — ChatGPT accounts/check enrich + privacy disable (unexported but behaviorally critical)
6. `RefreshAccountToken(ctx, *Account) (*OpenAITokenInfo, error)` — account-level orchestration + graceful fallback when no refresh token
7. `BuildAccountCredentials(*OpenAITokenInfo) map[string]any` — safe credential map constructor (no-overwrite on empty refresh token)
8. `Stop()` — session store lifecycle

Additional: `SetPrivacyClientFactory(factory)` — injects ImpersonateChrome client for enrichment

### GeminiOAuthService (9 exported methods)
1. `GetOAuthConfig() *GeminiOAuthCapabilities` — AI Studio OAuth enabled gate (operator-configured client check)
2. `GenerateAuthURL(ctx, proxyID, redirectURI, projectID, oauthType, tierID) (*GeminiAuthURLResult, error)` — code_assist / google_one / ai_studio branch logic
3. `ExchangeCode(ctx, *GeminiExchangeCodeInput) (*GeminiTokenInfo, error)` — oauthType-dispatched + auto fetchProjectID + FetchGoogleOneTier + canonical tier mapping
4. `RefreshToken(ctx, oauthType, refreshToken, proxyURL) (*GeminiTokenInfo, error)` — 3-attempt retry + non-retryable error detect
5. `RefreshAccountToken(ctx, *Account) (*GeminiTokenInfo, error)` — unauthorized_client fallback retry (code_assist ↔ ai_studio compat), 24h Drive tier cache
6. `FetchGoogleOneTier(ctx, accessToken, proxyURL) (tierID, *DriveStorageInfo, error)` — Drive API storage-quota → tier inference
7. `RefreshAccountGoogleOneTier(ctx, *Account) (tierID, extra, credentials, error)` — on-demand tier refresh for existing accounts
8. `BuildAccountCredentials(*GeminiTokenInfo) map[string]any` — with tier validation + extra Drive metadata
9. `Stop()` — session store lifecycle

Internal critical: `fetchProjectID` (LoadCodeAssist + OnboardUser + Cloud Resource Manager 3-path fallback), `canonicalGeminiTierID`, `inferGoogleOneTier`

### AntigravityOAuthService (7 exported methods)
1. `GenerateAuthURL(ctx, proxyID) (*AntigravityAuthURLResult, error)` — PKCE + state
2. `ExchangeCode(ctx, *AntigravityExchangeCodeInput) (*AntigravityTokenInfo, error)` — code→token + userInfo + loadProjectIDWithRetry + setAntigravityPrivacy
3. `RefreshToken(ctx, refreshToken, proxyURL) (*AntigravityTokenInfo, error)` — 3-retry + non-retryable + connection-error fast-exit
4. `ValidateRefreshToken(ctx, refreshToken, proxyID) (*AntigravityTokenInfo, error)` — refresh + userInfo + projectID (full re-validation path)
5. `RefreshAccountToken(ctx, *Account) (*AntigravityTokenInfo, error)` — preserves email + loadProjectID every refresh
6. `FillProjectID(ctx, *Account, accessToken) (string, error)` — project ID fill without token refresh
7. `BuildAccountCredentials(*AntigravityTokenInfo) map[string]any`
8. `Stop()`

### ClaudeOAuthClient / claudeOAuthService (repository layer, 4 methods)
1. `GetOrganizationUUID(ctx, sessionKey, proxyURL) (string, error)` — multi-org selection (team org preferred)
2. `GetAuthorizationCode(ctx, sessionKey, orgUUID, scope, codeChallenge, state, proxyURL) (string, error)` — claude.ai sessionKey → auth code
3. `ExchangeCodeForToken(ctx, code, codeVerifier, state, proxyURL, isSetupToken) (*TokenResponse, error)` — setup-token long-expiry support
4. `RefreshToken(ctx, refreshToken, proxyURL) (*TokenResponse, error)`

### TokenRefresher interface + 3 implementations
- `TokenRefresher`: `CanRefresh`, `NeedsRefresh`, `Refresh` — platform-dispatch interface
- `ClaudeTokenRefresher` — `CacheKey`, `CanRefresh`, `NeedsRefresh`, `Refresh`
- `OpenAITokenRefresher` — same + rate-limited NeedsRefresh logic (refresh even if expires_at missing when rate limited)
- `GeminiTokenRefresher` — same

### RefreshTokenCache (Redis, user auth tokens — 8 methods)
`StoreRefreshToken`, `GetRefreshToken`, `DeleteRefreshToken`, `DeleteUserRefreshTokens`, `DeleteTokenFamily`, `AddToUserTokenSet`, `AddToFamilyTokenSet`, `GetUserTokenHashes`, `GetFamilyTokenHashes`, `IsTokenInFamily`

### AuthService OAuth email flow (6 exported functions)
1. `SendPendingOAuthVerifyCode`
2. `VerifyOAuthEmailCode`
3. `RegisterOAuthEmailAccount` — email+code+invitation OAuth-triggered local account creation
4. `RegisterVerifiedOAuthEmailAccount` — pre-verified provider email shortcut
5. `FinalizeOAuthEmailAccount` — invitation redemption + affiliate bind post-creation
6. `RollbackOAuthEmailAccountCreation` — atomic rollback of partial creation + invitation restore
7. `ValidatePasswordCredentials`
8. `RecordSuccessfulLogin`

---

## HUAKAI Coverage Status Table

| sub2api behavior | F-CRED-001 plan covers? | F-AUTH-005 plans cover? | Committed code covers? | Status |
|---|---|---|---|---|
| GenerateAuthURL (PKCE + state, per-vendor) | YES — `POST /admin/v1/credentials/oauth-init` | YES | NO (acquisition handler not committed) | PLAN ONLY |
| ExchangeCode (code→token) | YES — `GET /admin/v1/credentials/oauth-callback` | YES | NO | PLAN ONLY |
| RefreshToken (per-vendor) | YES — credentialworker | YES | YES (AdapterRegistry + scheduler) | COVERED |
| RefreshTokenWithClientID (multi-clientID) | YES — F-CRED-001 row 49 "多 OAuth app clientID" | Implicit in mode registry | NO | PLAN ONLY |
| enrichTokenInfo / ChatGPT accounts/check | NOT MENTIONED | NOT MENTIONED | NO | **GAP** |
| OpenAI privacy-disable (training opt-out) | NOT MENTIONED | NOT MENTIONED | NO | **GAP** |
| RefreshAccountToken (account-level dispatch) | YES | YES (mode handler) | YES (credentialworker refresh) | COVERED |
| BuildAccountCredentials (safe no-overwrite) | YES | YES | YES (credentialstore mode handlers) | COVERED |
| Stop() / lifecycle | YES — "统一 lifecycle manager" | YES | YES (Scheduler.Stop) | COVERED |
| SetPrivacyClientFactory (ImpersonateChrome) | NOT MENTIONED | NOT MENTIONED | NO | **GAP** |
| GeminiOAuthService.GetOAuthConfig (AI Studio gate) | Implicit | Implicit | NO | PLAN ONLY |
| FetchGoogleOneTier (Drive API → tier) | YES — F-CRED-001 row 45, "tier auto-detect" | YES | NO | PLAN ONLY |
| RefreshAccountGoogleOneTier (on-demand) | YES — F-AUTH-005 mentioned | YES | NO | PLAN ONLY |
| Gemini tier canonicalization (8 tier IDs, legacy compat) | PARTIAL — mentions tier detect, not legacy mapping | NOT mentioned | NO | **GAP** |
| Gemini 24h Drive tier cache during refresh | NOT MENTIONED | NOT MENTIONED | NO | **GAP** |
| Gemini fetchProjectID (3-path: LoadCodeAssist + OnboardUser + ResourceManager) | YES (F-CRED-001 mentions retry 3 times) | Partial | NO | PLAN ONLY — 3rd path (ResourceManager fallback) not named |
| Gemini unauthorized_client fallback retry (code_assist ↔ ai_studio) | NOT MENTIONED | NOT MENTIONED | NO | **GAP** |
| AntigravityOAuthService.ValidateRefreshToken | NOT MENTIONED | NOT MENTIONED | NO | **GAP** |
| AntigravityOAuthService.FillProjectID | YES — F-CRED-001 row 44 | YES | NO | PLAN ONLY |
| loadProjectIDWithRetry (retry every refresh, not just acquisition) | PARTIAL — F-CRED-001 mentions acquisition; F-AUTH-005 does not explicitly cover "every refresh calls LoadCodeAssist" | NOT MENTIONED | NO | **GAP** |
| setAntigravityPrivacy (immediate on acquisition) | NOT MENTIONED | NOT MENTIONED | NO | **GAP** |
| ClaudeOAuthClient.GetOrganizationUUID (multi-org, team-preferred) | PARTIAL — F-CRED-001 mentions "1-click Login with Claude" | NOT MENTIONED | NO | **GAP — multi-org selection logic** |
| ClaudeOAuthClient.GetAuthorizationCode (sessionKey flow) | YES | YES | NO | PLAN ONLY |
| ExchangeCodeForToken isSetupToken (1-year expiry) | NOT MENTIONED | NOT MENTIONED | NO | **GAP** |
| TokenRefresher interface (platform dispatch) | YES | YES | YES (ModeAdapterRegistry) | COVERED |
| OpenAITokenRefresher NeedsRefresh rate-limit heuristic | NOT MENTIONED | NOT MENTIONED | NO | **GAP** |
| RefreshTokenCache (Redis, user HUAKAI tokens, family/rotation) | NOT IN SCOPE of F-CRED-001 / F-AUTH-005 | NOT IN SCOPE | No Redis cache for user tokens in HUAKAI yet | SCOPE GAP — different layer |
| AuthService OAuth email flow (6 functions) | NOT IN SCOPE | NOT IN SCOPE | NO | SCOPE GAP — user auth, not upstream credential |

---

## RED FLAGS (Silently Dropped)

**RF-1 (HIGH): ChatGPT accounts/check enrichment + training opt-out**
sub2api `enrichTokenInfo` calls ChatGPT `backend-api/accounts/check` after every token get/refresh to update `plan_type`, `subscription_expires_at`, privacy mode. Neither F-CRED-001 nor F-AUTH-005 Claude or Codex plans mention this behavior. HUAKAI will silently store stale plan_type and never disable training data sharing. This is a functional capability gap for OpenAI OAuth accounts.

**RF-2 (HIGH): Gemini tier legacy canonicalization (8 canonical + 7 legacy IDs)**
sub2api maps 15 tier ID strings (including `AI_PREMIUM`, `GOOGLE_ONE_UNLIMITED`, `STANDARD`, `ENTERPRISE`, kebab variants) to 8 canonical IDs. HUAKAI plans mention "tier auto-detect" but contain no mention of the legacy mapping table. A mismatch here means historical accounts with legacy tier IDs get `""` or wrong rate limits.

**RF-3 (MEDIUM): Gemini unauthorized_client fallback retry (client migration path)**
When a code_assist refresh fails with `unauthorized_client`, sub2api retries with ai_studio path (and vice versa for google_one). This handles operator OAuth client reconfiguration without requiring users to re-authorize. Not mentioned in either HUAKAI plan. Dropped silently.

**RF-4 (MEDIUM): Gemini 24h Drive tier cache during background refresh**
sub2api skips Drive API call during token refresh if `drive_tier_updated_at` is less than 24 hours ago. Without this, every background refresh calls Drive API, increasing latency and rate limit exposure. Not mentioned in HUAKAI plans.

**RF-5 (MEDIUM): Antigravity loadProjectIDWithRetry called on EVERY background refresh**
sub2api `AntigravityOAuthService.RefreshAccountToken` calls `loadProjectIDWithRetry` on every token refresh, not just acquisition. HUAKAI F-CRED-001 mentions project ID fill during acquisition only. F-AUTH-005 does not cover this. If HUAKAI only fills project_id at acquisition, a rotated upstream project assignment will never propagate to existing accounts.

**RF-6 (MEDIUM): AntigravityOAuthService.ValidateRefreshToken**
This is a distinct endpoint used to onboard refresh-token-only accounts (no full OAuth flow). It performs refresh + userInfo + projectID + privacy in one call. Neither HUAKAI plan mentions it. Accounts added via raw refresh token (without OAuth) would lack project_id and user email.

**RF-7 (LOW): isSetupToken / 1-year expiry in Claude ExchangeCodeForToken**
sub2api supports a "setup token" path (1-year expiry) for long-lived automation tokens. Not mentioned in HUAKAI plans. Operators who need non-expiring automation credentials have no path.

**RF-8 (LOW): OpenAITokenRefresher NeedsRefresh rate-limit heuristic**
sub2api refreshes even when `expires_at` is missing if the account is rate-limited (prevents silent expiry during quota window). HUAKAI credentialworker does not cover this edge case.

**RF-9 (LOW): Multi-org selection logic (team org preferred)**
sub2api `GetOrganizationUUID` filters for `raven_type == "team"` when multiple orgs exist. If HUAKAI picks org[0] by default, team accounts with mixed personal/team orgs will authenticate to the wrong org.

---

## Verdict

**FAIL_PRESERVATION**

9 red flags identified. RF-1 (ChatGPT enrichment + privacy), RF-2 (Gemini tier legacy mapping), RF-5 (Antigravity per-refresh project sync) are the most likely to cause silent production failures. RF-3 (Gemini OAuth client migration), RF-4 (Drive API rate exposure), RF-6 (ValidateRefreshToken path), RF-7 (setup token), RF-8 (rate-limit refresh heuristic), RF-9 (multi-org selection) are secondary but real capability gaps.

Codex F-CRED-001 plan is MISSING — the parallel-crossover discipline (CLAUDE.md #10) was not satisfied before this review. Codex plan must be produced independently before these red flags can be considered cross-validated.

**Mandatory actions before implementation:**
1. Address RF-1 through RF-9 in updated F-CRED-001 plan (or create explicit safe-equivalent roadmap entries per Feature Preservation Rule).
2. Produce Codex F-CRED-001 plan independently.
3. Run cross-review after both plans exist.
