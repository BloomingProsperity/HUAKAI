# Gap Spec: Tiered/Expression Billing DSL + Funding-Source Switch

**Spec status:** Verified residual — ready for implementation planning
**Verified by:** PM residual-verification agent, 2026-06-03
**Source design:** `docs/process/gap-designs/tiered-billing.md`
**Source critique:** `docs/process/gap-critiques/tiered-billing.md`
**Migration max at verification time:** 0076 (`0076_user_role.up.sql`)

---

## False premises in the design (verified against real code)

### FP-1 — `tier_rules` is NOT a key inside `pricing_data`; it is a separate DB column

The design says `completionCost()` checks "if rate table contains a `tiers` key" in the
existing `pricing_data` JSONB.  Real code refutes this:

- `internal/billing/rate_table_source.go:53-58`: `GetRateTable` selects
  `id, version, pricing_data, effective_from, effective_to, created_at` — no `tier_rules`.
- `internal/billing/rate_table_source.go:18-24`: `RateTable` struct has
  `PricingData json.RawMessage` only — no `TierRules` field.
- Migration 0002 (`sql/migrations/0002_observability_billing.up.sql:278-291`):
  `billing_pricing_versions` columns are `id, tenant_id, version, pricing_data,
  effective_from, effective_to, created_at, created_by_actor`.  No `tier_rules` column.

**Fix required:** Migration 0077 adds `tier_rules JSONB` as a separate column (correct).
`RateTable` struct must gain `TierRules json.RawMessage` and `TierRulesVersion string`.
`PGXRateTableSource.GetRateTable` SQL must select those two columns.
`completionCost()` checks `table.TierRules != nil`, NOT a key inside `pricing_data`.

### FP-2 — `GetClaimForSettle` uses an explicit column list; `funding_source` will be zero-value

Confirmed at `internal/db/billing/billing_settle.sql.go:16-57`: explicit `SELECT` list
with 13 named columns.  `funding_source` is absent.  Critique H4 is correct: after migration
0078 adds the column, sqlc will NOT auto-include it; every `subscription_cap` settle will
silently read `""` and fall through to the wallet path, double-charging.

**Fix required:** Add `funding_source` to the `GetClaimForSettle` SQL query before sqlc
regeneration.  Also add `FundingSource *string` to `GetClaimForSettleRow`.

### FP-3 — `InsertClaim` and `ReReserveAbortedClaim` have NO `quota_reservation_id` column

Confirmed at `internal/db/billing/billing_claims.sql.go:124-234`:
- `InsertClaimParams` (lines 136-151): 14 fields, no `quota_reservation_id`.
- `ReReserveAbortedClaim` SQL (lines 190-202): resets 10 columns, no `funding_source`,
  no `quota_reservation_id`.

Critique H1 and H2 are both confirmed.

### FP-4 — `Abort()` calls `balancehold.Release()` unconditionally; no quota release

Confirmed at `internal/billing/settler.go:305`:
```
balancehold.Release(ctx, tx, claimID)
```
`balancehold.go:143-147`: if no hold row for claimID, returns nil (silent no-op).
A `subscription_cap` claim has no hold row, so `Release` is a no-op — correct for balance.
But `Abort()` never calls `quota.Service.Release()`, permanently consuming quota budget.
Critique H3 confirmed.

### FP-5 — `Refund()` credits `user_balances` unconditionally (no funding_source check)

Confirmed at `internal/billing/settler.go:680-688`:
```go
creditTag, err := tx.Exec(ctx,
    `UPDATE user_balances SET balance = balance + $1 ...`,
    refundUSD, req.TenantID, userID,
)
```
No `funding_source` read anywhere in `RefundInTx`.  For a `subscription_cap` claim, this
gives the user a free wallet top-up on refund.  Critique M5 confirmed.

### FP-6 — Design's "fail-closed" fallback on `Resolver` error is actually fail-open

The design says falling through to `wallet` on DB error is fail-closed.  Real code shows:
`balancehold.go:59-77`: if no `user_balances` row exists (opt-in mode unprovisioned),
`Reserve` returns nil (passes the request).  So `subscription_cap` + DB error + no wallet
row = request proceeds with zero cost enforcement.  Critique H5 confirmed.

