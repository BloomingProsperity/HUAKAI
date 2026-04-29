# F-RATE-001: Upstream Rate-Limit Detection + Provider Account Cooldown

| Field | Value |
| --- | --- |
| Status | Released |
| Feature ID | F-RATE-001 |
| Specifier | Claude (PM-Orchestrator) + Codex (independent parallel pass), 2026-04-28 |
| Specifier date | 2026-04-28 |
| Reviewer | Codex final reviewer-lane, 2026-04-28 (APPROVE-WITH-FIXES; 10 fixes applied this revision) |
| Review date | 2026-04-28 |
| Released date | 2026-04-28 |
| Lane mode | Option C |
| Supersedes | — |
| Superseded by | — |

## Sources

> Reference material consulted by specifier-lane only. Implementer lane MUST NOT open these.

- Sub2API — LGPL-3.0 ([E-LIC-001](../07_REFERENCE_EVIDENCE_LEDGER.md), commit `b0a2252ed19c3720e6adafde6083e64fbac2efa9`)
- Specifier backing artifacts: [rate-limiting-synthesis.md](../decompositions/_cross-cutting/rate-limiting-synthesis.md), [rate-limiting-source-verified.md](../decompositions/sub2api/rate-limiting-source-verified.md), [rate-limiting-codex.md](../decompositions/_cross-cutting/rate-limiting-codex.md)

## Capability

This spec satisfies F-RATE-001 from [03_FEATURE_PARITY_MATRIX.md](../03_FEATURE_PARITY_MATRIX.md): upstream-side rate-limit detection + Provider Account cooldown management. The gateway parses upstream 429/529 + 401/403 + custom error codes, marks Provider Account in typed cooldown states, and clears state on recovery signals. **Distinct from client-facing F-SEC-001 (per-IP) and F-SEC-004 (per-User × per-Model)**: those rate-limit the User; F-RATE-001 handles the upstream's rate-limit signals targeting our Provider Account.

## Actor

- **System** (Gateway error-handler + cooldown service): runs the decision tree.
- **System** (Background token-refresh service): coordinates with cooldown state for OAuth refresh failures.
- **Operator**: tunes cooldown durations; observes state-distribution; clears state manually when needed.
- **External Provider**: sends rate-limit signals via headers / status codes / body fields.

## Preconditions

1. Tenant context established; Provider Account selected (per F-POOL-001 spec).
2. Upstream HTTP request issued; response received with status code + headers + body.
3. Per-Account credentials configured: account_type, OAuth flag, custom-error-code list, temp-unsched policy rules.
4. Cooldown state tables locked at field level per `docs/schema/rate-limiting.sql` (Phase 2.1, follows this spec).

## Normal Path

The error-handler runs **a layered decision tree** on every upstream response.

### Phase A — Filter Layers (early returns)

1. **Pool-mode short-circuit**: if Account in pool mode AND custom error codes NOT enabled → no state change; pass error through to client.
2. **Custom error code filter**: if Account has custom error code list AND status NOT in list → no state change; pass through.
3. **Temp-unsched rules** (status != 401): if Account has temp-unsched rules enabled AND incoming status + body matches a rule → set Account temp_unschedulable_until; record matched rule; return.

### Phase B — Status-Specific Handlers

For status codes not handled by Phase A:

4. **400 with disable-trigger string** (organization disabled / credit balance / identity verification): permanent disable.
5. **401 with permanent revocation indicator** (OpenAI token_invalidated / token_revoked / Unauthorized): permanent disable.
6. **401 OAuth (non-Antigravity)**: invalidate token cache + force credential expiry + temp_unschedulable for OAuth-401-cooldown duration (default 10 min).
7. **401 (other)**: SetError (permanent).
8. **402 deactivated_workspace** (OpenAI): permanent disable.
9. **402 (other)**: permanent disable (insufficient balance).
10. **403** — platform-specific dispatch:
    - **Antigravity**: classifier inspects body → validation (with extracted URL) / violation / generic → permanent disable.
    - **OpenAI with counter cache**: increment 180-min counter; permanent disable at count ≥ 3; else 10-min temp_unschedulable.
    - **OpenAI without counter cache**: permanent disable.
    - **Other platforms**: permanent disable.
11. **429** — multi-layer reset extraction (see Phase C).
12. **529**: overload cooldown (see Phase D).
13. **5xx (no custom codes)**: warn log only; no state change.
14. **Custom error codes enabled, other status**: handle as configured by operator policy.

### Phase C — 429 Reset Extraction (5 fallback layers)

