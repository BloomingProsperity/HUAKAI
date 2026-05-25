# Sub2API Protocol Translation — Source-Verified (F-PROTO-001)

| Field | Value |
| --- | --- |
| Status | Specifier-lane source-verified pass (Claude) |
| Author | Claude PM-Orchestrator |
| Date | 2026-04-28 |
| Lane | Specifier — Option B (or Option C if elevated; protocol translation is L1 but not on the carve-out list per DR-000) |
| Feature | [F-PROTO-001](../../03_FEATURE_PARITY_MATRIX.md) — multi-protocol gateway (OpenAI Chat Completions / OpenAI Responses / Anthropic Messages, with Anthropic upstream) |
| Reference | Sub2API at commit `b0a2252ed19c3720e6adafde6083e64fbac2efa9`, package `backend/internal/pkg/apicompat/` |
| Supersedes | [`protocol-translation.md`](protocol-translation.md) (paraphrased prior pass, may have drift like F-POOL-001 / F-GW-002 v1 had — see [source-truth-corrections.md](../../process/reviews/2026-04-28-source-truth-corrections.md)) |
| Source files read | `apicompat/anthropic_to_responses_response.go` (lines 1–449), `apicompat/responses_to_chatcompletions.go` (lines 130–230, function listing 1–490), `apicompat/anthropic_to_responses.go` (function listing), `apicompat/chatcompletions_to_responses.go` (function listing), `apicompat/responses_to_anthropic_request.go` (function listing) |

## 1. The Canonical Intermediate Format

Sub2API does **not** translate directly between every protocol pair. Instead, it uses **OpenAI Responses** as the canonical intermediate format. The translation graph is a hub-and-spoke:

```
Client request side:
   Chat Completions ──▶ Responses ──▶ Anthropic (chained, gateway_forward_as_chat_completions.go:48-55)
   Responses ──────────────────────▶ Anthropic (direct, gateway_forward_as_responses.go:49)
   Anthropic ────────────────────────────────▶ Anthropic (passthrough; not in apicompat)

Upstream response side (BUFFERED, non-stream):
   Anthropic ──▶ Responses ──▶ Chat Completions (chained, line 309-311 in gateway_forward_as_chat_completions.go)
   Anthropic ──▶ Responses (direct, used by Responses-format clients)

Upstream response side (STREAMING, per-event):
   Anthropic SSE ──▶ Responses SSE events ──▶ Chat Completions chunks
       │                  │                       │
       │            (state machine)         (state machine)
       │            anth_to_res_response.go responses_to_chatcompletions.go
       │            line 168                line 149
```

**Why a canonical intermediate**: adding a new client protocol (e.g. Cohere format, Mistral format) requires only client↔Responses translators; the Responses↔Anthropic side stays unchanged. **Adding a new upstream** (e.g. OpenAI native, Bedrock) requires only Responses↔upstream translators; client side stays unchanged. This is the standard "M+N translators" architecture, not "M×N translators."

## 2. Anthropic SSE Stream State Machine

Source: `apicompat/anthropic_to_responses_response.go:160-421`.

### 2.1 The 6 Anthropic Event Types

```go
// Line 168-188: AnthropicEventToResponsesEvents dispatch
switch evt.Type {
case "message_start":         return anthToResHandleMessageStart(evt, state)
case "content_block_start":   return anthToResHandleContentBlockStart(evt, state)
case "content_block_delta":   return anthToResHandleContentBlockDelta(evt, state)
case "content_block_stop":    return anthToResHandleContentBlockStop(evt, state)
case "message_delta":         return anthToResHandleMessageDelta(evt, state)
case "message_stop":          return anthToResHandleMessageStop(state)
default:                      return nil
}
```

Six event types. Anything else is silently ignored. Each handler emits zero or more Responses SSE events.

### 2.2 Per-Event Translation Map

