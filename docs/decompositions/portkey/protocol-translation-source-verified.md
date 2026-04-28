# Portkey Protocol Translation - Source-Verified (F-PROTO-001 Cross-Reference)

| Field | Value |
| --- | --- |
| Status | Specifier-lane source-verified second-source pass |
| Author | Codex |
| Date | 2026-04-28 |
| Reference | Portkey AI Gateway at commit `351692fd9236af222168134b416924fae0bdba23` |
| Lane | Specifier-lane source verification |
| License check | MIT license verified locally in `.omc/reference-src/portkey-gateway/LICENSE:1` |
| Clean-room note | This file records observed behavior and architecture. It is not an implementer-lane spec and must not be used as source-code guidance. |
| Compared against | `docs/decompositions/sub2api/protocol-translation-source-verified.md` |

Source files read directly under `.omc/reference-src/portkey-gateway/`:

- `src/index.ts:132`, `src/index.ts:147`, `src/index.ts:153`, `src/index.ts:233`
- `src/handlers/chatCompletionsHandler.ts:16`, `src/handlers/messagesHandler.ts:16`, `src/handlers/modelResponsesHandler.ts:8`
- `src/handlers/handlerUtils.ts:288`, `src/handlers/handlerUtils.ts:476`, `src/handlers/handlerUtils.ts:662`, `src/handlers/handlerUtils.ts:783`, `src/handlers/handlerUtils.ts:1231`
- `src/handlers/responseHandlers.ts:38`, `src/handlers/responseHandlers.ts:61`, `src/handlers/responseHandlers.ts:71`, `src/handlers/responseHandlers.ts:102`
- `src/handlers/streamHandler.ts:61`, `src/handlers/streamHandler.ts:139`, `src/handlers/streamHandler.ts:300`, `src/handlers/streamHandler.ts:414`
- `src/handlers/services/requestContext.ts:16`, `src/handlers/services/requestContext.ts:89`, `src/handlers/services/requestContext.ts:99`, `src/handlers/services/requestContext.ts:211`
- `src/handlers/services/providerContext.ts:13`, `src/handlers/services/providerContext.ts:28`, `src/handlers/services/providerContext.ts:96`
- `src/providers/types.ts:19`, `src/providers/types.ts:38`, `src/providers/types.ts:47`, `src/providers/types.ts:85`, `src/providers/types.ts:149`, `src/providers/types.ts:171`, `src/providers/types.ts:256`, `src/providers/types.ts:439`
- `src/providers/index.ts:78`
- `src/services/transformToProviderRequest.ts:75`, `src/services/transformToProviderRequest.ts:143`, `src/services/transformToProviderRequest.ts:234`
- `src/providers/openai/index.ts:50`, `src/providers/openai/api.ts:31`
- `src/providers/open-ai-base/index.ts:50`, `src/providers/open-ai-base/index.ts:240`, `src/providers/open-ai-base/index.ts:350`, `src/providers/open-ai-base/index.ts:386`, `src/providers/open-ai-base/index.ts:536`
- `src/providers/open-ai-base/createModelResponse.ts:40`, `src/providers/open-ai-base/createModelResponse.ts:159`
- `src/providers/open-ai-base/helpers.ts:103`, `src/providers/open-ai-base/helpers.ts:133`, `src/providers/open-ai-base/helpers.ts:147`
- `src/providers/anthropic/index.ts:19`, `src/providers/anthropic/api.ts:27`, `src/providers/anthropic/chatComplete.ts:141`, `src/providers/anthropic/chatComplete.ts:268`, `src/providers/anthropic/chatComplete.ts:538`, `src/providers/anthropic/chatComplete.ts:636`
- `src/providers/anthropic/messages.ts:9`, `src/providers/anthropic-base/messages.ts:3`, `src/providers/anthropic-base/utils/streamGenerator.ts:145`
- `src/providers/utils.ts:73`, `src/providers/utils.ts:91`, `src/providers/utils/finishReasonMap.ts:15`
- `src/providers/cohere/chatComplete.ts:170`, `src/providers/cohere/chatComplete.ts:217`
- `src/providers/google/chatComplete.ts:626`, `src/providers/google/chatComplete.ts:735`
- `src/providers/bedrock/messages.ts:430`, `src/providers/bedrock/messages.ts:546`

## 0. Direct Answers To The Assigned Questions

**0.a - Canonical intermediate?**

