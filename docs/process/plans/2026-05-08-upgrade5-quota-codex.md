# HUAKAI Upgrade #5 — Two-Stage Quota With tier_max_multiplier

| Field | Value |
| --- | --- |
| Lane | codex |
| Time | 2026-05-08 |
| Role | PLANNER only; no code execution beyond reading and writing this plan |
| Owner directive | "HUAKAI Upgrade #5 — 二阶段 quota with tier_max_multiplier" |
| Clean-room note | No non-MIT reference source was opened. This plan relies on local HUAKAI source/specs and the Owner-provided high-level contrast with sub2api. |

## Current-State Findings

1. Auth currently resolves only `tenant_id`, `api_key_id`, and `user_id`; no tier or quota policy is carried in `auth.Identity` [backend/internal/auth/api_key_resolver.go:38](/home/codex/HUAKAI/backend/internal/auth/api_key_resolver.go:38) and [backend/internal/auth/api_key_resolver.go:138](/home/codex/HUAKAI/backend/internal/auth/api_key_resolver.go:138).
2. `users` and `api_keys` schema currently have identity/status/key lifecycle fields, but no user tier, paid tier, base quota, or multiplier fields [backend/sql/migrations/0007_l0_inbound_auth.up.sql:18](/home/codex/HUAKAI/backend/sql/migrations/0007_l0_inbound_auth.up.sql:18) and [backend/sql/migrations/0007_l0_inbound_auth.up.sql:51](/home/codex/HUAKAI/backend/sql/migrations/0007_l0_inbound_auth.up.sql:51).
3. Existing quota-like columns live on `provider_accounts` and represent upstream capacity/account quota, not end-user/tier quota [backend/sql/migrations/0001_pool_routing.up.sql:139](/home/codex/HUAKAI/backend/sql/migrations/0001_pool_routing.up.sql:139).
4. The released billing spec already requires Tx1 reserve and Tx2 settle with quota across User/API Key/Subscription/Provider Account/rate-window dimensions [docs/specs/observability-billing.md:47](/home/codex/HUAKAI/docs/specs/observability-billing.md:47), but current `DefaultClaimGate.Reserve` only creates/reuses a claim row and does not yet mutate user quota counters [backend/internal/billing/claim_gate.go:71](/home/codex/HUAKAI/backend/internal/billing/claim_gate.go:71).
5. Current HTTP hot path is auth -> registry/router -> claim reserve -> pool select -> upstream -> settle [backend/internal/gatewayhttp/chat_completions_handler.go:68](/home/codex/HUAKAI/backend/internal/gatewayhttp/chat_completions_handler.go:68), [backend/internal/gatewayhttp/chat_completions_handler.go:169](/home/codex/HUAKAI/backend/internal/gatewayhttp/chat_completions_handler.go:169), [backend/internal/gatewayhttp/chat_completions_handler.go:198](/home/codex/HUAKAI/backend/internal/gatewayhttp/chat_completions_handler.go:198), and [backend/internal/gatewayhttp/chat_completions_handler.go:318](/home/codex/HUAKAI/backend/internal/gatewayhttp/chat_completions_handler.go:318).
6. Rate limiting is explicitly separate from client-facing user/model limits; F-RATE-001 is about upstream Provider Account cooldowns [docs/specs/rate-limiting.md:23](/home/codex/HUAKAI/docs/specs/rate-limiting.md:23).
7. Cross-module rules say Pool must not compute cost, and Ledger owns billing/quota mutations [docs/specs/_invariants/cross-module-boundaries.md:109](/home/codex/HUAKAI/docs/specs/_invariants/cross-module-boundaries.md:109) and [docs/specs/_invariants/cross-module-boundaries.md:149](/home/codex/HUAKAI/docs/specs/_invariants/cross-module-boundaries.md:149).

## Scope

In scope:

- Add a tenant-scoped quota/tier policy model that supports:
  - Stage 1 `base` quota for all users.
  - Stage 2 `tier_extension` quota for VIP/paid users where max = `base_limit * tier_max_multiplier`.
  - Normal users default to multiplier `1.0` and never consume extension quota.