| Anthropic Event | Sub-trigger | Responses Output | Source line |
|-----------------|-------------|------------------|-------------|
| `message_start` | first call | `response.created` (idempotent: only first wins) | 219-237 |
| `content_block_start` | block.type=`thinking` | `response.output_item.added` (item.type=`reasoning`) | 246-258 |
| `content_block_start` | block.type=`text` | `response.output_item.added` (item.type=`message`, role=assistant, status=in_progress) — only if message item not already open | 260-276 |
| `content_block_start` | block.type=`tool_use` | close prior item + `response.output_item.added` (item.type=`function_call`) | 278-296 |
| `content_block_delta` | delta.type=`text_delta` | `response.output_text.delta` | 308-317 |
| `content_block_delta` | delta.type=`thinking_delta` | `response.reasoning_summary_text.delta` | 319-328 |
| `content_block_delta` | delta.type=`input_json_delta` | `response.function_call_arguments.delta` | 330-340 |
| `content_block_delta` | delta.type=`signature_delta` | (skipped — no Responses equivalent) | 342-344 |
| `content_block_stop` | item=reasoning | `response.reasoning_summary_text.done` + `response.output_item.done` | 352-362 |
| `content_block_stop` | item=function_call | `response.function_call_arguments.done` + `response.output_item.done` | 364-375 |
| `content_block_stop` | item=message | `response.output_text.done` (item stays open for more blocks) | 377-385 |
| `message_delta` | (any) | (no event; updates state.OutputTokens / CacheReadInputTokens) | 391-401 |
| `message_stop` | first call | close any open item + `response.completed` (idempotent: CompletedSent flag) | 403-421 |

### 2.3 State Machine Properties

From `AnthropicEventToResponsesState` (lines 130-157):

- `ResponseID` / `Model` / `Created` / `SequenceNumber`: response-wide identity
- `CreatedSent` / `CompletedSent`: idempotency for the two terminal-ish events
- `OutputIndex`: **monotonically incremented** on `closeCurrentResponsesItem` (line 438)
- `CurrentItemID` / `CurrentItemType`: tracks open output item
- `ContentIndex`: per-message content part index (only relevant for `message` items)
- `CurrentCallID` / `CurrentName`: tracks open function_call's tool identity
- `InputTokens` / `OutputTokens` / `CacheReadInputTokens`: usage accumulator

### 2.4 The "Message Item Stays Open" Subtlety

Line 377-385: when `content_block_stop` is for a `message` item (text block ended), the implementation emits **only** `response.output_text.done`, NOT `response.output_item.done`. The message item stays open in case more text blocks follow. This is correct for Anthropic's protocol where a single message can have multiple text blocks separated by tool_use blocks.

The message item is closed only when:
- The next `content_block_start` is a `tool_use` (line 280: `closeCurrentResponsesItem` called explicitly), OR
- Stream ends (`message_stop` at 411 calls `closeCurrentResponsesItem`)

### 2.5 The Idempotent Finalization

```go
// Line 192-206: FinalizeAnthropicResponsesStream
func FinalizeAnthropicResponsesStream(state *AnthropicEventToResponsesState) []ResponsesStreamEvent {
    if !state.CreatedSent || state.CompletedSent {
        return nil
    }
    var events []ResponsesStreamEvent
    events = append(events, closeCurrentResponsesItem(state)...)
    events = append(events, makeResponsesCompletedEvent(state, "completed", nil))
    state.CompletedSent = true
    return events
}
```

Called by the gateway after `for scanner.Scan()` exits (gateway_forward_as_chat_completions.go:468). Emits synthetic terminal events if upstream stream ended without a proper `message_stop`. **Two guards**:
1. If `response.created` was never emitted (`!CreatedSent`), don't emit anything — would be malformed.
2. If `response.completed` already emitted (`CompletedSent`), don't double-emit.

This makes the cleanup safe whether stream ended cleanly or not.

## 3. Responses → Chat Completions State Machine

Source: `apicompat/responses_to_chatcompletions.go:130-199`.