15. **Layer 1 (OpenAI)**: parse `x-codex-*` headers → 5h / 7d window utilization + reset; choose actually-exhausted window; if both, prefer longer cooldown.
16. **Layer 2 (Anthropic per-window)**: parse `anthropic-ratelimit-unified-{5h,7d}-{reset,utilization,surpassed-threshold}` headers; choose between 5h vs 7d based on which exceeded.
17. **Layer 3 (aggregate header)**: fall back to `anthropic-ratelimit-unified-reset` (older format).
18. **Layer 4 (body parse)**: per-platform body parsers — OpenAI `usage_limit_reached`, Gemini reset format.
19. **Layer 5 (default)**: 5-minute default for non-Anthropic. **Anthropic 429 with NO reset header → pass-through, no state change** (treats as Extra Usage Required, NOT real rate-limit).

Set Account state to `rate_limited` until reset_at. Apply HUAKAI cooldown jitter ±15%.

### Phase D — 529 Overload Cooldown

20. Operator-toggleable via `OverloadCooldownEnabled`. If disabled → no state change.
21. Set Account state to `overloaded` until now() + overload-cooldown-minutes (default 10).
22. Distinct from `rate_limited`: separate observability track + different cooldown defaults.

### Phase E — OAuth 401 Background Coordination (interaction with F-AUTH-001)

When status is 401 AND OAuth path:
23. Background `TokenRefreshService` discovers temp_unschedulable Account.
24. Refresh attempt:
    - Success: clear temp_unsched + delete cache + invalidate OAuth cache.
    - Retry-exhausted failure: set temp_unsched for retry-cooldown duration (NOT permanent error; preserves active status for future retries).
    - Non-retryable error (invalid_grant): set Account error (permanent).
25. Refresh attempt counter per (account, window): max N refreshes before permanent disable (HUAKAI bound).

### Phase F — Cascade Clearing (HUAKAI-design extension)

When operator or auto-clear triggers ClearRateLimit:
26. Clear rate_limit_until + overload_until atomically.
27. Cascade-clear: model_rate_limits map + temp_unsched state + temp_unsched cache + per-platform 403 counter.
28. HUAKAI invariant: cascade is atomic OR compensation-safe across DB / cache / counter (Sub2API source shows sequential clears, not single-tx; HUAKAI strengthens).

### Phase G — UpdateSessionWindow (recovery signal handler)

On successful upstream response:
29. If headers contain `anthropic-ratelimit-unified-5h-status`:
    - Parse 5h reset (defensive: detect millisecond timestamps; range-validate `[now-5h, now+7d]`).
    - Update stored 5h session window.
    - If status `allowed` while Account currently rate_limited → clear rate_limit (recovery signal).

## Failure Path

Failure taxonomy (19 reasons, structured `routing_reason.rate_limit_reason` field on Usage Record):

| Reason | Trigger | Recovery | Source |
|--------|---------|----------|--------|
| `RATE_LIMIT_5H_EXCEEDED` | Anthropic 5h window | account_cooldown(reset_at) | Sub2API |
| `RATE_LIMIT_7D_EXCEEDED` | Anthropic 7d window | account_cooldown(reset_at) | Sub2API |
| `RATE_LIMIT_BOTH_WINDOWS` | both exceeded | account_cooldown(longer reset) | Sub2API |
| `RATE_LIMIT_RPM` | RPM bucket exceeded | account_cooldown(60s + jitter) | HUAKAI-design |
| `RATE_LIMIT_TPM` | TPM bucket exceeded | account_cooldown(60s + jitter) | HUAKAI-design |
| `EXTRA_USAGE_REQUIRED` | Anthropic 429 no reset | passthrough_to_client | Sub2API |
| `OVERLOADED` | 529 | account_cooldown(jittered 10m) | Sub2API |
| `TOKEN_REFRESH_REQUIRED` | OAuth 401 | temp_unsched(10m) + invalidate_credentials | Sub2API |
| `TOKEN_PERMANENTLY_REVOKED` | revocation indicator | permanent_disable | Sub2API |
| `KYC_REQUIRED` | 400 + identity verification | permanent_disable + alert | Sub2API |
| `ORG_DISABLED` | 400 + organization disabled | permanent_disable + alert | Sub2API |
| `CREDIT_EXHAUSTED` | 400 + credit balance OR 402 | permanent_disable + alert | Sub2API |
| `WORKSPACE_DEACTIVATED` | 402 + deactivated_workspace | permanent_disable + alert | Sub2API |
| `MODEL_LIMIT_EXCEEDED` | Antigravity model-specific 429 | model_only_cooldown(reset_at) | Sub2API |
| `TEMP_UNSCHED_RULE_MATCHED` | per-account custom rule | temp_unsched(rule.duration) | Sub2API |
| `OPENAI_403_COUNTED` | OpenAI 403 + counter < 3 | temp_unsched(10m) | Sub2API |
| `OPENAI_403_DISABLED` | OpenAI 403 + counter ≥ 3 | permanent_disable | Sub2API |
| `ANTIGRAVITY_403_VALIDATION` | Antigravity 403 validation | permanent_disable + show_url | Sub2API |
| `CUSTOM_ERROR_CODE` | operator-configured policy match | terminal account error/disable unless HUAKAI chooses recoverable cooldown variant | Sub2API terminal; HUAKAI design recoverable |

