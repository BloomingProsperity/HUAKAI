# F-RATE-001: Upstream Rate-Limit Detection + Provider Account Cooldown

| Field | Value |
| --- | --- |
| Status | Released — Extended by A13/A14/A21/A22 (DR-009 2026-05-02) |
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

---

## A13 Provider Error Normalization Rule Table (DR-009 Phase A)

> Extends Phase B of the Normal Path. Replaces the scattered if/else status-code branches with a versioned, data-driven decision table. DR-009 §1 / synthesis A13 (P0, Phase A).

### Rationale

Phase B currently encodes provider-specific logic as inline conditionals. As the number of supported providers grows, per-vendor quirks compound and drift out of sync with upstream changes. A versioned rule table externalises that knowledge into a single authoritative structure, making each rule auditable and overridable without touching control-flow code.

### ERROR_RULES Table

Each row is a rule record evaluated in `priority` order (lowest number wins). A rule matches when **all** non-null fields in its condition columns match the incoming response. The first matching rule determines the `action`.

| `rule_id` | `version` | `priority` | `provider` | `http_status` | `body_keyword` | `header_match` | `error_class` | `action` | `disable_tier` |
|---|---|---|---|---|---|---|---|---|---|
| R-001 | 1 | 10 | `*` | 401 | `invalid_grant` | — | `oauth_invalid_grant` | `permanent_disable` | `iron_clad` |
| R-002 | 1 | 10 | `*` | 400 | `identity verification` | — | `kyc_required` | `permanent_disable` | `iron_clad` |
| R-003 | 1 | 10 | `*` | 400 | `org_disabled` | — | `org_disabled` | `permanent_disable` | `iron_clad` |
| R-004 | 1 | 10 | `*` | 401 | `token_revoked` | — | `token_revoked` | `permanent_disable` | `iron_clad` |
| R-005 | 1 | 10 | `*` | 402 | `deactivated_workspace` | — | `workspace_deactivated` | `permanent_disable` | `iron_clad` |
| R-006 | 1 | 10 | `*` | 401 | `token_invalidated` | — | `token_revoked` | `permanent_disable` | `iron_clad` |
| R-007 | 1 | 20 | `*` | 402 | `credit` | — | `credit_exhausted` | `permanent_disable` | `iron_clad` |
| R-008 | 1 | 20 | `*` | 400 | `credit balance` | — | `credit_exhausted` | `permanent_disable` | `iron_clad` |
| R-009 | 1 | 30 | `*` | 401 | — | — | `auth_other` | `permanent_disable` | `iron_clad` |
| R-010 | 1 | 40 | `openai` | 403 | — | — | `platform_policy` | `counted_disable` | `ambiguous` |
| R-011 | 1 | 40 | `antigravity` | 403 | `validation` | — | `validation_required` | `permanent_disable` | `iron_clad` |
| R-012 | 1 | 40 | `*` | 403 | — | — | `platform_policy` | `permanent_disable` | `iron_clad` |
| R-013 | 1 | 50 | `*` | 429 | — | — | `rate_limited` | `cooldown` | `ambiguous` |
| R-014 | 1 | 50 | `*` | 529 | — | — | `overloaded` | `cooldown` | `ambiguous` |
| R-015 | 1 | 60 | `*` | `5xx` | — | — | `server_error` | `warn_only` | `ambiguous` |
| R-016 | 1 | 70 | `*` | `*` | — | — | `unknown` | `pass_through` | — |

**12 error classes**: `oauth_invalid_grant`, `kyc_required`, `org_disabled`, `token_revoked`, `workspace_deactivated`, `credit_exhausted`, `auth_other`, `platform_policy`, `validation_required`, `rate_limited`, `overloaded`, `server_error`.

Rules are loaded at gateway startup and reloaded on SIGHUP. Each rule record carries a `version` integer; the effective ruleset version is `max(version)` across all active rows. Provider-specific overrides are rows with a non-wildcard `provider` field; they shadow the corresponding wildcard row by virtue of lower (higher-priority) numeric priority.

### DR-009 Q1: Auto-Disable Tier Classification

The `disable_tier` column encodes the Owner Decision Q1 (DR-009 §1, synthesis §6 decision 1):

| Tier | Meaning | Auto-action |
|---|---|---|
| `iron_clad` | Keyword is unambiguous proof of permanent credential invalidity. Trigger examples: `invalid_grant`, `KYC`, `org_disabled`, `token_revoked`, `deactivated_workspace`. | Automatic `permanent_disable` — no operator confirmation required. |
| `ambiguous` | Signal is consistent with transient failure (e.g., cascading 5xx, transient 403). Keyword alone is not proof. | Set `degraded` state; flag for operator review. **Never auto-permanent-disable.** |

