# Protocol Translation — Synthesis (Source-Verified)

| Field | Value |
| --- | --- |
| Status | Action Plan (synthesized from source-verified inputs) |
| Feature ID | F-PROTO-001 |
| Lane mode | Option B (multi-protocol gateway is L1 but not on Option C carve-out per [DR-000](../../decisions/DR-000-clean-room-methodology.md)) |
| Author | Claude (PM-Orchestrator) |
| Date | 2026-04-28 |
| Sources | Sub2API ([E-LIC-001](../../07_REFERENCE_EVIDENCE_LEDGER.md), LGPL-3.0, commit `b0a2252ed19c3720e6adafde6083e64fbac2efa9`); Portkey ([E-LIC-006](../../07_REFERENCE_EVIDENCE_LEDGER.md), MIT) |
| Inputs | [protocol-translation-source-verified.md](../sub2api/protocol-translation-source-verified.md) (Claude Sub2API pass, F-PROTO-001 §12 attribution), [portkey/protocol-translation-source-verified.md](../portkey/protocol-translation-source-verified.md) (Codex Portkey cross-verify) |
| Becomes | After CL-001..011 review APPROVE, file moves (cleaned of source identifiers) to `docs/specs/protocol-translation.md` Status=Released. |

## 1. The Architectural Choice — Hub-and-Spoke Wins

Two patterns exist in the wild:

| Pattern | Reference | Topology |
|---------|-----------|----------|
| **Hub-and-spoke** | Sub2API | One canonical intermediate (OpenAI Responses); M client-format adapters + N upstream-format adapters; M+N translators. |
| **Endpoint-as-canonical fan-out** | Portkey | Each endpoint (chat / messages / responses) has its own local canonical type; provider adapters per (provider, endpoint) pair. |

**HUAKAI chooses hub-and-spoke.** Rationale (per Codex Portkey cross-verify recommendation):

- **One loss-auditable semantic model** across OpenAI Chat / OpenAI Responses / Anthropic Messages. Adding a fourth protocol = M+1 client adapter. Adding a fourth upstream = N+1 upstream adapter.
- Portkey's fan-out scales provider breadth but does NOT provide unified semantic model — chat-completions cannot represent Anthropic stream output-item lifecycle losslessly.
- HUAKAI's product identity is **relay-station**, not "broadest provider catalog". Semantic fidelity matters more than provider count.

## 2. Convergence (Both References Agree)

These behaviors appear in both Sub2API and Portkey; HUAKAI inherits them:

1. **Stateful streaming converter** — both use a per-request state machine, not stateless byte transformation. Sub2API has explicit `Created/CompletedSent/Finalized` flags; Portkey has equivalent per-stream state in chunk handlers.
2. **Per-event flush to client** — both flush after every event so customer sees real-time output.
3. **Idempotent terminal emission** — both guard against double-emitting `[DONE]` / `response.completed` on retry.
4. **Provider-specific request transformation** — both have provider-specific request body massage (OAuth token injection, model id mapping, system-prompt conditioning).
5. **Provider-specific response interpretation** — usage extraction, tool-call shape, stop_reason mapping is per-provider.

## 3. Where Sub2API Sharpens Portkey

These Sub2API patterns Portkey lacks:

- **S1 — Canonical intermediate (Responses) for cross-protocol semantic fidelity**. Without a canonical intermediate, lossy mappings between client and upstream protocols compound silently.
- **S2 — `OutputIndex` monotonic increment** preserves interleaving order across reasoning / message / tool_use blocks. Portkey's chat-completions canonical loses this.
- **S3 — Synthetic terminal events** in `FinalizeAnthropicResponsesStream` and `FinalizeResponsesChatStream` cleanly handle upstream EOF without `message_stop`.
- **S4 — Defensive empty-response normalization** when upstream returns zero blocks. Portkey may pass through; Sub2API synthesizes an empty message item.
- **S5 — Tool-call ID bidirectional translation** (`toResponsesCallID` / `fromResponsesCallID` pattern). Critical for tool-result correlation across protocol boundaries.

## 4. Where Portkey Sharpens Sub2API

These Portkey patterns Sub2API lacks:

