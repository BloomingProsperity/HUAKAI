# Portkey Streaming Response Handler - Source-Verified Decomposition

| Field | Value |
| --- | --- |
| Project | portkey |
| Feature | Streaming response handler: SSE forwarder lifecycle, provider stream format normalization, terminal frame detection, scanner buffer overflow, partial usage reconciliation |
| HUAKAI matrix row | F-GW-002 (L1) |
| Round | R3 source-verified decomposition |
| Date | 2026-04-29 |
| Source basis | Local MIT source snapshot; no upstream code, file paths, package names, function names, struct fields, or distinctive source layout are reproduced below |
| Companion critic | `.omc/artifacts/decomp-critic/C2-portkey-streaming-handler.md` read before source |
| Forbidden companion | `docs/decompositions/portkey/streaming-handler-claude-deep.md` exists and was not read |
| Truth-discipline | Observed regions: 16 / Inferences: 10 / Open questions: 8 |

## §1 WHY

Streaming is not a thin body pipe in the observed reference. The accepted provider response is routed through a dispatcher that treats streaming, cache-hit replay, non-streaming JSON mapping, audio/image/octet passthrough, hook processing, retry metadata, and response headers differently [region-1][region-4][region-7][region-9]. The pressure for HUAKAI is therefore not "can bytes be forwarded", but whether one request can produce one defensible Usage Record after early chunks, retries, cache hits, policy checks, and provider-specific terminal signals.

The critic's main warning is confirmed from source: streaming success includes an ordinary success status and a policy-warning status; for streaming responses without a completed JSON body, failed hooks can rewrite status while reusing the body [region-1][region-5]. That creates a product pressure for HUAKAI: policy verdict, tenant-visible transport status, operator audit, quota settlement, and Billing Ledger behavior must be separated instead of being inferred from one HTTP status.

Another pressure is provider heterogeneity. The source has a delimiter-based text reader, a separate binary event reader, cache JSON-to-event reconstruction, provider-specific terminal conversion, and provider-specific usage extraction [region-2][region-3][region-4][region-12][region-13][region-14][region-15]. HUAKAI should model this as provider stream normalization feeding a common Gateway Event lifecycle, not as one generic SSE forwarder.

## §2 WHAT

S-1. Streaming mode is selected from request body state for ordinary routes, but some providers are forced into streaming by provider URL shape rather than only by the user request flag [region-11][region-16]. This means HUAKAI must distinguish client-requested streaming from provider-imposed streaming and record both on the Usage Record [region-11].

S-2. The dispatcher considers both ordinary success and a policy-warning status as stream-transformable success, so a stream can still enter the streaming branch after a failed non-denying check [region-1][region-5]. HUAKAI must not collapse "stream accepted" and "policy clean" into the same state [region-5].

S-3. For live provider streams, the dispatcher returns a response body and no completed mapped response JSON to downstream code [region-1]. That means any later usage, billing, replay, and output-policy logic cannot depend on a terminal JSON object unless HUAKAI adds its own accumulator [region-1][region-9].

S-4. Cache-hit streaming is a separate path: cached JSON is read, transformed into event-stream chunks, and returned with event-stream content type [region-1][region-4]. HUAKAI must specify cache-origin stream headers, trace state, pacing, terminal events, and usage accounting separately from live provider streaming [region-4][region-8].

S-5. Cache lookup can attach prior before-request hook results to the cached response body and can return the policy-warning status when those hook results include failures [region-8]. HUAKAI must keep cache keys, hook verdicts, and tenant scope explicit so cached content cannot inherit or leak another User's policy context [region-8].

S-6. The text stream reader accumulates decoded text until it sees the provider-specific split delimiter, then emits each complete part through an optional transformer [region-2]. If no delimiter arrives, the reader keeps appending to its buffer until stream end; no maximum buffer size was observed in that region [region-2]. HUAKAI needs a bounded scanner with a terminal error state for oversized incomplete chunks [region-2].

