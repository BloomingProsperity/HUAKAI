# 2026-06-06 protocol completions count-tokens Codex plan

| Owner directive | "Add two missing relay endpoints to HUAKAI — /v1/completions (legacy) + /v1/messages/count_tokens (branch fix/protocol-completions-counttokens)." |
| Scope | In: HUAKAI-native implementation for `POST /v1/completions`, `POST /v1/messages/count_tokens`, runtime route wiring, OpenAPI entries, unit/wiring tests. Out: reading `/home/ubuntu/refs`, commits, database migrations, sockets, `integration_pg`, new files in frozen packages `backend/internal/{gatewayhttp,gateway,proto}`. |
| Success criteria | Runtime router mounts both POST endpoints; completions relays upstream `/v1/completions` and bills from response usage; count_tokens relays upstream `/v1/messages/count_tokens` and never reserves/settles; discriminating httptest coverage exists for happy path, abort, token billing, insufficient balance, validation, streaming passthrough, auth, and no-billing count_tokens; OpenAPI runtime consistency remains clean; `go build`, `go vet`, focused package tests, and cmd/gateway tests are run or honestly reported if sandbox-blocked. |
| Time estimate | 2-4 hours wall clock in this session; agent time mostly in tracing, TDD red/green, and verification. |
| Blast radius | Public gateway route table, billing reserve/settle chain for legacy completions, provider account selection and credential dispatch, OpenAPI path contract. No database, auth core, billing ledger schema, quota implementation, deployment, or secrets changes. |
| Failure modes | Billing undercharge if completions ignores `usage.completion_tokens`; leak reserved claims if dispatch/upstream failures skip abort; accidental charge for count_tokens; route/docs drift; package-structure violation by adding files to frozen packages; over-strict DTO dropping unknown OpenAI/Anthropic fields. Mitigation: tests assert actual token-derived cost, abort, no dispatch on 402/400, no reserve/settle for count_tokens, OpenAPI consistency, and permissive raw-body passthrough. |
| Decision points | None requiring new Owner confirmation under current directive. High-risk changes are avoided: no migrations, no auth-core changes, no billing-ledger changes, no dependencies, no destructive commands, no real secrets, no `/home/ubuntu/refs` reads. |
| Pre-execution checklist | 1. Confirm isolated worktree and branch. 2. Trace `backend/internal/embeddingshttp/*`, `backend/cmd/gateway/routes.go`, and route dependency builders. 3. Add tests first and observe RED from missing package/routes. 4. Implement only new non-frozen package files plus existing route/wiring/OpenAPI files. 5. Run focused tests after each endpoint. 6. Run final build/vet/focused cmd tests and record exact results. |

## Design Analysis

`backend/internal/embeddingshttp` is the closest local template because it already owns the same gateway relay shape: handler-local auth, registry model resolution, router planning, billing claim reservation, quota reservation, pool selection, credential resolution, dispatcher passthrough, and settlement from upstream-reported usage. The legacy completions handler should keep that flow but change validation from `input` to `prompt`, set `EndpointPath` to `/v1/completions`, and price both `usage.prompt_tokens` and `usage.completion_tokens` through `pricingeval` so the settlement is derived from actual upstream usage instead of a flat value. Streaming completions cannot be fully usage-settled without buffered final usage, so this implementation will forward SSE frames while closing the reserved claim with the best available estimated input charge and a pending reconciliation marker; the required test will assert SSE passthrough and reserve/settle closure rather than pretend exact stream usage is known. `POST /v1/messages/count_tokens` is a utility relay: it shares auth, registry/router, pool selection, credential lookup, and dispatcher path selection, but intentionally does not call `ClaimGate`, `QuotaReserver`, or `Settler`. Both endpoints remain default-inert because failed model resolution returns the same model-not-available behavior as embeddings instead of adding toggles.

## File Scope

- Create `backend/internal/completionshttp/handler.go`: shared deps, request execution, route preparation, attempt loop, public handler constructors.
- Create `backend/internal/completionshttp/request.go`: permissive request parsing, prompt/messages validation, token estimates, body hashing, usage parsing.
- Create `backend/internal/completionshttp/billing.go`: reserve/quota/settle/abort for billed completions only.
- Create `backend/internal/completionshttp/pricing.go`: input+output token price resolution using the existing `pricingeval` fallback model.
- Create `backend/internal/completionshttp/attempt.go`: pool selection, credential resolution, dispatcher calls, upstream response finish paths, streaming passthrough.
- Create `backend/internal/completionshttp/response.go`: JSON error helpers and allowed upstream response headers.
- Create `backend/internal/completionshttp/route.go`: local registry-to-router mapping helper copied by behavior from embeddings.
- Create `backend/internal/completionshttp/handler_test.go`: discriminating httptest tests and stubs for both endpoints.
- Modify `backend/cmd/gateway/routes.go`: import the new package, mount `/v1/completions` and `/v1/messages/count_tokens`, and add `completionsHandlerDeps`.
- Modify `backend/cmd/gateway/wiring_test.go`: assert completions deps reuse the shared money-path wiring.
- Modify `backend/cmd/gateway/openapi_consistency_test.go`: add the two paths to parser/runtime anchors.
- Modify `docs/openapi/openapi.yaml`: add both paths and permissive request/response schemas.

## TDD Execution Order

1. Add `backend/internal/completionshttp/handler_test.go` with the requested completions and count_tokens tests; run `GOCACHE=/tmp/go-build /usr/local/go/bin/go test ./internal/completionshttp` from `backend` and confirm RED due missing package/handlers.
2. Add `backend/cmd/gateway/openapi_consistency_test.go` anchor assertions for the two new paths; run the focused cmd test and confirm RED while routes/OpenAPI are absent.
3. Implement the minimal `completionshttp` package to satisfy validation, dispatch, billing, abort, no-billing count_tokens, and SSE passthrough tests.
4. Wire route imports, mounts, and `completionsHandlerDeps`; update wiring test to include completions money-path reuse.
5. Update OpenAPI paths and schemas.
6. Run focused tests: `go test ./internal/completionshttp`, `go test ./cmd/gateway -run 'TestOpenAPI|TestWiring_PricingRatioResolver'`.
7. Run final requested checks from `backend`: `GOCACHE=/tmp/go-build /usr/local/go/bin/go build ./...`, `GOCACHE=/tmp/go-build /usr/local/go/bin/go vet ./...`, plus focused unit tests for the new package and cmd/gateway.

## Clean-Room And Risk Notes

- No `/home/ubuntu/refs` reads are allowed or needed; implementation uses only HUAKAI local code and the PM-provided behavior summary.
- No non-MIT source, file structure, comments, schemas, or tests are copied from reference projects.
- New source files are in `backend/internal/completionshttp`, which is not a frozen package.
- Existing frozen packages are not modified and receive no new files.
- Billing risk is controlled by tests that fail if actual completion usage is ignored, abort is skipped, insufficient balance still dispatches, or count_tokens charges balance.
