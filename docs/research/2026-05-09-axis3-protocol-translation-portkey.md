# Portkey Gateway — Axis 3 Protocol Translation Specifier Read

Reference: `Portkey-AI/gateway@351692fd9236af222168134b416924fae0bdba23` (head 2026-03-25, MIT, archived=false, disabled=false, pushed_at within 45 days, 11.6k stars).

## TL;DR

Portkey treats protocol translation as a declarative *parameter table* — every provider exports a `ProviderConfig` (param name → target name + transform fn + min/max + default) plus a `ProviderAPIConfig` (URL + headers + endpoint) plus an optional `responseTransforms` map keyed by endpoint string. The shared canonical model is OpenAI Chat Completions augmented with content-block / cache-control / thinking extensions; cross-provider responses fold into that shape, then the **client side** can flip a `strictOpenAiCompliance` switch to either drop or pass through provider-specific extras.

For protocol divergence Portkey runs three layered escape hatches: (1) `getConfig({params,providerOptions})` for runtime sub-routing (e.g. Bedrock per-foundation-model), (2) per-endpoint `requestHandlers` override that bypasses the param table entirely (Bedrock S3 uploads, Vertex batch), (3) a separate canonical surface — `messages`/`messagesCountTokens` (Anthropic-shape canonical) — coexisting with the OpenAI-shape `chatComplete`. Streaming uses native Web Streams `TransformStream`; SSE is parsed by hand against a per-provider split pattern (`\n\n`, AWS framed binary, etc.) and each chunk runs through a single `*StreamChunkTransform(chunk, fallbackId, streamState, strictOpenAiCompliance, gatewayRequest)` that mutates `streamState` and emits OpenAI chunk JSON. Routing modes (`single` / `fallback` / `loadbalance` / `conditional`) sit in the recursion above the protocol layer and never see translated bodies — translation happens once per leaf attempt.

The 78-provider count is achieved through ~ProviderConfig+APIConfig declarations of usually 100–800 LoC each plus shared `messagesBaseConfig` / `OpenAIChatCompleteConfig` for OpenAI-clone vendors. New provider ≈ one config object + endpoint URL + (optional) response transformer; no schema is reused at runtime — `zod` is only used for inbound `config:` validation and for one Bedrock JSONL batch validator.

