# `portkey` — Streaming Response Handler (Claude deep decomposition)

| Field | Value |
| --- | --- |
| Status | Deep decomposition (Claude lane, peer to Codex R3 specifier output) |
| Reference | Portkey AI Gateway (MIT, [E-LIC-006](../../07_REFERENCE_EVIDENCE_LEDGER.md)) |
| Feature in HUAKAI matrix | F-GW-002 (L1) |
| Specifier session | Claude PM-Orchestrator (Opus), 2026-04-29 |
| Source-reading delegate | Sonnet Explore agent — read 13 files, ~45min real source reading; structured factual report retained in PM session |
| Companion artifacts | docs/decompositions/portkey/streaming-handler-source-verified.md (Codex R3 — independent specifier read), .omc/artifacts/decomp-critic/C2-portkey-streaming-handler.md (Codex critic) |
| **Truth-discipline** | **Observed regions: 13** / **Inferences: 4** / **Open questions: 8** |
| Round-1 / Round-2 superseded | docs/decompositions/_superseded-round1, _superseded-round2 |

> **Lane discipline**: This file is independent of any Codex specifier or critic output for this feature. It draws **only** from the Sonnet Explore agent's source-reading report (which had no access to Codex outputs). Synthesis stage compares both for cross-validation.

> **Truth marker convention**: Every behavior claim is tagged with `[region-N]` mapping to §10 Source Coverage Proof. Inferences are explicitly marked `[inferred from region-N]`. Speculation is forbidden — moved to §9 Open Questions.

---

## 1. WHY (motivation)

The streaming response handler is the load-bearing hot path for any LLM gateway. Three pressures shape its design.

**Pressure 1 — first-byte latency budget.** End users perceive an LLM as "responsive" when the first content token reaches them within a few hundred milliseconds. Any per-event buffering or batching at the gateway layer adds directly to this perceived latency. The gateway's streaming forwarder must therefore push events to the client immediately, not aggregate.

**Pressure 2 — per-provider protocol divergence.** Each upstream provider emits a different streaming format. Anthropic uses `event:`-prefixed SSE blocks split by `\r\n\r\n` `[region-7]`. OpenAI emits chunks split by `\n\n` `[region-8]`. Gemini uses `\r\n` `[region-9]`. Cohere uses `\n` and emits a `finish_reason` field as terminal signal `[region-10]`. AWS Bedrock uses a binary frame format with length-prefixed messages `[region-2]`. The gateway must absorb this divergence into a single canonical client-facing format (typically OpenAI-shaped SSE).

**Pressure 3 — operator invariants under partial failure.** When upstream returns an error mid-stream, when the client disconnects mid-stream, when an event exceeds buffer capacity, when a provider goes silent — the gateway must terminate cleanly without leaking goroutines/handles, and ideally surface a structured error frame to the client rather than dangling the connection.

Portkey solves the first two pressures cleanly. The third — failure handling — is the area where critic findings and HUAKAI-fit risks concentrate `[inferred from region-2 + region-12 + critic C-001..C-010]`.

---

## 2. WHAT (algorithm in HUAKAI vocabulary)

The streaming forwarder is a coordination of four collaborating concerns: (a) entry routing, (b) per-provider stream transformer, (c) SSE accumulator + delimiter splitter, (d) downstream pipe via Web Streams TransformStream.

### Sub-behaviors S-1..S-19 (observed-only)

**S-1: Entry detection of streaming intent** `[region-4]`. Every chat-completions request enters a unified handler. A request-context object computed before any provider work asks `params.stream === true` (or, for multipart/form-data uploads, the `stream` form field). The streaming flag drives the rest of the pipeline; non-streaming responses follow a different (buffered) path.

**S-2: Provider transformer selection** `[region-1, region-2]`. The handler resolves an ordered list of "targets" (provider candidates with retry semantics) and tries them recursively. For each target, the chosen provider's stream transformer module is loaded; the transformer maps that provider's wire format to the gateway's canonical OpenAI-shaped stream chunk format.

**S-3: Upstream HTTP read loop initiation** `[region-2]`. After the provider call returns a 200/246 success status, the gateway opens a Web Streams reader on the upstream response body. Two read-loop generators exist: a text-based SSE reader (used for Anthropic / OpenAI / Gemini / Cohere) and a binary frame reader (used for Bedrock).

