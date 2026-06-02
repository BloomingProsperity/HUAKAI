# Gap Design: Tiered/Expression Billing DSL + Funding-Source Switch

**Status:** Draft — 2026-06-03
**Author:** Senior HUAKAI Backend Architect (AI)
**Gap ID:** tiered-billing

---

## Summary

HUAKAI currently prices every token bucket with a flat per-token micro-USD rate
read from `billing_pricing_versions.pricing_data` (see
`internal/gatewayhttp/chat_completions_pricing.go`).  Two behaviors present in
comparable commercial systems are absent:

1. **Tiered/expression pricing DSL** — a per-request pricing expression whose
   rate varies with tier breakpoints driven by context length (total input
   tokens, inclusive of cache).  Example: first 32 k tokens at rate A, tokens
   32 k–128 k at rate B, tokens above 128 k at rate C.  The DSL is evaluated
   at the moment `completionCost()` is called; the result is still a
   `decimal.Decimal` USD cost compatible with the existing Tx1/Tx2
   reserve-settle pipeline.

2. **Per-session funding-source switch** — a billing session header (or
   per-API-key setting) that routes the mid-relay charge to either:
   - the tenant subscription's quota cap (`subscription_cap`), or
   - the user's wallet balance (`wallet`).

   "Billing session" here means the window from Tx1 `Reserve` through Tx2
   `Settle` for one logical request.  The switch is resolved once at Tx1 time,
   stamped on the claim row, and honoured by Tx2 Settle without re-reading
   the request.

Both gaps are additive; they do not alter existing Tx1/Tx2 semantics, the
immutability invariants of `billing_ledger_claims` / `usage_records` /
`billing_events`, or the `balancehold` reserve-capture pipeline.

---

## Package layout

All new code lives in **new packages** only.  Frozen packages
(`internal/gatewayhttp`, `internal/gateway`, `internal/proto`) receive no new
files; existing files in those packages may be extended with one call-site each
as noted.

```
internal/billingdsl/          ← pricing expression evaluator
    doc.go                    (package doc + CMB invariant note)           ~20 lines
    types.go                  (TierSpec, ExpressionSpec, EvalInput, EvalResult)  ~90 lines
    parser.go                 (ParsePricingExpression: JSON → ExpressionSpec)    ~130 lines
    evaluator.go              (Evaluate: ExpressionSpec × EvalInput → decimal)   ~160 lines
    evaluator_test.go         (discriminating unit tests)                        ~220 lines

internal/fundingsource/       ← funding-source resolution + claim annotation
    doc.go                    (package doc)                                ~15 lines
    types.go                  (FundingSource const, ResolveResult)          ~50 lines
    resolver.go               (Resolver interface + PGXResolver impl)       ~170 lines
    resolver_test.go          (unit tests with stub store)                  ~160 lines

internal/billinghttp/         ← HTTP handlers for admin CRUD + user session API
    doc.go                    (package doc)                                ~15 lines
    admin_tier_handler.go     (GET/POST/PUT /admin/billing/tier-rules)     ~190 lines
    session_funding_handler.go (POST /billing/session/funding-source)      ~160 lines
    register.go               (chi sub-router mount; called from cmd wire) ~50 lines
```

**Total new files: 10.  Every file is well under 500 lines.**

Each existing frozen-package file that gains a call-site:

| File | Change |
|---|---|
| `internal/gatewayhttp/chat_completions_pricing.go` | In `completionCost()`: if rate table contains a `tiers` key, delegate to `billingdsl.Evaluate` instead of flat `completionRateVector.price()`. Single `if` branch, ~6 lines. |
| `internal/billing/claim_gate.go` | In `Reserve()`: call `fundingsource.Resolver.Resolve()` and write the result into the new `funding_source` column on `billing_ledger_claims`. ~8 lines. |
| `internal/billing/billing.go` | Add `FundingSource string` field to `ReserveRequest` and `ReserveResult`. ~4 lines. |

No new files in frozen packages; only minimal additive edits to existing ones.

---

## Schema / migrations

### Migration 0077 — tiered pricing DSL columns

**File:** `sql/migrations/0077_tiered_billing_dsl.up.sql`

