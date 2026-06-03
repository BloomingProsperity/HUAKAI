# Gap Critique: Tiered/Expression Billing DSL + Funding-Source Switch

**Reviewer:** Adversarial principal reviewer (automated)
**Reviewed design:** `docs/process/gap-designs/tiered-billing.md`
**Review date:** 2026-06-03
**Current migration max:** 0076 (`0076_user_role.up.sql`)

---

## Verdict

**needs-work**

The migration numbering is correct (0077/0078 > 0076) and the package modularity
is clean. However there are five must-fix correctness defects before implementation
begins: a quota-transaction nesting flaw that will deadlock or double-charge under
`subscription_cap`, an abort/re-reserve path that silently skips the wallet release,
a missing rollback of a quota reservation on Tx1 failure, the `ReReserveAbortedClaim`
path that does not reset `funding_source`, and a parity gap where the design upgrades
the funding-source preference from a transient header to a durable column without
documenting the per-request header override path.

---

## Holes

### H1 — Tx1 quota-reservation rollback on claim INSERT failure (critical)

The design says (Risks table, last row):

> `quota.Service.Reserve` must run **before** `pool.BeginTx(Tx1)` … Claim INSERT
> records the `quota_reservation_id` for rollback on Tx1 failure.

But `billing_ledger_claims` has **no `quota_reservation_id` column** (confirmed in
`internal/db/billing/billing_claims.sql.go` lines 124–158 and the schema columns
listed in `InsertClaimParams`). The design acknowledges this column is required yet
provides no migration that adds it, and the sqlc-regeneration note says only that
`INSERT` queries need explicit parameter additions — it does not list adding this
column. Concretely:

- If the `quota.Service.Reserve` call succeeds but the subsequent Tx1 claim INSERT
  (or `balancehold.Reserve`) fails, the quota reservation window counter is
  permanently incremented with no matching claim, permanently consuming the user's
  monthly quota budget.
- Without the `quota_reservation_id` stored on the claim row, Tx2 `Settle` cannot
  pass the correct `ReservationID` to `quota.Service.Settle`, which will return
  `ErrReservationNotFound` and enqueue a reconciliation job for every single
  `subscription_cap` settlement.

**Required fix:** Add `quota_reservation_id BIGINT` (nullable, FK to quota
reservations) to `billing_ledger_claims` in migration 0078 (or a separate 0079).
Add it to `InsertClaimParams`, `InsertClaim`, `GetClaimForSettle`, and `ReReserveAbortedClaim`.
On Tx1 failure after `quota.Service.Reserve`, call `quota.Service.Release` with
`reason="pre_billing_failure"` before returning the error.

---

### H2 — `ReReserveAbortedClaim` does not reset `funding_source` (critical)

`internal/db/billing/billing_claims.sql.go` lines 190–203 show the `UPDATE`
executed by `ReReserveAbortedClaim`. It resets `status`, `aborted_reason`,
`settled_at`, `attempt_seq`, `lease_expires_at`, `predicted_cost`,
`pooling_group_id`, `provider_account_id`, and `acquisition_token` — but it will
**not** reset the new `funding_source` column.

If a claim was first attempted with `funding_source='subscription_cap'` (aborted
mid-flight), and the user has since cancelled their subscription, the re-reserve
path will call `fundingsource.Resolver.Resolve()`, get `wallet` (no active
subscription), and write `wallet` to `ReserveRequest.FundingSource` — but because
`ReReserveAbortedClaim` never touches the column, the old `subscription_cap` value
persists in the row. Tx2 Settle will read the stale column and skip
`balancehold.Capture`, causing a zero-cost deduction from a wallet that was never
held.

**Required fix:** Add `funding_source = $N` to the `ReReserveAbortedClaim` UPDATE
and pass the freshly resolved value.

---

### H3 — Abort path does not release the quota reservation under `subscription_cap` (critical)

`internal/billing/settler.go` `Abort()` (lines 259–397) calls
`balancehold.Release(ctx, tx, claimID)` unconditionally. Under the new design, a
`subscription_cap` claim never creates a balance hold — `balancehold.Release`
on a non-existent hold row is a no-op (lines 143–147 of `balancehold.go`). That is
correct for the balance side.