S-7. The text stream reader emits a leftover partial buffer at stream end, either transformed or raw, instead of discarding it [region-2]. HUAKAI must decide whether an unterminated final fragment is accepted, rejected, or recorded as partial-output evidence [region-2].

S-8. The binary event reader uses a frame-length prelude, buffers bytes until a full binary frame is present, extracts the payload, and then either transforms the payload or forwards the raw frame [region-3]. HUAKAI must support binary provider streams through a provider adapter boundary, not through the delimiter scanner [region-3].

S-9. The observed writer lifecycle creates a new outbound transform stream, starts an asynchronous read/write loop, writes optional hook-result chunks before provider chunks when compatibility mode permits it, logs processing errors, and attempts to close the writer in a final block [region-4]. HUAKAI should record stream-start, first-chunk, transformer-error, writer-close, and terminal-reason events durably [region-4].

S-10. No explicit client-disconnect cancellation path was observed in the stream writer loop; the loop reads from the provider reader and writes to the outbound writer until provider completion or an exception [region-4]. HUAKAI must add cancellation propagation and a "client disconnected" terminal state rather than relying on writer failure as the only signal [region-4].

S-11. Request timeout is applied around the provider fetch attempt before the accepted response is handed to the streaming reader [region-6]. I did not observe a per-chunk idle timeout, max stream duration, or max bytes/chunks setting in the streaming reader regions [region-2][region-3][region-6]. HUAKAI must treat fetch timeout and stream-drain timeout as different controls [region-6].

S-12. Retry happens before and around response mapping and hook processing; after hook processing, a retry can recur when the resulting status is configured as retriable and retry budget remains [region-7]. Once a live stream is returned to the tenant, the source region does not show a replay-safe fallback mechanism after partial emission [region-4][region-7].

S-13. Before-request denying hooks can return a JSON error before the provider request is sent [region-7]. After-request denying hooks can return a JSON error only when a response JSON exists; when no response JSON exists for streaming, failed hooks without denial can rewrite the status and preserve the body [region-5]. HUAKAI must separate pre-stream deny, buffered pre-first-chunk deny, inline chunk deny, and terminal-only policy marking [region-5][region-7].

S-14. Response headers are enriched after mapping with selected route option, trace id, retry count, cache status, and provider marker [region-9]. For streaming, those headers are attached even though the response JSON is null [region-1][region-9]. HUAKAI should expose equivalent trace and retry metadata but avoid using provider marker as an authority for billing or tenant isolation [region-9].

S-15. Logging captures transformed request, final request, original response JSON, cloned final response, cache metadata, hook span id, route option index, and execution time [region-10]. For streaming, original response JSON can be null because the dispatcher returned no terminal JSON [region-1][region-10]. HUAKAI needs a streaming accumulator or a documented "no terminal body" contract for observability [region-10].

S-16. Provider adapters can synthesize event-stream chunks from cached chat JSON by splitting large text-like content into bounded slices, emitting tool-call deltas, emitting finish-reason frames, and finally emitting a terminal done marker [region-12]. HUAKAI cache replay must preserve terminal semantics and usage metadata while marking the stream as cache-origin [region-12].

S-17. A completions-style cached response follows a similar reconstruction pattern: split output text into slices, emit a finish frame per choice, and emit a terminal done marker [region-15]. HUAKAI must test both chat-like and completion-like cache replay because the event payload shapes are not identical [region-12][region-15].

S-18. One provider adapter observes provider-native message-start, content-delta, tool-use, error, and message-stop events; it ignores ping/stop housekeeping events, turns provider error events into a final error-like event plus terminal done marker, and calculates final usage when the provider sends final usage metadata [region-13]. HUAKAI must store provider terminal reason and normalized terminal reason separately [region-13].