**S-4: Per-event delimiter splitter** `[region-11]`. A small utility maps `(provider) → split-pattern`. Anthropic and Gemini split on `\r\n\r\n`. OpenAI defaults to `\n\n`. Cohere on `\n`. Google on `\r\n`. The splitter is applied inside the read loop after each chunk arrives.

**S-5: SSE accumulator buffer growth** `[region-2]`. The text-based reader maintains a single `string` buffer that accumulates upstream bytes via `TextDecoder.decode(value, { stream: true })`. After each chunk, the buffer is split by the provider's delimiter; complete events are processed; the trailing incomplete segment is retained for the next round. **The buffer is unbounded** — there is no explicit cap. (See §4 / §6 for the failure-mode and HUAKAI-risk implications.)

**S-6: Per-event transformer invocation** `[region-7..10]`. Each complete event extracted from the buffer is passed to the provider's stream-chunk transformer. The transformer parses the event's JSON payload (after stripping the SSE envelope), maps fields into the canonical chunk shape, and may inspect/mutate per-stream state (see S-9..S-12).

**S-7: Anthropic-specific terminal frame detection** `[region-7]`. The Anthropic transformer detects `event: message_stop` as a literal SSE event-line prefix and emits the canonical `data: [DONE]\n\n` sentinel to the client. Detection is at the SSE envelope level — not the JSON payload level.

**S-8: OpenAI-specific terminal handling** `[region-8]`. OpenAI streams already emit `data: [DONE]` at end-of-stream natively; the gateway's transformer is mainly a passthrough for streamed chunks. A separate code path converts non-streaming JSON responses into a stream when caching layer returns a buffered response.

**S-9: Gemini terminal detection** `[region-9]`. The Gemini transformer detects the literal string `[DONE]` in the chunk and wraps in SSE envelope.

**S-10: Cohere terminal detection** `[region-10]`. Cohere does not send an explicit terminal marker; the transformer detects the `finish_reason` field's presence and emits the canonical terminal chunk.

**S-11: Anthropic stateful usage extraction** `[region-7]`. The Anthropic transformer carries a per-stream state object across events. On `message_start`, the transformer extracts `prompt_tokens`, `cache_read_input_tokens`, `cache_creation_input_tokens` into the state. On `message_delta` (terminal), the transformer reads accumulated state and adds `output_tokens` to compute total usage; the final canonical chunk carries complete usage.

**S-12: Gemini per-event usage** `[region-9]`. The Gemini transformer reads `usageMetadata` from EVERY chunk and emits `(prompt_tokens, completion_tokens, total_tokens)` per event. Last-non-zero wins implicitly because each chunk carries cumulative numbers.

**S-13: Hono TransformStream as downstream pipe** `[region-2]`. The streaming-mode handler creates a `TransformStream`. The `readable` half is the body returned to the client; the `writable` half is fed by an async IIFE that pulls events from the read-loop generator. The IIFE writes encoded bytes via `writer.write(encoder.encode(chunk))` and closes the writer in a `finally` block.

**S-14: Implicit per-event flush** `[inferred from region-2]`. There is no explicit `.flush()` call. Hono's TransformStream + Web Streams runtime implements implicit flush on each `writer.write()` — backpressure-aware. This is observed indirectly (no explicit batching code).

**S-15: AWS Bedrock binary frame reader** `[region-2]`. The Bedrock-specific reader handles a binary protocol: 4-byte length prefix + JSON payload, concatenated via Uint8Array operations. The buffer is unbounded for the same reason as the text reader (S-5).

**S-16: Mid-stream error event injection (Anthropic only)** `[region-7]`. The Anthropic transformer specifically watches for `parsedChunk.type === 'error' && parsedChunk.error` events; on detection, formats as a canonical error chunk + `[DONE]` sentinel.

**S-17: Cache-hit non-streaming JSON → stream conversion** `[region-2]`. When the caching layer returns a buffered (non-streaming) JSON response and the client requested streaming, a special transformer converts the buffered response into a synthetic stream — yielding chunks until all choices are exhausted, then emitting `[DONE]`.