**Hard constraint (DR-009 §6.6 / synthesis §6.6)**: A22 FSM must never reach `disabled` on an `ambiguous` signal alone. The gateway MUST require either an `iron_clad` rule match OR explicit operator action to cross into `disabled`.

### Integration with Phase B

The rule table replaces the inline conditionals in Phase B steps 4–13. The evaluation order is:

1. Phase A filter layers run first (unchanged).
2. Lookup `ERROR_RULES` where `provider` matches (exact or wildcard) AND `http_status` matches, ordered by `priority ASC`.
3. Apply `action` from the first matching rule to the A22 FSM (see §A22).
4. Record `error_class` on the Usage Record `routing_reason.rate_limit_reason` field.

The legacy Phase B step numbering is retained for backwards compatibility in audit logs; the rule table is the authoritative source of truth.

### Acceptance Tests

- **AT-RATE-021** — `invalid_grant` keyword in 401 body → rule R-001 matches → `error_class = oauth_invalid_grant` → A22 FSM transitions to `disabled`; audit log carries `rule_id = R-001`, `version = 1`.
- **AT-RATE-022** — 5 consecutive upstream 5xx responses → rule R-015 matches each time → `error_class = server_error`, `action = warn_only` → A22 FSM reaches `degraded` but NOT `disabled`; operator dashboard shows red flag awaiting manual action.
- **AT-RATE-023** — Operator adds provider-specific override row (priority 5, `provider = acme`, `http_status = 403`, `body_keyword = suspended`, `action = permanent_disable`, `disable_tier = iron_clad`) → rule takes precedence over R-012 for `acme` responses; existing providers unaffected; ruleset version increments; loaded without gateway restart.

---

## A14 Retry-After Harmonizer with Jittered Cooldown (DR-009 Phase D, P1)

> Extends Phase C (429 reset extraction) and Phase D (529 cooldown). Prevents thundering-herd return-to-service storms when many accounts share the same upstream reset timestamp. DR-009 §1 / synthesis A14 (P1, Phase D).

### Rationale

When a provider rate-limits a large number of accounts simultaneously (e.g., a burst of 429s during a shared quota window), all accounts receive the same `reset_at` timestamp. Without jitter, every account exits cooldown at the same instant and hammers the upstream endpoint together — reproducing the original overload. The harmonizer adds per-account deterministic jitter so that return-to-service is spread across a time window, while remaining stable across gateway restarts (no random seed drift).

### Jitter Formula

```
jitter_fraction  = stable_hash(account.id, error_class) mod 1000 / 1000  # ∈ [0, 1)
jitter_offset    = (jitter_fraction * 2 - 1) * 0.15 * base_cooldown       # ±15% of base
harmonized_until = base_reset_at + jitter_offset
```

Where:

- `base_reset_at` is the reset timestamp extracted by Phase C layers 1–4 (or `now() + default_cooldown` for layer 5).
- `stable_hash(account.id, error_class)` is a deterministic hash (e.g., SipHash-1-2 keyed with a fixed gateway secret) over the concatenation of `account.id` and the `error_class` string. This ensures the same account always gets the same offset for the same error class, preventing oscillation.
- The ±15% bound is the existing HUAKAI jitter range already specified in Phase C ("Apply HUAKAI cooldown jitter ±15%"), now formalised and extended to cover the multi-account thundering-herd case.

### Thundering-Herd Guarantee

With `N` accounts sharing the same `base_reset_at`, the uniform distribution of `stable_hash` values ensures that return-to-service events are spread approximately evenly across the `±15%` window. For `N = 100` accounts and a 5-minute base cooldown, the 30-second spread (±15 s) means at most ~4 accounts return per second on average, rather than 100 simultaneously.

The `Retry-After` header returned to the client reflects `harmonized_until - now()` (rounded up to the nearest second), not the raw upstream reset. This aligns the client's retry expectation with the account's actual availability.

### Acceptance Tests

- **AT-RATE-024** — 100 accounts receive upstream 429 with identical `reset_at = T`. After applying the harmonizer, the distribution of `harmonized_until` values across all accounts spans a window of `0.30 × base_cooldown` seconds (i.e., full ±15% range is used). No two accounts share the same `harmonized_until`. Client `Retry-After` headers match each account's `harmonized_until - now()`.

---

## A21 Risk-Weighted Probe Scheduler (DR-009 Phase C, P0)

> New subsystem. Governs when and how often the gateway probes degraded or cooling-down Provider Accounts to detect recovery, without generating unnecessary upstream load. DR-009 §1 / synthesis A21 (P0, Phase C).

### Rationale

