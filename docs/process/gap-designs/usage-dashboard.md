# Gap Design: Usage Analytics Dashboard (Admin + User Self-Serve)

**Gap ID:** usage-dashboard  
**Author:** HUAKAI Backend Architect  
**Date:** 2026-06-03  
**Status:** DRAFT — implementation-ready

---

## Summary

HUAKAI's existing observability surface (`internal/obs`, `internal/meusagehttp`,
`internal/gatewayhttp/admin_observability_handler.go`) delivers raw paginated
listing of usage records, claims, and audit events. Three capabilities are
missing that product requires:

1. **Time-series aggregation** — cost/token totals bucketed by calendar day,
   broken down by model and by pooling group.
2. **User spend ranking** — which API keys / users consumed the most cost in a
   window; admin cross-tenant and user self-serve (own API key only).
3. **RPM/TPM summaries** — requests-per-minute and tokens-per-minute peak/
   average derived from settled `usage_records` rows.

The design introduces a single new package `internal/usageanalytics` for
business logic, a new HTTP package `internal/usageanalyticshttp` for endpoint
handlers, and a new SQL query file
`sql/queries/usage_analytics.sql` with migration `0077`. No frozen packages
(`internal/gatewayhttp`, `internal/gateway`, `internal/proto`) gain new files;
`cmd/gateway/routes.go` receives minimal wiring additions only.

The implementation is **read-only** (SELECT only, CMB-7) and **tenant-scoped**
(every query carries `tenant_id`, CMB-5/CMB-7). No credentials, no raw upstream
payloads, no billing writes.

---

## Package layout

### New package: `internal/usageanalytics`

All business-logic types, interfaces, and the pure aggregation logic. No HTTP.
Each file is under 500 lines.

| File | Responsibility | Est. lines |
|------|----------------|------------|
| `doc.go` | Package-level godoc; cites CMB-5, CMB-7, references specs | ~15 |
| `types.go` | Domain types: `TimeSeriesPoint`, `SpendRankRow`, `RateWindow`, `DashboardFilter`, `SpendRankFilter`, `RateFilter` | ~90 |
| `service.go` | `Service` interface + `PgxService` struct constructor; delegates to `Repo` for raw data, applies derived calculations (RPM/TPM from settled rows) | ~130 |
| `repo.go` | `Repo` interface wrapping the four sqlc-generated query methods; `PgxRepo` implementation | ~80 |
| `aggregator.go` | Pure functions: `AggregateTimeSeries`, `RankSpend`, `ComputeRateWindows`; stateless, fully unit-testable without DB | ~160 |
| `aggregator_test.go` | Discriminating unit tests for aggregation logic | ~200 |

Total new lines in package: ~675 across 6 files, no single file exceeds 500.

### New package: `internal/usageanalyticshttp`

HTTP handlers only. Depends on `internal/usageanalytics` (not on
`internal/gatewayhttp`). Each handler file < 500 lines.

| File | Responsibility | Est. lines |
|------|----------------|------------|
| `doc.go` | Package-level godoc; CMB boundary note | ~15 |
| `types.go` | Request/response DTOs; shared `writeJSON`/`writeJSONError` helpers; `AdminAuth` + `UserAuth` interface definitions | ~80 |
| `admin_handler.go` | Three admin endpoints (time-series, spend rank, rate summary); admin RBAC via `admin.AdminIdentity`; `parseTenantScope` (copy of gatewayhttp pattern, not imported from frozen pkg) | ~220 |
| `user_handler.go` | Two user self-serve endpoints (time-series own key, spend rank own key); resolves `auth.Identity`; enforces `api_key_id == ident.APIKeyID` | ~160 |
| `admin_handler_test.go` | Discriminating tests for admin RBAC, tenant scoping, parameter validation | ~280 |
| `user_handler_test.go` | Discriminating tests for self-serve scoping, cursor, time range | ~200 |

Total new lines in package: ~955 across 6 files, no single file exceeds 500.

### Modified (not new): `cmd/gateway/routes.go`

Add ~30 lines to `mountRoutes` wiring the six new endpoints. No new file.

### New SQL: `sql/queries/usage_analytics.sql`

