# 2026-06-06 riskctl admin visibility and unban

| Owner directive | "Add risk-control ADMIN visibility + unban to HUAKAI (branch fix/riskctl-admin). Verified partial: moderation config + ban-enforcement done, but no logs/banned-list/unban admin surface. Reach CLOSURE. SECURITY-SENSITIVE (unban re-enables an API key) — admin-gated + audited + scoped. No shortcuts." |
| Scope | In: HUAKAI-native moderation admin routes under `/admin/v1/moderation`, SQL queries in `backend/sql/queries/moderation.sql`, generated `backend/internal/db/moderation`, moderation domain/store/http code, focused tests, and OpenAPI. Out: `/home/ubuntu/refs`, commits, new migrations, integration_pg execution, sockets tests, frozen-package new files. |
| Success criteria | Admin can list moderation logs, list currently moderation-disabled keys, and unban only tenant-scoped moderation-disabled keys; unban writes an audit row; non-admin/cross-tenant/non-moderation keys cannot be enabled; `go build ./...`, `go vet ./...`, focused moderation tests, and `cmd/gateway` tests are attempted locally. |
| Time estimate | 2-4 wall-clock hours; one Codex implementation session. |
| Blast radius | Security-sensitive admin control plane and inbound API-key status. A bad change can leak cross-tenant moderation data or incorrectly re-enable keys. |
| Failure modes | Missing tenant filter: caught by tenant-scoped tests. Unconditional enable: caught by non-moderation and cross-tenant tests. Missing audit: caught by unban audit test. OpenAPI drift: caught by `cmd/gateway` consistency test. Generated sqlc drift: regenerate after SQL edit. |
| Decision points | No Owner sign-off needed for low/medium-risk docs, tests, SQL query additions, generated code, and new handler file in non-frozen `internal/moderationhttp`. Stop before migrations, auth core changes, billing/quota changes, real secrets, frozen-package new files, or commits. |
| Pre-execution checklist | 1. Confirm disable semantics from `internal/moderation/ban_counter.go`, `sql/queries/moderation.sql`, and `api_keys` migration. 2. Add failing tests before production implementation. 3. Add SQL queries and regenerate sqlc. 4. Implement store/domain/http routes with tenant/admin gates. 5. Update OpenAPI paths/schemas. 6. Run local build, vet, and focused tests. |

Concrete execution order:

1. Add discriminating tests in existing `backend/internal/moderationhttp/admin_handlers_test.go` for logs, banned list, unban success audit, non-moderation reject, tenant isolation, and admin auth.
2. Add SQL tests or integration_pg tests in `backend/internal/moderation/moderation_admin_integration_test.go` to exercise real tenant scoping, pagination, and update guards against Postgres.
3. Extend `backend/sql/queries/moderation.sql` with `ListModerationLog`, `ListBannedKeys`, and `EnableModerationAPIKey`; use `moderation_violation_events` evidence because `api_keys` has no disabled-reason column.
4. Run `sqlc generate` from `backend` and keep generated changes scoped to `backend/internal/db/moderation`.
5. Extend moderation domain types and `SQLStore` with list-log/list-banned/unban methods. Unban will write `moderation_log` with `decision='fee_charged'`, `reason_code='admin_unban'`, and a non-sensitive payload hash marker to satisfy the existing no-migration CHECK.
6. Add `internal/moderationhttp` handlers and mount routes under the existing admin gate.
7. Update `docs/openapi/openapi.yaml` for the new public admin routes and response/request schemas.
8. Run the requested local checks, record any unavailable integration_pg/sockets work as PM-run only, and finish with the required Owner report.
