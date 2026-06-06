# 2026-06-06 Subscription Progress Endpoint Codex Plan

| Owner directive | "TASK: Add subscription consumed-vs-cap PROGRESS endpoint (branch fix/sub-progress). Verified real_missing: /me exposes caps only, no consumed/remaining. Reach CLOSURE. Read-only, self-scoped. No shortcuts." |
| --- | --- |
| Scope | Add `GET /v1/users/me/subscriptions/me/progress` as a session-authenticated, self-scoped read endpoint. Modify existing files in `backend/internal/subscriptionhttp`, `backend/internal/quota`, `backend/sql/queries`, generated `backend/internal/db/quota` only for the new read query, `backend/cmd/gateway/routes.go` for wiring, and `docs/openapi/openapi.yaml`. Do not read `/home/ubuntu/refs`. Do not add files to frozen packages `gatewayhttp`, `gateway`, or `proto`. Do not add a migration. Do not commit. |
| Success criteria | Endpoint returns 200 with current active subscription progress rows for the caller's user-scope `cost_usd` daily/weekly/monthly quota policies. Each row includes `window_kind`, `cap`, `consumed=settled+reserved`, `remaining=max(0, cap-consumed)`, `overage`, `request_count`, `window_start`, and `window_end`. No active subscription returns 200 with `subscription: null` and empty progress. Missing session returns 401. OpenAPI declares the public route. |
| Time estimate | 60-90 minutes wall clock, one Codex work unit. |
| Blast radius | Read-only route and quota projection. Possible compile break if sqlc generated types are incomplete; possible routing break if dependency wiring is nil; possible test fragility if time windows are computed with mismatched clocks. |
| Failure modes | Tenant/user leak if the quota query omits tenant or scope predicates; underreported consumption if reserved value is ignored; misleading remaining if negative values are not clamped; false positives if tests use non-discriminating fixtures; generated churn if unrelated sqlc output is reformatted. Mitigate with discriminating tests and a single new query only. |
| Decision points | No high-risk action planned. `cmd/gateway/routes.go` is outside the narrow implementation package list, but is required to pass the quota store into the already-mounted user subscription routes; it is an existing-file, low-risk wiring edit. PM will run `integration_pg` and socket tests. |
| Pre-execution checklist | 1. Confirm current worktree and avoid `/home/ubuntu/refs`. 2. Read `subscriptionhttp` user route and `/me` behavior. 3. Read subscription store mapping from active subscription to quota policies. 4. Read quota store/query/mapping patterns. 5. Add tests before production code and verify RED. 6. Implement minimal route, projection, quota store read method, and OpenAPI. 7. Run requested build/vet/unit commands. 8. Stage `backend/` and `docs/` only; do not commit. |

## Concrete Execution Order

1. Add unit tests in `internal/subscriptionhttp/purchase_test.go` for consumed-vs-cap, self-scope, no-active-subscription, and auth-required behavior. Include mutation comments for each requested scenario.
2. Add a quota store unit/integration-facing test in `internal/quota/pg_store_integration_test.go` for `ListCurrentWindowsForScope` tenant/scope filtering if needed by compile-time coverage. Keep it behind `integration_pg`.
3. Run targeted tests to confirm RED from missing `Quota` dependency, route, and method.
4. Add `quota.CurrentWindowRead` data type and `PGStore.ListCurrentWindowsForScope(ctx, tenantID, scopeKind, scopeID, at)` to `internal/quota`.
5. Add exactly one SQL query to `sql/queries/quota.sql`: read active `cost_usd` policies for one tenant/scope at `at_time`, left join the matching current `quota_windows` row by computed policy window start/end provided by the caller.
6. Run `sqlc generate` and keep only the related generated quota files if generation succeeds without unrelated churn.
7. Add `subscriptionhttp.UserDeps.Quota`, progress DTOs, `newUserSubscriptionProgressHandler`, and `MountSubscriptionUserRoutes` registration for `/me/progress`.
8. Wire `quota.NewPostgresStore(d.pgPool)` into `cmd/gateway/routes.go` user subscription deps.
9. Update `docs/openapi/openapi.yaml` with the new public route and response shape.
10. Run focused tests, then requested verification: `cd backend && GOCACHE=/tmp/go-build /usr/local/go/bin/go build ./...`, `go vet ./...`, and unit tests for `internal/subscription*`, `internal/quota`, and `cmd/gateway`.
11. Stage with `git add backend/ docs/`. Do not commit.

## Understand-First Notes

`MountSubscriptionUserRoutes` currently mounts list, `/me`, plans, cancel-renew, change-plan, and purchase; `/me` uses `SessionIdentity.TenantID/UserID`, calls `ListUserSubscriptions`, picks the active current subscription, and returns only cap snapshots plus `auto_renew`. Subscription activation stores cap limits as enabled user-scope `cost_usd` rows in `quota_policies`, linked through `subscription_policy_links`, with `daily_cap_usd`, `weekly_cap_usd`, and `monthly_cap_usd` mapped to calendar day/week/month window kinds. `quota_windows` stores `reserved_value`, `settled_value`, `overage_value`, and `request_count` per `(tenant_id, policy_id, window_start)`. `quota.ResolvePolicies` already uses `resolvePolicyWindow` and `ComputeWindow` to calculate current UTC window boundaries, so the read projection should reuse that behavior rather than inventing new calendar math. The existing quota read path has only `GetWindowForUpdate`; this task needs a read-only list method scoped by tenant, scope kind, scope id, metric, active validity, and current policy window.

## Risk Record

- Scope support edit: `cmd/gateway/routes.go` must change to wire the quota dependency; otherwise the public endpoint cannot work in production.
- No migration: the endpoint reads existing `quota_policies` and `quota_windows` only.
- Clean-room: HUAKAI-native only; no reference source read.
- Security: self-scoped by session identity and tenant/user predicates; no client-supplied tenant/user accepted.
