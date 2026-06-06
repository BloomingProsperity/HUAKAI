# 2026-06-06 rerank endpoint slice-1

| Owner directive | "Implement a /v1/rerank relay endpoint for HUAKAI — slice-1 (branch fix/rerank-endpoint)." |
| Scope | Add `backend/internal/rerankhttp` as a new relay+billing package; wire `POST /v1/rerank`; update OpenAPI; add discriminating unit tests. Out of scope: database migrations, reference-project source reading, sockets/integration_pg, commits, provider-specific non-passthrough adapters. |
| Success criteria | `POST /v1/rerank` validates `{model, query, documents, top_n, return_documents}`, resolves/routes/selects like embeddings, reserves deterministic search-unit cost, dispatches to `/v1/rerank`, settles the same cost on success, aborts reserved claims on dispatch/upstream failure, returns model-not-found when registry has no rerank model, and has tests for billing units, happy path, abort, insufficient balance, validation, tenant/auth behavior, route/wiring, and OpenAPI shape. |
| Time estimate | 2-4 hours wall clock in one Codex session; most time in test fixtures and verification. |
| Blast radius | New data-plane money path, route table, timeout allowlist, and OpenAPI contract. Existing chat/embeddings/images/audio should remain behavior-identical because shared billing and dispatch types are reused without modifying their handlers. |
| Failure modes | Underbilling if search units are computed from the wrong field; reservation leak if dispatch/upstream failures do not abort; dispatch to chat/embeddings path if EndpointPath is wrong; model/pricing fail-open if missing registry/rate entries are not treated as unavailable; frozen-package violation if new files are added under `gatewayhttp`, `gateway`, or `proto`. Mitigation: TDD tests with mutation comments, route/wiring tests, and no new files in frozen packages. |
| Decision points | No Owner sign-off needed for docs/tests/new package/route wiring. Stop before high-risk changes: database schema, auth core, billing ledger internals, quota enforcement internals, runtime dependency additions, `LICENSE`, secrets, or destructive shell commands. |
| Pre-execution checklist | 1. Read `docs/RULES.md` and embeddings template. 2. Confirm current branch and dirty worktree. 3. Do not read `/home/ubuntu/refs`. 4. Add no files under frozen packages `backend/internal/gatewayhttp`, `backend/internal/gateway`, `backend/internal/proto`. 5. Write tests before production code. 6. Run targeted tests, `go build ./...`, and `go vet ./...` from `backend` with `GOCACHE=/tmp/go-build` where applicable. |

## Design Notes

`backend/internal/rerankhttp` mirrors `embeddingshttp` at the relay shape: authenticate, validate request, resolve model, route, reserve cost, select account, resolve credential, dispatch upstream, then settle or abort. The endpoint family is `rerank`, the upstream endpoint override is `/v1/rerank`, and the upstream response body is copied back without parsing for billing. Search-unit cost is deterministic from request document count: `max(1, ceil(len(documents)/100))`, resolved through existing rate table JSON aliases and `pricingeval.BillableUnits` with the pool-group pricing ratio.

## File Scope

- Create `backend/internal/rerankhttp/handler.go`: dependencies, execution state, auth/model routing, attempt loop.
- Create `backend/internal/rerankhttp/request.go`: DTO, JSON/body validation, search-unit calculation, payload hash, upstream body reader.
- Create `backend/internal/rerankhttp/pricing.go`: rate table selection and per-search-unit cost calculation.
- Create `backend/internal/rerankhttp/billing.go`: reserve, optional quota reservation, settle request, abort, idempotency, balance mode.
- Create `backend/internal/rerankhttp/attempt.go`: account selection, credential resolution, dispatch, upstream response handling.
- Create `backend/internal/rerankhttp/response.go`: JSON errors and safe header copy.
- Create `backend/internal/rerankhttp/route.go`: registry-to-router model mapping.
- Create `backend/internal/rerankhttp/handler_test.go`: discriminating httptest/unit coverage.
- Modify `backend/cmd/gateway/routes.go`: import package, mount route, add `rerankHandlerDeps`.
- Modify `backend/cmd/gateway/middleware.go`: classify `/v1/rerank` as AI relay for timeout behavior.
- Modify `backend/cmd/gateway/wiring_test.go`: verify rerank shares money-path dependencies.
- Modify `docs/openapi/openapi.yaml`: add path and schemas.

## Execution Order

1. Add rerank tests first and run them to verify RED due to missing handler/package implementation.
2. Implement the minimal `rerankhttp` files needed for validation, routing, deterministic pricing, reserve, dispatch, settle, and abort.
3. Wire gateway route/deps and timeout allowlist; extend the existing wiring test.
4. Update OpenAPI path and schemas.
5. Run `go test ./internal/rerankhttp`, gateway wiring tests, OpenAPI grep checks, then `GOCACHE=/tmp/go-build /usr/local/go/bin/go build ./...` and `GOCACHE=/tmp/go-build /usr/local/go/bin/go vet ./...`.

## Assumptions

- Registered rerank models will use existing registry/router/pool metadata; no schema change is needed.
- A rerank-capable upstream can be reached through an existing OpenAI-compatible passthrough adapter or a tenant-provided passthrough base URL; the handler only overrides the endpoint path.
- Rate table entries may use `search_unit_micro_usd`, `request_micro_usd`, or compatible per-unit aliases. Missing rate table pricing makes the endpoint unavailable instead of free.
- Billing draft token fields remain zero because this slice bills search units, not text tokens; actual cost is still written through the existing settle chain.

## Risks

- Some vendors expose rerank under versioned paths other than `/v1/rerank`; this slice intentionally uses the generic OpenAI-compatible/Jina-style path requested by Owner.
- If a future provider requires body reshaping, it should live in a provider adapter or a later feature-flagged rerank adapter, not in this generic money-path handler.
