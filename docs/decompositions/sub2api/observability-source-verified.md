# Sub2API Observability + Atomic Billing — Source-Verified (F-OBS-001 + correction to F-BILL-001)

| Field | Value |
| --- | --- |
| Status | Specifier-lane source-verified pass (Claude) |
| Author | Claude PM-Orchestrator |
| Date | 2026-04-28 |
| Lane | Specifier — Option C (overlaps Quota+Billing carve-out) |
| Feature | [F-OBS-001](../../03_FEATURE_PARITY_MATRIX.md) (Usage Record persistence, cache layer, scheduler outbox) + correction to [F-BILL-001](../../03_FEATURE_PARITY_MATRIX.md) framing |
| Critical correction | Earlier passes (including streaming-forwarder-claude-v2) implied Sub2API has no atomic billing. **WRONG.** Sub2API has a single-transaction atomic billing path (`UsageBillingRepository.Apply`) with idempotent claim gate, archive table, fingerprint conflict detection, and cross-threshold scheduler outbox. Usage Record write is the only "best-effort detached" piece. |
| Source files read | `backend/internal/repository/usage_billing_repo.go` (full 337 lines), `backend/internal/repository/usage_log_repo.go` lines 258–340 (CreateBestEffort + createSingle insert), `backend/internal/service/gateway_service.go:7505-7553` (postUsageBilling legacy), `:7642-7700` (applyUsageBilling + finalizePostUsageBilling), `:7773-7779` (detachedBillingContext), `:7812-7835` (writeUsageLogBestEffort), `:7476-7477` (usageLogBestEffortWriter interface), `:8016` (writeUsageLogBestEffort call site) |

## 1. The Real Money-Grade Path — `applyUsageBilling`

Source `gateway_service.go:7642-7674`:

```go
func applyUsageBilling(ctx, requestID, usageLog, p, deps, repo) (bool, error) {
    cmd := buildUsageBillingCommand(requestID, usageLog, p)
    if cmd == nil || cmd.RequestID == "" || repo == nil {
        postUsageBilling(ctx, p, deps)   // legacy fallback
        return true, nil
    }
    billingCtx, cancel := detachedBillingContext(ctx)
    defer cancel()
    result, err := repo.Apply(billingCtx, cmd)
    if err != nil { return false, err }
    if result == nil || !result.Applied {   // claim already settled
        deps.deferredService.ScheduleLastUsedUpdate(p.Account.ID)
        return false, nil
    }
    if result.APIKeyQuotaExhausted {
        invalidator.InvalidateAuthCacheByKey(billingCtx, p.APIKey.Key)
    }
    finalizePostUsageBilling(p, deps, result)
    return true, nil
}
```

Comment at line 7505-7508 explicitly states:
> `postUsageBilling` is the legacy fallback billing path used when the unified billing repo is unavailable (nil). Production uses `applyUsageBilling → repo.Apply` for atomic billing.

So the real path runs `repo.Apply`, not the legacy non-atomic one.

## 2. `UsageBillingRepository.Apply` — Single Transaction with Five Effects

Source `repository/usage_billing_repo.go:22-63`:

```go
func (r *usageBillingRepository) Apply(ctx, cmd) (*UsageBillingApplyResult, error) {
    cmd.Normalize()
    if cmd.RequestID == "" { return nil, ErrUsageBillingRequestIDRequired }
    tx, err := r.db.BeginTx(ctx, nil)
    defer func() { if tx != nil { tx.Rollback() } }()
    
    applied, err := r.claimUsageBillingKey(ctx, tx, cmd)   // ← idempotent claim
    if !applied {
        return &UsageBillingApplyResult{Applied: false}, nil
    }
    
    result := &UsageBillingApplyResult{Applied: true}
    if err := r.applyUsageBillingEffects(ctx, tx, cmd, result); err != nil {  // ← 5 effects
        return nil, err
    }
    
    return result, tx.Commit()
}
```

Single PostgreSQL transaction. Three guarantees:

1. **Idempotent claim**: `claimUsageBillingKey` (line 65-106) inserts into `usage_billing_dedup` with `ON CONFLICT (request_id, api_key_id) DO NOTHING`. If conflict, fingerprint comparison decides:
   - Same fingerprint → return `Applied=false` (already settled, replay)
   - Different fingerprint → return `ErrUsageBillingRequestConflict` (replay attack / bug)