```sql
BEGIN;

-- Extend billing_pricing_versions to carry an optional structured tier ruleset.
-- The pricing_data JSONB column already exists and is used for flat rates;
-- tier_rules is a parallel JSONB column so old versions remain unaffected.
-- NULL = no tiers defined; use flat rate as before.
ALTER TABLE billing_pricing_versions
    ADD COLUMN IF NOT EXISTS tier_rules JSONB;

-- Admin-visible label for the tier ruleset version (human audit trail).
ALTER TABLE billing_pricing_versions
    ADD COLUMN IF NOT EXISTS tier_rules_version TEXT;

-- Partial index: quick lookup of versions that have tier rules defined.
CREATE INDEX IF NOT EXISTS idx_bpv_has_tier_rules
    ON billing_pricing_versions (tenant_id, version)
    WHERE tier_rules IS NOT NULL;

COMMIT;
```

**File:** `sql/migrations/0077_tiered_billing_dsl.down.sql`

```sql
BEGIN;
DROP INDEX IF EXISTS idx_bpv_has_tier_rules;
ALTER TABLE billing_pricing_versions
    DROP COLUMN IF EXISTS tier_rules_version,
    DROP COLUMN IF EXISTS tier_rules;
COMMIT;
```

### Migration 0078 — funding-source stamp on claims

**File:** `sql/migrations/0078_funding_source_claim.up.sql`

```sql
BEGIN;

-- Per-request funding source resolved at Tx1 time and immutable thereafter.
-- 'wallet'           = charge deducted from user_balances (existing balancehold path).
-- 'subscription_cap' = charge counted against the active subscription quota cap
--                      (quota engine cost_usd policy); wallet balance is NOT touched.
-- NULL               = legacy / not resolved (treated as 'wallet' by Tx2 for
--                      full backward compatibility with pre-0078 claims).
ALTER TABLE billing_ledger_claims
    ADD COLUMN IF NOT EXISTS funding_source TEXT
        CHECK (funding_source IS NULL OR funding_source IN ('wallet', 'subscription_cap'));

-- Index for analytics: distribution of funding source per tenant.
CREATE INDEX IF NOT EXISTS idx_claims_funding_source
    ON billing_ledger_claims (tenant_id, funding_source)
    WHERE funding_source IS NOT NULL;

-- Per-API-key default funding source preference (overrides tenant default).
-- NULL = inherit tenant default (backward compatible).
ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS default_funding_source TEXT
        CHECK (default_funding_source IS NULL
               OR default_funding_source IN ('wallet', 'subscription_cap'));

COMMIT;
```

**File:** `sql/migrations/0078_funding_source_claim.down.sql`

```sql
BEGIN;
DROP INDEX IF EXISTS idx_claims_funding_source;
ALTER TABLE billing_ledger_claims DROP COLUMN IF EXISTS funding_source;
ALTER TABLE api_keys DROP COLUMN IF EXISTS default_funding_source;
COMMIT;
```

---

## Endpoints

All new HTTP endpoints are wired through `internal/billinghttp/register.go`
and mounted by the cmd-layer wire function on the chi router.  They do **not**
require changes to frozen `gatewayhttp` routing tables.

### Admin endpoints (require admin auth scope)

| Method | Path | Auth scope | Responsibility |
|---|---|---|---|
| `GET` | `/admin/billing/tier-rules` | `admin` | List all `billing_pricing_versions` rows that have `tier_rules != NULL` for the tenant |
| `POST` | `/admin/billing/tier-rules` | `admin` | Upsert `tier_rules` + `tier_rules_version` on an existing pricing version row (validates DSL before writing) |
| `PUT` | `/admin/billing/tier-rules/{version}` | `admin` | Replace tier rules on named version (idempotent; validates DSL) |

Request body for POST/PUT:
```json
{
  "pricing_version": "2.0",
  "tier_rules_version": "v1-128k",
  "tier_rules": {
    "input": [
      {"up_to_tokens": 32768,  "rate_micro_usd": "0.30"},
      {"up_to_tokens": 131072, "rate_micro_usd": "0.50"},
      {"up_to_tokens": null,   "rate_micro_usd": "0.80"}
    ],
    "output": [
      {"up_to_tokens": null, "rate_micro_usd": "1.20"}
    ]
  }
}
```

