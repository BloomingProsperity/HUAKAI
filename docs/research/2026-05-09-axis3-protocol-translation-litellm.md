# LiteLLM Protocol Translation Mechanism — Specifier Lane

Reference: `BerriAI/litellm@b5d3a5fc856ed1cf9b101d37bd0ec6d6d44751b2` (2026-05-08, last commit "feat: add read-replica routing for Prisma DB via DATABASE_URL_READ_REPLICA #27493"). Repo is actively maintained — 1-day-old HEAD as of citation, archived/disabled both false (locally fetched ref).

## TL;DR

1. **Canonical = OpenAI Chat Completions**, not a vendor-neutral IR. Every non-OpenAI vendor has a `BaseConfig` subclass that exposes a four-method contract (`map_openai_params`, `transform_request`, `transform_response`, `get_model_response_iterator`) — vendor adapter is responsible for OpenAI→vendor on the way in and vendor→OpenAI on the way out. The output canonical type is `Usage` + `ModelResponse` (extends OpenAI `ChatCompletion`) and `ModelResponseStream` (extends OpenAI `ChatCompletionChunk`).
2. **Anthropic-side bidirectional translation is a separate adapter pair** living under `litellm/llms/anthropic/experimental_pass_through/adapters/` — it translates Anthropic-Messages-API ↔ OpenAI-Chat-Completions in *both* directions, then defers to the regular completion dispatcher. Same canonical (OpenAI) is reused; this is fan-in/fan-out around a single hub, not a bidirectional independent pair.
3. **Streaming is per-chunk transform with state carried on a wrapper class.** The Anthropic-flavoured wrapper holds `current_content_block_index`, `current_content_block_type`, queued chunks, holding chunks for the deferred `usage` merge, and emits SSE event lines itself (`event: <type>\ndata: <json>\n\n`) — no third-party SSE encoder.
4. **Vendor-specific richness lives on `Usage` private attrs and `prompt_tokens_details` extensions**, not in the canonical OpenAI shape. `_cache_creation_input_tokens` / `_cache_read_input_tokens` are `PrivateAttr` on `Usage`; `cache_creation_tokens`, `web_search_requests`, multimodal `image_count` etc. live on a `PromptTokensDetailsWrapper` subclass; reasoning tokens go through `CompletionTokensDetailsWrapper.reasoning_tokens` with auto-derivation of `text_tokens`.
5. **Errors are normalised by status-code branch + substring sniffing per provider into ~10 OpenAI-shaped exception classes**, not by a vendor→class map. The mapping function spans 2.5K LOC with one `elif custom_llm_provider == "<vendor>"` block per vendor — substring-priority before status-code-priority.

---

## Q1 Canonical model 选型

**Decision**: Canonical = OpenAI Chat Completions schema. Non-OpenAI fields are tolerated as "wrapper" types that subclass the OpenAI types and tack on extra attributes.

**Why** (inferred from contract surface):
- Every adapter subclass implements `map_openai_params(non_default_params, optional_params, model, drop_params)` — the input shape it accepts is OpenAI-named params (`tools`, `tool_choice`, `temperature`, `response_format`, `reasoning_effort`, ...). The vendor-specific knobs (`thinking`, `top_k`, ...) ride on `optional_params` once the adapter chooses to keep them. Cite: `BerriAI/litellm@b5d3a5fc:litellm/llms/base_llm/chat/transformation.py:264-272`.
- The return type from `transform_response` is `ModelResponse` which inherits from the OpenAI chat-completion shape (`ModelResponseBase` → `OpenAIObject`). Cite: `BerriAI/litellm@b5d3a5fc:litellm/types/utils.py:1873-1876` and `:1797-1798` for streaming.
- The Anthropic Messages API is treated as *another* surface that fans into the same hub — translated to OpenAI then dispatched through `litellm.completion`. Cite: `BerriAI/litellm@b5d3a5fc:litellm/llms/anthropic/experimental_pass_through/architecture.md:20-29`.

**Anthropic thinking blocks / tool_use blocks → OpenAI canonical**:
- `thinking` blocks are smuggled onto the OpenAI assistant message as a non-OpenAI field `thinking_blocks: List[ChatCompletionThinkingBlock]`. The OpenAI assistant-message TypedDict is widened to allow this extra key. Cite: `BerriAI/litellm@b5d3a5fc:litellm/llms/anthropic/experimental_pass_through/adapters/transformation.py:618-664` (block-by-block packing) and `:1229-1271` (unpacking back to Anthropic on response).
- `tool_use` Anthropic blocks become OpenAI `tool_calls` array entries with `id` (preserved), `type="function"`, and the function `name`/`arguments` (JSON-stringified Anthropic `input`). Anthropic-style `signature` (thought signature) is stashed in `function.provider_specific_fields["thought_signature"]`. Cite: `BerriAI/litellm@b5d3a5fc:litellm/llms/anthropic/experimental_pass_through/adapters/transformation.py:584-617`.