- Add additive schema only, behind feature flag or config gate:
  - New policy/config tables, recommended names:
    - `quota_tiers`
    - `user_quota_tier_assignments`
  - New transactional state/audit tables, recommended names:
    - `quota_window_counters`
    - `quota_reservation_events`
  - Optional additive linkage columns on `billing_ledger_claims`:
    - `quota_policy_version`
    - `quota_tier_id`
    - `quota_reservation_event_id`
- Add `backend/internal/quota/` as the quota engine package.
- Update sqlc query files under `backend/sql/queries/` and generated `backend/internal/db/` after migration design is approved.
- Wire quota reserve/settle/abort into `backend/internal/billing/claim_gate.go` and `backend/internal/billing/settler.go`, not into Pool.
- Map quota failures in `backend/internal/gatewayhttp/chat_completions_handler.go` to stable HTTP errors.
- Add focused tests under:
  - `backend/internal/quota/`
  - `backend/internal/billing/`
  - `backend/internal/gatewayhttp/`
  - migration/apply rollback checks.
- Update docs/specs or docs/plans after Owner chooses the decision points below.

Out of scope:

- No payment provider or wallet top-up implementation.
- No new runtime dependency; use existing `pgx`, `sqlc`, and `shopspring/decimal` [backend/go.mod:5](/home/codex/HUAKAI/backend/go.mod:5).
- No changes to `LICENSE`.
- No changes to authentication core semantics unless Owner explicitly approves carrying tier inside `auth.Identity`.
- No changes to Provider Account upstream quota semantics except preventing naming/behavior conflicts.
- No dynamic stream-time reserve extension beyond explicit settlement handling; A27 remains future work unless Owner pulls it into this slice [docs/03_FEATURE_PARITY_MATRIX.md:153](/home/codex/HUAKAI/docs/03_FEATURE_PARITY_MATRIX.md:153).

## Success Criteria

1. A normal user with multiplier `1.0` is admitted only up to base quota and receives a deterministic quota-exhausted response before upstream dispatch after base is exhausted.
2. A VIP/paid user consumes base quota first, then extension quota up to `base * tier_max_multiplier`; the extension path does not reduce any normal user's base quota.
3. Tx1 reservation, Tx2 settlement, and abort/release paths are idempotent and tenant-scoped.
4. Same logical request cannot double-reserve or double-settle quota under idempotency replay, claim race, retry, or handler retry.
5. Quota state remains exact under concurrent requests; if base quota allows N reservations, exactly N base-stage reservations succeed before extension/deny behavior applies.
6. `tier_max_multiplier` is range-validated and cannot create negative, NaN, zero, or absurdly high effective limits.
7. Rate-limit/cooldown failures remain separate from user quota failures and do not consume final user quota on aborted upstream attempts.
8. Rollout can be disabled without schema rollback; schema down migrations exist for staging/dev, but production rollback is feature-flag-off unless Owner approves destructive data removal.

## Time Estimate And Atomic Work Units

1. Schema design and migration plan: 0.5-1 day.
   - Produce exact DDL, down migration, lock-order note, and backfill/no-backfill decision.
   - Requires Owner confirmation because database schema and quota enforcement are high-risk.
2. Quota engine implementation: 1-1.5 days.
   - Add `internal/quota` interfaces, DB-backed engine, typed errors, and sqlc queries.
   - No new dependency.
3. Billing integration: 1 day.
   - Tx1 reserve inside `ClaimGate.Reserve`.
   - Tx2 settle/abort inside `Settler.Settle` / `Settler.Abort`.
   - Preserve claim idempotency and usage/billing event atomicity.
4. Gateway integration: 0.5 day.
   - Error mapping, response headers, and smoke path updates.
   - Keep auth resolver unchanged unless Owner chooses auth-level tier propagation.
