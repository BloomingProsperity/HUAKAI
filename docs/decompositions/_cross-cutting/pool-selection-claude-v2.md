# Provider Account Pool Selection — Claude v2 (Source-Verified Rewrite)

| Field | Value |
| --- | --- |
| Status | Specifier-lane draft (Claude pass v2, source-verified) |
| Author | Claude (PM-Orchestrator), specifier lane |
| Date | 2026-04-28 |
| Lane | Specifier — Option C strict spec input per [DR-000](../../process/decisions/DR-000-clean-room-methodology.md) carve-out for F-POOL-001 |
| Feature | [F-POOL-001](../../03_FEATURE_PARITY_MATRIX.md) (L1 MVP) |
| Supersedes | [pool-selection-claude.md](pool-selection-claude.md) (v1) — withdrawn per [2026-04-28-source-truth-corrections.md](../../process/reviews/2026-04-28-source-truth-corrections.md). v1 was paraphrased from prior prose decompositions; this v2 is read directly from source. |
| Sub2API verified | commit `b0a2252ed19c3720e6adafde6083e64fbac2efa9`, local clone at `c:/HUAKAI/repo/.omc/reference-src/sub2api/` (gitignored) |

## 1. Why F-POOL-001 Is Money-Grade

A relay-station gateway routes a tenant's logical request to one of N upstream Provider Accounts. Bad selection produces: customer-visible mid-conversation context loss, latency spike from cold Account, drained balance on one Account while others sit idle, and surprise rate-limit. Sub2API's selection is observable from source, but it does NOT make the selection itself transactionally consistent with quota / billing — that is a HUAKAI-side improvement.

## 2. Sub2API's Actual Selection Algorithm (Source-Verified)

Source: `backend/internal/service/gateway_service.go:1376–1928` (`SelectAccountWithLoadAwareness`).

### 2.1 Layers

| Layer | Trigger | What it does |
|-------|---------|--------------|
| **Layer 1 — Model Routing** (lines 1528–1752) | Group has `ModelRouting` config mapping requested model → list of Account IDs | Filter to `routingAccountIDs ∩ schedulable`. If empty after filtering, fall through. |
| **Layer 1.5 — Sticky-within-routing** (lines 1589–1665) | Sticky binding exists AND bound Account is in routing list AND not excluded | Re-validate sticky candidate; on success acquire slot; on full-but-valid return AccountWaitPlan with `StickySessionMaxWaiting`. |
| **Layer 1.5b — Sticky-standalone** (lines 1755–1803) | No model routing config AND sticky binding exists AND bound Account not excluded | Same re-validation; same wait plan. |
| **Layer 2 — Load-aware** (lines 1805–1911) | Layers 1/1.5 missed | Filter by 7 schedulability gates → score by load → strict lex-sort → tie-shuffle → try-acquire in order. |
| **Layer 3 — Fallback queue** (lines 1913–1927) | Layer 2 found candidates but no slot acquired | Return AccountWaitPlan with `FallbackWaitTimeout` / `FallbackMaxWaiting`. |

There is **no continuation-marker layer** in source; my v1 invented it.

### 2.2 The 7 Schedulability Gates

Applied at every layer (lines 1815–1838 for Layer 2):

1. `isAccountSchedulableForSelection(acc)` — lifecycle / disabled / cooldown.
2. `isAccountAllowedForPlatform(acc, platform, useMixed)` — platform match / mixed scheduling allowed.
3. `requestedModel != "" && !isModelSupportedByAccountWithContext(ctx, acc, requestedModel)` — model allow-list.
4. `isAccountSchedulableForModelSelection(ctx, acc, requestedModel)` — model rate-limit.
5. `isAccountSchedulableForQuota(acc)` — quota check.
6. `isAccountSchedulableForWindowCost(ctx, acc, isSticky)` — window cost cap.
7. `isAccountSchedulableForRPM(ctx, acc, isSticky)` — RPM cap.