### FP-7 — Admin GET endpoint tenant scoping: query param must be authenticated-identity-derived

The design says GET lists "for the tenant" without specifying how tenant_id is obtained.
Real pattern confirmed in `internal/gatewayhttp/admin_billing_settings_handler.go:189-202`:
`resolveAdminBillingTenantFromQuery` reads `?tenant_id=` from the query string but validates
it with `adminCanAccessTenant(ident, tenantID)` which checks
`ident.ScopeTenantID == tenantID` for tenant operators
(`internal/gatewayhttp/admin_cache_l2_handler.go:128-132`).
Design must specify this pattern explicitly.

### FP-8 — `billing_pricing_versions` is scoped by `is_public` for public routes

Confirmed at `internal/billing/rate_table_source.go:53-58`: queries use `is_public = true`.
Migration 0031 (`sql/migrations/0031_pricing_versions_public_scope_v2.up.sql`) established
this: `tenant_id=0` rows are public; `tenant_id!=0` rows are tenant-private.
Admin tier-rule upserts target tenant-private rows; the admin handler must filter by
`tenant_id` from the authenticated identity (not `is_public`).

---

## True residual (what is genuinely missing)

### TR-1 — Tiered pricing DSL evaluator (`internal/billingdsl`)

No code exists for per-tier token-count split with `decimal.Decimal` arithmetic.
The entire `internal/billingdsl` package is new.  Confirmed: `completionCost()` in
`internal/gatewayhttp/chat_completions_pricing.go:75-95` delegates only to
`rateVectorFromTable` → `rates.price(usage)` — no tier evaluation.

### TR-2 — `tier_rules` / `tier_rules_version` schema additions

No `tier_rules` or `tier_rules_version` columns on `billing_pricing_versions`.
Migration 0077 is fully required.  `RateTable` struct and `GetRateTable` SQL
also need updating (see FP-1).

### TR-3 — Funding-source resolution and stamping (`internal/fundingsource`)

No code exists for: reading `api_keys.default_funding_source`, resolving active
subscription existence, writing `funding_source` onto `billing_ledger_claims` at Tx1.
All `fundingsource` package code is new.

### TR-4 — `funding_source` schema column on `billing_ledger_claims` and `api_keys`

Migration 0078 is fully required.  Additionally `quota_reservation_id BIGINT` must be
added to `billing_ledger_claims` in 0078 (or 0079) per critique H1.

### TR-5 — `GetClaimForSettle`, `InsertClaim`, `ReReserveAbortedClaim` SQL updates

Three sqlc queries need explicit changes:
- `GetClaimForSettle`: add `funding_source` to SELECT list.
- `InsertClaim`: add `funding_source` parameter (nullable, default NULL).
- `ReReserveAbortedClaim`: add `funding_source = $N` to UPDATE SET clause.
- Both InsertClaim and ReReserveAbortedClaim: add `quota_reservation_id` parameter.
- `GetClaimForSettle`: add `quota_reservation_id` to SELECT list.

### TR-6 — `Abort()` quota release for `subscription_cap` claims

`settler.go:Abort()` must read `funding_source` from the locked claim row (add to the
existing `SELECT ... FOR UPDATE` at lines 277-283) and call `quota.Service.Release()` with
`reason="abort"` when `funding_source='subscription_cap'`.  Requires the
`quota.Service` to be injected into `DefaultSettler` (new field).

### TR-7 — Tx1 compensating `quota.Service.Release` on claim INSERT failure

After `quota.Service.Reserve()` commits (separate serializable tx), if the subsequent
Tx1 `balancehold.Reserve()` or `InsertClaim` fails, `Reserve()` in `claim_gate.go` must
call `quota.Service.Release(ctx, ...)` with `reason="pre_billing_failure"` before returning
the error.  Requires `quota.Service` injected into `DefaultClaimGate`.

### TR-8 — `Refund()` must skip wallet credit for `subscription_cap` claims

