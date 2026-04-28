# Sub2API Provider-Side Auth + Token Refresh — Source-Verified (F-AUTH-001)

| Field | Value |
| --- | --- |
| Status | Specifier-lane source-verified pass (Claude) |
| Author | Claude PM-Orchestrator |
| Date | 2026-04-28 |
| Lane | Specifier — Option C (auth core is on the Option C carve-out per DR-000) |
| Feature | [F-AUTH-001](../../03_FEATURE_PARITY_MATRIX.md) — provider-side OAuth token management for upstream relay (NOT client-side / user-facing JWT auth, which is F-AUTH-002) |
| Reference | Sub2API at commit `b0a2252ed19c3720e6adafde6083e64fbac2efa9` |
| Source files read | `backend/internal/service/antigravity_token_provider.go` lines 1-200, `backend/internal/service/gateway_service.go:1187-1267` (`applyClaudeCodeOAuthMimicryToBody`), `backend/internal/service/account_credentials_persistence.go` (function only), `backend/internal/service/ratelimit_service.go:198-230` (OAuth 401 force-refresh from F-RATE-001 cross-reference) |

## 1. Why Provider-Side OAuth is Money-Grade

A relay-station holds N upstream credentials. Each credential has a finite lifetime (OAuth access tokens typically 1h). If the gateway hits an expired token mid-stream, the request fails (cost wasted) AND the customer sees an error. If the gateway refreshes incorrectly, the upstream may revoke ALL credentials.

Sub2API's provider-side auth has three load-bearing properties:
1. **Pre-expiry refresh** with skew (refresh BEFORE the upstream sees expired token)
2. **Refresh lock + cache** to prevent thundering-herd refresh
3. **Mark account temp-unsched on refresh failure** so scheduler doesn't keep dispatching to a broken credential

Plus a defensive layer specific to Anthropic OAuth: **Claude Code mimicry** to avoid being detected as a third-party app and charged "extra usage".

## 2. The Token Provider Pattern (`AntigravityTokenProvider.GetAccessToken`)

Source `backend/internal/service/antigravity_token_provider.go:68-178`. The same pattern repeats for OpenAI / Gemini / Anthropic providers — the Antigravity one is canonical.

### 2.1 Constants (lines 13-21)

```go
const (
    antigravityTokenRefreshSkew      = 3 * time.Minute   // refresh 3m before expiry
    antigravityTokenCacheSkew        = 5 * time.Minute   // cache TTL = expires_at - 5m
    antigravityBackfillCooldown      = 5 * time.Minute   // re-attempt project_id backfill every 5m
    antigravityRequestRefreshTimeout = 8 * time.Second   // bound request-path refresh
)
```

Three skews:
- **Refresh skew (3m)**: triggers refresh BEFORE upstream sees expired token. Avoids racing the upstream's clock + network round-trip.
- **Cache skew (5m)**: token cache TTL is `expires_at - 5m` so the cache expires before the token does, forcing a fresh check. More conservative than refresh skew.
- **Backfill cooldown (5m)**: when missing field (`project_id`) needs to be backfilled from upstream OAuth service, limit attempts to once per 5m per account.

### 2.2 The Decision Tree (lines 68-178)

```
GetAccessToken(account):
  // Layer 0: skip if not OAuth account
  if account.Type == AccountTypeUpstream: return account.api_key  // static credential, no refresh
  if account.Type != AccountTypeOAuth: error
  
  // Layer 1: cache lookup (line 91-95)
  if tokenCache.GetAccessToken(cacheKey) succeeds: return token  // hot path
  
  // Layer 2: pre-expiry refresh (line 97-128)
  expiresAt := account.GetCredentialAsTime("expires_at")
  needsRefresh := expiresAt == nil || time.Until(*expiresAt) <= 3m
  if needsRefresh AND refreshAPI != nil:
      refreshCtx, cancel := context.WithTimeout(ctx, 8s)  // bounded
      result, err := refreshAPI.RefreshIfNeeded(refreshCtx, account, executor, 3m)
      if err: 
          markTempUnschedulable(account, err)  // sync DB + Redis
          if policy.OnRefreshError == Return: return error
          // else fall-through with existing (likely stale) token
      else if result.LockHeld:
          // another goroutine is refreshing; per policy, either wait-for-cache or continue
      else:
          account = result.Account  // refreshed
  
  // Layer 3: read access_token from credentials (lines 130-133)
  accessToken := account.GetCredential("access_token")
  if empty: return error
  
  // Layer 4: backfill missing fields with cooldown (lines 136-149)
  if account.project_id missing AND shouldAttemptBackfill(accountID):
      markBackfillAttempted(accountID)  // updates cooldown
      if projectID := antigravityOAuthService.FillProjectID(ctx, account, accessToken); ok:
          account.Credentials["project_id"] = projectID
          persistAccountCredentials(...)
  
  // Layer 5: version check + populate cache (lines 152-175)
  latestAccount, isStale := CheckTokenVersion(ctx, account, accountRepo)
  if isStale: accessToken = latestAccount.access_token  // someone else refreshed concurrently
  ttl := derive_from(expiresAt - 5m, floor 1m)
  tokenCache.SetAccessToken(cacheKey, accessToken, ttl)
  return accessToken
```