Plus: `isExcluded(accountID)` (per-request exclusion list passed in by caller).

For Sticky path, gates 6 and 7 use `isSticky=true` (typically more permissive).

### 2.3 The Sort Order (NOT a score formula)

Source lines 1691–1710:

```go
sort.SliceStable(routingAvailable, func(i, j int) bool {
    a, b := routingAvailable[i], routingAvailable[j]
    if a.account.Priority != b.account.Priority {
        return a.account.Priority < b.account.Priority   // smaller priority value = higher priority
    }
    if a.loadInfo.LoadRate != b.loadInfo.LoadRate {
        return a.loadInfo.LoadRate < b.loadInfo.LoadRate  // less loaded first
    }
    // … LastUsedAt asc (older first = LRU) …
})
shuffleWithinSortGroups(routingAvailable)  // shuffle within (priority, load_rate, last_used_at) tie groups
```

Three-key strict lexicographic sort. **`shuffleWithinSortGroups` only randomizes within ties**, not "top-K random". Layer 2 also has its own selection iteration (lines 1877–1910) using `filterByMinPriority` → `filterByMinLoadRate` → `selectByLRU`, which is the same lex-sort by another name.

### 2.4 Slot Acquisition

Source line 2250:
```go
func (s *GatewayService) tryAcquireAccountSlot(ctx context.Context, accountID int64, maxConcurrency int) (*AcquireResult, error) {
    if s.concurrencyService == nil {
        return &AcquireResult{Acquired: true, ReleaseFunc: func() {}}, nil
    }
    return s.concurrencyService.AcquireAccountSlot(ctx, accountID, maxConcurrency)
}
```

Slot acquisition has **two layers** (Codex source-verification 2026-04-28 caught the conflation in v1):

1. **Service-level entry**: `tryAcquireAccountSlot` (gateway_service.go:2250) is a thin wrapper that delegates to `concurrencyService.AcquireAccountSlot(ctx, accountID, maxConcurrency)` (interface, returns `*AcquireResult{Acquired bool, ReleaseFunc func()}`). When `concurrencyService == nil` (e.g. minimum config), wrapper returns `Acquired=true` with no-op release — slot accounting is bypassed entirely.

2. **Cache-level implementation**: per `testutil/stubs.go:24` the wider concurrency interface includes `(ctx, accountID, maxConcurrency, requestID) (bool, error)` — a cache-backed atomic increment with TTL fallback for crash recovery. Configured via `gateway.concurrency_slot_ttl_minutes` (config_test.go:1246). The implementation is **not a serializable DB transaction**; it is cache-only with TTL-bounded crash recovery.

This is a noted gap: Sub2API's slot accounting is eventual-consistent w.r.t. crashes; slot leak is bounded by `concurrency_slot_ttl_minutes`. Distinct from HUAKAI's Pattern B which uses PostgreSQL row-locked counters as authoritative + cache as hint.

### 2.5 Wait Plans

Source lines 1465–1470, 1740–1745, 1920–1925:
```go
return &AccountWaitPlan{
    AccountID:      acc.ID,
    MaxConcurrency: acc.Concurrency,
    Timeout:        cfg.StickySessionWaitTimeout,    // or FallbackWaitTimeout
    MaxWaiting:     cfg.StickySessionMaxWaiting,     // or FallbackMaxWaiting
}
```

Sticky path uses `StickySessionMaxWaiting` (default observably lower); fallback uses `FallbackMaxWaiting` (higher). The plan is honored by the upper layer (`ConcurrencyHelper.AcquireAccountSlotWithWait` at `internal/handler/gateway_helper.go:267`); whether the waiter re-validates schedulability on resume is **a question I have not yet verified** in source. **TODO/UNVERIFIED.**

### 2.6 Sticky Cache Miss Reasons (Fixed Enum)

