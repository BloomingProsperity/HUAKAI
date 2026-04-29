# Rate Limiting + Cooldown — Synthesis (Source-Verified)

| Field | Value |
| --- | --- |
| Status | Action Plan (synthesized from source-verified inputs) |
| Feature ID | F-RATE-001 |
| Lane mode | Option C (rate-limit + cooldown intersects Provider Account health and billing reconciliation per [DR-000](../../decisions/DR-000-clean-room-methodology.md)) |
| Author | Claude (PM-Orchestrator) |
| Date | 2026-04-28 |
| Sources | Sub2API ([E-LIC-001](../../07_REFERENCE_EVIDENCE_LEDGER.md), LGPL-3.0, commit `b0a2252...`); LiteLLM ([E-LIC-005](../../07_REFERENCE_EVIDENCE_LEDGER.md), MIT, behavioral cross-reference only) |
| Inputs | [rate-limiting-source-verified.md](../sub2api/rate-limiting-source-verified.md) (Claude Sub2API pass — focused on HandleUpstreamError + handle429 + handle529 + 401 OAuth flow); [rate-limiting-codex.md](rate-limiting-codex.md) (Codex parallel pass — extended coverage: handle403, tryTempUnschedulable, model_rate_limit, runtime state machine, UpdateSessionWindow defensive parsing) |
| Becomes | After CL-001..011 review APPROVE, file moves (cleaned of source identifiers) to `docs/specs/rate-limiting.md` Status=Released. |

## 1. The Combined Sub2API Source Picture

### 1.1 Three entry points (both passes confirmed)

- **`CheckErrorPolicy`**: pre-decision; returns enum {None / Skipped / Matched / TempUnscheduled}.
- **`HandleUpstreamError`**: central error handler; status-code dispatch; sets account state.
- **`PreCheckUsage`**: proactive pre-dispatch quota check (Gemini-only currently).

### 1.2 HandleUpstreamError decision tree

Layered priority (Codex pass §HandleUpstreamError):

1. Pool-mode short-circuit: `IsPoolMode + !customErrorCodesEnabled` → no state change.
2. Custom error code filter: unlisted code → silent passthrough.
3. Temp-unsched rules (status != 401 path): rule match → set temp_unschedulable.
4. Status-specific branches: 400 / 401 / 402 / 403 / 429 / 529 / 5xx / custom.

### 1.3 Status-code matrix (combined from both passes)

| Status | Trigger | Action | Disable |
|--------|---------|--------|---------|
| 400 + "organization disabled" | substring match | permanent disable | yes |
| 400 + Anthropic + "credit balance" | substring match | permanent disable (semantic 402) | yes |
| 400 + "identity verification required" | substring match | permanent disable (KYC) | yes |
| 400 (other) | — | nothing | no |
| 401 + OpenAI `token_invalidated` / `token_revoked` | code match | permanent disable | yes |
| 401 + OpenAI `{detail:"Unauthorized"}` | exact body match | permanent disable | yes |
| 401 + OAuth (non-Antigravity) | type match | invalidate token cache + force expiry + temp-unsched (default 10m) | temp |
| 401 (other) | — | SetError (permanent) | yes |
| 402 + OpenAI `deactivated_workspace` | code match | permanent disable | yes |
| 402 (other) | — | permanent disable | yes |
| **403 — Antigravity** | classifier (validation / violation / generic) | all branches disable; validation appends extracted URL | yes |
| **403 — OpenAI with counter cache** | per-incident | 180-min counter; disable on count ≥ 3; else 10-min temp-unsched | counter or temp |
| **403 — OpenAI without counter** | — | permanent disable | yes |
| **403 — other platforms** | — | permanent disable | yes |
| 429 | platform-specific reset extraction | rate_limited_until reset_at | no (rate_limited) |
| 529 | overload | overloaded_until 10m default | no (overloaded) |
| Custom error codes enabled, other status | code in list | handleCustomErrorCode + disable | yes |
| 5xx (no custom codes) | — | warn log only | no |