- **P1 — Provider adapter registry pattern**: `ProviderConfigs[provider][endpoint]` lookup with explicit interface. Adding a new provider is mechanical (implement the interface). Sub2API duplicates per-provider logic across hand-rolled files.
- **P2 — Per-provider awareness of usage shapes** in adapter modules. HUAKAI's hub-and-spoke must avoid losing this — each adapter knows its provider's quirks.

## 5. Where Both Are Insufficient (HUAKAI Design Improvements)

These are HUAKAI-DESIGN, NOT inherited:

- **H1 — Typed warning on unknown event/block/delta types** instead of silent drop. Both references silently drop unknown protocol elements; HUAKAI emits structured telemetry.
- **H2 — Lossless stop-reason mapping**: enumerate all upstream stop_reasons explicitly, no default-completed silent fallback.
- **H3 — `length` finish_reason preserved across Responses→Chat boundary** when upstream `max_tokens`. Sub2API loses this; HUAKAI propagates via state.SawIncomplete.
- **H4 — Streaming-vs-buffered semantic equivalence test**: same upstream events → equivalent final Usage Record values, regardless of streaming mode. Both refs lack formal test.
- **H5 — Conditional `signature_delta` carry-forward** for Anthropic OAuth flows. Sub2API hardcodes skip; HUAKAI makes it Route policy.
- **H6 — Buffered-path interleaving preservation**: Sub2API buffered translator batches all text into one message item, losing interleaving with tool_use. HUAKAI emits separate message items per text block when tool_use intervenes.
- **H7 — Translation latency SLO** (≤ 100µs per event in streaming hot path). Neither reference has explicit budget.
- **H8 — Provider adapter interface borrowed from Portkey** (P1) under HUAKAI hub-and-spoke topology. Best of both: HUAKAI adapter implements (a) request: client-format → canonical, (b) request: canonical → upstream-format, (c) response: upstream-format → canonical, (d) response: canonical → client-format. Adding a new client protocol = implement (a)(d). Adding a new upstream = implement (b)(c).

## 6. The Synthesized HUAKAI Algorithm — Final

### 6.1 Architectural primitives

- **Canonical intermediate**: HUAKAI Canonical Stream Format (HCSF), structurally aligned with OpenAI Responses but in HUAKAI's own type definitions (no upstream type names).
- **Adapter interface** (HUAKAI-DESIGN, Portkey-inspired):
  ```
  type ClientAdapter {
      RequestToCanonical(client_request) → CanonicalRequest
      CanonicalToClientResponse(canonical_response) → client_response
      CanonicalEventToClientChunk(canonical_event, state) → []client_chunk
      FinalizeClientStream(state) → []client_chunk
  }
  type UpstreamAdapter {
      CanonicalToProviderRequest(canonical_request) → provider_request
      ProviderResponseToCanonical(provider_response) → CanonicalResponse
      ProviderEventToCanonicalEvents(provider_event, state) → []CanonicalEvent
      FinalizeUpstreamStream(state) → []CanonicalEvent
  }
  ```
- **State machines** are **per-stream** not per-process.
- **Idempotent finalization** flags: `CreatedSent`, `CompletedSent`, `Finalized` (HUAKAI naming).

### 6.2 Streaming hot-path algorithm

**Phase A — Upstream event parsing**: pluggable per-provider parser produces `UpstreamEvent` records with `envelope_type` enum, `parsed_json`, `observed_at`. Buffer overflow → typed `RESPONSE_EVENT_TOO_LARGE` terminal failure (NOT silent truncation, per F-GW-002 §4 fix).

**Phase B — Upstream → Canonical translation**: `ProviderEventToCanonicalEvents` runs per event. Updates per-stream upstream state. Returns 0..N canonical events.

**Phase C — Canonical event handling**: usage merge into accumulator; tool-call accumulator update; routing reason payload field updates.

**Phase D — Canonical → Client translation**: `CanonicalEventToClientChunk` runs per canonical event. Updates per-stream client state.

**Phase E — Client emission**: write to ResponseWriter; explicit flush; track `firstTokenMs` on first chunk.

