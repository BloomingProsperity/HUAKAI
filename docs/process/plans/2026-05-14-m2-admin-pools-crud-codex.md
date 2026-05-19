# 2026-05-14 M2 admin pools CRUD Codex plan

| Owner directive | "HUAKAI M2 — /admin/v1/pools CRUD 真接入 (消除 4 个 501 stub)。" |
| Scope | In: pool_groups admin CRUD SQL, sqlc generated methods, gatewayhttp pools handler, focused handler tests, main route mount, /tmp evidence. Out: pool_accounts behavior, credentialworker, hcsf_graph_marshal, trust-chain, R-3 mimicry, database schema changes, LICENSE, production secrets. |
| Success criteria | `/admin/v1/pools` supports list/create/get/patch with admin auth; name validation enforces required 1-64 chars; duplicate active pool name maps to 409; not found maps to 404; unauthorized maps to 401; the four 501 stubs are gone; `go test ./internal/gatewayhttp/...` and `go vet ./internal/gatewayhttp/...` pass from `backend/`. |
| Time estimate | 45-75 minutes wall clock; one Codex implementation pass plus sqlc/test iteration. |
| Blast radius | Admin-only pool group management and generated db query interface. Gateway hot request path, billing, quota, auth core, credential refresh, schema, and provider routing should remain untouched. |
| Failure modes | sqlc query shape may produce awkward nullable params: use simple typed params and targeted handler mapping. Existing admin audit enum may not include pool actions: avoid adding pool audit writes in this slice instead of changing migration/schema. Route path mismatch could regress Q1 pool-accounts: only replace `/admin/v1/pools`, do not touch `/v1/admin/pool-accounts`. |
| Decision points | No high-risk Owner sign-off expected. If sqlc generation forces more than the requested 5 touched files, keep the implementation source files bounded and record generated-file expansion explicitly. If a schema change appears necessary, stop before editing migrations. |
| Pre-execution checklist | 1. Read rules and current admin handler/auth patterns. 2. Inspect pool_groups schema and sqlc config. 3. Add pool_groups SQL only against existing schema. 4. Generate sqlc code. 5. Implement handler with minimal store/auth interfaces. 6. Add >=8 unit tests with stubs. 7. Replace the four notImplemented routes with a mounted handler. 8. Run requested checks. 9. Write `/tmp/codex-m2-pool-admin-final.txt`. |

Concrete execution order:

1. Add `backend/sql/queries/pools.sql` with `InsertPool`, `GetPool`, `ListPools`, `UpdatePool`, and `DeletePool`.
2. Run `sqlc generate` from `backend/` and inspect generated Go names/params.
3. Add `backend/internal/gatewayhttp/admin_pools_handler.go`, reusing the admin resolver pattern from `admin_pool_accounts_handler.go` while keeping dependency interfaces local.
4. Add `backend/internal/gatewayhttp/admin_pools_handler_test.go` with success and failure cases.
5. Change `backend/cmd/gateway/main.go` to mount `gatewayhttp.NewAdminPoolsHandler(...)` at `/admin/v1/pools`.
6. Run gofmt, `go test ./internal/gatewayhttp/...`, and `go vet ./internal/gatewayhttp/...`.
7. Append progress/evidence to `/tmp/codex-m2-pool-admin.txt` and write the final evidence file.
