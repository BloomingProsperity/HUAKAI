# Content-Features Domain Audit

**Domain summary:** HUAKAI's content-feature layer is strongest in core chat-completion transformations (streaming, multimodal input types, tool-calling, system-prompt rewriting, idempotency, format conversion) and introduces several features beyond typical references (thinking/reasoning, computer-use data model, trust-chain audit, ZDR tracking). Major commercial gaps are: no embeddings endpoint, no image/audio-output endpoints, no batch inference endpoint, no semantic cache, no content moderation pipeline, no prompt-injection guard, and no conversation-history manager. Sub2api/new-api expose all of these.

---

## Feature Coverage Table

| # | Feature | Status | Evidence (file:line) | Gap Note |
|---|---------|--------|----------------------|----------|
| 1 | **SSE/chunked streaming** | PRESENT | `backend/internal/proto/stream_plan.go` — `StreamModeBuffered/Streaming/Replay`; `backend/internal/gatewayhttp/chat_completions_stream.go` | Full SSE relay with mid-stream fallback boundary |
| 2 | **Mid-stream cancellation / fallback** | PRESENT | `backend/internal/proto/stream_plan.go` — `FallbackBoundary`, `MidStreamFallbackPolicy` (none/continuation/restart) | P-8 continuation prompt synthesis wired |
| 3 | **Multimodal input — images (base64/URL)** | PRESENT | `backend/internal/proto/capability_image.go` — `ImageNode`, `DataSourceKind` (inline_base64/url/file_id/digest_ref), `MediaDimensions` | Full; dimension tracking included |
| 4 | **Multimodal input — audio** | PRESENT | `backend/internal/proto/capability_audio.go` — `AudioNode`, `MediaTransport`, `TranscriptPolicy` | Four transport modes; live-compatible flag |
| 5 | **Multimodal input — video** | PRESENT | `backend/internal/proto/capability_video.go` — `VideoNode` with codec, dimensions, time-range clipping | Full data model |
| 6 | **Multimodal input — file/document attachments** | PRESENT | `backend/internal/proto/capability_file.go` — `FileNode`, MIME type, digest, retention labels | Used for PDF/docx vision passthrough |
| 7 | **Tool / function-calling (request side)** | PRESENT | `backend/internal/proto/hcsf.go` — `CanonicalTool`; `backend/internal/proto/request_meta.go:79-82` — `Tools[]`, `ToolChoice` | json.RawMessage tool_choice keeps vendor dialect |
| 8 | **Tool-use blocks (assistant turn)** | PRESENT | `backend/internal/proto/capability_tool.go` — `ToolUseNode` with streaming `partial_input` | Handles streaming partial inputs |
| 9 | **Tool-result injection (user/tool turn)** | PRESENT | `backend/internal/proto/capability_tool.go` — `ToolResultNode`, `is_error` flag, content array | EdgeRequires links result→use for multi-turn |
| 10 | **Parallel tool calls** | PRESENT | `backend/internal/proto/request_meta.go:103` — `ParallelToolCalls *bool`; `backend/internal/proto/capability_matrix.go:121` — `FeatureParallelToolCalls` | Provider-level cap tracked in capability matrix |
| 11 | **Tool name rewriting / obfuscation** | PRESENT | `backend/internal/gateway/tool_name_rewrite.go` — `ToolRename` audit trail, rewrite in tools[], tool_use, tool_choice.name | HUAKAI-unique; upstream names obfuscated for trust |
| 12 | **JSON mode (`response_format: json_object`)** | PRESENT | `backend/internal/proto/capability_structured.go:9` — `StructuredOutputJSONMode`; `backend/internal/proto/request_meta.go:106` — `ResponseFormat` struct | Validated by `envelope_validate.go:831` |
| 13 | **Structured output / JSON schema** | PARTIAL | `backend/internal/proto/capability_structured.go` — data model complete; `backend/internal/proto/openai_chat_request.go:85-88` — D5 raw-passthrough placeholder, loss entry emitted | Schema enforcement deferred to D5.x; openai_chat protocol passes raw body only, no StructuredOutputNode population |
| 14 | **`reasoning_effort` parameter (o1/o3/Sonnet-thinking)** | PARTIAL | `backend/internal/proto/openai_responses_request.go:80` — recorded as info-loss; `backend/internal/proto/openai_chat_request.go:19` — D5.x deferred | ThinkingNode data model complete (`capability_thinking.go`); openai_chat ingress does not yet parse reasoning_effort into ThinkingNode |
| 15 | **Thinking / extended reasoning blocks** | PRESENT | `backend/internal/proto/capability_thinking.go` — `ThinkingNode` (BudgetTokens, Blocks, HiddenTokens, Redaction classes); `backend/internal/proto/anthropic_messages_request.go:262` — populates ThinkingNode from upstream | Four redaction classes (public/redacted/hidden/provider_only) |
| 16 | **System prompt inject / override** | PRESENT | `backend/internal/gateway/system_rewrite.go` — `SystemRewriteMode` (EnsurePrefix/ReplaceAll/AppendAfter); idempotency check; preserves cache_control markers | Three rewrite modes; idempotent prefix detection |
| 17 | **System prompt — prefix injection** | PRESENT | `backend/internal/gateway/system_rewrite.go` — `EnsurePrefix` mode with duplicate-detection | |
| 18 | **System prompt — suffix / append** | PRESENT | `backend/internal/gateway/system_rewrite.go` — `AppendAfter` mode | |
| 19 | **System prompt — block array form** | PRESENT | `backend/internal/gateway/system_rewrite.go` — handles string, single block, block array | |
| 20 | **max_tokens enforcement** | PRESENT | `backend/internal/proto/request_meta.go:84-85` — `MaxTokens *int`; `backend/internal/proto/openai_chat_request.go:52-56` — parses both max_tokens and max_completion_tokens | |
| 21 | **Stop sequences** | PRESENT | `backend/internal/proto/request_meta.go:87-91` — `Stop []string` (OpenAI), `StopSequences []string` (Anthropic); `backend/internal/proto/capability_matrix.go` — `FeatureStopSequenceEmit` | Dual-field normalisation |
| 22 | **Temperature / top_p sampling controls** | PRESENT | `backend/internal/proto/request_meta.go:93-97` — `Temperature *float64`, `TopP *float64` | Nil ≠ 0.0 invariant preserved |
| 23 | **Seed / reproducibility** | PRESENT | `backend/internal/proto/request_meta.go:109` — `Seed *int` | |
| 24 | **frequency_penalty / presence_penalty** | PARTIAL | `backend/internal/gateway/upstream_dispatcher_hcsf_test.go:251,284` — tested as raw passthrough; NOT in `RequestControls` struct | Not a typed field; passed through raw body only; no per-channel override possible |
| 25 | **logit_bias / logprobs** | PARTIAL | `backend/internal/proto/field_matrix.go:150` — logprobs listed as passthrough-preserved; logit_bias raw passthrough test only | Raw passthrough via PassthroughEnvelope; no typed extraction |
| 26 | **Token counting — prompt + completion** | PRESENT | `backend/internal/proto/hcsf.go:106-128` — `CanonicalUsage` with InputTokens, OutputTokens, TotalTokens | |
| 27 | **Token counting — reasoning tokens** | PRESENT | `backend/internal/proto/hcsf.go` — `ReasoningTokens` separate field; `backend/internal/gatewayhttp/chat_completions_billing.go` — cross-check audit | Separate from OutputTokens for billing accuracy |
| 28 | **Cache-aware token counting** | PRESENT | `backend/internal/proto/hcsf.go` — `CacheCreationInputTokens`, `CacheReadInputTokens`, 5m/1h TTL variants | Anthropic prompt-cache billing tiers |
| 29 | **Per-call usage billing** | PRESENT | `backend/internal/gatewayhttp/chat_completions_billing.go` — `actualCompletionCost()`, settler EventBus integration | |
| 30 | **Cross-check / audit of reported tokens** | PRESENT | `backend/internal/gatewayhttp/chat_completions_billing.go` — `crossCheckAudit`, confidence scoring, mismatch flagging | HUAKAI-unique trust feature |
| 31 | **Exact-match response cache** | PRESENT | `backend/internal/cache/store.go` — LRU+TTL MemoryStore; `backend/internal/cache/key.go` — `BuildKey` on canonical hash | |
| 32 | **Cache-aware sticky routing (prompt prefix affinity)** | PRESENT | `backend/internal/cache_routing/auto_inject.go` — auto-injects cache_control ephemeral; PASR Track B prefix hash routes to same account | |
| 33 | **Cache breakpoints / cache_control markers** | PRESENT | `backend/internal/proto/capability_cache.go` — `CacheControlNode` with scopes (request/message/block/session/vendor), cache key hints | |
| 34 | **Cache hit metrics** | PRESENT | `backend/internal/cachemetrics/l2.go` — `ObserveL2Hit`, `ObserveL2Miss` | |
| 35 | **Semantic / embedding-based cache** | MISSING | grepped: `semantic.cache`, `SemanticCache`, `vector.cache`, `similarity.cache` — no files found | Sub2api/new-api/OpenRouter all offer semantic cache; high cost-saving value |
| 36 | **Format conversion: OpenAI ↔ Anthropic ↔ Gemini** | PRESENT | `backend/internal/proto/proto.go` — `ClientAdapter`, `UpstreamAdapter` interfaces; adapters for openai_chat, openai_responses, anthropic_messages, gemini, bedrock, antigravity | |
| 37 | **Protocol loss tracking** | PRESENT | `backend/internal/proto/` — `ProtocolLossEntry`, directions, verdicts (PRESERVED/LOSSY/UNSUPPORTED) | HUAKAI-unique; feeds audit and capability matrix |
| 38 | **Capability matrix (per-provider feature detection)** | PRESENT | `backend/internal/proto/capability_matrix.go` — 40+ feature flags × upstream protocol pairs | |
| 39 | **Native passthrough route** | PRESENT | `backend/internal/proto/client_adapter_default_registry.go:52` — `/v1/native/openai/responses`; `backend/internal/proto/request_meta.go:66-67` — `NativePassthrough bool` | Allows unmodified vendor-specific requests |
| 40 | **Idempotency / request deduplication** | PRESENT | `backend/internal/gatewayhttp/chat_completions_idempotency_replay.go` — `recordIdempotencyReplay`; payload_fingerprint unique constraint | |
| 41 | **Response post-processing hooks (general)** | PARTIAL | Only system rewrite + tool name rewrite hooks exist; no plugin-style post-processing pipeline | No generic hook chain; redaction is privacy-only |
| 42 | **Content redaction / privacy sanitisation** | PARTIAL | `backend/internal/privacy/default_redactor.go` — `AllowlistRedactor`; `backend/internal/redact/allowlist.go`; `backend/internal/privacy/redactor.go` — error-type safe mapping | Allowlist model only; no keyword blocklist; no LLM-based moderation |
| 43 | **Content moderation / keyword blocklist** | MISSING | grepped: `moderat`, `ContentFilter`, `SafetyFilter`, `toxicity`, `nsfw`, `bad_word`, `blocklist` — no files found | Sub2api has sensitive-word filter; new-api has content_filter; OpenRouter has safety layer |
| 44 | **Prompt injection detection** | MISSING | grepped: `prompt.inject`, `injection.detect`, `PromptInject`, `PromptInjection` — no files found | No attack-pattern detection on user content |
| 45 | **Output watermarking** | MISSING | grepped: `watermark`, `Watermark`, `steganograph` — no files found | Audit ledger tracks request-level; no content-level watermark |
| 46 | **Fallback content on error** | PRESENT | `backend/internal/proto/stream_plan.go` — `FallbackBoundary` (before_first_byte / after_first_byte_blocked / after_first_byte_allowed); DLQ for settlement failures | |
| 47 | **Embeddings endpoint (`/v1/embeddings`)** | MISSING | grepped: `v1/embeddings`, `v1/embed`, `EmbeddingsHandler` — no files; routes.go shows no embeddings route | Sub2api, new-api, OpenRouter all proxy embeddings; very common customer need |
| 48 | **Image generation endpoint (`/v1/images/generations`)** | MISSING | grepped: `v1/images`, `ImageCreate`, `dall.e`, `stable.diff` — no files; routes.go has no image route | OpenAI-compatible image gen passthrough missing |
| 49 | **Audio TTS endpoint (`/v1/audio/speech`)** | MISSING | grepped: `v1/audio`, `AudioCreate`, `SpeechCreate`, `text.to.speech` — no files | |
| 50 | **Audio transcription endpoint (`/v1/audio/transcriptions`)** | MISSING | grepped: `v1/audio/transcription`, `Transcription`, `Whisper` — no files; AudioNode exists as data model only | AudioNode in proto is for input; no transcription proxy endpoint |
| 51 | **Batch inference endpoint (`/v1/batches`)** | MISSING (endpoint) | `backend/internal/proto/capability_batch.go` — `BatchNode` data model complete; routes.go has NO `/v1/batches` route | BatchNode is spec-only; no handler, no queue, no status polling |
| 52 | **Realtime WebSocket endpoint (`/v1/realtime`)** | MISSING | `backend/cmd/gateway/routes.go:51` — `handleRealtimeRoadmap` returns HTTP 501 with "Phase 9+ mandatory roadmap" message | Explicitly stubbed as not-implemented |
| 53 | **Computer-use agentic passthrough** | PARTIAL | `backend/internal/proto/capability_computer_use.go` — `ComputerUseNode` with environment, action, approval_state; routes.go has no computer-use endpoint | Data model only; no HTTP handler; requires_native edge in capability graph |
| 54 | **MCP server integration** | PARTIAL | `backend/internal/proto/capability_mcp.go` — `MCPServerNode` (server_label, server_uri, allowed_operations, auth_ref) | Data model only; no active MCP proxy handler found |
| 55 | **Data retention / Zero Data Retention (ZDR) tracking** | PARTIAL | `backend/internal/proto/capability_data_retention.go` — `DataRetentionNode` (5-value enum, enforcement, region, no_train intent) | Tracking metadata only; no active enforcement or vendor-ZDR contract verification |
| 56 | **Live sessions / WebSocket bidirectional** | PARTIAL | `backend/internal/proto/capability_live.go` — `LiveSessionNode` (wss/sse transport, modalities, resume_token); routes.go:51 returns 501 for /v1/realtime | Data model only; runtime not implemented |
| 57 | **Named prompt templates (stored)** | MISSING | grepped: `PromptTemplate`, `named.template`, `stored.prompt`, `template.store` — no files | System rewrite supports prefix/suffix injection but not stored named templates with variable substitution |
| 58 | **Variable substitution in prompts** | MISSING | No template engine or variable interpolation found | |
| 59 | **Conversation history management (stateful)** | MISSING | grepped: `ConversationHistory`, `history.trim`, `MessageHistory`, `TrimHistory`, `summarize.context` — no files | No session-level history storage, trimming, or summarisation |
| 60 | **Context summarisation (auto-compress long contexts)** | MISSING | No summarisation module found | Common in sub2api (history management) and OpenRouter (auto-truncate) |
| 61 | **RAG / retrieval hooks** | MISSING | grepped: `RAG`, `retrieval.augment`, `VectorStore`, `ChunkRetrieval` — no files | No pre-LLM retrieval pipeline |
| 62 | **Per-channel parameter defaults (channel config)** | MISSING | No "channel" concept with configurable defaults found; RequestControls is per-request only | New-api has channel-level temperature/max_tokens/system_prompt config; sub2api has similar |
| 63 | **`/v1/moderations` endpoint** | MISSING | grepped: `v1/moderations`, `ModerationHandler` — no files | OpenAI moderation API passthrough absent |
| 64 | **Logprob output passthrough** | PARTIAL | `backend/internal/proto/field_matrix.go:150` — logprobs passthrough-preserved via PassthroughEnvelope; no typed extraction or filtering | Raw passthrough only; no per-token logprob normalisation |
| 65 | **`n` (multiple completions) parameter** | PARTIAL | Appears in raw passthrough tests (`backend/internal/gateway/upstream_dispatcher_hcsf_test.go:251` — `"n":2`); NOT in RequestControls struct | Raw body passthrough only; no typed handling or cost multiplication |

