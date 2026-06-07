# 2026-06-07 W2 composite alertmetrics

| Owner directive | "TASK: W2 — per-tenant composite alerting MetricSource ... No migration (read-only SQL). No shortcuts." |
| Scope | In scope: HUAKAI-internal Go changes only; add a non-frozen alert metric package; add a SELECT-only tenant-scoped recent usage rollup query if absent; wire the scheduler to use the composite source; add discriminating unit tests and optional tagged PG query test. Out of scope: migrations, reference-project reading, auth/billing/quota mutation logic, runtime dependency additions, git commit. |
| Success criteria | `alerting.Scheduler` receives a `MetricSource` whose `Snapshot(ctx, tenantID)` preserves existing expvar global keys and overlays tenant-scoped recent-window usage metrics for the same tenant; DB failures fail soft with globals only; tests prove overlay, globals, tenant ID forwarding, and fail-soft behavior; requested build/vet/test commands pass or failures are reported honestly. |
| Time estimate | 45-75 minutes wall clock; one Codex session. |
| Blast radius | Low-to-medium: alert evaluation input changes and gateway wiring change. SQL is read-only and tenant-scoped; no migrations or write-path changes. |
| Failure modes | SQL codegen drift: run `sqlc generate` if available, otherwise manually keep generated output consistent and report. Cross-tenant leakage: require `WHERE ur.tenant_id = sqlc.arg(tenant_id)::bigint` and a tenant-forwarding unit test. Alert outage on DB down: composite source returns expvar snapshot and logs warning instead of propagating DB error. Frozen package violation: add new code under `backend/internal/alertmetrics`, not frozen packages. |
| Decision points | None expected. Stop for Owner only if sqlc generation requires a new runtime dependency, a migration, or a high-risk billing/quota/auth schema/write-path change. |
| Pre-execution checklist | Confirm branch/status; read `docs/RULES.md` Owner start gate; read scheduler, expvar bridge, usage analytics SQL/generated query file, and gateway wiring; verify no existing recent tenant rollup; create tests before implementation; run focused tests red before production code; implement minimal code; run requested build/vet/test. |

## Concrete Execution Order

1. Add failing unit tests in `backend/internal/alertmetrics/composite_test.go` for overlay values, expvar preservation, tenant ID forwarding, and DB fail-soft behavior. Each test will include the requested mutation comment.
2. Run `cd backend && /usr/local/go/bin/go test ./internal/alertmetrics/...` and confirm the new package fails because production code is missing.
3. Add `backend/internal/alertmetrics/composite.go` implementing `CompositeMetricSource`, constructor, metric key constants/comments, configurable default window, fail-soft DB warning, decimal cost parsing, and tenant-scoped query interface.
4. Add a SELECT-only `RecentUsageRollupByTenant` query to `backend/sql/queries/usage_analytics.sql` because no current query provides tenant-scoped recent success/error/cost totals without API-key filtering.
5. Regenerate or update `backend/internal/db/billing/usage_analytics.sql.go` and `backend/internal/db/billing/querier.go` for the new query.
6. Add a tagged `integration_pg` test in `backend/internal/db/billing` for the query if local fixtures make this practical without migrations.
7. Wire `cmd/gateway/wiring.go` to pass `alertmetrics.NewCompositeMetricSource(alertmetrics.CompositeMetricSourceConfig{GlobalSource: otelbridge.NewExpvarMetricSource(), UsageRolluper: billing.New(pgPool)})` into the alerting scheduler, with nil-safe behavior in the composite.
8. Run focused package tests, then the requested commands:
   - `cd backend && GOCACHE=/tmp/go-build /usr/local/go/bin/go build ./...`
   - `cd backend && /usr/local/go/bin/go vet ./internal/alerting/... ./internal/otelbridge/... ./internal/db/billing/... ./cmd/gateway/...`
   - `cd backend && /usr/local/go/bin/go test -count=1 ./internal/alerting/... ./internal/otelbridge/... ./internal/db/billing/... ./cmd/gateway/...`

## Self-Review

- Frozen package check: no new files in `backend/internal/gatewayhttp`, `backend/internal/gateway`, or `backend/internal/proto`.
- Clean-room check: no reference source or reference behavior claim involved.
- Tenant fence check: query must use a required positive `tenant_id` argument and the composite must pass the exact scheduler tenant ID.
- Test quality check: fixtures distinguish success and error counts so ignoring the body/rollup cannot pass.