Four named queries consumed via sqlc. Target generated package:
`internal/db/billing` (same as all observability queries — consistent with
`obs_queries.sql` and `observability.sql` placement). Approximately 120 lines.

### New migration: `sql/migrations/0077_usage_analytics_views.up.sql` / `.down.sql`

Two files, each < 100 lines (see Schema section below).

---

## Schema / migrations

### Migration 0077

**File:** `sql/migrations/0077_usage_analytics_views.up.sql`

```sql
-- Migration 0077: analytics helper indexes for time-series + ranking queries.
-- No new tables; adds partial indexes on usage_records to support the three
-- analytics query shapes without full-table scans on large tenants.
-- All indexes are partial: WHERE settled_at IS NOT NULL (aborted claims have
-- NULL settled_at and must never appear in cost aggregations).
-- CMB-7: this file is DDL only; no DML, no credential columns touched.

BEGIN;

-- Index A: time-series by tenant + day bucket + requested_model.
-- Supports: GROUP BY date_trunc('day', settled_at), requested_model
-- with a leading tenant_id = $1 predicate.
CREATE INDEX CONCURRENTLY IF NOT EXISTS
    idx_ur_analytics_tenant_day_model
ON usage_records (
    tenant_id,
    date_trunc('day', settled_at AT TIME ZONE 'UTC'),
    requested_model
)
INCLUDE (actual_cost, tokens_input, tokens_output, cache_read_tokens, cache_creation_tokens)
WHERE settled_at IS NOT NULL;

-- Index B: spend ranking by tenant + api_key_id (user spend).
-- Supports: GROUP BY api_key_id ORDER BY sum(actual_cost) DESC
CREATE INDEX CONCURRENTLY IF NOT EXISTS
    idx_ur_analytics_tenant_apikey_cost
ON usage_records (tenant_id, api_key_id)
INCLUDE (actual_cost, settled_at)
WHERE settled_at IS NOT NULL;

-- Index C: pooling_group dimension via billing_ledger_claims join.
-- The join key (claim_id, tenant_id) already has a primary-key path;
-- no additional index needed on that side.

COMMIT;
```

**File:** `sql/migrations/0077_usage_analytics_views.down.sql`

```sql
BEGIN;
DROP INDEX CONCURRENTLY IF EXISTS idx_ur_analytics_tenant_apikey_cost;
DROP INDEX CONCURRENTLY IF EXISTS idx_ur_analytics_tenant_day_model;
COMMIT;
```

### New sqlc queries: `sql/queries/usage_analytics.sql`

Four queries. All are SELECT-only (CMB-7). All carry `tenant_id` predicate
(CMB-5 / cross-tenant prevention). No credential columns selected.

**Query 1 — `AggregateUsageByDay`** `:many`

```sql
-- name: AggregateUsageByDay :many
-- Time-series: cost + token totals bucketed by UTC day, filtered by model
-- and/or pooling group. tenant_id is always required (non-nullable).
-- CMB-5: no credential columns selected.
-- CMB-7: SELECT only.
SELECT
    date_trunc('day', ur.settled_at AT TIME ZONE 'UTC')::timestamptz AS day,
    ur.requested_model,
    blc.pooling_group_id,
    sum(ur.actual_cost)::numeric(20,8)             AS total_cost,
    sum(ur.tokens_input)::bigint                   AS total_tokens_input,
    sum(ur.tokens_output)::bigint                  AS total_tokens_output,
    sum(ur.cache_read_tokens)::bigint              AS total_cache_read_tokens,
    sum(ur.cache_creation_tokens)::bigint          AS total_cache_creation_tokens,
    count(*)::bigint                               AS request_count
FROM usage_records ur
JOIN billing_ledger_claims blc
  ON blc.id = ur.claim_id AND blc.tenant_id = ur.tenant_id
WHERE ur.tenant_id = sqlc.arg(tenant_id)::bigint
  AND ur.settled_at IS NOT NULL
  AND ur.settled_at >= sqlc.arg(from_ts)::timestamptz
  AND ur.settled_at <  sqlc.arg(to_ts)::timestamptz
  AND (sqlc.narg(model)::text       IS NULL OR ur.requested_model  = sqlc.narg(model)::text)
  AND (sqlc.narg(pool_group_id)::bigint IS NULL
       OR blc.pooling_group_id = sqlc.narg(pool_group_id)::bigint)
GROUP BY 1, 2, 3
ORDER BY 1 DESC, 2 ASC;
```