**Phase F — Stream end handling**: graceful (terminal marker) → run both finalizes (idempotent). Non-graceful → run synthetic terminal generation in both finalizes; record `end_class` in Usage Record.

### 6.3 Tool-call ID translation

Bidirectional helpers `toCanonicalCallID` / `fromCanonicalCallID` per upstream-format. Anthropic-style `toolu_<hex>` → canonical `call_<hex>` → OpenAI client. Round-trip stability is a tested invariant (P5).

### 6.4 Failure taxonomy

| Reason | Source-verified or HUAKAI-design | Action |
|--------|-----------------------------------|--------|
| `UPSTREAM_PARSE_ERROR` | SUB2API-VERIFIED + HUAKAI-IMPROVED | Skip event in Sub2API; HUAKAI emits typed warning + telemetry |
| `UNKNOWN_EVENT_TYPE` | SUB2API-VERIFIED + HUAKAI-IMPROVED | Silent drop in Sub2API; HUAKAI emits typed warning |
| `UNKNOWN_BLOCK_TYPE` | SUB2API-VERIFIED + HUAKAI-IMPROVED | Silent omit in Sub2API; HUAKAI emits typed warning |
| `UNKNOWN_DELTA_TYPE` | SUB2API-VERIFIED + HUAKAI-IMPROVED | Same |
| `UNKNOWN_STOP_REASON` | HUAKAI-DESIGN | Sub2API silently maps to `completed`; HUAKAI maps to typed `paused` / `tool_required` / etc. or rejects |
| `RESPONSE_EVENT_TOO_LARGE` | HUAKAI-DESIGN | Sub2API has no terminal; HUAKAI emits typed terminal failure |
| `EMPTY_UPSTREAM_RESPONSE` | SUB2API-VERIFIED | Synthesize empty message item |
| `STREAM_ENDED_WITHOUT_TERMINAL` | SUB2API-VERIFIED | Synthetic terminal events from finalize |
| `TOOL_CALL_ID_TRANSLATION_FAIL` | HUAKAI-DESIGN | Sub2API trusts blindly; HUAKAI rejects malformed |
| `STREAMING_VS_BUFFERED_USAGE_MISMATCH` | HUAKAI-DESIGN | Test-only invariant H4 |
| `TRANSLATION_LATENCY_SLO_VIOLATION` | HUAKAI-DESIGN | New telemetry |

## 7. Concurrency / Correctness Invariants

- **P1**: Each translator is pure (or has only state-machine state). No global mutation. Verified in Sub2API (functions are pure), KEEP.
- **P2**: Streaming and buffered paths produce equivalent Usage Records given identical upstream input. HUAKAI test-only.
- **P3**: Unknown event/block/delta types produce typed warning + telemetry counter. HUAKAI improvement over silent drop.
- **P4**: Stop-reason mapping is total. No default-completed.
- **P5**: Tool-call ID translation is bijective; round-trip test verifies.
- **P6**: Translation latency ≤ 100µs per event in streaming hot path. HUAKAI SLO.
- **P7**: `OutputIndex` monotonic across stream lifetime; no duplicate item index. KEEP from Sub2API.
- **P8**: Tenant isolation: every translator state object scoped to tenant_id; no cross-tenant data.

## 8. Sub2API Behavior to KEEP (with HUAKAI vocabulary translation)

| Behavior | Sub2API vocabulary | HUAKAI vocabulary |
|----------|---------------------|-------------------|
| 6 Anthropic SSE event types handled | `message_start`, `content_block_start`, `content_block_delta`, `content_block_stop`, `message_delta`, `message_stop` | Upstream-event handler dispatch on envelope_type enum |
| 7 Responses event types handled (Chat conversion) | `response.created`, `response.output_text.delta`, `response.output_item.added`, etc. | Canonical-event handler dispatch on canonical envelope enum |
| Idempotent terminal flags | `CreatedSent`, `CompletedSent`, `Finalized` | Same names in HUAKAI state struct (these are HUAKAI domain names, not borrowed) |
| Synthetic finalize | `FinalizeAnthropicResponsesStream`, `FinalizeResponsesChatStream` | `FinalizeUpstreamStream`, `FinalizeClientStream` per adapter |