5. Tests and race coverage: 1-2 days.
   - Migration up/down, engine unit tests, billing integration tests, handler tests, concurrent serializable tests.
6. Docs and review: 0.5 day.
   - Update quota spec/plan, risk register, acceptance matrix rows, and run required cross-review before commit.

Recommended atomic delivery order:

1. `schema-only` branch or commit.
2. `quota-engine-only` commit with isolated tests.
3. `billing-integration` commit.
4. `gateway-error-mapping` commit.
5. `acceptance-and-docs` commit.

## Blast Radius

Highest risk.

- Database schema: new quota state tables and optional claim linkage columns.
- Money/quota path: Tx1/Tx2 reserve and settle affect revenue correctness.
- Wallet/account behavior: even without payment provider changes, quota exhaustion and VIP entitlement are commercial controls.
- Gateway availability: fail-closed quota backend errors can reject valid traffic.
- Pool/routing: quota errors must not masquerade as upstream pool depletion or rate-limit cooldown.
- Operator trust: wrong counters create visible "paid user was denied" or "free user was over-served" incidents.

## Failure Modes And Mitigations

| Failure mode | Risk | Mitigation |
| --- | --- | --- |
| Double reserve | Idempotent retry or claim race consumes quota twice | `quota_reservation_events` unique by `(tenant_id, claim_id, stage, event_type)`; Tx1 locks claim and quota counter rows in fixed order. |
| Double settle | Replay of Tx2 mutates used counters twice | Settle is idempotent by claim/reservation event status; repeat settle returns prior result or no-ops after committed. |
| Quota leak on abort | Upstream failure leaves `reserved` amount held forever | `Settler.Abort` releases reservation deltas in same Tx as abort event and slot release; orphan sweep later handles missed claims. |
| Actual usage > reserved | Under-estimation allows overrun | Owner decision required. Recommended: never skip audit settlement; mark overrun debt/block future requests rather than dropping Tx2. |
| Multiplier overflow | `base * multiplier` exceeds numeric bounds or grants excessive quota | DB check multiplier range, app clamp, decimal arithmetic, and max effective limit guard. |
| Multiplier bypass for normal users | Free users accidentally get extension | Default multiplier = 1; extension allocation allowed only when tier assignment is active and multiplier > 1. |
| VIP squeezes normal base | Paid users consume a tenant-wide pool intended for free users | Recommended model is per-subject base window plus tier extension. If Owner wants shared tenant pool, use separate free/paid pool partitions. |
| Tenant bleed | Tenant A quota row updated by tenant B claim | Composite tenant-scoped FKs and every query includes `tenant_id`; tests mirror existing FK regression pattern. |
| Rate-limit conflict | Upstream 429/529 changes user quota incorrectly | User quota reserve happens before upstream, but abort path releases on upstream terminal failure; Provider Account cooldown remains in `internal/rate`. |
| Fail-open during quota DB outage | Requests are served untracked | Admission path fail-closed by default: return 503 before upstream. No successful upstream response may bypass durable settlement. |
| Fail-closed too broad | Quota store transient failure rejects all traffic | Circuit breaker state visible to operator; optional per-tenant emergency override needs explicit Owner decision and audit. |
| Schema rollback loses data | Down migration drops live quota events | Production rollback is feature-flag-off; SQL down only for staging/dev unless Owner approves destructive cleanup. |

## Decision Points Requiring Owner Confirmation

1. Where does `tier_max_multiplier` live?
   - Recommended: tenant-scoped `quota_tiers`, assigned to users via `user_quota_tier_assignments`.
   - Alternatives: direct `users.tier`, per-API-key tier, per-route override.
   - Reason: user tier maps cleanly to VIP/paid entitlement without changing auth resolver or route policy.
2. Is quota aggregated across API keys for one user, or independent per API key?
   - Recommended: user-level quota is authoritative; API key may have a stricter optional cap later.
   - Reason: prevents a user from multiplying quota by creating keys.
