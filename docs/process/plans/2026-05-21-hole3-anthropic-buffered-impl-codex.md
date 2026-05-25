# 2026-05-21 hole3-anthropic-buffered-impl-codex

| Owner directive | "HUAKAI 方向 1 Phase 2 洞③ —— 实现非流式 Anthropic Messages 响应翻译。" |
| Scope | In: `backend/internal/proto/anthropic/sse.go` buffered Anthropic Messages response parsing, focused response/client shape support needed to preserve those blocks, gateway removal of the non-streaming Anthropic 501 bypass, and tests. Out: DB schema/SQL, runtime dependencies, billing/claim/settle logic, pluggable translator pipeline, validator framework, non-streaming keepalive, and broader loss reporting framework. |
| Success criteria | Tests first show current `ProviderResponseToCanonical` and handler behavior fail; implementation then maps text/tool/thinking/redacted/empty/unknown blocks, usage/cache fields, stop reasons, model, and invalid body errors; `/v1/messages` stream:false no longer returns `buffered_anthropic_not_supported`; required build and full test commands run with real output. |
| Time estimate | 60-90 minutes wall clock; one Codex session. |
| Blast radius | `anthropic_messages` non-streaming response translation and related client serialization; stream path should remain unchanged except for shared helper reuse if needed. |
| Failure modes | Bad JSON or wrong top-level type could be hidden as empty success: tests assert errors. Tool input could pass non-object JSON: normalize to `{}` and emit loss. Missing usage could silently look perfect: emit loss while preserving response. Body over limit could truncate silently: add a typed raw-buffered read overflow check if existing behavior is not explicit. Thinking/redacted blocks could be dropped by client response serialization: add minimal block fields/serialization tests only for this protocol. |
| Decision points | No Owner sign-off needed per directive. Stop only if implementation requires high-risk files (`LICENSE`, DB schema, auth core, billing ledger, quota enforcement, real secrets, destructive migrations) or a new runtime dependency. |
| Pre-execution checklist | 1. Read adapter/gateway/client response context. 2. Add red tests in `backend/internal/proto/anthropic/sse_test.go` and `backend/internal/gatewayhttp/chat_completions_dispatch_test.go`. 3. Run targeted tests and confirm expected failures. 4. Implement buffered translator and small client serialization support. 5. Remove gateway 501 bypass and make raw body overflow explicit. 6. Run targeted tests, then required build and full test commands. |

## Concrete Execution Order

1. Add `ProviderResponseToCanonical` tests for text-only, tool-only, text+tool, thinking signature, redacted thinking, empty content, bad JSON, empty body, wrong `type`, missing usage loss, cache read/create, stop sequence, and unknown/empty block loss behavior.
2. Add a handler regression test for `/v1/messages` with `stream:false` on the raw buffered path (`HUAKAI_DISPATCH_HCSF=0`) returning 200 from a local Anthropic-shaped upstream response.
3. Run targeted tests and record that they fail because the adapter still returns `proto.ErrNotImplemented` and the handler still returns the 501 bypass.
4. Implement Anthropic buffered response parsing in `backend/internal/proto/anthropic/sse.go`, returning a minimal HCSF envelope with `Version`, `BufferedResponse`, `Accounting.Usage`, model-chain upstream value, and non-silent `ProtocolLossEntry` values for missing/normalized data.
5. Extend the canonical block/client response surface only as needed for Anthropic buffered `thinking` signature and `redacted_thinking` preservation.
6. Remove the non-streaming Anthropic 501 bypass from `runAttempt`; keep existing claim/settle paths unchanged.
7. Make `dispatchRawBuffered` detect bodies larger than the 1 MiB cap as an explicit failure instead of accepting a limited prefix.
8. Run targeted tests, then:
   - `GOCACHE=/tmp/go-cache go build -C /home/codex/HUAKAI/backend ./...`
   - `GOCACHE=/tmp/go-cache go test -C /home/codex/HUAKAI/backend ./... -count=1 -timeout 600s`

## Assumptions

- Anthropic Messages response shape follows the public vendor contract named in the directive: top-level `type:"message"`, assistant content blocks, `stop_reason`, optional `stop_sequence`, and `usage`.
- Anthropic `input_tokens` is copied according to Anthropic's contract as the prompt/input token count reported by Anthropic; cache read/create fields are separate usage dimensions and are not subtracted locally.
- Existing billing and settle code will continue to derive usage from `BufferedResponse.Usage`; no billing logic changes are needed.

## Risks

- `CanonicalContentBlock` currently lacks fields for thinking signatures and redacted payload data. A minimal schema extension may be needed; this is a low-risk internal HCSF extension and avoids silently dropping vendor data.
- The current raw buffered read uses `io.LimitReader`; without an explicit overflow check, a too-large JSON body could become a misleading parse error. The plan adds one byte of overflow probing while preserving the 1 MiB cap.