### User-facing billing session endpoint

| Method | Path | Auth scope | Responsibility |
|---|---|---|---|
| `POST` | `/billing/session/funding-source` | `user` (valid API key) | Persist per-API-key `default_funding_source` preference for subsequent requests |

Request body:
```json
{ "funding_source": "subscription_cap" }
```

This is a "one-session switch": the caller sets their preference once; all
subsequent Tx1 reservations for that API key will use the resolved funding
source until changed or reset.  It does **not** affect in-flight claims.

---

## Invariants honored

**CMB invariants**

- Credentials and raw upstream payloads are never logged anywhere in the new
  packages.  `billingdsl` operates only on token counts and `decimal.Decimal`
  values; `fundingsource` resolves only from DB rows (tenant_id, api_key_id,
  user_id) and writes a single enum column.
- Router reads no credentials and writes nothing: the new packages are not
  imported by `internal/router` or `internal/gateway`.
- Fail-closed on ambiguity: if `billingdsl.Evaluate` encounters an
  unparseable tier rule or a missing rate, it returns
  `errCompletionPricingUnavailable` (same sentinel the existing flat-rate path
  uses).  If `fundingsource.Resolver.Resolve` returns an error, `Reserve`
  falls through to `wallet` (the existing balancehold path) and logs a warning
  — no request is silently passed without a funding source.

**Money-path invariants**

- Tx1/Tx2 audit chain is preserved: the `funding_source` column is written
  inside the Tx1 serializable transaction (same transaction as the claim
  INSERT), so it is visible to Tx2 `Settle` with the same row-lock snapshot.
- `wallet` funding source: no change to existing `balancehold.Reserve` +
  `balancehold.Capture` pipeline; it runs exactly as before.
- `subscription_cap` funding source: `balancehold.Reserve` is **skipped**
  (no wallet hold); the quota engine's existing `cost_usd` policy for the
  user's subscription already enforces the cap via `quota.Service.Reserve`.
  Tx2 `Settle` skips `balancehold.Capture` for this claim (checking the
  `funding_source` column) and instead calls `quota.Service.Settle`.
- `shopspring/decimal` is used for all tier arithmetic.  No float64 at any
  money-path boundary.
- The tiered cost expression produces a single `decimal.Decimal` that is
  passed to `ReserveRequest.PredictedCost` and `SettleRequest.ActualCost`
  unchanged; the downstream Tx1/Tx2 code paths see no structural difference.
- Schema changes use `ADD COLUMN IF NOT EXISTS` with `NULL` defaults, so all
  pre-0077/0078 claims (NULL `funding_source`) degrade gracefully to the wallet
  path — backward compatible with zero data migration.

**Modularity (Owner hard rule)**

- No file exceeds 500 lines; no function exceeds 80 lines.
- Each new package has a single responsibility:
  - `billingdsl` — pure DSL parse and evaluate (no DB, no HTTP).
  - `fundingsource` — resolve and store funding source (no pricing math).
  - `billinghttp` — HTTP wire; imports both, writes nothing to DB directly.

---

## Discriminating tests

Tests must fail if the specific defect they guard is introduced.

### `internal/billingdsl/evaluator_test.go`

| Test name | Defect it catches |
|---|---|
| `TestEvaluate_FlatFallback` | Tier rules absent → must use flat rate, not zero |
| `TestEvaluate_FirstTierOnly` | Tokens below first breakpoint use only tier-0 rate, not tier-1 |
| `TestEvaluate_CrossesTierBoundary` | Tokens that span two tiers must split at boundary; wrong split → wrong cost |
| `TestEvaluate_LastTierUnbounded` | Final tier `up_to_tokens: null` must absorb all remaining tokens |
| `TestEvaluate_NegativeRateFails` | Negative rate in tier → `errCompletionPricingUnavailable` (fail-closed) |
| `TestEvaluate_MissingOutputTier` | DSL has input tiers but no output entry → error for non-zero output |
| `TestEvaluate_DecimalPrecision` | 1,000,000 tokens × "0.30" micro-USD = exactly "0.30" USD (no float64 drift) |

