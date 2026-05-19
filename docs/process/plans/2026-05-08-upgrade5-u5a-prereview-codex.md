# HUAKAI Upgrade #5 U5-A PRE-REVIEW

Lane: codex / Time: 2026-05-08

## Scope

This is a pre-review before any schema migration for two-stage quota with `tier_max_multiplier`. I only inspected HUAKAI-local plans, schema migrations, SQL queries, and backend code. I did not read non-MIT reference source and did not modify repository code.

Primary plan inputs:

- `docs/process/plans/2026-05-08-upgrade5-quota-claude.md:15` defines Stage 1 base quota; `:16` defines paid-tier extension as base x multiplier without squeezing free pool; `:23` proposes `tenants` or `tenant_tier`; `:81-95` sketches `tenants` columns plus `quota_period`.
- `docs/process/plans/2026-05-08-upgrade5-quota-codex.md:25-40` recommends a tenant-scoped quota/tier policy model plus `quota_tiers`, assignments, counters, events, and optional claim linkage; `:129-148` marks entitlement scope and auth propagation as Owner decision points; `:262-270` recommends integrating quota through Ledger Tx1/Tx2, not standalone HTTP middleware.

## Existing Schema Status

### Tenant / auth identity schema

- `tenants` exists with only `id`, `name`, `status`, timestamps, and `deleted_at`; there is no tenant tier, quota base, or multiplier column. Evidence: `backend/sql/migrations/0001_pool_routing.up.sql:15-22`.
- `users` exists with `tenant_id`, optional `email`, `display_name`, `status`, timestamps, and `deleted_at`; no tier/quota fields. Evidence: `backend/sql/migrations/0007_l0_inbound_auth.up.sql:18-28`.
- `api_keys` exists with `tenant_id`, `user_id`, key hash/prefix, status, expiry/revocation timestamps, and soft delete fields; no quota limit/counter/tier fields. Evidence: `backend/sql/migrations/0007_l0_inbound_auth.up.sql:51-70`.
- Composite tenant-scoped FK discipline is already established for money-path identity joins: `billing_ledger_claims` references `(tenant_id, api_key_id)` and `(tenant_id, user_id)`, and `usage_records` does the same. Evidence: `backend/sql/migrations/0009_ledger_fk_backfill.up.sql:43-74`.

Conclusion: product-tier fields do not already exist on `tenants`, `users`, or `api_keys`.

### Existing quota-like schema

The only existing quota columns are on `provider_accounts`, and they are upstream Provider Account capacity/account quota, not end-user/tier quota:

- `cap_quota_total`, `quota_used_total`
- `cap_quota_daily`, `quota_used_daily`, `quota_window_daily_start`
- `cap_quota_weekly`, `quota_used_weekly`, `quota_window_weekly_start`
- `quota_status`

Evidence: `backend/sql/migrations/0001_pool_routing.up.sql:139-149`.

Pool routing can read these fields in the account-listing query, but the pool-group hot path currently selects only account id, tenant id, concurrency, priority, last dispatch, and queue caps. Evidence: `backend/sql/queries/pool_accounts.sql:71-97` and `backend/internal/pool/db_account_source.go:37-63`.

Conclusion: no tenant/user/API-key quota table exists today. Existing `provider_accounts.*quota*` should not be reused for paid/free user entitlement quota.

### Billing / usage tables

Existing billing schema:

- `billing_ledger_claims`: tenant, idempotency key/fingerprint, API key, user, model, provider account placeholder, acquisition token, predicted/actual cost, lifecycle status, lease. Evidence: `backend/sql/migrations/0002_observability_billing.up.sql:19-54`.
- `billing_events`: tenant, claim, event type, actual cost, end class, usage source, fingerprint. Evidence: `backend/sql/migrations/0002_observability_billing.up.sql:93-105`.
- `usage_records`: tenant, claim, API key, user, provider account, acquisition token, token counts, actual/input/output/cache costs, end class, usage source, timing, model, stream. Evidence: `backend/sql/migrations/0002_observability_billing.up.sql:121-178`.
- `usage_record_dlq`, `usage_record_reconciliation_events`, `billing_ledger_adjustments`, and `billing_pricing_versions` exist for recovery, reconciliation, adjustment, and pricing context. Evidence: `backend/sql/migrations/0002_observability_billing.up.sql:198-291`.

Important gap: none of these tables currently stores quota stage, quota reservation id, quota tier id, policy version, reserved amount, base used, or extension used.

## Existing Auth Tier Concept

No entitlement tier is present in auth.