This is a single-canonical-model (with Anthropic shadow) declarative-table design — not metadata-driven, not zod-validated per provider, not adapter-class-OO. Closer to LiteLLM's lookup-table than to Helicone's pass-through. **HUAKAI take-away**: this design is cheap to extend (good for axis 3) but lossy by default (extras hidden behind a compliance flag), routing-blind to translation cost (good — separation), and not fingerprint-aware at the SSE chunker layer (gap that HUAKAI's R5/R7/R8 targets).

---

## Q1 Provider Abstraction

**Where**: directory `src/providers/<vendor>/` with `index.ts` exporting a `ProviderConfigs` object aggregated by `src/providers/index.ts`. `Portkey-AI/gateway@351692fd:src/providers/index.ts:1-51` shows `~78` distinct vendor imports plus utility re-exports.

**Interface shape** (paraphrased, not verbatim): each vendor exports a `ProviderConfigs` whose keys are *endpoint strings* (`'complete' | 'chatComplete' | 'messages' | 'embed' | 'imageGenerate' | 'createSpeech' | 'createTranscription' | 'realtime' | 'uploadFile' | 'createBatch' | 'createFinetune' | …` — full union at `Portkey-AI/gateway@351692fd:src/providers/types.ts:85-120`, 36 endpoints). Each endpoint maps to a `ProviderConfig` — a flat `{ canonicalParamName: { param, default, min, max, required, transform } }` table (`Portkey-AI/gateway@351692fd:src/providers/types.ts:19-41`). The vendor also supplies an `api: ProviderAPIConfig` object with three required functions — `headers(args)`, `getBaseURL(args)`, `getEndpoint(args)` — and two optional ones — `transformToFormData(args)`, `getProxyEndpoint(args)` (`Portkey-AI/gateway@351692fd:src/providers/types.ts:47-83`). A `responseTransforms` map keyed by the same endpoint strings (e.g. `'stream-chatComplete'`, `'chatComplete'`, `'messages'`) carries the inverse direction, e.g. `Portkey-AI/gateway@351692fd:src/providers/anthropic/index.ts:25-31`.

**Three escape hatches** when the table model breaks:

1. `getConfig({params, providerOptions}): ProviderConfigs` — runtime sub-routing inside one provider entry. Bedrock dispatches on the model prefix (`anthropic.*` vs `amazon.*` vs converse) and returns a different sub-config object per request. Source: `Portkey-AI/gateway@351692fd:src/providers/bedrock/index.ts:90-109`. Honored at translation time: `Portkey-AI/gateway@351692fd:src/services/transformToProviderRequest.ts:150-156` (`if (providerConfig.getConfig) providerConfig = providerConfig.getConfig({params, providerOptions})[fn]`).
2. `requestHandlers: Partial<Record<endpointStrings, RequestHandler>>` — per-endpoint complete bypass. The handler signature is `({c, providerOptions, requestURL, requestHeaders, requestBody}) => Promise<Response>`, types at `Portkey-AI/gateway@351692fd:src/providers/types.ts:131-143`. Bedrock declares it for `uploadFile / retrieveFile / getBatchOutput / retrieveFileContent` (S3-backed paths that don't fit the param-table model), `Portkey-AI/gateway@351692fd:src/providers/bedrock/index.ts:92-97`; Vertex AI similarly for batch endpoints (`src/providers/google-vertex-ai/index.ts:205`).
3. Per-endpoint `responseTransforms[fn]` slot may be missing — meaning identity passthrough — or may be a *generator function* that streams chunks instead of returning one (`Portkey-AI/gateway@351692fd:src/handlers/streamHandler.ts:428-466`).

**100+ providers — exhaustive or schema-driven?** Exhaustive enumerated. There is no metadata file mapping providers to capabilities; capability is inferred from "is this endpoint key present in the provider's `ProviderConfigs`?". Many cheap-to-add OpenAI-clones (DeepInfra, Together, Fireworks, etc.) reuse the OpenAI configs by re-export — see `Portkey-AI/gateway@351692fd:src/providers/index.ts:1-51` for the import list. Anthropic-base (`src/providers/anthropic-base/messages.ts`) gives a shared `messagesBaseConfig` that Anthropic + Bedrock-Anthropic + Vertex-Anthropic re-skin via `getMessagesConfig({exclude, defaultValues, extra})` (`Portkey-AI/gateway@351692fd:src/providers/anthropic-base/messages.ts:69-95`).

**Cost of a new provider**:
- Pure OpenAI-compatible: ~50 LoC (one `api.ts` overriding `getBaseURL` + headers + a one-line `index.ts` that aliases OpenAI configs).
- Anthropic-clone: ~100 LoC (`getMessagesConfig({})` + custom `api.ts`).
- Native shape (Google, Bedrock Converse, Cohere): 600–900 LoC — `chatComplete.ts` reaches 892 lines for Google. Source: `wc -l src/providers/google/chatComplete.ts → 892`.

Three dimensions of upgrade observed: **架构** (declarative param table + endpoint-keyed inverse transforms, not OO adapter); **算法** (no scoring — strategy lives one layer up in `tryTargetsRecursively`); **生态** (zod validation only on inbound config, not on per-provider transforms — runtime relies on TS type erasure).

## Q2 Canonical Model

**Two coexisting canonical surfaces**:
- **OpenAI-shape** for `chatComplete / complete / embed`. Defined at `Portkey-AI/gateway@351692fd:src/types/requestBody.ts:427-488` as the `Params` interface — `model, messages, tools, tool_choice, max_tokens, max_completion_tokens, temperature, top_p, n, stream, logprobs, top_logprobs, stop, presence_penalty, frequency_penalty, response_format, seed, store, metadata, modalities, audio, prediction, service_tier, …` plus an `[anthropic_beta, anthropic_version, thinking, top_k, dimensions, parameters]` annex for non-OpenAI parameters that pass through.
- **Anthropic-shape** for `messages / messagesCountTokens`. The base config that all Anthropic-shape providers (Anthropic, Bedrock-Anthropic, Vertex-Anthropic) inherit lists `model, messages, max_tokens, system, temperature, top_k, top_p, tools, tool_choice, thinking, mcp_servers, container, service_tier, stop_sequences, stream, metadata` — `Portkey-AI/gateway@351692fd:src/providers/anthropic-base/messages.ts:3-67`. Inbound type is `MessagesRequest` from `src/types/MessagesRequest.ts`.

So Portkey is *bi-canonical*, not single-canonical. The `chatCompletionsHandler` route goes through the OpenAI-shape canonical; the `messagesHandler` route goes through the Anthropic-shape canonical. Cross-shape conversion (e.g. Anthropic in / OpenAI out) is *not* a first-class operation — translation is always *canonical → vendor*. The two canonicals coexist at the route layer, not in a unified IR.

**zod / typebox / runtime validation?** `zod ^3.22.4` is in `package.json` (`Portkey-AI/gateway@351692fd:package.json:58`). It is used in exactly two production places: (a) inbound `config:` header validation at `Portkey-AI/gateway@351692fd:src/middlewares/requestValidator/schema/config.ts:1` and following lines (`z.object({...api_key, virtual_key, retry, conditions, …})`), and (b) Bedrock JSONL batch line validation at `Portkey-AI/gateway@351692fd:src/providers/bedrock/uploadFileUtils.ts:31`. The per-provider `Params → vendor body` transform is *not* zod-validated — it relies on TS types at compile time and at runtime on the `min/max/default/required` fields of `ParameterConfig` (`Portkey-AI/gateway@351692fd:src/providers/types.ts:19-32`) plus the per-param `transform(params, providerOptions)` callback. Out-of-range numerics are silently clamped — see the `value < paramConfig.min` branch in `Portkey-AI/gateway@351692fd:src/services/transformToProviderRequest.ts:52-69`.

**Cross-provider information fidelity** is handled with *opt-in extra fields plus a strict-compliance flag*:
- Anthropic `thinking` block, content blocks, prompt-cache `cache_creation_input_tokens` / `cache_read_input_tokens` are passed through into the OpenAI-shape response under `usage.cache_*` plus a `content_blocks` array on the message — only when `strictOpenAiCompliance` is false (`Portkey-AI/gateway@351692fd:src/providers/anthropic/chatComplete.ts:597-602` for content blocks; `:617-626` for usage).
- Anthropic stop reasons (`end_turn`, `tool_use`, `pause_turn`, `max_tokens`, `stop_sequence`) collapse to `stop / tool_calls / length` per a single `finishReasonMap` (`Portkey-AI/gateway@351692fd:src/providers/utils/finishReasonMap.ts:15-122`) when strict; otherwise the upstream raw enum value passes through.
- Reverse direction (provider stop reason → Anthropic-canonical) uses a separate `AnthropicFinishReasonMap` covering only Bedrock-Anthropic so far (`Portkey-AI/gateway@351692fd:src/providers/utils/finishReasonMap.ts:124-144`) — i.e. Anthropic-canonical mode is currently second-class for non-Anthropic upstreams.
- OpenAI logprobs: passed through unchanged (`Portkey-AI/gateway@351692fd:src/providers/openai/chatComplete.ts:84-90`); other vendors don't expose them, so the field is absent rather than synthesized.

**Fidelity gap visible in source**: when Anthropic returns `tool_use` content blocks, the `content_blocks` array filters them out before forwarding (`response.content.filter((item) => item.type !== 'tool_use')` at `Portkey-AI/gateway@351692fd:src/providers/anthropic/chatComplete.ts:599`) — so non-strict callers get text/thinking blocks but never the raw tool_use block; they only see `tool_calls` (OpenAI shape). This is a deliberate one-way collapse.

## Q3 Streaming

**TS Web Streams API**, not Node `Readable`. The core is `new TransformStream()` whose `readable` is returned to the client and whose `writable` is fed by an async IIFE that pulls from the upstream `response.body.getReader()`. Source: `Portkey-AI/gateway@351692fd:src/handlers/streamHandler.ts:300-411`.

**Chunk transformation = single transformer function, NOT a chain.** The `readStream` async generator pulls bytes, decodes UTF-8, splits the buffer on a per-provider `splitPattern` (`'\n\n'` for SSE, custom for others; resolved by `getStreamModeSplitPattern(proxyProvider, requestURL)` at `Portkey-AI/gateway@351692fd:src/handlers/streamHandler.ts:310`), and on each completed part calls *one* `transformFunction(part, fallbackChunkId, streamState, strictOpenAiCompliance, gatewayRequest)` (`Portkey-AI/gateway@351692fd:src/handlers/streamHandler.ts:189-202`). The transformer mutates `streamState` (a per-stream object initialized at `streamHandler.ts:151` as `const streamState = {};`) so subsequent chunks can reference accumulated metadata (model name, prompt token count, tool index — see Anthropic's use at `Portkey-AI/gateway@351692fd:src/providers/anthropic/chatComplete.ts:648-704`).

This means: *not* a transform-stream chain; *not* a pipe of `decode → split → translate → reencode`; instead, a single `for-await` loop that does all four in-line, with per-provider switches inside the loop body. The Bedrock branch uses a different generator `readAWSStream` because Bedrock's binary framing requires `readUInt32BE(buffer, offset)` parsing of length-prefixed chunks plus base64 payload decode (`Portkey-AI/gateway@351692fd:src/handlers/streamHandler.ts:38-137`).

**SSE encode/decode — self-implemented.** No `eventsource-parser` or similar library. Decoding is `chunk.startsWith('event: ping')` style string matching plus regex strips for known event names, e.g. `Portkey-AI/gateway@351692fd:src/providers/anthropic/chatComplete.ts:651-669`:

```
chunk = chunk.replace(/^event: content_block_delta[\r\n]*/, '');
chunk = chunk.replace(/^event: content_block_start[\r\n]*/, '');
chunk = chunk.replace(/^event: message_delta[\r\n]*/, '');
chunk = chunk.replace(/^event: message_start[\r\n]*/, '');
chunk = chunk.replace(/^event: error[\r\n]*/, '');
chunk = chunk.replace(/^data: /, '');
```

Encoding back to OpenAI SSE shape is `'data: ' + JSON.stringify(...) + '\n\n'` template literals (e.g. `Portkey-AI/gateway@351692fd:src/providers/anthropic/chatComplete.ts:710-728`). `[DONE]` sentinel hardcoded at `Portkey-AI/gateway@351692fd:src/providers/anthropic/chatComplete.ts:660`.

**First-chunk latency tweak**: the first emitted chunk waits 25ms before yield (`Portkey-AI/gateway@351692fd:src/handlers/streamHandler.ts:182-184`); subsequent chunks wait 1ms only when `proxyProvider === AZURE_OPEN_AI` (`:185-187`). This is to give Azure clients time to register listeners. HUAKAI should decide whether this is safe; on a high-throughput gateway it costs N×25ms.

**JSON-to-stream synthesis**: `handleJSONToStreamResponse` (`Portkey-AI/gateway@351692fd:src/handlers/streamHandler.ts:414-476`) lets a non-streaming upstream response be re-emitted as SSE chunks if the client asked for stream — used when a provider doesn't support streaming for a given endpoint. Detected via `Object.prototype.toString.call(responseTransformerFunction) === '[object GeneratorFunction]'` to distinguish generator vs array.

## Q4 Conditional Routing

**Routing decides BEFORE protocol translation.** Translation happens inside the leaf-most `tryPost` call after the recursion has resolved a single target. Source: `Portkey-AI/gateway@351692fd:src/handlers/handlerUtils.ts:476-834` — `tryTargetsRecursively` switches on `strategyMode` (`FALLBACK | LOADBALANCE | CONDITIONAL | SINGLE | default`) and recurses with a sub-target before any translation. Translation is invoked by `requestContext.transformToProviderRequestAndSave()` only inside `tryPost` (referenced at lines 330 and 362), which runs after recursion bottoms out.

**Routing inputs** (`ConditionalRouter` at `Portkey-AI/gateway@351692fd:src/services/conditionalRouter.ts:32-156`):
- **Metadata** from request header `x-portkey-metadata` parsed as JSON (`Portkey-AI/gateway@351692fd:src/handlers/handlerUtils.ts:728`).
- **Params** from request body (the canonical `Params` object — pre-translation).
- **URL pathname** (`c.req.path`).

These three sources are flattened into a context tree and queried by Mongo-style operator JSON: `$eq, $ne, $gt, $gte, $lt, $lte, $in, $nin, $regex, $and, $or` (`Portkey-AI/gateway@351692fd:src/services/conditionalRouter.ts:15-30`). `evaluateOperator` walks the operator object and returns boolean (`:92-135`); `evaluateQuery` recurses through `$and / $or` and key→operator pairs (`:64-90`); `getContextValue` dot-walks two levels deep only — `parts[0]?.[parts[1]]` (`:150-155`). So `metadata.user_tier` and `params.model` work, but `params.messages.0.content` does not — a deliberate shallow restriction.

**Does routing influence protocol translation?** Indirectly only. The routing layer picks a `provider` string; the translation layer keys on that string and calls the corresponding `ProviderConfig`. There is no awareness in routing of "this target costs more translation effort" or "this target's chunker is slower". Conditional routing can use `params.model` as a query input (so a router CAN say "if model starts with `claude-` → target B"), but the routing decision itself is pre-translation.

**Strategy modes — first-class semantics** (`Portkey-AI/gateway@351692fd:src/handlers/handlerUtils.ts:663-779`):
- `FALLBACK`: iterate targets in order; break when status code matches `onStatusCodes` *negated* (response code NOT in the retry list and `response.ok`) or the gateway-exception header is set. Stops cascading the moment a non-error is seen.
- `LOADBALANCE`: weighted random selection, weights default to 1, single target per request (no fan-out).
- `CONDITIONAL`: first-match wins; if no condition matches and `default:` is set, fall back to that target name; else throw.
- `SINGLE`: just unwraps `targets[0]`.
- default (no strategy): treat current target as a leaf and call `tryPost`.

**Circuit breaker** is layered above routing but inside the target group: if `currentInheritedConfig.id` is set (i.e. this target group has a CB id), filter out `target.isOpen === true` before strategy dispatch (`Portkey-AI/gateway@351692fd:src/handlers/handlerUtils.ts:646-658`). The `handleCircuitBreakerResponse` callback is invoked after `tryPost` (`:792-799`) — the actual breaker math lives in `c.get('handleCircuitBreakerResponse')` which is registered by external middleware; the gateway code only consults the result.

## Q5 Multi-modal

**Image normalization across providers** uses a content-item type in the canonical `Params.messages[].content[]` array: `{type, text, image_url: {url, detail, mime_type}, file: {file_data, file_id, file_name, file_url, mime_type}, input_audio: {data, format}, …}` — `Portkey-AI/gateway@351692fd:src/types/requestBody.ts:248-270`. The canonical is OpenAI's "image_url with optional data: URI" shape.

Per-vendor translation:
- **Anthropic** (`Portkey-AI/gateway@351692fd:src/providers/anthropic/chatComplete.ts:198-237`): inspect `image_url.url`; if it starts with `data:` parse the `data:<mime>;base64,<data>` URI and emit `{type: 'image' or 'document', source: {type: 'base64', media_type, data}}`; else emit `{type: 'image', source: {type: 'url', url}}`. PDFs are converted from `image_url` to Anthropic's `document` block based on the parsed mime type — `if (mediaType === fileExtensionMimeTypeMap.pdf) → 'document' else 'image'` at `:222-229`. A separate `transformAndAppendFileContentItem` handles the canonical `file` content type (`:239-266`) for explicit document upload.
- **Google / Vertex** (`Portkey-AI/gateway@351692fd:src/providers/google/chatComplete.ts:270-302`): `image_url.url` becomes `{inlineData: {mimeType, data}}` if base64, with a hardcoded `mimeType: 'image/jpeg'` fallback when the data URI prefix is missing (`:300-302`) — note the lossy fallback, an actual PNG would be mis-tagged. Reverse direction at `:678-682` and `:866-870` reconstructs `data:<mime>;base64,<data>` URLs.
- **OpenAI**: passes `image_url` through unchanged (no transform — the canonical IS OpenAI shape).

**Audio / file APIs** are handled per-endpoint, not in the chat-completion translation layer. Endpoint strings include `createSpeech, createTranscription, createTranslation, uploadFile, retrieveFile, retrieveFileContent, deleteFile, listFiles, createBatch, getBatchOutput, retrieveBatch, cancelBatch, listBatches` (`Portkey-AI/gateway@351692fd:src/providers/types.ts:96-110`). OpenAI's `createSpeech` config is 41 lines, `createTranscription` 14 lines (`wc -l` outputs above) — these reuse `transformToFormData` (`Portkey-AI/gateway@351692fd:src/services/transformToProviderRequest.ts:164-208`) because audio APIs require `multipart/form-data`. Bedrock's S3-backed file ops bypass the param table via `requestHandlers` (`Portkey-AI/gateway@351692fd:src/providers/bedrock/index.ts:92-96`).

**Audio chat input** (OpenAI's `audio: {voice, format}` and `modalities: ['text', 'audio']`) is exposed in `Params` (`Portkey-AI/gateway@351692fd:src/types/requestBody.ts:461-465`) and accepted in `OpenAIChatCompleteConfig` (`:109-114`). No reverse translation table exists for non-OpenAI providers — audio input is OpenAI-only at the chat-completion endpoint as of this commit.

**Realtime** (WebSocket-based bidirectional) gets its own handler (`src/handlers/realtimeHandler.ts`, 92 LoC) plus a per-event parser (`src/services/realtimeLlmEventParser.ts`, 165 LoC). This is a separate codepath from chat-completions translation.

## Q6 Error / Retry

**Error normalization** is per-provider-trivial. The canonical `ErrorResponse` is the OpenAI error shape: `{error: {message, type, param, code}, provider}` (`Portkey-AI/gateway@351692fd:src/providers/types.ts:266-274`). Each provider's error transform is a one-liner that maps upstream `{error: {message, type}}` into the canonical shape; e.g. Anthropic's transform is 19 lines total (`Portkey-AI/gateway@351692fd:src/providers/anthropic/utils.ts:5-18`) — it just copies `message` and `type`, sets `param: null, code: null`, and stamps the provider name. OpenAI's is even shorter: 14 lines, spread the existing error object through `generateErrorResponse` (`Portkey-AI/gateway@351692fd:src/providers/openai/utils.ts:4-14`). There is no central error-code registry; the canonical OpenAI types come through verbatim, non-OpenAI providers' types pass through as-is. So a caller seeing `error.type = 'overloaded_error'` knows it's Anthropic, but the caller has to do the mapping.

**Stop-reason normalization** is the place where Portkey *does* invest in a central table (`Portkey-AI/gateway@351692fd:src/providers/utils/finishReasonMap.ts:15-122`) — 8 vendors mapped to 5 OpenAI finish reasons. `transformFinishReason(finishReason, strictOpenAiCompliance)` returns the OpenAI value when strict and the upstream raw value when not (`Portkey-AI/gateway@351692fd:src/providers/utils.ts:73-84`). When the map misses a value, the function returns `FINISH_REASON.stop` (the safest collapse).

**Retry strategy** (`Portkey-AI/gateway@351692fd:src/handlers/retryHandler.ts:65-220`):
- Wrapped in `async-retry` — retries up to `retryCount` attempts, only when `statusCodesToRetry.includes(response.status)`.
- 200–204 → success, no retry.
- Other codes outside `statusCodesToRetry` → `bail(error)` — propagates immediately, no retry.
- Special 429 path (`:108-152`): when `followProviderRetry` is true, look up a `Retry-After` header from `POSSIBLE_RETRY_STATUS_HEADERS` list; convert seconds→ms; if the value exceeds `MAX_RETRY_LIMIT_MS` or remaining budget, set `retrySkipped = true` and clear `_timeouts` to abort backoff; else override the rate limiter's backoff timeouts to `0` and `setTimeout(retryAfter)` before the next throw. So a 429 with `Retry-After: 30` skips backoff when within budget but gives up when over budget — *not infinite retry*.
- Timeout path (`fetchWithTimeout` at `:4-50`) creates a 408 Response on `AbortError` (signal aborted by `AbortController`) — converted directly to a JSON-shape OpenAI error with `type: 'timeout_error'`. `ConnectTimeoutError` becomes a 503.
- Unknown exception → 500 with `Message: ... Cause: ... Name: ...` body.

**Retry × Streaming**: streaming retries are *not* enabled per stream chunk. The retry layer sits above `tryPost`, which sees the upstream `response` object. Once a stream is opened (response.ok / 200), the retry handler returns and the stream is plumbed through `handleStreamingMode` (`Portkey-AI/gateway@351692fd:src/handlers/streamHandler.ts:300`). Mid-stream failures (network drop after first chunk) are not retried by the retry handler — they raise inside the IIFE at `streamHandler.ts:343` / `:377` and only `console.error` ('Error during stream processing') runs; the stream just closes. So Portkey **does not synthesize "fallback continuation prompt" for mid-stream failure** — that's a HUAKAI gap to claim against.

## HUAKAI 借鉴可行性 (clean-room 升级 delta)

**Pattern 1: declarative param table (架构升级 candidate)**
- Upstream: Portkey's `ProviderConfig = {canonicalParam: {param, default, min, max, transform}}` table (`Portkey-AI/gateway@351692fd:src/providers/types.ts:19-32`); LiteLLM uses a similar lookup map (already cited in `docs/research/2026-05-09-source-read-oneapi-portkey-litellm.md`).
- HUAKAI delta: combine declarative table + zod runtime validation per param (Portkey only zod-validates inbound config not provider transforms); add explicit fingerprint-preserving fields — `prompt_cache_key`, `safety_identifier`, `verbosity` (which Portkey already added — `Portkey-AI/gateway@351692fd:src/providers/openai/chatComplete.ts:124-132`) plus HUAKAI-specific fingerprint hardening (R5/R7/R8) that Portkey doesn't have.
- Dimension(s): 架构 (declarative shape) + 算法 (zod schema-driven validation) + 生态 (fingerprint-aware param surface).

**Pattern 2: bi-canonical (OpenAI + Anthropic) coexistence (架构升级)**
- Upstream: Portkey runs `chatComplete` (OpenAI canonical) and `messages` (Anthropic canonical) as parallel route surfaces with separate base configs (`Portkey-AI/gateway@351692fd:src/providers/anthropic-base/messages.ts:3-67` and `:src/types/requestBody.ts:427-488`).
- HUAKAI delta: HUAKAI is already in this territory with the chat handler split (per `project_slice_order_post_n5a`); upgrade is *third* canonical for codex / Claude-Code-CLI native shape so the round-trip is lossless for cache-control + thinking blocks (Portkey collapses `tool_use` content blocks at `:src/providers/anthropic/chatComplete.ts:599`).
- Dimension(s): 架构 (additional canonical surface) + 生态 (no-loss for HUAKAI's primary 4 vendor real-test scope).

**Pattern 3: SSE chunk transform with stream state (算法升级)**
- Upstream: `transformFunction(chunk, fallbackId, streamState, strictOpenAiCompliance, gatewayRequest)` signature with single mutating state object (`Portkey-AI/gateway@351692fd:src/handlers/streamHandler.ts:149-202`); Anthropic's transform tracks `model`, `usage`, `toolIndex` across chunks (`:src/providers/anthropic/chatComplete.ts:648-704`).
- HUAKAI delta: extend stream state with **mid-stream-fallback continuation prompt** synthesis — when a chunk indicates upstream failure, emit a continuation prompt to a fallback target that includes the partial assistant content already streamed. Portkey lacks this entirely; LiteLLM does fallback only at request boundary not mid-stream (`docs/research/2026-05-09-source-read-oneapi-portkey-litellm.md` LiteLLM section).
- Dimension(s): 算法 (stream state extended with continuation synthesis) + 架构 (fallback decision moved into stream layer not just route layer) + 生态 (vendor-sliced metric for "mid-stream-fallback rate").

**Pattern 4: conditional routing with Mongo-style operators (算法升级)**
- Upstream: `ConditionalRouter` evaluates `$eq/$ne/$gt/$gte/$lt/$lte/$in/$nin/$regex/$and/$or` against `metadata + params + url.pathname` context (`Portkey-AI/gateway@351692fd:src/services/conditionalRouter.ts:15-156`).
- HUAKAI delta: HUAKAI already has PASR cache-aware scoring; extend conditional with **score input** (e.g. `$gt: {score.locality: 0.7}`) so an operator can dispatch by cache locality estimate, not just by metadata equality. Portkey's router is shallow `parts[0]?.[parts[1]]` (`:150-155`) — HUAKAI can lift to deep path with explicit allowlist.
- Dimension(s): 算法 (cache-locality as routing input) + 架构 (deeper context tree) + 生态 (router-decision metric per vendor slice).

**Pattern 5: requestHandlers escape hatch (架构升级)**
- Upstream: `requestHandlers: Partial<Record<endpointStrings, RequestHandler>>` lets a provider entirely bypass the param table for endpoints that don't fit (Bedrock S3-backed uploads, Vertex batch). Source: `Portkey-AI/gateway@351692fd:src/providers/types.ts:131-143`, `:src/providers/bedrock/index.ts:92-97`.
- HUAKAI delta: import this escape pattern; HUAKAI has no equivalent today (per AGENTS.md current scope) — needed for Bedrock real-upstream smoke once Owner has AWS creds (currently blocked per `project_no_aws_credentials`). Pre-build the slot now so Bedrock arrival is config-only, not refactor.
- Dimension(s): 架构 (per-endpoint adapter override slot).

**Pattern 6: zod inbound config validation (生态升级)**
- Upstream: `Portkey-AI/gateway@351692fd:src/middlewares/requestValidator/schema/config.ts:1+` validates `config:` header with full strategy/conditions/operator schema.
- HUAKAI delta: adopt for HUAKAI admin-config write path (Slice 4b admin keys) so misconfigured operator JSON fails fast at write, not silently at routing time. Combine with zod source-of-truth for the canonical `Params` so the param table can be auto-generated from the schema (Portkey's table is hand-typed).
- Dimension(s): 生态 (admin-config validation surface) + 算法 (codegen param table from schema).

**What Portkey explicitly does NOT do (HUAKAI gap-list claim):**
- No mid-stream fallback continuation (confirmed: `streamHandler.ts:343 / :377` only `console.error`).
- No fingerprint preservation across vendor translation — `strictOpenAiCompliance` is a binary; no per-field allow/deny.
- No per-vendor metric slicing in observability (no evidence in `src/apm/` directory for vendor-tagged counters; needs separate read to confirm — flagged as not-yet-verified rather than asserted).
- No content-block round-trip preservation when the canonical is OpenAI-shape (Anthropic `tool_use` blocks are filtered out at `:src/providers/anthropic/chatComplete.ts:599`).
- No score-based routing — load-balance is plain weighted random (`handlerUtils.ts:704-722`).

---

## Source files read

- `~/refs/portkey-gateway/src/providers/index.ts` (1-51)
- `~/refs/portkey-gateway/src/providers/types.ts` (full)
- `~/refs/portkey-gateway/src/types/requestBody.ts` (full)
- `~/refs/portkey-gateway/src/providers/anthropic/api.ts` (full)
- `~/refs/portkey-gateway/src/providers/anthropic/chatComplete.ts` (full)
- `~/refs/portkey-gateway/src/providers/anthropic/index.ts` (full)
- `~/refs/portkey-gateway/src/providers/anthropic/messages.ts` (full)
- `~/refs/portkey-gateway/src/providers/anthropic/utils.ts` (full)
- `~/refs/portkey-gateway/src/providers/anthropic-base/messages.ts` (full)
- `~/refs/portkey-gateway/src/providers/openai/chatComplete.ts` (1-200)
- `~/refs/portkey-gateway/src/providers/openai/utils.ts` (full)
- `~/refs/portkey-gateway/src/providers/utils.ts` (full)
- `~/refs/portkey-gateway/src/providers/utils/finishReasonMap.ts` (full)
- `~/refs/portkey-gateway/src/providers/bedrock/index.ts` (80-110)
- `~/refs/portkey-gateway/src/handlers/chatCompletionsHandler.ts` (full)
- `~/refs/portkey-gateway/src/handlers/streamHandler.ts` (full)
- `~/refs/portkey-gateway/src/handlers/retryHandler.ts` (full)
- `~/refs/portkey-gateway/src/handlers/handlerUtils.ts` (460-860 + grep)
- `~/refs/portkey-gateway/src/services/conditionalRouter.ts` (full)
- `~/refs/portkey-gateway/src/services/transformToProviderRequest.ts` (full)
- `~/refs/portkey-gateway/package.json` (zod dep verification)
- `~/refs/portkey-gateway/src/middlewares/requestValidator/schema/config.ts` (1-100 grep)
- Directory listings: `src/`, `src/providers/`, `src/handlers/`, `src/types/`, `src/services/`, `src/handlers/services/`

Lane: specifier (axis-3 protocol translation focus)
Agent: general-purpose
UTC timestamp: 2026-05-09T14:57Z