S-19. Another provider adapter normalizes array-like provider chunks, extracts text, reasoning, image, function-call, finish reason, and usage metadata, and includes usage only when the provider chunk carries usage data [region-14]. HUAKAI partial-usage reconciliation must tolerate providers that send usage late or only on selected chunks [region-14].

S-20. Configuration validation exposes cache mode, retry attempts and status codes, request timeout, before/after hooks, input/output guardrails, and strict compatibility settings as operator-configurable request behavior [region-16]. HUAKAI should map these to Route and Channel policy fields using HUAKAI glossary terms, not upstream config names [region-16].

## §2-bis Lifecycle Traces

Trace A - live text stream, normal terminal: request context marks streaming; the dispatcher selects streaming; the writer opens an outbound transform stream; the text reader buffers until delimiters, transforms complete parts, writes chunks, and closes when the provider stream ends [region-1][region-2][region-4][region-11]. Terminal evidence is the provider stream ending plus any provider terminal frame carried through adapter output [region-2][region-13][region-14].

Trace B - cache hit reconstructed as stream: cache lookup returns JSON plus cache status/key; response service invokes dispatcher with cache-hit streaming; dispatcher chooses JSON-to-event conversion; converter writes optional hook-result chunk, generated chunks, finish frames, and terminal done marker; response headers include cache status [region-4][region-8][region-9][region-12][region-15].

Trace C - failed non-denying hook on stream: hook results exist; dispatcher still treats the warning status as success for stream transformation; after-request hook path sees no response JSON and a failed hook result, rewrites the status, and preserves the response body [region-1][region-5][region-7]. HUAKAI should treat this as policy-warning stream completion, not as a clean success [region-5].

Trace D - provider binary stream: streaming is detected from provider URL shape; binary reader buffers complete binary frames, extracts payload, optionally normalizes to event chunks, and the same writer loop emits transformed chunks to the tenant [region-3][region-4][region-11]. This lifecycle bypasses the delimiter scanner [region-3].

Trace E - timeout before stream acceptance: retry wrapper applies request timeout to the provider fetch attempt; on abort it produces a timeout response before the stream reader lifecycle begins [region-6][region-7]. I did not observe an idle timeout once provider headers/body are accepted, so stalled-after-first-byte remains an open HUAKAI design requirement [region-2][region-4][region-6].

## §3 INPUTS

Observed input inventory:

| Input | Observed role | Regions |
| --- | --- | --- |
| Client stream flag | Marks ordinary request as streaming | [region-11] |
| Provider URL shape | Forces streaming for selected provider stream endpoints | [region-11] |
| Provider response status | Determines whether streaming branch treats response as transformable success | [region-1] |
| Provider response content type | Routes audio/image/octet/plain/non-streaming behavior | [region-1] |
| Provider split delimiter | Controls text stream scanner boundary | [region-2] |
| Binary frame length | Controls binary stream scanner boundary | [region-3] |
| Response transformer | Converts provider chunks or cached JSON into normalized stream events | [region-1][region-4][region-12][region-13][region-14][region-15] |
| Cache mode and max age | Controls cache lookup and cache status | [region-8][region-11][region-16] |
| Retry attempts/status codes | Controls fetch retry and post-hook retry recursion | [region-6][region-7][region-16] |
| Request timeout | Applies to provider fetch attempt | [region-6][region-11][region-16] |
| Hook/guardrail config | Drives before/after request policy execution and warning/deny outcomes | [region-5][region-7][region-16] |
| Compatibility flag | Controls whether hook-result chunks and provider-specific content extensions may appear | [region-4][region-11][region-13][region-14] |

## §4 FAILURE MODES

Only source-observed or direct source-implied failure modes are listed.