Source line 1656 logs `[StickyCacheMiss] reason=<X> account_id=<id> session=<hash>` with reasons:
- `session_limit` (line 1610)
- `wait_queue_full` (line 1639)
- `gate_check` (line 1644)
- `rpm_red` (line 1646)
- `account_cleared` (line 1661)

Five-value enum. This matches my v1 `STICKY_BREAK_<reason>` claim — confirmed correct.

### 2.7 Session Hash Derivation

Source lines 648–707 (`GenerateSessionHash`):

1. **Highest priority**: `parsed.MetadataUserID` parsed via `ParseMetadataUserID()` → if it has `SessionID`, use that.
2. **Next**: `extractCacheableContent(parsed)` → SHA hash of the content marked with `cache_control: {type: "ephemeral"}`.
3. **Fallback**: combined hash of `SessionContext.ClientIP` + `:` + `NormalizeSessionUserAgent(UserAgent)` + `:` + `APIKeyID` + `|` + `system text` + `messages text`.

The "session id" concept that my v1 claimed exists is actually **derived from request semantics**, not a client-supplied opaque id. This is an important nuance: the session is produced from cache-control markers + content fingerprint + connection identity, not from a header.

### 2.8 Per-Request Exclusion (Two Mechanisms)

a. **`excludedIDs map[int64]struct{}`** — caller-supplied parameter on `SelectAccountWithLoadAwareness` (line 1376). The outer retry-on-upstream-failover loop adds the failed Account ID and re-calls. (See gateway_service.go retry loop at line ~4262.)

b. **`localExcluded` map** (lines 1426–1452, fallback path with `LoadBatchEnabled=false`): used when `checkAndRegisterSession` fails (session-limit rejection) — the failed Account is marked locally and re-selected.

### 2.9 Failover Status Codes

`shouldFailoverUpstreamError` at line 3669:
```go
switch statusCode {
case 401, 403, 429, 529:
    return true
default:
    return statusCode >= 500
}
```

Failover triggers only for these specific codes OR any 5xx. Other 4xx codes (e.g. 400, 404, 422) do NOT trigger failover — they are returned to the client directly.

## 3. one-api Cross-Reference (Source-Verified)

Source: `c:/HUAKAI/repo/.omc/reference-src/one-api/relay/controller/text.go` and `relay/channeltype/select.go`.

### 3.1 one-api's Selection (Brief)

one-api's selection is much simpler than Sub2API:
- Channel-priority sort (descending by priority).
- Tie-broken by random pick.
- No load-aware sort, no sticky session, no model routing per-User.
- `forceChannel` mechanism allows admin override.
- Failover on retryable status: re-pick excluding the failed channel.

This confirms the docs/07 evidence row E-OAI-001 framing: one-api is the "minimal Channel routing baseline" and Sub2API is "stronger on layered affinity".

### 3.2 Money-Grade Gap in one-api

one-api's quota check is split — "pre-consume + post-settle" with cache-only validation pre-call. There is no idempotent claim row. This was the basis of E-OAI-DEEP-004/005/008 evidence rows; those still hold.

## 4. Failure Modes Sub2API Handles vs Does NOT Handle

### Handled in source

- **Stale sticky binding**: re-validation gate catches; binding cleared on `gate_check` / `rpm_red` etc.
- **Account becomes ineligible mid-flight**: 7 gates re-checked at every layer (Layer 2 explicit re-check at lines 1813–1814 comment cites: "Scheduler snapshots can be temporarily stale").
- **Top-scored Account starves**: `shuffleWithinSortGroups` randomizes ties.
- **Per-request retry exclusion**: `excludedIDs` parameter.
- **Session-limit rejection on retry**: `localExcluded` re-selection.
- **Failover on specific status codes**: `shouldFailoverUpstreamError`.

### NOT handled in source (real gaps)

