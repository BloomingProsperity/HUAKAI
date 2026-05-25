# Quota Atomic Reservation + Billing Idempotent Claim Gate (Codex pass)

| Field | Value |
| --- | --- |
| Status | Draft |
| Author | Codex (gpt-5.5 + xhigh, critic agent) via `omc ask codex` |
| Date | 2026-04-28 |
| Sources read | one-api at commit `8df4a2670b98266bd287c698243fff327d9748cf` (full quota mutation path: relay handler entry → API Key resolution → pre-call estimate → upstream call invocation → post-call reconcile → DB write) + Sub2API at commit `b0a2252ed19c3720e6adafde6083e64fbac2efa9` (full billing claim path: fingerprint computation → claim → DB transaction → multi-dimension mutation → replay rejection) |
| Companion file | The Claude parallel pass for the same algorithm lives at [quota-billing-claim-gate-claude.md](quota-billing-claim-gate-claude.md). Synthesized final design lives at [quota-billing-claim-gate-synthesis.md](quota-billing-claim-gate-synthesis.md) (the consolidated action plan). |
| Raw artifact | `.omc/artifacts/ask/codex-role-codex-specifier-lane-miner-for-huakai-core-algorithm-di-2026-04-28T06-22-08-984Z.md` (gitignored) |

## Pre-Decomposition Insight from Codex

> Sub2API's strong point is "claim + multi-dimension deduction in the same transaction", but it is still post-call billing, not pre-call atomic reservation. HUAKAI cannot just adopt the idempotent claim; HUAKAI must put balance / quota admission INTO the same strict reservation transaction.

This is the load-bearing insight of Codex's pass. Neither one-api nor Sub2API combine pre-call reservation with idempotent claim. HUAKAI is the first to demand both.

## one-api Decomposition

### 1. WHY

one-api is the **negative reference** for HUAKAI's quota model. It demonstrates a commercially useful relay flow but the quota mutation model is not strict enough for Model 1 commercial use. The lessons HUAKAI must absorb are what to avoid: pre-call quota checks based on cache or stale snapshots, request retries that can reopen the same deduction path, post-call writes split across User / API Key / Provider Account / Usage Record / cache without one atomic boundary.

### 2. WHAT step-by-step in HUAKAI vocabulary

A request enters the relay route, passes API Key authentication, resolves the owning User, checks User status, checks API Key status, optional API Key model scope, and optional network restrictions. The API Key object may be loaded from cache. That means the authentication decision can be based on an earlier API Key quota snapshot until cache expiry or invalidation.

The relay then resolves the User's Pooling Group and chooses a Provider Account that supports the requested model. Provider Account selection is read-only from either DB or memory cache. No User Quota, API Key Quota, or Provider Account quota is locked during selection.

For text-like requests, the gateway estimates a pre-call Quota amount from prompt size, configured base pre-consumption, requested maximum output, model ratio, and Pooling Group ratio. It reads User Quota, often through cache. If cached User Quota appears too low, it refreshes from DB; otherwise it trusts cache. It then decreases cached User Quota before the authoritative API Key/User DB pre-consumption path. **If the User appears to have far more Quota than the estimate, the request is treated as trusted and the effective pre-consumption is set to zero**, even though the cache may already have been decreased. If not trusted, the DB path reads the API Key and User again, checks whether each has enough quota, and performs separate decrement updates for API Key and User.

The request is then transformed and sent to the selected Provider Account. If upstream returns a retryable failure, the relay may select another Provider Account and run the relay helper again. **Each attempt is its own pre-consume / rollback / post-consume sequence. There is no logical Billing Ledger claim shared across attempts.**

On successful text response, actual usage is extracted from the provider response. Post-call reconcile computes actual Quota, compares it with pre-consumed Quota, and applies the delta. The delta mutation again updates User and API Key independently. It then refreshes or rewrites the User Quota cache, writes a Usage Record, increments User used-quota / request counters, and increments Provider Account used-quota. These are separate operations and may be async or batched depending on configuration.