**Query 2 — `RankSpendByAPIKey`** `:many`

```sql
-- name: RankSpendByAPIKey :many
-- Spend ranking: top N api_keys by settled cost in window.
-- api_key_id enforced equals caller's key when self-serve (enforced in handler,
-- not here — SQL receives the scoped api_key_id when user self-serve).
SELECT
    ur.api_key_id,
    ur.user_id,
    sum(ur.actual_cost)::numeric(20,8)  AS total_cost,
    count(*)::bigint                    AS request_count,
    sum(ur.tokens_input + ur.tokens_output)::bigint AS total_tokens
FROM usage_records ur
WHERE ur.tenant_id = sqlc.arg(tenant_id)::bigint
  AND ur.settled_at IS NOT NULL
  AND ur.settled_at >= sqlc.arg(from_ts)::timestamptz
  AND ur.settled_at <  sqlc.arg(to_ts)::timestamptz
  AND (sqlc.narg(api_key_id)::bigint IS NULL
       OR ur.api_key_id = sqlc.narg(api_key_id)::bigint)
GROUP BY ur.api_key_id, ur.user_id
ORDER BY total_cost DESC
LIMIT sqlc.arg(rank_limit)::integer;
```

**Query 3 — `SummariseRPMTPM`** `:many`

```sql
-- name: SummariseRPMTPM :many
-- RPM/TPM: aggregate by 1-minute bucket; caller computes peak/avg in Go.
-- Kept as raw minute buckets so the Go aggregator can compute p-values
-- without a second round-trip.
SELECT
    date_trunc('minute', ur.settled_at AT TIME ZONE 'UTC')::timestamptz AS minute_bucket,
    count(*)::bigint                                   AS rpm,
    sum(ur.tokens_input + ur.tokens_output)::bigint    AS tpm
FROM usage_records ur
WHERE ur.tenant_id = sqlc.arg(tenant_id)::bigint
  AND ur.settled_at IS NOT NULL
  AND ur.settled_at >= sqlc.arg(from_ts)::timestamptz
  AND ur.settled_at <  sqlc.arg(to_ts)::timestamptz
  AND (sqlc.narg(api_key_id)::bigint IS NULL
       OR ur.api_key_id = sqlc.narg(api_key_id)::bigint)
GROUP BY 1
ORDER BY 1 ASC;
```

**Query 4 — `QuotaWindowSnapshot`** `:many`

```sql
-- name: QuotaWindowSnapshot :many
-- Current quota window state per policy for a tenant.
-- Used by the dashboard to show remaining quota vs. consumed.
-- Joins quota_policies so the handler can show metric + window_kind.
SELECT
    qp.id              AS policy_id,
    qp.scope_kind,
    qp.scope_id,
    qp.metric,
    qp.window_kind,
    qp.limit_value,
    qw.window_start,
    qw.window_end,
    qw.reserved_value,
    qw.settled_value,
    qw.overage_value,
    qw.request_count
FROM quota_policies qp
JOIN quota_windows qw
  ON qw.tenant_id = qp.tenant_id AND qw.policy_id = qp.id
WHERE qp.tenant_id = sqlc.arg(tenant_id)::bigint
  AND qp.enabled = true
  AND qw.window_end > sqlc.arg(at_time)::timestamptz
ORDER BY qp.priority ASC, qp.id ASC;
```

---

## Endpoints

All new endpoints live under the `internal/usageanalyticshttp` package and are
wired in `cmd/gateway/routes.go`.

### Admin endpoints (auth: admin bearer `hk_admin_*`)

RBAC follows the existing `parseTenantScope` pattern:
`platform_admin` may query any tenant (pass `?tenant_id=`); `tenant_operator`
is auto-scoped to its `ScopeTenantID` and may not cross tenant.