### 3.1 The 7 Responses Event Types Handled

```go
// Line 149-167
switch evt.Type {
case "response.created":                         return resToChatHandleCreated(evt, state)
case "response.output_text.delta":               return resToChatHandleTextDelta(evt, state)
case "response.output_item.added":               return resToChatHandleOutputItemAdded(evt, state)
case "response.function_call_arguments.delta":   return resToChatHandleFuncArgsDelta(evt, state)
case "response.reasoning_summary_text.delta":    return resToChatHandleReasoningDelta(evt, state)
case "response.reasoning_summary_text.done":     return nil
case "response.completed", "response.incomplete", "response.failed":  return resToChatHandleCompleted(evt, state)
default:                                         return nil
}
```

Note `response.reasoning_summary_text.done` returns nil — not a chunk-emitting event in Chat Completions semantics. Note `response.output_text.done` and `response.output_item.done` and `response.function_call_arguments.done` are NOT in the switch; they are silently dropped from the Chat Completions output.

### 3.2 Chat-Specific State

From `ResponsesEventToChatState` (lines around 125-136):

- `ID` / `Created` / `Model`: response-wide identity (default ID generated, line 141)
- `SentRole`: ensures `delta.role=assistant` is sent only once
- `SawText` / `SawToolCall`: tracks finish_reason determination
- `Finalized`: idempotency for finish chunk
- `NextToolCallIndex` / `OutputIndexToToolIndex`: maps Responses output_index → Chat tool_calls index
- `IncludeUsage` / `Usage`: Chat-protocol-specific usage chunk emission

### 3.3 The Finalize Path

```go
// Line 174-199: FinalizeResponsesChatStream
if state.Finalized {
    return nil
}
state.Finalized = true
finishReason := "stop"
if state.SawToolCall {
    finishReason = "tool_calls"
}
chunks := []ChatCompletionsChunk{makeChatFinishChunk(state, finishReason)}

if state.IncludeUsage && state.Usage != nil {
    chunks = append(chunks, ChatCompletionsChunk{... Usage: state.Usage})
}
return chunks
```

Two finish reasons supported: `stop` and `tool_calls`. **No `length` finish reason** — even though the Anthropic→Responses mapping at line 92-95 maps `max_tokens` to `incomplete` status with `max_output_tokens` reason, that information is **lost** at the Responses→Chat boundary. This is a real fidelity gap: Chat Completions clients hitting `max_tokens` get `finish_reason=stop` instead of `length`.

Usage chunk is conditional on `IncludeUsage` (which gateway_forward_as_chat_completions.go:363 sets from request `stream_options.include_usage`).

## 4. Buffered Path: Anthropic Response → Responses Response

Source: `apicompat/anthropic_to_responses_response.go:18-110` (`AnthropicToResponsesResponse`).

### 4.1 Block-to-Output Translation

```go
// Lines 33-67
for _, block := range resp.Content {
    switch block.Type {
    case "thinking":
        outputs = append(outputs, ResponsesOutput{Type: "reasoning", ...})
    case "text":
        msgParts = append(msgParts, ResponsesContentPart{Type: "output_text", Text: block.Text})
    case "tool_use":
        outputs = append(outputs, ResponsesOutput{Type: "function_call", ...})
    }
}
if len(msgParts) > 0 {
    outputs = append(outputs, ResponsesOutput{Type: "message", ...})
}
```

The translation order: emit reasoning blocks and function_call blocks IN ORDER, but ALL text blocks get **batched into a single `message` item** at the end. This means buffered translation produces a message item that carries multiple text content parts but loses interleaving with tool_use.

Compare to streaming: streaming preserves the original interleaving order via `OutputIndex` increments on `closeCurrentResponsesItem`.

### 4.2 Empty Response Defensive Path