On upstream response errors recognized after the provider call, pre-consumed Quota is returned asynchronously. On some earlier local errors after pre-consumption, the return path is incomplete. **For audio and image paths the behavior differs**: audio resembles text but has its own rollback branch; image mostly checks User Quota before the call and charges after success, leaving a larger concurrent overspend window.

### 3. INPUTS exhaustive

Tenant-equivalent deployment context, User identity from API Key, API Key status, API Key remaining quota, API Key model scope, API Key subnet/network rule, User status, User Quota, cached User Quota, User Pooling Group, requested model, mapped model, endpoint family, request body, prompt/input size, max output setting, stream flag, Provider Account type, Provider Account model mapping, Provider Account priority, Provider Account status, model billing ratio, Pooling Group billing ratio, pre-consumption configuration, retry count configuration, cache enablement, batch update enablement, logging enablement, Provider Account response status, response usage tokens, elapsed time, final Provider Account selected after retry.

### 4. FAILURES HANDLED

Rejects invalid or exhausted API Keys on the authentication snapshot. Rejects disabled Users. Rejects API Keys outside model scope or network scope. Rejects requests when cached or refreshed User Quota is clearly below estimated pre-consumption. Rolls back some pre-consumed Quota when upstream returns an error response or response conversion fails. Can retry another Provider Account after selected classes of upstream failure. Records Provider Account health errors and may disable bad Provider Accounts. Can batch low-priority statistics updates to reduce write pressure.

### 5. FAILURES NOT HANDLED

Does NOT make User Quota / API Key Quota / Provider Account quota / Usage Record / billing state one atomic mutation. Does NOT claim one logical request before billing, so replay and retry can double-count or double-reserve. Does NOT hold a DB lock between quota check and quota decrement. Cache-based admission can accept concurrent requests that together exceed available Quota. **Trusted high-balance branch can mutate cache without matching DB reservation.** API Key auth cache can remain stale after quota mutation. Provider Account selection is not quota-reserved before the upstream call. Some local failures after pre-consumption do not reliably refund. Usage Record write failure does not roll back quota mutation. Batched updates introduce a crash window where accepted usage has not reached DB.

### 6. KEEP / IMPROVE / AVOID specific to HUAKAI

**KEEP** the user-facing behavior: API Key auth, User status gate, Pooling Group routing, Provider Account retry, pre-call affordability check, post-call actual reconcile, Usage Record, Provider Account usage stats.

**IMPROVE** by replacing cache-first pre-consumption with a DB-owned reservation. HUAKAI uses cache only as a read-through hint, never as the source of truth for allowing spend. Retries across Provider Accounts share one Billing Ledger claim and one reservation lifecycle.

**AVOID** split writes; async quota refund as correctness path; trusted no-reserve high-balance shortcuts; API Key cache decisions without immediate invalidation; post-call-only image charging for strict commercial billing.

### 7. ATTRIBUTION

Reference: one-api, MIT, github.com/songquanpeng/one-api, verified at commit `8df4a2670b98266bd287c698243fff327d9748cf` on 2026-04-28. Used as behavioral evidence only; no source, identifiers, schema, comments, or file structure copied.

## Sub2API Decomposition

### 1. WHY

Sub2API is the **positive idempotent billing reference**. Its strongest contribution is **NOT pre-call reservation**; it is the post-call claim gate that ensures a completed billable request is applied at most once. HUAKAI should adopt the claim concept, but **move it earlier and make it part of strict quota reservation**.

### 2. WHAT step-by-step in HUAKAI vocabulary

A request first authenticates API Key and User. In standard mode, the middleware checks API Key status, API Key expiration, API Key quota snapshot, User status, User balance or active subscription, subscription window limits, and optional IP restrictions. **This is still pre-call validation, not final billing mutation.**

The gateway selects a Provider Account, forwards the request, receives a result, extracts usage, and computes cost. The request body is hashed before the worker task is submitted. The usage worker receives User, API Key, Provider Account, subscription, endpoint metadata, request payload hash, and provider result. It builds a Usage Record object and a billing command.