`settler.go:RefundInTx()` must add `funding_source` to its `FOR UPDATE` select at line
605-611, and skip the `UPDATE user_balances` credit when `funding_source='subscription_cap'`.
Instead, a `quota.Service.Release` (or reconciliation note) should be issued.

### TR-9 — Admin billing tier-rules HTTP handlers (`internal/billinghttp`)

No `internal/billinghttp` package exists.  The three admin endpoints
(`GET/POST/PUT /admin/billing/tier-rules`) and user session endpoint
(`POST /billing/session/funding-source`) are entirely new.
The GET handler must use `resolveAdminBillingTenantFromQuery` + `adminCanAccessTenant`
pattern from `gatewayhttp` (or an equivalent in `billinghttp`).

### TR-10 — `fundingsource.Resolver` must fail-closed (not fall through) on DB error

On `Resolver.Resolve()` DB error, propagate the error upward and abort Tx1.
Graceful degradation to `wallet` is not acceptable as the default (see FP-6).

---

## Reuse points (verified file:line)

| What | File:line | How reused |
|---|---|---|
| `adminCanAccessTenant` (tenant scope guard) | `internal/gatewayhttp/admin_cache_l2_handler.go:128-132` | Import or copy pattern into `billinghttp` handler |
| `resolveAdminBillingTenantFromQuery` (query-param tenant resolver) | `internal/gatewayhttp/admin_billing_settings_handler.go:189-202` | Pattern to replicate in `billinghttp.adminTierHandler` |
| `balancehold.Reserve`/`Release`/`Capture` (balance hold ops) | `internal/balancehold/balancehold.go:49,139,95` | `fundingsource` skips these for `subscription_cap`; no change needed |
| `quota.Service.Reserve` / `Release` / `Settle` | `internal/quota/service.go:66` / `service_settle.go:79,164` | Called from `claim_gate.go` (new) and `settler.go:Abort()` (new) |
| `errCompletionPricingUnavailable` sentinel | `internal/gatewayhttp/chat_completions_pricing.go:17` | `billingdsl.Evaluate` returns same-style wrapped error |
| `pricingUnavailable(reason)` helper | `internal/gatewayhttp/chat_completions_pricing.go:462-464` | Pattern for `billingdsl` error construction |
| `RateTable` struct + `PGXRateTableSource.GetRateTable` | `internal/billing/rate_table_source.go:18-24,77-101` | Extend with `TierRules`/`TierRulesVersion` fields |
| `decimal.NewFromInt` / `shopspring/decimal` usage | throughout `internal/billing` | Same library for all tier arithmetic |
| `dbbilling.InsertClaimParams` | `internal/db/billing/billing_claims.sql.go:136-151` | Add `FundingSource *string`, `QuotaReservationID *int64` |
| `billing.ReserveRequest` | `internal/billing/billing.go:59-73` | Add `FundingSource string` field |
| `billing.SettleRequest.SnapshotVersion` | `internal/billing/billing.go:113` | Existing field; `tier_rules_version` gets a separate new field to avoid conflation (critique P3) |

---

## Migration plan

### Migration 0077 — `billing_pricing_versions` tier-rules columns

**File:** `sql/migrations/0077_tiered_billing_dsl.up.sql`

Adds `tier_rules JSONB` (nullable) and `tier_rules_version TEXT` (nullable) to
`billing_pricing_versions`.  Partial index on `(tenant_id, version) WHERE tier_rules IS NOT NULL`.

No backward-compat risk: NULL = no tiers, existing flat-rate path unchanged.

**Down migration guard:** Before dropping `tier_rules`, preflight check that no row has
`tier_rules IS NOT NULL` — fail with an error message rather than silently destroying data.

### Migration 0078 — `billing_ledger_claims` + `api_keys` funding-source columns

**File:** `sql/migrations/0078_funding_source_claim.up.sql`

Adds to `billing_ledger_claims`:
- `funding_source TEXT CHECK (funding_source IS NULL OR funding_source IN ('wallet','subscription_cap'))` — NULL = legacy wallet path
- `quota_reservation_id BIGINT` (nullable) — ID of the committed quota reservation; NULL for wallet-path claims or pre-0078 claims