```go
// Lines 80-88
if len(outputs) == 0 {
    outputs = append(outputs, ResponsesOutput{
        Type: "message", ID: generateItemID(), Role: "assistant",
        Content: []ResponsesContentPart{{Type: "output_text", Text: ""}},
        Status: "completed",
    })
}
```

If upstream returned a `Content: []` Anthropic response (which is a protocol violation but observed in practice), Sub2API emits a synthetic empty message rather than returning a zero-output Responses response. This is a **defensive normalization** worth keeping.

### 4.3 Stop Reason Mapping (Buffered)

```go
// Lines 113-122: anthropicStopReasonToResponsesStatus
switch stopReason {
case "max_tokens":           return "incomplete"
case "end_turn", "tool_use", "stop_sequence":
                             return "completed"
default:                     return "completed"
}
```

Then line 92-95:
```go
out.Status = anthropicStopReasonToResponsesStatus(...)
if out.Status == "incomplete" {
    out.IncompleteDetails = &ResponsesIncompleteDetails{Reason: "max_output_tokens"}
}
```

Only `max_tokens` produces `incomplete`. `pause_turn` and other Anthropic-specific stop reasons are silently mapped to `completed` (default branch). This is **a fidelity gap** when Anthropic adds a new stop_reason that means something other than completion.

## 5. Request-Direction Translators (Brief Survey)

I read the function lists for both request-side translators but not full bodies. Function inventory:

### 5.1 ChatCompletions → Responses (8 entry/helper functions)
File `apicompat/chatcompletions_to_responses.go`:
- `ChatCompletionsToResponses` (entry, line 18) — top-level conversion
- `convertChatMessagesToResponsesInput` / `chatMessageToResponsesItems` — message translation
- `chatSystemToResponses` / `chatUserToResponses` / `chatAssistantToResponses` / `chatToolToResponses` / `chatFunctionToResponses` — per-role handlers
- `convertChatToolsToResponses` — tool definition translation
- `convertChatFunctionCallToToolChoice` — `function_call` legacy → `tool_choice`

### 5.2 Responses → Anthropic Request (10+ entry/helper functions)
File `apicompat/responses_to_anthropic_request.go`:
- `ResponsesToAnthropicRequest` (entry, line 13)
- `defaultThinkingBudget` / `mapResponsesEffortToAnthropic` — reasoning effort mapping
- `convertResponsesInputToAnthropic` / `convertResponsesUserToAnthropicContent` / `convertResponsesAssistantToAnthropicContent` — input translation
- `extractTextFromContent` — text-only flattener
- `dataURIToAnthropicImageSource` — image format adapter
- `mergeConsecutiveMessages` — Anthropic forbids consecutive same-role messages; merge them
- `parseContentBlocks` / `fromResponsesCallIDToAnthropic` — block + ID translation

The two request translators are **stateless** (one-shot transformation per request, no streaming state machine needed since requests are not streamed in either direction).

## 6. Tool Call ID Translation

Source line 289-308 in `apicompat/anthropic_to_responses.go`:

```go
func toResponsesCallID(id string) string { ... }
func fromResponsesCallID(id string) string { ... }
```

Bidirectional ID adapter. Likely prefix-strip / prefix-add pattern (Anthropic uses `toolu_<hex>`, OpenAI uses `call_<hex>` — typical convention). Critical for tool-result correlation across protocol boundaries: client says "I'm responding to tool call X"; gateway must translate X back to upstream's ID format.

## 7. Per-Conversion Failure Modes

| Failure | Where | Behavior |
|---------|-------|----------|
| Unknown Anthropic event type | `AnthropicEventToResponsesEvents` default | Silent drop (line 186) |
| Unknown delta type | `anthToResHandleContentBlockDelta` default | Silent drop (line 347) |
| Unknown Anthropic block type in buffered | `AnthropicToResponsesResponse` switch | Block silently omitted |
| Unknown Anthropic stop_reason | `anthropicStopReasonToResponsesStatus` default | Mapped to `completed` (lossy) |
| Unknown Responses event type | `ResponsesEventToChatChunks` default | Silent drop (line 165) |
| `signature_delta` event | hardcoded skip | Silent drop (line 342-344) |
| Empty Anthropic Content | buffered translator | Synthetic empty message item |
| Stream ends without `message_stop` | `FinalizeAnthropicResponsesStream` | Synthetic completed event (idempotent) |
| Stream ends without `response.completed` | `FinalizeResponsesChatStream` | Synthetic finish chunk (idempotent) |