The idempotency key uses a resolved request identity. Preference is given to a client-stable request identity when present, then a local gateway request identity, then provider response identity, then a generated fallback. The fingerprint is a hash over the billing meaning of the request: User, Provider Account, API Key, Provider Account type, model, service tier, reasoning effort, billing mode, token / image / media usage dimensions, subscription identity, balance cost, subscription cost, API Key quota cost, API Key rate-window cost, Provider Account quota cost, optional request payload hash.

The billing repository starts one DB transaction. First it attempts to claim the `(request identity, API Key)` pair with the fingerprint. If the pair is new, the claim is inserted and the transaction continues. If the pair already exists with the same fingerprint, it returns "not applied" and performs no billing effects. If the pair exists with a different fingerprint, it returns a conflict. **It also checks an archive of older claims, so cleanup does not silently allow old replays.**

After a successful claim, the same transaction mutates every requested billing dimension: subscription usage windows, User balance, API Key quota and exhausted status, API Key rate windows, Provider Account quota counters, and scheduler outbox when Provider Account quota crosses a limit. Then the transaction commits. After commit, caches and notifications are queued, API Key auth cache is invalidated if quota exhaustion occurred, and Provider Account last-used state is scheduled. **The Usage Record is written after billing and is best-effort.**

### 3. INPUTS exhaustive

User, API Key, Provider Account, optional subscription, Pooling Group billing mode, endpoint family, request body hash, client/local/provider request identity, model, requested/upstream model relationship, service tier, reasoning effort, stream flag, duration, first-token latency, input/output/cache/image usage, image count, image size, media type, pricing result, User-specific and Pooling Group rate multipliers, Provider Account billing multiplier, balance cost, subscription cost, API Key quota cost, API Key rate-window cost, Provider Account quota cost, current API Key quota limit, API Key rate-window limits, Provider Account quota limits, subscription daily/weekly/monthly limits, cache invalidation services.

### 4. FAILURES HANDLED

Handles duplicate billing submission for the same logical request. Detects same idempotency key reused with different billing meaning. Keeps billing effects together in one DB transaction. If any effect fails, the claim rolls back with the transaction. Prevents old dedup cleanup from reopening replay billing by archiving old claims. Updates API Key exhausted state in the same billing transaction. Updates rate windows atomically with billing. Returns post-update User balance and Provider Account quota state so notifications can avoid stale reads.

### 5. FAILURES NOT HANDLED

It is still post-call billing. Does NOT reserve User balance / API Key quota / subscription quota / API Key rate window / Provider Account quota before the upstream call. Therefore concurrent requests can pass admission against the same snapshot and only become over-limit after success. User balance deduction can go below zero if policy allows or no guard is applied in the billing transaction. Usage Record write is outside the billing transaction, so Billing Ledger and Usage Record can diverge. Claim key is scoped by API Key and request identity, but HUAKAI's tenant isolation must additionally scope by `tenant_id` everywhere. **Generated fallback request identities are not replay-stable across client retries**, so HUAKAI must require or derive a deterministic logical request identity for strict deduplication.

### 6. KEEP / IMPROVE / AVOID specific to HUAKAI

**KEEP** the claim-first billing transaction, fingerprint conflict check, archived replay guard, atomic mutation of multiple billing dimensions, API Key cache invalidation on exhaustion, post-commit notification/cache outbox.

**IMPROVE** by moving claim creation to pre-call reservation, adding `tenant_id` to every key and mutation, making the request identity stable across retries and Provider Account switches, storing Usage Record inside the final billing transaction rather than best-effort after commit.

**AVOID** relying on post-call billing as the only quota control. Avoid generated non-stable request IDs for commercial billing. Avoid applying balance deduction without a strict "available >= reserve" invariant when selling API access.

### 7. ATTRIBUTION

Reference: Sub2API, LGPL-3.0, github.com/Wei-Shaw/sub2api, verified at commit `b0a2252ed19c3720e6adafde6083e64fbac2efa9` on 2026-04-28. Used as behavioral evidence only; no source, identifiers, schema, comments, or file structure copied.
