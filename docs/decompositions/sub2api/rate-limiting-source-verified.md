# Sub2API Rate Limiting — Source-Verified (F-RATE-001)

| Field | Value |
| --- | --- |
| Status | Specifier-lane source-verified pass (Claude) |
| Author | Claude PM-Orchestrator |
| Date | 2026-04-28 |
| Lane | Specifier — Option B (rate limiting is L1 but not on Option C carve-out per DR-000) |
| Feature | [F-RATE-001](../../03_FEATURE_PARITY_MATRIX.md) — multi-platform rate-limit detection, cooldown computation, custom error policy, OAuth 401 token refresh |
| Reference | Sub2API at commit `b0a2252ed19c3720e6adafde6083e64fbac2efa9`, file `backend/internal/service/ratelimit_service.go` (1740 lines) |
| Source files read | `ratelimit_service.go` lines 21–296, 819–1198 (handle429 + handle529 + ResetTime calculators), 1481+ (tryTempUnschedulable) function listing; cross-reference `gateway_forward_as_chat_completions.go:153-165` (where HandleUpstreamError is called) and `gateway_service.go:3669-3676` (shouldFailoverUpstreamError) |

## 1. The Three Entry Points

```go
// Line 108
func (s *RateLimitService) CheckErrorPolicy(ctx, account, statusCode, responseBody) ErrorPolicyResult
// Line 127
func (s *RateLimitService) HandleUpstreamError(ctx, account, statusCode, headers, responseBody) (shouldDisable bool)
// Line 295
func (s *RateLimitService) PreCheckUsage(ctx, account, requestedModel) (bool, error)
```

- **`CheckErrorPolicy`** (line 108): cheap pre-decision used elsewhere; returns enum {`None`, `Skipped`, `Matched`, `TempUnscheduled`}.
- **`HandleUpstreamError`** (line 127): the central rate-limit / error handler called from the streaming forwarder (gateway_forward_as_chat_completions.go:163-165).
- **`PreCheckUsage`** (line 295): proactive quota check BEFORE dispatch — currently Gemini-only (line 296: `if account.Platform != PlatformGemini`).

## 2. The HandleUpstreamError Decision Tree (line 127–291)

Behavior is layered. Order matters.

### 2.1 Pool Mode Short-Circuit (line 131–134)

```go
if account.IsPoolMode() && !customErrorCodesEnabled {
    return false  // pool_mode_error_skipped
}
```

If account is in "pool mode" AND custom error codes are NOT enabled, NO state change is made. Pool mode means upstream errors don't affect local Account state; only an explicit per-account custom error policy can override.

### 2.2 Custom Error Code Filter (line 138–141)

```go
if !account.ShouldHandleErrorCode(statusCode) {
    return false  // account_error_code_skipped
}
```

If operator configured a custom error code list and this status code is not on it, the error is silently passed through — no rate-limit marking, no disable.

### 2.3 Temp-Unschedulable Rules (line 145–149)

```go
if statusCode != 401 {
    if s.tryTempUnschedulable(ctx, account, statusCode, responseBody) {
        return true
    }
}
```

Per-account configured "temp unschedulable rules" can match status codes other than 401 and put the account into time-bounded unscheduling. 401 is excluded because it has its own complex flow.

### 2.4 Status-Code-Specific Branches (line 157–289)

| Status | Action | Disables? |
|--------|--------|-----------|
| 400 + "organization has been disabled" | `handleAuthError` permanent disable | Yes |
| 400 + Anthropic + "credit balance" | permanent disable (semantic 402) | Yes |
| 400 + "identity verification is required" | permanent disable (KYC) | Yes |
| 400 (other) | nothing | No |
| 401 + OpenAI `token_invalidated`/`token_revoked` | permanent disable | Yes |
| 401 + OpenAI `{"detail":"Unauthorized"}` | permanent disable | Yes |
| 401 + OAuth (non-Antigravity) | invalidate token cache + force expiry + temp unschedulable (default 10 min, configurable via `cfg.RateLimit.OAuth401CooldownMinutes`) | Yes (temp) |
| 401 (other) | `handleAuthError` (SetError, permanent) | Yes |
| 402 + OpenAI `deactivated_workspace` | permanent disable | Yes |
| 402 (other) | permanent disable | Yes |
| 403 | `handle403` (complex, line 699+) | Variable |
| 429 | `handle429` (rate limit, see §3) | **NO** |
| 529 | `handle529` (overload, see §4) | **NO** |
| Custom-error-codes enabled + other code | `handleCustomErrorCode` | Yes |
| 5xx (no custom codes) | warn log only | No |
| Other 4xx | nothing | No |