But `Abort()` never calls `quota.Service.Release()`. The quota window
counter incremented at `quota.Service.Reserve` is never decremented, permanently
consuming the user's quota cap for the aborted request.

The design's test `TestSettle_SubscriptionCapSkipsBalanceCapture` only covers Tx2
`Settle`; there is no test for `Abort` on a `subscription_cap` claim. There is also
no test `TestAbort_SubscriptionCapReleasesQuota`.

**Required fix:** In `Abort()`, read `funding_source` from the locked claim row and,
if `subscription_cap`, call `quota.Service.Release` before committing. Also add
`TestAbort_SubscriptionCapReleasesQuota` to the required test list.

---

### H4 — Tx2 `Settle` reads `funding_source` from the claim row but the design does not specify the SELECT column (moderate)

`DefaultSettler.Settle` obtains claim data via `qtx.GetClaimForSettle` (line 90 of
`settler.go`). The design says Tx2 "checks the `funding_source` column" but never
specifies that `funding_source` must be added to the `GetClaimForSettle` query and
its return type. `GetClaimForSettle` is a sqlc-generated query; adding a column
means regenerating sqlc **and** updating every call-site that pattern-matches the
returned struct fields.

The design's sqlc note only says "queries that SELECT * will automatically include
the new column." `GetClaimForSettle` uses an explicit column list (as shown by the
struct type at line 90+ of settler.go). This will silently compile but `funding_source`
will be zero-value (`""`) at runtime, causing every `subscription_cap` claim to be
treated as `wallet` and double-charging the wallet.

**Required fix:** Explicitly add `funding_source` to the `GetClaimForSettle` query
and regenerate sqlc before any implementation begins. Document this in the design.

---

### H5 — Fail-open disguised as fail-closed in `fundingsource.Resolver` (moderate)

The design states (CMB invariants):

> If `fundingsource.Resolver.Resolve` returns an error, `Reserve` falls through to
> `wallet` … no request is silently passed without a funding source.

Falling through to `wallet` on a DB error **is fail-open**: the request proceeds
and charges the wallet even though the operator-configured preference
(`subscription_cap`) could not be read. For a tenant that has no wallet balance
(opt-in mode with no `user_balances` row), `balancehold.Reserve` returns nil
(lines 73–78 of `balancehold.go`), meaning the request proceeds with zero cost
enforced — a free-pass on DB error. The log-warning-and-continue pattern should
instead return an error and abort Tx1, consistent with the existing fail-closed
sentinel `errCompletionPricingUnavailable`.

**Required fix:** On `Resolver.Resolve` DB error, return the error from `Reserve`
(aborting Tx1) rather than silently downgrading to `wallet`. If graceful degradation
is truly required, it must be an explicit operator opt-in flag, not the default.

---

## Money/Schema/Auth/CMB risks

### M1 — Double-charge risk: `subscription_cap` Abort path does not release quota

See H3. A quota window counter that is not released on Abort effectively charges the
user for a request that returned no tokens. For monthly-cap users this drains quota
silently.

### M2 — `ReReserveAbortedClaim` stale `funding_source` causes missed wallet hold

See H2. Re-reserve with stale `subscription_cap` means no `balancehold.Reserve` is
called at Tx1. At Tx2, `balancehold.Capture` is also skipped (matching the column
value). The user is never charged. This is a revenue leak on every aborted+retried
`subscription_cap` request where the subscription was cancelled between attempts.

### M3 — `quota_reservation_id` not stored → every `subscription_cap` Tx2 reconciliation-queues

See H1. Without the ID on the claim, `quota.Service.Settle(req)` cannot be called
with a valid `ReservationID`. Every `subscription_cap` settle will hit
`ErrReservationNotFound`, trigger `enqueueFinalizationReconciliation`, and
permanently bloat the reconciliation queue.

### M4 — Quota nested transaction risk is understated