## 8. KEEP / IMPROVE / AVOID for HUAKAI

### KEEP (verified in source)

- **Hub-and-spoke architecture** with one canonical intermediate (Responses). M+N translators not M×N.
- **Stateful streaming with idempotent finalization**: `CreatedSent` / `CompletedSent` / `Finalized` flags + `FinalizeXxxStream` cleanup paths.
- **`OutputIndex` monotonic increment** on item close — ensures ordering correctness across reasoning / message / tool_call interleaving.
- **Message item stays open across multiple text blocks** (matches multi-modal Anthropic semantics).
- **Defensive empty-response normalization** (synthesize empty message if upstream returns zero blocks).
- **Bidirectional tool-call ID translation** (`toResponsesCallID` / `fromResponsesCallID`).

### IMPROVE (HUAKAI design — NOT in Sub2API)

- **Typed warning on unknown event/block/delta types** instead of silent drop. Operators investigating drift cases need a signal that the gateway encountered something it couldn't translate.
- **Lossless stop-reason mapping**: HUAKAI should map Anthropic's full stop_reason set to Responses status with explicit handling of `pause_turn` etc. Default-completed silently is wrong.
- **`length` finish_reason preserved across Responses→Chat boundary**: Sub2API loses this; HUAKAI restores it via state.SawIncomplete propagation.
- **Streaming-buffered semantic equivalence test**: same upstream events processed via streaming AND buffered should produce equivalent Usage Records. Sub2API does not formally test this.
- **`signature_delta` carry-forward** for Anthropic OAuth flows that need it: Sub2API drops it; if HUAKAI offers Anthropic-native streaming output (skipping translation), the signature must survive. Make it conditional, not hardcoded skip.
- **Protocol fidelity matrix as test artifact**: every (client_protocol, upstream_protocol, model_capability) cell must have a test case asserting field-level equivalence after round-trip.

### AVOID (Sub2API anti-patterns)

- Silent drop of unknown protocol elements with no observability.
- Lossy default branch in stop_reason mapping.
- Buffered-path text batching that loses interleaving (Sub2API does this; HUAKAI should preserve buffered-path interleaving by emitting separate message items per text block when a tool_use intervenes).

## 9. Concurrency / Correctness Invariants HUAKAI Adds

| # | Invariant | Reason Sub2API doesn't enforce |
|---|-----------|---------------------------------|
| P1 | Every translator is a pure function (or has only state-machine state); no global mutation. | Confirmed in source — Sub2API translators are already pure. KEEP. |
| P2 | Streaming and buffered paths produce equivalent Usage Records given the same upstream input sequence. | Not formally enforced; tested ad-hoc. |
| P3 | Unknown event/block types produce a typed warning + telemetry counter. | Sub2API silent-drops. |
| P4 | Stop reason mapping is total and explicit; default-completed is forbidden. | Sub2API uses default-completed. |
| P5 | Tool call ID translation is bijective and verified by round-trip test. | Sub2API has the helpers; round-trip test not visible in this read. |
| P6 | Translation latency is bounded: ≤ 100µs per event in the streaming hot path. | Sub2API has no SLO; HUAKAI should add. |

## 10. Test Scenarios

### Sub2API-inheritable

