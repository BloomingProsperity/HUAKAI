# 2026-06-03 generation-endpoint-codex

| Owner directive | "实现+验证。先读 CLAUDE.md + AGENTS.md。目标(openrouter 兼容:GET /v1/generation?id=<request_id>):按 request_id 返回调用方自己某一次请求的用量明细(成本/token/计时)" |
| Scope | Backend only. Add `GET /v1/generation` as a self-scoped usage lookup by `request_id`; add a sqlc SELECT beside `ListUsageRecords`; add handler in non-frozen `backend/internal/meusagehttp`; wire route in `backend/cmd/gateway/routes.go`; update `docs/openapi/openapi.yaml`; add discriminating tests. Out of scope: schema/migration, billing/quota/auth core mutation, external reference source reading, new runtime dependencies, commits. |
| Success criteria | Authenticated user A gets 200 for A's `request_id`; user A gets 404 for user B's `request_id`; missing/nonexistent ID gets 400/404 as appropriate; response uses the existing `/v1/me/usage` projection only; OpenAPI implementation consistency is green; requested backend gate passes or any failure is reported honestly. |
| Time estimate | 60-90 minutes wall clock; one Codex implementation lane. |
| Blast radius | Read-only usage lookup path, sqlc generated query surface, public route table, OpenAPI spec. If wrong, risk is cross-user usage disclosure or contract drift; mitigated by `tenant_id` + `user_id` query predicates and route/spec tests. |
| Failure modes | Missing `user_id` predicate could leak another user's request: covered by A-vs-B discriminating test. Divergent projection could expose internal cost fields: mitigated by reusing `mapUsageRecord`. SQLC generation may alter generated files: limited to billing query generated code. OpenAPI route drift could fail gateway consistency: update spec in same slice. |
| Decision points | None requiring new Owner approval if implementation stays read-only and self-scoped. Stop if a schema migration, auth-core change, billing-ledger mutation, quota-enforcement change, new dependency, or frozen-package new file becomes necessary. |
| Pre-execution checklist | 1. Read `CLAUDE.md`, `AGENTS.md`, `docs/RULES.md`, `.coordination/README.md`. 2. Inspect `/v1/me/usage` handler, tests, route, query, and OpenAPI schema. 3. Claim shared files through `.coordination`. 4. Write failing tests before production code. 5. Generate sqlc and run requested gate. |

## Reference Projects In Scope

- Required default mirrors listed for rule compliance: CLIProxyAPI + sub2api + new-api.
- This Codex implementation lane does not read external reference source and does not make new reference-project behavior claims. The task uses the PM-supplied OpenRouter compatibility requirement plus HUAKAI internal code evidence only.

## Concrete Execution Order

1. Add failing `meusagehttp` tests for `GET /v1/generation`: own request 200, other-user request 404, nonexistent request 404, and missing `id` 400. Add a billing SQL filter test proving the query contains request, tenant, and user predicates.
2. Run targeted test and confirm the failure is the missing handler/query surface.
3. Add `GetUsageRecordByRequestID` to `backend/sql/queries/observability.sql` using the same selected columns and `blc.logical_request_id` source as `ListUsageRecords`, filtered by `ur.tenant_id`, `ur.user_id`, and request ID.
4. Run `sqlc generate` to update generated billing query files.
5. Add `backend/internal/meusagehttp/generation_handler.go`; parse bearer auth through existing `AuthResolver`, require non-empty `id`, call the new query, map `pgx.ErrNoRows` to 404, and reuse `mapUsageRecord`.
6. Wire `GET /v1/generation` in `backend/cmd/gateway/routes.go` with the same inbound auth and billing query deps.
7. Add OpenAPI path declaration for `/v1/generation` and reuse `MeUsageRecord` as the 200 schema.
8. Run targeted red/green tests, then the requested verification gate.
