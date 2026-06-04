# 2026-06-04 Audio Endpoints Codex Plan

| Owner directive | "给 HUAKAI 加音频端点 /v1/audio/{speech,transcriptions,translations}(OpenAI 兼容)...不要 git commit(PM 提交)。实现+验证。先读 CLAUDE.md + AGENTS.md。严禁读 /home/ubuntu/refs 或任何外部参考。只读 HUAKAI 自有码。" |
| Scope | In: new `backend/internal/audiopricing` package, new `backend/internal/audiohttp` package, existing passthrough content-type fields in `provider/adapter.go`, `provider/openai/passthrough.go`, `gateway/upstream_dispatcher.go`, and route wiring in `cmd/gateway/routes.go`. Out: pricingeval core changes, database schema, auth/billing ledger/quota enforcement internals, runtime dependencies, commits, external/reference-project reads. |
| Success criteria | Three audio routes mount; TTS reserves and settles deterministic per-rune char cost; transcription/translation multipart rejects invalid bodies before reserve, preserves boundary upstream, bills exact wav duration or conservative size-bound duration, prefers provider duration, marks pending reconciliation when only size-bound is available, aborts failures, and returns 500 without abort on settle backend error. Required build/vet/test gate runs from `backend/`. |
| Time estimate | 3-5 wall-clock hours for code + discriminating tests + gate, depending on existing API drift. |
| Blast radius | Money-path reservation/settlement for new audio endpoints; OpenAI passthrough headers for any endpoint using `InboundContentType`; gateway dispatcher BuildInput plumbing; route registration. Existing chat/embeddings/images should remain behavior-compatible because empty content type defaults to `application/json`. |
| Failure modes | Under-billing compressed audio: mitigate with conservative bytes-to-seconds bound and pending reconciliation. Multipart corruption: mitigate by forwarding captured raw multipart bytes and inbound content type unchanged. Double-close claims: follow embeddings/images reserve/abort/settle lifecycle and assert no hanging claims. PII/content leakage: only body SHA-256, model, chars/seconds/cost enter billing request/draft; tests inspect no transcript/file text in fingerprints. Pricing unavailable zero-charge: audiopricing returns errors when audio rate keys are absent. |
| Decision points | No Owner decision needed during implementation unless a database schema/runtime dependency/core billing change becomes necessary. If compressed-duration policy is insufficient for production accuracy, park deeper decoder/reconciliation worker work for PM review rather than adding a dependency now. |
| Pre-execution checklist | Read `CLAUDE.md`; read `AGENTS.md`; read `embeddingshttp`, `imageshttp`, `imagepricing`, `pricingeval`, OpenAI passthrough, gateway dispatcher, route wiring; coordinate file locks; write this plan; then implement test-first where practical and run the requested gate. |

## Clean-Room Note

AGENTS.md default feature research asks for reference-project reads, but this Owner directive explicitly forbids `/home/ubuntu/refs` and external references for this task. I will not read reference projects and will not make reference-project behavior claims. Implementation is based on the PM spec plus HUAKAI-owned code patterns already read in `backend/internal/{embeddingshttp,imageshttp,imagepricing,pricingeval,provider,gateway}` and `backend/cmd/gateway/routes.go`.

## File Structure

- Create `backend/internal/audiopricing/catalog.go`: rate-table lookup for `pricing_scheme` in `per_char`, `per_second`, `token`; expose `SchemeFor`, `CharMicroUSD`, `SecondMicroUSD`, and token rates for 4o-style usage.
- Create `backend/internal/audiopricing/catalog_test.go`: discriminating rate lookup and missing-rate tests.
- Create `backend/internal/audiohttp/{handler,billing,pricing,request,attempt,response,route,duration}.go`: audio lifecycle, request parsing, bounded multipart handling, duration estimation, pricing, dispatch, response copy.
- Create `backend/internal/audiohttp/handler_test.go`: Owner-specified discriminating tests for TTS, multipart, wav duration, provider duration override, size-bound pending reconciliation, abort/settle/idempotency/group-ratio behavior.
- Modify `backend/internal/provider/adapter.go`: add `BuildInput.InboundContentType string`.
- Modify `backend/internal/gateway/upstream_dispatcher.go`: add `DispatchInput.InboundContentType string` and pass to BuildInput.
- Modify `backend/internal/provider/openai/passthrough.go`: default to `application/json`, but use `InboundContentType` when non-empty.
- Modify provider/gateway tests in existing files only to prove the content-type field is preserved and default behavior remains.
- Modify `backend/cmd/gateway/routes.go`: import `audiohttp`, mount 3 routes, add `audioHandlerDeps`.
- Modify `backend/cmd/gateway/wiring_test.go` if needed to include audio deps in shared pricing ratio verification.

## Execution Order

1. Add audiopricing tests, run them to verify red.
2. Implement audiopricing catalog and rerun package tests.
3. Add provider/gateway multipart content-type tests, verify red.
4. Add minimal `InboundContentType` plumbing and rerun provider/gateway tests.
5. Add audiohttp tests for TTS, wav, provider duration, size-bound, upstream failure, invalid multipart, idempotency, group ratio, settle error, no hanging claims.
6. Implement audiohttp by cloning the embeddings/images lifecycle while keeping files under the package/file structure budget.
7. Wire routes/deps and add/adjust route wiring tests.
8. Run `gofmt`.
9. Run requested gate: `cd backend && (sqlc generate>/dev/null 2>&1||true) && go build ./... && go vet ./... && go test ./internal/audiohttp/... ./internal/audiopricing/... ./internal/pricingeval/... ./internal/provider/... ./internal/gateway/... ./cmd/gateway/... 2>&1 | tail -25`.
10. Report Chinese Owner summary; do not commit.