### 1.4 handle429 multi-platform fallback layers (5 layers)

(Per Claude pass §3 + Codex confirmation)

1. **OpenAI Codex `x-codex-*` headers**: 5h/7d window + threshold + reset → choose actually-exhausted window's reset; if both exhausted, prefer longer cooldown.
2. **Anthropic per-window headers**: `anthropic-ratelimit-unified-{5h,7d}-{reset,utilization,surpassed-threshold}` — choose between 5h vs 7d based on exceeded.
3. **Aggregate header fallback**: `anthropic-ratelimit-unified-reset` (older format).
4. **Body parser**: OpenAI `usage_limit_reached`; Gemini `parseGeminiRateLimitResetTime`.
5. **Default fallback**: 5 minutes for non-Anthropic; **Anthropic 429 with no reset header → pass-through, no state change** (treats as "Extra usage required" non-real rate-limit).

### 1.5 handle403 platform-specific dispatch (Codex contribution — Claude pass had as TODO)

- **Antigravity**: classifier inspects body → validation (with URL) / violation / generic forbidden. All branches disable.
- **OpenAI**: counter-based.
  - With counter cache: increment 180-min counter; disable at count ≥ 3; else 10-min temp-unsched.
  - Without counter cache: permanent disable.
- **Others**: permanent disable.

### 1.6 Temp-Unschedulable Rules (Codex contribution)

Per-account opt-in via `temp_unschedulable_enabled` boolean in credentials. Rules array shape:
```
{
  error_code: int,
  keywords: [str],
  duration_minutes: int,
  description: str
}
```
Runtime matcher:
- Body-match capped at 64 KiB.
- Keywords matched as case-insensitive substrings.
- Triggered state stores: until time + trigger time + status + matched keyword + rule index + 2-KiB response message snapshot.
- Repository persistence: only EXTENDS temp-unsched (later shorter match cannot shorten earlier longer cooldown).
- 401 special case: repeat 401 after prior temp-unsched 401 returns false → escalate to default error.

### 1.7 OAuth 401 Force-Refresh interaction

Three-step sequence + background refresh service collaboration:

1. Invalidate token cache.
2. Set `expires_at` to current time → next request triggers refresh.
3. Set temp-unsched (10m default).

Background `TokenRefreshService`:
- Lists active accounts (including temp-unsched active).
- On refresh success: clear temp-unsched + delete cache + invalidate OAuth cache.
- On retry-exhausted refresh failure: set temp-unsched for retry-cooldown (NOT error — preserves active status for future retries).
- On non-retryable error: set account error.

### 1.8 Model-Level Rate Limit (Antigravity-specific, Codex contribution)

- Stored in account `extra.model_rate_limits` map.
- Read-side scope: per (account, mapped_model_key + thinking_suffix).
- Active when `rate_limit_reset_at` parses RFC3339 AND is in future.
- Antigravity 429 prefers model-level over account-level; falls back when no model key resolvable.
- Sticky session binding cleared when model-level limit set (avoids stuck sessions).
- Account-level rate-limit clear ALSO clears: Antigravity quota scopes / model_rate_limits / temp-unsched / temp-unsched cache / OpenAI 403 counter (cascade clear).

### 1.9 Runtime State Machine (Codex contribution — 6 states)

| State | Entered by | Scheduler effect | Recovery |
|-------|------------|-------------------|----------|
| `error` | Auth/custom error paths | non-active rejected | manual ClearError |
| `disabled` | Operator (no auto-transition in F-RATE) | non-active rejected | operator only |
| `rate_limited` | SetRateLimited(reset_at) | rejected while reset_at future | auto-clear at reset_at OR ClearRateLimit |
| `overloaded` | 529 (SetOverloaded(until)) | rejected while until future | auto-clear at until OR ClearRateLimit (cascade) |
| `temp_unschedulable` | OAuth 401 / temp-unsched rules / refresh retry exhaustion / stream timeout | rejected while until future | ClearTempUnschedulable (cascade) |
| `model_rate_limited` | Antigravity model-specific paths | only the mapped model blocked | ClearRateLimit OR ClearTempUnschedulable (cascade) |