Portkey does **not** use OpenAI Responses as a universal intermediate for the protocol path studied here. The chat-completions route calls `tryTargetsRecursively(..., 'chatComplete', ...)`, the Anthropic Messages route calls the same executor with `messages`, and the Responses route calls `modelResponsesHandler('createModelResponse', ...)` as a separate route (`src/handlers/chatCompletionsHandler.ts:21-29`, `src/handlers/messagesHandler.ts:21-29`, `src/handlers/modelResponsesHandler.ts:17-25`). The provider request is resolved from `ProviderConfigs[provider][fn]` or `getConfig(...)[fn]`, so the active endpoint is the local canonical surface (`src/services/transformToProviderRequest.ts:149-161`).

**0.b - Streaming strategy?**

Portkey uses chunk transforms with per-stream mutable state. `readStream()` splits the upstream body, passes each chunk to the provider's stream transformer, and yields the returned string (`src/handlers/streamHandler.ts:139-199`). Anthropic chat streaming branches on parsed chunk type and emits Chat Completions chunks directly; it does not first emit Responses SSE events (`src/providers/anthropic/chatComplete.ts:636-831`). Native Anthropic Messages streaming is pass-through when no `stream-messages` transformer exists (`src/providers/anthropic/index.ts:25-31`, `src/handlers/streamHandler.ts:200-201`).

**0.c - Adapter count and shape?**

The local commit has 72 entries in the `Providers` map, counted from the object at `src/providers/index.ts:78-151`. Adapter shape is a provider object with endpoint request configs, `api` config, optional request handlers/transforms, optional dynamic `getConfig()`, and `responseTransforms` (`src/providers/types.ts:19-83`, `src/providers/types.ts:149-160`, `src/providers/openai/index.ts:50-100`, `src/providers/anthropic/index.ts:19-31`).

**0.d - Usage extraction?**

Usage extraction is adapter-local, not centralized. Anthropic stream usage is split between `message_start` and `message_delta` (`src/providers/anthropic/chatComplete.ts:695-765`); Cohere stream usage arrives on `message-end` and may use either token counts or billed units (`src/providers/cohere/chatComplete.ts:240-254`); Google usage comes from `usageMetadata` (`src/providers/google/chatComplete.ts:767-889`); Bedrock Messages stream converts Bedrock usage into Anthropic `message_delta` usage (`src/providers/bedrock/messages.ts:596-616`).

**0.e - Anthropic subtleties?**

Portkey handles `tool_use` interleaving in the Chat Completions stream by maintaining `streamState.toolIndex` and emitting `tool_calls` deltas (`src/providers/anthropic/chatComplete.ts:648-649`, `src/providers/anthropic/chatComplete.ts:768-797`). It maps Anthropic `max_tokens` to OpenAI `length` in strict mode (`src/providers/utils/finishReasonMap.ts:17-21`, `src/providers/utils.ts:73-84`). It preserves `signature_delta` for native/cached Anthropic Messages streams, but strict Chat Completions cannot expose it; non-strict Chat Completions may carry provider content-block data when it is not a tool call (`src/providers/anthropic-base/utils/streamGenerator.ts:110-122`, `src/providers/anthropic-base/utils/streamGenerator.ts:165-169`, `src/providers/anthropic/chatComplete.ts:818-821`).

Scope boundaries for this pass:

- Verified protocol translation for Chat Completions, Anthropic Messages, OpenAI Responses route handling, stream handling, adapter shape, finish reasons, and usage extraction.
- Did not mine guardrails, semantic cache, auth, billing ledger, RBAC, or admin UI in this file.
- Did not evaluate runtime tests or live provider behavior; all conclusions are source evidence from the local shallow clone.
- Did not produce implementer-facing algorithms; HUAKAI implementation must restate any accepted behavior in local design language.
- Did not update feature parity, evidence ledger, or risk register because the Owner explicitly requested only the new decomposition file.

## 1. Portkey's Canonical Model Architecture

**Finding:** Portkey does **not** use OpenAI Responses as the universal canonical intermediate for chat and Anthropic translation. It uses a **route-local canonical surface plus provider adapter fan-out**:

- `/v1/chat/completions` enters `chatCompletionsHandler`, which calls `tryTargetsRecursively(..., 'chatComplete', ...)`; the endpoint string, not Responses, drives the adapter selection (`src/index.ts:143-147`, `src/handlers/chatCompletionsHandler.ts:16-29`).
- `/v1/messages` enters `messagesHandler`, which calls the same target executor with endpoint `messages`; this is an Anthropic-format route, separate from chat completions (`src/index.ts:132-135`, `src/handlers/messagesHandler.ts:16-29`).
- `/v1/responses` enters `modelResponsesHandler('createModelResponse', 'POST')`; this is an exposed OpenAI Responses route, not the internal hub for all chat and messages translation (`src/index.ts:233-238`, `src/handlers/modelResponsesHandler.ts:8-25`).