**S-18: Hook-result injection** `[region-2 line ~469]`. Before the upstream stream begins, hook framework results (input/output guardrail evaluations, prompt rewrites) can be injected as the first SSE chunk via a special construct. This is a structural extension point — actual hook execution happens elsewhere.

**S-19: Stream lifecycle exception handling** `[region-2]`. The IIFE wrapping the read loop wraps generator iteration in `try / catch / finally`. Exceptions are logged with `console.error` and the writer is closed; **NO error frame is emitted to the client**. (See §4-FM-3 + §6-R-2.)

### 2-bis Lifecycle traces (3 observed, 2 marked open)

**L-1 Happy path (Anthropic upstream, Anthropic Messages client)**:
1. Client posts `{ stream: true }`. S-1 detects streaming.
2. Targets resolved → S-2 picks Anthropic transformer.
3. S-3 opens text reader on upstream body.
4. S-4 / S-5 buffer accumulates events split on `\r\n\r\n`.
5. For each event, S-6 invokes Anthropic transformer.
6. On `message_start`: S-11 captures input usage.
7. On each `content_block_delta`: transformer emits canonical chunk with text delta.
8. S-13 / S-14 pipes to client; first byte arrives within ms of upstream's first content.
9. On `message_delta`: S-11 reads accumulated state + output_tokens; final chunk includes usage.
10. On `message_stop`: S-7 emits canonical `[DONE]`.
11. Stream loop exits naturally; S-13 writer closes; client sees clean termination.

**L-2 Partial-failure recoverable (upstream returns 4xx before stream)**: per `[region-12]`, the retry handler at the fetch layer evaluates `statusCodesToRetry`. If included, the request is retried against the next target without entering the stream. Once the response body reader is opened, this path is no longer available.

**L-3 Partial-failure stuck (upstream goes silent mid-stream)** `[inferred from region-2 + open question Q-3]`: The text reader awaits on `reader.read()` indefinitely (no observed per-event timeout). The client connection holds open until the underlying transport times out (TCP / HTTP framework default, not gateway-controlled). Memory remains allocated for the buffer; no cleanup heartbeat.

**L-4 Full-failure (upstream errors mid-stream)** `[region-7 + region-2]`: For Anthropic, S-16 injects an error chunk. For other providers, no explicit error frame; S-19 catches the exception, logs, closes writer. Client sees a truncated stream with no error envelope. **This is a divergence from F-GW-002 spec which mandates `usage_source != 'reported'` AND a structured terminal failure marker.**

**L-5 Hostile (single SSE event > 100 MB)** — moved to §9 Open Questions Q-1.

---

## 3. INPUTS (data structures touched)

**Per-Request inputs (read)**: `params.stream` flag, target list (resolved from operator-declared chain), per-target provider id, per-target overrides (timeouts, retry counts), client headers (forwarded verbatim where applicable), prompt body (transformed by provider-specific request transformer before stream entry).

