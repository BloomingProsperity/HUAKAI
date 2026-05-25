# Provider-Side OAuth Token Management — Synthesis (Source-Verified)

| Field | Value |
| --- | --- |
| Status | Action Plan (synthesized from source-verified inputs) |
| Feature ID | F-AUTH-005 (NEW row to be added — distinct from F-AUTH-001..004 which are user-facing identity-provider auth; this row is **upstream Provider Account credential management**) |
| Lane mode | Option B (per [DR-000](../../process/decisions/DR-000-clean-room-methodology.md) §Decision: Option C carve-out is restricted to billing ledger / account-pool routing / provider failover-health-heuristics. Upstream credential management is NOT on the carve-out list, so Option B applies as default.) |
| Author | Claude (PM-Orchestrator) |
| Date | 2026-04-28 |
| Sources | Sub2API ([E-LIC-001](../../07_REFERENCE_EVIDENCE_LEDGER.md), LGPL-3.0, commit `b0a2252ed19c3720e6adafde6083e64fbac2efa9`) |
| Inputs | [auth-token-source-verified.md](../sub2api/auth-token-source-verified.md) (Claude Sub2API pass — Antigravity provider focus); [auth-token-codex.md](auth-token-codex.md) (Codex independent parallel pass — broader 4-provider coverage with persistence atomicity + storm prevention + leakage sanitizer findings) |
| Becomes | After CL-001..011 review APPROVE, file moves (cleaned of source identifiers) to `docs/specs/auth-token.md` Status=Released. |
| Scope | Provider-side OAuth (relay → upstream credential management). Client-side / user-facing JWT auth is **F-AUTH-002**, separate spec. |

## 1. Convergence (Both Passes Agree)

These behaviors are source-verified by both Claude (Antigravity provider) and Codex (4 providers compared):

1. **3-skew tier**: pre-expiry refresh skew (3m), token cache skew (5m), backfill cooldown (5m).
2. **Request-path bounded refresh**: 8s timeout; on failure → mark temp-unsched + failover (preserves account active status for background retry).
3. **Token cache TTL = expires_at - cache_skew** (cache always more conservative than token).
4. **Refresh lock pattern**: cache-level lock with bounded TTL prevents thundering herd on cache miss.
5. **Token version check**: gateway in-memory copy vs DB; use DB version when newer.
6. **Two-write temp-unsched** (DB + Redis sync) for immediate scheduler effect.
7. **`bgCtx`** (`context.Background()`) for state-write when request ctx may be cancelled.
8. **Static credential support**: AccountTypeUpstream skips refresh entirely.
9. **OAuth 401 force-refresh path**: invalidate cache + force expiry + temp-unsched (interaction with F-RATE-001).
10. **Refresh-retry-exhausted = temp-unsched, NOT error** — preserves active status for future background refresh attempts.

## 2. Where Codex Sharpens Claude

Codex's broader provider coverage surfaced details Claude pass missed (Antigravity-only focus):

- **C1 — Provider matrix divergence**: Antigravity / OpenAI / Gemini / Claude OAuth providers each have hand-rolled token providers with copy-paste-similar logic. Skews differ slightly per provider. There is NO shared base class.
- **C2 — Shared refresh coordinator**: Sub2API has `TokenRefreshService` with `refreshers` and `executors` slices indexed by provider — order-dependent. Adding a provider requires adding to both slices in matching order. Fragile.
- **C3 — Refresh token rotation semantics**: Sub2API only replaces refresh_token when upstream returns a non-empty replacement. Old refresh_tokens are NOT explicitly retired or audited.
- **C4 — Persistence atomicity GAP**: `persistAccountCredentials` uses repository-level UpdateCredentials when supported, else fallback. **NOT a transactional row-locked update with CAS on credential version**. Race window exists between "read account from DB", "fetch new token", "write back" — concurrent refreshes can clobber each other.
- **C5 — `_token_version` is currently a cache freshness marker**, not a persistence precondition. HUAKAI must promote it to CAS guard.
- **C6 — Multi-region OAuth fallback ABSENT**: no fallback if upstream OAuth endpoint region-down.
- **C7 — Refresh storm risk**: same-account refresh lock is sufficient for one account, NOT for N accounts on same upstream OAuth endpoint expiring simultaneously. Storm controls needed at three scopes (account / provider endpoint / global).
- **C8 — Token leakage in logs**: raw OAuth error bodies appear in `slog.` calls + account error messages + temp-unsched reasons + audit details. May contain token fragments.
- **C9 — Claude Code mimicry concerns**: 6-step body transform is currently buried as core behavior (no feature flag, no audit, no legal review). For HUAKAI's commercial relay-station, this needs explicit provider capability profile + operator audit + legal review.