The request-side canonical object is the incoming endpoint's request shape plus endpoint name:

- `RequestContext.params` merges the request body with override parameters and exposes the endpoint as `requestContext.endpoint` (`src/handlers/services/requestContext.ts:22-37`, `src/handlers/services/requestContext.ts:51-60`).
- `RequestContext.transformToProviderRequestAndSave()` calls `transformToProviderRequest(provider, params, requestBody, endpoint, ...)` (`src/handlers/services/requestContext.ts:211-223`).
- `transformToProviderRequestJSON()` resolves `ProviderConfigs[provider][fn]` or a dynamic `getConfig(...)[fn]`, then applies the provider config table (`src/services/transformToProviderRequest.ts:143-161`).
- `transformUsingProviderConfig()` loops over provider parameter config rows and writes renamed/defaulted/min-max-clamped values into a provider-specific request body (`src/services/transformToProviderRequest.ts:75-126`).

The response-side canonical object is likewise route-local:

- `responseHandler()` loads `Providers[provider].responseTransforms`, then uses `stream-${responseTransformer}` for streaming success responses and `responseTransformer` for non-streaming responses (`src/handlers/responseHandlers.ts:61-77`).
- The target executor passes the current endpoint string as `responseTransformer` when mapping the provider response (`src/handlers/handlerUtils.ts:1231-1248`).
- Generic OpenAI-compatible response wrappers exist for chat completions, completions, embeddings, speech, and OpenAI Responses, but they wrap provider-specific custom transforms rather than forcing all providers through Responses (`src/providers/open-ai-base/index.ts:350-384`, `src/providers/open-ai-base/index.ts:386-420`, `src/providers/open-ai-base/index.ts:536-595`).

**Architecture label for HUAKAI:** Portkey is **endpoint-as-canonical fan-out**, not Responses-as-hub and not full M x N protocol-pair translation. Adding a provider requires implementing that provider's endpoint configs and response transforms for each exposed route it supports (`src/providers/types.ts:85-120`, `src/providers/types.ts:149-160`).

## 2. Streaming Translation Strategy

Portkey's generic streaming path is chunk-oriented:

- `handleStreamingMode()` creates a `TransformStream`, reads the provider response body, and writes transformed chunks to the client (`src/handlers/streamHandler.ts:300-320`, `src/handlers/streamHandler.ts:365-375`).
- `readStream()` buffers text until the provider split pattern is found, calls `transformFunction(part, fallbackChunkId, streamState, strictOpenAiCompliance, gatewayRequest)`, and yields the returned string if present (`src/handlers/streamHandler.ts:139-152`, `src/handlers/streamHandler.ts:175-199`).
- A mutable `streamState` object is created per stream and passed into every chunk transform (`src/handlers/streamHandler.ts:151`, `src/handlers/streamHandler.ts:189-195`).
- For Bedrock AWS event streams, `readAWSStream()` decodes AWS event payloads and passes each payload to the same transform function pattern (`src/handlers/streamHandler.ts:61-70`, `src/handlers/streamHandler.ts:117-130`).

Anthropic chat-completion streaming is not a Responses-event state machine:

- The Anthropic chat stream transformer removes Anthropic SSE event labels, parses the JSON payload, and returns OpenAI Chat Completions chunks (`src/providers/anthropic/chatComplete.ts:636-671`, `src/providers/anthropic/chatComplete.ts:806-829`).
- It explicitly drops `event: ping` and `event: content_block_stop` by returning `undefined` (`src/providers/anthropic/chatComplete.ts:651-657`).
- It maps `event: message_stop` to `data: [DONE]` (`src/providers/anthropic/chatComplete.ts:659-661`).
- It records input/cache usage and model from `message_start`, then returns an assistant-role chat chunk (`src/providers/anthropic/chatComplete.ts:695-729`).
- It uses `message_delta` usage to emit a final chat chunk with `finish_reason` and normalized usage (`src/providers/anthropic/chatComplete.ts:732-765`).
- It maps `content_block_start` for `tool_use` to an OpenAI `tool_calls` delta and maps `content_block_delta.partial_json` to tool-call argument deltas (`src/providers/anthropic/chatComplete.ts:768-797`).
- It maps text deltas into `choices[].delta.content`; in non-strict mode it also carries a `content_blocks` array for non-tool chunks (`src/providers/anthropic/chatComplete.ts:798-829`).

Native Anthropic Messages streaming is mostly pass-through for the Anthropic provider:

- `responseHandler()` only chooses a streaming transformer if `Providers[provider].responseTransforms['stream-' + responseTransformer]` exists (`src/handlers/responseHandlers.ts:71-77`).
- Anthropic's provider config defines `messages` but does not define `stream-messages` (`src/providers/anthropic/index.ts:25-31`).
- With no transform function, `readStream()` yields the original chunk plus split pattern (`src/handlers/streamHandler.ts:200-201`).

There is no generic synthetic stream finalizer comparable to Sub2API's `Finalize...` path:

- On upstream EOF, `readStream()` emits only leftover buffered text through the transformer, then breaks (`src/handlers/streamHandler.ts:153-169`).
- `handleStreamingMode()` closes the writer in `finally`, but it does not synthesize `[DONE]`, `message_stop`, or `response.completed` (`src/handlers/streamHandler.ts:376-380`).
- Provider-specific cached JSON-to-stream paths do synthesize terminal events, but only for JSON cache hits (`src/handlers/responseHandlers.ts:81-97`, `src/handlers/streamHandler.ts:414-475`).

## 3. Provider Adapter Interface And Fan-Out

Portkey's adapter shape is configuration-first:

- `ParameterConfig` maps a gateway parameter to a provider parameter, with optional default/min/max/transform behavior (`src/providers/types.ts:19-32`).
- `ProviderConfig` is a map from request parameter names to one or more `ParameterConfig` entries (`src/providers/types.ts:38-41`).
- `ProviderAPIConfig` supplies provider-specific headers, base URL, endpoint selection, optional form-data detection, and optional proxy endpoint mapping (`src/providers/types.ts:47-83`).
- `endpointStrings` enumerates the route functions a provider can support, including chat completions, completions, embeddings, streaming chat, Anthropic messages, OpenAI Responses, audio, images, files, batches, realtime, and finetunes (`src/providers/types.ts:85-120`).
- `ProviderConfigs` allows arbitrary provider config objects plus optional `requestHandlers` and dynamic `getConfig()` selection (`src/providers/types.ts:149-160`).

The provider catalog is broad:

- The `Providers` object contains **72 provider entries** in the local commit; the map starts at `src/providers/index.ts:78` and ends at `src/providers/index.ts:151`.
- Provider entries point to provider modules such as OpenAI, Anthropic, Bedrock, Google, Vertex, Cohere, Mistral, DeepSeek, OpenRouter, Workers AI, and many OpenAI-compatible providers (`src/providers/index.ts:78-151`).

Representative adapter examples:

- OpenAI's provider object includes endpoint configs, an `api` config, request transforms for file upload, request handlers for batch output, and response transforms for many endpoints (`src/providers/openai/index.ts:50-100`).
- Anthropic's provider object includes `complete`, `chatComplete`, `messages`, `messagesCountTokens`, `api`, and response transforms for completion, chat-completion, stream-chat-completion, and messages (`src/providers/anthropic/index.ts:19-31`).
- OpenAI API endpoint routing returns `/chat/completions` for `chatComplete` and preserves base paths for `/v1/responses` operations (`src/providers/openai/api.ts:31-88`).
- Anthropic API endpoint routing maps Portkey `chatComplete` to Anthropic `/messages`, and maps Portkey `messages` to the same Anthropic `/messages` endpoint (`src/providers/anthropic/api.ts:27-39`).

This is the primary Portkey strength relative to Sub2API's protocol package: Portkey has a reusable provider-adapter chassis and a large fan-out surface. It is weaker as a cross-protocol semantic normalizer because each route/provider pair can implement slightly different field preservation, usage, and stream finalization behavior.

## 4. Usage Extraction Strategy

Portkey does not have one central usage extractor for all protocols. Usage normalization is embedded in provider response transformers.

Common target shape:

- The OpenAI-style base response type includes `usage.prompt_tokens`, `usage.completion_tokens`, `usage.total_tokens`, optional token-detail objects, and Anthropic cache-token fields (`src/providers/types.ts:171-194`).
- Chat completion responses extend that base response (`src/providers/types.ts:256-260`).

Anthropic chat-completion usage:

- Non-streaming Anthropic response usage is converted from `input_tokens`, `output_tokens`, `cache_creation_input_tokens`, and `cache_read_input_tokens` into OpenAI-style usage; Portkey adds cache tokens into `total_tokens` and exposes `prompt_tokens_details.cached_tokens` (`src/providers/anthropic/chatComplete.ts:554-627`).
- Streaming Anthropic chat records prompt/cache tokens on `message_start` (`src/providers/anthropic/chatComplete.ts:695-709`).
- Streaming Anthropic chat emits final usage on `message_delta`, adding cached input, cache creation input, and output tokens into `total_tokens` (`src/providers/anthropic/chatComplete.ts:732-765`).

