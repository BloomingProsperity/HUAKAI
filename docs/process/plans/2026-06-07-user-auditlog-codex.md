# 2026-06-07 user-auditlog-codex

| Owner directive | "模块A闭环 — 持久化用户 API-key 自助审计日志(AUTH-194,DB-backed,用户可查自己的 key 签发/吊销历史)。Branch fix/a-auditlog. HUAKAI-internal,clean-room。加性、append-only、tenant+user 围栏。... 含一条加性 migration 0107。" |
| Scope | In: additive migration 0107, new `internal/userauditlog`, new `internal/userauditloghttp`, additive `userkey.Service` sink injection, session-scoped GET `/v1/me/audit-events`, OpenAPI declaration, focused unit and integration_pg tests. Out: reference-project source reads, frozen `gatewayhttp` new files, auth/billing/quota/schema rewrites beyond the additive table, git commit. |
| Success criteria | `logIssue` and `logRevoke` still write slog and also best-effort insert append-only user audit rows; DB rows are tenant+user scoped and contain only key prefix, never plaintext or key_hash; GET `/v1/me/audit-events` returns only the caller's rows with pagination; OpenAPI and runtime route are in sync; requested build/vet/test command passes locally; integration_pg test names are present for PM to run. |
| Time estimate | 60-90 minutes wall clock; one Codex implementation session. |
| Blast radius | Medium. New table and route are additive, but userkey service construction changes and cmd/gateway routing/wiring are on the runtime path. A mistake could block user key issuance if best-effort is not preserved, leak same-tenant user audit rows if `user_id` predicate is missing, or fail OpenAPI route sync tests. |
| Failure modes | Sink error accidentally propagates: guard with unit test using a failing sink. Query only filters tenant: guard with integration_pg same-tenant second-user assertion. Secret persistence: schema and event type have no plaintext/hash fields, tests grep persisted audit fields against issued plaintext/hash. OpenAPI drift: add runtime+spec sync assertion in cmd/gateway tests or rely on existing parser with new path declared. Migration order conflict: verify latest is 0106 before writing 0107. |
| Decision points | Stop for Owner only if a high-risk change becomes necessary: altering auth core, billing/quota enforcement, destructive migration, deleting files, modifying `LICENSE`, adding a new runtime dependency, or changing existing table structures. No such decision is expected. |
| Pre-execution checklist | Confirm branch `fix/a-auditlog`; confirm linked worktree; note unrelated `TASK.md`; read `userkey.go` log contract; read migration 0010 admin audit shape; read `SessionFromContext` and `/v1/me` route patterns; confirm latest migration 0106; write failing tests before production code. |

## Concrete Execution Order

1. Add RED unit test in `internal/userkey` proving a failing audit sink does not fail `Issue`.
2. Add RED HTTP unit tests in `internal/userauditloghttp` for session identity, pagination, and service calls.
3. Add RED integration_pg test `TestPGUserAuditEventsSelfScoped` covering issue+revoke row visibility and same-tenant second-user isolation.
4. Add migration `backend/sql/migrations/0107_user_audit_events.up.sql` and `.down.sql` with append-only table, CHECK enums, FK references, and `(tenant_id, user_id, occurred_at DESC)` index.
5. Create `internal/userauditlog` with event model, `UserAuditSink`, noop sink, PG insert sink, list query with mandatory `(tenant_id, user_id)`, and small pagination bounds.
6. Modify existing `internal/userkey/userkey.go` only: add sink field/options, preserve `NewService`, add `SetAuditSink` or option-style injection, call sink from `logIssue/logRevoke` best-effort with `context.Background()`, and keep slog fields.
7. Create `internal/userauditloghttp` with GET handler mounted under `/v1/me/audit-events`; session identity comes only from `SessionFromContext`, not query/body.
8. Add gateway wiring in existing files: construct PG sink in `cmd/gateway/wiring.go`, store it on deps, and mount handler in the existing `/v1/me` session group in `cmd/gateway/routes.go`.
9. Update `docs/openapi/openapi.yaml` for GET `/v1/me/audit-events` with `tags: [user-audit]` and response schemas.
10. Run targeted tests for new packages and userkey; fix failures.
11. Run requested gate:
    `cd backend && GOCACHE=/tmp/go-build /usr/local/go/bin/go build ./... && /usr/local/go/bin/go vet ./internal/userauditlog/... ./internal/userauditloghttp/... ./internal/userkey/... ./cmd/gateway/... && /usr/local/go/bin/go test -count=1 ./internal/userauditlog/... ./internal/userauditloghttp/... ./internal/userkey/... ./cmd/gateway/...`
12. Report final Chinese 8-point summary and name `TestPGUserAuditEventsSelfScoped`; do not git commit.

## Clean-Room Note

This is HUAKAI-internal work. The plan intentionally does not read non-MIT reference source. The requested new-api alignment is treated as product behavior vocabulary: a user-facing operation-log endpoint for the caller's own key-management events.