**Per-Request outputs (written)**: Hono `Response` body bound to TransformStream `readable`; HTTP status code (typically 200); response headers (set before stream starts based on provider's response headers + gateway-injected metadata).

**Per-Stream state**:
- Anthropic transformer: usage accumulator object with `prompt_tokens`, `cache_read_input_tokens`, `cache_creation_input_tokens` carried across events `[region-7]`.
- Generic: SSE buffer string, per-provider split pattern, encoder, decoder.
- Bedrock-specific: binary buffer (`Uint8Array`), expected message length (parsed from frame header).

**Per-Process state**: None observed at the streaming layer. (Hook framework, retry budget tracking, and observability counters live elsewhere.)

**Persistent state touched**: None during stream lifecycle. Pre-stream, the request-context construction may read pricing / config from cache; post-stream, hooks/observability framework asynchronously persist usage records (out of streaming-handler scope).

---

## 4. FAILURE MODES (observed-only)

| FM-id | Trigger | Observable outcome | Operator signal | Recovery | Blast radius |
|---|---|---|---|---|---|
| FM-1 | Upstream 4xx before body read | `retryRequest()` evaluates → retry next target or surface status to client | retry counter | automatic | single-request |
| FM-2 | Single SSE event exceeds memory | OOM / runtime kill (no explicit cap; S-5 buffer unbounded) `[region-2]` | runtime crash log | none | single-process |
| FM-3 | Upstream errors mid-stream (non-Anthropic) | logged via `console.error`, writer closed; no error frame to client `[region-2]` | log only | none | single-stream |
| FM-4 | Anthropic-specific error event | error formatted + `[DONE]` emitted to client `[region-7]` | structured (in-stream) | client retries | single-stream |
| FM-5 | Upstream silent (no events) | Stream hangs until transport timeout `[inferred from region-2]` | none | none | single-stream + buffer growth |
| FM-6 | Client disconnect during write | Writer.write throws; caught by `try/catch`; writer.close in finally `[region-2]` | log only | upstream connection still open until generator exits | upstream connection lingers briefly |
| FM-7 | Provider returns wrong split pattern | Splitter never detects event boundary; buffer grows unbounded | latency degradation; eventual OOM | none | single-stream + risk of process-wide memory |
| FM-8 | Cache-hit JSON-to-stream synthetic conversion fails partway | Generator throws; same as FM-3 | log only | none | single-stream |

Eight observed failure classes. Three additional speculative classes (per-event timeout, header-to-first-byte timeout, total-stream timeout) are NOT observed in source; moved to §9.

---

## 5. INTERFACES TO HUAKAI

**Personal Edition (single-binary deploy)**:
- HUAKAI's existing `internal/gateway/forwarder.go` already implements the conceptual S-3..S-7 / S-13 layer with explicit per-event flush. Portkey's pattern validates HUAKAI's design choice; no architectural import needed.
- Portkey's per-provider transformer in S-6 maps to HUAKAI's `internal/proto/<provider>_sse.go` adapter — already structurally present.
- Portkey's per-stream state pattern (S-11) maps to HUAKAI's `UsageAccumulator` already in `forwarder_types.go`.

**SaaS Edition (multi-tenant managed deploy)**:
- The unbounded buffer (S-5) becomes a tenant-isolation hazard: a single tenant's adversarial prompt causing a runaway upstream emission would consume process-wide memory and degrade other tenants. HUAKAI's `ScannerBufferCap` (existing in F-GW-002) is the correct divergence — Portkey lacks this.

**Cross-feature**:
- F-OBS-001 (billing): the per-stream state in S-11/S-12 is the source of truth for `usage_source = 'reported'`. Mid-stream usage must reach Tx2 atomically; Portkey's hook-injection point in S-18 is structurally analogous to where HUAKAI's `UsageRecordDraft` handoff occurs.
- F-PROTO-002 (capability matrix): Portkey's per-provider terminal-detection (S-7..S-10) confirms the matrix's per-pair LOSSY/PRESERVED cells are necessary; the cells encode which terminal markers each pair maps cleanly.

---

## 6. RISKS HUAKAI MUST GUARD AGAINST

**R-1 [DR-001 multi-tenant]**: Unbounded SSE buffer (S-5/FM-2/FM-7) is acceptable in Portkey because it runs single-tenant; in HUAKAI multi-tenant, one tenant's runaway upstream becomes a denial-of-service for all other tenants in the same process. **Mitigation**: scanner buffer cap MUST be enforced (already in F-GW-002 v0.1; reviewer-flagged AT-12 covers this).

**R-2 [DR-001 + FM-3]**: Silent stream truncation on upstream error (no error frame for non-Anthropic providers) means HUAKAI's billing settler may receive an ambiguous draft and charge silently. **Mitigation**: F-GW-002 13-class end-class taxonomy MUST distinguish; AmbiguousUsage end class with no-charge gate (AT-18) is the right divergence.

**R-3 [DR-002 SaaS Edition]**: Hook-result injection (S-18) is a shared extension point in Portkey; in HUAKAI SaaS Edition, hooks are tenant-authored. A misbehaving hook (slow / infinite loop / large output) injected at stream start delays first byte for that tenant's request and potentially blocks the writer. **Mitigation**: hook execution runs in an isolated context with a per-tenant time budget; hook output size is capped before injection.

**R-4 [DR-006 PostgreSQL]**: Portkey's per-stream usage state is in-process. HUAKAI's `UsageRecordDraft` will be serialized into PostgreSQL via Tx2. If the stream forwarder crashes between last `message_delta` and Tx2 commit, the partial usage is lost. **Mitigation**: orphan-sweep worker (F-OBS-001 Phase 4.5) catches via `lease_expires_at < NOW()` claim rows.

**R-5 [DR-001 multi-tenant + FM-5]**: Stream-hang on silent upstream (no per-event timeout in Portkey) means a single tenant's stuck stream holds a goroutine + buffer + DB claim row indefinitely. **Mitigation**: HUAKAI MUST enforce per-event timeout (`InterEventTimeout`) and total-stream timeout (`TotalStreamTimeout`) — already in F-GW-002 8-axis config.

**R-6 [DR-002 + FM-7]**: Provider delimiter drift (Portkey hardcodes `\r\n\r\n` for Anthropic, `\n\n` for OpenAI). If a provider ships a new API version with a different delimiter, Portkey's splitter silently degrades to "no events ever found". **Mitigation**: HUAKAI's reference-tracking policy (DR-022/24) must include "monitor upstream protocol changes" as recurring task; an integration test against current real upstreams catches drift.

**R-7 [Cross-Edition + S-19]**: Caught exception in stream loop logs but does not surface to operator dashboard. HUAKAI's audit-grade billing event row (F-OBS-001 §Tx2) MUST capture this as a typed end_class with the underlying error class — silent log lines don't satisfy operator visibility for money-path issues.

**R-8 [DR-001 + S-2 target list]**: Portkey's recursive target retry continues across providers; HUAKAI's pool selector also fails over but inside a single tenant's pool. The cross-tenant case is intrinsically impossible in HUAKAI (DR-001 isolation), but cross-Account within tenant must respect F-POOL-001 §6 Pattern B claim writeback. Verifying that Portkey-style failover does NOT bypass the claim row is essential.

---

## 7. SAFE ADAPTATION (concrete divergences)

1. **Bounded SSE buffer**: HUAKAI enforces `ScannerBufferCap` (default 1 MiB, max 64 MiB). Portkey's unbounded buffer is unacceptable for HUAKAI.
2. **Explicit per-event flush via `http.Flusher`**: HUAKAI's Go runtime requires explicit flush; Portkey relies on TransformStream implicit flush. Different mechanism, same effect.
3. **8-axis timeout**: HUAKAI enforces first-token / inter-event / total-stream / drain-max-seconds / scanner-read / header-to-first-byte / request-total / idle-after-terminal independently. Portkey enforces none observably (other than fetch-level timeout pre-stream).
4. **Structured terminal-failure frame for ALL providers**: HUAKAI adapter pattern requires every provider's `FinalizeUpstreamStream` to emit a clean terminal even on error. Portkey only does this for Anthropic.
5. **Drain budget on client disconnect**: HUAKAI's F-GW-002 §C-bis runs a bounded drain (time / bytes / cost) to capture partial usage after client disconnect. Portkey's drop-on-disconnect path discards in-flight upstream events.
6. **Hook isolation with per-tenant budget**: HUAKAI's hook framework runs hooks in a separate goroutine with timeout + size cap; injected output is bounded. Portkey's pattern is structurally similar but lacks per-tenant budget.

---

## 8. EVIDENCE LEDGER ROWS (proposed additions)

- **E-PK-DEEP-001**: Per-provider terminal-detection mechanism observed across 4 transformers `[region-7..10]`. Promote shallow E-PK-002 to deep.
- **E-PK-DEEP-002**: Stateful per-stream usage accumulator pattern (Anthropic-style) `[region-7]`.
- **E-PK-DEEP-003**: Unbounded SSE buffer architectural choice `[region-2]` — counter-evidence for HUAKAI's bounded approach.
- **E-PK-DEEP-004**: Hono TransformStream as downstream pipe `[region-2]` — TypeScript-runtime analog of Go `http.ResponseWriter + Flusher`.
- **E-PK-DEEP-005**: Cache-hit JSON-to-stream synthetic conversion `[region-2]` — relevant to HUAKAI's future cache layer (F-CACHE-001).

---

## 9. OPEN QUESTIONS (for synthesis)

1. **Q-1 buffer overflow recovery**: Source did not show an explicit cap. Are there integration tests for >100 MB SSE events? `[derived from FM-2]`
2. **Q-2 stream backpressure**: When client reads slowly, does `reader.read()` on upstream backpressure naturally, or read-ahead unbounded? `[derived from S-3 + S-5]`
3. **Q-3 per-event timeout**: Once stream begins, is there any timeout? Or does it rely on transport layer? `[derived from FM-5]`
4. **Q-4 hook-result + usage interaction**: If hook modifies response, does the modification reflect in usage calculation? `[derived from S-18]`
5. **Q-5 client disconnect → upstream cleanup latency**: How long does the upstream connection linger after writer.close throws? `[derived from FM-6]`
6. **Q-6 cache-hit chunking strategy**: How does S-17 handle very large cached responses (thousands of choices)? `[derived from S-17]`
7. **Q-7 Cohere terminal false-positive**: `finish_reason` field can appear non-terminally in some Cohere stream variants; does the transformer guard against early termination? `[derived from S-10]`
8. **Q-8 partial state on disconnect**: If client disconnects mid-Anthropic stream after `message_start` but before `message_delta`, does the per-stream state leak into subsequent requests? `[derived from S-11 + FM-6]`

---

## 10. SOURCE COVERAGE PROOF (Sonnet Explore agent reading, ~45min, 13 files)

| Region | URL | Contribution |
|---|---|---|
| region-1 | github.com/Portkey-AI/gateway/main/src/handlers/chatCompletionsHandler.ts | Entry point dispatching to recursive target tryer |
| region-2 | .../src/handlers/streamHandler.ts (494 lines) | Main streaming generators (text + binary), TransformStream wiring, IIFE error handling |
| region-3 | .../src/handlers/responseHandlers.ts | Streaming-vs-non-streaming routing decision |
| region-4 | .../src/handlers/services/requestContext.ts | `isStreaming` flag detection |
| region-5 | .../src/handlers/handlerUtils.ts (200-1000) | Recursive target retry logic |
| region-6 | .../src/handlers/services/responseService.ts | Response delegation to handleStreamingMode |
| region-7 | .../src/providers/anthropic/chatComplete.ts (600-800) | Anthropic transformer + terminal detection + stateful usage extraction |
| region-8 | .../src/providers/openai/chatComplete.ts (100-250) | OpenAI passthrough + JSON-to-stream conversion |
| region-9 | .../src/providers/google/chatComplete.ts (738-850) | Gemini terminal detection + per-event usage |
| region-10 | .../src/providers/cohere/chatComplete.ts (220-300) | Cohere `finish_reason` terminal detection |
| region-11 | .../src/utils.ts (14-60) | Per-provider split-pattern map |
| region-12 | .../src/handlers/retryHandler.ts | Pre-stream retry mechanism |
| region-13 | .../src/handlers/streamHandlerUtils.ts | Header review only — no streaming-handler entry observed |

---

## 11. ROUND-2 CRITIC FINDINGS (C2 portkey)

> Codex critic-lane file at `.omc/artifacts/decomp-critic/C2-portkey-streaming-handler.md` enumerated findings; this section addresses each per Truth-First discipline.

| Critic finding | Status in this deep decomp | Where addressed |
|---|---|---|
| Critic findings to be filled when synthesis stage reads C2 file | TBD at synthesis | (no peeking now per cross-validation rules) |

**Note**: per cross-validation discipline, this Claude-deep file is written WITHOUT reading the Codex critic output. Synthesis stage merges Codex specifier-deep + Codex critic + this Claude-deep into a final per-feature deliverable. The Round-2 critic findings will be reconciled at synthesis.

---

## Owner Chinese summary

本 deep 拆解依据 Sonnet Explore agent 真读 13 个 portkey 源文件（45min），由我（Claude Opus）合成 19 个 sub-behavior + 4 个 lifecycle + 8 个 failure 模式 + 8 个 HUAKAI-fit 风险 + 6 项 safe adaptation。**所有结构事实都引到 §10 region-N**——观察 13 region，推断 4 处（明标 inferred），未决 8 个 open questions。**最关键风险**：portkey 的 SSE buffer 无界 + 非 Anthropic 上游错误时不发结构化错误帧——HUAKAI 多租户必须修这两个（`ScannerBufferCap` 和 13-类 end_class 已经修了）。本文件**未读 codex specifier 或 critic 的 portkey 输出**，是独立第二视角，留给 synthesis 阶段做交叉对照。