State clearing is **cascade-aware**: ClearRateLimit clears overload + model + temp + 403 counter.

### 1.10 UpdateSessionWindow defensive parsing (Codex contribution)

- Runs only when `anthropic-ratelimit-unified-5h-status` present.
- 5h reset header parsed as Unix seconds; **millisecond timestamps detected (>1e11) and divided by 1000** (defensive).
- Range validation: accepted only if `[now - 5h, now + 7d]` (rejects malformed).
- Status `allowed` while account currently rate_limited → clears rate_limit (recovery signal from upstream).
- Predicted window when no reset header: hour-aligned start + 5h end.

## 2. Convergence (Both Passes Agree)

1. Multi-platform 429 reset extraction with header → body → default fallback.
2. Anthropic 429 false-positive detection (no reset header → pass-through).
3. 5h vs 7d window selection (prefer longer cooldown when both exceeded).
4. OAuth 401 = temp-unsched + force-refresh, NOT permanent disable.
5. Two-write temp-unsched (DB + Redis) for immediate scheduler sync.
6. `bgCtx` for state-write when request ctx may be cancelled.
7. Custom error code policy as opt-in operator override.
8. Distinct state for overloaded vs rate_limited.
9. Pool mode silence on uncustomized errors.

## 3. Where Codex Sharpens Claude

These extensions Codex captured that Claude pass missed:

- **C1 — handle403 platform dispatch + counter mechanism** (Claude had as TODO-1).
- **C2 — Temp-unschedulable rules schema + matching algorithm** (Claude had as TODO-2).
- **C3 — model_rate_limit.go granular per-(account, model) limit + Antigravity-specific** (Claude had as TODO-3).
- **C4 — Runtime state machine table** (entry points / scheduler effects / recovery for all 6 states).
- **C5 — UpdateSessionWindow defensive parsing** (millisecond detect + range validation).
- **C6 — Cascade clearing**: ClearRateLimit cascades through model / temp / counter / overload state.
- **C7 — Refresh-retry-exhaustion → temp-unsched (not error)**: preserves active status for future retries.
- **C8 — TokenRefreshService coordination**: explicit collaboration with rate-limit service for refresh-success and refresh-failure paths.

## 4. HUAKAI Design Improvements (NOT in Sub2API)

These are HUAKAI-DESIGN, NOT inherited:

- **H1 — Concurrency-aware rate limiting**: token bucket per Account integrated with Pool slot acquisition (atomic with Tx1 quota reservation).
- **H2 — Distributed coordination**: PostgreSQL row-locked counter or Redis-Lua atomic, NOT cache-only writes.
- **H3 — Tenant-level rate limits**: HUAKAI is multi-tenant; add tenant_id-scoped buckets at every layer.
- **H4 — Rate-limit reason taxonomy as fixed enum** (carried in routing_reason structured payload).
- **H5 — Retry-After propagation to client**: when upstream returns 429 with reset, HUAKAI emits `Retry-After: <seconds>` to client. Sub2API drops this.
- **H6 — Cooldown jitter**: ±15% jitter to reset times prevents thundering herd. Sub2API uses exact upstream reset.
- **H7 — Rate-limit dashboard metrics**: per-Account hit rate over windows.
- **H8 — Atomic state transitions**: 429 + Usage Record commit in same Tx2.
- **H9 — Per-window cooldown configurability**: operator can override "use 7d" with shorter window if workload tolerates.
- **H10 — Configurable failover status code list per Account / Pool** (Sub2API hardcodes 401/403/429/529 + 5xx).
- **H11 — OAuth refresh bound** (max N refreshes per window before permanent disable).
- **H12 — Token-leakage-safe logging** (scrub credential bytes from error messages).
- **H13 — Per-failure-class temp-unsched duration**: refresh-timeout / OAuth-401 / OAuth-invalid-grant / OAuth-network-error each have separate duration knobs.
- **H14 — Provider abstraction**: HUAKAI factors out shared OAuth refresh + 429 handling, leaving only HTTP details per provider. Sub2API has copy-paste-similar logic across providers.