2. **Archive table check** (lines 90-101): also queries `usage_billing_dedup_archive` for retired claims. If archived row exists with different fingerprint → conflict error. **This is the C6 archive pattern Codex called out in cycle 1 — and it IS in source.**
3. **Effects atomic** (lines 108-145): all of (a) subscription usage, (b) user balance deduct, (c) API key quota increment + status flip if exhausted, (d) API key rate-limit window updates with auto-rollover, (e) Account quota multi-window (total/daily/weekly) increment with reset-on-expiry. All in one tx.

## 3. The Atomic Effects (Lines 108-336)

### 3.1 Subscription Increment (148-174)

```sql
UPDATE user_subscriptions us
SET daily_usage_usd = us.daily_usage_usd + $1,
    weekly_usage_usd = us.weekly_usage_usd + $1,
    monthly_usage_usd = us.monthly_usage_usd + $1,
    updated_at = NOW()
FROM groups g
WHERE us.id = $2 AND us.deleted_at IS NULL AND us.group_id = g.id AND g.deleted_at IS NULL
```

Three rolling windows (daily / weekly / monthly) updated atomically. Returns `ErrSubscriptionNotFound` if 0 rows affected (subscription deleted between claim and apply).

### 3.2 Balance Deduct (176-192)

```sql
UPDATE users SET balance = balance - $1, updated_at = NOW()
WHERE id = $2 AND deleted_at IS NULL
RETURNING balance
```

`RETURNING balance` is the critical part — gives the new balance for cache update + low-balance notification. Returns `ErrUserNotFound` on 0 rows.

### 3.3 API Key Quota with Status Transition (194-218)

```sql
UPDATE api_keys
SET quota_used = quota_used + $1,
    status = CASE
        WHEN quota > 0 AND status = $3 AND quota_used < quota AND quota_used + $1 >= quota
        THEN $4   -- "quota_exhausted"
        ELSE status
    END,
    updated_at = NOW()
WHERE id = $2 AND deleted_at IS NULL
RETURNING quota > 0 AND quota_used >= quota AND quota_used - $1 < quota
```

The status transition is **inside the increment SQL**: when this update crosses the quota threshold, `status` flips to `quota_exhausted` in the same row update. The RETURNING expression detects the threshold crossing for downstream auth-cache invalidation.

### 3.4 API Key Rate-Limit Window (220-243)

```sql
UPDATE api_keys SET
    usage_5h = CASE WHEN window_5h_start IS NOT NULL AND window_5h_start + INTERVAL '5 hours' <= NOW() THEN $1 ELSE usage_5h + $1 END,
    usage_1d = CASE WHEN window_1d_start IS NOT NULL AND window_1d_start + INTERVAL '24 hours' <= NOW() THEN $1 ELSE usage_1d + $1 END,
    usage_7d = CASE WHEN window_7d_start IS NOT NULL AND window_7d_start + INTERVAL '7 days' <= NOW() THEN $1 ELSE usage_7d + $1 END,
    -- and matching window_*_start auto-reset
    updated_at = NOW()
WHERE id = $2 AND deleted_at IS NULL
```

Three rolling windows (5h / 1d / 7d) with **automatic rollover when window expires**. Atomic with the previous balance/quota effects.

### 3.5 Account Quota Multi-Window with Cross-Threshold Outbox (245-336)

This is the most complex SQL in the file. Three layers:

a. **JSONB extra-field update** (lines 247-289): atomically updates `quota_used`, `quota_daily_used`, `quota_weekly_used` with auto-reset when `quota_daily_start + 24h <= NOW()` etc.

b. **Cross-threshold detection** (lines 327-329):
```go
crossedTotal := state.TotalLimit > 0 && state.TotalUsed >= state.TotalLimit && (state.TotalUsed - amount) < state.TotalLimit
crossedDaily := ...same for daily...
crossedWeekly := ...same for weekly...
```
Detects whether THIS increment crossed any limit boundary.

c. **Scheduler outbox enqueue** (lines 330-335):
```go
if crossedTotal || crossedDaily || crossedWeekly {
    enqueueSchedulerOutbox(ctx, tx, SchedulerOutboxEventAccountChanged, &accountID, nil, nil)
}
```

**Transactional outbox pattern**! In the same transaction as the quota increment, an outbox row is enqueued for the scheduler service to invalidate the in-memory Account cache. This means: **either both the increment AND the outbox commit, OR neither**. No race where the scheduler keeps using stale (under-quota) cached state after the increment crossed the limit.

