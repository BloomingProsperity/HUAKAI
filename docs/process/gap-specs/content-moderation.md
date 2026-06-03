# Gap Spec: Content Moderation (F-CONTENT-MOD-001)

**Produced by:** residual-verification subagent (READ-ONLY pass)  
**Date:** 2026-06-03  
**Migration reserved:** 0077 (current max is 0076 — verified)  
**Risk class:** schema + money (pre-dispatch screener is safe-read; violation-fee path touches billing_ledger_claims via Abort — same class as normal request abort)

---

## 1. False Premises in the Design

The following design claims were checked against real code and are WRONG or need correction.

### FP-1: Violation-fee billing uses Settle() with EndClass = "content_policy_violation"

**Design claim (line ~246–249):** `FeeCharger.ChargeViolation` calls `billing.ClaimGate.Reserve` (Tx1) then `billing.Settler.Settle` (Tx2) with `EndClass = "content_policy_violation"`.

**Reality (verified):**

- `usage_records.end_class` has a hard `CHECK` constraint at `sql/migrations/0002_observability_billing.up.sql:146–154` permitting only: `stream_end_graceful`, `stream_end_no_terminal_marker`, `upstream_error_4xx`, `upstream_error_5xx`, `upstream_rate_limit`, `upstream_auth_failure`, `first_token_timeout`, `inter_event_timeout`, `total_stream_timeout`, `client_disconnect`, `event_size_exceeded`, `orchestrator_cancelled`, `usage_ambiguous`, `unknown_termination`, `non_streaming`. The value `content_policy_violation` is NOT in this list. Inserting it would cause a Postgres constraint violation on every fee-charge attempt.

- `billing.Settler.Settle()` (internal/billing/settler.go:222–231) always calls `ReleaseSlotAndDecrementInFlight` with the `AcquisitionToken`. For a pre-dispatch block (keyword/hash match before `selectPoolAccount`), there is no pool slot acquisition token. Calling `Settle()` here would fail with `ErrSlotReleaseMissed` (released==0).

- For a post-upstream-error charge (upstream returns `platform_policy`), a pool slot WAS acquired, so `Settle()` is usable — but `EndClass` must be one of the allowed values. The correct value to use is `upstream_error_4xx` (the actual classification for `ErrorClassPlatformPolicy` per internal/gateway/error_apply.go:45).

**Correction for FP-1:** The violation-fee billing path cannot use `Settle()` as described for pre-dispatch blocks. For post-upstream-policy errors: use the existing `Settle()` with `EndClass = gateway.UpstreamError4xx` (`"upstream_error_4xx"`) and set `ActualCost = violationFeeUSD`. No new EndClass value is needed and no migration to `usage_records` is required for the end_class column.

### FP-2: Design does not distinguish pre-dispatch vs post-upstream fee paths

**Design claim:** Both the pre-dispatch screener (keyword/hash block) and the post-upstream platform_policy error are wired to the same `FeeCharger.ChargeViolation` path using `Reserve` + `Settle`.

**Reality:** They require different billing treatments:

- **Pre-dispatch block** (keyword/hash match, no pool slot acquired): The existing reserve was already created before the screener call (per `chat_completions_dispatch.go:prepareClaimAndAccount` — the screener runs before `selectPoolAccount` per the design). Therefore the claim is in `reserving` state with no acquisition token. The correct disposition is `Settler.Abort()` with `reason = "content_policy_block"` and optionally a violation fee as a separate charge event. However, `Abort()` writes zero cost by design (see settler.go:259–450). A violation fee for pre-dispatch blocks requires either (a) skip charging (fee only applies to upstream policy errors where upstream cost was already incurred), or (b) a separate DB write outside the Settler. Option (a) is the cleanest and matches the design's own Risk #4: only `block_keyword` and `block_hash` decisions are NOT counted toward upstream cost because they never reach the upstream.

- **Post-upstream error** (upstream returns 403 with `platform_policy` body keyword): Pool slot IS acquired. The existing `Settle()` path with `ActualCost = violationFeeUSD` and `EndClass = gateway.UpstreamError4xx` works correctly.