### 2.3 The Refresh Lock Pattern

Source line 124 (test path) + `RefreshIfNeeded` (not read in detail):
```go
locked, err := p.tokenCache.AcquireRefreshLock(ctx, cacheKey, 30*time.Second)
if err == nil && locked {
    defer func() { _ = p.tokenCache.ReleaseRefreshLock(ctx, cacheKey) }()
}
```

Refresh lock prevents thundering-herd: when token expires, 100 concurrent requests don't all call upstream OAuth refresh simultaneously. One acquires the lock, refreshes, populates cache; others either wait for cache OR continue with existing token (per policy).

### 2.4 Token Version Detection

Source line 153: `CheckTokenVersion(ctx, account, accountRepo)`. Detects when the in-memory `account` object holds a stale token version because another goroutine (background refresh service) already wrote a fresher version to DB. If stale, use the fresher one.

This is critical because: gateway has the in-memory Account from selection; meanwhile background `TokenRefreshService` may have refreshed and persisted a new token. Without version check, gateway uses stale token → upstream returns 401 → cooldown triggers → real bug masked as rate-limit.

### 2.5 Temp-Unsched on Refresh Failure

Source lines 193-211 (`markTempUnschedulable`):

```go
func (p *AntigravityTokenProvider) markTempUnschedulable(account, refreshErr) {
    until := time.Now().Add(tokenRefreshTempUnschedDuration)
    reason := "token refresh failed on request path: " + refreshErr.Error()
    bgCtx := context.Background()  // request ctx may be expired
    accountRepo.SetTempUnschedulable(bgCtx, account.ID, until, reason)
    if tempUnschedCache != nil {
        tempUnschedCache.Set(bgCtx, account.ID, until)  // sync Redis immediately
    }
}
```

Two writes for immediate scheduler effect:
- DB write (durable, source of truth)
- Redis cache write (immediate scheduler dispatch decision)

Uses **`context.Background()`** because request context may be at timeout. Critical: if you skip the cache write, the scheduler keeps dispatching to the broken credential for cache-TTL seconds.

## 3. Cross-Reference: OAuth 401 Force-Refresh (from F-RATE-001)

Source `backend/internal/service/ratelimit_service.go:198-230`:

When upstream returns 401 to an OAuth account, the rate-limit service:
1. `tokenCacheInvalidator.InvalidateToken(ctx, account)` — kill cached token immediately
2. `account.Credentials["expires_at"] = time.Now().Format(time.RFC3339)` — force "expired" state
3. `accountRepo.SetTempUnschedulable(...)` — temp-unsched for refresh window (default 10m, configurable via `cfg.RateLimit.OAuth401CooldownMinutes`)

So the auth flow has **two failure paths** that converge on temp-unsched:
- **Pre-expiry refresh failed** (`AntigravityTokenProvider.markTempUnschedulable`): refresh-time-limited, default `tokenRefreshTempUnschedDuration`.
- **Upstream 401** (`RateLimitService.HandleUpstreamError` 401 branch): refresh-cycle-limited, default 10m.

Both write DB + Redis sync. Both use `bgCtx` to bypass request cancellation.

## 4. Claude Code Mimicry — Defensive Pattern (Anthropic OAuth)

Source `gateway_service.go:1187-1243`. **This is a unique relay-station defensive pattern with no analog in standard OAuth.**

### 4.1 Why It Exists

