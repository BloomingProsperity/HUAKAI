# Provider/Channel Catalog Implementation Plan

| Owner directive | "HUAKAI P0 #2 BUILD — provider/channel 目录查询端点... 自主:实现→判别测试→build/test→self-review→commit→push。" |
| Scope | In: read-only `GET /admin/v1/providers` and `GET /admin/v1/channels`, tenant-scoped admin auth, sqlc read queries, chi route wiring, OpenAPI paths/schemas, discriminating tests. Out: schema migrations, secrets/credentials, write endpoints, frontend UI, frozen-package new files, reference-project source reads. |
| Success criteria | Both endpoints require admin auth, enforce tenant scope via `AdminIdentity.CanIssueForTenant`, require `tenant_id` for `platform_admin`, default `tenant_operator` to its scope when omitted, return only whitelisted fields, reject `limit` outside 1-500, apply offset, and pass targeted tests/build/self-review. |
| Time estimate | 2-4 hours wall clock, mostly tests + sqlc/OpenAPI verification. |
| Blast radius | Medium-low: additive admin read endpoints plus existing route/OpenAPI/sqlc generated interface updates. Failure could expose route drift, compile breaks in gateway wiring, or tenant-scope leaks. |
| Failure modes | Weak tenant fixtures could pass despite cross-tenant leakage; whitelist tests may miss secret-like keys; sqlc config drift could omit generated methods; OpenAPI path drift could fail consistency tests; local PostgreSQL may be unavailable for integration-tag tests. |
| Decision points | No further Owner confirmation expected: no schema change, no runtime dependency, no frozen-package new file, no auth/billing/quota core change, no secrets. If a required fix touches high-risk files, stop and ask Owner. |
| Pre-execution checklist | 1. Read `AGENTS.md`/`CLAUDE.md`/`docs/RULES.md`. 2. Confirm branch `work/p0-provider-channel-catalog` and linked worktree. 3. Claim edit files through `.coordination`. 4. Confirm target package `backend/internal/adminhttp` is not frozen. 5. Write RED handler tests before production code. 6. Add sqlc queries only; do not alter migrations/schema. 7. Run sqlc generation, targeted tests, build, integration-tag checks when possible, and pre-commit Codex review. |

## File Scope

- Create `backend/internal/adminhttp/provider_catalog_handler.go` in package `adminhttp` (not frozen): provider list handler only.
- Create `backend/internal/adminhttp/channel_catalog_handler.go` in package `adminhttp` (not frozen): channel list handler only.
- Create `backend/internal/adminhttp/provider_catalog_handler_test.go` and `backend/internal/adminhttp/channel_catalog_handler_test.go` in package `adminhttp` (not frozen): discriminating handler tests.
- Create `backend/sql/queries/admin_provider_channel_catalog.sql`: two read-only sqlc queries; no schema changes.
- Modify `backend/sqlc.yaml`: include the new query file in the admin sqlc group.
- Regenerate `backend/internal/db/admin/*`: generated sqlc output only.
- Modify `backend/cmd/gateway/routes.go`: mount the two endpoints with `d.adminAuth` and `d.adminQueries`.
- Modify `docs/openapi/openapi.yaml`: add the two paths and schemas.
- Modify `backend/cmd/gateway/openapi_consistency_test.go` only if needed to add explicit route/schema guard tests.

Frozen package check: no new files in `backend/internal/gatewayhttp`, `backend/internal/gateway`, or `backend/internal/proto`.

## Execution Order

1. Write RED adminhttp tests for unauthenticated 401, tenant-operator cross-tenant 403 without query calls, platform admin tenant filtering, whitelist/no-secret response shape, limit validation, and offset propagation.
2. Run focused adminhttp tests and confirm RED from missing handlers/types.
3. Add sqlc query file and config entry, then run `sqlc generate` from `backend`.
4. Implement provider and channel handlers with shared local pagination/scope helpers if needed, keeping files focused.
5. Wire `/admin/v1/providers` and `/admin/v1/channels` in `backend/cmd/gateway/routes.go`.
6. Add OpenAPI path and schema definitions for both read-only lists.
7. Run `go test ./internal/adminhttp/...`, `go test ./internal/db/admin/...`, `go test ./cmd/gateway/...`, and `go build ./...` from `backend`.
8. Run the requested integration-tag command with `HUAKAI_DATABASE_URL` if PostgreSQL is reachable; report a truthful skip/blocker otherwise.
9. Perform mutation self-check by temporarily breaking tenant/whitelist/pagination behavior and confirming targeted tests fail, then restore.
10. Stage intended diff, run `timeout 600 codex exec review --uncommitted -m gpt-5.5 -c model_reasoning_effort=xhigh < /dev/null`, fix unresolved S0/S1 if any, commit, and push `origin HEAD:work/p0-provider-channel-catalog`.
