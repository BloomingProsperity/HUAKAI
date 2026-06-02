# Gap Design: Application-Level Per-User / Per-Group Rate Limiting

**Feature ID:** F-RL-APP-001  
**Author:** HUAKAI Backend Architect  
**Date:** 2026-06-03  
**Status:** READY FOR IMPLEMENTATION  

---

## Summary

HUAKAI currently enforces two rate-limiting layers:

| Existing layer | Location | Key | Metric |
|---|---|---|---|
| IP front-door (global + auth-strict) | `cmd/gateway/rate_limit.go` | client IP | requests/window |
| Upstream-account cooldown | `internal/rate/` | provider account | 429/5xx back-pressure |

**What is missing:** application-level token-bucket enforcement keyed on
`(tenant_id, user_id)` and `(tenant_id, user_group)` for **RPM** (requests per
minute) and **TPM** (tokens per minute), with per-group policy overrides and
multi-instance safety.

This design introduces a new package `internal/userratelimit` that:

1. Maintains in-process token buckets for RPM and TPM, keyed per-user and
   per-group.
2. Loads per-group policy overrides from a PostgreSQL table
   (`user_rate_limit_policies`, migration 0077), falling back to tenant-level
   defaults.
3. Exposes a `Gate` interface consumed by the chat handler *after* auth
   resolution and *before* `ClaimGate.Reserve`, so a denied request never
   touches billing or pool selection.
4. Tracks a success-count sliding window (60-second tumbling windows) per user
   so observe-mode policies can emit accurate metrics without blocking.
5. Is multi-instance safe: buckets are per-process (not shared across replicas).
   Policy limits are read from DB at startup and lazily refreshed; no
   distributed lock is needed because each instance enforces independently and
   aggregate throughput across N instances converges to N × per-instance limit
   (this matches standard token-bucket SaaS practice and is documented as the
   chosen trade-off — see §Risks).

---

## Package layout

All new code lives in **one new package**: `internal/userratelimit`.  
No files are added to the frozen packages `internal/gatewayhttp`,
`internal/gateway`, or `internal/proto`; those receive only **one-line field
additions** to existing structs (see §Endpoints).

```
internal/userratelimit/
  doc.go               ~30 lines   Package doc + design note
  types.go             ~120 lines  Policy, Decision, Scope, Metrics types
  policy_store.go      ~90 lines   PolicyStore interface + PostgreSQL adapter (sqlc shim)
  policy_loader.go     ~130 lines  Background-refreshing policy cache (TTL 60s)
  bucket_registry.go   ~160 lines  Per-(user/group) TokenBucket registry; bounded map + eviction
  gate.go              ~200 lines  Gate.Check(ctx, GateInput) → GateDecision; RPM + TPM paths
  success_window.go    ~110 lines  60-second tumbling success counter per user
  middleware.go        ~130 lines  chi-compatible middleware wrapping Gate; writes 429 JSON
  errors.go            ~40 lines   Sentinel errors
  gate_test.go         ~300 lines  Unit tests (see §Discriminating tests)
  bucket_registry_test.go ~150 lines
  success_window_test.go  ~100 lines
  policy_loader_test.go   ~120 lines

sql/migrations/
  0077_user_rate_limit_policies.up.sql    ~70 lines
  0077_user_rate_limit_policies.down.sql  ~15 lines

cmd/gateway/
  (modify existing) middleware.go        +~30 lines   wire userratelimit.Middleware after auth
  (modify existing) wiring.go            +~25 lines   construct Gate + inject dependencies
  (modify existing) config.go            +~20 lines   RPM/TPM env defaults
```

Every hand-written file is well under 500 lines. The test files are also within
bound because each covers only one concern.

---

## Schema / migrations

### Migration 0077

**File:** `sql/migrations/0077_user_rate_limit_policies.up.sql`