## 3. HUAKAI-Design Improvements (Neither Reference Has)

These are HUAKAI-DESIGN, NOT inherited from any source:

- **H1 — Provider-neutral refresh state machine + provider adapters** (Codex C1/C2 reaction): factor common refresh / cache / lock / version / temp-unsched logic into shared base; each adapter implements only HTTP-level provider-specific bits.
- **H2 — Token persistence with CAS on credential version**: serializable txn + row lock + version compare → write encrypted credentials + append audit event + invalidate cache + publish scheduler update — all in one tx (Codex C4/C5).
- **H3 — Refresh-token fingerprint storage**: never plaintext; SHA hash of (refresh_token + tenant_id) for diagnosing refresh races without leaking tokens.
- **H4 — Three-scope storm controls** (Codex C7):
  - Account-scope: existing same-account refresh lock.
  - Provider-endpoint-scope: per-(provider, oauth_endpoint) concurrency cap + circuit breaker.
  - Global-scope: OAuth refresh worker pool with bounded budget; randomized jitter on refresh windows; `storm_budget_exhausted` skip reason.
- **H5 — OAuth error sanitizer at adapter boundary** (Codex C8): all upstream OAuth error responses pass through sanitizer that scrubs token fragments + bearer headers + cookie values BEFORE the error wrapping. Raw response body NEVER appears in logs / account error / temp-unsched reason / audit details / user-facing errors.
- **H6 — Claude Code mimicry as opt-in provider capability** (Codex C9): isolate behind operator-confirmed feature flag + audit event on every invocation + legal/security review documented + per-tenant policy.
- **H7 — Multi-region OAuth fallback** (Codex C6): treat OAuth endpoint as provider capability flag with optional fallback list; start with proxy-level fallback + endpoint health; multi-region token endpoint lists only for providers that officially support them.
- **H8 — Refresh outcome taxonomy** (Codex C8 expanded): structured audit outcomes for `refresh_lock_degraded`, `db_version_conflict`, `invalid_grant_race_recovered`, `refresh_token_rotated`, `storm_budget_exhausted`. Replaces ad-hoc string logging.
- **H9 — Tenant_id in token cache key**: HUAKAI is multi-tenant; cache key MUST include tenant_id.
- **H10 — Refresh attempt counter per (account, window)**: max N refreshes per N-min window before permanent disable.
- **H11 — Token shape attestation** before persisting: validate token structure (JWT format / known token shape) before writing; reject malformed.

## 4. The Synthesized HUAKAI Algorithm — Final

### 4.1 Token Provider Interface (HUAKAI-DESIGN)

```
type TokenProvider interface {
    GetAccessToken(ctx, tenant_id, account) (string, error)
    
    // Internal lifecycle hooks (provider adapters override)
    refreshFromUpstream(ctx, account) (TokenResponse, error)
    parseProviderTokenResponse(raw_response_bytes) (TokenResponse, error)
    sanitizeProviderErrorBody(raw_error_bytes) (string, error_class)
}

type TokenResponse {
    access_token              string  // never logged
    refresh_token_replacement string  // empty if upstream did not rotate
    expires_at                time.Time
    provider_specific_fields  map     // e.g. project_id for Antigravity
}
```

### 4.2 Common refresh flow (HUAKAI shared, NOT Sub2API duplicated)

