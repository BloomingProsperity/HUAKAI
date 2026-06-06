# 2026-06-06 api-key usage summary
| Owner directive | "TASK: Add per-api-key usage SUMMARY endpoint (branch fix/apikey-usage-summary)." |
| Scope | Add `GET /v1/me/keys/{id}/usage-summary` as a session-authenticated self-serve read-only endpoint. Modify `backend/internal/usageanalyticshttp`, `backend/internal/db/billing`, `backend/sql/queries/usage_analytics.sql`, `backend/cmd/gateway/routes.go`, and `docs/openapi/openapi.yaml`. No reference-source reads, no migration, no new files in frozen packages, no commit. |
| Success criteria | A session user can request totals for one owned API key and receives `{api_key_id,total_cost,total_tokens_input,total_tokens_output,request_count,from,to}`. Foreign-user and cross-tenant key IDs return 404 via `userkey.Service.Get`, unauthenticated requests return 401, and the SQL totals remain tenant- and api-key-scoped. |
| Time estimate | 60-90 minutes wall clock in this Codex session; PM runs `integration_pg` and mutation checks separately. |
| Blast radius | New read-only handler route, generated sqlc query surface, OpenAPI docs, and focused tests. No gateway hot path, auth core, billing ledger writes, quota enforcement, database schema, deployment scripts, secrets, or production credentials. |
| Failure modes | Missing ownership check would leak another user's key totals; mitigate with handler tests that expect 404 when `UserKeyService.Get` returns `ErrNotFound`. Dropping `api_key_id` or `tenant_id` in SQL would broaden aggregation; mitigate with SQL string tests and store-argument assertions. Session-route mis-mount could allow bearer-only auth; mitigate by mounting inside the existing `/v1/me` `SessionMiddleware` block and adding a route/auth test where feasible. Generated sqlc drift could break build; mitigate by updating only the new query's generated surface and running focused tests plus build/vet. |
| Decision points | The prompt explicitly authorizes the design and asks to reach closure; this plan records that approval assumption and proceeds without reading `/home/ubuntu/refs`. `from`/`to` are optional for this totals endpoint, unlike the existing time-series route; omitted bounds mean full settled history for the already ownership-verified key. |
| Pre-execution checklist | Confirm `usageanalyticshttp` currently scopes time-series to `ident.APIKeyID`; confirm `userkey.Service.Get(ctx, tenantID, userID, apiKeyID)` enforces ownership and maps non-owned keys to `ErrNotFound`; confirm `/v1/me` block uses `auth.SessionMiddleware`; write RED tests first; add minimal SQL query and generated sqlc Go; wire handler and route; update OpenAPI; run requested checks; stage `backend/` and `docs/`. |

## Concrete Execution Order

1. Add failing unit tests in `backend/internal/usageanalyticshttp` for ownership 404, totals, auth-required behavior, and SQL tenant/api-key scoping.
2. Run the focused package test to confirm the new tests fail because the endpoint/query surface does not exist yet.
3. Add `AggregateMyUsageTotals` to `backend/sql/queries/usage_analytics.sql`.
4. Update generated sqlc files minimally for the new query in `backend/internal/db/billing/usage_analytics.sql.go` and `backend/internal/db/billing/querier.go`.
5. Implement the new session-based handler in `backend/internal/usageanalyticshttp`, using `userkey.Service.Get`-compatible ownership interface before querying totals.
6. Mount the route under the existing `/v1/me` session middleware in `backend/cmd/gateway/routes.go`.
7. Add the public route contract to `docs/openapi/openapi.yaml`.
8. Run focused tests, `go build ./...`, `go vet ./...`, then stage `backend/` and `docs/` without committing.
