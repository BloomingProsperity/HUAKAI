# 2026-06-07 B Gemini Native v1beta

| Owner directive | "模块B闭环旗舰 — Gemini 原生入站 /v1beta(generateContent / streamGenerateContent / countTokens)... 授权:立即实现... 直接按 TDD 实现全部范围" |
| Scope | Add Gemini native inbound `/v1beta/models/{model}:{action}` support for `generateContent`, `streamGenerateContent`, and `countTokens`; add a Gemini client adapter; mount and document routes. Out of scope: reading reference-project source, changing auth/billing/quota/database schema, changing `LICENSE`, or adding runtime dependencies. |
| Success criteria | Tests first: Gemini request/response adapter unit test fails before implementation and passes after; Gemini ingress routing test proves stream action selects streaming and generate action selects non-streaming; OpenAPI consistency covers new `/v1beta` routes. Final gate runs `cd backend && GOCACHE=/tmp/go-build /usr/local/go/bin/go build ./... && /usr/local/go/bin/go vet ./internal/proto/... ./internal/geminihttp/... ./cmd/gateway/... && /usr/local/go/bin/go test -count=1 ./internal/proto/... ./internal/geminihttp/... ./cmd/gateway/...`. |
| Time estimate | 2-4 wall-clock hours in this session; largest risk is aligning the new handler with existing gatewayhttp production dispatch without growing frozen packages. |
| Blast radius | New client protocol registration, model path inference, Gemini request/response translation, gateway route table, OpenAPI path consistency. Existing OpenAI/Anthropic routes must remain unchanged. |
| Failure modes | Adapter drops unsupported Gemini fields silently: mitigate with `ProtocolLossEntry` for unmodeled fields. Route parsing ignores `:streamGenerateContent`: mitigate with action-driven tests. Frozen-package drift: only add constants/registry/path mapping and a narrow exported gatewayhttp entry in existing files. OpenAPI drift: add explicit consistency test and YAML paths. |
| Decision points | None needing Owner sign-off under the task authorization. High-risk areas remain untouched: `LICENSE`, real secrets, auth core, billing ledger, quota enforcement, DB schema, deployment scripts. |
| Pre-execution checklist | Read `docs/RULES.md`; read `internal/proto` ClientAdapter and OpenAI Chat adapter patterns; read existing `internal/proto/gemini` upstream helpers; read `cmd/gateway/routes.go`; write RED tests; implement minimal additive code; run requested gate; produce Chinese 8-point summary. |

## Concrete Execution Order

1. Add `internal/proto/gemini` RED tests for Gemini request-to-HCSF and HCSF-to-Gemini response shape.
2. Add `internal/geminihttp` RED routing tests using `httptest` to assert `streamGenerateContent` selects streaming and `generateContent` selects non-streaming.
3. Add `cmd/gateway` RED OpenAPI/route consistency test for `/v1beta/models`, `/v1beta/models/{rest}`, POST and GET.
4. Implement `gemini.GeminiClient` as a `proto.ClientAdapter` in new files under `internal/proto/gemini`.
5. Additive edit frozen `internal/proto` constants, default registry, and ingress path mapping for `ClientProtocolGemini`.
6. Add a narrow exported native-client entry in an existing `gatewayhttp` file so `geminihttp` can reuse production dispatch without rewriting the body.
7. Implement `internal/geminihttp.NewGenerateContentHandler` and a model-list bridge.
8. Mount `/v1beta/models` routes in `cmd/gateway/routes.go` and add OpenAPI YAML paths/schemas.
9. Run targeted RED/GREEN tests as each slice lands, then the full requested gate.

## Assumptions

- The path model is the authoritative Gemini inbound model field for `/v1beta/models/{model}:{action}`.
- `countTokens` is a free utility route like existing `/v1/messages/count_tokens`: authenticated, routed to a provider account, no reserve/settle/quota charge.
- Gemini native request JSON is preserved as raw body for dispatch; no request-body rewrite/shim is used to infer route state.

## Clean-Room Note

No reference-project source is in scope. The implementation uses only this Owner directive, HUAKAI-internal code, and public protocol names present in the directive.