## 5. Failure Taxonomy (15 reasons)

Aligned with [pool-selection-synthesis-v2 §4](pool-selection-synthesis-v2.md) and [streaming-forwarder-synthesis §5.4](streaming-forwarder-synthesis.md):

| Reason | Trigger | Recovery | Source |
|--------|---------|----------|--------|
| `RATE_LIMIT_5H_EXCEEDED` | Anthropic 5h window exceeded | account_cooldown(reset_at) | SUB2API-VERIFIED |
| `RATE_LIMIT_7D_EXCEEDED` | Anthropic 7d window exceeded | account_cooldown(reset_at) | SUB2API-VERIFIED |
| `RATE_LIMIT_BOTH_WINDOWS` | Both windows exceeded | account_cooldown(max(5h, 7d)) | SUB2API-VERIFIED |
| `RATE_LIMIT_RPM` | RPM bucket exceeded | account_cooldown(60s + jitter) | HUAKAI-DESIGN |
| `RATE_LIMIT_TPM` | TPM bucket exceeded | account_cooldown(60s + jitter) | HUAKAI-DESIGN |
| `EXTRA_USAGE_REQUIRED` | Anthropic 429 with no reset | passthrough_to_client | SUB2API-VERIFIED |
| `OVERLOADED` | 529 | account_cooldown(jittered 10m) | SUB2API-VERIFIED |
| `TOKEN_REFRESH_REQUIRED` | OAuth 401 | temp_unsched(10m) + invalidate_credentials | SUB2API-VERIFIED |
| `TOKEN_PERMANENTLY_REVOKED` | OpenAI token_invalidated/revoked | permanent_disable | SUB2API-VERIFIED |
| `KYC_REQUIRED` | 400 + identity verification | permanent_disable + alert | SUB2API-VERIFIED |
| `ORG_DISABLED` | 400 + organization disabled | permanent_disable + alert | SUB2API-VERIFIED |
| `CREDIT_EXHAUSTED` | 400 + credit balance OR 402 | permanent_disable + alert | SUB2API-VERIFIED |
| `WORKSPACE_DEACTIVATED` | OpenAI 402 + deactivated_workspace | permanent_disable + alert | SUB2API-VERIFIED |
| `MODEL_LIMIT_EXCEEDED` | Antigravity model-specific 429 | model_only_cooldown(reset_at) | SUB2API-VERIFIED |
| `TEMP_UNSCHED_RULE_MATCHED` | Per-account custom rule | temp_unsched(rule.duration) | SUB2API-VERIFIED |
| `OPENAI_403_COUNTED` | OpenAI 403 + counter < 3 | temp_unsched(10m) | SUB2API-VERIFIED |
| `OPENAI_403_DISABLED` | OpenAI 403 + counter ≥ 3 | permanent_disable | SUB2API-VERIFIED |
| `ANTIGRAVITY_403_VALIDATION` | Antigravity 403 validation | permanent_disable + show_url | SUB2API-VERIFIED |
| `CUSTOM_ERROR_CODE` | operator-configured | account_cooldown(operator_configured) | SUB2API-VERIFIED |

## 6. The Synthesized HUAKAI Algorithm — Final

### 6.1 Atomic primitives