**Correction for FP-2:** The design overstates the pre-dispatch fee path. The genuinely new billing work is limited to the post-upstream policy-error path, which reuses `Settler.Settle()` with a non-zero `ActualCost` — this already works without code changes to the billing package.

### FP-3: Design claims moderation_log.billing_event_id is an FK to billing_events.id

**Design claim (migration, line ~149):** `billing_event_id bigint` references `billing_events.id`.

**Reality:** This is technically valid (billing_events.id exists per migration 0002), but there is no need for a FK here given that moderation_log is audit-only. An FK constraint adds write-time overhead on every moderation log insert and requires the billing event to be committed before the moderation log row. Since the fee charge happens in one transaction and the audit log in another, the ordering is safe but the FK creates coupling. The FK should be a bare `bigint` reference (no constraint), or be omitted from the first slice.

### FP-4: Design assumes "chat_completions_dispatch.go" is a frozen file but must be edited

**Design claim (line ~63–65):** "Neither point modifies the frozen packages... callers in those packages are updated at the two existing integration points inside `chat_completions_dispatch.go` and `chat_completions_attempt.go`".

**Reality (verified):** `internal/gatewayhttp` IS listed as a frozen package ("no new files"). The design then immediately says existing files in it can be edited — that IS consistent with the constraint ("editing existing is OK"). The `ChatHandlerDeps` struct in `chat_completions_handler.go` (line 38) is a struct in a frozen package. Adding fields to it is an edit to an existing file — this is allowed per the constraint. No false premise here, but the spec must be explicit that only the three specific files (`chat_completions_handler.go`, `chat_completions_dispatch.go`) receive minimal additions.

### FP-5: Design claims payloadHash must be pre-computed in chatExecution for the screener

**Design claim:** `Screener.Screen` receives a `PayloadHash` from `chatExecution`.

**Reality (verified):** `chatExecution.payloadHash` IS computed via `normalizedPayloadHash(ex.body)` (internal/gatewayhttp/chat_completions_billing.go:247 — `sha256.Sum256(body)` hex). This field already exists on `chatExecution` at line 116 in `chat_completions_handler.go`. The screener can receive this value directly — no new hash computation needed. The payloadHash is set inside `ensureIdempotencyState()` which is called before `reserveClaim`. The screener call should go after `ensureIdempotencyState()` returns (i.e., after `payloadHash` is populated) and before `selectPoolAccount`. This placement in `prepareClaimAndAccount` (dispatch.go:249) is correct.

### FP-6: Design treats api_keys.status = 'banned' as an auto-ban mechanism

**Design claim:** The auto-ban counter sets the API key to banned status.

**Reality (verified):** `api_keys.status` has a `CHECK (status IN ('active', 'disabled', 'revoked', 'expired'))` constraint (migration 0007, line 58–59). There is NO `'banned'` value. Auto-ban via moderation must use `status = 'disabled'` — or the migration 0077 must NOT add a `banned` status to `api_keys` (which would require altering a core table in a way that breaks existing CHECK constraints). The cleanest approach is to track the banned state in the new `moderation_log` table's aggregation (count of block decisions in window) and mark the key as `'disabled'` via an UPDATE on `api_keys.status`. The unban endpoint would set it back to `'active'`.

---

## 2. True Residual (What Is Genuinely Missing)

After removing false premises, the genuine gap is:

1. **New tables** (migration 0077): `moderation_keywords`, `moderation_hashes`, `moderation_log`, `moderation_config` — all new. These do not overlap with any existing table.

2. **Pre-dispatch keyword + hash screener** (`internal/moderation`): No equivalent exists. `internal/gateway/error_normalize.go` classifies upstream errors but does NOT inspect inbound request bodies. The screener is a new in-process gate.

3. **Sampled audit logger** writing to `moderation_log` — new.

4. **Ban counter** using `moderation_log` aggregation + `api_keys.status = 'disabled'` write — new.

5. **Violation-fee billing for post-upstream platform_policy errors** — partially new: the billing machinery (`Settle()`) already exists and works. The new piece is the conditional call inside `dispatchCanonicalBuffered`/`dispatchRawBuffered` when `classification.Class == gateway.ErrorClassPlatformPolicy`, passing `ActualCost = violationFeeUSD` instead of zero. This requires editing `chat_completions_dispatch.go` (existing file in frozen package — edits allowed).