- `auth.Identity` carries only `TenantID`, `APIKeyID`, and `UserID`. Evidence: `backend/internal/auth/api_key_resolver.go:38-46`.
- The inbound resolver returns those three IDs after checking key/user/tenant status. Evidence: `backend/internal/auth/api_key_resolver.go:126-142`.
- The SQL backing auth returns key/user/tenant status, but no tier/quota policy columns. Evidence: `backend/sql/queries/auth_inbound.sql:18-27`.
- Other `tier` mentions found in gateway error classification or protocol passthrough are not product entitlement tier and should not be reused for quota.

Conclusion: U5-A should not assume an existing auth-tier concept. If tier lookup is needed, the safer boundary is Ledger/quota Tx1 lookup using `tenant_id`, `user_id`, and `api_key_id`, matching `docs/process/plans/2026-05-08-upgrade5-quota-codex.md:147-148`.

## Existing Billing / Quota Flow

The intended contract already mentions quota, but implementation is still mostly claim/usage/event only.

- `billing.ClaimGate.Reserve` is documented as opening Tx1 and reserving quota across five dimensions. Evidence: `backend/internal/billing/billing.go:19-24`.
- Actual `DefaultClaimGate.Reserve` starts a serializable transaction, locks/reuses or inserts `billing_ledger_claims`, and commits. Evidence: `backend/internal/billing/claim_gate.go:77-90` and `backend/internal/billing/claim_gate.go:141-171`.
- No current code in `claim_gate.go` mutates user/API-key/subscription quota counters.
- `billing.Settler.Settle` is documented as committing usage, billing event, claim status, outbox, and in-flight decrement. Evidence: `backend/internal/billing/billing.go:27-33`.
- Actual `DefaultSettler.Settle` locks the claim, inserts `usage_records`, inserts `billing_events`, optionally inserts scheduler outbox, releases the pool slot, and commits the claim. Evidence: `backend/internal/billing/settler.go:42-53`, `:84-147`, and `:150-178`.
- Current gateway handler calls `ClaimGate.Reserve` after auth/registry/router and before pool selection. Evidence: `backend/internal/gatewayhttp/chat_completions_handler.go:169-182`.
- Current gateway handler aborts the claim on pool, credential, dispatch, upstream error, or forwarder failure, then calls `Settler.Settle` after successful forward. Evidence: `backend/internal/gatewayhttp/chat_completions_handler.go:209-224`, `:235-280`, `:287-318`.

Conclusion: the right quota integration point is Tx1/Tx2 inside `billing`, not `internal/auth`, not `internal/pool`, and not a detached HTTP-only middleware. This also matches CMB-7: Pool writes only slot/acquisition and in-flight; Ledger owns billing writes. Evidence: `docs/specs/_invariants/cross-module-boundaries.md:149-158`.

## Existing Lock Patterns

### Billing row locks

- Tx1 claim lookup uses `FOR UPDATE`. Evidence: `backend/sql/queries/billing_claims.sql:4-17`.
- Tx1 runs under `pgx.Serializable`. Evidence: `backend/internal/billing/claim_gate.go:77`.
- Tx2 settle locks claim by tenant/id/acquisition token/status using `FOR UPDATE`. Evidence: `backend/sql/queries/billing_settle.sql:6-26`.
- Tx2 settle and abort run under `pgx.Serializable`. Evidence: `backend/internal/billing/settler.go:42` and `backend/internal/billing/settler.go:186`.
- Abort and settle-failure classification manually lock the claim row with `FOR UPDATE`. Evidence: `backend/internal/billing/settler.go:199-203` and `backend/internal/billing/settler.go:304-305`.

### Pool row locks / atomic updates

- Project schema rule says PostgreSQL/sqlc with row-level locks via `SELECT FOR UPDATE`. Evidence: `backend/sql/migrations/0001_pool_routing.up.sql:6`.
- `provider_accounts.in_flight_count` is documented as row-locked for atomic admission. Evidence: `backend/sql/migrations/0001_pool_routing.up.sql:165`.
- Pool revalidation query locks a provider account row using `FOR UPDATE`. Evidence: `backend/sql/queries/pool_accounts.sql:161-164`.
- In-flight admission uses an atomic conditional `UPDATE ... in_flight_count < cap_concurrency`. Evidence: `backend/sql/queries/pool_accounts.sql:166-174`.
- Slot release uses an idempotent CTE: flip `pool_slot_acquisitions.status` from `acquired`, then decrement `provider_accounts.in_flight_count`. Evidence: `backend/sql/queries/billing_settle.sql:83-101`.
- `DBSlotManager.Acquire` runs a serializable transaction and notes a missing serializable retry loop under contention. Evidence: `backend/internal/pool/db_slot_manager.go:52-58` and `backend/internal/pool/db_slot_manager.go:67-107`.

### Advisory locks