Adds to `api_keys`:
- `default_funding_source TEXT CHECK (default_funding_source IS NULL OR default_funding_source IN ('wallet','subscription_cap'))` — NULL = tenant default

Index: `(tenant_id, funding_source) WHERE funding_source IS NOT NULL` on `billing_ledger_claims`.

---

## First-slice spec

### Slice: `internal/billingdsl` — pure DSL evaluator (no DB, no HTTP, no money path)

This is the highest-value, zero-collision first slice.  It touches no shared files except
a call-site addition to `internal/gatewayhttp/chat_completions_pricing.go` (one `if` branch).
It does not require migration 0077 to be applied first (evaluator is pure Go; integration
is gated by the `tier_rules != nil` check).

#### Files to ADD

**`internal/billingdsl/doc.go`** (~20 lines)
- Package doc. States CMB invariants: no credentials, no DB, no HTTP.

**`internal/billingdsl/types.go`** (~90 lines)
```go
// TierSpec represents one tier breakpoint.
// UpToTokens == nil means "unbounded upper end" (catch-all last tier).
type TierSpec struct {
    UpToTokens   *int64
    RateMicroUSD decimal.Decimal
}

// BucketSpec holds the ordered tier slice for one token bucket (input/output/etc.).
type BucketSpec []TierSpec

// ExpressionSpec is the parsed DSL for all buckets.
type ExpressionSpec struct {
    Input         BucketSpec
    Output        BucketSpec
    CacheCreation BucketSpec
    CacheRead     BucketSpec
}

// EvalInput carries token counts for one pricing call.
type EvalInput struct {
    InputTokens         int64
    OutputTokens        int64
    CacheCreationTokens int64
    CacheReadTokens     int64
}

// EvalResult carries the per-bucket and total USD cost.
type EvalResult struct {
    Total             decimal.Decimal
    CacheCreationCost decimal.Decimal
    CacheReadCost     decimal.Decimal
}
```

**`internal/billingdsl/parser.go`** (~130 lines)
```go
// ParsePricingExpression parses a tier_rules JSONB blob into ExpressionSpec.
// Returns error on: overlapping breakpoints, unsorted breakpoints, multiple
// unbounded tiers, negative rates.
// Signature:
func ParsePricingExpression(raw json.RawMessage) (ExpressionSpec, error)
```
Validation rules (each is discriminating-test-backed):
- Breakpoints must be strictly increasing.
- Exactly one tier per bucket may have `up_to_tokens: null`.
- `null` tier must be last.
- `rate_micro_usd` must be non-negative.

**`internal/billingdsl/evaluator.go`** (~160 lines)
```go
// Evaluate computes the USD cost for one request using the tier expression.
// For each bucket: splits token count at each up_to_tokens boundary,
// multiplies each segment by its tier rate (decimal.Decimal), sums.
// If a required bucket has no tier spec AND no flat-rate fallback is provided,
// returns errPricingUnavailable (same shape as gatewayhttp sentinel).
//
// Signature:
func Evaluate(spec ExpressionSpec, input EvalInput, flatRateFallback completionRateVector) (EvalResult, error)
```
Note: `flatRateFallback` is passed from the caller (gatewayhttp) so `billingdsl` has
zero dependency on gatewayhttp types. The caller passes the already-parsed `completionRateVector`
so the fallback case (`pricing_data` has rate but no tier) stays working.

**`internal/billingdsl/evaluator_test.go`** (~220 lines)
See discriminating tests below.

**`internal/billingdsl/parser_test.go`** (~120 lines)
See discriminating tests below.

#### Files to EDIT

**`internal/billing/rate_table_source.go`**
- Add `TierRules json.RawMessage` and `TierRulesVersion string` to `RateTable` struct (lines 18-24).
- Extend `getPublicRateTableSQL` to select `COALESCE(tier_rules, 'null'::jsonb)` and `tier_rules_version` (add `tier_rules`, `tier_rules_version` to SELECT; scan into new fields).
- Note: This edit is blocked on migration 0077 being applied to the DB. Guard with `IF tier_rules IS NOT NULL` in application code, not SQL.