3. Is base quota per subject or tenant-wide shared?
   - Recommended: per-user/per-window base plus per-user extension.
   - If tenant-wide shared is required, free and paid pools must be separated to preserve the stated decoupling.
4. What unit is enforced first?
   - Recommended for Upgrade #5: tokens first, with USD/cost quotas as a follow-up if Owner wants F-SEC-006 money-axis parity in the same slice.
5. What happens when actual usage exceeds reserved quota after upstream has already produced output?
   - Recommended: settle durably with overrun annotation, block future requests until quota recovers, and emit operator alert.
   - Strict alternative: Tx2 reject and leave claim for manual/orphan recovery, but this risks settlement backlog.
6. Fail-open or fail-closed?
   - Recommended: fail-closed before upstream when quota engine/storage is unavailable; never fail-open for billable traffic.
   - Emergency override, if allowed, must be per-tenant/per-tier, time-bounded, and audited.
7. Should tier be returned by auth?
   - Recommended: no. Keep auth read-only and identity-only; quota engine looks up tier inside Ledger Tx1 using `tenant_id/user_id/api_key_id`.

## Design Outline

### Schema

Recommended additive tables:

1. `quota_tiers`
   - `id bigserial primary key`
   - `tenant_id bigint not null`
   - `code text not null`
   - `display_name text not null`
   - `base_limit_tokens numeric(20,8) not null`
   - `window_kind text not null check in ('daily', 'weekly', 'monthly')`
   - `tier_max_multiplier numeric(10,4) not null default 1.0000`
   - `status text not null check in ('active', 'disabled')`
   - `policy_version text not null`
   - `effective_from timestamptz not null`
   - `effective_until timestamptz`
   - checks: base >= 0, multiplier >= 1, multiplier <= Owner-approved hard max.
2. `user_quota_tier_assignments`
   - `tenant_id`, `user_id`, `tier_id`
   - `status`, `effective_from`, `effective_until`
   - unique active assignment per `(tenant_id, user_id)`.
3. `quota_window_counters`
   - `tenant_id`
   - `subject_type text` initially `user`
   - `subject_id bigint`
   - `tier_id bigint`
   - `policy_version text`
   - `window_start`, `window_end`
   - `base_reserved_tokens numeric(20,8)`
   - `base_used_tokens numeric(20,8)`
   - `extension_reserved_tokens numeric(20,8)`
   - `extension_used_tokens numeric(20,8)`
   - `updated_at`
   - unique `(tenant_id, subject_type, subject_id, window_start, window_end, policy_version)`.
4. `quota_reservation_events`
   - `tenant_id`
   - `claim_id`
   - `api_key_id`
   - `user_id`
   - `tier_id`
   - `policy_version`
   - `stage text check in ('base', 'tier_extension', 'mixed')`
   - `estimated_tokens`
   - `actual_tokens`
   - `base_reserved_delta`
   - `extension_reserved_delta`
   - `base_used_delta`
   - `extension_used_delta`
   - `status text check in ('reserved', 'settled', 'aborted', 'released', 'overrun')`
   - `reason text`
   - `created_at`, `settled_at`
   - unique `(tenant_id, claim_id)` for the active reservation.
5. Optional claim linkage:
   - Add nullable `quota_policy_version`, `quota_tier_id`, `quota_reservation_event_id` to `billing_ledger_claims`.
   - Keep nullable for migration/backward compatibility.

Migration approach:

- Use a new numbered migration after `0012`, likely `0013_quota_tiers.up.sql` plus matching `.down.sql`.
- Prefer one atomic `BEGIN`/`COMMIT` migration for new tables and nullable columns.
- No destructive rewrite of existing rows.
- Backfill only default `quota_tiers` rows for existing tenants if Owner approves; otherwise require explicit seed/admin setup and keep feature flag disabled.
- Down migration drops only new foreign keys, indexes, columns, and tables in reverse order; production rollback uses feature flag off.

### Core Engine API

Recommended package: `backend/internal/quota`.

Interfaces:

- `PolicyStore.LookupActiveTier(ctx, tenantID, userID, apiKeyID, at)`.
- `Engine.Reserve(ctx, tx, ReserveInput) (ReservationResult, error)`.
- `Engine.Settle(ctx, tx, SettleInput) (SettleResult, error)`.
- `Engine.Abort(ctx, tx, AbortInput) error`.
- `Engine.Headroom(ctx, tenantID, userID, apiKeyID, at) (Headroom, error)` for admin/operator read APIs later.

Inputs:

- `tenant_id`, `user_id`, `api_key_id`, `claim_id`, `request_class`, `model`, `policy_version`.
- Estimated tokens from request/max token estimator. Current handler uses a hard-coded predicted cost, so this slice should replace or wrap that placeholder before real enforcement.
- Actual tokens from `gateway.UsageRecordDraft` during Tx2.

Reserve algorithm:

1. Lock or create the current `quota_window_counters` row for `(tenant, user, window, policy)`.
2. Read active tier assignment and multiplier.
3. Compute:
   - `base_limit = tier.base_limit_tokens`
   - `effective_limit = base_limit * tier_max_multiplier`
   - `extension_limit = effective_limit - base_limit`
4. Allocate estimate first to base headroom, then extension headroom if multiplier > 1.
5. If the request cannot fit either stage, rollback Tx1 and return typed quota exhaustion.
6. Insert one `quota_reservation_events` row with exact stage breakdown.

Settle algorithm:

1. Lock claim and quota event/counter rows in the same fixed order as billing.
2. Convert reservation from `reserved` to `used` using actual tokens.
3. If actual < estimate, release unused reservation.
4. If actual > estimate but within effective limit, consume additional base/extension headroom.
5. If actual exceeds effective limit, follow Owner-chosen overrun policy; recommended is durable settle with overrun annotation and block future requests.
6. Update quota event status to `settled` in the same Tx2 as Usage Record and Billing Event.

Abort algorithm:

1. Lock event/counter rows by tenant and claim.
2. Move reserved deltas back out of counters.
3. Mark event `aborted` or `released`.
4. Execute in the same abort Tx2 path that writes zero-cost usage/audit artifacts when applicable.

### Middleware / Handler Integration

Recommended integration is Ledger-first, not a standalone HTTP middleware:

1. Keep `auth.Resolve` as identity-only.
2. Add quota dependencies to billing construction, not to Pool.
3. `ClaimGate.Reserve` calls `quota.Engine.Reserve` inside Tx1 after claim creation/reuse and before commit.
4. `Settler.Settle` calls `quota.Engine.Settle` inside Tx2 before claim status changes to committed.
5. `Settler.Abort` calls `quota.Engine.Abort` inside abort Tx2.
6. `gatewayhttp` maps typed errors:
   - `quota_exhausted_base` -> 402 or 429, Owner to choose external code.
   - `quota_exhausted_tier_extension` -> 402.
   - `quota_backend_error` -> 503 fail-closed.
7. Response/debug headers, if enabled:
   - `X-Huakai-Quota-Stage: base|tier_extension|mixed`
   - `X-Huakai-Quota-Headroom-Tokens`
   - Avoid exposing paid/free classification unless product policy allows it.

## Test Matrix