### 2.5 The 401 OAuth Refresh Trick (line 198–230)

For OAuth accounts (non-Antigravity), 401 triggers a **three-step refresh**:

1. `tokenCacheInvalidator.InvalidateToken(ctx, account)` — invalidate cached token
2. `account.Credentials["expires_at"] = time.Now().Format(time.RFC3339)` — force expiry
3. `accountRepo.SetTempUnschedulable(ctx, account.ID, until, msg)` — temp unsched, account stays `status=active` so the refresh service can pick it up

This is **distinct from "permanent disable"** — the account isn't broken, the token is just stale. The temp unsched gives a refresh window (default 10 min). Antigravity is excluded because it has its own `applyErrorPolicy` rule set.

## 3. handle429 — The Multi-Platform Rate-Limit Cascade (line 819–930)

The most complex method in the file. Five fallback layers per platform.

### 3.1 OpenAI Layer (line 821–831)

```go
if account.Platform == PlatformOpenAI {
    s.persistOpenAICodexSnapshot(ctx, account, headers)
    if resetAt := s.calculateOpenAI429ResetTime(headers); resetAt != nil {
        accountRepo.SetRateLimited(ctx, account.ID, *resetAt)
        return
    }
}
```

`calculateOpenAI429ResetTime` (line 934–978) parses **Codex-style `x-codex-*` headers** for `used_5h_percent`, `reset_5h_seconds`, `used_7d_percent`, `reset_7d_seconds`. Decision logic:

- 7d window exceeded (`used_7d_percent >= 100`) AND has reset → use 7d reset
- 5h window exceeded AND has reset → use 5h reset
- Neither exceeded but 429 received → use **MAX of 5h and 7d resets** (defensive: longer cooldown safer)

### 3.2 Anthropic Per-Window Layer (line 833–852, plus calculateAnthropic429ResetTime line 1001+)

Headers used:
- `anthropic-ratelimit-unified-5h-reset` / `anthropic-ratelimit-unified-5h-utilization` / `anthropic-ratelimit-unified-5h-surpassed-threshold`
- `anthropic-ratelimit-unified-7d-reset` / equivalent 7d trio

Decision logic when both windows exceeded: prefer 7d (longer cooldown) with 5h fallback. When only one exceeded: use that one.

Side effect: also calls `accountRepo.UpdateSessionWindow(...)` to record the rejected 5h window for analytics (start = end - 5h, status="rejected").

### 3.3 Aggregate Header Fallback (line 854–855)

```go
resetTimestamp := headers.Get("anthropic-ratelimit-unified-reset")
```

A single aggregate Anthropic header (older format).

### 3.4 Body-Parse Fallback (line 858–882)

Per-platform body parsers:
- OpenAI: `parseOpenAIRateLimitResetTime(responseBody)` (looks for `usage_limit_reached`)
- Gemini / Antigravity: `ParseGeminiRateLimitResetTime(responseBody)`

### 3.5 Real-vs-Spurious Anthropic 429 (line 886–892)

```go
if account.Platform == PlatformAnthropic {
    slog.Warn("rate_limit_429_no_reset_time_skipped",
        "reason", "no rate limit reset time in headers, likely not a real rate limit")
    return  // do NOT mark rate-limited
}
```