## Operator Recovery

| Failure | Detection | Recovery |
|---|---|---|
| Permanent disable triggers | account state `error` | operator confirms + clears via admin API (not auto). |
| Rate-limited / overloaded | auto-cleared at reset_at / until | no operator action; or manual ClearRateLimit cascade. |
| Temp_unschedulable | auto-cleared at until | manual ClearTempUnschedulable if operator wants earlier recovery. |
| Model-rate-limited | per-(account, model) auto-cleared | operator can also remove model from list manually. |
| Refresh attempt counter at limit (HUAKAI bound) | dashboard alert | operator investigates upstream credential health; resets counter or replaces credential. |

## Audit / Usage / Log Evidence

Every failure produces:
1. **Usage Record** carrying `routing_reason.rate_limit_reason` enum string (no free-form).
2. **Account state mutation** event in operator audit trail (`provider_account_state_changes` table — fragment Phase 2.1 schema).
3. **Operator metrics** counter increment per (rate_limit_reason × tenant × account).

## Acceptance Test Direction

Per [11_ACCEPTANCE_TEST_MATRIX.md](../11_ACCEPTANCE_TEST_MATRIX.md), tests AT-RATE-001..020.

Sub2API-inheritable:
- AT-RATE-001 / OpenAI 429 with x-codex-* headers + 7d exhausted → SetRateLimited(now + reset_7d).
- AT-RATE-002 / Anthropic 429 with both windows → SetRateLimited(7d.reset).
- AT-RATE-003 / Anthropic 429 with NO reset → no state change.
- AT-RATE-004 / OpenAI 401 token_invalidated → permanent disable.
- AT-RATE-005 / OAuth 401 → temp_unsched + invalidate cache + force expires_at.
- AT-RATE-006 / Pool mode + 429 + no custom codes → no state change.
- AT-RATE-007 / 529 with overload-cooldown disabled → no state change.
- AT-RATE-008 / Custom error codes enabled, 503 in list → handle + permanent disable.
- AT-RATE-009 / OpenAI 403 with counter < 3 → 10-min temp_unsched.
- AT-RATE-010 / OpenAI 403 with counter ≥ 3 → permanent disable.
- AT-RATE-011 / Antigravity 403 validation → permanent disable + URL extraction.
- AT-RATE-012 / Antigravity model-specific 429 → model_only_cooldown.
- AT-RATE-013 / ClearRateLimit cascade clears overload / model / temp / 403 counter; HUAKAI variant verifies the cascade is atomic or compensation-safe across DB/cache/counter state.

HUAKAI-design:
- AT-RATE-014 / Tenant-level rate limit: T1 hits per-tenant cap → T1 rejected; T2 unaffected.
- AT-RATE-015 / Multi-instance race: 10 concurrent 429s → exactly 1 SetRateLimited transaction wins.
- AT-RATE-016 / Cooldown jitter: 100 accounts hit 429 with same reset → return-to-service spread across ±15%.
- AT-RATE-017 / Retry-After header: client sees `Retry-After: 240` when upstream reset 240s out.
- AT-RATE-018 / OAuth refresh bound: 3 consecutive 401s within window → permanent disable on 4th.
- AT-RATE-019 / Reason taxonomy: every 429 produces structured `rate_limit_reason` enum, never free-form.
- AT-RATE-020 / Token-leakage-safe logging: simulate refresh failure with token fragment in error → log line contains `[REDACTED]`.

## Open Questions

None remaining at release. All four prior open questions resolved during Codex final review 2026-04-28; resolutions in [rate-limiting-synthesis.md §9](../decompositions/_cross-cutting/rate-limiting-synthesis.md).

## Implementer Notes (added by implementer lane)

> Filled by implementer after consuming the spec.

(empty until implementer-lane work begins)