6. **Admin CRUD endpoints** (`internal/moderationhttp`): `moderation_keywords`, `moderation_hashes`, `moderation_config` CRUD + `moderation_log` query + ban list/unban — all new.

7. **Route wiring** in `cmd/gateway/routes.go` for the new admin endpoints.

---

## 3. Reuse Points (Existing Code)

| Item | File:line | What it gives |
|---|---|---|
| `gateway.ErrorClassPlatformPolicy` | internal/gateway/error_normalize.go:37 | Existing classification constant for upstream 403 policy errors |
| `gateway.UpstreamError4xx` (EndClass) | internal/gateway/forwarder_types.go:24 | Correct end_class for violation-fee settle |
| `chatExecution.payloadHash` | internal/gatewayhttp/chat_completions_handler.go:116 | SHA-256 hex of request body, already computed |
| `normalizedPayloadHash()` | internal/gatewayhttp/chat_completions_billing.go:247 | SHA-256 hex function, reuse same hash |
| `billing.ClaimGate` / `billing.Settler.Settle()` | internal/billing/billing.go:25,33 | Existing Tx1/Tx2 machinery for violation fee |
| `billing.Settler.Abort()` | internal/billing/billing.go:43 | For pre-dispatch block disposition |
| `admin.AdminResolver` + `admin.AdminIdentity` | internal/admin/operator_auth.go:39 | Admin auth — reuse same resolver pattern as adminhttp |
| `privacy.LogSystem` | internal/privacy/logger.go:88 | System-level logging for fee charge failures |
| `d.adminAuth` in routes.go | cmd/gateway/routes.go:382 | Auth dep already on deps struct |
| `shopspring/decimal` | internal/billing/billing.go:19 | Already a dependency |

---

## 4. First Slice Spec

The first slice is: **pre-dispatch screener core + migration + admin keyword/config CRUD**. This is the highest-value, completable, collision-free slice. It does NOT include violation-fee billing (which requires editing `chat_completions_dispatch.go` — a potential collision point) or the ban counter (which depends on the screener being wired).

### Migration

**File to ADD:** `sql/migrations/0077_content_moderation.up.sql`  
**File to ADD:** `sql/migrations/0077_content_moderation.down.sql`

Tables: `moderation_keywords`, `moderation_hashes`, `moderation_log`, `moderation_config` — as designed with these corrections:

- `moderation_log.billing_event_id` is a bare `bigint` (no FK constraint) to avoid write-ordering coupling.
- No new column added to `api_keys` or `usage_records`.
- The `moderation_config` table stands as designed.

### New package: internal/moderation

Files to ADD (each under 500 lines):

- `internal/moderation/doc.go` (~20 lines): package doc + invariant list
- `internal/moderation/screener.go` (~120 lines): `Screener` interface + `ScreenRequest{TenantID int64, APIKeyID int64, UserID int64, PayloadHash string, RequestID string}` + `ScreenResult{Decision Decision, ReasonCode string}` + `Decision` type constants (`DecisionPass`, `DecisionBlockKeyword`, `DecisionBlockHash`)
- `internal/moderation/keyword_store.go` (~140 lines): `KeywordStore` interface + `DBKeywordStore` with bounded LRU cache (TTL 30s)
- `internal/moderation/hash_store.go` (~110 lines): `HashStore` interface + `DBHashStore` with bounded LRU cache (TTL 30s)
- `internal/moderation/ban_counter.go` (~130 lines): `BanCounter` interface + `DBBanCounter` (counts block decisions in `moderation_log` within window, updates `api_keys.status = 'disabled'`)
- `internal/moderation/audit_log.go` (~130 lines): `AuditLogger` interface + `DBModerationLogger` (sampled write to `moderation_log`)
- `internal/moderation/sampler.go` (~60 lines): deterministic sampler (fnv hash % sample_rate_pct)
- `internal/moderation/config.go` (~70 lines): `ModerationConfig` value type + DB loader
- `internal/moderation/errors.go` (~30 lines): sentinel errors (`ErrModerationDisabled`, `ErrScreenerBackend`)

### New package: internal/db/moderation (sqlc-generated)