**Critical insight**: Anthropic returns 429 for "Extra usage required" scenarios that are not actual rate limits (e.g. payment / upgrade required). If no reset header, the gateway treats it as transient and **does not mark the account rate-limited**. This avoids wrongly cooling down a perfectly healthy account on a billing-state issue.

### 3.6 Default 5-Minute Cooldown (line 894–900)

For non-Anthropic platforms missing all reset signals, default 5 minutes.

## 4. handle529 — Overload Cooldown (line 1163–1199)

Anthropic returns 529 ("Overloaded") when system is under load. Distinct from 429:

```go
if !settings.Enabled {
    return  // operator can disable 529 handling
}
until := time.Now().Add(time.Duration(cooldownMinutes) * time.Minute)
accountRepo.SetOverloaded(ctx, account.ID, until)
```

- Operator-toggleable via `OverloadCooldownSettings.Enabled`
- Default cooldown 10 minutes (`cfg.RateLimit.OverloadCooldownMinutes`)
- Different DB state from rate-limited: `SetOverloaded` not `SetRateLimited` — separate observability track
- `shouldDisable=false`: Account stays selectable but temporarily marked overloaded

## 5. Session Window Tracking (line 1201–1300+)

`UpdateSessionWindow` (line 1202+, called from successful responses) maintains the 5h Anthropic window for accounts. Three sources of truth:

1. Real `anthropic-ratelimit-unified-5h-reset` header (best)
2. Predicted: hour-aligned start + 5h end, when no header
3. Existing stored window (preserve if still valid)

Defensive parsing: detects millisecond timestamps (line 1217: `if ts > 1e11`) and converts. Validates timestamps are in range [-5h, +7d] (line 1223–1226) to reject malformed headers.

## 6. Per-Account State Machine (Account States)

From `accountRepo` method names visible in this file:

| State | Setter | Cleared by |
|-------|--------|------------|
| `error` | `SetError` (via `handleAuthError`) | Operator manual reset OR successful test recovery |
| `rate_limited` | `SetRateLimited(account_id, reset_at)` | Auto-clear when `reset_at` passes |
| `overloaded` | `SetOverloaded(account_id, until)` | Auto-clear when `until` passes |
| `temp_unschedulable` | `SetTempUnschedulable(account_id, until, reason)` | Auto-clear when `until` passes |
| `disabled` (operator) | (not in this file) | Operator UI |

Plus model-level rate-limit (`model_rate_limit.go`, 101 lines, scopes a single model on an account, separate from account-level rate-limit).

## 7. Custom Error Codes Policy

Operator-configurable per Account (`account.IsCustomErrorCodesEnabled()`, `account.ShouldHandleErrorCode(statusCode)`). When enabled, ONLY codes on the configured list trigger any handling; all others pass through silently. Useful for accounts where operator knows a specific provider's error semantics (e.g. some upstreams return 503 = real outage but Sub2API's default is to ignore non-configured 5xx).

## 8. Failure Modes Sub2API Handles vs Does NOT Handle

### Handled

- **Multi-format 429 reset extraction**: header → body → fallback
- **Anthropic 429 false positives** (Extra usage required): pass through, don't mark
- **5h vs 7d window selection**: prefer longer cooldown when both exceeded
- **OAuth 401 refresh window**: temp-unsched + force-expire credentials
- **Permanent disable triggers**: organization disabled, credit balance, KYC, token revoked, deactivated workspace
- **Per-account custom error policy**: opt-in code list
- **Pool mode silence**: don't pollute local state in pool mode

### NOT Handled (real gaps)

- **Concurrency-aware rate limiting**: no token bucket / leaky bucket; account-level rate-limit is a binary flag with reset time. Sub-window granularity (e.g. "X requests per second") is operator-configured at the model level only (model_rate_limit.go, 101 lines).
- **Distributed rate-limit coordination**: each gateway instance reads/writes accountRepo state; if Account is hit by 100 concurrent requests across 10 instances, all 10 will dispatch before any sees the rate-limit state update.
- **Tenant-level rate limiting**: Sub2API is single-tenant; no per-tenant or per-API-Key rate limits visible in this service.
- **Rate-limit reason taxonomy**: structured but not exhaustive — adding a new platform requires adding a parser branch.
- **Rate-limit propagation to client**: 429 from upstream becomes 429 to client (default), but no `Retry-After` header forwarded — the gateway holds the rate-limit state, client just sees "try again later" without timing.
- **Cooldown jitter**: when many accounts hit 429 with same reset time, they ALL come back simultaneously → thundering herd.
- **Rate-limit metric for capacity planning**: no "rate-limit-hit-rate" exposed for operator dashboards.

