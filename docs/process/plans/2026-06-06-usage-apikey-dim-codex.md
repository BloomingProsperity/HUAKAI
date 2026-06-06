# 2026-06-06 usage api-key dimension
| Owner directive | "Add api-key DIMENSION to admin usage analytics (branch fix/usage-apikey-dim)." |
| Scope | Add `by=api_key` to the existing admin usage leaderboard route. Modify only `backend/internal/usageanalyticshttp`, `backend/internal/db/billing`, and `backend/sql/queries` plus this plan. No new route, no migration, no reference-source reads, no commit. |
| Success criteria | `GET /v1/admin/usage/leaderboard?by=api_key` is accepted, dispatches to a new read-only query grouped by `usage_records.api_key_id`, returns cost/token/request buckets, and invalid `by` values still return 400. SQL and handler tests cover grouping, validation, window/sort/limit, and tenant-scope filtering via optional `tenant_id`. |
| Time estimate | 45-75 minutes wall clock in this Codex session. |
| Blast radius | Admin usage analytics handler/interface and generated billing query surface. No gateway hot path, auth core, billing ledger writes, quota enforcement, schema, or route shape changes. |
| Failure modes | Missing sqlc surface breaks build; mitigate by updating query SQL, generated Go, and querier interface together. Weak test could pass if grouped by user; mitigate with same user owning multiple API keys. Dropped tenant predicate could leak cross-tenant rows if this query is scoped; mitigate with SQL filter checks if a tenant argument is introduced. Cache aliasing could reuse other dimensions; existing cache key includes `by` and will cover `api_key`. |
| Decision points | Performance `by=api_key` is optional in the task; default decision is not to add it unless required by compilation or local patterns. Existing admin leaderboard queries are platform-wide and `adminGate` does not pass identity into handlers, so `api_key` keeps tenant_id=0 as the global default and supports a positive optional `tenant_id` for tenant-focused drilldown without touching auth core. |
| Pre-execution checklist | Read `leaderboard_handler.go`, `usage_analytics.sql.go`, `usage_analytics.sql`, route mount, tests, and migration table definition; confirm `usage_records.api_key_id` exists; write RED tests; implement SQL and handler; run focused package tests, `go build ./...`, `go vet ./...`, and requested package tests. |

## Concrete Execution Order

1. Add failing handler tests for `by=api_key` aggregation and validation, using same-user different-key fixtures so `GROUP BY user_id` is caught.
2. Add SQL filter coverage for the new query's `api_key_id` grouping, window, spend ordering, and limit.
3. Add `AggregateUsageLeaderboardByApiKey` to `sql/queries/usage_analytics.sql`.
4. Mirror sqlc-generated code in `internal/db/billing/usage_analytics.sql.go` and `querier.go`.
5. Wire `leaderboardByApiKey` through the usage analytics HTTP `Querier`, validation, switch, and row adapter.
6. Run focused tests and requested build/vet/unit checks; report any command that cannot be run locally.