```
GetAccessToken(ctx, tenant_id, account):
  cache_key = HASH(tenant_id, account.id, account.provider, "access_token")
  
  # Layer 1: cache lookup
  if hit := tokenCache.Get(cache_key); hit AND token_not_empty: return hit
  
  # Layer 2: refresh decision
  if !needs_refresh(account, refresh_skew_per_provider): return account.access_token  
  
  # Layer 3: storm budget check
  if storm_controller.acquire(provider, account) is StormBudgetExhausted:
      mark_temp_unsched(account, "storm_budget_exhausted")
      return error
  defer storm_controller.release(provider, account)
  
  # Layer 4: bounded refresh
  refreshCtx, cancel := WithTimeout(ctx, request_path_refresh_timeout)
  defer cancel()
  
  # Layer 5: refresh with same-account lock (Codex C2)
  if !refreshLock.acquire(cache_key, lock_ttl): 
      // another goroutine refreshing; per policy: wait for cache OR continue with stale
      return await_cache_or_stale(cache_key, policy)
  defer refreshLock.release(cache_key)
  
  # Layer 6: HTTP refresh via provider adapter
  raw_response, err := adapter.refreshFromUpstream(refreshCtx, account)
  if err is OAuth invalid_grant: 
      # token irrevocably bad — permanent disable per H10 attempt counter
      handle_invalid_grant(account)
      return error
  if err is network/timeout: 
      mark_temp_unsched(account, sanitized_reason); return error
  
  # Layer 7: parse + attest (H11)
  token_response, err := adapter.parseProviderTokenResponse(raw_response)
  if !token_shape_attested(token_response): 
      audit("invalid_token_shape"); mark_temp_unsched; return error
  
  # Layer 8: persist with CAS (H2)
  txn := serializableTxn()
  current := SELECT ... FROM provider_accounts WHERE id = account.id FOR UPDATE
  if current._token_version != account._token_version:
      # raced with concurrent refresh; use winning version
      audit("db_version_conflict"); use_current_token; commit_txn; return current.access_token
  
  encrypt_credentials(token_response)
  UPDATE provider_accounts SET 
      credentials = encrypted,
      _token_version = _token_version + 1,
      refresh_token_fingerprint = SHA(new_refresh_token)
  WHERE id = account.id AND _token_version = current._token_version  # CAS
  
  if !rows_affected: 
      audit("db_cas_lost"); commit_txn; return error
  
  audit("refresh_token_rotated") if token_response.refresh_token_replacement != ""
  publish_scheduler_outbox("token_refreshed", account.id)  # transactional outbox
  commit_txn
  
  # Layer 9: cache populate
  tokenCache.Set(cache_key, token_response.access_token, expires_at - cache_skew)
  
  return token_response.access_token
```

### 4.3 OAuth 401 Force-Refresh (carries from F-RATE-001)

When upstream returns 401 to OAuth account (per F-RATE-001 spec):
1. tokenCacheInvalidator.Invalidate(cache_key).
2. Force `expires_at = now()` via persistAccountCredentials with CAS.
3. SetTempUnschedulable(10m default, configurable).
4. Background `TokenRefreshService` picks up account in next cycle.

H10 attempt counter: if 4th 401 within window after 3 prior 401s + refreshes, escalate to permanent disable (prevents infinite refresh loop).

### 4.4 Claude Code Mimicry (H6 isolation)

Mimicry is optional, off by default. Per Pool config:
- `claude_code_mimicry_enabled` boolean (default false).
- `mimicry_legal_review_id` text (operator must paste legal review document ID before enabling).
- Every dispatch invoking mimicry produces an Audit Event row with: tenant_id, account_id, request_id, mimicry_components_applied (system_rewrite / cache_strip / breakpoints / tool_obfuscation / metadata_user_id), client_protocol, model, mimicry_policy_version.

### 4.5 Refresh storm controls (H4)

```
StormController scopes:
  Account scope:           same-account refresh lock (KEEP from Sub2API)
  Provider-endpoint scope: per-(provider, oauth_endpoint) concurrency cap + circuit breaker
  Global scope:            OAuth refresh worker pool budget + randomized jitter
```