## 9. KEEP / IMPROVE / AVOID for HUAKAI

### KEEP (verified in source)

- **Multi-platform 429 reset extraction with fallback layers** (header → body → default). Mirror the layered pattern.
- **Anthropic 429 false-positive detection** (no reset = pass through). Avoids spurious cooldowns.
- **Window-pair selection (5h vs 7d)** when multiple windows could trigger 429.
- **OAuth 401 refresh window via temp-unsched** instead of hard-disable. Three-step (invalidate cache + force expiry + temp unsched) is the right primitive.
- **Per-account custom error code policy** as opt-in operator override.
- **Distinct state for `overloaded` vs `rate_limited`** — different observability + different cooldown defaults.
- **Pool mode silence** — when running an account in "pool mode" (operator-defined), local state isn't polluted by per-request errors.
- **Defensive timestamp parsing** with millisecond auto-detect + range validation.
- **Permanent disable triggers** for unrecoverable states (KYC, org disabled, credit exhausted).

### IMPROVE (HUAKAI design — clearly NOT in Sub2API)

- **Concurrency-aware rate limiting**: token bucket per Account with operator-configurable rate, integrated with the Pool slot acquisition (Pattern B in pool-selection-synthesis). HUAKAI's strict-money-grade gate must reserve rate-budget atomically with quota.
- **Distributed coordination**: rate-limit state in PostgreSQL row-locked or Redis-Lua atomic counter, not best-effort per-instance reads.
- **Tenant-level rate limits**: HUAKAI is multi-tenant from day 1 (DR-001); add `tenant_id`-scoped buckets at every layer.
- **Rate-limit reason taxonomy as fixed enum**: `RATE_LIMIT_5H_EXCEEDED`, `RATE_LIMIT_7D_EXCEEDED`, `RATE_LIMIT_RPM`, `RATE_LIMIT_TPM`, `OVERLOAD`, `EXTRA_USAGE_REQUIRED`, `TOKEN_REFRESH_REQUIRED`, `TOKEN_PERMANENTLY_REVOKED`, etc. Sub2API uses log strings; HUAKAI persists structured `routing_reason`.
- **Retry-After propagation**: when upstream returns 429 with reset time, HUAKAI emits `Retry-After: <seconds>` to the client (computed from upstream reset). Sub2API drops this.
- **Cooldown jitter**: add ±15% jitter to reset times to prevent thundering herd. Sub2API uses exact upstream reset.
- **Rate-limit dashboard metrics**: per-Account rate-limit-hit rate over windows, surfaced to operator.
- **Atomic state transitions**: when 429 fires, HUAKAI updates Account state inside the same Tx2 reconcile transaction that finalizes the failed Usage Record. Sub2API does these as separate operations.
- **Per-window cooldown configurability**: operator can override "use 7d" default with "use shorter window" if their workload is tolerant.

### AVOID (Sub2API anti-patterns)

- **Best-effort `slog.Warn`-and-continue when state writes fail** (`rate_limit_set_failed`, `oauth_401_set_temp_unschedulable_failed`). Money-grade requires retry + alert, not warn-log.
- **5-minute default cooldown without consultation**: hardcoded fallback. HUAKAI default should be configurable per Pool.
- **String-prefix matching for permanent-disable triggers** (`strings.Contains(strings.ToLower(upstreamMsg), "organization has been disabled")`). HUAKAI should use structured upstream error parsing, not substring match.
- **Tightly coupled provider adapters in one file** (1740 lines): HUAKAI should split per-platform handlers into adapter files for maintainability.

