# HUAKAI Bedrock Plan A4 - BedrockEventStreamAdapter

| Owner directive | "你是 codex lane (PLANNER)。只写 plan，不写代码。任务: HUAKAI Bedrock plan A4 - 设计 backend/internal/proto/BedrockEventStreamAdapter。" |
| --- | --- |
| Lane | codex |
| Time | 2026-05-08 |

## Scope

In scope:
- Design a new `backend/internal/proto` upstream adapter named `BedrockEventStreamAdapter`.
- Translate A3 output into HCSF `CanonicalEvent` values. A3 has already decoded AWS Binary EventStream frames and Bedrock `{"bytes":"<base64>"}` chunk envelopes, so A4 receives the inner Anthropic JSON event bytes.
- Keep `proto` independent from `gateway`; `proto` must not import `backend/internal/gateway`.
- Reuse the existing Anthropic event semantics where valid, because Bedrock Claude chunk inner JSON is Anthropic-format JSON.
- Define state expectations, accepted provider event input shapes, error behavior, and focused unit tests.

Out of scope:
- No code implementation in this planner lane.
- No AWS Binary EventStream decoding changes; A2 owns that.
- No chunk envelope/base64 handling changes; A3 owns that.
- No gateway registry wiring, scanner selection, `bedrock_invoke` enablement, provider HTTP request signing, or Bedrock request body translation.
- No non-streaming Bedrock response adapter.
- No reading or copying `aws-sdk-go`, `botocore`, or `aws-encryption-sdk` source.

## Success Criteria

- `BedrockEventStreamAdapter` implements `proto.UpstreamAdapter`.
- Normal A3 payloads (`[]byte` containing inner Anthropic event JSON) produce the same canonical events as `AnthropicAdapter` for Claude-on-Bedrock.
- The adapter has an explicit Bedrock type boundary, so future Bedrock-on-Llama / Bedrock-on-Cohere support can branch without pretending every Bedrock stream is Anthropic.
- `proto` has zero dependency on `gateway`.
- Malformed inner JSON, unknown inner event types, unknown deltas, duplicate terminal events, and EOF finalization behave consistently with HCSF/F-PROTO-002.
- Tests cover happy path, tool use, reasoning, signature policy, unknown event handling, malformed payload, and finalization.
- No clean-room violation: only HUAKAI internal code and public protocol facts are used.

## Time Estimate

- Planning only: 20-30 minutes.
- Implementation after Owner-approved synthesized plan: 1.5-2.5 hours.
- Focused tests after implementation: 30-45 minutes.
- Optional gateway registry integration in a later atomic: 1-2 hours because it touches `gateway` behavior and existing tests.

## Blast Radius

- Primary blast radius: `backend/internal/proto`.
- Test blast radius: `backend/internal/proto/*_test.go`.
- Should not affect runtime until a later gateway registration/integration atomic wires `bedrock_invoke` to this adapter.
- If implemented by mutating shared adapter state, concurrent streams could race. The design should keep the adapter stateless except for immutable policy fields.
- If gateway later passes only `evt.Data`, normal chunk events work, but A3 protocol-level error events lose `evt.Type="error"` context. That is an integration decision for a later atomic, not something `proto` can solve by importing `gateway`.

## Failure Modes

- Wrong package dependency: importing `gateway` from `proto` creates a reverse dependency. Mitigation: accept `[]byte` and/or a proto-local event wrapper.
- Over-reuse of `AnthropicAdapter`: treating all Bedrock model families as Anthropic would silently break non-Claude Bedrock models. Mitigation: expose a Bedrock-specific adapter type and document current support as Claude/Anthropic-on-Bedrock only.
- Data race: storing a lazily initialized inner `*AnthropicAdapter` on a registry-shared adapter can race under concurrent requests. Mitigation: compose by value or create a local Anthropic delegate per call; no mutation in `ProviderEventToCanonicalEvents`.
- Tool-call ID mismatch: current Anthropic delegate expects Anthropic-style tool IDs, while `UpstreamProtocolBedrock` has its own helper prefix in `tool_call_id.go`. Because the task states Bedrock Claude inner JSON is Anthropic format, A4 should preserve Anthropic-style IDs for Claude-on-Bedrock and add a test. Future non-Anthropic Bedrock models need a model-family branch.
- Protocol-level Bedrock exceptions: A3 emits `SSEEvent{Type:"error", Data: raw payload}` before yielding `ErrBedrockException`. If a future gateway integration passes only `Data`, A4 sees JSON without an Anthropic `type` and returns unknown/malformed event behavior. Mitigation: later integration should either not route protocol-level error events through A4, or pass a proto-local wrapper preserving `Type`.
- Spec tension: F-PROTO-002 says Bedrock has a separate state machine, while A4 context says inner JSON is Anthropic. Mitigation: make Bedrock a separate adapter type with a shared/delegated Anthropic Claude state machine for the current model family; do not register it as native Anthropic.