---

## Top Missing Features, Ranked by Commercial Value

1. **Embeddings endpoint `/v1/embeddings`** — Extremely common customer need; required for RAG pipelines, semantic search, and classification. Sub2api and new-api both proxy this. Every OpenAI SDK user expects it. High churn risk if absent.

2. **Batch inference endpoint `/v1/batches`** — 50% cost discount on OpenAI Batch API; enterprises use it for bulk evaluation, fine-tune data prep, and async processing. BatchNode data model already exists — needs a handler, queue, and status-polling route.

3. **Content moderation pipeline (keyword blocklist + safety filter)** — Compliance requirement for enterprise and regulated markets. Sub2api has sensitive-word filter; new-api has `content_filter`. Without this, HUAKAI cannot serve customers in education, healthcare, or government verticals.

4. **Semantic / embedding-based cache** — Reduces upstream LLM costs by 20–40% for repetitive prompts. Sub2api, new-api, and OpenRouter all offer this. Exact-match cache already present; semantic layer is the next increment.

5. **Image generation passthrough `/v1/images/generations`** — Required for any customer using DALL-E 3, Flux, or Stable Diffusion via the OpenAI-compatible API. Route simply needs to forward to provider; format conversion is simpler than chat.

6. **Audio TTS `/v1/audio/speech` and transcription `/v1/audio/transcriptions`** — Whisper and TTS are increasingly bundled into AI applications. Passthrough only required initially.