Cohere chat-completion usage:

- Non-streaming Cohere chooses `usage.tokens` first, then `usage.billed_units`, and falls back to zero (`src/providers/cohere/chatComplete.ts:170-179`).
- Streaming Cohere extracts final usage from the `message-end` event's nested `delta.usage` shape, again preferring token counts and falling back to billed units (`src/providers/cohere/chatComplete.ts:240-254`).

Google chat-completion usage:

- Non-streaming Google maps `usageMetadata.promptTokenCount`, `candidatesTokenCount`, `totalTokenCount`, `thoughtsTokenCount`, `cachedContentTokenCount`, and audio-token modality details into OpenAI-style usage/detail fields (`src/providers/google/chatComplete.ts:626-728`).
- Streaming Google emits usage only when `parsedChunk.usageMetadata?.candidatesTokenCount` is present, carrying the same detail mapping into the streamed chunk (`src/providers/google/chatComplete.ts:767-889`).

Bedrock Anthropic-Messages usage:

- Bedrock's Messages response maps Bedrock usage fields into Anthropic `input_tokens`, `output_tokens`, `cache_read_input_tokens`, and `cache_creation_input_tokens` (`src/providers/bedrock/messages.ts:445-464`).
- Bedrock's Messages stream transform emits Anthropic `message_delta` usage from the final Bedrock usage chunk and then emits `message_stop` (`src/providers/bedrock/messages.ts:596-616`).

Implication for HUAKAI:

- KEEP Portkey's per-provider awareness of usage shapes; it captures provider-specific realities that a naive generic extractor would miss.
- IMPROVE by centralizing the normalized Usage Record contract and making each adapter produce that contract explicitly. Portkey's usage fields are normalized in response chunks, but the extraction logic is scattered across provider modules.
- AVOID unconditional usage emission semantics. Anthropic chat streaming emits usage in the final Portkey chat chunk without checking a Chat Completions `stream_options.include_usage` equivalent (`src/providers/anthropic/chatComplete.ts:732-765`), while OpenAI-native streaming is largely pass-through.

## 5. Sub2API Vs Portkey Comparison Matrix