**`internal/gatewayhttp/chat_completions_pricing.go`**
- In `completionCost()` (line 75), after `table, err := ex.d.RateTables.GetRateTable(...)`:
```go
if len(table.TierRules) > 0 && string(table.TierRules) != "null" {
    spec, err := billingdsl.ParsePricingExpression(table.TierRules)
    if err != nil {
        return completionCostBreakdown{}, pricingUnavailable(fmt.Sprintf("tier_rules parse failed: %v", err))
    }
    result, err := billingdsl.Evaluate(spec, billingdsl.EvalInput{
        InputTokens:         int64(usage.InputTokens),
        OutputTokens:        int64(usage.OutputTokens),
        CacheCreationTokens: int64(usage.CacheCreationTokens),
        CacheReadTokens:     int64(usage.CacheReadTokens),
    }, rates)
    if err != nil {
        return completionCostBreakdown{}, err
    }
    return completionCostBreakdown{
        Total:             result.Total,
        CacheCreationCost: result.CacheCreationCost,
        CacheReadCost:     result.CacheReadCost,
    }, nil
}
```
This requires importing `billingdsl`. The `rates` variable (parsed from `pricing_data`) is
passed as the flat-rate fallback to `Evaluate`.

**Migration required for slice 1:** YES — migration 0077 must be applied before `RateTable`
gains the new columns. The evaluator itself compiles and passes tests without the migration,
but the `GetRateTable` SQL edit is blocked.

**Migration number:** 0077

---

## Discriminating tests

### `internal/billingdsl/evaluator_test.go`

| Test name | Mutation that makes it go red |
|---|---|
| `TestEvaluate_FlatFallback` | Remove the flat-rate fallback path; assert non-zero cost when tier_rules is nil |
| `TestEvaluate_FirstTierOnly` | Change tier-split to use tier-1 rate for all tokens instead of tier-0 |
| `TestEvaluate_CrossesTierBoundary` | Change split logic to not split at boundary (apply first-tier rate to all) |
| `TestEvaluate_LastTierUnbounded` | Return error when `up_to_tokens` is nil instead of treating it as catch-all |
| `TestEvaluate_NegativeRateFails` | Remove negative-rate check; expect `errPricingUnavailable` |
| `TestEvaluate_MissingOutputTier` | Return zero cost instead of error for non-zero output with no output tier |
| `TestEvaluate_DecimalPrecision` | Replace `decimal.NewFromInt` multiplication with `float64` cast |
| `TestEvaluate_MissingOutputTierAndFlatRateFallbackAbsent` | Return zero cost instead of `errPricingUnavailable` when both DSL output tier and flat fallback are absent |

### `internal/billingdsl/parser_test.go`

| Test name | Mutation that makes it go red |
|---|---|
| `TestParse_RejectsOverlappingBreakpoints` | Remove duplicate-breakpoint check |
| `TestParse_RejectsUnsortedBreakpoints` | Remove sort-order check |
| `TestParse_RejectsMultipleUnbounded` | Allow multiple nil-breakpoint tiers |

### Additional tests required (from critique)

| Test name | Package | Mutation |
|---|---|---|
| `TestAbort_SubscriptionCapReleasesQuota` | `internal/billing` | Remove `quota.Service.Release` call from `Abort()` |
| `TestReserve_SubscriptionCapFundingSourceSkipsWallet` | `internal/billing` | Remove funding-source check; always call `balancehold.Reserve` |
| `TestSettle_SubscriptionCapSkipsBalanceCapture` | `internal/billing` | Remove funding-source check; always call `balancehold.Capture` |
| `TestSettle_WalletPathUnchanged` | `internal/billing` | Change NULL `funding_source` to behave as `subscription_cap` |
| `TestReserve_WalletTx1FailureReleasesQuota` | `internal/billing` | Remove compensating `quota.Service.Release` after `balancehold.Reserve` error |
| `TestRefund_SubscriptionCapSkipsWalletCredit` | `internal/billing` | Remove `funding_source` check; always `UPDATE user_balances` |
| `TestResolve_DBErrorAbortsReserve` | `internal/fundingsource` | Change DB error from `return err` to `return wallet, nil` |
| `TestAdminTierRulesGet_TenantScopedByIdentity` | `internal/billinghttp` | Replace identity-derived tenant_id with a raw URL parameter |

