# 2026-06-02 provider account test endpoint

| Owner directive | "HUAKAI P0 #5 BUILD — provider account 测试端点。IMPLEMENTER lane... 自主跑完:实现→判别测试→build/test→codex self-review→commit→push。" |
| Scope | Add `POST /admin/v1/provider-accounts/{id}/test` as a dry-run credential validation endpoint. In scope: non-frozen `adminhttp` handler/tests, `credentialworker` dry-run service/tests, minimal existing route wiring, OpenAPI update. Out of scope: schema changes, billing/quota/auth-core changes, background scheduler changes, reference-source reading. |
| Success criteria | Authenticated tenant-scoped operators can test only accounts in their scope; response is `{ok,error_class,message}`; no secret/raw upstream body/credential payload leaks; no refresh success/failure state is persisted; OpenAPI documents dry-run rate-limit caution; `go build ./...`, targeted Go tests, mutation self-checks, Codex review, commit, and push complete. |
| Time estimate | Wall clock 2-4 hours; agent time one implementation session. |
| Blast radius | Admin ops endpoint, credential refresh adapter invocation, route table, OpenAPI contract. If wrong, risks are credential leakage, cross-tenant account probing, or unintended credential health mutation. |
| Failure modes | Cross-tenant access if body/query tenant is trusted: derive tenant only from admin identity and call `GetAdminProviderAccount(id, tenantID)`. Secret leak if raw adapter errors are echoed: map to generic messages only. State mutation if refresher `Refresh` is reused: call adapter directly and skip `SaveRefreshSuccess`/`SaveRefreshFailure`. Weak tests if fixtures are non-discriminating: include secret marker and before/after state controls. |
| Decision points | No Owner sign-off expected unless implementation requires DB schema, auth core, quota/billing, new runtime dependency, deleting files, or adding files to frozen packages. |
| Pre-execution checklist | Read `CLAUDE.md`, `AGENTS.md`, `docs/RULES.md`; check branch and coordination locks; confirm new files are not in `gatewayhttp`, `gateway`, or `proto`; inspect existing admin and refresh patterns; write failing tests before production code; run build/test/review before commit. |

## Execution Order

1. Add `credentialworker` tests for dry-run success, invalid-grant classification, no store save calls, and safe error-class wrapper.
2. Implement `credentialworker.TestProviderAccountCredential` plus exported `ClassifyRefreshErrorClass` wrapper around the internal mode-refresh classifier.
3. Add `adminhttp` handler tests for unauthorized 401, non-tenant scope 403/404, cross-tenant body ignored, no secret leak, dry-run no-state-write via store call counters, invalid-grant mapping, and success.
4. Implement `adminhttp.MountProviderAccountTestRoutes` in a new non-frozen handler file.
5. Wire the handler from existing `backend/cmd/gateway/routes.go` alongside current provider-account admin routes.
6. Update `docs/openapi/openapi.yaml` with `POST /admin/v1/provider-accounts/{id}/test` and response schema, including the upstream rate-limit caution.
7. Run targeted tests and mutation self-checks; restore any intentional mutations.
8. Run `go test ./internal/adminhttp/... ./internal/credentialworker/...` and `go build ./...`.
9. Stage intended diff, run `timeout 600 codex exec review --uncommitted -m gpt-5.5 -c model_reasoning_effort=xhigh < /dev/null`, fix unresolved S0/S1 if any.
10. Commit with root cause and `Rules touched: ...`, then push `origin HEAD:work/p0-account-test`.

## Rule Notes

- Package discipline: new files target `backend/internal/adminhttp` and `backend/internal/credentialworker`, both non-frozen. No new files in `backend/internal/gatewayhttp`, `backend/internal/gateway`, or `backend/internal/proto`.
- Clean-room: this implementer lane uses HUAKAI internal code/spec only; no non-MIT reference source is read.
- Risk: endpoint may consume upstream token endpoint quota, so OpenAPI marks it as operator-only dry-run validation to use sparingly.

## Self-Review Fix Notes

- Codex self-review flagged a P1: refresh-token grant dry-runs can rotate upstream refresh tokens while the local dry-run discards the returned payload. Fix: known rotating refresh-token modes now fail closed before adapter invocation until a non-mutating validator exists.
- Codex self-review flagged a P2: `LoadForRefresh` is scheduler-filtered and hides disabled/unhealthy/static accounts. Fix: added low-risk read-only support in `backend/internal/credentialstore/postgres_store.go` for tenant-scoped provider-account test loading without scheduler eligibility filters.
- Codex self-review round 2 flagged a P1: `ErrNoRefreshRequired` no-op adapters produced false-positive `ok:true`. Fix: modes without a real non-mutating upstream validator now fail closed with safe `operator_config_required`.
- Codex self-review round 2 flagged a P2: multiple testable credential modes were hidden by `LIMIT 1`. Fix: provider-account test loading now counts candidate modes atomically and returns `ErrCredentialAmbiguous` when more than one exists.
- Codex self-review round 3 flagged P2 decrypt handling: corrupt stored credentials returned generic 503. Fix: decrypt/invalid-payload load errors now return safe `{ok:false,error_class:"payload_invalid"}`.
- Codex self-review round 3 flagged P2 auditability: upstream-touching dry-run calls lacked actor/result trace. Fix: handler now writes sanitized `test_provider_account_credential` admin audit events without raw upstream body or credential material.
- Codex self-review round 4 flagged P1: `test_provider_account_credential` was not in the existing `admin_audit_events.action` CHECK. DB schema changes are out of scope/high risk for this task, so the fix reuses the allowed `list_account_credentials` action and marks the exact dry-run operation in sanitized audit payload.