The design risk row says "Quota uses a separate connection and is committed
independently." The real `quota.Service` (confirmed in `quota/service.go`) uses
`s.withStore` which calls `txStore.WithTx`, meaning it opens its own serializable
transaction internally. That is correct — it is already separate from Tx1. However
the design also says the claim INSERT "records the `quota_reservation_id` for
rollback on Tx1 failure." If quota Reserve commits and then Tx1 fails (e.g.,
`balancehold.Reserve` hits `ErrInsufficientBalance`), the already-committed quota
reservation must be explicitly released via a second `quota.Service.Release` call
outside the failed transaction. The design does not specify where this compensating
call is made, which call path owns it, or how it is tested. This is a coordination
gap independent of H1.

### M5 — `billing_events` immutability: refund path not updated for `subscription_cap`

`DefaultSettler.Refund` (lines 535–700 of `settler.go`) credits `user_balances`
on refund. For a `subscription_cap` claim there is no wallet deduction, so crediting
`user_balances` on refund is incorrect (it would give the user a free wallet top-up).
The design does not address the refund path for `subscription_cap` claims at all.

### S1 — Migration number collision check: PASS (0077/0078 > 0076)

Current highest confirmed migration is `0076_user_role.up.sql`. Proposed 0077 and
0078 are clear. No collision.

### S2 — Down-migration safety

Both down migrations use `DROP COLUMN IF EXISTS` and `DROP INDEX IF EXISTS`, which
are safe. However `0077_tiered_billing_dsl.down.sql` drops `tier_rules` and
`tier_rules_version` from `billing_pricing_versions`. If any live tenant has
`tier_rules IS NOT NULL` the column drop will succeed but silently destroy live
pricing data — there is no guard. Add a preflight check (or fail-fast) if any row
has `tier_rules IS NOT NULL` before allowing the down migration.

### A1 — Admin endpoint `POST /admin/billing/tier-rules` — no tenant scoping on GET

The GET lists all `billing_pricing_versions` rows with `tier_rules != NULL` "for
the tenant." The design does not specify that the query is filtered by `tenant_id`
extracted from the **authenticated admin session** rather than from a URL parameter.
If the handler reads `tenant_id` from a query parameter it enables cross-tenant data
access. The existing `admin_billing_settings_handler.go` pattern must be audited and
explicitly mirrored.

### A2 — `POST /billing/session/funding-source` — no rate limiting specified

This endpoint writes to `api_keys.default_funding_source` on every call. There is no
mention of rate limiting or idempotency. A caller can spam it to toggle funding
source mid-flight and cause Tx1/Tx2 disagreement (Tx1 resolves `wallet`, spammer
switches to `subscription_cap` between Tx1 and Tx2, but Tx2 reads from the claim
row so it is actually safe — however the spam still creates DB write contention on
the `api_keys` row). Rate limiting is required.

### CMB1 — `tier_rules` JSONB written by admin handler — must validate before persist

The design correctly specifies that the admin POST/PUT endpoint calls
`billingdsl.ParsePricingExpression` before writing. This is correct. Confirm that
the parser rejects any blob with a `rate_micro_usd` string that parses to a value
greater than some reasonable ceiling (e.g., $1 per token). There is no maximum-rate
guard in the DSL spec, which could allow an admin to create a pricing rule that
overcharges by orders of magnitude before being noticed.

---

## Parity gaps

### P1 — Per-request header override path not documented

The design "upgrades" the reference behavior from a transient per-request header to
a durable `api_keys.default_funding_source` column (claimed as "Better"). The
reference behavior is a **per-session/per-request** header that takes effect for
that one request. The design's durable preference affects **all subsequent requests**
until changed. These are different semantics:

- A user who temporarily needs `subscription_cap` for one large request cannot do so
  without changing their permanent preference and then changing it back.
- The design does not provide a per-request header override of the durable preference.

This is not strictly "Better" — it removes a capability. Either add a per-request
header override path (checked at `fundingsource.Resolver.Resolve` before the DB
column) or document the intentional semantic change as a known gap with a roadmap
item.

### P2 — Output tier fallback behavior unspecified for missing output tiers