| Failure mode | Observed source behavior | HUAKAI implication |
| --- | --- | --- |
| Missing delimiter / very large incomplete text chunk | Text scanner appends decoded content until a delimiter or stream end; no max buffer was observed | Add max incomplete-chunk bytes and terminal reason `scanner_limit_exceeded` |
| Unterminated final text fragment | Text scanner emits leftover buffer at stream end | Decide accept/reject policy and record partial-output evidence |
| Incomplete binary frame | Binary reader waits until buffered bytes meet expected frame length | Add max frame bytes and idle timeout |
| Stream transformer exception | Writer loop catches processing error, logs it, and closes writer | Emit durable transformer-error terminal state and partial Usage Record |
| Writer close failure | Writer close is attempted and close errors are logged | Record close failure separately from provider terminal success |
| Fetch timeout before stream | Retry wrapper aborts fetch and returns timeout response | Keep fetch timeout separate from stream idle timeout |
| Failed non-denying hook on stream | Status can be rewritten while body is preserved | Separate policy warning from success and billing settlement |
| Denying hook before provider request | JSON error returned before upstream call | No provider cost; Usage Record should show policy denial |
| Cache replay with failed before hook | Cache response can include hook results and warning status | Cache-origin streams need tenant-scoped cache and policy metadata |
| Provider error event inside stream | Provider adapter can map an error event to an event plus terminal marker | Store provider terminal reason and normalized terminal reason |

## §5 INTERFACES TO HUAKAI

Personal Edition:

| Interface | Required HUAKAI behavior |
| --- | --- |
| Gateway endpoint | Accept stream and non-stream requests through the same API Key, Route, Channel, Provider Account pipeline. |
| Usage Record | One immutable Usage Record per request, with `stream_mode`, `cache_origin`, `retry_attempt_count`, `first_chunk_at`, `terminal_reason`, `partial_completion_tokens`, and final reconciliation fields. |
| Quota | Reserve before upstream call; reconcile on terminal success, provider error, client disconnect, scanner limit, timeout, or policy denial. |
| Cache | Cache-hit streaming must mark cache-origin and never look like fresh provider spend. |
| Policy | Pre-stream deny is L1; inline chunk scanning can be feature-flagged but the lifecycle must leave room for it. |
| Ops UI | Show trace id, Provider Account, Channel, cache status, policy verdict, terminal reason, and correction action. |

SaaS Edition:

| Interface | Required HUAKAI behavior |
| --- | --- |
| Tenant isolation | Every stream lifecycle row, cache key, Usage Record, and Audit Event is tenant-scoped per DR-001. |
| Cross-tenant operations | Abuse review can search stream terminals by tenant, User, API Key, Route, Channel, Provider Account, and policy verdict. |
| Billing Ledger | Partial stream correction must be append-only per DR-006; no mutable billing rewrite. |
| Edition gates | Enterprise retention, inline policy plugins, and long-term stream transcript retention can be SaaS-gated, but stream correctness cannot be removed from Personal Edition. |

## §6 RISKS

R-1 (inference, not observed): HUAKAI DR-001 tenant-aware design means cache-hit stream replay must include tenant in cache key derivation and cache audit metadata. The source shows cache status/key and cached JSON replay, but I did not observe tenant isolation because the reference design is not HUAKAI's domain model [region-8].

R-2 (inference, not observed): DR-006 append-only Usage Records require a stream lifecycle table or append-only stream events table. The source shows null terminal JSON for streams and log capture of cloned responses, but not a durable stream accumulator [region-1][region-10].

R-3 (inference, not observed): DR-002 Personal Edition still needs commercial-grade partial billing because Owner may sell API access from Personal Edition. Streaming partial usage cannot be deferred to SaaS-only work.

R-4 (inference, not observed): Preserving a body after a policy-warning status is too weak for HUAKAI output guardrails on high-risk Routes. HUAKAI needs fail-closed pre-first-chunk buffering or inline chunk scanning as operator-selectable policy.

R-5 (inference, not observed): Retry after hook status is source-observed before response return, but transparent fallback after first emitted chunk is not observed. HUAKAI must prohibit automatic replay after first tenant byte unless the client opted into a resumable protocol.

