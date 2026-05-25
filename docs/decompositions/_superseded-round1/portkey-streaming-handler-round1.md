# Portkey | Streaming Response Handler

| Field | Value |
| --- | --- |
| Status | Draft |
| Reference | Portkey AI Gateway (MIT, [E-LIC-006](../../07_REFERENCE_EVIDENCE_LEDGER.md)) |
| Feature in HUAKAI matrix | [F-GW-002](../../03_FEATURE_PARITY_MATRIX.md) (L1) |
| Evidence ledger row | E-LIC-006; E-PK-001; E-PK-002; E-PK-007 |
| Specifier session | Codex specifier-lane, 2026-04-29 |
| Specifier date | 2026-04-29 |
| Reviewer session | TBD |
| Reviewer date | TBD |
| Source files read | https://github.com/Portkey-AI/gateway/blob/main/src/handlers/responseHandlers.ts |
| Source files read | https://github.com/Portkey-AI/gateway/blob/main/src/handlers/streamHandler.ts |
| Source files read | https://github.com/Portkey-AI/gateway/blob/main/src/utils.ts |
| Source files read | https://github.com/Portkey-AI/gateway/blob/main/src/providers/google-vertex-ai/chatComplete.ts |

## 1. WHY

Portkey optimizes streaming for a different runtime pressure than Sub2API. Sub2API's decomposition is Go-style, line-oriented, and strongly tied to `ResponseWriter` flush behavior. Portkey runs in JavaScript runtimes including Hono and edge workers, so the design pressure is to normalize several upstream stream shapes into one client-facing stream while keeping the hot path small enough for low overhead. Public docs and source summaries indicate support for SSE, JSON-line style streams, and binary Provider streams, with a stream transform sitting after Provider selection and retry connection setup.

The motivation is client compatibility: users expect OpenAI-compatible streaming even when the selected Provider speaks another event protocol. A second motivation is routing reliability. Retry and fallback happen before a client-visible stream is consumed; once bytes have reached the client, Portkey treats the stream as non-replayable and closes cleanly on processing failure instead of attempting mid-stream failover. This is a practical design for edge runtimes but leaves HUAKAI accounting gaps to close.

## 2. WHAT

The handler first classifies the Provider response by request streaming intent, success status, content type, Provider family, and whether the data came from cache. Only successful upstream responses enter the streaming path; pre-stream error responses are handled by the normal error and retry machinery.

For native streaming, the gateway creates a pair of connected stream endpoints: one side receives upstream events and one side is returned to the client. A Provider-specific delimiter policy decides whether the upstream is split as SSE event groups, newline-delimited JSON objects, or a binary Provider event stream. Text protocols are decoded into a rolling buffer until a complete event boundary is found. Binary Provider events are decoded into JSON-like payloads before entering the same transformation step.

Each complete upstream event is transformed independently. The transform may pass an event through, convert a Provider-native event to an OpenAI-compatible SSE event, or use a small per-stream state object to keep cross-event context such as content block indexing. After transformation, the event is written immediately to the downstream stream. In worker-style runtimes this write is the flush boundary: Portkey does not wait for the whole response before making the next event visible to the client.

End-of-stream detection is protocol-specific. OpenAI-style streams rely on the usual terminal sentinel. Cached JSON converted to a stream synthesizes initial metadata, content deltas, a final finish event, and a terminal sentinel. Google-style Provider events can carry usage metadata in later or terminal chunks; those values are mapped into the normalized event. Anthropic-style message streams use message lifecycle events, where the final delta and stop event indicate completion. Binary Provider streams are decoded event-by-event and stop when upstream read completes.

Portkey includes a small latency smoothing rule for at least one Provider family: the first event can be delayed by a short fixed amount and later events by a much smaller delay. This is not a general latency budget model; it is a Provider compatibility behavior.

For cache hits where the client requested streaming, Portkey converts a stored non-streaming JSON result into synthetic SSE. That preserves client contract but changes usage provenance: usage comes from the cached response object, not from live upstream terminal frames.

## 3. INPUTS

- Request streaming flag and endpoint family.
- Upstream HTTP status and response content type.
- Provider identity and Provider-specific transform registration.
- Provider stream split policy: SSE group, newline JSON, or binary event stream.
- Cached-response flag and cached JSON body when present.
- Strict OpenAI compatibility setting, which controls whether hook metadata can be prepended.
- Per-stream transformation state for Provider formats that need cross-event context.
- Upstream response body reader and downstream stream writer state.
- Retry and timeout configuration already applied before the streaming body is consumed.
- Usage signals embedded in Provider event payloads, including terminal usage frames or cached JSON usage.

## 4. FAILURE MODES HANDLED

