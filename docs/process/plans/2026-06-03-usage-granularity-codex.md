# 2026-06-03 usage granularity

| Owner directive | "实现+验证... GET /v1/me/analytics/time-series ... 补周/月粒度聚合... 默认 day" |
| Scope | In: existing self-serve time-series handler, usage analytics SQL/sqlc output, focused tests, OpenAPI parameter docs. Out: new endpoint, auth changes, quota/billing ledger writes, schema/migration, frozen packages `backend/internal/{gatewayhttp,gateway,proto}`. |
| Reference projects in scope | CLIProxyAPI + sub2api + new-api per project rule. PM supplied the source-verified feature shape for this implementation slice; Codex will not re-read or summarize non-MIT reference source and will make no new reference-project behavior claims. |
| Success criteria | `granularity` accepts `day|week|month`, defaults to `day`, rejects invalid values with 400, keeps tenant/api_key scope from bearer auth identity only, returns week/month buckets from static sqlc queries, OpenAPI documents the parameter, and required gate passes. |
| Time estimate | 45-75 minutes wall clock; one Codex implementation pass plus verification. |
| Blast radius | Read-only analytics endpoint and generated billing query interface. Bad changes could mis-bucket self-serve usage, break OpenAPI consistency, or accidentally widen tenant/api_key scope. |
| Failure modes | Handler ignores `granularity` and always calls day query; SQL uses wrong bucket; generated sqlc types diverge; OpenAPI omits parameter; tests are non-discriminating. Mitigation: red handler test with same-week/two-day fixture, db SQL contract test, sqlc generate, build/vet/test gate. |
| Decision points | None expected. If implementing week/month requires schema/index changes, stop for Owner confirmation; otherwise use two static sqlc queries as directed. |
| Pre-execution checklist | Read `CLAUDE.md` and `AGENTS.md`; check coordination board; claim edited files; inspect existing handler/query/tests/OpenAPI; write failing tests before production code; run sqlc generation and required gate. |

Concrete execution order:

1. Add handler tests proving default day behavior, explicit day/week/month selection, invalid granularity 400, and auth-derived scope remains unchanged.
2. Add db/billing SQL contract tests for day/week/month bucket functions and scope predicates.
3. Add static `AggregateMyUsageByWeek` and `AggregateMyUsageByMonth` sqlc queries, then regenerate sqlc output.
4. Parse `granularity` in the handler and dispatch to the matching query without accepting tenant/api_key from query parameters.
5. Update OpenAPI for `granularity` enum/default and response echo if implemented.
6. Run the required verification gate from `backend/`.