## Decision Points

1. Reuse `AnthropicAdapter`?

   Recommendation: yes, but only behind a separate `BedrockEventStreamAdapter` type. The public adapter identity should be Bedrock; the current Claude-on-Bedrock implementation can delegate the actual inner JSON translation to `AnthropicAdapter` because A3 already outputs Anthropic JSON. This avoids duplicated parser/state-machine logic and keeps future Bedrock model-family branching possible.

2. How to handle `SSEEvent` across packages?

   Recommendation: do not move or import `gateway.SSEEvent` into `proto`. For A4, accept `[]byte` as the main provider event because current `StreamForwarder` already passes `evt.Data` to adapters. Optionally define a proto-local wrapper:

   ```go
   type BedrockEventStreamEvent struct {
       Type string
       Data []byte
   }
   ```

   This wrapper lets a later gateway integration preserve `Type="error"` without a reverse import. A4 can support both `[]byte` and `BedrockEventStreamEvent`.

3. Should A4 update gateway registries?

   Recommendation: no. Keep A4 as proto-only. Registry wiring for `bedrock_invoke` should be a later atomic because it changes runtime routing and must coordinate both protocol adapter registry and stream scanner registry.

4. Should A4 introduce new error sentinels?

   Recommendation: minimal new errors. Reuse `ErrNotImplemented` for request/buffered paths and `ErrUnknownEventType` / existing protocol-loss behavior from `AnthropicAdapter` for inner Anthropic events. Add `ErrBedrockProtocolEvent` only if A4 accepts `BedrockEventStreamEvent{Type:"error"}` and intentionally reports Bedrock protocol-level exception payloads as non-canonical terminal errors.

## Design Outline

### Structs

- `type BedrockEventStreamAdapter struct { CarryForwardSignatureDelta bool }`
  - Stateless and safe for registry reuse.
  - No mutable cached `inner` pointer.

- Optional proto-local input wrapper:
  - `type BedrockEventStreamEvent struct { Type string; Data []byte }`
  - Used only to avoid importing `gateway.SSEEvent`.

### Key Functions

- `CanonicalToProviderRequest(ctx context.Context, canonical *HCSF) ([]byte, []ProtocolLossEntry, error)`
  - Return `ErrNotImplemented`; Bedrock request translation is out of A4.

- `ProviderResponseToCanonical(ctx context.Context, raw []byte) (*HCSF, []ProtocolLossEntry, error)`
  - Return `ErrNotImplemented`; non-streaming Bedrock response is out of A4.

- `ProviderEventToCanonicalEvents(ctx context.Context, providerEvt any, state any) ([]any, []ProtocolLossEntry, error)`
  - Validate `state.(*UpstreamState)`.
  - Coerce input:
    - `[]byte`: inner Anthropic JSON from A3.
    - `string`: optional convenience for tests; convert to bytes.
    - `BedrockEventStreamEvent`: if `Type=="error"`, return a typed Bedrock protocol error/loss if that sentinel is adopted; otherwise use `Data`.
  - Instantiate local `AnthropicAdapter{CarryForwardSignatureDelta: s.CarryForwardSignatureDelta}` and delegate.
  - Return canonical events as `[]any`, preserving the `UpstreamAdapter` interface.

- `FinalizeUpstreamStream(ctx context.Context, state any) ([]any, error)`
  - Validate `*UpstreamState`.
  - Delegate to a local `AnthropicAdapter` to synthesize open `content_block_stop` and `message_stop`.

- `coerceBedrockInnerEvent(providerEvt any) ([]byte, string, error)`
  - Keeps input-shape handling localized.
  - Returns payload bytes plus optional outer/A3 event type.

### Error Types

- Reuse:
  - `ErrNotImplemented`
  - `ErrUnknownEventType`
  - JSON unmarshal errors from the Anthropic delegate
  - `ProtocolLossEntry` from `newLossEntry(...)`

- Optional new sentinel:
  - `ErrBedrockProtocolEvent = errors.New("proto: Bedrock protocol-level event is not a canonical model event")`
  - Only needed if A4 accepts a proto-local wrapper and explicitly detects `Type=="error"`.

## Testing Matrix

1. Happy path Claude-on-Bedrock stream:
   - Input: `message_start`, `content_block_start`, text `content_block_delta`, `content_block_stop`, `message_delta` with usage, `message_stop` as `[]byte`.
   - Assert: canonical event order, message ID/model, text delta, usage, terminal state.

