# 2026-06-07 module A AUTH-156 AUTH-157 Codex plan

| Owner directive | "模块A闭环 — 入站 API key 的 per-key 模型白名单(AUTH-156)+ per-key 请求计数速率(AUTH-157)。Branch fix/a-keyctl. HUAKAI-internal,clean-room。全加性、向后兼容" |
| Scope | In: additive `api_keys.allowed_models` migration; user key controls service/store/http get-set APIs; request-count metric option for per-key quota; read-only model allowlist matcher; inbound model gate after request model is parsed. Out: reference-source reading, auth core redesign, quota enforcement changes, billing ledger changes, DB destructive edits, dependency additions, git commit. |
| Success criteria | `allowed_models` is nullable and empty means unrestricted; non-empty allowlist denies unmatched requested models with HTTP 403 before routing/billing; per-key quota defaults to `cost_usd` and can write `requests`; SQL remains tenant/user scoped; unit tests protect matcher and metric selection; integration_pg tests named for PM cover DB set/get and request metric policy; requested build/vet/test command passes locally where environment permits. |
| Time estimate | 60-90 minutes wall-clock; one Codex session. |
| Blast radius | Medium: touches auth identity projection, sqlc-generated DB code, user key controls API, and model-bearing inbound handlers. No high-risk auth credential semantics, billing ledger, quota enforcement, or schema backfill changes. |
| Failure modes | Missing sqlc regeneration causes compile failure; mitigate by running `sqlc generate` or manual generated-code alignment if tool is unavailable. Model gate placed before body validation could break malformed request semantics; mitigate by checking after each handler's existing validation. Per-key metric upsert could overwrite cost policy if metric is still hard-coded; mitigate with a red unit test and SQL string test. New allowlist column might be absent in test DB until migration; PM runs integration after applying migrations. |
| Decision points | No Owner sign-off expected unless implementation requires changing auth core semantics, quota enforcement internals, payment/billing ledger logic, adding runtime dependencies, or destructive migration behavior. |
| Pre-execution checklist | 1. Confirm branch and dirty worktree. 2. Read local migrations `0007`, `0079`, `0085`. 3. Read `internal/userkeycontrols`, `internal/userkeycontrolshttp`, `internal/auth/api_key_resolver.go`. 4. Do not read non-HUAKAI reference source. 5. Add failing tests before production code. 6. Run requested build/vet/test command. |

## Concrete execution order

1. Add `internal/apikeymodelallow` unit tests for unrestricted, exact match, unmatched fail-closed, and whitespace/case normalization.
2. Add userkeycontrols unit tests for `MetricRequests` selection, model allowlist storage/clear, and SQL query shape.
3. Add userkeycontrolshttp tests for `metric` body propagation and `/{id}/model-allowlist` route behavior.
4. Add integration_pg tests `TestSetGetModelAllowlist` and `TestPerKeyRequestMetric`.
5. Implement migration `0086_api_key_model_allowlist` with nullable `allowed_models text`, comment, and down.
6. Add `SetKeyModelAllowlist` / `GetKeyModelAllowlist` types, errors, store methods, SQL queries, and HTTP routes.
7. Extend per-key quota request/http body with optional `metric`; default empty to `cost_usd`, allow only `cost_usd` or `requests`.
8. Extend auth lookup to select `api_keys.allowed_models` and carry it on `auth.Identity`.
9. Call `apikeymodelallow.AllowsCSV` after model-bearing request validation and before route/registry/claim work in model ingress handlers.
10. Run gofmt, sqlc generation if available, then the requested build/vet/test command.

## Clean-room note

This plan uses only HUAKAI-internal code and docs. No reference project source is in scope.