R-6 (inference, not observed): A scanner without observed max buffer can become a memory pressure vector. HUAKAI should bound incomplete text buffer, binary frame size, total bytes, total chunks, and stream duration.

## §7 SAFE ADAPTATION

1. Replace policy-warning transport status with HUAKAI policy verdict fields: `policy_clean`, `policy_warn`, `policy_denied_before_emit`, `policy_denied_after_emit`.
2. Implement a normalized Gateway Event stream: `stream_start`, `provider_chunk`, `normalized_chunk`, `usage_delta`, `terminal_frame`, `client_disconnect`, `scanner_limit`, `policy_verdict`, `settled`.
3. Use provider adapters for delimiter text streams, binary frame streams, and cache JSON replay. Core routing should only see normalized Gateway Events.
4. Add scanner limits: max incomplete chunk bytes, max binary frame bytes, max chunks, max bytes, idle timeout, and max duration.
5. Add first-byte and post-first-byte rules: retry/fallback allowed before first tenant chunk; after first tenant chunk, only abort, mark partial, or use explicit resumable protocol.
6. Add cache-origin stream contract: deterministic headers, tenant-scoped cache key, cache-created timestamp, policy replay metadata, and no fresh provider Billing Ledger entry.
7. Add partial usage reconciliation: provider usage if supplied, adapter usage deltas if supplied, estimator fallback if configured, and operator correction as append-only ledger adjustment.

## §8 EVIDENCE LEDGER ROWS

| Proposed row | Source type | Clean-room behavior evidence | HUAKAI KEEP / IMPROVE / AVOID |
| --- | --- | --- | --- |
| E-PK-DEEP-STREAM-001 | Source code deep read | Streaming dispatcher treats ordinary success and policy-warning status as transformable streaming success and returns null terminal JSON for streams. | IMPROVE: typed policy verdict plus durable stream accumulator. |
| E-PK-DEEP-STREAM-002 | Source code deep read | Live provider streaming has separate delimiter text and binary frame readers with provider-specific transformation. | KEEP outcome; IMPROVE via adapter boundary and scanner limits. |
| E-PK-DEEP-STREAM-003 | Source code deep read | Cached JSON can be reconstructed into event-stream chunks for streaming clients. | KEEP outcome; IMPROVE tenant-scoped cache-origin audit and deterministic terminal contract. |
| E-PK-DEEP-STREAM-004 | Source code deep read | Hook processing can deny before request, but streaming after-request failures without completed JSON can preserve the live body and rewrite status. | AVOID as final policy model; implement pre-first-chunk or inline enforcement. |
| E-PK-DEEP-STREAM-005 | Source code deep read | Request timeout wraps provider fetch, while no per-chunk idle timeout was observed in stream reader regions. | IMPROVE with stream lifecycle timeout taxonomy. |
| E-PK-DEEP-STREAM-006 | Source code deep read | Provider adapters can emit terminal markers and usage metadata only when provider chunks carry them. | IMPROVE with partial usage reconciliation and terminal state table. |

## §9 OPEN QUESTIONS

1. Does the reference intentionally allow failed non-denying output checks to preserve live stream bodies, or is this an implementation limitation?
2. Is there any client-disconnect cancellation path outside the regions read?
3. Is there any deployment-level stream idle timeout outside the stream reader and retry regions?
4. Are cache keys tenant/workspace scoped in hosted deployments, or only request/header scoped in this open-source path?
5. How are streamed Usage Records persisted outside the log object regions read?
6. Are provider usage chunks always forwarded to the tenant, or are some consumed only for logs in other adapters?
7. Is there a documented contract for the policy-warning status in SDKs and dashboards?
8. Do WebSocket realtime streams share any of this lifecycle, or are they a separate feature outside F-GW-002?

## §10 SOURCE COVERAGE PROOF

Region 1 - Response dispatcher streaming branch, local MIT snapshot around the early response routing region. Contributed: success status set includes ordinary success and policy-warning status; streaming responses return null response JSON; cache-hit streaming uses JSON-to-stream conversion.