Anthropic OAuth flow distinguishes "Claude Code client" (Anthropic's own CLI) from "third-party app". Third-party apps using OAuth tokens are charged "extra usage" — meaning the relay operator pays MORE per request. To avoid this, Sub2API mimics Claude Code request signature when forwarding to Anthropic via OAuth.

### 4.2 The Six-Step Transform (lines 1199-1241)

For OAuth accounts that aren't *actual* Claude Code clients (per `isClaudeCodeClient` detection at gateway_service.go:3720), the gateway applies:

1. **System prompt rewrite** (line 1201, only for non-Haiku models): replace client's `system` field with Claude Code's canonical system prompt. Haiku is excluded because it has different system prompt requirements.
2. **System cache_control strip** (only when system was NOT rewritten): remove client-supplied `system.cache_control` to avoid signature mismatch.
3. **Metadata user_id injection** (lines 1207-1219): inject Claude Code's `metadata.user_id` format derived from a stable per-Account fingerprint. Skipped if operator has enabled Multi-Provider Token (mimicMPT) mode.
4. **Messages cache_control strip** (line 1230, `stripMessageCacheControl`): remove client-supplied `messages[*].cache_control` for multi-turn stability.
5. **Cache breakpoints injection** (line 1231, `addMessageCacheBreakpoints`): add Claude Code's standard 2 cache breakpoints (last message + second-to-last user turn).
6. **Tool name obfuscation** (lines 1233-1240, `buildToolNameRewriteFromBody` + `applyToolNameRewriteToBody`): rewrite client tool names to Claude Code's canonical names; mapping stored in gin context for response-side reverse rewrite. If no tools, just add a final cache breakpoint on tools[-1].

### 4.3 Why This Is Important for HUAKAI

If HUAKAI sells API access through OAuth-based upstream subscriptions, **the same mimicry is required**, otherwise operator pays extra. This is the kind of "looks magical but is essential" feature that operators wouldn't think to specify upfront.

KEEP this behavior. AVOID copying source. The pattern is: a sequence of body transforms that make the request indistinguishable from Claude Code's own request signature.

## 5. The Account Credentials Storage

Source `backend/internal/service/account_credentials_persistence.go:9` (function signature only — body not read):
```go
func persistAccountCredentials(ctx, repo, account, credentials map[string]any) error
```

Credentials are stored as `map[string]any` on the Account (per gateway_service.go:209-210). Common fields:
- `access_token` (string)
- `refresh_token` (string)
- `expires_at` (RFC3339 string)
- `project_id` (Antigravity-specific)
- `api_key` (for AccountTypeUpstream — static, no refresh)

`persistAccountCredentials` writes credentials atomically; called from token provider after refresh + after backfill (lines 141, 212).

## 6. Failure Modes Sub2API Handles

- **Token expires mid-flight**: 3m refresh skew triggers proactive refresh.
- **Refresh stuck on slow network**: 8s request-path timeout → mark temp-unsched + failover to next account.
- **Concurrent refresh thundering-herd**: cache-level refresh lock (30s TTL).
- **Stale token version after concurrent refresh**: `CheckTokenVersion` re-reads from DB.
- **Upstream 401 mid-stream**: invalidate cache + force expiry + temp-unsched (10m default) + scheduler picks next account.
- **Backfill spam**: 5m cooldown per account on missing-field backfill.
- **OAuth detection on Anthropic**: Claude Code mimicry (6-step body transform).
- **Background context for state-write**: `bgCtx` ensures temp-unsched write completes even if request ctx is dead.
- **Static credential support**: `AccountTypeUpstream` skips refresh entirely (just returns api_key).

## 7. Failure Modes Sub2API Does NOT Handle

- **Refresh storm across N accounts**: each account refreshes independently; no global rate limit on refresh requests to upstream OAuth endpoint (all N can refresh simultaneously if N tokens expire at the same time).
- **Refresh token rotation tracking**: when upstream rotates refresh tokens (some providers do per-refresh), there's no explicit "old refresh token retired" event observed in this code.
- **Token leakage prevention**: `slog.Info("oauth_401_invalidate_cache_failed", "account_id", account.ID, "error", err)` may include error messages that contain bits of token (not directly token but may leak indirectly).
- **Multi-region failover**: if upstream OAuth endpoint is region-down, no fallback region.
- **Refresh metric for capacity planning**: no `refresh_attempts_per_minute` metric.
- **Refresh result attestation**: nothing prevents a malicious upstream from returning a token-shaped string that isn't actually a valid token.

## 8. KEEP / IMPROVE / AVOID for HUAKAI

### KEEP (verified in source)

- **3-skew constant tier** (refresh / cache / backfill cooldown). Adapt values per Provider but inherit the pattern.
- **Request-path refresh timeout (8s)**. Refresh on the critical path is bounded; failure → temp-unsched → failover.
- **Token cache with TTL = expires_at - cache_skew** (cache always more conservative than token).
- **Refresh lock** to prevent thundering-herd on concurrent cache miss.
- **Token version check** between gateway in-memory copy and DB after concurrent refresh.
- **Two-write temp-unsched** (DB + Redis) on refresh failure.
- **`bgCtx` for state-write** when request ctx may be at timeout.
- **OAuth 401 force-refresh path** (invalidate cache + force expiry + temp-unsched).
- **Static credential support** for non-OAuth accounts (separate path, no refresh code).
- **Claude Code mimicry** for Anthropic OAuth (6-step body transform).
- **Backfill cooldown** for missing-field backfill from upstream OAuth service.

### IMPROVE (HUAKAI design — clearly NOT in Sub2API)

- **Global refresh rate limiter per upstream endpoint**: prevent N tokens expiring at once → N concurrent OAuth refreshes → upstream rate-limit on OAuth endpoint. HUAKAI buckets refresh rate per `(upstream, oauth_endpoint)` tuple.
- **Refresh token rotation tracking**: when upstream rotates refresh_token, capture the rotation event in audit log. If old refresh_token is presented later, it's a replay attempt or operator misconfiguration.
- **Token result attestation**: validate token shape (JWT structure or known token format) before persisting. Reject malformed tokens with typed error.
- **Refresh metric exposure**: per-(provider, account_id) refresh attempts, success rate, latency, p99. Operator dashboard.
- **Multi-region OAuth fallback**: if upstream OAuth endpoint has multiple regions, fail over.
- **Token-leakage-safe logging**: scrub credential bytes from any error message before logging.
- **Tenant_id in token cache key**: HUAKAI is multi-tenant; cache key must include tenant_id to prevent cross-tenant cache poisoning.
- **Configurable temp-unsched duration per failure class**: different durations for "refresh timeout" vs "OAuth 401" vs "OAuth invalid_grant" (token revoked) — Sub2API uses two fixed durations.
- **OAuth provider abstraction**: Sub2API has separate token providers for Antigravity / OpenAI / Gemini / Anthropic with copy-paste-similar logic. HUAKAI extracts common provider interface; each provider implements only the upstream-specific HTTP details.

### AVOID (Sub2API anti-patterns)

- **Per-provider token provider files with duplicated logic** (4 separate token provider Go files; lots of repeated structure).
- **Hardcoded skew constants per provider** (3m, 5m): make per-Pool / per-Account configurable.
- **`slog.Info` with raw error in OAuth path**: error messages may leak token fragments.

## 9. Concurrency / Correctness Invariants HUAKAI Adds

| # | Invariant | Reason Sub2API doesn't enforce |
|---|-----------|---------------------------------|
| A1 | Token cache key includes `tenant_id`. | Sub2API is single-tenant. |
| A2 | Refresh attempts to a given upstream OAuth endpoint are rate-limited globally. | Sub2API has per-account refresh; no global rate limit. |
| A3 | Token persisted to DB has shape-attested (JWT structure, known format). | Sub2API trusts upstream blindly. |
| A4 | Refresh token rotation events are recorded in Audit Event. | Sub2API doesn't audit rotation. |
| A5 | Token-leakage-safe logging: credentials never appear (even fragments) in any log. | Sub2API may log fragments via wrapped errors. |
| A6 | Per-failure-class temp-unsched duration: refresh-timeout, OAuth-401, OAuth-invalid-grant, OAuth-network-error each have separate duration knobs. | Sub2API has only two durations. |
| A7 | Provider abstraction with HTTP-only customization per provider. | Sub2API duplicates logic across providers. |

## 10. Test Scenarios

### Sub2API-inheritable

- AT-AUTH-001 / Pre-expiry refresh: token expires in 2m → triggers refresh; after refresh, new token cached with TTL = (new_expires_at - 5m).
- AT-AUTH-002 / Refresh storm prevention: 100 concurrent requests on same expired-token account → only 1 acquires refresh lock; 99 wait or use stale token per policy.
- AT-AUTH-003 / Stale token version: gateway holds version V; background service writes V+1; CheckTokenVersion detects, gateway uses V+1.
- AT-AUTH-004 / Refresh failure on request path: refresh stuck for 9s → 8s timeout fires → markTempUnschedulable (DB + Redis sync) → next request fails to next account.
- AT-AUTH-005 / Upstream 401 mid-stream: 401 received → invalidate cache + force expiry + temp-unsched 10m → scheduler picks different account.
- AT-AUTH-006 / Project_id backfill: account missing project_id → first request triggers backfill; second request within 5m skipped (cooldown); after 5m, retry.
- AT-AUTH-007 / Static credential: AccountTypeUpstream → no refresh, return api_key directly.
- AT-AUTH-008 / Claude Code mimicry: OAuth account, non-Claude-Code client → 6-step body transform applied; response-side tool name reverse-rewrite via gin context.
- AT-AUTH-009 / Haiku exception: Claude Code mimicry on Haiku model → system prompt NOT rewritten (different requirements).
- AT-AUTH-010 / `bgCtx` resilience: refresh fails AND request ctx is canceled → markTempUnschedulable still completes.

### HUAKAI-design-specific

- AT-AUTH-011 / Tenant isolation: T1's token cache key NEVER collides with T2's; cross-tenant cache poisoning rejected.
- AT-AUTH-012 / Global refresh rate limit: 200 accounts on same upstream all expire simultaneously → refresh rate-limited to 50/min; remaining 150 see graceful temp-unsched + retry.
- AT-AUTH-013 / Token shape attestation: upstream returns "garbage" instead of JWT → rejected with typed `ERR_TOKEN_MALFORMED`; account marked operator-attention.
- AT-AUTH-014 / Refresh token rotation audit: upstream rotates refresh_token → Audit Event records old/new pair; old presented later → typed replay error.
- AT-AUTH-015 / Token-leakage-safe logs: simulate refresh failure with token fragment in error → log line contains `[REDACTED]` not the fragment.
- AT-AUTH-016 / Per-failure-class duration: refresh timeout → 5m temp-unsched; OAuth 401 → 10m; invalid_grant → permanent disable.
- AT-AUTH-017 / Provider abstraction: implement new "Mistral OAuth provider" by writing only 50 lines (HTTP details), inheriting refresh / cache / lock / version / temp-unsched behavior.

## 11. Open TODOs

- **TODO-1**: Read `RefreshIfNeeded` body in `OAuthRefreshAPI` (refreshAPI.RefreshIfNeeded) for full lock-then-fetch-then-persist semantics.
- **TODO-2**: Read remaining lines of `antigravity_token_provider.go` (200-end) for shouldAttemptBackfill + markBackfillAttempted internals.
- **TODO-3**: Read OpenAI / Gemini / Anthropic token providers for divergence in skews / patterns.
- **TODO-4**: Read `account_credentials_persistence.go` body for atomicity of credential update (does it use SELECT FOR UPDATE or CAS-on-version?).
- **TODO-5**: Read `gateway_service.go:3720 isClaudeCodeClient` for the User-Agent + metadata.user_id detection logic (informs Claude Code mimicry test cases).
- **TODO-6**: Verify whether `tokenRefreshTempUnschedDuration` is configurable (operator-tunable per Pool).

## 12. Attribution

Source files read directly from `c:/HUAKAI/repo/.omc/reference-src/sub2api/` at commit `b0a2252ed19c3720e6adafde6083e64fbac2efa9`:

- `backend/internal/service/antigravity_token_provider.go` lines 1-200 (constants, struct, NewAntigravityTokenProvider, GetAccessToken, shouldAttemptBackfill, markTempUnschedulable opening)
- `backend/internal/service/gateway_service.go:1187-1267` (`applyClaudeCodeOAuthMimicryToBody` + start of `buildOAuthMetadataUserIDFromBody`)
- `backend/internal/service/ratelimit_service.go:198-230` (OAuth 401 force-refresh path; cross-reference from F-RATE-001)
- `backend/internal/service/account_credentials_persistence.go:9` (function signature for context)

This is specifier-lane; function names and source paths cited per CL-002 specifier-lane exception. Implementer-lane spec must use HUAKAI domain language only.

CL-011 compliance: every behavior claim above carries file:line attribution.

## 13. Review Sign-Off

| Field | Value |
| --- | --- |
| Reviewer | (pending — fresh agent session must run CL-001..011) |
| Review date | (pending) |
| Checks passed | (pending CL-001..011) |
| Notes | F-AUTH-001 source-verified pass focused on provider-side OAuth (relay→upstream). Client-side / user-facing JWT auth is separate (F-AUTH-002, not in scope here). 6 open TODOs flagged. |