**Vendor-only fields**:
- `reasoning_effort` is the canonical knob; Anthropic `thinking.budget_tokens` is bucket-mapped to `minimal/low/medium/high` by hard thresholds (2K/5K/10K). Cite: `BerriAI/litellm@b5d3a5fc:litellm/llms/anthropic/experimental_pass_through/adapters/transformation.py:668-707`.
- `cache_creation_input_tokens` (Anthropic) → `Usage._cache_creation_input_tokens` private attr **plus** `prompt_tokens_details.cache_creation_tokens`. Cite: `BerriAI/litellm@b5d3a5fc:litellm/types/utils.py:1527-1533, 1637-1647, 1668-1671`.
- `cache_read_input_tokens` (Anthropic) / `prompt_cache_hit_tokens` (DeepSeek) / OpenAI's `prompt_tokens_details.cached_tokens` — three sources fold into the same `prompt_tokens_details.cached_tokens` field with a per-source `if`. Cite: `BerriAI/litellm@b5d3a5fc:litellm/types/utils.py:1615-1635`.
- Anthropic `web_search_requests` lives on `PromptTokensDetailsWrapper.web_search_requests`. Cite: `BerriAI/litellm@b5d3a5fc:litellm/types/utils.py:1488-1489`.

**Trade-off observed**:
- Picking OpenAI-as-canonical is cheap when OpenAI is a strict subset (no thinking, no caching, no web search), but every non-OpenAI feature has to either (a) ride on private attrs / wrapper subclasses (visible if the consumer knows to look) or (b) get smuggled in via a non-typed extension key. The Anthropic adapter ends up effectively round-tripping through a richer-than-OpenAI dialect of OpenAI before re-emerging as Anthropic — the canonical OpenAI shape is more like "OpenAI plus extension slots" than pure OpenAI.

---

## Q2 Bidirectional translation

**Decision**: Pair of dedicated adapter classes covering OpenAI ↔ Anthropic in both directions; canonical is OpenAI; no third pivot format.

**Where**:
- `AnthropicAdapter.translate_completion_input_params{,_with_tool_mapping}` — Anthropic-request → OpenAI-request. Cite: `BerriAI/litellm@b5d3a5fc:litellm/llms/anthropic/experimental_pass_through/adapters/transformation.py:135-192`.
- `LiteLLMAnthropicMessagesAdapter.translate_anthropic_to_openai` — does the heavy lifting (messages, system, tools, thinking, tool_choice, output_format, metadata, plus a "copy untranslated" fallback). Cite: `BerriAI/litellm@b5d3a5fc:litellm/llms/anthropic/experimental_pass_through/adapters/transformation.py:1123-1193`.
- Reverse direction: `LiteLLMAnthropicMessagesAdapter.translate_openai_response_to_anthropic` — OpenAI `ModelResponse` → `AnthropicMessagesResponse`. Cite: `BerriAI/litellm@b5d3a5fc:litellm/llms/anthropic/experimental_pass_through/adapters/transformation.py:1332-1390`.
- The reverse path of "Anthropic-native vendor responding to non-Anthropic-shaped calls" doesn't exist as a symmetric pair — the system always uses Anthropic→OpenAI canonical, then routes via `litellm.completion`.

**Independent functions vs canonical → fan-out**:
- It's **canonical → fan-out**. Once translated to OpenAI, the regular completion dispatcher picks the vendor adapter (`AnthropicConfig`, `OpenAIGPTConfig`, `VertexGeminiConfig`, ...) by `custom_llm_provider`, and that adapter's own `transform_request` produces vendor-native HTTP. Cite: `BerriAI/litellm@b5d3a5fc:litellm/main.py:1471-1480` (provider_config lookup) and `:1543` (passed into completion logic).

**Translation failure / unmappable fields**:
- Best-effort with silent loss in many places: e.g. unrecognised content-block types in `translate_anthropic_messages_to_openai` are simply not appended to `new_messages`. Cite: `BerriAI/litellm@b5d3a5fc:litellm/llms/anthropic/experimental_pass_through/adapters/transformation.py:556-635` (the `if/elif` cascade has no terminal `else` — anything unknown drops on the floor).
- A few invariants do throw: tool_choice with an unknown `type` raises `ValueError`. Cite: `BerriAI/litellm@b5d3a5fc:litellm/llms/anthropic/experimental_pass_through/adapters/transformation.py:790-793`.
- A few raise on illegal *combinations*: streaming chunks containing both `thinking` and `signature` simultaneously — explicit `raise ValueError`. Cite: `BerriAI/litellm@b5d3a5fc:litellm/llms/anthropic/experimental_pass_through/adapters/transformation.py:1442-1444, 1501-1504`.
- For tool name length (Anthropic up to 128 chars / OpenAI cap 64), the system *does not* drop or refuse — it deterministically truncates with a SHA-256 hash suffix and stores a per-request mapping to restore the original name on the response side. Cite: `BerriAI/litellm@b5d3a5fc:litellm/llms/anthropic/experimental_pass_through/adapters/transformation.py:23-67, 795-848, 1294-1305`.