Region 2 - Text stream reader region. Contributed: delimiter-based buffering, first-chunk delay, optional transformer, leftover partial buffer emission, and absence of observed max buffer limit.

Region 3 - Binary provider stream reader region. Contributed: length-prefixed frame buffering, payload extraction, adapter transformation, and separate lifecycle from text SSE.

Region 4 - Stream writer and cache JSON-to-stream writer regions. Contributed: outbound transform stream lifecycle, hook-result chunk emission, provider read/write loop, content-type adjustment for JSON streams, generated cache replay chunks, writer close behavior.

Region 5 - After-request hook handling region. Contributed: response JSON is absent for streams; failed hooks on streams can rewrite status while preserving body; denying hooks create JSON errors when response JSON exists.

Region 6 - Retry and fetch timeout region. Contributed: provider fetch timeout with abort controller, timeout response, retry status handling, retry-after handling, and request-level retry budget.

Region 7 - Main request flow and recursive post-response region. Contributed: before hook execution, cache before provider request, pre-request validation, provider request with retry, response mapping, after-hook recursion on retriable status, and final retry count.

Region 8 - Cache service region. Contributed: cacheability checks, cache lookup inputs, cached JSON response construction, cache status/key, and policy-warning status when failed before hooks exist.

Region 9 - Response service region. Contributed: response mapping orchestration and appended headers for route option, trace id, retry attempt, cache status, and provider marker.

Region 10 - Logging service region. Contributed: log object fields for transformed request, original response JSON, cloned final response, cache metadata, hook span id, route option, and execution time.

Region 11 - Request context region. Contributed: request-body stream detection, request timeout resolution, retry normalization, cache config normalization, compatibility flag.

Region 12 - Chat JSON-to-stream adapter region. Contributed: cached chat JSON reconstruction into event chunks, text slicing, content blocks, tool calls, finish frame, usage metadata, and terminal done marker.

Region 13 - Provider-native message stream adapter region. Contributed: ping/housekeeping suppression, provider stop-to-terminal mapping, provider error-to-terminal mapping, tool-call deltas, final usage calculation, and cache-token usage handling.

Region 14 - Provider-native generated-content stream adapter region. Contributed: array-like chunk cleanup, reasoning/text/image/function-call normalization, finish reason mapping, and usage metadata forwarded only when present.

Region 15 - Completion JSON-to-stream adapter region. Contributed: cached completion JSON reconstruction, text slicing, finish frame, usage metadata, and terminal done marker.

Region 16 - Configuration schema region. Contributed: operator-configurable cache, retry, request timeout, before/after hooks, input/output guardrails, and compatibility settings.

## §11 ROUND-2 CRITIC FINDINGS