```sql
BEGIN;

-- Per-group (or per-tenant global) rate limit policies for application-level
-- RPM / TPM enforcement (F-RL-APP-001).
--
-- Scope resolution order (highest priority first):
--   1. user_group IS NOT NULL AND user_group = caller's users.user_group  → group override
--   2. user_group IS NULL                                                  → tenant default
--
-- Both rpm_limit and tpm_limit may be NULL (meaning "unlimited for that metric").
-- mode:  'enforce' = 429 on exceed | 'observe' = allow + metrics only | 'disabled' = skip
--
-- Multi-instance note: each gateway process loads this table at startup and
-- refreshes every 60 s via policy_loader.go.  No distributed lock needed;
-- per-process buckets are independent.
CREATE TABLE IF NOT EXISTS user_rate_limit_policies (
    id            bigserial     PRIMARY KEY,
    tenant_id     bigint        NOT NULL REFERENCES tenants(id),
    user_group    text,                          -- NULL = tenant-wide default
    rpm_limit     integer       CHECK (rpm_limit IS NULL OR rpm_limit > 0),
    tpm_limit     integer       CHECK (tpm_limit IS NULL OR tpm_limit > 0),
    -- burst = integer multiple of the per-minute limit (default 1 = no burst headroom)
    rpm_burst_mul integer       NOT NULL DEFAULT 1 CHECK (rpm_burst_mul >= 1),
    tpm_burst_mul integer       NOT NULL DEFAULT 1 CHECK (tpm_burst_mul >= 1),
    mode          text          NOT NULL DEFAULT 'enforce'
                                CHECK (mode IN ('enforce', 'observe', 'disabled')),
    enabled       boolean       NOT NULL DEFAULT true,
    created_at    timestamptz   NOT NULL DEFAULT now(),
    updated_at    timestamptz   NOT NULL DEFAULT now()
);

-- One row per (tenant, group-or-null) — prevents duplicate policies.
CREATE UNIQUE INDEX IF NOT EXISTS uq_url_policy_tenant_group
    ON user_rate_limit_policies (tenant_id, user_group)
    WHERE user_group IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_url_policy_tenant_default
    ON user_rate_limit_policies (tenant_id)
    WHERE user_group IS NULL;

CREATE INDEX IF NOT EXISTS idx_url_policy_tenant_enabled
    ON user_rate_limit_policies (tenant_id, enabled);

COMMENT ON TABLE user_rate_limit_policies IS
    'Per-group / tenant-default application rate limit policies (F-RL-APP-001).';
COMMENT ON COLUMN user_rate_limit_policies.user_group IS
    'NULL = tenant-wide default; non-null = per-group override (maps to users.user_group).';
COMMENT ON COLUMN user_rate_limit_policies.rpm_limit IS
    'Requests-per-minute cap per user bucket.  NULL = unlimited.';
COMMENT ON COLUMN user_rate_limit_policies.tpm_limit IS
    'Tokens-per-minute cap per user bucket.  NULL = unlimited.';
COMMENT ON COLUMN user_rate_limit_policies.rpm_burst_mul IS
    'Burst capacity = rpm_limit × rpm_burst_mul.  Default 1 = no burst headroom.';
COMMENT ON COLUMN user_rate_limit_policies.mode IS
    'enforce: deny on exceed.  observe: allow + metrics.  disabled: gate inactive.';

COMMIT;
```

**File:** `sql/migrations/0077_user_rate_limit_policies.down.sql`

```sql
BEGIN;
DROP TABLE IF EXISTS user_rate_limit_policies;
COMMIT;
```

---

## Endpoints

No new HTTP endpoints. The gate is purely internal middleware.

**Wiring change (existing files, modify only):**

### `internal/gatewayhttp/chat_completions_handler.go` — `ChatHandlerDeps`

Add one field to the existing struct (this is an **existing file**, modify only):

```go
// AppRateLimiter enforces per-user/per-group RPM+TPM caps (F-RL-APP-001).
// Nil = gate disabled (backward-compatible; production must inject).
AppRateLimiter userratelimit.Gater
```

The `NewChatCompletionsHandler` function checks this gate immediately after
`auth.Resolve` succeeds and before `validateChatCompletionsRequest`:

```go
if d.AppRateLimiter != nil {
    dec := d.AppRateLimiter.Check(ctx, userratelimit.GateInput{
        TenantID:  ident.TenantID,
        UserID:    ident.UserID,
        UserGroup: ident.UserGroup,
        // TokenEstimate is 0 at this point (request body not yet parsed).
        // RPM is enforced here; TPM is enforced after body parse (in prepareRoute).
    })
    if dec.Denied {
        writeJSONError(w, http.StatusTooManyRequests, "rate_limited",
            "request rate limit exceeded")
        return
    }
}
```

TPM check (token estimate available post-body-parse) is done inside
`prepareRoute` with the estimated token count from the request body.

