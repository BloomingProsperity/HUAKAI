# 2026-06-07 Codex Responses Ingress

| Owner directive | "TASK: Add inbound Codex-CLI ingress /backend-api/codex/responses (branch fix/codex-responses-in). HUAKAI is an AI relay gateway; this lets Codex CLI clients hit HUAKAI natively. Verified real_missing (404 today). EDIT-ONLY, reuse existing Responses pipeline. No shortcuts." |
| Scope | In: add `/backend-api/codex/responses` as an inbound route, map it to `openai_responses`, add discriminating tests, document the public path in `docs/openapi/openapi.yaml`. Out: no reference-source reads, no `/backend-api/codex/completions` unless confirmed, no normalizer unless request body diverges, no migrations, no commits. |
| Success criteria | `POST /backend-api/codex/responses` reaches the same Responses handler and billing/settle path as `/v1/responses`; protocol resolution returns `ClientProtocolOpenAIResponses`; auth remains required; OpenAPI includes the new path; requested Go build/vet/package tests run or any blockers are reported honestly. |
| Time estimate | 30-45 minutes wall clock, one Codex execution slice. |
| Blast radius | Low-to-medium: route table, path-to-protocol resolver, handler tests, OpenAPI docs. Frozen packages are edited only in existing files; no new files in `gatewayhttp`, `gateway`, or `proto`. |
| Failure modes | Wrong protocol mapping could silently route to another adapter; tests assert exact protocol. Missing route would keep 404; httptest covers the chi route. Billing regression could bypass settlement; tests assert settle path. Auth bypass could expose public ingress; test asserts missing auth is rejected. OpenAPI drift could hide the route from clients; docs path is updated. |
| Decision points | Owner confirmation would be needed only if Codex CLI body diverged from OpenAI Responses and required a new normalizer, or if adding `/backend-api/codex/completions` became necessary. Current evidence says neither is needed. |
| Pre-execution checklist | Read `backend/cmd/gateway/routes.go`, `backend/internal/gatewayhttp/chat_completions_handler.go`, `backend/internal/proto/client_adapter_default_registry.go`, `backend/internal/proto/openai_responses_request.go`, existing Responses handler tests, and OpenAPI `/v1/responses` section; confirm no `/home/ubuntu/refs` reads; keep edits scoped. |

## Concrete Execution Order

1. Add failing tests first:
   - `backend/internal/proto/client_adapter_default_registry_test.go`: assert `/backend-api/codex/responses` resolves to `ClientProtocolOpenAIResponses`.
   - `backend/internal/gatewayhttp/chat_completions_dispatch_test.go`: assert the Codex path reaches the Responses pipeline, records `openai_responses` metadata, settles billing, and requires auth.
   - `backend/cmd/gateway/routes_test.go` if existing route-level tests are available; otherwise add to the smallest existing route test file without creating a frozen-package file.
2. Run targeted tests to confirm RED where practical.
3. Edit existing implementation files only:
   - `backend/internal/proto/client_adapter_default_registry.go`: add `/backend-api/codex/responses` to the Responses case.
   - `backend/cmd/gateway/routes.go`: mount `r.Post("/backend-api/codex/responses", gatewayhttp.NewResponsesHandler(chatHandlerDeps(d)))`.
   - Do not add a normalizer because the existing `OpenAIResponsesClient` already parses `instructions`, `input`, `store`, and `stream`.
4. Update `docs/openapi/openapi.yaml` with `/backend-api/codex/responses`, reusing the `/v1/responses` request/response schema.
5. Run targeted tests, then requested build/vet commands as far as the local environment allows.
6. Stage requested paths with `git add backend/ docs/`; do not commit.