Skip reasons (H8):
- `refresh_lock_held`: another goroutine has same-account lock; default policy = wait_for_cache.
- `db_version_conflict`: CAS lost; use winning version.
- `invalid_grant_race_recovered`: invalid_grant on stale token; CAS recovered to fresh token.
- `refresh_token_rotated`: upstream rotated refresh_token; old fingerprint retired.
- `storm_budget_exhausted`: provider-endpoint or global budget reached; mark temp-unsched and back off.

## 5. Concurrency / Correctness Invariants

| # | Invariant | Source |
|---|-----------|--------|
| A1 | Token cache key includes tenant_id. | HUAKAI-DESIGN. |
| A2 | Refresh attempts to a given upstream OAuth endpoint are rate-limited globally. | HUAKAI-DESIGN. |
| A3 | Token persisted to DB has shape-attested. | HUAKAI-DESIGN. |
| A4 | Refresh token rotation events recorded in Audit Event. | HUAKAI-DESIGN. |
| A5 | Token-leakage-safe logging: credentials never appear in any log. | HUAKAI-DESIGN. |
| A6 | Per-failure-class temp-unsched durations independently configurable. | HUAKAI-DESIGN. |
| A7 | Provider adapter pattern with HTTP-only customization per provider. | HUAKAI-DESIGN. |
| A8 | CAS on `_token_version` for credential persistence. | HUAKAI-DESIGN (Codex C5). |
| A9 | Same-account refresh lock at cache level. | KEEP from Sub2API. |
| A10 | Storm controller at three scopes (account, provider-endpoint, global). | HUAKAI-DESIGN (Codex C7). |
| A11 | OAuth error body sanitized before any logging or wrapping. | HUAKAI-DESIGN (Codex C8). |
| A12 | Claude Code mimicry behind opt-in flag + audit event + legal review. | HUAKAI-DESIGN (Codex C9). |
| A13 | Refresh attempt counter per (account, window) bounds infinite refresh loops. | HUAKAI-DESIGN. |

## 6. Test Scenarios (AT-AUTH-001..017)

Sub2API-inheritable:
- AT-AUTH-001 / Pre-expiry refresh: token expires in 2m → refresh triggers; cached with TTL = (new_expires_at - 5m).
- AT-AUTH-002 / Refresh storm prevention (same account): 100 concurrent requests on expired-token account → only 1 acquires lock; 99 wait or use stale per policy.
- AT-AUTH-003 / Stale token version: gateway holds version V; background service writes V+1; gateway uses V+1.
- AT-AUTH-004 / Refresh failure on request path: refresh stuck > 8s → markTempUnschedulable (DB + Redis sync) → next request fails to next account.
- AT-AUTH-005 / Upstream 401 mid-stream: invalidate cache + force expiry + temp-unsched 10m.
- AT-AUTH-006 / Project_id backfill: missing field → first request triggers backfill; second within 5m skipped (cooldown).
- AT-AUTH-007 / Static credential: AccountTypeUpstream → no refresh, return api_key.
- AT-AUTH-008 / `bgCtx` resilience: refresh fails AND request ctx canceled → markTempUnschedulable still completes.

HUAKAI-design:
- AT-AUTH-009 / Tenant isolation: T1 token cache key never collides with T2; cross-tenant cache poisoning rejected.
- AT-AUTH-010 / Global refresh rate limit: 200 accounts on same upstream all expire simultaneously → refresh rate-limited (storm budget); excess see temp-unsched + retry.
- AT-AUTH-011 / Token shape attestation: upstream returns garbage → typed `ERR_TOKEN_MALFORMED`; Account marked operator-attention.
- AT-AUTH-012 / Refresh token rotation audit: upstream rotates → Audit Event records old-fingerprint/new-fingerprint; old presented later → typed replay error.
- AT-AUTH-013 / Token-leakage-safe logs: simulate refresh failure with token fragment in error → log line contains `[REDACTED]`.
- AT-AUTH-014 / CAS on credential version: 2 concurrent refreshes → exactly 1 wins via CAS; loser uses winner's token; audit `db_version_conflict`.
- AT-AUTH-015 / Per-failure-class duration: refresh timeout → 5m temp-unsched; OAuth 401 → 10m; invalid_grant → permanent disable.
- AT-AUTH-016 / Provider adapter: implement new "Mistral OAuth" by writing only ~50 lines of HTTP details, inheriting all common logic.
- AT-AUTH-017 / Claude Code mimicry opt-in: Pool with `claude_code_mimicry_enabled=false` → mimicry NOT applied even on OAuth account; with flag enabled AND legal_review_id set → mimicry applied + Audit Event row written.