After an account enters a non-`normal` state (see A22 FSM), the gateway must eventually verify whether the account has recovered. Naive fixed-interval polling wastes budget on accounts unlikely to recover soon and under-probes accounts that are nearly ready. A risk-weighted scheduler assigns probe priority based on a multi-signal risk vector, ensuring probe budget is spent where recovery probability is highest.

### Risk Vector

Each non-normal account is assigned a probe priority score computed from the following signals:

| Signal | Weight rationale |
|---|---|
| `time_since_last_error` | Older errors are more likely to have cleared; higher elapsed time → higher probe priority. |
| `error_class_recovery_rate` | Historical fraction of accounts with this `error_class` that recovered within 1 h; higher rate → higher priority. |
| `account_health_score` | A22 `health_score` (see §A22); higher baseline score → more likely to recover quickly. |
| `provider_incident_flag` | If the provider's status page (or aggregated error rate) signals a known incident, deprioritise all accounts on that provider (incident unlikely to clear per-account). |
| `consecutive_probe_failures` | Each failed probe increments a counter; high counter → lower priority (account less likely to recover). |

The composite priority score is computed as a weighted sum of normalised signal values. Weights are operator-tunable via configuration; defaults are specified in the implementation plan.

### Probe Budget

A `probe_budget` counter is maintained per provider (scoped to the HUAKAI instance or cluster, depending on deployment mode). The budget governs the maximum number of probe requests per minute to any single provider across all accounts. This prevents the probe scheduler itself from triggering upstream rate-limits.

```
probe_budget[provider]  — max probes per minute (operator-configured, default 10)
probe_interval_min[account] — minimum seconds between probes for this account
                              (starts at 60 s, doubles on each failed probe up to 3600 s)
```

The scheduler maintains a **min-priority-queue** ordered by `(next_probe_at, priority_score DESC)`. On each scheduling tick:

1. Dequeue accounts whose `next_probe_at ≤ now()` up to `probe_budget[provider]` slots.
2. Issue a lightweight probe request (e.g., a minimal token request or a provider health-check endpoint if available).
3. On probe success: feed result into A22 FSM as a `clean_success` event.
4. On probe failure: increment `consecutive_probe_failures`; recalculate priority; re-enqueue with doubled `probe_interval`.

### Acceptance Tests

- **AT-MON-001** — Two accounts on the same provider: account A has `error_class = server_error` (ambiguous, 2 min ago), account B has `error_class = rate_limited` (30 min ago, high `error_class_recovery_rate`). Scheduler assigns B higher probe priority; B is probed first within the same budget window.
- **AT-MON-002** — Provider incident flag set for provider P. All accounts on P have their probe priority reduced to `LOW`; probe budget consumed by accounts on other providers first. When incident flag clears, P accounts return to normal priority queue ordering within one scheduling tick.

---

## A22 Account Health Hysteresis State Machine (DR-009 Phase A, P0)

> Replaces the implicit boolean `error` / `temp_unschedulable` / `rate_limited` flags with an explicit, versioned FSM. Hard constraint per DR-009 §6.6 / synthesis §6.6: the FSM must not auto-transition to `disabled` on ambiguous signals. DR-009 §1 / synthesis A22 (P0, Phase A).

### Rationale

The existing spec (Phase B / Failure Path) manages account health through separate flags (`SetError`, `SetTempUnschedulable`, `SetRateLimited`). These flags can be set independently, leading to ambiguous combined states and making it hard to reason about valid recovery paths. An explicit hysteresis FSM with non-overlapping upgrade/downgrade thresholds ensures that a single transient error does not permanently disable a healthy account (protecting account inventory per DR-009 §6.6), and that a degraded account must exhibit sustained recovery before returning to full service.

### States

| State | Meaning | Schedulable? |
|---|---|---|
| `normal` | Account healthy; no recent errors. | Yes |
| `degraded` | Elevated error rate or ambiguous signal; still schedulable with lower weight. | Yes (reduced weight) |
| `cooling_down` | Recent `rate_limited` or `overloaded` signal; held off until `cooldown_until`. | No (until timer expires) |
| `needs_refresh` | OAuth token requires refresh; credential temporarily unavailable. | No (background refresh in progress) |
| `needs_manual_recovery` | Ambiguous signals accumulated beyond operator threshold; operator intervention required. | No |
| `disabled` | Iron-clad keyword matched (A13 `iron_clad` tier) OR operator explicit action. | No (permanent until operator re-enables) |

### Transitions

