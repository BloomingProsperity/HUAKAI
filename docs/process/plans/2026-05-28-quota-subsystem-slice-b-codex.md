# 2026-05-28 Quota Subsystem Slice B Codex Plan

| Owner directive | "实现 HUAKAI 配额子系统「切片 B」:sqlc 生成 + pg_store 实现 + store 层真 PG 集成测试。" |
| --- | --- |
| Scope | In: add `quota.sql` to sqlc config, generate `backend/internal/db/quota`, implement `backend/internal/quota/pg_store.go`, add mutation-discriminating `integration_pg` store tests in `backend/internal/quota`. Out: migration 0070 changes, gatewayhttp/gateway/proto changes, billing/quota service orchestration, commits/pushes. |
| Success criteria | `cd backend && make generate` or `sqlc generate` produces quota queries; `cd backend && go build ./internal/quota/...` exits 0; integration tests compile and are runnable with `HUAKAI_DATABASE_URL=... go test -tags=integration_pg ./internal/quota/...`; tests cover the six requested defects with exact positive/negative assertions. |
| Time estimate | 2-4 wall-clock hours, mostly sqlc type adjustment and PG fixture work. |
| Blast radius | Medium. New sqlc package and quota store touch the persistence boundary for quota only. No schema, auth, billing ledger, quota enforcement, or deployment script changes are planned. |
| Failure modes | sqlc cannot infer `jsonb_to_recordset(scopes)` or function return types: adjust query casts/aliases only, not schema. Decimal/pgtype conversions drift: centralize conversion helpers in `pg_store.go`. Tests accidentally pass without checking the intended mutation: each test includes a direct row-count/readback assertion tied to the named defect. PG fixture cleanup misses FK order: use tenant-scoped cleanup in reverse dependency order. |
| Decision points | Stop and request Owner confirmation if migration 0070 must change, if a new runtime dependency appears necessary, if sqlc requires touching a frozen package, or if tests need destructive DB operations outside tenant-scoped cleanup. |
| Pre-execution checklist | 1. Confirm branch and dirty state. 2. Confirm sqlc config/generation path. 3. Confirm `integration_pg` uses `HUAKAI_DATABASE_URL` and `internal/db.Open`. 4. Run sqlc generation before hand-written store mapping. 5. Write failing integration tests before pg_store behavior where feasible; generated code/config is exempt from TDD. 6. Run targeted build; provide PG test command for Owner local run. |

## Concrete Execution Order

1. Modify `backend/sqlc.yaml` to add a new non-frozen sqlc output package `internal/db/quota` with `sql/queries/quota.sql`.
2. Run `cd backend && sqlc generate`; if it fails on query typing, make the smallest query-only adjustment in `backend/sql/queries/quota.sql` and rerun.
3. Add `backend/internal/quota/pg_store_integration_test.go` with `//go:build integration_pg`, `HUAKAI_DATABASE_URL`, `internal/db.Open`, tenant/user/api_key/billing claim fixtures, and the six requested mutation-discriminating tests.
4. Run the new integration package command without `HUAKAI_DATABASE_URL` only to verify build/skip behavior if local PG is unavailable; do not claim PG pass unless a real DSN run succeeds.
5. Implement `backend/internal/quota/pg_store.go` using the generated `internal/db/quota` queries, tenant-scoped params, JSONB scope serialization, decimal/pgtype conversion helpers, and `pgx.ErrNoRows` handling for empty acquire/list paths.
6. Run `gofmt` on touched Go files.
7. Run `cd backend && go build ./internal/quota/...`.
8. Report exact verification state and the command Owner should run for real PG: `cd backend && HUAKAI_DATABASE_URL="$HUAKAI_DATABASE_URL" go test -tags=integration_pg -count=1 ./internal/quota/...`.

## File Structure Check

- Modify: `backend/sqlc.yaml` only to add the quota sqlc package.
- Generated create/update: `backend/internal/db/quota/*.go`; package is new and not frozen.
- Modify if sqlc typing requires: `backend/sql/queries/quota.sql`; no schema changes.
- Create: `backend/internal/quota/pg_store.go`; existing package is small and non-frozen.
- Create: `backend/internal/quota/pg_store_integration_test.go`; test file in the same non-frozen package.

No files are added to frozen packages `backend/internal/gatewayhttp`, `backend/internal/gateway`, or `backend/internal/proto`.