| # | Sub2API-confirmed behavior | Portkey equivalent | Mark | Portkey evidence |
| --- | --- | --- | --- | --- |
| 1 | OpenAI Responses is the canonical intermediate hub. | Portkey exposes `/v1/responses`, but chat/messages use endpoint-local adapters rather than Responses as the hub. | DIFFERENT-PATTERN | `src/index.ts:233-238`; `src/handlers/chatCompletionsHandler.ts:21-29`; `src/handlers/messagesHandler.ts:21-29`; `src/services/transformToProviderRequest.ts:143-161` |
| 2 | M+N translators around a canonical Responses surface. | Provider fan-out by endpoint: `ProviderConfigs[provider][endpoint]` plus response transform lookup. | DIFFERENT-PATTERN | `src/providers/types.ts:85-120`; `src/providers/types.ts:149-160`; `src/handlers/responseHandlers.ts:61-77` |
| 3 | Request translators are explicit protocol-to-protocol functions. | Request translation is table-driven parameter mapping with optional field transforms. | DIFFERENT-PATTERN | `src/providers/types.ts:19-41`; `src/services/transformToProviderRequest.ts:75-126` |
| 4 | Anthropic SSE becomes Responses SSE, then may become Chat chunks. | Anthropic SSE is parsed directly into Chat Completions chunks for `/chat/completions`. | DIFFERENT-PATTERN | `src/providers/anthropic/chatComplete.ts:636-671`; `src/providers/anthropic/chatComplete.ts:806-829` |
| 5 | Handles Anthropic `message_start`. | Handles `message_start` for model/usage state and emits assistant-role chat chunk. | SAME-BEHAVIOR / DIFFERENT-FORM | `src/providers/anthropic/chatComplete.ts:699-729` |
| 6 | Handles Anthropic `content_block_start` for text/tool/reasoning item lifecycle. | Handles `tool_use` starts for tool-call index; non-tool starts become generic content block data only in non-strict mode. | DIFFERENT-PATTERN | `src/providers/anthropic/chatComplete.ts:768-803`; `src/providers/anthropic/chatComplete.ts:818-821` |
| 7 | Handles `content_block_delta.text_delta`. | Emits Chat Completions `delta.content` from `parsedChunk.delta.text`. | SAME-BEHAVIOR / DIFFERENT-FORM | `src/providers/anthropic/chatComplete.ts:798-829` |
| 8 | Handles `content_block_delta.input_json_delta` for tool arguments. | Emits OpenAI `tool_calls[].function.arguments` deltas from `partial_json`. | SAME-BEHAVIOR / DIFFERENT-FORM | `src/providers/anthropic/chatComplete.ts:775-797` |
| 9 | Handles or deliberately drops `signature_delta` in Responses conversion. | Native Messages pass-through preserves provider chunks; cached Messages JSON-to-stream emits `signature_delta`; Chat Completions strict mode does not expose it. | DIFFERENT-PATTERN | `src/providers/anthropic-base/utils/streamGenerator.ts:110-122`; `src/providers/anthropic-base/utils/streamGenerator.ts:165-169`; `src/providers/anthropic/index.ts:25-31`; `src/handlers/streamHandler.ts:200-201`; `src/providers/anthropic/chatComplete.ts:818-821` |
| 10 | Tracks an open Responses output item and closes it idempotently. | Chat Completions stream has no output-item lifecycle; it returns chunk deltas and relies on provider `message_stop` for `[DONE]`. | NOT-PRESENT | `src/providers/anthropic/chatComplete.ts:659-661`; `src/providers/anthropic/chatComplete.ts:806-829` |
| 11 | Synthetic finalization if upstream stream ends without terminal event. | No generic EOF finalizer; EOF closes writer after leftover handling. | NOT-PRESENT | `src/handlers/streamHandler.ts:153-169`; `src/handlers/streamHandler.ts:376-380` |
| 12 | Usage is accumulated from Anthropic `message_start` and `message_delta`. | Same concept for Anthropic chat: stores prompt/cache usage in stream state and emits final output usage later. | SAME | `src/providers/anthropic/types.ts:1-13`; `src/providers/anthropic/chatComplete.ts:695-765` |
| 13 | `max_tokens` maps to Responses incomplete, but Chat finish can lose `length`. | Strict OpenAI finish mapping converts Anthropic `max_tokens` to Chat `length`; non-strict mode preserves provider stop reason. | IMPLEMENTED BETTER FOR CHAT | `src/providers/utils/finishReasonMap.ts:15-21`; `src/providers/utils.ts:73-84` |
| 14 | Anthropic `pause_turn` is not fully modeled in the Sub2API pass. | Portkey enumerates `pause_turn`; strict OpenAI mode maps it to `stop`, while non-strict preserves `pause_turn`. | DIFFERENT-PATTERN | `src/providers/anthropic/types.ts:25-32`; `src/providers/utils/finishReasonMap.ts:17-21`; `src/providers/utils.ts:73-84` |
| 15 | Tool-call ID translation is explicit and bidirectional. | Portkey preserves tool IDs across Anthropic chat transforms; no separate prefix/bijection adapter is visible. | DIFFERENT-PATTERN | `src/providers/anthropic/chatComplete.ts:164-175`; `src/providers/anthropic/chatComplete.ts:184-195`; `src/providers/anthropic/chatComplete.ts:572-583` |
| 16 | Buffered Anthropic response creates Responses outputs, with a defensive empty-message fallback. | Anthropic non-streaming chat concatenates text into one Chat message and gathers tool uses into `tool_calls`; no Responses output item fallback exists. | DIFFERENT-PATTERN / NOT-PRESENT | `src/providers/anthropic/chatComplete.ts:565-584`; `src/providers/anthropic/chatComplete.ts:586-628` |
| 17 | Streaming preserves text/tool interleaving through output indices. | Chat stream can interleave text chunks and tool-call deltas, but Chat Completions cannot express Responses output-item lifecycle. | DIFFERENT-PATTERN | `src/providers/anthropic/chatComplete.ts:768-829` |
| 18 | Unknown Anthropic event/delta types are silently dropped in the Sub2API pass. | Portkey strips known event labels and parses the remaining payload; unknown valid payloads may become generic empty/content-block chunks, and malformed chunks fall into stream handler catch/close. | DIFFERENT-PATTERN, NOT BETTER | `src/providers/anthropic/chatComplete.ts:651-671`; `src/providers/anthropic/chatComplete.ts:798-829`; `src/handlers/streamHandler.ts:376-380` |
| 19 | Mid-stream Anthropic error handling is not central in the Sub2API summary. | Anthropic stream `type: error` maps to a Chat chunk with provider error type as finish reason and then `[DONE]`. | IMPLEMENTED | `src/providers/anthropic/chatComplete.ts:673-692` |
| 20 | Cached JSON-to-stream for Responses is not the core Sub2API path. | Portkey can synthesize OpenAI Responses stream events from cached JSON, but this is a cache path, not a provider-translation hub. | DIFFERENT-PATTERN | `src/handlers/responseHandlers.ts:81-97`; `src/providers/open-ai-base/createModelResponse.ts:159-299`; `src/providers/open-ai-base/helpers.ts:103-144` |