### `internal/billingdsl/parser_test.go`

| Test name | Defect it catches |
|---|---|
| `TestParse_RejectsOverlappingBreakpoints` | Two tiers with the same `up_to_tokens` value → parse error |
| `TestParse_RejectsUnsortedBreakpoints` | Out-of-order tier breakpoints → parse error (prevents silent mis-pricing) |
| `TestParse_RejectsMultipleUnbounded` | Two tiers with `null` breakpoint → parse error |

### `internal/fundingsource/resolver_test.go`

| Test name | Defect it catches |
|---|---|
| `TestResolve_APIKeyPreferenceWins` | API-key `default_funding_source=subscription_cap` must override tenant default `wallet` |
| `TestResolve_TenantDefaultWallet` | No API-key preference + no active subscription → `wallet` (not panic/error) |
| `TestResolve_NoActiveSubscription_CapRequested` | API key requests `subscription_cap` but user has no active subscription → must degrade to `wallet` (fail-closed) |
| `TestResolve_DBErrorFallsToWallet` | DB error during resolution → `wallet` returned, no panic, error logged |
| `TestResolve_ImmutableAfterTx1` | `funding_source` written at Tx1; calling `Resolve` again on same claim_id returns the stored value, not a re-resolved one |

### `internal/billinghttp` integration-style tests

| Test name | Defect it catches |
|---|---|
| `TestAdminTierRulesUpsert_ValidatesDSL` | Posting a tier rule with unsorted breakpoints returns 422, does not write to DB |
| `TestAdminTierRulesUpsert_IdempotentSameVersion` | Identical PUT twice returns 200 both times (no constraint violation) |
| `TestSessionFundingSource_RejectsUnknownValue` | POST `{"funding_source":"unknown"}` returns 400 |
| `TestSessionFundingSource_WritesAPIKeyColumn` | POST `{"funding_source":"subscription_cap"}` and verify `api_keys.default_funding_source` is set |

### Tx1/Tx2 claim-level integration test (lives in `internal/billing/`)

| Test name | Defect it catches |
|---|---|
| `TestReserve_WalletFundingSourceSkipsQuota` | `funding_source=wallet` → `balancehold.Reserve` called, quota reserve NOT called |
| `TestReserve_SubscriptionCapFundingSourceSkipsWallet` | `funding_source=subscription_cap` → quota reserve called, `balancehold.Reserve` NOT called; claim row has `funding_source='subscription_cap'` |
| `TestSettle_SubscriptionCapSkipsBalanceCapture` | Tx2 with `funding_source=subscription_cap` claim → `balancehold.Capture` skipped; quota settle called |
| `TestSettle_WalletPathUnchanged` | Tx2 with `funding_source=wallet` (or NULL) claim → existing balancehold.Capture pipeline runs exactly as before |

---

## Parity-or-better vs reference

The reference system (fusion-upgrade decomposition) implements per-model tiered
pricing and a session-level billing channel switch.  The HUAKAI design matches
or improves on each behavior:

| Reference behavior | Reference location (behavioral) | HUAKAI implementation | Parity/Better |
|---|---|---|---|
| Tier breakpoints on input token count; linear interpolation within each tier; final tier has no upper bound | pricing evaluator, tier-split loop | `billingdsl.Evaluate`: splits token count at each `up_to_tokens` boundary using `decimal.NewFromInt`; final `nil` breakpoint is the catch-all bucket | Parity |
| Rate vector expression per bucket (input, output, cache) with per-tier overrides | pricing DSL JSON schema | `ExpressionSpec` carries independent tier slices per bucket; each tier carries a `rate_micro_usd` decimal string; missing bucket falls back to flat rate from `pricing_data` | Better — explicit fallback preserves backward compat |
| Session-level billing channel (subscription vs balance) set by caller header before first request | session/channel init | `POST /billing/session/funding-source` persists preference on `api_keys.default_funding_source`; `fundingsource.Resolver` reads it at every Tx1; immutable per claim (stamped on `billing_ledger_claims.funding_source`) | Better — preference is durable across process restarts and not a transient header |
| Subscription cap route: deduct from subscription quota counter, not wallet | billing channel routing | `funding_source=subscription_cap` skips `balancehold.Reserve`; routes to `quota.Service.Reserve` using existing `MetricCostUSD` + `WindowCalendarMonth` policy installed by `internal/subscription` | Parity |
| Fail-closed: unknown or missing pricing expression → request rejected, not zero-charged | pricing unavailable sentinel | `billingdsl.Evaluate` returns `errCompletionPricingUnavailable` for any parse/eval failure; `completionCost()` in `gatewayhttp` already propagates this to a request abort | Parity |
| Audit record carries the effective tier rule version | usage_records | `tier_rules_version` stored on `billing_pricing_versions`; surfaced in `SettleRequest.SnapshotVersion` extension (existing field) | Better — version pinned at snapshot time, not inferred |