### `cmd/gateway/middleware.go` — insert after auth middleware

The chi middleware chain (in `cmd/gateway/routes.go` / `lifecycle.go`) gets
`userratelimit.Middleware(gate)` inserted after IP rate-limit but the gate is
also called inside `ChatHandlerDeps` (post-auth) so the keyed-per-user check
can use the resolved identity.  The middleware layer handles unauthenticated
paths gracefully (no identity → gate skips).

### `cmd/gateway/config.go` — new env vars

| Env var | Default | Meaning |
|---|---|---|
| `HUAKAI_APP_RL_DISABLE` | `""` | Set `1` to disable the gate entirely |
| `HUAKAI_APP_RL_DEFAULT_RPM` | `600` | Tenant-default RPM when no DB policy found |
| `HUAKAI_APP_RL_DEFAULT_TPM` | `200000` | Tenant-default TPM when no DB policy found |
| `HUAKAI_APP_RL_POLICY_REFRESH_S` | `60` | Policy cache TTL in seconds |

---

## Invariants honored

| Invariant | How honored |
|---|---|
| **CMB-1: credentials never logged** | `GateInput` carries only `TenantID`, `UserID`, `UserGroup`, `TokenEstimate` — no bearer token, no credential material. Log output on deny includes only `tenant_id`, `user_id` (operator-visible numeric IDs), `user_group`, `metric`, `limit`. |
| **CMB raw upstream payloads never logged** | Gate runs before any upstream dispatch; no upstream payload exists at check time. |
| **Router reads no credentials / writes nothing** | Gate is called in `ChatHandlerDeps` (handler layer), not in pool router. It has no write path. |
| **Fail-closed on ambiguity** | Policy load error → gate returns `Denied=true` in `enforce` mode (fail-closed). In `observe` mode, policy load error → allow + emit metric. Admin-tunable via `mode` column. |
| **Money paths Tx1/Tx2 reserve+settle** | Gate fires **before** `ClaimGate.Reserve` (Tx1). A denied request never enters the billing path; no reservation is created and no settlement is needed. |
| **Schema changes get new numbered migration** | Migration 0077 (current max is 0076). |
| **Frozen packages not modified with new files** | `internal/gatewayhttp`, `internal/gateway`, `internal/proto` receive only field additions to existing structs. All new code lives in `internal/userratelimit`. |
| **No god-files; files < 500 lines; functions < 80 lines** | Largest file (`gate.go`) is ~200 lines. Every function delegates sub-concerns to `bucket_registry`, `policy_loader`, `success_window`. |
| **shopspring/decimal for money** | Not applicable — RPM/TPM are integer counters. Token-bucket arithmetic uses `float64` (same as existing `gateway.TokenBucket`). |
| **golang-migrate numbered migrations** | File named `0077_user_rate_limit_policies.{up,down}.sql`. |

---

## Discriminating tests

All tests in `internal/userratelimit/` are unit tests (no DB, no network).
Each test is named so that the exact defect it defends can be identified.

### `gate_test.go`

**`TestGate_RPM_EnforceDeniesOnExceed`**  
Defect defended: removing the RPM bucket deduction or changing `>` to `>=`
would allow the (limit+1)-th request through.  
Setup: policy RPM=5 burst=5, fire 6 requests in one second, clock held fixed.  
Assert: requests 1-5 → `Denied=false`; request 6 → `Denied=true`,
`Metric="rpm"`.

**`TestGate_RPM_ObserveAllowsButSetsObserveFlag`**  
Defect defended: swapping `observe` for `enforce` logic would block requests
that must be allowed.  
Setup: policy mode=`observe` RPM=2, fire 5 requests.  
Assert: all 5 → `Denied=false`, `ObservedExceed=true` on requests 3-5.

**`TestGate_TPM_EnforceDeniesWhenTokensExceed`**  
Defect defended: TPM path bypassed when `TokenEstimate==0` or bucket not
checked.  
Setup: policy TPM=1000, fire request with `TokenEstimate=600` (ok), then
`TokenEstimate=600` again (should deny: 600+600 > 1000).  
Assert: first → allow; second → `Denied=true`, `Metric="tpm"`.