---

## Risk classification

**`internal/billingdsl`:** `safe-read` — pure computation, no DB, no HTTP, no money path.

**`internal/fundingsource`:** `money` — reads from DB at Tx1 time and influences whether
`balancehold.Reserve` is called.

**`internal/billinghttp`:** `schema` — writes `tier_rules` and `default_funding_source`.

**`internal/billing/claim_gate.go` changes:** `money` — touches Tx1 reserve path.

**`internal/billing/settler.go` changes:** `money` — touches Tx2 settle and abort paths.

**Migrations 0077/0078:** `schema`.

---

## Parallelizability

**First slice (`internal/billingdsl`) is parallelizable = true.**

An agent can implement `internal/billingdsl` entirely in an isolated worktree.
The only shared-file touch is `internal/gatewayhttp/chat_completions_pricing.go`
(one `if` branch) and `internal/billing/rate_table_source.go` (two struct fields + SQL).
These edits are additive and collision-free with any concurrent work on `fundingsource`
or `billinghttp` which touch different files.

The money-path slices (fundingsource, claim_gate.go, settler.go changes) are NOT
parallelizable with each other — they share `billing.go`, `claim_gate.go`, `settler.go`,
and the sqlc-generated files.  They must be sequenced after migration 0078.

---

## Must-fix list (from critique, all confirmed by code verification)

All 10 must-fix items from the critique are confirmed real defects.  Priority order:

1. **(H1/M3)** Add `quota_reservation_id BIGINT` to `billing_ledger_claims` in migration 0078.
   Update `InsertClaim`, `ReReserveAbortedClaim`, `GetClaimForSettle` SQL + regenerate sqlc.
   Add compensating `quota.Service.Release(reason="pre_billing_failure")` in `claim_gate.go`
   when Tx1 fails after quota reserve has committed.

2. **(H2/M2)** Add `funding_source = $N` to `ReReserveAbortedClaim` SQL.

3. **(H3/M1)** In `settler.go:Abort()`, read `funding_source` from the locked claim row
   (extend existing `SELECT ... FOR UPDATE` at lines 277-283); call `quota.Service.Release`
   for `subscription_cap` claims.  Add `TestAbort_SubscriptionCapReleasesQuota`.

4. **(H4)** Add `funding_source` (and `quota_reservation_id`) to `GetClaimForSettle`
   SELECT list before sqlc regeneration.

5. **(H5)** `fundingsource.Resolver.Resolve` DB error must propagate upward and abort Tx1,
   not fall through to wallet.

6. **(M5)** `settler.go:RefundInTx()` must check `funding_source`; skip `user_balances`
   credit for `subscription_cap` claims.

7. **(P1)** Add per-request header override (`X-Billing-Funding-Source`) checked at
   `fundingsource.Resolver.Resolve`, OR document as known gap on roadmap.

8. **(P2 extension)** Add `TestEvaluate_MissingOutputTierAndFlatRateFallbackAbsent`.

9. **(A2)** Add rate limiting to `POST /billing/session/funding-source`.

10. **(S2)** Add down-migration preflight guard to `0077_tiered_billing_dsl.down.sql`.

---

## Additional false premise found by this verification (not in original critique)

**FP-9 — `GetRateTable` does not return `tier_rules`; `RateTable` struct has no `TierRules` field**

The design says `completionCost()` checks "if rate table contains a `tiers` key" as if
`tier_rules` were embedded in `pricing_data`.  This conflates two separate DB columns.
The correct integration is: `GetRateTable` SQL selects `tier_rules` separately; `RateTable`
struct gets `TierRules json.RawMessage`; `completionCost()` checks `table.TierRules != nil`.
This requires editing `rate_table_source.go` in addition to the files the design lists.

The design's frozen-package edit table omits `internal/billing/rate_table_source.go`.
This file is in `internal/billing`, NOT in a frozen package, so it is editable.