```
normal ──[ambiguous_error]──────────────────────────────► degraded
normal ──[iron_clad_keyword]────────────────────────────► disabled
normal ──[cooldown_trigger (429/529)]───────────────────► cooling_down
normal ──[oauth_401]────────────────────────────────────► needs_refresh

degraded ──[clean_success_streak ≥ UPGRADE_STREAK]──────► normal
degraded ──[iron_clad_keyword]──────────────────────────► disabled
degraded ──[ambiguous_errors ≥ MANUAL_THRESHOLD]────────► needs_manual_recovery
degraded ──[cooldown_trigger]───────────────────────────► cooling_down

cooling_down ──[timer_expired AND health_score ≥ COOLDOWN_EXIT]── ► degraded  (not normal)
cooling_down ──[iron_clad_keyword]──────────────────────────────► disabled

needs_refresh ──[refresh_success]───────────────────────► normal
needs_refresh ──[iron_clad_keyword e.g. invalid_grant]──► disabled
needs_refresh ──[refresh_exhausted]─────────────────────► needs_manual_recovery

needs_manual_recovery ──[operator_clear]────────────────► degraded  (not normal)

disabled ──[operator_explicit_reenable]─────────────────► needs_manual_recovery
```

**Hysteresis invariant**: the downgrade threshold (error count or score drop that moves toward `disabled`) is strictly lower than the upgrade threshold (successes required to move toward `normal`). This gap prevents oscillation. Specifically:

- `UPGRADE_STREAK` (successes required to exit `degraded` → `normal`): default 10 consecutive clean successes.
- `DOWNGRADE_THRESHOLD` (errors to enter `degraded` from `normal`): default 3 errors within a 5-minute window.
- The two thresholds intentionally do not overlap: an account that just entered `degraded` at error count 3 cannot immediately return to `normal` on the next success — it must accumulate 10 consecutive clean successes.

### Health Score and Decay

Each account carries a `health_score ∈ [0.0, 1.0]`:

```
On clean success:    health_score = min(1.0, health_score + SCORE_INCREMENT)
On error event:      health_score = max(0.0, health_score - SCORE_DECREMENT × severity(error_class))
On idle (no events): health_score decays toward 0.5 at rate DECAY_RATE per minute
                     (prevents stale high scores on dormant accounts)
```

Default constants: `SCORE_INCREMENT = 0.05`, `SCORE_DECREMENT = 0.15` (base; multiplied by `severity`), `DECAY_RATE = 0.01/min`. Severity: `iron_clad` = 5×, `ambiguous` = 1×.

`health_score` feeds into A21 probe priority (§A21) and the `COOLDOWN_EXIT` gate (default: `health_score ≥ 0.4` before exiting `cooling_down` into `degraded`).

### Integration with Existing SetError / SetTempUnschedulable / SetRateLimited

The FSM replaces the semantics of the three existing state-setting operations:

| Existing call | A22 FSM event | Resulting state |
|---|---|---|
| `SetError(permanent=true)` with `iron_clad` rule | `iron_clad_keyword` | `disabled` |
| `SetError(permanent=true)` with `ambiguous` rule | `ambiguous_error` (repeated) → eventually `needs_manual_recovery` | `degraded` → `needs_manual_recovery` |
| `SetTempUnschedulable` | `cooldown_trigger` | `cooling_down` |
| `SetRateLimited` | `cooldown_trigger` | `cooling_down` |
| OAuth 401 handling | `oauth_401` | `needs_refresh` |
| `ClearRateLimit` (cascade) | `operator_clear` | `degraded` |

Callers keep the same external function names for backwards compatibility. Internally, each call delivers the corresponding FSM event and persists the resulting state to `provider_accounts.account_state` + appends to `provider_account_state_changes` audit log.

### Acceptance Tests

- **AT-STATE-001** — Account in `normal` state receives 3 upstream errors with `error_class = server_error` (ambiguous) within 5 minutes. FSM transitions to `degraded`. Account remains schedulable with reduced weight. No operator action taken. `health_score` drops to ≤ 0.55. Audit log records each transition event with timestamp and triggering `rule_id`.
- **AT-STATE-002** — Account in `degraded` state achieves 10 consecutive clean successes (`clean_success_streak = 10`). FSM transitions to `normal`. `health_score ≥ 0.9`. Scheduler weight restored to full. Verify that 9 successes are insufficient (hysteresis gap enforced).
- **AT-STATE-003** — Account in `cooling_down` state with `health_score = 0.2` (low). Timer expires. FSM does NOT transition to `degraded` because `health_score < COOLDOWN_EXIT (0.4)`; account remains in `cooling_down` for the next probe cycle. After A21 probe succeeds and `health_score` rises to 0.45, FSM transitions to `degraded` (not `normal`). Verify that `disabled` state is never reached via this path (no `iron_clad` keyword was present).