## 6. KEEP / IMPROVE / AVOID Recommendation For HUAKAI

**KEEP from Sub2API as HUAKAI core:**

- Keep the canonical Responses-as-hub strategy for cross-protocol semantic translation. Portkey's evidence shows that route-local adapters scale provider breadth, but they do not provide one loss-auditable semantic model across OpenAI Chat, OpenAI Responses, and Anthropic Messages (`src/handlers/chatCompletionsHandler.ts:21-29`, `src/handlers/messagesHandler.ts:21-29`, `src/handlers/modelResponsesHandler.ts:17-25`).
- Keep a real stream state machine with explicit terminal finalization. Portkey's EOF path closes the stream without synthesizing a terminal semantic event (`src/handlers/streamHandler.ts:153-169`, `src/handlers/streamHandler.ts:376-380`).
- Keep explicit testable handling for Anthropic event classes, tool interleaving, and usage final chunks. Portkey handles many of these in direct Chat chunks, but the lifecycle is less auditable than a Responses item state machine (`src/providers/anthropic/chatComplete.ts:699-829`).

**IMPROVE HUAKAI using Portkey evidence:**

- Add a provider adapter chassis like Portkey's `ParameterConfig` / `ProviderAPIConfig` / `ProviderConfigs` model, but implement it in HUAKAI's Go style and terminology (`src/providers/types.ts:19-83`, `src/providers/types.ts:149-160`).
- Add a provider catalog target inspired by Portkey's breadth. The local Portkey commit has 72 provider entries in its provider map (`src/providers/index.ts:78-151`).
- Add a strict-vs-rich compatibility switch. Portkey's `strictOpenAiCompliance` defaults to strict but allows non-strict provider extras such as content blocks and provider finish reasons (`src/handlers/services/requestContext.ts:99-108`, `src/providers/anthropic/chatComplete.ts:818-821`, `src/providers/utils.ts:73-84`).
- Build a finish-reason map that covers provider-specific stop reasons, including Anthropic `max_tokens`, `tool_use`, and `pause_turn`; Portkey's map is a useful behavior reference (`src/providers/utils/finishReasonMap.ts:15-122`).
- Build a normalized Usage Record contract with adapter-specific extractors. Portkey proves the provider-specific differences are real: Anthropic usage arrives across `message_start` and `message_delta`, Cohere may use tokens or billed units, Google uses `usageMetadata`, and Bedrock Messages emits Anthropic-style usage after conversion (`src/providers/anthropic/chatComplete.ts:695-765`, `src/providers/cohere/chatComplete.ts:240-254`, `src/providers/google/chatComplete.ts:767-889`, `src/providers/bedrock/messages.ts:596-616`).
- Preserve native protocol pass-through where the client asks for that protocol. Portkey's `/v1/messages` path can pass through Anthropic streams because Anthropic has no `stream-messages` transformer (`src/index.ts:132-135`, `src/providers/anthropic/index.ts:25-31`, `src/handlers/streamHandler.ts:200-201`).

**AVOID from Portkey for HUAKAI core protocol translation:**

- Avoid making route-local Chat Completions the only canonical surface. It loses Responses output-item lifecycle and cannot represent all Anthropic/Responses stream semantics (`src/providers/anthropic/chatComplete.ts:806-829`).
- Avoid missing terminal synthesis. A gateway stream ending without `[DONE]`, `message_stop`, or `response.completed` should produce a typed incomplete/error finalization event or an operator-visible warning (`src/handlers/streamHandler.ts:153-169`, `src/handlers/streamHandler.ts:376-380`).
- Avoid scattered usage extraction as the only billing source. Adapter-local extraction is necessary, but HUAKAI should require each adapter to emit a centralized Usage Record with provenance fields.
- Avoid silent or ambiguous unknown-event behavior. Portkey's Anthropic chat transformer does not have a typed unknown-event path; malformed stream processing is caught at the stream handler and only logged before close (`src/providers/anthropic/chatComplete.ts:651-671`, `src/handlers/streamHandler.ts:376-380`).

**Recommended HUAKAI hybrid:**

Use Sub2API's **Responses-as-canonical state machine** for protocol translation correctness, and Portkey's **provider adapter chassis** for provider breadth. In HUAKAI terms: the protocol core should normalize into a gateway-owned semantic event model; provider adapters should be pluggable producers/consumers of that model; native routes may pass through provider-native streams when explicitly requested, but billing/observability must still receive a normalized Usage Record.

## 7. Updates To The Sub2API F-PROTO-001 Pass