2. A3 payload boundary without gateway import:
   - Input: `BedrockEventStreamEvent{Type:"content_block_delta", Data:<inner JSON>}` if wrapper is added.
   - Assert: adapter uses `Data`; `proto` tests do not import `gateway`.

3. Tool-use block preservation:
   - Input: Anthropic-format `tool_use` content block from Bedrock Claude inner JSON.
   - Assert: canonical `tool_use` block, index preservation, call ID canonicalization, no silent loss. Record that current A4 expects Anthropic-style Claude tool IDs.

4. Reasoning and signature policy:
   - Input: `thinking_delta` and `signature_delta`.
   - Assert: reasoning maps to `reasoning_delta`; signature is skipped with `FeatureSignatureDelta` loss by default and emitted only when `CarryForwardSignatureDelta=true`.

5. Unknown inner event type:
   - Input: `{"type":"future_bedrock_claude_event"}`.
   - Assert: no canonical event, `errors.Is(err, ErrUnknownEventType)`, protocol-loss entry present.

6. Malformed inner JSON:
   - Input: invalid bytes that A3 could theoretically pass after base64 decode.
   - Assert: non-nil error, no canonical events; no panic.

7. EOF finalization:
   - Input: started message and open content block, then call `FinalizeUpstreamStream`.
   - Assert: synthetic `content_block_stop` and `message_stop`; second finalize is idempotent.

8. Bedrock protocol-level error wrapper, if adopted:
   - Input: `BedrockEventStreamEvent{Type:"error", Data:{"message":"rate limited"}}`.
   - Assert: no canonical model event; typed Bedrock protocol error/loss. If wrapper is not adopted in A4, mark this as an A5 gateway integration test instead.

9. State type guard:
   - Input: valid event with wrong state type.
   - Assert: clear `proto:` error and no panic.

10. Concurrency smoke:
   - Input: many goroutines sharing one adapter instance, each with its own `UpstreamState`.
   - Assert: no data race under `go test -race` in future verification; design avoids mutable shared inner pointer.

## Pre-Execution Checklist

- Confirm Owner-approved synthesized plan exists before implementation.
- Confirm no one expects A4 to register `bedrock_invoke` in gateway in the same atomic.
- Confirm whether `BedrockEventStreamEvent` wrapper is wanted now or deferred to A5.
- Confirm whether `ErrBedrockProtocolEvent` should be introduced now or deferred to integration.
- Before editing, check working tree for existing untracked/parallel files and do not overwrite another lane's work.
- Run targeted tests after implementation: `go test ./backend/internal/proto`.
- If gateway integration is later added, also run `go test ./backend/internal/gateway ./backend/cmd/gateway`.

## Referenced Sources

- User prompt, 2026-05-08: A4 assignment and clean-room constraints.
- `docs/RULES.md`: Owner start gate, clean-room rules, F-PROTO disposition rules.
- `docs/specs/protocol-translation.md`: F-PROTO-002 HCSF streaming adapter contract, failure paths, acceptance test directions.
- `backend/internal/proto/proto.go`: `UpstreamAdapter` interface.
- `backend/internal/proto/hcsf.go`: `CanonicalEvent`, `CanonicalContentBlock`, `CanonicalContentDelta`, `CanonicalUsage`, stop reasons.
- `backend/internal/proto/anthropic_sse.go`: existing Anthropic SSE to canonical event translator and `UpstreamState`.
- `backend/internal/proto/proto_test.go`: current Anthropic adapter test expectations.
- `backend/internal/proto/tool_call_id.go`: upstream/canonical tool-call ID helper behavior.
- `backend/internal/proto/capability_matrix.go`: `UpstreamProtocolBedrock` and capability feature names.
- `backend/internal/gateway/event_scanner.go`: `gateway.SSEEvent` shape.
- `backend/internal/gateway/forwarder.go`: current adapter call path passes `evt.Data`.
- `backend/internal/gateway/bedrock_stream_scanner.go`: A3 output shape and Bedrock exception behavior.
- `backend/internal/gateway/bedrock_stream_scanner_test.go`: A3 scanner behavior tests.
- `backend/internal/provider/bedrock/eventstream/decoder.go`: A2 decoder contract.
- `backend/internal/gateway/protocol_selector.go` and `stream_scanner.go`: current `bedrock_invoke` registry gap.
- Working tree note: `backend/internal/proto/bedrock_eventstream.go` is currently untracked in this checkout; treat it as parallel/unfinished work until reviewed, not as accepted A4 implementation.

No forbidden SDK/reference source was read.

Lane: codex / Time: 2026-05-08