| Method | Path | Auth scope | Description |
|--------|------|------------|-------------|
| `GET` | `/admin/v1/analytics/usage/time-series` | `platform_admin` or `tenant_operator` | Time-series cost+tokens by day, model, pool group. Params: `tenant_id`, `from`, `to` (RFC3339), `model`, `pool_group_id`. Response: `{ items: [TimeSeriesPoint], period: {from, to} }` |
| `GET` | `/admin/v1/analytics/usage/spend-rank` | `platform_admin` or `tenant_operator` | Top-N API key spend rank. Params: `tenant_id`, `from`, `to`, `limit` (1–100). Response: `{ items: [SpendRankRow] }` |
| `GET` | `/admin/v1/analytics/usage/rate-summary` | `platform_admin` or `tenant_operator` | RPM/TPM peak + average over window. Params: `tenant_id`, `from`, `to`, `api_key_id` (optional). Response: `{ peak_rpm, avg_rpm, peak_tpm, avg_tpm, period: {from,to} }` |
| `GET` | `/admin/v1/analytics/quota/snapshot` | `platform_admin` or `tenant_operator` | Current quota window state for all active policies. Params: `tenant_id`. Response: `{ items: [QuotaWindowView] }` |

### User self-serve endpoints (auth: customer API key `hk_live_*` / `hk_test_*`)

Scoped to the authenticated API key's `TenantID` and `APIKeyID`. The handler
passes `api_key_id = ident.APIKeyID` to SQL — cross-key reads are structurally
impossible.

| Method | Path | Auth scope | Description |
|--------|------|------------|-------------|
| `GET` | `/v1/me/analytics/time-series` | API key bearer | Time-series for own API key. Params: `from`, `to` (RFC3339, required), `model`. Response: `{ items: [TimeSeriesPoint], period: {from,to} }` |
| `GET` | `/v1/me/analytics/spend-rank` | API key bearer | Spend breakdown by model for own key (reuses `RankSpendByAPIKey` with `api_key_id` locked). Params: `from`, `to`, `limit` (1–50). Response: `{ items: [SpendRankRow] }` |

---

## Invariants honored

| CMB invariant | How honored in this design |
|---------------|---------------------------|
| **CMB-5** — credentials never logged, never selected | Every SQL query is SELECT-only; no `credentials`, `acquisition_token`, `key_hash`, `key_prefix`, or `plaintext_bearer` column appears anywhere. Handler error paths use static codes; no credential material in error strings. |
| **CMB-7** — router reads no credentials, writes nothing | All four SQL queries are pure SELECT. The `usageanalytics` and `usageanalyticshttp` packages contain zero INSERT/UPDATE/DELETE. |
| **CMB-1** — `adminhttp`/`admin` never imported from hot path | `usageanalyticshttp` imports `internal/admin` for `AdminIdentity`/`ErrAdminUnauthorized` only in the admin handler; the user handler imports `internal/auth` for `Identity`. Neither handler is wired into `internal/router`. |
| **Tenant scoping** | Every SQL query carries `tenant_id = sqlc.arg(tenant_id)::bigint` as a non-nullable predicate. The `parseTenantScope` helper (mirrored from `gatewayhttp`, not imported from the frozen package) enforces `tenant_operator` cannot supply a different tenant_id. |
| **Fail-closed on nil deps** | All handlers check `deps == nil` first and return 503 `gateway_not_configured` before touching any logic. |
| **Money precision** | Aggregated costs use `numeric(20,8)` in SQL (same as `billing_ledger_claims.actual_cost`); mapped to `shopspring/decimal` in Go via the existing sqlc decimal codec. No float64 intermediary. |
| **No Tx1/Tx2 involvement** | These are analytics reads on already-settled rows (`settled_at IS NOT NULL`). No reserve/settle path touched. |
| **FROZEN packages** | `internal/gatewayhttp`, `internal/gateway`, `internal/proto` gain zero new files. `admin_observability_handler.go` is modified only in `routes.go` wiring (zero-change to the file itself). |
| **Modularity** | Every new file is under ~280 lines; aggregation logic is isolated in `aggregator.go` for independent testability. |

---

## Discriminating tests

Each test is named with the defect it would catch if the production code
regressed. All live in `*_test.go` files within the relevant package.

### `internal/usageanalytics/aggregator_test.go`

1. **`TestAggregateTimeSeries_RejectsFutureSettledAt`** — verifies that rows
   with `settled_at` in the future (clock skew) are excluded from day buckets;
   would fail if the `>= from_ts AND < to_ts` boundary check is inverted.