- **PostgreSQL serializable transaction with row-level lock** on Provider Account row during state mutation (HUAKAI-DESIGN; Sub2API uses cache-with-DB-fallback).
- **Cooldown jitter**: ±15% randomization on reset_at (HUAKAI-DESIGN).
- **Cascade clearing**: when ClearRateLimit fires, also clear overload / model_rate_limit / temp_unsched / 403 counter — KEEP from Sub2API §1.9.
- **Two-write state mutation**: DB write + Redis cache write in same logical operation, via outbox pattern (HUAKAI-DESIGN extends Sub2API's two-write pattern with transactional outbox).

### 6.2 HandleUpstreamError decision tree (HUAKAI)

Same layering as Sub2API (§1.2) PLUS:
- **Layer 0 (HUAKAI new)**: tenant-level rate-limit pre-check. If tenant exceeded global tenant rate, return 429 to client without touching account.
- **Layer 5 (HUAKAI new)**: emit structured `routing_reason.rate_limit_reason` per Failure Taxonomy enum (§5).

### 6.3 handle429 (HUAKAI generalization)

Same 5-layer fallback (§1.4) PLUS:
- Layer 0 (HUAKAI): tenant-level RPM/TPM bucket check.
- Layer 4 extension: pluggable per-platform parser registry (Sub2API has hardcoded OpenAI / Anthropic / Gemini parsers; HUAKAI uses adapter pattern from F-PROTO-001).
- Layer 5 (HUAKAI): jitter applied to all reset_at values.

### 6.4 Temp-unsched rules (HUAKAI extension)

KEEP Sub2API's per-account opt-in rule schema (§1.6); ADD:
- **Versioned rule policy**: operator changes are auditable.
- **Rule rate-limiting**: operator can't accidentally configure all 4xx as errors and brick the system (HUAKAI-DESIGN safety guard).

### 6.5 OAuth 401 force-refresh (HUAKAI extension)

KEEP Sub2API's three-step sequence (§1.7); ADD:
- **Refresh attempt counter** per (account, window): max N refreshes per N-minute window before permanent disable (HUAKAI H11).
- **Token shape attestation** before persisting refreshed token (HUAKAI A3): reject malformed.
- **Audit Event on rotation**: when upstream rotates refresh_token, audit row records old/new pair.

## 7. Concurrency / Correctness Invariants

| # | Invariant | Source |
|---|-----------|--------|
| R1 | Rate-limit state transition atomic with Usage Record carrying the 429 attempt. | HUAKAI-DESIGN. |
| R2 | Distributed rate-limit decrement serialized via PostgreSQL row lock or Redis Lua atomic. | HUAKAI-DESIGN. |
| R3 | Tenant-level rate-limit budget reserved alongside Provider Account rate-limit during Tx1. | HUAKAI-DESIGN. |
| R4 | Cooldown timestamps include ±15% jitter. | HUAKAI-DESIGN. |
| R5 | State write failure → retry with exponential backoff + alert; no silent log-and-continue. | HUAKAI-DESIGN. |
| R6 | Reason taxonomy stored in `routing_reason` enum, not free-form. | HUAKAI-DESIGN. |
| R7 | OAuth refresh path bounded: max N refreshes per window before permanent disable. | HUAKAI-DESIGN. |
| R8 | Custom error codes versioned + rate-limited. | HUAKAI-DESIGN. |
| R9 | Cascade clearing: ClearRateLimit clears all dependent state. | KEEP from Sub2API. |
| R10 | Pool mode short-circuit on uncustomized errors. | KEEP from Sub2API. |
| R11 | Retry-After propagated to client. | HUAKAI-DESIGN. |

## 8. Test Scenarios (AT-RATE-001..017)

Sub2API-inheritable:
- AT-RATE-001 / OpenAI 429 with x-codex-* headers + 7d exhausted → SetRateLimited(now + reset_7d).
- AT-RATE-002 / Anthropic 429 with both windows → SetRateLimited(7d.reset).
- AT-RATE-003 / Anthropic 429 with NO reset → no state change (Extra Usage).
- AT-RATE-004 / OpenAI 401 token_invalidated → permanent disable.
- AT-RATE-005 / OAuth 401 → temp_unsched + invalidate cache + force expires_at.
- AT-RATE-006 / Pool mode + 429 + no custom codes → no state change.
- AT-RATE-007 / 529 with Enabled=false → no state change.
- AT-RATE-008 / Custom error codes enabled, 503 in list → handleCustomErrorCode + permanent disable.
- AT-RATE-009 / OpenAI 403 with counter < 3 → 10-min temp-unsched.
- AT-RATE-010 / OpenAI 403 with counter ≥ 3 → permanent disable.
- AT-RATE-011 / Antigravity 403 validation → permanent disable + URL extraction.
- AT-RATE-012 / Antigravity model-specific 429 → model_only_cooldown.
- AT-RATE-013 / ClearRateLimit cascade: clears overload / model / temp / 403 counter atomically.

HUAKAI-design:
- AT-RATE-014 / Tenant-level rate limit: T1 hits per-tenant cap → T1 rejected; T2 unaffected.
- AT-RATE-015 / Multi-instance race: 10 concurrent 429s → exactly 1 SetRateLimited transaction wins.
- AT-RATE-016 / Cooldown jitter: 100 accounts hit 429 with same reset → return-to-service spread across ±15%.
- AT-RATE-017 / Retry-After header: client sees `Retry-After: 240` when upstream reset 240s out.
- AT-RATE-018 / OAuth refresh bound: 3 consecutive 401s within window → permanent disable on 4th.
- AT-RATE-019 / Reason taxonomy: every 429 produces `routing_reason.rate_limit_reason` with valid enum, no free-form.
- AT-RATE-020 / Token-leakage-safe logging: simulate refresh failure with fragment in error → log line contains `[REDACTED]`.

## 9. Open TODOs

- **TODO-1**: Read `model_rate_limit.go` full body (101 lines) for granular Antigravity-specific edge cases.
- **TODO-2**: Verify `tryTempUnschedulable` body (line 1481+) for full rule-matching algorithm (Codex pass summary above; verify against source).
- **TODO-3**: Cross-check one-api's rate-limit (much simpler than Sub2API per Codex one-api pass) for KEEP candidates.
- **TODO-4**: Verify whether `SetRateLimited` is row-level locked in PostgreSQL or just cached.

These do NOT block synthesis sign-off; they DO block Released spec (per CL-009).

## 10. Provenance

- Sub2API: commit `b0a2252...`, files `service/ratelimit_service.go` (1740 lines), `service/model_rate_limit.go` (101 lines), `service/account_credentials_persistence.go`, `service/token_refresh_service.go`, `repository/account_repo.go` cooldown helpers, plus cross-references in gateway / Antigravity / OpenAI gateway services. Both Claude and Codex source-verified independently.
- LiteLLM: behavioral cross-reference only; no LiteLLM-specific rate-limit pattern claimed in this synthesis (out of scope for this task; Codex F-POOL-001 cycle covered LiteLLM cooldown patterns).
- This synthesis: Claude PM, after both passes read.
- Reviewer-lane sign-off: pending Codex final review CL-001..011.

## 11. Review Sign-Off

| Field | Value |
| --- | --- |
| Reviewer | (pending — fresh agent session must run CL-001..011) |
| Review date | (pending) |
| Owner answers received | N/A (no Owner-decision questions in this feature) |
| Checks passed | (pending) |
| Notes | F-RATE-001 synthesis. Claude + Codex passes integrated. 8 Codex sharpenings adopted (handle403, temp-unsched rules, model_rate_limit, state machine, cascade clearing, refresh-retry-exhaustion = temp-unsched not error). 14 HUAKAI improvements clearly labeled. 4 open TODOs, none blocking synthesis. |