---

## Effort

**L** (Large)

Rationale:
- Two distinct logical subsystems (DSL evaluator + funding-source switch) must
  be built from scratch with no shared state.
- Schema migrations touch two existing tables (`billing_pricing_versions`,
  `billing_ledger_claims`, `api_keys`) plus `billing_ledger_claims` Tx1 path.
- The DSL evaluator must handle arbitrary-length tier slices with exact decimal
  arithmetic and comprehensive negative-path coverage.
- The funding-source switch requires a coordinated change across `billing`
  (Tx1 + Tx2), `balancehold` (conditional skip), and `quota` (conditional
  delegate), all within the existing serializable transaction boundary.
- Integration tests for both money paths are non-trivial and require a live PG
  schema.
- Total estimated new LOC: ~1,500 (production) + ~1,200 (tests).

---

## Risks

| Risk | Likelihood | Severity | Mitigation |
|---|---|---|---|
| **Decimal precision regression in tier split** — floating point leaking into token count arithmetic produces incorrect USD amounts at large token counts | Medium | High | All token counts are `int64` multiplied by `decimal.Decimal` rates using `shopspring/decimal`; no `float64` at any tier boundary; `TestEvaluate_DecimalPrecision` pins exact expected value |
| **Backward-compat break for NULL `funding_source`** — Tx2 Settle sees NULL column and takes wrong branch | Medium | High | Migration adds NULL-allowed column with no default; Tx2 code treats NULL identical to `wallet`; `TestSettle_WalletPathUnchanged` guards this explicitly |
| **`subscription_cap` requested but no active subscription** — user has no quota policy installed; quota reserve returns allow (no policy = allow in observe mode) | High | Medium | `fundingsource.Resolver` queries active subscription existence before resolving `subscription_cap`; degrades to `wallet` if absent; `TestResolve_NoActiveSubscription_CapRequested` covers this |
| **Tier rule written with wrong JSON shape** — admin saves a syntactically valid JSON blob that fails DSL parse at request time | Medium | High | Admin POST/PUT endpoint calls `billingdsl.ParsePricingExpression` before writing; returns 422 on failure; DB is never updated with invalid shape |
| **Frozen package contract drift** — future edits to `chat_completions_pricing.go` change the call site added for tier delegation | Low | Medium | The new `billingdsl` package is only called from that one `if` branch; the function signature is stable (takes `json.RawMessage` + `completionUsageForCost`, returns `completionCostBreakdown, error`) |
| **Quota service concurrency under subscription_cap path** — serializable Tx1 holds row locks while quota reserve runs; quota uses its own serializable tx → nested transaction risk | Medium | High | `quota.Service.Reserve` must run in its own connection (not nested inside Tx1); design calls `quota.Service.Reserve` before `pool.BeginTx(Tx1)`, storing the reservation ID on the claim, mirroring the existing pattern in `claim_gate.go` where balancehold.Reserve is inside Tx1 but balancehold itself uses the passed `pgx.Tx` directly. Quota uses a separate connection and is committed independently. Claim INSERT records the `quota_reservation_id` for rollback on Tx1 failure. |
| **sqlc regeneration required** — adding columns to `billing_ledger_claims` and `api_keys` means sqlc queries in `internal/db/billing` must be regenerated | High | Low | Tracked as a required step in implementation; queries that SELECT * will automatically include the new column; targeted INSERT queries need explicit parameter addition |