Portkey evidence should cause these changes to the Sub2API pass and follow-up roadmap:

1. Add "Portkey route-local adapter fan-out" as the second-source architecture. The Sub2API pass currently frames the choice mostly as Responses hub vs M x N; Portkey demonstrates a third practical pattern: endpoint-local canonical surfaces with provider-specific request and response transforms (`src/services/transformToProviderRequest.ts:143-161`, `src/handlers/responseHandlers.ts:61-77`).
2. Keep the Sub2API canonical hub recommendation for HUAKAI's cross-format protocol core. Portkey's pattern scales providers but does not give the same loss-auditable item lifecycle for Anthropic-to-Responses-to-Chat streaming (`src/providers/anthropic/chatComplete.ts:768-829`).
3. Strengthen the finish-reason section. Portkey maps Anthropic `max_tokens` to OpenAI `length` in strict mode, which directly addresses the Sub2API pass's noted Chat-boundary fidelity gap (`src/providers/utils/finishReasonMap.ts:17-21`, `src/providers/utils.ts:73-84`).
4. Reconsider hard-dropping `signature_delta`. Portkey preserves `signature_delta` on native Anthropic Messages pass-through and in cached Messages JSON-to-stream generation, while strict Chat Completions cannot expose it cleanly (`src/providers/anthropic-base/utils/streamGenerator.ts:110-122`, `src/providers/anthropic-base/utils/streamGenerator.ts:165-169`, `src/handlers/streamHandler.ts:200-201`).
5. Add a "native protocol route" requirement. When the client requests Anthropic Messages, HUAKAI should not force translation through Chat or Responses unless the product contract says so; native pass-through plus normalized observability is a valid safe equivalent (`src/index.ts:132-135`, `src/providers/anthropic/api.ts:31-34`).
6. Add explicit acceptance tests for EOF-without-terminal-event and malformed stream chunk behavior. Portkey's generic stream path logs and closes on transform errors, which is operationally weaker than a typed terminal failure (`src/handlers/streamHandler.ts:376-380`).
7. Add a provider-usage shape matrix. Portkey shows at least four usage arrival patterns: Anthropic split start/final, Cohere final nested usage/billed units, Google `usageMetadata`, and Bedrock Messages usage converted to Anthropic `message_delta` (`src/providers/anthropic/chatComplete.ts:695-765`, `src/providers/cohere/chatComplete.ts:240-254`, `src/providers/google/chatComplete.ts:767-889`, `src/providers/bedrock/messages.ts:596-616`).

## 8. Chinese Summary For Owner

Portkey 的协议翻译策略和 Sub2API 根本不同：Sub2API 是 OpenAI Responses 做中心中间层，再把 Anthropic SSE 变成 Responses SSE / Chat chunks；Portkey 是按入口路由做本地 canonical surface，再用每个 provider 的 adapter 做请求字段映射和响应归一化。Portkey 的强项是 provider fan-out：本地 commit 里 `Providers` 映射有 72 个 provider，adapter 接口覆盖参数映射、header/baseURL/endpoint、响应 transform、stream transform、动态 provider config。它还把 Anthropic/Cohere/Google/Bedrock 等不同 usage 形状分别抽出来，finish_reason 映射也比 Sub2API 当前 Chat 边界更好，尤其是 Anthropic `max_tokens` 能在 strict OpenAI 模式下映射成 `length`。

建议 HUAKAI 采用混合路线：协议核心继续保留 Sub2API 式 Responses-as-hub + 有状态流式 state machine，因为这更适合做跨协议语义一致性、tool interleaving、终止事件和 usage finalization 的可验证 contract；同时吸收 Portkey 的 provider adapter chassis、finish_reason map、usage shape matrix、strict/rich compatibility 开关和 native protocol pass-through。没有功能缩水；clean-room 风险低，因为 Portkey 已验证 MIT 且本文件只记录行为证据，不复制实现；安全风险主要是未来实现时不要沿用 Portkey 的弱点，即 EOF 无终止事件时静默 close、未知事件缺少 typed warning、usage 抽取散落在 adapter 内。Owner 需要确认的是：HUAKAI 是否把 "native Anthropic Messages pass-through + normalized Usage Record" 纳入 F-PROTO-001/F-PROTO-002 的正式验收项。

## 9. CL-011 Source-Citation Check

Every Portkey behavior claim above cites a local source file and line or line range from `.omc/reference-src/portkey-gateway/` at commit `351692fd9236af222168134b416924fae0bdba23`. Claims about Sub2API are comparisons against the existing HUAKAI source-verified pass, not new Sub2API source mining in this file.