- **Money-grade slot accounting**: cache-only via `concurrencyService`; crash leaks slot until `concurrency_slot_ttl_minutes` expires.
- **Cross-instance fairness**: `LoadRate` is computed but distribution measurement / alarm not present.
- **Score weight tuning**: priorities are operator-set ints, no auto-tuning.
- **Multi-instance distributed coordination**: depends on cache backend; if cache is in-process, in-process only.
- **Continuation-marker layer**: doesn't exist; sticky session is the only "preserve same Account" mechanism.

## 5. KEEP / IMPROVE / AVOID for HUAKAI

### KEEP (verified in source)

- The 5-layer structure (Routing → Sticky-within-routing → Sticky-standalone → Load-aware → Fallback queue).
- 7-gate revalidation at every layer.
- Strict lex-sort `(priority, load_rate, last_used_at)` with tie-group shuffle.
- Sticky vs Fallback wait limits (sticky shorter).
- Two exclusion mechanisms (caller-supplied `excludedIDs` for upstream-failover retry, internal `localExcluded` for session-limit retry).
- Sticky cache miss reasons as fixed enum.

### IMPROVE (HUAKAI design — not in source)

- **Money-grade slot acquisition**: PostgreSQL row-locked counter (Pattern B from synthesis), not cache-only. Crash detection via lease + heartbeat + orphan sweep.
- **Routing reason as structured Usage Record field**: Sub2API only logs `[StickyCacheMiss]` to plain text logs. HUAKAI's `routing_reason` typed payload (with selection layer, candidate counts by exclusion, scoring contributions, etc.) is a real improvement.
- **Re-validation on wait-plan resume**: confirm in source path through `ConcurrencyHelper.AcquireAccountSlotWithWait` (gateway_helper.go:267); even if Sub2API does this correctly, codify it in HUAKAI spec.
- **Coupling to Quota+Billing Tx1/Tx2**: Sub2API does not have HUAKAI's idempotent claim gate. HUAKAI Pattern B (Pool acquire after Tx1 commit, with placeholder + token writeback) is necessary because HUAKAI's gate is real.
- **LiteLLM-style single-Account exemption**: NOT in Sub2API. Should verify in LiteLLM source (now cloned) before claiming.

### AVOID (real Sub2API anti-patterns to NOT inherit)

- Cache-only slot accounting that leaks on crash beyond TTL.
- Free-form `[StickyCacheMiss]` log lines (operators have to grep logs to investigate).
- Restricting failover to `401/403/429/529 + 5xx` without a configurable retry policy per Account / per Pool.
- Two-mechanism exclusion (caller-supplied + internal-local) without a unified "all exclusions for this request" object.

## 6. Concurrency Invariants HUAKAI Adds

These are HUAKAI design properties NOT visible in Sub2API:

| # | Invariant | Why Sub2API doesn't have it |
|---|-----------|------------------------------|
| I1 | Slot acquisition is committed in a serializable PostgreSQL transaction with row-level lock on `provider_account`. | Sub2API uses cache-backed counter; crash semantics are TTL-bounded. |
| I2 | Acquired slot has a UUID `acquisition_token`; release is idempotent (decrement only if token matches). | Sub2API releases via `ReleaseFunc` closure; double-release semantics depend on cache primitive. |
| I3 | Acquisition is paired with Quota+Billing Tx1 reservation (Pattern B: claim row carries `provider_account_id IS NULL` placeholder pre-acquire, written back post-acquire). | Sub2API has no idempotent billing claim. |
| I4 | Orphan sweep finds claim rows with `provider_account_id IS NULL AND created_at < now() - sweep_threshold` and rolls back the reservation. | N/A in Sub2API. |
| I5 | Wait-plan waiter re-enters Phase C admission and re-validates against current authoritative state on resume. | Need to verify in Sub2API gateway_helper.go (TODO). |
| I6 | Tenant isolation: every key, lock, cache key, audit event carries `tenant_id`. | Sub2API is single-tenant in its current incarnation; multi-tenant is HUAKAI-specific. |
| I7 | Routing reason is a structured payload on Usage Record (not log-only). | Sub2API logs free-form. |

