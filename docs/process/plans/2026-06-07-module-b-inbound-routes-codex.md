# 2026-06-07 module-b-inbound-routes-codex

| Owner directive | "TASK: 模块B闭环 — 3 个入站路由小闭环(协议广度)... 授权:立即实现... 直接 TDD 实现... 每个新路由必须进 docs/openapi/openapi.yaml, gate 必含 cmd/gateway OpenAPI 一致性测试" |
| Scope | In: additive HUAKAI-internal handlers/tests/routes/OpenAPI for `POST /v1/responses/compact`, `POST /backend-api/codex/responses/compact`, `POST /engines/{model}/embeddings`, and `GET /v1/models/{model}`. Out: reference source reading, runtime dependency additions, schema migrations, auth/billing/quota core changes, commits. |
| Success criteria | Unit tests prove compact `stream:true` returns 400, engines alias injects missing body model from path before the embeddings handler, model detail returns matching model object and `model_not_found` for misses, and cmd/gateway OpenAPI consistency covers all new routes. Requested build/vet/test command completes or failures are reported with evidence. |
| Time estimate | 60-90 minutes wall clock; one Codex session. |
| Blast radius | Gateway route table, new small internal packages, `controlhttp` model read handler, and OpenAPI docs. No frozen-package new files. |
| Failure modes | Alias wrapper could hide canonical pipeline behavior; mitigation: delegate to existing handlers after only request normalization. OpenAPI drift could leave runtime/spec mismatch; mitigation: update `docs/openapi/openapi.yaml` and cmd/gateway route gate. Model detail could diverge from list shape; mitigation: reuse the same local `modelObject` projection helper. |
| Decision points | None during this approved low/medium-risk additive slice. High-risk areas remain untouched: `LICENSE`, secrets, auth core, billing ledger, quota enforcement, DB schema, deployment. |
| Pre-execution checklist | Confirm branch/worktree; inspect existing routes/handlers/tests; write failing tests before production code; verify frozen packages receive no new files; update OpenAPI; run requested checks; leave `integration_pg` to PM as instructed. |

## Concrete Execution Order

1. Add RED tests for `responsescompacthttp`, `engineembeddingsalias`, `controlhttp` model detail, and cmd/gateway OpenAPI route consistency.
2. Run targeted tests to confirm expected missing package/function/route failures.
3. Implement `internal/responsescompacthttp` as a body-normalizing wrapper that rejects `stream:true`, removes non-true `stream`, canonicalizes delegate path, and calls the existing Responses handler.
4. Implement `internal/engineembeddingsalias` as a body-normalizing wrapper that injects the chi `{model}` path value only when the JSON body lacks a non-empty `model`.
5. Add `controlhttp.NewModelGetHandler` in a new file, reusing the list model projection shape and auth/catalog/pricing handling.
6. Mount all routes additively in `cmd/gateway/routes.go`.
7. Update `docs/openapi/openapi.yaml` with all new paths and request/response contracts.
8. Run the requested build, vet, and tests; report `integration_pg` test names for PM.