| Critic finding | R3 disposition |
| --- | --- |
| C-001 streaming success includes ordinary success and policy-warning status | CONFIRM-from-source: §2 S-2, §2-bis Trace C, regions 1 and 5. |
| C-002 output guardrail denial weak for live SSE | CONFIRM-from-source: §2 S-13; no completed response JSON for streams, body can be preserved on failed non-denying hooks. |
| C-003 stream timeout under-specified | CONFIRM-from-source: §2 S-11; fetch timeout observed, stream idle timeout not observed. |
| C-004 fallback/retry after partial emission missing | CONFIRM-from-source plus OPEN: retry before return observed; replay after first tenant chunk not observed. |
| C-005 cache-hit streaming distinct from live provider streaming | CONFIRM-from-source: §2 S-4, S-5, S-16, S-17. |
| C-006 backpressure/client disconnect glossed over | CONFIRM-from-source as absence in read regions: §2 S-10; open question remains for code outside regions read. |
| C-007 binary provider streams are not generic SSE | CONFIRM-from-source: §2 S-8 and Trace D. |
| C-008 streaming observability split from non-streaming | CONFIRM-from-source: §2 S-3 and S-15. |
| F-001 protocol normalization hidden by "forward chunks" | CONFIRM-from-source: regions 2, 3, 12, 13, 14, 15. |
| F-002 guardrails work with streaming flattering read | CONFIRM-from-source: §2 S-13 and §7. |
| F-003 request timeout does not cover full stream lifecycle | CONFIRM-from-source: §2 S-11. |
| F-004 OpenAI-compatible streaming is not one format | CONFIRM-from-source: §2 S-16 through S-19. |
| F-005 cache saves cost hides audit/billing complexity | CONFIRM-from-source for cache replay; billing specifics OPEN because persistence was not observed. |
| D-001 docs/source drift on streaming hooks and observability | CONFIRM-from-source for source side; docs side not re-read in R3. |
| D-002 multiple stream modes | CONFIRM-from-source: text, binary, cache replay, provider-native adapters. |
| D-003 retry/fallback cannot act like ordinary retry after live body return | CONFIRM-from-source plus OPEN after partial emission. |
| D-004 cached streaming correctness edge cases | OPEN-question-because-source-ambiguous: source confirms separate cache replay path but not release-history defect details. |
| N-001 do not copy pseudo-success status | CONFIRM-from-source and adopted in §7. |
| N-002 do not copy live-body reuse after failed streaming checks | CONFIRM-from-source and adopted in §7. |
| N-003 do not copy loose adapter functions without durable tenant state | CONFIRM-from-source for adapter heterogeneity; HUAKAI risk inferred from DR-001/006. |
| N-004 do not copy single edition story | CONFIRM-by-HUAKAI-reasoning: DR-002 requires edition gates without feature removal. |
| N-005 do not copy cache-hit streaming without deterministic contract | CONFIRM-from-source for cache replay; HUAKAI contract added in §7. |
| N-006 do not copy binary parsing into core | CONFIRM-from-source: binary stream separate; adapter boundary required. |
| S-001 inconsistent error taxonomy | CONFIRM-from-source: status warning and deny statuses observed; HUAKAI should use typed policy results. |
| S-002 fail-open risk | CONFIRM-from-source for body preservation on streaming failed hooks; actual unsafe content emission remains policy-dependent. |
| S-003 magic status constants | CONFIRM-from-source; avoided in HUAKAI adaptation. |
| S-004 hidden lifecycle state | CONFIRM-from-source: null terminal JSON for streaming. |
| S-005 tenant data leakage potential | OPEN/INFERENCE: source confirms cache replay and cache keys; tenant leakage requires HUAKAI comparison and more cache-key source. |
| S-006 single-lifecycle assumption | CONFIRM-from-source: lifecycle spans detection, fetch, retry, cache, readers, adapters, hooks, headers, logging. |

Owner 中文总结：本文件对 portkey 的 streaming handler 做了 R3 深拆，真实观察包括：流式分支把普通成功和 policy-warning 状态都当作可转换流、流式响应没有 completed JSON、文本流和二进制流是两套读取路径、cache-hit JSON 会重建为 SSE、hook 在流式场景可能保留 body 只改状态、fetch timeout 与 stream idle timeout 分离、provider adapter 的终止帧和 usage 元数据差异很大；合理推断集中在 HUAKAI 的 DR-001 租户隔离、DR-002 双版本、DR-006 PostgreSQL/append-only 约束如何要求更强的审计、缓存隔离、partial usage reconciliation 和 fail-closed policy；critic 的主要发现均已逐条 CONFIRM/OPEN 处理，没有 REFUTE；仍有 8 个 open questions，主要是 client disconnect、idle timeout、cache tenant scope、stream usage 持久化和 SDK/status 合同。功能没有缩水，clean-room 风险已通过不复制 upstream 名称/路径/代码形状来控制。OWNER 需要确认的是：HUAKAI 是否采用本文建议的 typed policy verdict、bounded scanner、partial-emission 后禁止透明 fallback，以及 cache-origin stream 的审计合同。