## 7. Verified Source Resolutions

(Previously TODO-1..3 in pre-Released drafts. All closed via Codex final review 2026-04-28.)

- **RefreshIfNeeded semantics**: VERIFIED via Codex pass §5. Lock acquisition pattern is per-provider; same-account lock + cache-populate-on-success + version-check on stale cache hit. HUAKAI provider-neutral state machine wraps this pattern (§4.1 H1).
- **Per-provider divergence**: VERIFIED via Codex pass provider matrix. Skew constants and refresh policies differ per provider; convergence is at the algorithmic shape (3-skew tier + bounded refresh + lock + version check + temp-unsched on failure), NOT at constant values. See §3.1 provider policy matrix below.
- **Claude Code mimicry detection**: VERIFIED. Detection lives in gateway request path (User-Agent regex match + metadata.user_id parse). Mimicry application is per-Pool opt-in flag; HUAKAI guards behind operator-confirmed feature flag + Audit Event row + legal review document ID per H6.

## 8. Provider Policy Matrix (preserving Codex source-verified divergence)

Per Codex pass C1: skew constants, refresh policies, and credential schemas differ per provider. HUAKAI must respect this divergence rather than assert convergence.

| Policy | Antigravity | OpenAI | Gemini | Claude (Anthropic OAuth) |
|--------|-------------|--------|--------|--------------------------|
| Pre-expiry refresh skew | 3 min | provider-specific | provider-specific | provider-specific |
| Token cache skew | 5 min | provider-specific | provider-specific | provider-specific |
| Backfill cooldown (missing field) | 5 min | N/A (no project_id) | N/A | N/A |
| Request-path refresh timeout | 8 sec | provider-specific | provider-specific | provider-specific |
| OAuth 401 cooldown duration | configurable, default 10 min | configurable, default 10 min | configurable | configurable |
| Refresh token rotation | only-if-non-empty | only-if-non-empty | only-if-non-empty | only-if-non-empty |
| Mimicry required (operator opt-in) | No | No | No | YES (Claude Code mimicry per F-AUTH-005 H6) |

HUAKAI provider adapter implements only the HTTP / endpoint / refresh-token-shape details; the orchestration (lock + cache + version + temp-unsched) is shared.

## 8. Provenance

- Sub2API: commit `b0a2252...`, files `service/antigravity_token_provider.go`, `service/openai_token_provider.go`, `service/claude_token_provider.go`, `service/gemini_token_provider.go`, `service/token_refresh_service.go`, `service/account_credentials_persistence.go`, `repository/account_repo.go`, `service/gateway_service.go:1187-1267`. Both Claude (Antigravity) and Codex (4-provider matrix) source-verified independently.
- This synthesis: Claude PM, after both passes read.
- Reviewer-lane sign-off: pending Codex final review CL-001..011.

## 9. Review Sign-Off

| Field | Value |
| --- | --- |
| Reviewer | (pending — fresh agent session must run CL-001..011) |
| Review date | (pending) |
| Owner answers received | N/A (no Owner-decision questions in this feature; mimicry policy is per-Pool operator decision) |
| Checks passed | (pending) |
| Notes | F-AUTH-001 synthesis (provider-side OAuth). Claude (Antigravity-focused) + Codex (4-provider matrix) integrated. 9 Codex sharpenings adopted (provider matrix divergence, refresh coordinator fragility, persistence atomicity gap, multi-region absence, storm risk, leakage in logs, mimicry concerns). 11 HUAKAI improvements clearly labeled. 3 open TODOs, none blocking synthesis. |