**`TestGate_GroupOverrideTakesPriorityOverTenantDefault`**  
Defect defended: group-policy lookup falls through to tenant default when a
group override exists.  
Setup: tenant default RPM=10, group `"premium"` RPM=100. Identity has
`UserGroup="premium"`.  
Assert: 50 rapid requests all allowed (use premium bucket, not default bucket).

**`TestGate_NoGroupPolicyFallsBackToTenantDefault`**  
Defect defended: missing group entry causes nil-pointer or wrong bucket.  
Setup: no group policy for `"unknown_group"`, tenant default RPM=5. Identity
`UserGroup="unknown_group"`.  
Assert: 6th request denied (tenant default enforced).

**`TestGate_PolicyLoadErrorFailsClosed`**  
Defect defended: policy store error silently allows all traffic.  
Setup: `PolicyStore.LoadPolicies` returns `errors.New("db down")`.  
Assert: `Gate.Check` returns `Denied=true` (fail-closed in enforce mode).

**`TestGate_DisabledPolicyAllowsAll`**  
Defect defended: `mode=disabled` rows are incorrectly enforced.  
Setup: policy mode=`disabled` RPM=1. Fire 100 requests.  
Assert: all 100 → `Denied=false`.

**`TestGate_NilGaterIsNoop`**  
Defect defended: nil gate panics at request time.  
Assert: calling `(*Gate)(nil).Check(...)` returns `{Denied: false}` with no
panic (nil-safe guard).

### `bucket_registry_test.go`

**`TestBucketRegistry_SeparateBucketsPerUser`**  
Defect defended: two users sharing one bucket.  
Setup: RPM=2, user A fires 2 requests (exhausts), user B fires 1 request.  
Assert: user B's request is allowed (not affected by A's exhaustion).

**`TestBucketRegistry_EvictionAtCap`**  
Defect defended: unbounded memory growth under many distinct user IDs.  
Setup: `maxEntries=3`, register 4 distinct users.  
Assert: registry is reset (len==1 after 4th entry) — memory ceiling holds.

**`TestBucketRegistry_RefillOverTime`**  
Defect defended: tokens never refill after exhaustion.  
Setup: RPM=60 (1/s), exhaust bucket, advance clock by 2s.  
Assert: 2 new requests allowed.

### `success_window_test.go`

**`TestSuccessWindow_CountIncrementsOnSuccess`**  
Defect defended: success counter not incremented.  
Setup: record 3 successes. Assert `Count(userID, now)` == 3.

**`TestSuccessWindow_WindowResetOnNewMinute`**  
Defect defended: sliding window leaks counts across minute boundaries.  
Setup: record 5 at T=0s, advance clock to T=61s, record 1.  
Assert: `Count` == 1 (old window dropped).

### `policy_loader_test.go`

**`TestPolicyLoader_RefreshesAfterTTL`**  
Defect defended: stale policy cached forever after DB update.  
Setup: initial load returns RPM=10. Advance clock past TTL. Mock store now
returns RPM=50.  
Assert: after clock advance, `LoadForTenant` returns RPM=50.

**`TestPolicyLoader_RetainsStaleOnRefreshError`**  
Defect defended: refresh error wipes cached policy, causing fail-open.  
Setup: initial load ok (RPM=10), next refresh returns error.  
Assert: `LoadForTenant` still returns RPM=10 (stale-on-error safety net).

---

## Parity-or-better vs reference

The reference behavior comes from standard SaaS per-user/per-group rate
limiting as observed in OpenAI's platform tier system and comparable commercial
API gateways. The key behavioral requirements and where HUAKAI meets or exceeds
them:

| Behavior | Reference behavior | HUAKAI design | Status |
|---|---|---|---|
| Per-user RPM cap | Per-user token bucket, 1-minute window, returns 429 on exceed | `bucket_registry.go`: per-(tenant,userID) `gateway.TokenBucket` with RPM rate | Parity |
| Per-group RPM override | Group-level policy overrides org default; higher-tier users get higher caps | `policy_loader.go` scope resolution: group row beats tenant-default row | Parity |
| TPM (tokens/min) cap | Per-user token bucket for token count, checked with estimated token count from request body | Separate TPM bucket in `gate.go`; `GateInput.TokenEstimate` carries body-parsed estimate | Parity |
| Observe mode | Allow requests but record metric; used for gradual rollout / capacity planning | `mode='observe'` policy column; `GateDecision.ObservedExceed` flag for metric emission | Better — explicit flag avoids silent allow |
| Multi-instance safety | Each replica enforces independently; aggregate throughput = N × per-instance limit | Per-process buckets, no distributed lock, documented trade-off (see §Risks) | Parity — matches industry standard for in-process token buckets |
| Success-count sliding window | Track successful requests in rolling window for SLA / abuse detection | `success_window.go`: 60-second tumbling window per user, incremented post-settle | Better — tumbling window is cheaper and sufficient for RPM-scale detection |
| Fail-closed on config error | Policy misconfiguration → deny, not allow | `PolicyLoadError` → `Denied=true` in enforce mode | Better — explicit fail-closed vs implicit allow in some reference impls |
| Memory-bounded registry | Registry evicted/reset when entry count exceeds cap | `maxEntries` constant in `bucket_registry.go`; reset-on-cap (same as `rate_limit.go` `ipBucketRegistry`) | Parity with existing HUAKAI IP limiter pattern |
| Retry-After header | Return `Retry-After: <seconds>` on 429 | `middleware.go` computes `ceil(1/rate_per_sec)` mirroring `rate_limit.go:retryAfterForRatePerSec` | Parity |
| Group policy stored in DB | Group limits configurable without redeploy | `user_rate_limit_policies` table, refreshed every 60 s | Better than env-only config |

---

## Effort

**M** (medium)

Justification: The token-bucket primitive (`gateway.TokenBucket`) already
exists and is reused. The policy table is small. The injection point is
well-defined (post-auth, pre-reserve in `ChatHandlerDeps`). The main complexity
is the policy-loader cache, the two-metric (RPM+TPM) gate logic, and writing
discriminating tests. No distributed infrastructure (Redis, etc.) is required.

Estimated implementation: 3–4 working days for a competent implementer familiar
with the codebase.

---

## Risks

### R1 — Multi-instance over-admission
**Risk:** With N gateway replicas each enforcing RPM=X, aggregate throughput
is N×X per user/group, not X.  
**Mitigation:** This is the accepted trade-off for in-process token buckets (no
distributed lock, no Redis dependency, no cross-instance latency). Operators
MUST set RPM limits as `desired_aggregate_rpm / num_replicas`. Document this
prominently in the policy table comments and ops runbook. A future slice can
add a Redis-backed Lua script counter if tighter aggregate enforcement is
needed.

### R2 — TPM estimate inaccuracy
**Risk:** `GateInput.TokenEstimate` is derived from the request body
(input tokens only); actual output tokens are not known until after streaming
completes. TPM enforcement is therefore approximate.  
**Mitigation:** Input-token pre-check is sufficient for abuse prevention.
`success_window.go` tracks actual token counts post-settle (from
`UsageRecordDraft`) for observe-mode accuracy metrics. A future slice can add
a post-settle TPM sliding window for tighter accounting.

### R3 — Policy cache stale window
**Risk:** Between policy updates in DB and the next TTL refresh (60 s), old
limits are enforced. A limit reduction takes up to 60 s to take effect.  
**Mitigation:** 60-second stale window is acceptable for rate policy changes
(not a money-path invariant). Operators can force refresh by rolling the
gateway process. TTL is env-tunable via `HUAKAI_APP_RL_POLICY_REFRESH_S`.

### R4 — Eviction hiccup under IP-spoof-style user-ID flood
**Risk:** An attacker generating many distinct (fake) user IDs could trigger
the registry reset, giving legitimate users fresh full buckets.  
**Mitigation:** Auth layer (`auth.APIKeyResolver`) validates bearer tokens
against the DB before the gate is reached. User IDs are not attacker-controlled
at this layer. If a compromised key is used to generate many requests with
different claimed user IDs, the IP front-door tier blocks the flood first.

### R5 — gate.go `ChatHandlerDeps.AppRateLimiter` nil-skip
**Risk:** If `AppRateLimiter` is not wired in `cmd/gateway/wiring.go`,
the gate silently no-ops. No test currently catches a missing wire.  
**Mitigation:** `cmd/gateway/wiring.go` must inject the gate, and the
smoke test (`smoke_test.go`) should assert RPM enforcement under a
`HUAKAI_APP_RL_DEFAULT_RPM=1` env to catch missing wire at CI time.
`chatHandlerConfigured()` should be updated to include `AppRateLimiter != nil`
once the feature is fully rolled out (guarded behind a release flag initially).