## 9. Provider Adapter Pattern (HUAKAI-DESIGN, Portkey-inspired)

```
package adapter

type ClientAdapter interface { ... }   // §6.1
type UpstreamAdapter interface { ... }  // §6.1

var ClientAdapters = map[ClientProtocol]ClientAdapter{
    ClientProtocolOpenAIChat:      &openaiChatAdapter{},
    ClientProtocolOpenAIResponses: &openaiResponsesAdapter{},
    ClientProtocolAnthropic:       &anthropicMessagesAdapter{},
}

var UpstreamAdapters = map[UpstreamProtocol]UpstreamAdapter{
    UpstreamProtocolAnthropic:  &anthropicUpstreamAdapter{},
    UpstreamProtocolOpenAI:     &openaiUpstreamAdapter{},
    UpstreamProtocolGemini:     &geminiUpstreamAdapter{},
    UpstreamProtocolBedrock:    &bedrockUpstreamAdapter{},
}
```

Adding a fourth client protocol = implement `ClientAdapter` once. Adding a fifth upstream = implement `UpstreamAdapter` once. Hub-and-spoke topology preserved (canonical = `Canonical*` types).

## 10. Test Scenarios (AT-PROTO-001..014)

Sub2API-inheritable:
- AT-PROTO-001 / Anthropic SSE → Canonical → Chat chunks: 50-event stream.
- AT-PROTO-002 / Tool-call interleaving preserves OutputIndex.
- AT-PROTO-003 / Reasoning preserved through canonical.
- AT-PROTO-004 / `signature_delta` skipped by default (Sub2API behavior).
- AT-PROTO-005 / Stream ended without `message_stop`: synthetic terminal.
- AT-PROTO-006 / Empty upstream Content: synthetic empty message.
- AT-PROTO-007 / `max_tokens` stop_reason → buffered Status=`incomplete` + IncompleteDetails.

HUAKAI-design:
- AT-PROTO-008 / Unknown event type → typed warning + telemetry counter (NOT silent drop).
- AT-PROTO-009 / Unknown stop_reason → typed canonical status (NOT default-completed).
- AT-PROTO-010 / Buffered-path interleaving: text → tool_use → text emits 3 items, NOT 1 batched message.
- AT-PROTO-011 / Streaming-vs-buffered usage equivalence (H4).
- AT-PROTO-012 / Tool-call ID round-trip bijection (P5).
- AT-PROTO-013 / `length` finish_reason preserved across Responses→Chat (H3).
- AT-PROTO-014 / Translation latency SLO p99 < 100µs over 1000-event stream.

## 11. Open TODOs

- **TODO-1**: Verify by reading `chatcompletions_to_responses.go` and `responses_to_anthropic_request.go` full bodies whether Sub2API's request-side translators silently drop fields. (Claude pass surveyed function lists; bodies not read.)
- **TODO-2**: Verify exact transformation in `toResponsesCallID` / `fromResponsesCallID` (Sub2API).
- **TODO-3**: Cross-check Bedrock state machine — does it piggyback on Anthropic SSE state or have its own?

These do NOT block synthesis sign-off; they DO block Released spec (per CL-009).

## 12. Provenance

- Sub2API: commit `b0a2252...`, files `apicompat/anthropic_to_responses_response.go` (lines 1-449), `apicompat/responses_to_chatcompletions.go` (lines 130-230 + listings), function-name surveys of 5 other apicompat files. Source-verified by Claude PM 2026-04-28.
- Portkey: commit verified by Codex; behavior covered in `docs/decompositions/portkey/protocol-translation-source-verified.md`.
- This synthesis: Claude PM, after both inputs read.
- Reviewer-lane sign-off: pending Codex final review CL-001..011.

## 13. Review Sign-Off

| Field | Value |
| --- | --- |
| Reviewer | (pending — fresh agent session must run CL-001..011) |
| Review date | (pending) |
| Owner answers received | N/A (no Owner-decision questions in this feature) |
| Checks passed | (pending) |
| Notes | F-PROTO-001 synthesis. Hub-and-spoke from Sub2API + provider adapter registry from Portkey. 8 HUAKAI-design improvements clearly labeled. 3 open TODOs. |