The parity table says: "missing bucket falls back to flat rate from `pricing_data`."
The reference evaluator treats a missing output tier as a hard error when output
tokens are non-zero (fail-closed). The design's fallback-to-flat-rate behavior is
"Better" only if the flat rate is always present; if `pricing_data` also has no
output rate, the design must still return `errCompletionPricingUnavailable`. The
test `TestEvaluate_MissingOutputTier` covers the case where the DSL has input tiers
but no output entry, but does **not** cover the case where both the DSL output tier
and the flat-rate fallback are absent. Add
`TestEvaluate_MissingOutputTierAndFlatRateFallbackAbsent` to ensure fail-closed
behavior in that scenario.

### P3 — `tier_rules_version` not surfaced in `billing_events`

The design says `tier_rules_version` is "surfaced in `SettleRequest.SnapshotVersion`
extension." But `SnapshotVersion` in the existing `SettleRequest` (line 113 of
`billing.go`) is described as "the registry+router stamp produced by `router.Plan`."
Mixing tier-rule version into a field that already carries routing-snapshot data
conflates two independent audit dimensions. A dedicated `pricing_tier_version` field
(or a sub-key in the snapshot string) should be used to preserve clean audit replay
semantics.

---

## Maintainability (god-file check)

All proposed files are under 500 lines individually. No god-file violation.
However one concern:

- `internal/billinghttp/admin_tier_handler.go` (~190 lines) and
  `session_funding_handler.go` (~160 lines) both live in `internal/billinghttp`.
  The `register.go` mounts both. This is acceptable for now but as further billing
  endpoints are added this package will grow toward the 500-line limit per file. The
  design should note that future billing HTTP handlers belong in sub-packages of
  `billinghttp`, not in the same files.

---

## Must-fix before implementation (numbered list)

1. **Add `quota_reservation_id` column to `billing_ledger_claims`** in migration
   0078 (or new 0079). Update `InsertClaim`, `ReReserveAbortedClaim`,
   `GetClaimForSettle` SQL and regenerate sqlc. Implement compensating
   `quota.Service.Release` call on any Tx1 failure that occurs after
   `quota.Service.Reserve` has committed.

2. **Add `funding_source = $N` reset to `ReReserveAbortedClaim`** so the re-reserve
   path always stamps the freshly resolved funding source, not the stale value from
   the aborted attempt.

3. **Fix `Abort()` to release quota reservation under `subscription_cap`**: read
   `funding_source` from the locked claim row; if `subscription_cap`, call
   `quota.Service.Release(ctx, ...)` before committing the abort transaction. Add
   `TestAbort_SubscriptionCapReleasesQuota` as a discriminating test.

4. **Add `funding_source` to the `GetClaimForSettle` explicit SELECT list** and
   regenerate sqlc before any implementation. Document this as a mandatory sqlc
   step in the design (not a "will happen automatically" assumption).

5. **Fix fail-open in `fundingsource.Resolver`**: on DB error, propagate the error
   upward and abort Tx1 rather than silently downgrading to `wallet`. If graceful
   degradation is required, make it an explicit operator-configurable flag.

6. **Address the refund path for `subscription_cap` claims**: `DefaultSettler.Refund`
   must check `funding_source` on the claim and skip the `user_balances` credit (or
   deduct from quota overage instead) for `subscription_cap` claims.

7. **Add per-request header override for funding source**: either implement a
   `X-Billing-Funding-Source` request header that overrides `default_funding_source`
   for one request, or explicitly document the semantic downgrade as a known gap on
   the roadmap.

8. **Add `TestEvaluate_MissingOutputTierAndFlatRateFallbackAbsent`** to
   `evaluator_test.go` to cover fail-closed behavior when neither DSL tier nor flat
   rate fallback provides an output rate.

9. **Add rate limiting to `POST /billing/session/funding-source`** (e.g., 10 writes
   per API key per minute) to prevent write-contention abuse on the `api_keys` row.

10. **Add down-migration preflight guard** to `0077_tiered_billing_dsl.down.sql`:
    fail (or warn) if any `billing_pricing_versions` row has `tier_rules IS NOT NULL`
    before dropping the column, to prevent silent data destruction on rollback.