- **Pre-stream upstream failure**: non-success statuses do not enter the stream body path. Retry/fallback policy can act before client-visible output exists. Observable artifact: normal error response or fallback attempt.
- **Malformed or untransformable event**: stream processing errors are caught, associated with Provider context in logs, and the downstream writer is closed so clients do not hang indefinitely. Observable artifact: truncated client stream plus server log.
- **Writer close failure**: close errors are caught separately and logged rather than escaping the request lifecycle.
- **Partial upstream chunks**: incomplete text chunks are buffered until a delimiter arrives, preventing partial JSON parsing.
- **Provider stream diversity**: SSE, newline JSON, cached JSON replay, and binary Provider events are handled through separate parsing paths before normalization.
- **Cache hit with streaming client**: cached JSON is replayed as synthetic SSE rather than returned as one JSON body.

## 5. INTERFACES TO HUAKAI

HUAKAI should connect this behavior to F-GW-002 as the client-visible stream forwarder, not as the billing authority. The forwarder should emit normalized stream events, first-event timestamp, per-event latency, terminal marker observed flag, terminal class, accumulated usage candidate, and usage provenance. Tx2 should consume that outcome to finalize the Usage Record, Billing Ledger entry, Quota reconciliation, and Provider Account active-stream release.

Provider adapters should expose three capabilities: stream framing family, terminal detection rule, and usage extraction rule. Route policy should own timeout budgets. Channel and Provider Account policy should own active-stream limits and downstream-disconnect drain budgets.

## 6. RISKS

- No explicit scanner overflow exists because this is not Go, but the rolling text buffer has the equivalent risk if a Provider never sends a delimiter. HUAKAI needs a per-event byte cap and a typed terminal error.
- Mid-stream retry is absent by design. This avoids duplicate output, but HUAKAI must record that retry was suppressed after client-visible output.
- Usage extraction is delegated to Provider transforms and cached JSON replay. Without a central reconciler, terminal usage, partial usage, and inferred usage can diverge.
- Artificial per-event delay is Provider compatibility logic, not a universal latency budget. HUAKAI should not copy fixed sleeps as policy.
- Closing the stream cleanly on transform error can look successful to some clients unless HUAKAI sends a typed terminal error event where protocol permits.

## 7. SAFE ADAPTATION FOR HUAKAI

- **KEEP** event-by-event transformation and immediate downstream write.
- **KEEP** separate parser choices for SSE, newline JSON, cached JSON replay, and binary Provider events.
- **KEEP** pre-stream retry/fallback only as the default after client-visible output begins.
- **IMPROVE** with an explicit per-event byte ceiling to replace implicit unbounded text buffering.
- **IMPROVE** with central usage reconciliation: reported terminal usage wins, normalized Provider usage is second, inferred tokenizer usage is last, and partial usage is always labeled.
- **IMPROVE** with a bounded post-disconnect drain budget for billing preservation, measured by seconds, bytes, and estimated cost.
- **IMPROVE** with terminal classes: graceful, upstream EOF without terminal marker, provider protocol violation, transform failure, client disconnect, first-token timeout, inter-event timeout, and total-stream timeout.
- **AVOID** Provider-specific fixed sleeps in core gateway policy. If a Provider needs pacing, make it adapter metadata with operator-visible defaults.

## 8. EVIDENCE LEDGER ROWS

- E-LIC-006: Portkey is MIT and therefore a safe source-verified anchor under DR-000 Option C.
- E-PK-001: retry behavior exists but should stay pre-stream for normal streaming requests.
- E-PK-002: fallback behavior exists and must be constrained once output is client-visible.
- E-PK-007: request timeout is configurable; HUAKAI should refine this into stream-axis budgets.
- F-GW-002 matrix row: streaming and non-streaming must share consistent usage accounting; Portkey provides runtime diversity evidence, while HUAKAI must add stronger accounting invariants.

## 9. OPEN QUESTIONS

- Should HUAKAI emit a client-visible terminal error event on transform failure, or only record the terminal class in operator/audit surfaces?
- What is the default per-event byte cap for SSE and newline JSON across reasoning models with large tool-call deltas?
- Should cached JSON replay preserve the original usage source as `cached_reported`, or collapse it into `reported` with a cache-hit flag?
- Which Provider adapters must be L1 for terminal usage extraction: OpenAI-compatible SSE, Anthropic message lifecycle, Google Gemini/Vertex JSON events, and Bedrock binary streams are the likely minimum.

Owner 中文摘要：本文件拆解了 Portkey 在 Hono / Workers / TypeScript 运行时下的 streaming handler：它按 Provider 流格式选择解析方式，逐事件转换并写回客户端，缓存命中时把 JSON 合成为 SSE，错误时关闭流而不是中途重试。与已有 sub2api 拆解的关键差异是 Portkey 不是 Go line scanner 模型，而是 TransformStream + rolling buffer + Provider transform 模型；因此没有传统 scanner buffer overflow，但有“未遇到分隔符时缓冲无限增长”的等价风险。HUAKAI 应吸收它的多协议逐事件归一化、缓存 JSON 转 stream、pre-stream retry 边界，同时补强统一 usage reconciliation、per-event byte cap、typed terminal class、bounded drain 和 Tx2 原子结算。