2. **`TestAggregateTimeSeries_GroupsByDayUTC`** — two rows at 23:59 UTC day T
   and 00:01 UTC day T+1 must land in different day buckets; catches
   timezone-naive `date_trunc` bugs.

3. **`TestAggregateTimeSeries_CostDecimalPrecision`** — sum of two costs
   `0.00000001` + `0.00000001` must equal `0.00000002` (not `0`); catches
   float64 truncation if someone switches from `decimal.Decimal`.

4. **`TestRankSpend_OrderByDescCost`** — given three API keys with costs
   `[0.5, 1.0, 0.25]`, rank must be `[1.0, 0.5, 0.25]`; catches ascending
   sort regression.

5. **`TestRankSpend_LimitEnforced`** — with `limit=2` and 5 rows, output
   length must be exactly 2; catches off-by-one in LIMIT passthrough.

6. **`TestComputeRateWindows_PeakAndAvg`** — minute buckets
   `[10rpm, 5rpm, 7rpm]` must yield `peak=10, avg=7` (integer truncation
   toward zero acceptable); catches mean vs median confusion.

7. **`TestComputeRateWindows_EmptyBuckets`** — zero buckets returns
   `{peak:0, avg:0}` without panic; catches nil-slice dereference.

### `internal/usageanalyticshttp/admin_handler_test.go`

8. **`TestAdminTimeSeries_TenantOperatorCannotCrossScope`** — a
   `tenant_operator` with `ScopeTenantID=7` supplying `?tenant_id=99` must
   receive 403; catches the RBAC bypass if `parseTenantScope` forgets the
   cross-tenant check.

9. **`TestAdminTimeSeries_PlatformAdminNoTenantIDAllowed`** — `platform_admin`
   with no `?tenant_id=` must get 200 with an empty result (not 400); catches
   over-strict validation that breaks cross-tenant admin queries.

10. **`TestAdminTimeSeries_NilDepsReturn503`** — handler with nil deps must
    return 503 `gateway_not_configured`; catches the fail-closed invariant.

11. **`TestAdminSpendRank_LimitCappedAt100`** — supplying `?limit=500` must
    return 400; catches missing upper-bound validation.

12. **`TestAdminRateSummary_MissingFromReturns400`** — omitting `from` must
    return 400; catches missing required-parameter check.

### `internal/usageanalyticshttp/user_handler_test.go`

13. **`TestUserTimeSeries_LockedToOwnAPIKey`** — stub store records which
    `api_key_id` was passed to SQL; must equal `ident.APIKeyID`, not any
    caller-supplied override; catches the self-serve isolation invariant.

14. **`TestUserTimeSeries_CrossKeyParamIgnored`** — even if the caller passes
    `?api_key_id=999` in the query string, the handler must use
    `ident.APIKeyID`; catches query-string injection into the key scope.

15. **`TestUserTimeSeries_AuthBackendError503`** — auth resolver returning
    `auth.ErrAuthBackend` must yield 503 (not 401); catches error-class
    mapping regression.

16. **`TestUserSpendRank_FromToRequired`** — omitting `to` must return 400;
    catches missing validation on the self-serve endpoint (which has tighter
    required params than admin, since admin can do unbounded admin scans but
    user endpoints must require a window to prevent accidental full-history
    scans).

---

## Parity-or-better vs reference

The reference behavior specification describes three analytics capabilities.
The following table maps each reference behavior to the implementation path
in this design.