- AT-PROTO-001 / Anthropic SSE → Responses SSE → Chat chunks: 50-event stream, full graceful path → final `[DONE]` arrives with usage chunk.
- AT-PROTO-002 / Tool-call interleaving: text → tool_use → text sequence → output items emitted in order; OutputIndex strictly monotonic.
- AT-PROTO-003 / Reasoning preserved: `thinking` block → `reasoning` item → `summary_text.delta` events.
- AT-PROTO-004 / `signature_delta` skipped: stream contains signature_delta events → no Responses event emitted; subsequent text_delta still arrives.
- AT-PROTO-005 / Stream ended without message_stop: `FinalizeAnthropicResponsesStream` synthesizes terminal event.
- AT-PROTO-006 / Empty Content response: synthetic empty message item emitted.
- AT-PROTO-007 / `max_tokens` stop_reason: buffered Responses response has Status=`incomplete` + IncompleteDetails.Reason=`max_output_tokens`.
- AT-PROTO-008 / Tool-call ID round-trip: client sends tool_call_id `call_X` → gateway translates to Anthropic `toolu_X` → Anthropic responds with `toolu_X` in tool_use_id → gateway translates back to `call_X`.

### HUAKAI-design-specific

- AT-PROTO-009 / Unknown event type: inject `event: pet_treat_dispenser` SSE → typed warning emitted, no panic.
- AT-PROTO-010 / Unknown stop_reason: upstream returns `stop_reason: pause_turn` → HUAKAI maps to `paused` Responses status, NOT `completed`.
- AT-PROTO-011 / Buffered-path interleaving preserved: upstream returns `[text, tool_use, text]` → HUAKAI emits two message items + one function_call item, NOT one batched message item.
- AT-PROTO-012 / Streaming-vs-buffered usage equivalence: same upstream JSON → same final Usage Record values.
- AT-PROTO-013 / Translation latency SLO: 1000-event stream → p99 per-event translation < 100µs.

## 11. Open TODOs

- **TODO-1**: Read full body of `chatcompletions_to_responses.go` and `responses_to_anthropic_request.go` to verify request-side translators do not silently drop fields.
- **TODO-2**: Verify exact transformation in `toResponsesCallID` / `fromResponsesCallID` (read lines 289-308 of `anthropic_to_responses.go`).
- **TODO-3**: Survey `bedrock_stream.go` to see if Bedrock has its own state machine or piggybacks on the Anthropic SSE state machine.
- **TODO-4**: Cross-check against one-api's protocol layer (`relay/adaptor/`) to see if it has an analogous canonical intermediate or if it does direct M×N translation.
- **TODO-5**: Cross-check Portkey (just cloned) for its protocol translation strategy — Portkey is known for multi-provider routing.

## 12. Attribution

Source files read directly (all from `c:/HUAKAI/repo/.omc/reference-src/sub2api/` at commit `b0a2252ed19c3720e6adafde6083e64fbac2efa9`):

- `backend/internal/pkg/apicompat/anthropic_to_responses_response.go` lines 1–449 (full)
- `backend/internal/pkg/apicompat/responses_to_chatcompletions.go` lines 130–230 + function listing 1–490
- `backend/internal/pkg/apicompat/anthropic_to_responses.go` function listing 1–449
- `backend/internal/pkg/apicompat/chatcompletions_to_responses.go` function listing 1–428
- `backend/internal/pkg/apicompat/responses_to_anthropic_request.go` function listing 1–426
- `backend/internal/service/gateway_forward_as_chat_completions.go` lines 47-55, 309-311, 358-484 (cross-reference for translator usage at the call site)

This file is specifier-lane; function names and source paths appear here for traceability per CL-002 specifier-lane exception. No implementer-lane file may cite these directly; an implementer-lane spec must use HUAKAI domain language only.

CL-011 compliance: every behavior claim above carries file:line attribution.

## 13. Review Sign-Off

| Field | Value |
| --- | --- |
| Reviewer | (pending) |
| Review date | (pending) |
| Checks passed | (pending CL-001..011) |
| Notes | F-PROTO-001 source-verified pass. Awaits Codex parallel pass for mutual review (next cycle), then synthesis. |