- Advisory locks exist for admin bootstrap and API-key issuance, not for billing/quota hot path. Evidence: `backend/sql/queries/admin_audit.sql:41-56`.
- Bootstrap uses the constant advisory lock to avoid double insert. Evidence: `backend/internal/admin/bootstrap.go:46-69`.
- API-key issuance uses a per-actor advisory lock before count-and-insert rate limiting. Evidence: `backend/internal/admin/issuer.go:163-176`.

Conclusion: for U5-A/U5-B quota counters, the local production precedent is row locks/atomic updates inside serializable Tx1/Tx2. Advisory locks are available as a known pattern, but current money-path code does not use them. If quota uses advisory locks, it needs a new lock-order rule to avoid deadlocks with existing row locks.

## Review Of `ALTER TABLE tenants ADD tier_quota_multiplier`

Conflict check:

- No direct column-name conflict exists because `tenants` currently has only identity/status/timestamps/soft-delete fields. Evidence: `backend/sql/migrations/0001_pool_routing.up.sql:15-22`.

Reasonableness:

- Additive and technically safe if defaulted to `1.0` with a strict check.
- But tenant-level multiplier is too coarse for the stated multi-tier product behavior unless Owner explicitly means "one commercial tier per tenant".
- It cannot represent free + paid + enterprise + VIP users within one tenant.
- It cannot represent per-user assignment, per-API-key override, effective time, policy version, or audit of tier switches.
- It creates naming ambiguity: `tier_quota_multiplier` on `tenants` sounds like tenant-wide entitlement, while the plan goal says tier-level multiplier.

Recommendation:

- Prefer a dedicated policy table such as `quota_tiers` plus a tenant/user assignment table, matching the merged codex plan at `docs/process/plans/2026-05-08-upgrade5-quota-codex.md:156-172`.
- If Owner deliberately chooses tenant-wide entitlement for U5-A, name it explicitly, e.g. `tenant_quota_multiplier`, and document that user-level VIP/free separation is Mandatory Roadmap or a later table. Do not silently present tenant-wide multiplier as full tier parity.
- Use `NOT NULL DEFAULT 1.0` and `CHECK (multiplier >= 1.0 AND multiplier <= <Owner-approved cap>)`. A `0` multiplier should not be accepted unless there is a separate disabled/fail-closed status, because `0` makes entitlement semantics ambiguous.

## Review Of Proposed `quota_period(tenant_id, period_start, base_used, multiplier_used)`

This design is not sufficient for the core production scenarios.

What it covers:

- A minimal tenant-period aggregate counter can record base and extension consumption at tenant level.
- It can support the narrow case "one tenant has one tier and one shared pool" if all quota decisions are tenant-wide.

Blocking gaps:

- No subject dimension: it cannot distinguish user-level or API-key-level entitlement. A single `tenant_id` row means one noisy user can consume the tenant's entire base or extension quota.
- No tier/policy dimension: it cannot distinguish free, paid_pro, enterprise, VIP, or policy versions.
- No reservation counters: Tx1 needs reserve/hold state; Tx2 needs settle/release/overrun. `base_used` and `multiplier_used` alone either over-admit concurrent requests or permanently count aborted upstream attempts.
- No claim/event linkage: idempotency replay, abort, settle, and overrun need a durable reservation event keyed to the billing claim.
- No `period_end`, `window_kind`, or timezone/canonical window rule: `period_start` alone is ambiguous for daily/weekly/monthly reset semantics.
- `multiplier_used` is misnamed: the used bucket is an extension amount, not the multiplier factor.
- No audit fields or stage enum: operators cannot answer why a request was denied or why extension quota was consumed.

Recommendation:

- Use a counter table with at least tenant, subject type/id, tier/policy version, window start/end, base reserved/used, extension reserved/used.
- Add a reservation/event table keyed by tenant + claim, with estimated/actual deltas, stage, status, and timestamps.
- Keep FKs composite by tenant, following `0009`'s tenant-isolation pattern. Evidence: `backend/sql/migrations/0009_ledger_fk_backfill.up.sql:11-18` and `:118-143`.

## Risks

### BLOCKING

1. Entitlement scope is not decided. `tenants.tier_quota_multiplier` is safe only for one commercial tier per tenant; it does not satisfy multi-tier users inside one tenant. Owner must choose tenant-wide vs user/API-key/tier assignment before DDL.

2. `quota_period` lacks Tx1 reservation semantics. Without reserved counters and claim-linked events, concurrent admission and abort/retry paths can double-spend or leak quota. This conflicts with the released Tx1/Tx2 model requiring quota reserve before upstream and final reconciliation after upstream. Evidence: `docs/specs/observability-billing.md:47-56` and `docs/specs/observability-billing.md:63-86`.