| Reference behavior | Reference path (behavioral) | HUAKAI implementation | Notes |
|--------------------|-----------------------------|-----------------------|-------|
| Time-series quota by day, broken down by model | `AggregateUsageByDay` query + `AggregateTimeSeries` pure func | `sql/queries/usage_analytics.sql:AggregateUsageByDay` + `internal/usageanalytics/aggregator.go:AggregateTimeSeries` | **Parity.** Adds pooling-group dimension (HUAKAI extension). |
| Time-series quota by group | `AggregateUsageByDay` with `pool_group_id` filter | Same query, `pool_group_id` nullable param | **Parity.** |
| User spend ranking | `RankSpendByAPIKey` query (admin: any key in tenant; user: locked to own key) | `sql/queries/usage_analytics.sql:RankSpendByAPIKey` | **Better:** self-serve endpoint enforces isolation in handler layer, not just SQL. |
| RPM/TPM summaries | `SummariseRPMTPM` query + `ComputeRateWindows` aggregator | `sql/queries/usage_analytics.sql:SummariseRPMTPM` + `internal/usageanalytics/aggregator.go:ComputeRateWindows` | **Parity.** Returns peak+avg in one response vs. reference's separate peak/avg endpoints. |
| Admin cross-user view | Admin endpoints with `tenant_operator`/`platform_admin` RBAC | `internal/usageanalyticshttp/admin_handler.go` | **Parity.** Consistent with existing HUAKAI RBAC model. |
| User self-serve | User endpoints scoped to own API key | `internal/usageanalyticshttp/user_handler.go` | **Better:** structural impossibility of cross-key reads (key ID never taken from query string). |
| Quota snapshot | Reference shows remaining quota alongside usage | `sql/queries/usage_analytics.sql:QuotaWindowSnapshot` | **Better than reference:** exposes `overage_value` for audit visibility, which the reference omits. |

---

## Effort

**L** (Large)

Breakdown:
- Migration + 4 SQL queries + sqlc regeneration: 1 day
- `internal/usageanalytics` (types, repo, service, aggregator): 2 days
- `internal/usageanalyticshttp` (admin + user handlers, tests): 2 days
- `cmd/gateway/routes.go` wiring + integration smoke: 0.5 days
- Review + discriminating test validation: 0.5 days

**Total estimate: ~6 developer-days.**

The main complexity is the RPM/TPM aggregator correctness (UTC bucketing,
decimal precision) and the two-layer RBAC enforcement (admin vs. user).

---

## Risks

1. **Index build time on large `usage_records` tables.** Migration 0077 uses
   `CREATE INDEX CONCURRENTLY` so it does not lock the table, but on a table
   with >100M rows it may run for 10–30 minutes. The down migration also uses
   `DROP INDEX CONCURRENTLY`. **Mitigation:** run in a maintenance window or
   deploy during low-traffic hours; the application continues to function
   (indexes are advisory for performance, not correctness).

2. **RPM/TPM accuracy vs. ingestion lag.** `settled_at` is written in Tx2
   (settlement), not at request arrival. A request with a 10-second streaming
   response appears in the `settled_at` bucket of its completion, not its
   start. This means RPM/TPM from `usage_records` is a _settlement-rate_ metric,
   not an arrival-rate metric. **Mitigation:** document this in the API response
   as `settlement_rpm`/`settlement_tpm`; for arrival-rate metrics a separate
   in-memory counter (outside scope) is needed.

3. **`date_trunc` day bucketing and multi-timezone tenants.** The design uses
   `AT TIME ZONE 'UTC'` consistently, but some tenants may expect local-timezone
   day boundaries. **Mitigation:** add an optional `tz` parameter (IANA tz
   string) to the time-series endpoint in a follow-up slice; for now UTC is
   explicit in the response envelope so the frontend can re-bucket.

4. **`RankSpendByAPIKey` scan cost.** For a tenant with many API keys over a
   long window, the GROUP BY on `usage_records` may be expensive. Index B
   (`idx_ur_analytics_tenant_apikey_cost`) helps for the primary filter but a
   full tenant scan is still O(settled rows). **Mitigation:** enforce a maximum
   window of 31 days on the spend-rank endpoints; reject requests with
   `to - from > 31d` with 400.

5. **sqlc regeneration touching shared `internal/db/billing` package.** Adding
   queries to `sql/queries/usage_analytics.sql` will add generated methods to
   the `billing.Queries` struct. This is safe (additive) but requires the
   worker who implements this to regenerate sqlc and commit the generated files.
   **Mitigation:** note in the implementation ticket that `make sqlc` must be
   run and generated files committed alongside the migration.

6. **`quota_windows` table join for snapshot.** `QuotaWindowSnapshot` joins
   `quota_policies` and `quota_windows`. If a tenant has many policies with
   many historical windows (wide window_end range), the query may return a
   large result set. **Mitigation:** the `WHERE qw.window_end > at_time`
   predicate limits results to currently-active windows; existing index on
   `quota_policies(tenant_id, ...)` covers the primary filter.
