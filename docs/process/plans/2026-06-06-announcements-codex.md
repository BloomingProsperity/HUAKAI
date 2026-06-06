# 2026-06-06 announcements Codex plan
| Owner directive | "TASK: Add ANNOUNCEMENTS feature to HUAKAI (branch fix/announcements). Verified real_missing (no announcement anywhere). Reach CLOSURE: admin CRUD + user fetch + migration + tests + gate-ready. No shortcuts." |
| Scope | In: HUAKAI-native announcement migration 0102, `internal/announcement`, `internal/announcementhttp`, gateway wiring, OpenAPI paths/schemas, focused unit tests, integration_pg migration/store tests. Out: reading `/home/ubuntu/refs`, frontend UI, commits, production deployment, integration_pg/socket runs reserved for PM. |
| Success criteria | `GET /v1/announcements` returns tenant-scoped active, published, non-expired announcements ordered by published_at desc; admin can create/list/update/delete with tenant scope and validation; OpenAPI lists all new paths; migration up/down works; local build/vet/focused tests run with reported evidence. |
| Time estimate | 2-4 hours wall clock in one Codex session; PM integration_pg/mutation run separate. |
| Blast radius | New DB table and new routes only; low routing risk in `cmd/gateway`; no frozen package new files; no auth/quota/billing schema changes beyond announcements table. |
| Failure modes | Route mounted without OpenAPI entry -> existing consistency test fails, mitigated by adding all paths. Tenant filter bug -> cross-tenant leak, mitigated by discriminating tests and SQL predicate. Expiry/active/published filter bug -> stale/inactive/future announcements leak, mitigated by integration test. Admin auth confusion -> non-admin mutation allowed, mitigated by handler test using tenant operator/platform role and forbidden role. Migration rollback broken -> table remains, mitigated by integration_pg migration test. |
| Decision points | No high-risk file changes planned. If implementation requires auth core, billing/quota, production secrets, destructive migration, or new runtime dependency, stop for Owner confirmation. |
| Pre-execution checklist | Confirm isolated branch/worktree; read `backend/internal/controlhttp/notify_handler.go`, `backend/cmd/gateway/routes.go`, `backend/cmd/gateway/routes_notifications.go`, recent migration pair 0100/0101, deps wiring/admin auth/panelauth context; verify latest migration number 0101; avoid `/home/ubuntu/refs`; write tests before production code; keep new code out of frozen packages. |

## Concrete execution order

1. Write failing unit tests in `backend/internal/announcement` for validation and in-memory service behavior.
2. Write failing HTTP tests in `backend/internal/announcementhttp` for user active-only, tenant-scoped listing, admin CRUD, validation, and admin auth required, with mutation comments.
3. Write integration_pg tests in `backend/internal/announcement` for Postgres store filters and `TestMigration0102`.
4. Run focused tests to confirm RED due missing packages/symbols.
5. Add migration files `backend/sql/migrations/0102_announcements.up.sql` and `.down.sql`.
6. Implement `internal/announcement` types, validation, service, in-memory store for unit tests, and Postgres store.
7. Implement `internal/announcementhttp` handlers with JSON error shape, session tenant scope for users, admin resolver scope for CRUD, pagination, and response mapping.
8. Wire service into `backend/cmd/gateway/wiring.go` and mount routes from gateway routing.
9. Update `docs/openapi/openapi.yaml` with `/v1/announcements`, `/v1/admin/announcements`, and `/v1/admin/announcements/{id}` plus schemas.
10. Run gofmt.
11. Run focused tests for `internal/announcement`, `internal/announcementhttp`, and `cmd/gateway`.
12. Run `cd backend && GOCACHE=/tmp/go-build /usr/local/go/bin/go build ./...` and `go vet ./...`.
13. Report exact files changed, verification evidence, risks, clean-room status, and Owner follow-ups in Chinese.