## 7. Test Scenarios (Sub2API-verified + HUAKAI-design)

Mapped to [docs/11_ACCEPTANCE_TEST_MATRIX.md](../../11_ACCEPTANCE_TEST_MATRIX.md). Tests are split by whether they verify Sub2API-inherited behavior or HUAKAI-design behavior.

**Sub2API-inherited (verifiable against source as oracle)**:
- AT-POOL-001 / 5-layer order: routing-config present + sticky bound to routing Account → Layer 1.5 wins.
- AT-POOL-002 / Sticky-standalone: no routing config + sticky bound + valid → Layer 1.5b wins.
- AT-POOL-003 / Load-aware lex-sort: 5 candidates with varied priority/load → returned order matches strict lex-sort.
- AT-POOL-004 / Tie-shuffle: 1000 trials over identical (priority, load_rate, last_used_at) → uniform distribution within ±15%.
- AT-POOL-005 / Sticky cache miss reasons: deliberately trigger each of the 5 enum values → log line matches.
- AT-POOL-006 / `excludedIDs` honored: outer retry adds failed ID → next selection skips it.
- AT-POOL-007 / Session-limit `localExcluded`: trigger session-limit on selected Account → next iteration skips it without affecting `excludedIDs`.

**HUAKAI-design (Sub2API has no equivalent — these test HUAKAI's improvements)**:
- AT-POOL-008 / Pattern B compensation: simulated crash after Tx1 commit but before Pool acquire → orphan sweep rolls back reservation.
- AT-POOL-009 / Acquisition token idempotent release: simulated double-release decrements only once.
- AT-POOL-010 / Tenant isolation: T1's selection never picks T2's Provider Accounts.
- AT-POOL-011 / Routing reason payload: every Usage Record carries structured `routing_reason` matching schema.
- AT-POOL-012 / Wait-plan resume re-validation: waiter resumed after Account becomes UNHEALTHY → falls through to next candidate (depends on TODO/UNVERIFIED §2.5).

## 8. Open TODOs (Source-Verification Gaps)

These I have not yet confirmed and should be verified before reviewer-lane sign-off:

- **TODO-1**: Verify `ConcurrencyHelper.AcquireAccountSlotWithWait` (gateway_helper.go:267) re-validates schedulability on waiter resume — or does NOT.
- **TODO-2**: Verify whether Sub2API has any "capability shift" / safe-equivalent fallback (Codex's pass claimed yes; I cannot find it in code I read).
- **TODO-3**: Verify LiteLLM (now cloned at `.omc/reference-src/litellm/`) for single-Account exemption logic.
- **TODO-4**: Verify whether `forcePlatform` in Sub2API is platform-only (line 1328) or extends to Account-level forcing — my read so far suggests platform-only.
- **TODO-5**: Verify what `LoadRate` actually computes — is it `(in_flight / cap_concurrency) * 100`, or includes wait queue, or something else?

## 9. Attribution

- Source files read: 
  - Sub2API: `backend/internal/service/gateway_service.go` (lines 648–707, 1315–1928, 2250–2255, 3669–3676, 4267–4339, 7781–7789), `backend/internal/service/gateway_forward_as_chat_completions.go` (lines 1–496), `backend/internal/service/gateway_forward_as_responses.go` (lines 1–260), `backend/internal/testutil/stubs.go:24`.
  - one-api: `relay/controller/text.go`, `relay/channeltype/select.go` (browsed; details summarized §3).
- Behavior described in HUAKAI vocabulary; no upstream function name appears in implementer-lane facing files (this file IS specifier-lane, so function names are cited for traceability per CL-002 specifier-lane exception).
- This pass was authored AFTER reading source directly. Codex's parallel pass (`pool-selection-codex.md`) was authored before; mutual review against this v2 follows.
- v1 (`pool-selection-claude.md`) is **withdrawn**; see `docs/process/reviews/2026-04-28-source-truth-corrections.md` for the catalogue of v1 hallucinations.