3. Existing implementation has no actual user/API-key quota counter path yet. Comments mention quota, but code only handles claim/usage/event/slot. U5-A schema must be shaped for future `ClaimGate.Reserve`, `Settler.Settle`, and `Settler.Abort` integration, not just a final-used counter. Evidence: `backend/internal/billing/claim_gate.go:85-171` and `backend/internal/billing/settler.go:84-178`.

4. Serializable retry strategy is missing in nearby pool admission code, and quota counters will introduce the same or higher contention. Evidence: `backend/internal/pool/db_slot_manager.go:52-58`. Before enforcement, quota engine needs bounded retry/backoff for `40001`/deadlock/lock-timeout classes.

5. Unit of quota is not fixed. Current billing uses `numeric(20,8)` cost fields; `usage_records` token counts are integers; handler uses placeholder `0.01` predicted/actual cost. Evidence: `backend/sql/migrations/0002_observability_billing.up.sql:130-144` and `backend/internal/gatewayhttp/chat_completions_handler.go:169-181`, `:300-313`. U5-A should decide tokens vs cost vs both before naming `base_used`.

### SHOULD_FIX

1. Do not allow multiplier `< 1` in normal active tier rows. Use a separate disabled/config-error state for fail-closed behavior.

2. Add `policy_version` and `effective_from/effective_until` to policy rows. Without this, historical quota decisions become non-replayable after tier changes.

3. Add `quota_policy_version`, `quota_tier_id`, and `quota_reservation_event_id` as nullable claim linkage only after the event/counter design is approved. Keep nullable for backward compatibility.

4. Define lock order before implementation. Existing spec lock order is Billing Ledger claim -> User -> API Key -> Subscription -> Provider Account quota -> rate-window rows. Evidence: `docs/specs/observability-billing.md:49-56` and `:65-73`. If new quota counter rows are added, put them in the same order for Tx1 and Tx2.

5. Add indexes for hot-path counter lookup and operator reads: tenant/subject/window/policy unique key; tenant/tier/window; claim linkage; status/time for orphan reservation sweep.

6. Down migrations should be explicit about data loss and should fail-fast when quota events/counters are non-empty, similar to the 0011 fail-fast pattern. Evidence: `backend/sql/migrations/0011_protocol_family_session_extension.down.sql:16-52`.

### NICE

1. Add scheduler/outbox event type for user quota threshold transitions, separate from provider-account `account_quota_changed`.

2. Add non-sensitive response/debug headers for quota stage and headroom behind product policy, as already proposed in the merged codex plan. Evidence: `docs/process/plans/2026-05-08-upgrade5-quota-codex.md:271-278`.

3. Add an operator-facing audit trail for tier assignment changes, not just quota consumption.

4. Record `window_kind` and canonical reset policy in DB comments to avoid daily/monthly timezone drift.

## Suggested Migration Order

Do not implement the current `ALTER tenants + quota_period` sketch as one final schema. It is too coarse and will force a corrective migration when Tx1/Tx2 enforcement starts.

Recommended split:

1. `0013_quota_policy.up.sql` / `.down.sql`
   - Add policy tables only: `quota_tiers` and `user_quota_tier_assignments`, or the explicitly Owner-approved tenant-wide multiplier column.
   - No enforcement and no backfill unless Owner approves seed semantics.
   - Keep feature flag off.

2. `0014_quota_counters.up.sql` / `.down.sql`
   - Add `quota_window_counters` and `quota_reservation_events`.
   - Add nullable linkage columns to `billing_ledger_claims` only if the event table exists.
   - Use composite tenant-scoped FKs.

3. `0015_quota_claim_link_validation.up.sql` only if any FKs to existing hot tables need `NOT VALID` + `VALIDATE`, following the 0009 pattern. Evidence: `backend/sql/migrations/0009_ledger_fk_backfill.up.sql:21-23` and `:152-161`.

If Owner wants a single U5-A migration for speed, it should be one additive migration with policy + counter + event tables, no destructive data changes, no enforcement, no required backfill, and a destructive `.down.sql` clearly marked for staging/dev only. Existing migration style supports `BEGIN`/`COMMIT` around additive DDL. Evidence: `backend/sql/migrations/0012_provider_accounts_proxy_url.up.sql:18-28` and `.down.sql:8-12`.

## Bottom Line

`ALTER TABLE tenants ADD tier_quota_multiplier` is not a direct schema conflict, but it is only a tenant-wide shortcut. The proposed `quota_period(tenant_id, period_start, base_used, multiplier_used)` is not sufficient for two-stage quota under real concurrency, retries, aborts, tier changes, and per-user paid/free separation. Before schema is changed, Owner needs to decide the entitlement scope; then U5-A should add policy, counter, and reservation-event schema that matches the existing Ledger Tx1/Tx2 lock model.