Added to `sql/queries/moderation.sql` (new file). Queries needed:

```sql
-- name: GetModerationConfig :one
SELECT * FROM moderation_config WHERE tenant_id = $1;

-- name: ListEnabledKeywords :many
SELECT id, keyword FROM moderation_keywords
WHERE tenant_id = $1 AND enabled = true;

-- name: ContainsHash :one
SELECT id FROM moderation_hashes
WHERE tenant_id = $1 AND hash_hex = $2 AND enabled = true LIMIT 1;

-- name: InsertModerationLog :one
INSERT INTO moderation_log (tenant_id, api_key_id, user_id, request_id,
    payload_hash, decision, reason_code, violation_fee_usd, billing_event_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING id;

-- name: CountBlocksInWindow :one
SELECT COUNT(*) FROM moderation_log
WHERE tenant_id = $1 AND api_key_id = $2
  AND decision IN ('block_keyword', 'block_hash')
  AND occurred_at >= now() - ($3 * interval '1 second');

-- name: UpsertModerationConfig :one
INSERT INTO moderation_config (tenant_id, enabled, sample_rate_pct,
    ban_threshold, ban_window_seconds, violation_fee_usd, updated_by)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (tenant_id) DO UPDATE
  SET enabled = EXCLUDED.enabled,
      sample_rate_pct = EXCLUDED.sample_rate_pct,
      ban_threshold = EXCLUDED.ban_threshold,
      ban_window_seconds = EXCLUDED.ban_window_seconds,
      violation_fee_usd = EXCLUDED.violation_fee_usd,
      updated_by = EXCLUDED.updated_by,
      updated_at = now()
RETURNING *;
```

Note: `CountBlocksInWindow` uses `($3 * interval '1 second')` — `$3` is `ban_window_seconds integer`. Verify column name `occurred_at` exists in `moderation_log` (it does: defined in migration).

### New package: internal/moderationhttp

Files to ADD (first-slice subset — keyword + config admin only):

- `internal/moderationhttp/admin_keywords_handler.go` (~200 lines): `GET/POST/DELETE /admin/v1/moderation/keywords`
- `internal/moderationhttp/admin_config_handler.go` (~160 lines): `GET/PUT /admin/v1/moderation/config`
- `internal/moderationhttp/mount.go` (~80 lines): `MountModerationAdminRoutes(r chi.Router, deps ModerationAdminDeps)` + `ModerationAdminDeps` struct

### Existing files to EDIT (first slice only)

**`cmd/gateway/routes.go`** — add inside `mountAdminRoutes`:
```go
r.Route("/admin/v1/moderation", func(r chi.Router) {
    moderationhttp.MountModerationAdminRoutes(r, moderationhttp.ModerationAdminDeps{
        Auth:    d.adminAuth,
        Queries: d.moderationQueries,  // new field on deps
    })
})
```

**`cmd/gateway/main.go` (or deps.go)** — add `moderationQueries *dbmoderation.Queries` field and initialize it from `pgPool`. (Edit only; no new file required in cmd/.)

No edits to `internal/gatewayhttp/*` in the first slice. The screener wiring into the hot path (`chat_completions_dispatch.go`) is slice 2 to avoid collision with parallel work.

---

## 5. Discriminating Tests

Each test name + the exact code mutation that makes it go red:

### Package internal/moderation

1. **TestScreener_KeywordMatch_RejectsRequest**  
   Mutation: `Screener.Screen` returns `DecisionPass` unconditionally.  
   Test: stub `KeywordStore.List` → `["forbidden"]`; call `Screen` with `PayloadHash` of a body containing `"forbidden"`. Assert `ScreenResult.Decision == DecisionBlockKeyword`.

2. **TestScreener_HashPrecheck_RejectsKnownHash**  
   Mutation: `Screener.Screen` skips hash store lookup.  
   Test: stub `HashStore.Contains` → `true`. Assert `DecisionBlockHash`.

3. **TestScreener_HashTakesPriorityOverKeyword**  
   Mutation: keyword match short-circuits before hash check.  
   Test: stub both stores to match. Assert `DecisionBlockHash` (hash wins).