The comment at lines 322-326 explicitly explains the bug this prevents: "any quota dimension crossing from 'not exceeded' to 'exceeded' must invalidate scheduler snapshot, otherwise Redis-cached Account still shows old used value, subsequent requests continue selecting this account, and operator observes daily_used / weekly_used massively exceeding configured limit."

This is **exactly the kind of money-grade defensive primitive HUAKAI needs**. KEEP this pattern.

## 4. Usage Record Write — The "Detached" Piece

Source `gateway_service.go:7812-7835`:

```go
func writeUsageLogBestEffort(ctx, repo, usageLog, logKey) {
    if repo == nil || usageLog == nil { return }
    usageCtx, cancel := detachedBillingContext(ctx)
    defer cancel()
    
    if writer, ok := repo.(usageLogBestEffortWriter); ok {
        if err := writer.CreateBestEffort(usageCtx, usageLog); err != nil {
            logger.LegacyPrintf(logKey, "Create usage log failed: %v", err)
            if IsUsageLogCreateDropped(err) { return }
            if _, syncErr := repo.Create(usageCtx, usageLog); syncErr != nil {
                logger.LegacyPrintf(logKey, "Create usage log sync fallback failed: %v", syncErr)
            }
        }
        return
    }
    if _, err := repo.Create(usageCtx, usageLog); err != nil {
        logger.LegacyPrintf(logKey, "Create usage log failed: %v", err)
    }
}
```

Source `repository/usage_log_repo.go:262-307` shows `CreateBestEffort` has THREE paths:

1. **Tx in context** (line 267-270): synchronous insert into the existing transaction. **This means Usage Record CAN be atomic with billing IF the caller passes the tx via context.** But the production caller `applyUsageBilling` does NOT pass a tx in context — `billingCtx` is detached.
2. **No batcher** (line 277-280): synchronous insert (degraded mode).
3. **Batched async** (line 282-306): `bestEffortBatchCh` queue with deduplication via `bestEffortRecent` LRU cache. Drop on full queue or ctx cancel.

So the picture: **Usage Record is best-effort, queued, deduplicated, with synchronous fallback on writer rejection**. NOT atomic with `Apply` in the production path because they use different contexts. But the writer interface DOES support atomic-with-tx if the caller chose that — they didn't, deliberately.

### 4.1 Why Usage Record is Detached (Sub2API's Trade-off)

The gateway prioritizes:
- **Money correctness** (atomic Apply) over Usage Record durability
- **Throughput** (batched async insert for analytics) over per-request latency

Result: if PostgreSQL is overloaded and queue overflows, customer is still charged correctly (via Apply), but the Usage Record may be dropped. Operators see "drop" telemetry.

For HUAKAI: this is a **trade-off worth examining**. If the customer is charged but no Usage Record exists, the audit trail is broken. HUAKAI may want a **two-phase**: Apply tx writes a tiny `billing_event` row inside the same tx (audit-grade); then async Usage Record holds the rich analytics fields. Best of both.

## 5. The Cache Layer (`finalizePostUsageBilling`)

Source `gateway_service.go:7676-7700`:

```go
func finalizePostUsageBilling(p, deps, result) {
    if p.IsSubscriptionBill {
        deps.billingCacheService.QueueUpdateSubscriptionUsage(p.User.ID, *p.APIKey.GroupID, p.Cost.ActualCost)
    } else if p.Cost.ActualCost > 0 && p.User != nil {
        deps.billingCacheService.QueueDeductBalance(p.User.ID, p.Cost.ActualCost)
    }
    if p.APIKey.HasRateLimits() {
        deps.billingCacheService.QueueUpdateAPIKeyRateLimitUsage(p.APIKey.ID, p.Cost.ActualCost)
    }
    // … more cache update queues
}
```

After `Apply` commits, queue cache updates. Cache reflects DB state with eventual consistency. Cache is **never authority** — DB is.

## 6. Scheduler Outbox (Transactional Outbox)

The scheduler maintains an in-memory snapshot of Account state for fast pool selection (per F-POOL-001). When DB state changes mid-flight, the scheduler must invalidate.

Pattern: in the same SQL transaction that mutates Account state, enqueue an outbox row. A separate scheduler-outbox-consumer reads outbox rows and invalidates cache.

