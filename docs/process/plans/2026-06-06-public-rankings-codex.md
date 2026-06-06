# 2026-06-06 Public Rankings Endpoint

| Owner directive | "Add UNAUTH public model-usage rankings endpoint to HUAKAI (branch fix/public-rankings)." |
| Scope | Add `GET /v1/public/rankings`, a new `backend/internal/publicrankinghttp` package, route mount, focused tests, and OpenAPI contract. Do not read `/home/ubuntu/refs`, do not add migrations, do not add files to frozen packages, and do not commit. |
| Success criteria | No-auth request returns public model usage rankings; response projects only model usage volume fields; limit is capped at 100; results are ordered by usage volume descending; OpenAPI consistency remains green. |
| Time estimate | 60-90 minutes wall clock in this session. |
| Blast radius | Public unauthenticated HTTP surface, gateway route registration, OpenAPI path list, and read-only usage analytics query path. |
| Failure modes | Auth accidentally applied: route/handler tests catch non-200 or auth body. Cost or identity leak: projection test scans JSON field names. Limit not capped: cap test checks store argument and response size. Wrong ordering: ordering test uses cost/count-discriminating rows. OpenAPI drift: `cmd/gateway` consistency test catches missing path. |
| Decision points | None expected. High-risk areas are avoided: no schema migration, no auth core, no billing ledger mutation, no quota enforcement change, no new runtime dependency, no `LICENSE` change. |
| Pre-execution checklist | 1. Read `usageanalyticshttp` leaderboard and cache. 2. Read sqlc by-model aggregate shape. 3. Read `cmd/gateway/routes.go` public pricing mount pattern. 4. Write failing tests first. 5. Implement minimal read-only projection. 6. Update OpenAPI. 7. Run targeted tests, full build, vet. 8. Stage `backend/` and `docs/` only. |

## Execution Order

1. Create `backend/internal/publicrankinghttp/handler_test.go` with discriminating no-auth, no-leak, limit-cap, and ordering tests.
2. Run `go test ./internal/publicrankinghttp` and confirm it fails because the package/handler does not exist.
3. Create `backend/internal/publicrankinghttp/handler.go` with a narrow store interface over `AggregateUsageLeaderboardByModel`, a 30s `usageanalyticshttp.GetOrLoad` cache call, limit parsing, post-query ordering by request count, and JSON structs that omit cost and identity fields.
4. Run `go test ./internal/publicrankinghttp` and keep it green.
5. Add route import and top-level `r.Get("/v1/public/rankings", ...)` in `backend/cmd/gateway/routes.go`.
6. Add route-level no-auth mount test in existing `backend/cmd/gateway/models_route_test.go`.
7. Add `/v1/public/rankings` to `docs/openapi/openapi.yaml` with `security: []` and a schema that exposes only model, rank, request_count, token_total, and request_share.
8. Run requested verification commands from `backend/`.
9. Run `git add backend/ docs/` without committing.