4. **TestScreener_KeywordStoreError_FailsClosed**  
   Mutation: error from `KeywordStore.List` causes `Screen` to return `pass`.  
   Test: stub `List` → `errors.New("db gone")`. Assert `Screen` returns non-nil error wrapping `ErrScreenerBackend`.

5. **TestScreener_DisabledConfig_SkipsChecks**  
   Mutation: disabled config still calls keyword store.  
   Test: `ModerationConfig.Enabled = false`. Assert `DecisionPass` and keyword store is never called (verified via fake call counter).

6. **TestBanCounter_ThresholdTriggered_DisablesKey**  
   Mutation: `BanCounter.RecordAndCheck` does not update `api_keys.status` when threshold reached.  
   Test: insert `threshold` `block_keyword` rows in `moderation_log`; call `RecordAndCheck`. Assert `api_keys.status = 'disabled'` for the key.

7. **TestBanCounter_WindowExpiry_DoesNotBan**  
   Mutation: violations outside the window count toward threshold.  
   Test: insert `threshold-1` rows older than `ban_window_seconds`; call `RecordAndCheck` once. Assert key remains `'active'`.

8. **TestAuditLogger_ZeroSampleRate_DoesNotWrite**  
   Mutation: `AuditLogger` writes regardless of sample rate.  
   Test: `SampleRatePct = 0`. Assert `moderation_log` row count does not increase.

9. **TestAuditLogger_StoresHashNotBody**  
   Mutation: `AuditLogger` stores a body-derived field other than payload_hash.  
   Test: call `Log(Decision=pass, PayloadHash="abc...")`. Assert inserted row has `payload_hash = "abc..."` and `reason_code` does not contain raw keyword text.

### Package internal/moderationhttp

10. **TestAdminKeywords_PostAddsKeyword**  
    Mutation: handler does not persist keyword or returns non-201.  
    Test: POST `/admin/v1/moderation/keywords` with valid body; assert HTTP 201 and DB row present.

11. **TestAdminKeywords_DuplicateReturns409**  
    Mutation: duplicate keyword upserts silently instead of returning conflict.  
    Test: post same keyword twice; assert second call returns 409.

12. **TestAdminConfig_GetReturnsDefaults**  
    Mutation: GET returns 404 when no config row exists.  
    Test: no prior config; GET `/admin/v1/moderation/config`; assert 200 with default values.

---

## 6. Parallelizable Assessment

**True.** The first slice (migration + `internal/moderation` + `internal/moderationhttp` keyword/config + route wiring) does NOT edit `internal/gatewayhttp/chat_completions_dispatch.go` or `chat_completions_handler.go`. It only adds new packages and a small route mount in `routes.go`. A second parallel slice can implement the screener wiring into the hot path without touching the same files as the first slice, except for a single collision point: both slices need `routes.go`. That conflict is small and mergeable (the route block is additive).

---

## 7. Notes on Remaining Slices (not first slice)

**Slice 2 — screener hot-path wiring:**  
Edit `internal/gatewayhttp/chat_completions_handler.go`: add `Screener moderation.Screener` field to `ChatHandlerDeps`.  
Edit `internal/gatewayhttp/chat_completions_dispatch.go`: in `prepareClaimAndAccount`, after `ensureIdempotencyState()` and before `selectPoolAccount`, call `ex.d.Screener.Screen(...)`. If `Decision != DecisionPass`, call `ex.d.Settler.Abort(...)` with reason `"content_policy_block"`, write HTTP 451, return.  
Wire in `cmd/gateway/routes.go` chatHandlerDeps.

**Slice 3 — violation-fee billing on upstream platform_policy error:**  
Edit `internal/gatewayhttp/chat_completions_dispatch.go`: in the `dispatchCanonicalBuffered` error branch, when `classification.Class == gateway.ErrorClassPlatformPolicy` and `violationFeeUSD > 0`, call `Settler.Settle()` with `ActualCost = violationFeeUSD`, `Draft.EndClass = gateway.UpstreamError4xx`, all token counts zero. Write `moderation_log` row with `decision = 'fee_charged'`.

**Slice 4 — hash blocklist + ban counter admin handlers:**  
Add `internal/moderationhttp/admin_hashes_handler.go`, `admin_ban_handler.go`, `admin_log_handler.go`. Wire into mount.go.