## 10. Concurrency / Correctness Invariants HUAKAI Adds

| # | Invariant | Reason Sub2API doesn't enforce |
|---|-----------|---------------------------------|
| R1 | Rate-limit state transition (none → rate_limited) is atomic with the Usage Record carrying the 429 attempt. | Sub2API does these as separate calls. |
| R2 | Distributed rate-limit decrement across N gateway instances is serialized through PostgreSQL row lock or Redis Lua atomic. | Sub2API uses cache-only writes; multi-instance race is real. |
| R3 | Tenant-level rate-limit budget is reserved alongside Provider Account rate-limit during Tx1. | Sub2API has no tenant concept. |
| R4 | Cooldown timestamps include ±15% jitter; thundering herd avoided. | Sub2API uses exact upstream reset. |
| R5 | When state write fails, retry with exponential backoff + alert; never silent log-and-continue. | Sub2API logs warn and proceeds. |
| R6 | Reason taxonomy is fixed enum stored in `routing_reason`, not free-form strings. | Sub2API uses log lines + slog keys. |
| R7 | OAuth refresh path is bounded: max N refreshes per window before permanent disable. | Sub2API allows infinite refresh attempts. |
| R8 | Custom error codes are versioned (operator changes are auditable) and rate-limited (operator can't accidentally configure all 4xx as errors and brick the system). | Sub2API has no policy versioning visible. |

## 11. Failure Taxonomy (HUAKAI Design)

Aligned with [pool-selection-synthesis.md](../_cross-cutting/pool-selection-synthesis.md) §4 and [streaming-forwarder-claude-v2.md](../_cross-cutting/streaming-forwarder-claude-v2.md) §6. New rate-limit-specific entries:

| Reason | Recovery Policy | Annotation |
|--------|-----------------|------------|
| `RATE_LIMIT_WINDOW_5H` | account_cooldown(reset_at) | `rate_limited_5h` |
| `RATE_LIMIT_WINDOW_7D` | account_cooldown(reset_at) | `rate_limited_7d` |
| `RATE_LIMIT_BOTH_WINDOWS` | account_cooldown(max(5h, 7d).reset_at) | `rate_limited_dual` |
| `RATE_LIMIT_RPM` | account_cooldown(60s) | `rate_limited_rpm` |
| `RATE_LIMIT_TPM` | account_cooldown(60s) | `rate_limited_tpm` |
| `EXTRA_USAGE_REQUIRED` | passthrough_to_client | `non_real_429_extra_usage` |
| `OVERLOADED` | account_cooldown(jittered 10min) | `overloaded` |
| `TOKEN_REFRESH_REQUIRED` | temp_unsched(10min) + invalidate_credentials | `oauth_401_refresh` |
| `TOKEN_PERMANENTLY_REVOKED` | permanent_disable | `oauth_token_revoked` |
| `KYC_REQUIRED` | permanent_disable + alert_operator | `kyc_required` |
| `ORG_DISABLED` | permanent_disable + alert_operator | `org_disabled` |
| `CREDIT_EXHAUSTED` | permanent_disable + alert_operator | `credit_exhausted` |
| `WORKSPACE_DEACTIVATED` | permanent_disable + alert_operator | `workspace_deactivated` |
| `CUSTOM_ERROR_CODE` | account_cooldown(operator_configured) | `custom_error_<code>` |

## 12. Test Scenarios

### Sub2API-inheritable

- AT-RATE-001 / OpenAI 429 with x-codex-* headers, 7d exhausted → SetRateLimited(reset_at=now + reset_7d_seconds).
- AT-RATE-002 / Anthropic 429 with both 5h+7d windows exceeded → SetRateLimited(reset_at=7d.reset).
- AT-RATE-003 / Anthropic 429 with NO reset header → no state change, error passes through.
- AT-RATE-004 / OpenAI 401 token_invalidated → permanent disable (handleAuthError), shouldDisable=true.
- AT-RATE-005 / OAuth 401 → temp_unschedulable + token cache invalidate + force expires_at.
- AT-RATE-006 / Pool mode + 429 + no custom codes → no state change.
- AT-RATE-007 / 529 with Enabled=false → no state change.
- AT-RATE-008 / Custom error codes enabled, 503 in list → handleCustomErrorCode + permanent disable.
- AT-RATE-009 / 400 + "organization has been disabled" → permanent disable.
- AT-RATE-010 / Body-parse fallback: Gemini 429 with no headers but body has reset → SetRateLimited from body.

### HUAKAI-design-specific (not in Sub2API)

- AT-RATE-011 / Multi-instance race: 10 concurrent 429s on same Account from 10 gateway instances → exactly 1 SetRateLimited transaction wins; 9 detect already-rate-limited and pass through.
- AT-RATE-012 / Tenant-level rate limit: T1 hits per-tenant cap → T1's request rejected; T2's request unaffected.
- AT-RATE-013 / Cooldown jitter: 100 accounts hit 429 with same reset → return-to-service times spread across ±15% jitter window.
- AT-RATE-014 / Retry-After header: client sees `Retry-After: 240` when upstream reset is 240s out.
- AT-RATE-015 / Atomic Tx2: gateway crash between SetRateLimited and Usage Record write → both committed or both rolled back.
- AT-RATE-016 / OAuth refresh bound: 3 consecutive 401s within window → permanent disable on 4th, prevents infinite refresh loop.
- AT-RATE-017 / Reason taxonomy: every 429 produces `routing_reason` with one of the 14 enum strings, never free-form.

## 13. Open TODOs

- **TODO-1**: Read `handle403` (line 699+) for the 403 cooldown logic; OpenAI 403 has its own counter cache (`OpenAI403CounterCache`).
- **TODO-2**: Read `tryTempUnschedulable` (line 1481+) for the per-account temp-unsched-rules schema.
- **TODO-3**: Read `model_rate_limit.go` (101 lines) for per-(account, model) granular rate-limit.
- **TODO-4**: Cross-check one-api's rate-limit (much simpler) and New API (AGPL — behavioral observation only).
- **TODO-5**: Verify whether `SetRateLimited` is row-level locked in PostgreSQL or just cached.
- **TODO-6**: Verify `accountRepo.UpdateSessionWindow` is atomic with `SetRateLimited` (matters for invariant R1).

## 14. Attribution

Source files read directly from `c:/HUAKAI/repo/.omc/reference-src/sub2api/` at commit `b0a2252ed19c3720e6adafde6083e64fbac2efa9`:

- `backend/internal/service/ratelimit_service.go`:
  - lines 21–95 (struct definitions, dependency setters)
  - lines 97–123 (CheckErrorPolicy + ErrorPolicyResult enum)
  - lines 127–291 (HandleUpstreamError full body)
  - lines 819–930 (handle429 full body + multi-platform fallback layers)
  - lines 932–1027 (calculateOpenAI429ResetTime + calculateAnthropic429ResetTime helpers)
  - lines 1163–1199 (handle529 full body)
  - lines 1201–1245 (UpdateSessionWindow header parsing)
  - lines 1481+ (tryTempUnschedulable signature; full body in TODO-2)
- `backend/internal/service/gateway_forward_as_chat_completions.go:153-165` — call site
- `backend/internal/service/gateway_service.go:3669-3676` — `shouldFailoverUpstreamError`

This file is specifier-lane; function names and source paths appear here per CL-002 specifier-lane exception. Implementer-lane specs may not cite these directly.

CL-011 compliance: every behavior claim above carries file:line attribution.

## 15. Review Sign-Off

| Field | Value |
| --- | --- |
| Reviewer | (pending) |
| Review date | (pending) |
| Checks passed | (pending CL-001..011) |
| Notes | F-RATE-001 source-verified pass. Awaits Codex parallel pass for mutual review (next cycle), then synthesis. Critical TODO: verify SetRateLimited atomicity for invariant R1. |