| Area | Scenario | Expected |
| --- | --- | --- |
| Migration | Apply up on empty DB | New tables/columns exist, constraints valid. |
| Migration | Apply up on DB with existing users/api_keys/claims | Existing hot path still works with feature flag off. |
| Migration | Apply down in staging | Drops new schema additions in reverse order without touching older tables. |
| Policy | Missing tier assignment | Default tier with multiplier 1 or typed config error per Owner choice. |
| Policy | Multiplier < 1, zero, huge, malformed | Rejected by DB/app validation. |
| Reserve base | Normal user under base | Succeeds, stage `base`, no extension delta. |
| Reserve deny | Normal user over base | Fails before Pool/Adapter, no upstream dispatch, no quota mutation leak. |
| Reserve extension | VIP exceeds base but within multiplier | Succeeds with `tier_extension` or `mixed` event. |
| Reserve extension deny | VIP exceeds base * multiplier | Fails before upstream with typed quota error. |
| Decoupling | VIP consumes full extension, normal user still has own base | Normal request still succeeds if its own base headroom remains. |
| Idempotency | Same logical request replay | No double reserve; returns prior claim behavior. |
| Fingerprint conflict | Same logical id, different payload | 409 path remains, quota not double-mutated. |
| Abort | Upstream terminal failure after reserve | Reservation released, claim aborted, zero final user quota used. |
| Settle refund | Actual tokens less than estimate | Unused reservation released. |
| Settle extension | Actual tokens greater than estimate but within multiplier | Additional headroom consumed once. |
| Settle overrun | Actual tokens beyond multiplier | Owner-chosen overrun path; audit event and future blocking verified. |
| Concurrency | 100 parallel reserves for quota allowing exactly N | Exactly N base/extension admissions; no negative counters. |
| Tenant isolation | Same user id / claim id across tenants | Counters and events never cross tenants. |
| Rate-limit conflict | Upstream 429/529 after reserve | Provider cooldown handled by rate layer; user quota released or settled according to terminal/partial outcome. |
| Pool boundary | Pool selection sees no decimal/cost fields | CMB-2 preserved. |
| Handler | Quota backend unavailable | 503 before upstream; no untracked spend. |
| Observability | Usage/billing event includes quota stage linkage | Operator can answer why request was denied or why extension was used. |

## Required Review And Docs Updates

- Update or create quota-specific spec under `docs/specs/` after Owner confirms decision points.
- Update `docs/03_FEATURE_PARITY_MATRIX.md` for Upgrade #5 disposition.
- Update `docs/10_RISK_REGISTER.md` for tier/multiplier overrun and quota-engine fail-closed risk.
- Update `docs/11_ACCEPTANCE_TEST_MATRIX.md` with AT-QUOTA-005-* rows.
- Stage changes and run `codex exec review --uncommitted --full-auto` before any commit, per AGENTS.md.

## References Read

- `backend/internal/auth/auth.go`
- `backend/internal/auth/api_key_resolver.go`
- `backend/internal/rate/rate.go`
- `backend/internal/billing/billing.go`
- `backend/internal/billing/claim_gate.go`
- `backend/internal/billing/settler.go`
- `backend/internal/gatewayhttp/chat_completions_handler.go`
- `backend/internal/db/models.go`
- `backend/internal/db/billing_claims.sql.go`
- `backend/internal/db/billing_settle.sql.go`
- `backend/internal/db/auth_inbound.sql.go`
- `backend/internal/pool/db_slot_manager.go`
- `backend/internal/pool/db_account_source.go`
- `backend/sql/queries/billing_claims.sql`
- `backend/sql/queries/billing_settle.sql`
- `backend/sql/queries/auth_inbound.sql`
- `backend/sql/queries/pool_accounts.sql`
- `backend/sql/queries/pool_slot_acquisitions.sql`
- `backend/sql/migrations/0001_pool_routing.up.sql`
- `backend/sql/migrations/0002_observability_billing.up.sql`
- `backend/sql/migrations/0004_rate_limiting.up.sql`
- `backend/sql/migrations/0007_l0_inbound_auth.up.sql`
- `backend/sql/migrations/0009_ledger_fk_backfill.up.sql`
- `backend/go.mod`
- `docs/specs/observability-billing.md`
- `docs/specs/pool-routing.md`
- `docs/specs/rate-limiting.md`
- `docs/specs/_invariants/cross-module-boundaries.md`
- `docs/decompositions/_cross-cutting/quota-billing-claim-gate-synthesis.md`
- `docs/03_FEATURE_PARITY_MATRIX.md`
- `docs/10_RISK_REGISTER.md`
- `docs/11_ACCEPTANCE_TEST_MATRIX.md`
- `docs/19_DOMAIN_MODEL.md`