---

## Q3 Streaming chunk 翻译

**Decision**: Per-chunk transform with cross-chunk state carried on a long-lived wrapper class; SSE serialised by hand, not by a SSE library.

**Wrapper state machine**:
- The wrapper subclasses `AdapterCompletionStreamWrapper` (renamed in this summary as "the Anthropic stream wrapper" to avoid quoting upstream class names beyond what's needed for citation). State fields: `sent_first_chunk`, `sent_content_block_start`, `sent_content_block_finish`, `current_content_block_type`, `current_content_block_index`, `current_content_block_start`, plus a `chunk_queue: deque`, plus a `holding_chunk` and `holding_stop_reason_chunk` for deferred merges. Cite: `BerriAI/litellm@b5d3a5fc:litellm/llms/anthropic/experimental_pass_through/adapters/streaming_iterator.py:31-44, 46-55`.
- First call emits a synthetic `message_start` event (with `id=msg_<uuid>`, model, and a zero-initialised `usage` so clients can detect cache support). Cite: `:78-104`.
- Second call emits `content_block_start` for an empty text block at index 0. Cite: `:106-115, 246-255`.
- For each upstream OpenAI chunk: detect whether the block type changed (text → tool_use / thinking → text / new tool call), and if so emit `content_block_stop(prev_index)` then `content_block_start(new_index)` before relaying the delta. Cite: `:117-167, 257-393, 460-518`.
- Final OpenAI chunk (the one carrying `finish_reason`) is *held* in `holding_stop_reason_chunk` until the *next* chunk's `usage` arrives, so usage tokens can be merged into the Anthropic `message_delta` event. If the upstream stream ends before usage arrives, the held chunk is flushed without it. Cite: `:271-320, 395-412`.
- After the upstream is exhausted, the wrapper emits a final synthetic `message_stop`. Cite: `:197-204, 405-412, 422-425`.

**Buffer vs immediate transform**:
- Immediate per-chunk, no full buffering. Held chunks exist only to (a) join `usage` onto `finish_reason` and (b) retain a previous chunk while a new content-block is being opened, but the queue depth is small (≤3 in practice).

**Cross-chunk state carried**:
- Block-index counter (`current_content_block_index`) — increments on each block-type transition.
- Block-type marker (`current_content_block_type`) — the predicate for "do we open a new block".
- Tool-call name mapping (truncated → original) used to un-truncate names that were abbreviated for OpenAI's 64-char limit. Cite: `:54-55, 484-498`.
- "First chunk sent" / "first content-block start sent" booleans gate the synthetic prelude.
- For parallel tool calls, every chunk carrying a *function name* is treated as a new tool-call block — i.e. the second OpenAI tool call opens a fresh Anthropic `content_block_start` even if the block-type didn't change. Cite: `:505-516`.

**Wrapper class name**: the upstream class is the `AdapterCompletionStreamWrapper` subclass `AnthropicStreamWrapper` (left here only as a citation handle; HUAKAI must not reuse this class name). It exposes `__next__` and `__anext__` (sync / async) that both implement the same logic separately rather than sharing a generator. Cite: `:78-216, 218-425`.

**SSE encoding**:
- Hand-rolled. Two methods: `anthropic_sse_wrapper` (sync) and `async_anthropic_sse_wrapper` (async) — both iterate the wrapper, then for each dict emit `event: <chunk['type']>\ndata: <json.dumps(chunk)>\n\n` as `bytes`. Non-dict items are forwarded unchanged (e.g. when upstream passed pre-encoded bytes). Cite: `BerriAI/litellm@b5d3a5fc:litellm/llms/anthropic/experimental_pass_through/adapters/streaming_iterator.py:427-455`.

**Provider-side streaming aggregation**:
- The shared `CustomStreamWrapper` in `litellm/litellm_core_utils/streaming_handler.py` (2.4K LOC) is the *general* stream normaliser — every vendor adapter feeds chunks into it, and it produces `ModelResponseStream`. It's a giant `chunk_creator` with an `if/elif` ladder per vendor, plus a `GenericStreamingChunk` TypedDict (`text`, `tool_use`, `is_finished`, `finish_reason`, `usage`, `index`) which is the de-facto vendor-side IR for "I've parsed my chunk into these fields". Cite: `BerriAI/litellm@b5d3a5fc:litellm/types/utils.py:275-285` and `BerriAI/litellm@b5d3a5fc:litellm/litellm_core_utils/streaming_handler.py:1121-1187` (generic-chunk branch handling).
- Stream timeout enforcement is centralised: `LITELLM_MAX_STREAMING_DURATION_SECONDS` is checked on each `__next__`/`__anext__` and raises `litellm.Timeout` mid-stream if exceeded. Cite: `BerriAI/litellm@b5d3a5fc:litellm/litellm_core_utils/streaming_handler.py:184-196`.

---

## Q4 Tool call format 互转

**Decision**: All tool calls funnel through the OpenAI `tool_calls` array shape; Anthropic ↔ OpenAI is bidirectionally lossy on `name` (length-truncated) but lossless on `id` (preserved verbatim). Gemini is not in the bidirectional pair — it goes through the canonical OpenAI shape via the regular Gemini adapter.

**Anthropic → OpenAI**:
- Each `tool_use` content block becomes one entry in `assistant.tool_calls`: `{id: <anthropic_id>, type: "function", function: {name: <truncated_anthropic_name>, arguments: <json.dumps(input)>}}`. Cite: `BerriAI/litellm@b5d3a5fc:litellm/llms/anthropic/experimental_pass_through/adapters/transformation.py:584-617`.
- `tool_result` user-content blocks become standalone `{role: "tool", tool_call_id: <anthropic_tool_use_id>, content: ...}` messages, *separated* from the surrounding user message. Multiple result items collapse into one `tool` message (multi-content list) when they share an id, to honour OpenAI's "exactly one tool message per tool_call_id" rule. Cite: `BerriAI/litellm@b5d3a5fc:litellm/llms/anthropic/experimental_pass_through/adapters/transformation.py:421-545`.

**OpenAI → Anthropic**:
- `assistant.tool_calls` array iterates into separate `tool_use` content blocks in the Anthropic response. Cite: `BerriAI/litellm@b5d3a5fc:litellm/llms/anthropic/experimental_pass_through/adapters/transformation.py:1281-1317`.
- `function.arguments` (a JSON string) is parsed back to the Anthropic `input` dict via `parse_tool_call_arguments` — a forgiving JSON parser that absorbs partial / malformed JSON. Cite: same range plus the imported helper at `:73`.

**ID mapping**:
- `tool_use.id` (Anthropic) → `tool_calls[].id` (OpenAI) is straight passthrough.
- `tool_result.tool_use_id` (Anthropic) → `tool_message.tool_call_id` (OpenAI) is straight passthrough.
- Direction-consistency is preserved because vendor-side echoes the same id in the response.
- For "thought signature" smuggling: OpenAI doesn't have such a slot, so it's hidden in `function.provider_specific_fields["thought_signature"]` on the OpenAI side and pulled back out into Anthropic `tool_use.provider_specific_fields["signature"]` on the way back. Cite: `:242-264, 1287-1316`.

**Name mapping**:
- OpenAI cap = 64 chars; Anthropic = 128. For Anthropic→OpenAI, anything > 64 chars is replaced with `<55-char-prefix>_<8-char-sha256-hex>`, and a per-request map keeps the original. On the response (or response stream) the truncated name is restored via the map. Cite: `BerriAI/litellm@b5d3a5fc:litellm/llms/anthropic/experimental_pass_through/adapters/transformation.py:21-67`.

**Parallel tool calls**:
- OpenAI emits a single `tool_calls: [...]` array on one assistant message; the Anthropic adapter expands this into N parallel `tool_use` blocks at successive indices. Cite: `:1281-1317`.
- In streaming, each new function name (delta) triggers a fresh `content_block_start` to keep parallel calls in distinct Anthropic blocks. Cite: `:505-516`.

**Anthropic-hosted tools (web_search, code_execution, computer)**:
- These have shapes that don't fit OpenAI's `function` paradigm. The adapter detects them via `ANTHROPIC_HOSTED_TOOLS` enum and *bypasses* translation — passes the original tool dict through as-is on the wire, relying on the destination being Anthropic-native. Cite: `:811-816`.
- Anthropic web search tool special-cased: when present, the adapter *also* sets `web_search_options = {}` on the OpenAI request so OpenAI-compatible providers that have the search tool can opt in. Cite: `:996-1029, 326-344`.

**Gemini**:
- Not handled in the bidirectional adapter pair. Gemini goes through `litellm.completion(model="gemini/...")`, the regular `VertexGeminiConfig`/`GoogleAIStudioGeminiConfig` adapter does OpenAI↔Gemini in `transform_request`/`transform_response`, and any caller wanting Anthropic-shaped Gemini just chains the two: Anthropic→OpenAI (this adapter) → OpenAI→Gemini (Gemini adapter) on the way in, and reverse on the way out. Cite: `BerriAI/litellm@b5d3a5fc:litellm/llms/gemini/chat/transformation.py:18-22` (extends Vertex Gemini config) and the architecture diagram at `BerriAI/litellm@b5d3a5fc:litellm/llms/anthropic/experimental_pass_through/architecture.md:32-51`.

---

## Q5 Usage 字段

**Decision**: A wrapper `Usage` class extending OpenAI's `CompletionUsage`, with PrivateAttr fields for Anthropic-specific cache tokens, plus extended sub-types `PromptTokensDetailsWrapper` and `CompletionTokensDetailsWrapper`. Vendor sources (`prompt_cache_hit_tokens` from DeepSeek, `cache_read_input_tokens` from Anthropic, OpenAI native `cached_tokens`) all *write into* the same canonical slot in different `if` branches.

**Field-by-field**:
- `prompt_tokens` (OpenAI) ≡ Anthropic `usage.input_tokens`. Cite: `BerriAI/litellm@b5d3a5fc:litellm/types/utils.py:1649-1652` and `BerriAI/litellm@b5d3a5fc:litellm/llms/anthropic/experimental_pass_through/adapters/transformation.py:1357-1368`.
- `completion_tokens` (OpenAI) ≡ Anthropic `usage.output_tokens`.
- Cached input tokens — three sources, one canonical slot:
  - Anthropic: `cache_read_input_tokens` → `Usage._cache_read_input_tokens` (private) + `prompt_tokens_details.cached_tokens`. Cite: `:1626-1635, 1673-1676`.
  - DeepSeek: `prompt_cache_hit_tokens` → `prompt_tokens_details.cached_tokens`. Cite: `:1615-1624`.
  - OpenAI (native): `prompt_tokens_details.cached_tokens` straight through.
- Cache *creation* tokens — Anthropic-only on the way in: `cache_creation_input_tokens` → `Usage._cache_creation_input_tokens` (private) + `prompt_tokens_details.cache_creation_tokens` + (optionally) `cache_creation_token_details.{ephemeral_5m_input_tokens,ephemeral_1h_input_tokens}`. Cite: `:1471-1474, 1500-1505, 1637-1647, 1668-1671`.
- Reasoning tokens — `completion_tokens_details.reasoning_tokens` is canonical; Anthropic thinking-content tokens and OpenAI o-series `reasoning_tokens` both write here. The constructor auto-derives `text_tokens = completion_tokens - reasoning_tokens - image_tokens - audio_tokens` if the provider didn't set it (clamped at 0). Cite: `:1572-1597`.
- Web search calls — `prompt_tokens_details.web_search_requests` (Anthropic, surfaced from `server_tool_use.web_search_requests`). Cite: `:1488-1489, 1522-1525, 1535`.
- Vertex AI multimodal embedding — `character_count`, `image_count`, `video_length_seconds` on `PromptTokensDetailsWrapper`, with `__init__` deleting attributes that are `None` so they don't pollute the OpenAI-shaped JSON. Cite: `:1491-1519`.
- Cost — `Usage.cost: Optional[float]`, deleted from the instance when absent so it disappears from JSON. Cite: `:1536, 1662-1665`.

**Reverse direction (OpenAI → Anthropic response usage)**:
- `Anthropic.input_tokens = OpenAI.prompt_tokens - OpenAI.prompt_tokens_details.cached_tokens` (because Anthropic reports input minus cached separately).
- `Anthropic.output_tokens = OpenAI.completion_tokens`.
- If `Usage._cache_creation_input_tokens > 0` → emit `cache_creation_input_tokens`.
- If `cached_tokens > 0` → emit `cache_read_input_tokens`. Cite: `BerriAI/litellm@b5d3a5fc:litellm/llms/anthropic/experimental_pass_through/adapters/transformation.py:1356-1377`.
- The *streaming* variant repeats this logic in `translate_streaming_openai_response_to_anthropic` for the final usage chunk. Cite: `:1521-1574`.

---

## Q6 Error normalization

**Decision**: Single 2.5K LOC `exception_type` function that maps to ten OpenAI-shaped exception classes (`AuthenticationError`, `BadRequestError`, `RateLimitError`, `ContextWindowExceededError`, `ContentPolicyViolationError`, `Timeout`, `NotFoundError`, `InternalServerError`, `BadGatewayError`, `ServiceUnavailableError`, plus `APIError` / `APIConnectionError` fallbacks). Branches are per-`custom_llm_provider`; within each provider, the precedence is **substring-of-error-message first, then HTTP status-code, then a generic `APIError` fallthrough**.

**Where**: `BerriAI/litellm@b5d3a5fc:litellm/litellm_core_utils/exception_mapping_utils.py:236-2536`.

**Provider precedence inside the OpenAI/openai-compatible branch** (representative): Cite: `:367-652`.
1. Cross-vendor early outs by error-string substring: `"Request Timeout"`, `"Request timed out"`, `"timed out"` → `Timeout` (applied **before** any provider branch). Cite: `:343-356`.
2. Substring-prioritised provider checks: `is_error_str_rate_limit`, `is_error_str_context_window_exceeded`, `"invalid_request_error" + "model_not_found"` → `NotFoundError`, `"content_policy_violation"` → `ContentPolicyViolationError`, etc. Cite: `:399-494`.
3. Status-code branch (`hasattr(original_exception, "status_code")`): 400→`BadRequestError`, 401→`AuthenticationError`, 404→`NotFoundError`, 408→`Timeout`, 422→`BadRequestError`, 429→`RateLimitError`, 500→`InternalServerError`, 502→`BadGatewayError`, 503→`ServiceUnavailableError`, 504→`Timeout`, else→`APIError`. Cite: `:539-640`.
4. No status code → `APIConnectionError`. Cite: `:641-652`.

**Anthropic branch is similar but smaller**: substring sniffing for `"prompt is too long"` → `ContextWindowExceededError`, `"overloaded_error"` → `InternalServerError`, `"Invalid API Key"` → `AuthenticationError`, `"content filtering policy"` → `ContentPolicyViolationError`, then status-code fallback (note Anthropic groups 500+529 together because `529: Overloaded` is treated like a 500). Cite: `:653-767`.

**Status-only vs body-aware**: It's body-aware. The function reads the *string* of the exception (`str(original_exception)` and `original_exception.message`) and applies substring tests *before* it ever looks at status codes — meaning a 400 with body `"context_length_exceeded"` becomes `ContextWindowExceededError` (not `BadRequestError`). Cite: `:264-269, 407-415`.

**Streaming mid-vs-pre distinction**: Not handled in `exception_type` itself — instead the streaming wrapper distinguishes by *what's been emitted so far*. Specifically, `nlp_cloud` branch checks `self.received_finish_reason` to decide whether a parse error should re-raise or be swallowed as a normal end-of-stream. Cite: `BerriAI/litellm@b5d3a5fc:litellm/litellm_core_utils/streaming_handler.py:1229-1235`. The Anthropic-translation streaming wrapper, by contrast, has a try/except that downgrades `Exception` to `verbose_logger.error` + `raise StopAsyncIteration` — so mid-stream errors after first chunk become a *clean stream end* rather than a thrown exception. This is a deliberate design choice that prioritises client survival over error visibility. Cite: `BerriAI/litellm@b5d3a5fc:litellm/llms/anthropic/experimental_pass_through/adapters/streaming_iterator.py:212-216`.

**Pre-translation**: LiteLLM's own exception types short-circuit at the very top: `if any(isinstance(original_exception, exc_type) for exc_type in litellm.LITELLM_EXCEPTION_TYPES): return original_exception` — already-normalised exceptions don't get re-mapped. Cite: `:244-248`.

---

## Q7 模型路由 / 重命名

**Decision**: The `model` parameter accepts `"<provider>/<model>"`, `"<model>"` (provider inferred by name patterns or `api_base` URL), or `"<provider>/<provider>/<model>"` (e.g. OpenRouter passthrough). Resolution is single-pass with a clear precedence order in `get_llm_provider`.

**Resolution order** (Cite: `BerriAI/litellm@b5d3a5fc:litellm/litellm_core_utils/get_llm_provider_logic.py:137-247, 411, 557-558`):
1. If the caller passed `litellm_params`, extract `custom_llm_provider`/`api_base`/`api_key` from it.
2. Special-case Azure-AI-Studio for non-OpenAI Azure models.
3. Cohere chat-route detection (rewrite `cohere/command-r-plus` → `cohere_chat/...`).
4. Anthropic-text route detection (similar rewrite).
5. If `custom_llm_provider` was passed and model doesn't already carry that prefix, prepend it: `model = custom_llm_provider + "/" + model`.
6. OpenRouter special-case: if `custom_llm_provider == "openrouter"` and model starts with `"openrouter/"`, strip the prefix to leave the native ID intact (so `openrouter/anthropic/claude-3.5-sonnet` → `anthropic/claude-3.5-sonnet`, but `openrouter/auto` stays as `openrouter/auto`).
7. JSON-configured providers via `JSONProviderRegistry.exists(provider_prefix)` — checked **before** the enum-based provider list.
8. Enum-based provider list (`litellm.provider_list`) split: if `model.split("/")[0]` is a known provider → use it as `custom_llm_provider` and strip from model.
9. If still unresolved and `api_base` is set, scan `litellm.openai_compatible_endpoints` for a URL match → infer provider + lazy-load `<PROVIDER>_API_KEY` from env (Perplexity, Anyscale, DeepInfra, Mistral, Groq, NVIDIA NIM, Cerebras, Baseten, SambaNova, AI21, Codestral, Empower, DeepSeek, Ollama, FriendliAI, Galadriel, Meta-Llama, ...). Cite: `:248-315`.
10. As a last resort there's a `model.split(":")` branch (for vendors like Replicate that use `vendor:model:tag` notation). Cite: `:411`.

**Multiple prefixes accepted**: Yes — `"gpt-4"`, `"openai/gpt-4"`, and `"openrouter/openai/gpt-4"` all resolve. Resolution order is deterministic above.

**Model alias mechanism**:
- Module-level: `litellm.model_alias_map: Dict[str, str]` — caller-populated (e.g. `{"my-fast": "gpt-3.5-turbo"}`) and consulted during request build. Cite: `BerriAI/litellm@b5d3a5fc:litellm/__init__.py:356`.
- Router-level: `Router.model_group_alias: Dict[str, Union[str, RouterModelGroupAliasItem]]` — maps caller-facing group names to physical model groups, evaluated inside `Router.completion`. Cite: `BerriAI/litellm@b5d3a5fc:litellm/router.py:482-483, 8539-...` (alias resolution).
- `RouterModelGroupAliasItem` is the shape that lets aliases carry routing weights / hidden flags (not just a plain rename).

---

## HUAKAI 借鉴可行性

### 直接借鉴 (paraphrased rewrite, 不抄码)

1. **Canonical = OpenAI Chat Completions**, with vendor extensions on private/extended types — picking this avoids the temptation to invent a new neutral IR. HUAKAI 已经 use OpenAI canonical in some places; lock it in everywhere as the contract surface. **架构升级 dimension**: 与 LiteLLM 同 (BerriAI/litellm@b5d3a5fc:litellm/types/utils.py:1873) + portkey 同 (Portkey 也是 OpenAI canonical, 见已有 source-read-oneapi-portkey-litellm.md); **HUAKAI delta**: 在 canonical 周围加 PASR cache-aware vendor 扩展字段 + per-vendor metric slicing,这两点 LiteLLM 的 `Usage` 没做到 metric slice 粒度。
2. **Bidirectional Anthropic↔OpenAI as a separate adapter pair feeding the same canonical hub**, *not* a third pivot format. HUAKAI 的 axis-3 推进就是: 写一对 `AnthropicMessages↔OpenAI` 翻译器,然后 dispatcher 不变。**算法升级 dimension**: LiteLLM 的实现是 fan-in fan-out 但 streaming 状态机有 holding_chunk + holding_stop_reason_chunk 这种 ad-hoc workaround,HUAKAI 可以用统一的 cross-chunk state machine (如 PASR 的 segment table 思路) 替代两个 holding 字段。
3. **Tool name truncation with deterministic SHA-suffix + per-request mapping** — 这是 HUAKAI 必须复用的算法 pattern。OpenAI 64 字符 vs Anthropic 128 字符的限制 HUAKAI 也会撞到。Truncation algorithm 是 `<55-char-prefix>_<8-char-sha256-hex>` (BerriAI/litellm@b5d3a5fc:litellm/llms/anthropic/experimental_pass_through/adapters/transformation.py:21-67). **算法升级 dimension**: HUAKAI 可以加一层 collision detection — 即使 SHA-8 也有 1/2^32 撞概率,在 N>10K tools 场景下需要二次验证 + 失败时切换到 SHA-12 后缀。
4. **Per-chunk transform with cross-chunk state machine on a wrapper class** — 流式状态机的字段集合 (block index counter, block type marker, queued chunks deque, holding chunk for usage merge) 是 HUAKAI 流式 axis-3 的最低门槛要求。**架构升级 dimension**: HUAKAI 的 fusion delta 是 cross-account + cross-vendor 复制 streaming state — LiteLLM 没做到 (它的 wrapper 是 per-request scope)。我们可以把 wrapper state lift 到 attempt-lease 维度,跨 attempt 共享 block-index 计数器。
5. **Status-code + body-substring exception mapping** — substring before status-code 的优先级是关键 (因为 400 with `"context_length_exceeded"` body is semantically a different class)。HUAKAI exception normalisation 之前没明确 substring 优先,需补。**算法升级 dimension**: LiteLLM 的 substring 列表是硬编码 (BerriAI/litellm@b5d3a5fc:litellm/litellm_core_utils/exception_mapping_utils.py:343-494),HUAKAI 可以做成 vendor 注册表 + per-vendor pluggable matcher 函数,避免 2.5K LOC 单文件。
6. **Three-tier Usage extension**: `_cache_creation_input_tokens` / `_cache_read_input_tokens` 用 PrivateAttr 隐藏 (避免污染 OpenAI JSON 输出),细节用 `PromptTokensDetailsWrapper` / `CompletionTokensDetailsWrapper` 子类承载,`__init__` 主动 `del` `None` 字段保持 OpenAI 原生形状兼容。这是在不破坏 OpenAI client 的前提下挂载 vendor 扩展的干净办法 (BerriAI/litellm@b5d3a5fc:litellm/types/utils.py:1506-1519)。**生态升级 dimension**: HUAKAI 可加 per-vendor metric slicing — LiteLLM 把所有 cached_tokens 折叠到一个 slot,看不出"这次 cache hit 来自 Anthropic 还是 DeepSeek",HUAKAI 在 Usage 旁附加一个 `vendor_breakdown: Dict[str, VendorUsage]` 维度。

### 需要替换 (license / 架构差异决定不能 1:1 借)

1. **2.5K LOC 单文件 exception_type** — LiteLLM 写法很反代码 review,HUAKAI 必须拆成 `vendor → ExceptionMapper` 注册表 + 每 vendor 一个 ~150 LOC 的 mapper。原则相同,组织不同。
2. **module-level mutable state** like `litellm.model_alias_map: Dict[str, str]` — HUAKAI 用 admin-managed 持久化的 alias 表 (account_alias / vendor_alias),不放进进程级单例。LiteLLM 这种全局可变 dict 在多租户场景就是 race condition 温床。
3. **Anthropic-hosted tool 直接绕过翻译** (BerriAI/litellm@b5d3a5fc:litellm/llms/anthropic/experimental_pass_through/adapters/transformation.py:811-816) — LiteLLM 是直接把原 dict 透传给下游,因为它假设 destination 一定是 Anthropic-native;HUAKAI 因为支持 cross-vendor 路由,不能这么做,必须有 host-tool capability 检测 + 优雅 fallback (例如 OpenAI 没有 web_search 就 mark unsupported / route to 一个有该 host tool 的 vendor)。
4. **Fake/silent drop of unrecognised content blocks** (`:556-635` 的 if/elif 没有 else 分支) — 对 HUAKAI 不可接受,必须显式 log + 返回 capability mismatch warning。生产场景里 silent drop 是 axis-3 第一大坑。

### Trap (看起来好但有坑)

1. **`self.received_finish_reason` 检查 swallow streaming exception** (`BerriAI/litellm@b5d3a5fc:litellm/litellm_core_utils/streaming_handler.py:1229-1235`) — 这是把"已经发了 finish 的流出错"当 OK 的兜底逻辑。HUAKAI 不应照搬,因为我们要的是 stream-mid 故障可以触发 fallback continuation prompt synthesis (PASR 已有思路);LiteLLM 的 swallow 会让上层完全失去重试机会。
2. **streaming 中 `holding_stop_reason_chunk` 等 usage**: 如果 upstream 在 finish 后从来不发 usage chunk (Bedrock Converse 单次响应模式就是),holding_stop_reason_chunk 永远不被释放直到 stream 关闭 — 在长连接场景下用户最后看到 Anthropic message_stop 之前会有可观察的延迟。HUAKAI 应该 timeout-bounded 或者 first-chunk-or-N-ms whichever-first 释放。
3. **bucket-mapping `thinking.budget_tokens` to `reasoning_effort` by hard thresholds** (2K/5K/10K) — LiteLLM 这是 lossy pessimisation。Anthropic 用户传 budget_tokens=4500 想要中等思考,被 bucket 成 `low`(< 5K),回去再被映射成 default budget,数值漂移。HUAKAI 的精度 delta 应该是: keep `budget_tokens` 原值 + 在 vendor adapter 内重新映射,不在 canonical 层 bucket 化。**算法升级 dimension** + 这是 HUAKAI 比 LiteLLM 强的真实 delta 之一。
4. **同步/异步 `__next__`/`__anext__` 完全独立两份逻辑** (`streaming_iterator.py:78-216` vs `:218-425`,~200 LOC duplicate) — 维护噩梦。HUAKAI 应该写一份 generator,sync/async wrap 即可。LiteLLM 这是历史包袱,不要 copy。
5. **Tool name truncation 用 SHA-256 + 截 8 字符** — 看起来稳但当 prefix 完全相同 (e.g. `openapi_get_repos_owner_repo_pulls_pull_number_files` vs `openapi_get_repos_owner_repo_pulls_pull_number_commits`) 时,55-char prefix 已经分流,但 hash 还是基于完整 name。这种情况下两个 tool 的 truncated name 会因 prefix 同前缀而前 55 char 相同,后 8 char 哈希区分 — 实际是没问题,但 audit 时一眼看上去会以为 collision。HUAKAI 加一段单元测试 + 注释解释比 LiteLLM 的注释更清楚。

---

## Source files read

- `/home/codex/refs/litellm/litellm/llms/anthropic/experimental_pass_through/architecture.md`
- `/home/codex/refs/litellm/litellm/llms/anthropic/experimental_pass_through/adapters/transformation.py`
- `/home/codex/refs/litellm/litellm/llms/anthropic/experimental_pass_through/adapters/streaming_iterator.py`
- `/home/codex/refs/litellm/litellm/anthropic_interface/messages/__init__.py`
- `/home/codex/refs/litellm/litellm/llms/anthropic/chat/transformation.py` (header + class boundaries only)
- `/home/codex/refs/litellm/litellm/llms/base_llm/chat/transformation.py`
- `/home/codex/refs/litellm/litellm/litellm_core_utils/exception_mapping_utils.py` (lines 236-1130 sampled)
- `/home/codex/refs/litellm/litellm/litellm_core_utils/get_llm_provider_logic.py` (lines 137-310, plus reference to 411/557-558)
- `/home/codex/refs/litellm/litellm/litellm_core_utils/streaming_handler.py` (lines 98-260, 1121-1320 sampled)
- `/home/codex/refs/litellm/litellm/types/utils.py` (lines 270-285, 1376-1900 sampled)
- `/home/codex/refs/litellm/litellm/main.py` (grep landmarks 138-2751)
- `/home/codex/refs/litellm/litellm/router.py` (grep landmarks 225-8539)
- `/home/codex/refs/litellm/litellm/__init__.py` (grep `model_alias_map`)
- `/home/codex/refs/litellm/litellm/llms/gemini/chat/transformation.py` (header line 18-22)

Source code under `litellm/enterprise/` was deliberately not read per lane guard.

## Lane / Agent / UTC

- Lane: specifier (axis-3 protocol translation focus)
- Agent: general-purpose
- UTC timestamp: 2026-05-09T17:30Z