7. **Realtime WebSocket `/v1/realtime`** — Currently hard-coded 501. Required for voice assistants and real-time copilots. LiveSessionNode data model and capability_live.go already exist.

8. **Per-channel parameter defaults** — New-api and sub2api let admins set default temperature, max_tokens, and system_prompt per channel/API-key group. Without this, operators cannot offer tiered service levels (creative vs. precise modes) without client-side changes.

9. **Prompt injection detection** — Security feature; increasingly required by enterprise security reviews. No detection whatsoever found in codebase.

10. **Conversation history management (stateful sessions)** — Sub2api and new-api support server-side message history with configurable retention and truncation. Stateless clients (mobile apps, chatbots) need this.

11. **Named prompt templates with variable substitution** — Sub2api supports stored templates. Enables operators to define reusable prompts without exposing system prompt details to end-users.

12. **`n` (multiple completions) as typed field** — Currently raw-passthrough only. Billing implications (cost × n) and load balancing (need n responses from one account) require typed handling.

13. **`frequency_penalty` / `presence_penalty` as typed fields** — Currently raw-passthrough only. Cannot be overridden per-channel or enforced by operator policy.

14. **`/v1/moderations` endpoint** — Required by customers using OpenAI-compatible SDKs that call moderation before sending to chat. Simple passthrough to OpenAI/AWS Comprehend.

15. **Output watermarking** — Enterprise-grade feature for audit trails and content provenance. Audit ledger provides request-level tracking but not content-level watermarking.
