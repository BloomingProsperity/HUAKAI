# 2026-06-03 usage-performance codex plan

| Field | Content |
| --- | --- |
| Owner directive | "实现+验证...新增 admin-only GET /v1/admin/usage/performance,按 model 或 provider_account 聚合延迟/吞吐/错误率" |
| Scope | In: add static sqlc performance aggregates, admin handler, admin route, OpenAPI path, focused tests. Out: migrations, money/cost exposure, auth core, billing ledger, quota enforcement, runtime dependencies, git commit. |
| Reference projects in scope | CLIProxyAPI + sub2api + new-api are the mandatory default mirrors. PM stated the source mapping is already verified and supplied; this Codex implementation lane will not read non-MIT reference source and will use only HUAKAI-internal code plus the Owner-provided contract. |
| Success criteria | `GET /v1/admin/usage/performance` is mounted behind `adminGate`; supports `by=model|provider_account`, `window`, `limit`; returns ranked entries with `avg_ttft_ms`, `avg_tps`, `request_count`, and 4-decimal `error_rate`; sqlc generation/build/vet/target tests pass, including OpenAPI consistency under `cmd/gateway`. |
| Time estimate | 45-75 minutes wall clock; one Codex implementation session. |
| Blast radius | Read-only admin analytics path over `usage_records`; generated db/billing code updates may affect compile-time interfaces; OpenAPI path affects contract tests. |
| Failure modes | SQL precision/NULL behavior wrong: cast aggregates to text and parse/format in handler. Divide-by-zero in TPS: use `NULLIF` plus SQL string test. Weak error-rate fixture: seed success+error rows with distinguishable expected rate. Route/OpenAPI drift: update both and run `cmd/gateway` tests. |
| Decision points | None expected. Stop for Owner only if implementation would require schema migration, auth core, billing ledger/quota changes, new dependency, or touching frozen packages. |
| Pre-execution checklist | 1. Read `CLAUDE.md`, `AGENTS.md`, and `docs/RULES.md` Owner Start Gate. 2. Check `.coordination` and claim target files. 3. Inspect leaderboard handler, route, SQL, generated sqlc, OpenAPI segment. 4. Write failing handler/SQL tests first. 5. Implement minimal SQL/handler/route/OpenAPI. 6. Run sqlc generate and verification gate. |

## File Scope

- Create `backend/internal/usageanalyticshttp/performance_handler.go` in non-frozen package `internal/usageanalyticshttp` (currently 4 Go files).
- Create `backend/internal/usageanalyticshttp/performance_handler_test.go` in the same non-frozen package.
- Modify `backend/sql/queries/usage_analytics.sql` to add two SELECT-only sqlc queries.
- Regenerate existing `backend/internal/db/billing/usage_analytics.sql.go` and `backend/internal/db/billing/querier.go`; no new file in `internal/db/billing`.
- Modify existing `backend/internal/db/billing/sql_filters_test.go` for SQL guard coverage.
- Modify `backend/cmd/gateway/routes_usageadmin.go` to mount one admin route.
- Modify `docs/openapi/openapi.yaml` to add the contract path.

## Execution Order

1. Add RED tests for the performance handler using a stub Querier with discriminating rows.
2. Add RED SQL string guards for `FILTER`, success/error classification, request-count ordering, `LIMIT`, and `NULLIF`.
3. Add static sqlc queries and run `sqlc generate`.
4. Implement `NewPerformanceHandler`, query parsing, row adaptation, decimal formatting, and error-rate calculation.
5. Mount `/v1/admin/usage/performance` beside leaderboard.
6. Add the OpenAPI path beside leaderboard.
7. Run targeted tests, then the full requested gate.

## Risk Record

- Money semantics: no `actual_cost` or amount fields are selected or returned.
- Security semantics: endpoint is admin-only by route wiring and read-only SQL; no credentials are selected.
- Clean-room: no reference source read or copied in this lane; implementation follows HUAKAI's existing leaderboard pattern and Owner-provided schema facts.
- Feature preservation: no scope reduction from the requested slice.