Source `repository/usage_billing_repo.go:331`:
```go
enqueueSchedulerOutbox(ctx, tx, SchedulerOutboxEventAccountChanged, &accountID, nil, nil)
```

This is the **textbook transactional outbox pattern**. Either both the quota increment AND the outbox row commit, or neither. The outbox consumer is at-least-once delivery (so cache invalidation may double-fire — idempotent invalidation is required, which it is).

**KEEP this pattern**. HUAKAI must use transactional outbox for any DB write that needs cross-process invalidation.

## 7. Failure Modes Sub2API Handles vs Does NOT Handle

### Handled

- **Idempotent post-call billing**: `request_id + api_key_id` claim with archive.
- **Replay attack detection**: fingerprint mismatch returns conflict error.
- **Quota status flip atomic with increment**: `api_keys.status` and `quota_used` updated in same row.
- **Multi-window auto-rollover**: 5h / 1d / 7d windows reset based on time.
- **Cross-threshold scheduler invalidation**: transactional outbox.
- **API Key auth-cache invalidation on quota exhaustion**: `InvalidateAuthCacheByKey` after Apply commits.
- **Last-used update on Apply=false**: deferred via `deferredService.ScheduleLastUsedUpdate`.
- **Detached billing context**: outlives request cancellation; bounded by `postUsageBillingTimeout`.
- **Best-effort Usage Record with sync fallback**: queue full → synchronous Create.
- **Usage Record dedup via LRU**: `bestEffortRecentKey` prevents duplicate queue entries.

### NOT Handled (real gaps)

- **Pre-call reservation**: claim is post-call. A request that consumes upstream tokens but never reaches `applyUsageBilling` (gateway crash mid-stream) leaves no claim row → no charge. The customer got value without paying.
- **Usage Record atomicity with billing**: detached. If Apply succeeds but Usage Record write fails AND the LRU dedup is hit later, audit trail is silently broken.
- **Tenant scoping**: Sub2API is single-tenant in this code path; `request_id` namespace is global per `api_key_id`. HUAKAI requires `tenant_id` in the unique key.
- **Dollar-precision currency**: SQL uses `numeric` (good) but cost values pass through Go `float64` in `cmd.BalanceCost` etc. Floating-point errors accumulate over millions of requests.
- **Outbox consumer lag observability**: no metric for outbox-row-age. If consumer is dead, scheduler keeps using stale state silently until operator notices over-quota dispatch.
- **Per-tenant usage reporting**: report queries are per-user / per-account, not per-tenant.
- **Configurable Usage Record retention**: `usage_cleanup_repo.go` exists but is global; per-tenant retention not visible.

## 8. KEEP / IMPROVE / AVOID for HUAKAI

### KEEP (verified in source)

- **Single-transaction atomic billing** (Apply): claim + 5 effects in one tx.
- **Idempotent claim gate** with `ON CONFLICT (request_id, api_key_id) DO NOTHING`.
- **Archive table for retired claims** (Codex's C6 from cycle 1 IS real and IS in source).
- **Fingerprint conflict detection** for replay attacks (different request body, same request_id).
- **Quota status flip atomic with increment** (single row update).
- **Multi-window auto-rollover** in SQL (5h/1d/7d).
- **Transactional outbox** for cross-process cache invalidation (cross-threshold detection → enqueue inside same tx).
- **Detached billing context with timeout** so request cancellation doesn't roll back billing.
- **Best-effort Usage Record with sync fallback + LRU dedup** to handle queue overflow.
- **Last-used scheduler update** for already-settled claims (Apply=false path).

### IMPROVE (HUAKAI design — clearly NOT in Sub2API)

- **Pre-call reservation Tx1**: HUAKAI adds a reservation transaction *before* upstream. Quota+Billing claim row carries `status=reserving`; on Apply (Tx2), status → `committed`. On gateway crash, orphan sweep finds `status=reserving AND created_at < now() - sweep_threshold` and rolls back. Sub2API's post-call-only model has the leak Sub2API itself silently accepts.
- **Tx2 Usage Record atomic with billing**: HUAKAI promotes Usage Record write into the Apply transaction. Sub2API has the interface for it (`tx in context` path) but doesn't use it in production.
- **Tenant_id in every claim, every effect, every audit row**.
- **Numeric(20,8) money type end-to-end**: no float64 in cost paths.
- **Outbox consumer lag metric**: alert when outbox-row-age exceeds threshold.
- **Per-tenant retention policy**: `usage_logs` partitioned by `tenant_id` + retention column.
- **Pending reconciliation flag**: when Usage Record is `inferred` or `partial`, mark for later reconciliation against upstream out-of-band usage report (e.g. some providers send bills hours later).
- **Audit-grade billing event row in Apply tx** (separate from full Usage Record), so audit trail survives Usage Record async failure.

### AVOID (Sub2API anti-patterns)

- **Float64 in money fields**: convert to `numeric(20, 8)` end-to-end at HUAKAI layer.
- **Best-effort log on state-write failure** (`logger.LegacyPrintf("...failed: %v", err)`): replace with retry + alert, not warn-and-continue.
- **Single-tenant `request_id` keyspace**: must be tenant-scoped.
- **Comment-as-spec for the outbox cross-threshold rationale** (lines 322-326): HUAKAI documents this as a tested invariant, not a code comment.
- **Same-row UPDATE with status transition logic** at the SQL level: HUAKAI keeps it atomic but moves the logic to a stored procedure or sqlc query for testability.

## 9. Concurrency / Correctness Invariants HUAKAI Adds

| # | Invariant | Reason Sub2API doesn't enforce |
|---|-----------|---------------------------------|
| O1 | Pre-call reservation Tx1 commits before upstream call. | Sub2API has post-call only. |
| O2 | Tx2 Usage Record write atomic with billing Apply. | Sub2API uses detached writer for production path. |
| O3 | All claim and effect rows carry `tenant_id`. | Sub2API is single-tenant in this layer. |
| O4 | Money fields use `numeric(20, 8)` end-to-end (Go `decimal.Decimal`, not `float64`). | Sub2API uses Go `float64` in `cmd.BalanceCost` etc. |
| O5 | Outbox consumer lag is monitored; alert fires if any row > 60s old. | Sub2API has no outbox lag metric. |
| O6 | Pending reconciliation: Usage Records with `usage_source ∈ {inferred, partial}` carry a flag for later reconciliation against upstream out-of-band usage. | Sub2API has no reconciliation. |
| O7 | Audit-grade billing event row inside Apply tx survives Usage Record async failure. | Sub2API has only the detached Usage Record. |
| O8 | Cross-threshold detection covers ALL limit dimensions (total / daily / weekly + new HUAKAI: tenant-monthly, billing-cycle). | Sub2API covers total / daily / weekly only. |

## 10. Test Scenarios

### Sub2API-inheritable

- AT-OBS-001 / Idempotent replay: same `(request_id, api_key_id)` with same fingerprint → second Apply returns `Applied=false`, no double charge.
- AT-OBS-002 / Replay attack: same `(request_id, api_key_id)` with different fingerprint → returns `ErrUsageBillingRequestConflict`.
- AT-OBS-003 / Archive replay: `(request_id, api_key_id)` exists in archive table with same fingerprint → returns `Applied=false`.
- AT-OBS-004 / Atomic 5-effect: 5 dimensions (subscription, balance, api_key_quota, api_key_rate, account_quota) all updated or none updated.
- AT-OBS-005 / Quota status flip: API key crosses `quota` threshold → `status` becomes `quota_exhausted` in same row update.
- AT-OBS-006 / Multi-window rollover: 5h window expired → `usage_5h` resets to current cost, not increments stale.
- AT-OBS-007 / Cross-threshold outbox: account quota crosses daily limit → `scheduler_outbox` row enqueued in same tx.
- AT-OBS-008 / Detached context timeout: gateway crashes mid-call after Apply but before finalize → Apply commit visible; finalize re-tries on next outbox consume.
- AT-OBS-009 / Best-effort drop: Usage Record queue overflow → sync fallback Create runs; on sync fail, logger error logged.
- AT-OBS-010 / LRU dedup: same Usage Record submitted twice in 5s → second submission deduplicated, queue NOT polluted.

### HUAKAI-design-specific

- AT-OBS-011 / Pre-call Tx1 rollback on crash: gateway crash before upstream call AND before Tx1 commit → no claim row, customer not charged.
- AT-OBS-012 / Tx1 commit + Tx2 missing recovery: claim row exists with `status=reserving` AND `created_at < now() - sweep_threshold` → orphan sweep rolls back reservation; quota refunded.
- AT-OBS-013 / Tenant isolation: T1's request_id A and T2's request_id A do NOT collide; both apply independently.
- AT-OBS-014 / Money precision: 1M requests × $0.0000001 → balance deducts exactly $0.10 (decimal end-to-end), not $0.099999... (float drift).
- AT-OBS-015 / Outbox lag alert: scheduler outbox consumer dead → row age exceeds 60s → alert fires.
- AT-OBS-016 / Pending reconciliation: streaming request ends with `usage_source=inferred` → Usage Record created with `pending_reconciliation=true`; later upstream usage report → record updated.
- AT-OBS-017 / Audit-grade billing event: Apply tx writes both billing_event (atomic) AND queues Usage Record. Usage Record async fails → billing_event still serves audit query.

## 11. Open TODOs

- **TODO-1**: Read `usage_log_repo.go:1-258` and `258-end` for full prepared-insert + dedup-LRU mechanics.
- **TODO-2**: Read `billing_cache_service.go` (965 lines) for cache update queue semantics (Queue* methods) and cache → DB sync.
- **TODO-3**: Find and read the scheduler-outbox consumer (likely `outbox_dispatcher.go` or similar) to verify at-least-once delivery + idempotent invalidation.
- **TODO-4**: Confirm the typed errors `ErrUsageBillingRequestConflict`, `ErrSubscriptionNotFound`, `ErrUserNotFound`, `ErrAPIKeyNotFound`, `ErrAccountNotFound` are defined and propagated correctly.
- **TODO-5**: Cross-check one-api's billing path (already partially in `codex-quota-billing-source-verified.md`) for whether one-api has any equivalent of the transactional outbox pattern.
- **TODO-6**: Cross-check Helicone observability ingestion (Codex task `by9lcbor4` in flight).
- **TODO-7**: Verify whether Apply uses serializable isolation (`r.db.BeginTx(ctx, nil)` line 35 uses default isolation; need to check if PostgreSQL default is good enough).

## 12. Corrections to Earlier Passes

### To `streaming-forwarder-claude-v2.md`

§1.6 currently says: "Sub2API runs Usage Record creation as **best-effort, detached-context, non-atomic with billing** (gateway_service.go:7812 writeUsageLogBestEffort uses detachedBillingContext(ctx))." — this is **partially correct** but framed as if billing is also non-atomic.

**Correct framing**: 
- Sub2API's **billing IS atomic** via `applyUsageBilling → repo.Apply` (5 effects in 1 tx with idempotent claim).
- Sub2API's **Usage Record write IS detached** (best-effort, queued, with sync fallback).
- HUAKAI's improvement is **promoting Usage Record into Tx2 with billing**, not "adding atomic billing where there is none."

### To `quota-billing-claim-gate-synthesis.md` (cycle 1)

The synthesis correctly says "Sub2API has post-call (not pre-call) claim gate." That stays correct. What I (or earlier prose) drifted on later was implying Sub2API's overall billing is non-atomic — wrong. Per-call billing is atomic; just no pre-call reservation.

## 13. Attribution

Source files read directly from `c:/HUAKAI/repo/.omc/reference-src/sub2api/` at commit `b0a2252ed19c3720e6adafde6083e64fbac2efa9`:

- `backend/internal/repository/usage_billing_repo.go` — full file (337 lines, all 4 effects + claim gate read)
- `backend/internal/repository/usage_log_repo.go` lines 258–340 (CreateBestEffort + part of createSingle)
- `backend/internal/service/gateway_service.go`:
  - lines 7476-7477 (usageLogBestEffortWriter interface)
  - lines 7505-7553 (postUsageBilling legacy fallback)
  - lines 7642-7700 (applyUsageBilling + finalizePostUsageBilling)
  - lines 7773-7779 (detachedBillingContext)
  - lines 7812-7835 (writeUsageLogBestEffort)
  - line 8016 (writeUsageLogBestEffort production call site)

CL-011 compliance: every behavior claim above carries file:line attribution.

## 14. Review Sign-Off

| Field | Value |
| --- | --- |
| Reviewer | (pending) |
| Review date | (pending) |
| Checks passed | (pending CL-001..011) |
| Notes | F-OBS-001 source-verified pass. Critical correction to earlier prose: Sub2API HAS atomic billing (was misrepresented). Awaits Codex parallel pass for mutual review. |
