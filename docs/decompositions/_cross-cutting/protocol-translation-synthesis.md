# Protocol Translation — Synthesis (Source-Verified, Regenerated as F-PROTO-002)

| Field | Value |
| --- | --- |
| Status | Action Plan (regenerated 2026-04-28 after Codex final review REJECT verdict on the F-PROTO-001-mislabeled prior version) |
| Feature ID | **F-PROTO-002** (NOT F-PROTO-001 — the prior label was wrong; F-PROTO-001 in the parity matrix is MCP/A2A external agent/tool protocol bridging, deferred to L3 Phase 9+) |
| Lane mode | Option B (multi-protocol gateway is L1/L2 but not on Option C carve-out per [DR-000](../../decisions/DR-000-clean-room-methodology.md)) |
| Author | Claude (PM-Orchestrator) |
| Date | 2026-04-28 |
| Sources | Sub2API ([E-LIC-001](../../07_REFERENCE_EVIDENCE_LEDGER.md), LGPL-3.0, commit `b0a2252ed19c3720e6adafde6083e64fbac2efa9`); Portkey ([E-LIC-006](../../07_REFERENCE_EVIDENCE_LEDGER.md), MIT, commit pinned in [portkey/protocol-translation-source-verified.md](../portkey/protocol-translation-source-verified.md)); New API ([E-LIC-002](../../07_REFERENCE_EVIDENCE_LEDGER.md), AGPL-3.0, behavioral capability matrix evidence at E-NAI-003 only — no source code consulted for clean-room policy) |
| Inputs | [protocol-translation-source-verified.md](../sub2api/protocol-translation-source-verified.md) (Claude Sub2API pass with 3 verified-resolution items), [portkey/protocol-translation-source-verified.md](../portkey/protocol-translation-source-verified.md) (Codex Portkey cross-verify) |
| Becomes | After CL-001..011 review APPROVE, file moves (cleaned of source identifiers) to `docs/specs/protocol-translation.md` Status=Released. |
| Supersedes | Earlier draft mislabeled as F-PROTO-001 (rejected 2026-04-28). |

## 0. Feature Identity Correction

Per [03_FEATURE_PARITY_MATRIX.md](../../03_FEATURE_PARITY_MATRIX.md):

- **F-PROTO-001** = External agent/tool protocol bridging (MCP, A2A). L3 Phase 9+. Plugin. **NOT this synthesis.**
- **F-PROTO-002** = Single client request shape translated across provider protocols (OpenAI ↔ Claude, Gemini → OpenAI), with **explicit capability matrix exposing what is lost in translation**. Source: New API E-NAI-003. **THIS synthesis covers F-PROTO-002.**

## 1. F-PROTO-002 Required Scope (per parity matrix)

The matrix row mandates:

1. **Protocol translator with explicit per-pair capability matrix**.
2. **Conversion that loses capability emits operator-visible warning** + **records `protocol_loss` field on Usage Record**.

Both requirements are HUAKAI-DESIGN improvements over Sub2API and Portkey, neither of which has a structured capability-loss matrix or per-Usage-Record `protocol_loss` field.

## 2. The Architectural Choice — Hub-and-Spoke (KEEP from Sub2API)

Two patterns observed:

| Pattern | Reference | Topology |
|---------|-----------|----------|
| **Hub-and-spoke** | Sub2API | Canonical intermediate (HUAKAI domain: HCSF — HUAKAI Canonical Stream Format); M client adapters + N upstream adapters; M+N. |
| **Endpoint-as-canonical fan-out** | Portkey | Each client endpoint has its own local canonical; provider adapters per (provider, endpoint) pair. |

**HUAKAI chooses hub-and-spoke** (per Codex Portkey cross-verify recommendation):
- One loss-auditable semantic model across OpenAI Chat / OpenAI Responses / Anthropic Messages.
- Hub topology directly enables F-PROTO-002 capability matrix: every translation pair is `client_protocol → HCSF → upstream_protocol`, so a single capability-matrix per (HCSF feature × adapter) covers all pairs.

## 3. Convergence (Both References Agree)

1. **Stateful streaming converter** — both use per-request state machine.
2. **Per-event flush to client** — real-time visibility.
3. **Idempotent terminal emission** — guard against double-emit on retry.
4. **Provider-specific request transformation** — OAuth token injection, model id mapping, system-prompt conditioning.
5. **Provider-specific response interpretation** — usage shape, tool-call shape, stop_reason mapping per-provider.

## 4. F-PROTO-002 Capability Matrix (HUAKAI-DESIGN, the matrix-row mandate)

Per-pair capability matrix exposes what each (client_protocol, upstream_protocol) pair preserves vs loses. The matrix is operator-visible in admin UI; conversion losses produce structured Usage Record `protocol_loss` field.

### 4.0 Capability verdict semantics (decision criteria)

For each (client_protocol × upstream_protocol × feature) cell, the verdict is decided as:

- **PRESERVED** = round-trip semantic equivalence verified: encoding the feature in client format → translating to canonical → translating to upstream → upstream-side semantics match → upstream response → translating back to canonical → translating to client format → result preserves the feature exactly. Verified by acceptance test AT-PROTO-002-15.
- **LOSSY** = at least one direction loses some sub-feature (e.g. `length` finish_reason loses incomplete-details mapping at Responses→Chat boundary). Translation succeeds but result is approximate. Operator warning emitted on Usage Record `protocol_loss`.
- **UNSUPPORTED** = the feature has no defined translation path; request rejected at Phase A or response rejected at Phase D. Client receives 400 + structured error explaining unsupported feature.

Operator UI shows the matrix; tooling generates "what does my product support" docs by selecting cells where verdict is PRESERVED for the configured client × upstream combinations.

### 4.1 Matrix structure

```
ProtocolCapabilityMatrix[client_protocol][upstream_protocol] = {
   text_streaming:           PRESERVED | LOSSY | UNSUPPORTED
   tool_use:                 PRESERVED | LOSSY | UNSUPPORTED
   reasoning_summary:        PRESERVED | LOSSY | UNSUPPORTED  
   parallel_tool_calls:      PRESERVED | LOSSY | UNSUPPORTED
   structured_output_schema: PRESERVED | LOSSY | UNSUPPORTED
   image_input:              PRESERVED | LOSSY | UNSUPPORTED
   audio_input:              PRESERVED | LOSSY | UNSUPPORTED
   image_output:             PRESERVED | LOSSY | UNSUPPORTED
   max_tokens_finish_reason: PRESERVED | LOSSY | UNSUPPORTED
   max_completion_tokens:    PRESERVED | LOSSY | UNSUPPORTED
   stop_sequence_emit:       PRESERVED | LOSSY | UNSUPPORTED
   cache_breakpoints:        PRESERVED | LOSSY | UNSUPPORTED
   signature_delta:          PRESERVED | LOSSY | UNSUPPORTED
}
```

Operator UI surfaces this matrix; tooling generates "what does my product support" docs from it automatically.

### 4.2 `protocol_loss` field on Usage Record (HUAKAI-DESIGN)

Every Usage Record carries:
```
protocol_loss: [
   { feature: "max_tokens_finish_reason", direction: "responses_to_chat", verdict: LOSSY, note: "Sub2API drops length finish_reason at Responses→Chat boundary" },
   { feature: "signature_delta", direction: "anthropic_upstream", verdict: LOSSY, note: "skipped per default policy" },
   ...
]
```

Empty array when translation is fully PRESERVED. Operator can query Usage Records with non-empty `protocol_loss` to identify systematic capability loss patterns.

### 4.3 Operator-visible warning emission

When a request triggers LOSSY conversion:
- Structured warning entry in Usage Record `protocol_loss`.
- Optional response header `X-HUAKAI-Protocol-Loss: <feature_list>` for client developers (operator-tunable per Route).
- Counter increment on operator dashboard for the (feature, direction) tuple.

## 5. Where Sub2API Sharpens Portkey (KEEP from Sub2API)

Sub2API patterns Portkey lacks:

- **S1 — Canonical intermediate** (HCSF in HUAKAI vocabulary; OpenAI Responses in Sub2API) for cross-protocol semantic fidelity.
- **S2 — `OutputIndex` monotonic increment** preserves interleaving order across reasoning / message / tool_use blocks.
- **S3 — Synthetic terminal events** clean up upstream EOF without `message_stop` (idempotent).
- **S4 — Defensive empty-response normalization** when upstream returns zero blocks.
- **S5 — Tool-call ID bidirectional translation** — see §6 for VERIFIED format.

## 6. Tool-Call ID Translation (VERIFIED CORRECTION)

**Earlier prose said `toolu_x → call_x`; that was incorrect.** Verified format from Sub2API source:

| Direction | Sub2API actual | HUAKAI canonical |
|-----------|----------------|-------------------|
| Anthropic upstream `tool_use_id` | `toolu_<hex>` | `toolu_<hex>` |
| Responses canonical `call_id` | `fc_toolu_<hex>` (Sub2API uses `fc_` prefix on Anthropic IDs) | `call_<hex>` (HUAKAI redesign — no upstream-prefix leakage in HUAKAI canonical) |
| OpenAI client `tool_call_id` | `call_<hex>` | `call_<hex>` |

HUAKAI's translation function strips upstream-format prefixes when entering canonical, applies upstream-format prefixes when exiting canonical. Round-trip stability is a tested invariant (P5).

## 7. Where Portkey Sharpens Sub2API

- **P1 — Provider adapter registry pattern**: explicit interface per (provider, endpoint). Adding new provider = implement interface once. Sub2API duplicates per-provider logic across hand-rolled files.

## 8. HUAKAI Design Improvements (NOT in either reference)

- **H1 — F-PROTO-002 capability matrix** (the mandate, see §4).
- **H2 — `protocol_loss` field on Usage Record** (the mandate, see §4.2).
- **H3 — Typed warning on unknown event/block/delta types** (replaces Sub2API silent drop).
- **H4 — Lossless stop-reason mapping**: enumerate all upstream stop_reasons explicitly, no default-completed silent fallback.
- **H5 — `length` finish_reason preserved end-to-end**: at the Anthropic upstream boundary, `stop_reason: max_tokens` translates into canonical incomplete-details signal; at the Anthropic→Canonical boundary, that signal is preserved; at the Canonical→Chat boundary, finish_reason='length' is emitted (not 'stop'). Sub2API loses this at the Anthropic→Responses-canonical boundary because incomplete-details is dropped from streaming events. HUAKAI canonical event type carries an explicit `incomplete_reason` field that the Chat adapter consumes.
- **H6 — Streaming-vs-buffered semantic equivalence test** (formal property test).
- **H7 — Conditional `signature_delta` carry-forward** for Anthropic OAuth flows when needed (Sub2API hardcodes skip; HUAKAI Route policy).
- **H8 — Buffered-path interleaving preservation** (Sub2API batches text, loses interleaving with tool_use).
- **H9 — Translation latency SLO** with explicit measurement scope: payload size class + event size class + adapter class + parse-time included. Default: p99 < 200µs per event with payload < 4 KiB excluding JSON parse.
- **H10 — Provider adapter registry** under hub-and-spoke topology (Portkey-inspired but NOT abandoning canonical hub).

## 9. The Synthesized HUAKAI Algorithm — Final

### 9.1 Architectural primitives

- **Canonical intermediate**: HCSF (HUAKAI Canonical Stream Format), structurally aligned with OpenAI Responses semantics but in HUAKAI's own type definitions.
- **Adapter interface** (HUAKAI-DESIGN, Portkey-inspired):
  ```
  type ClientAdapter interface {
      RequestToCanonical(client_request, ctx) (CanonicalRequest, []protocol_loss_entry)
      CanonicalToClientResponse(canonical_response) (client_response, []protocol_loss_entry)
      CanonicalEventToClientChunk(canonical_event, state) ([]client_chunk, []protocol_loss_entry)
      FinalizeClientStream(state) []client_chunk
  }
  type UpstreamAdapter interface {
      CanonicalToProviderRequest(canonical_request) (provider_request, []protocol_loss_entry)
      ProviderResponseToCanonical(provider_response) (CanonicalResponse, []protocol_loss_entry)
      ProviderEventToCanonicalEvents(provider_event, state) ([]CanonicalEvent, []protocol_loss_entry)
      FinalizeUpstreamStream(state) []CanonicalEvent
  }
  ```
- Each adapter method returns `[]protocol_loss_entry` so the gateway accumulates losses across both client and upstream sides for the Usage Record.

### 9.2 Streaming hot-path algorithm

**Phase A — Upstream event parsing**: pluggable per-provider parser produces UpstreamEvent records. Buffer overflow → typed `RESPONSE_EVENT_TOO_LARGE` terminal failure.

**Phase B — Upstream → Canonical translation**: `ProviderEventToCanonicalEvents` runs per event; updates upstream state; returns 0..N canonical events + protocol_loss entries.

**Phase C — Canonical event handling**: usage merge into accumulator; tool-call accumulator update; routing reason payload field updates; protocol_loss accumulator update.

**Phase D — Canonical → Client translation**: `CanonicalEventToClientChunk` runs per canonical event; updates client state; returns client chunks + protocol_loss entries.

**Phase E — Client emission**: write to ResponseWriter; explicit flush; track `firstTokenMs` on first chunk.

**Phase F — Stream end handling**: graceful (terminal marker) → run both finalizes (idempotent). Non-graceful → run synthetic terminal generation. Record `end_class` + accumulated `protocol_loss` in Usage Record.

### 9.3 Failure taxonomy

(per F-GW-002 §5.4 streaming taxonomy)

Plus protocol-specific:

| Reason | Source-verified or HUAKAI-design | Action |
|--------|-----------------------------------|--------|
| `UNKNOWN_EVENT_TYPE` | SUB2API-VERIFIED (silent drop) + HUAKAI-IMPROVED | Typed warning + telemetry counter; protocol_loss entry |
| `UNKNOWN_BLOCK_TYPE` | Same | Same |
| `UNKNOWN_DELTA_TYPE` | Same | Same |
| `UNKNOWN_STOP_REASON` | HUAKAI-DESIGN | Map to typed `paused` / `tool_required` etc.; reject default-completed; protocol_loss entry |
| `RESPONSE_EVENT_TOO_LARGE` | HUAKAI-DESIGN | Typed terminal failure (NOT silent truncation) |
| `EMPTY_UPSTREAM_RESPONSE` | SUB2API-VERIFIED | Synthesize empty message item |
| `STREAM_ENDED_WITHOUT_TERMINAL` | SUB2API-VERIFIED | Synthetic terminal events |
| `TOOL_CALL_ID_TRANSLATION_FAIL` | HUAKAI-DESIGN | Reject malformed; protocol_loss entry |
| `STREAMING_VS_BUFFERED_USAGE_MISMATCH` | HUAKAI-DESIGN | Test-only invariant |
| `TRANSLATION_LATENCY_SLO_VIOLATION` | HUAKAI-DESIGN | Telemetry only |

## 10. Concurrency / Correctness Invariants

- **P1**: Each translator pure or per-stream-state only. KEEP from Sub2API.
- **P2**: Streaming and buffered paths produce equivalent Usage Records given identical upstream input. HUAKAI test-only.
- **P3**: Unknown event/block/delta types produce typed warning + protocol_loss entry + telemetry counter.
- **P4**: Stop-reason mapping total. No default-completed.
- **P5**: Tool-call ID translation bijective with verified format (§6).
- **P6**: Translation latency p99 ≤ 200µs per event (with explicit measurement scope).
- **P7**: `OutputIndex` monotonic across stream lifetime. KEEP from Sub2API.
- **P8**: Tenant isolation: every translator state object scoped to tenant_id.
- **P9** (HUAKAI mandate): every conversion that loses capability emits `protocol_loss` entry on Usage Record.

## 11. Test Scenarios (AT-PROTO-002-01..15)

(Re-numbered from original `AT-PROTO-001..014` to avoid colliding with F-PROTO-001 MCP/A2A test ID.)

Sub2API-inheritable:
- AT-PROTO-002-01 / Anthropic SSE → Canonical → Chat chunks: 50-event stream graceful path.
- AT-PROTO-002-02 / Tool-call interleaving preserves OutputIndex; round-trip ID bijection (§6).
- AT-PROTO-002-03 / Reasoning preserved through canonical.
- AT-PROTO-002-04 / `signature_delta` carry-forward governed by Route policy (default skip).
- AT-PROTO-002-05 / Stream ended without `message_stop`: synthetic terminal.
- AT-PROTO-002-06 / Empty upstream Content: synthetic empty message.
- AT-PROTO-002-07 / `max_tokens` stop_reason → buffered Status=incomplete + IncompleteDetails.

HUAKAI-design (F-PROTO-002 mandate):
- AT-PROTO-002-08 / Unknown event type → typed warning + protocol_loss entry (NOT silent drop).
- AT-PROTO-002-09 / Unknown stop_reason → typed canonical status (NOT default-completed).
- AT-PROTO-002-10 / Buffered-path interleaving: text → tool_use → text emits 3 items, NOT 1 batched message.
- AT-PROTO-002-11 / Streaming-vs-buffered usage equivalence (P2 test).
- AT-PROTO-002-12 / Tool-call ID round-trip bijection (P5 test).
- AT-PROTO-002-13 / `length` finish_reason preserved across Responses→Chat (H5).
- AT-PROTO-002-14 / Translation latency SLO p99 < 200µs over 1000-event stream.
- **AT-PROTO-002-15 / Capability matrix matches reality**: every cell asserted via property test that runs each (client × upstream) pair through a multi-feature canonical request and verifies the matrix entry matches the actual translation outcome.
- **AT-PROTO-002-16 / `protocol_loss` field populated**: when conversion is LOSSY for any feature, Usage Record `protocol_loss` array contains entry with feature name + direction + verdict.

## 12. Verified Source Resolutions

(Previously TODO-1..3. All closed; per CL-009 a Released spec carries no open questions.)

- **Request-side translator field handling**: VERIFIED. Sub2API request-side translators (`chatcompletions_to_responses.go`, `responses_to_anthropic_request.go`) silently drop unsupported fields. HUAKAI MUST emit protocol_loss entries instead (P9, mandate).
- **Tool-call ID format**: VERIFIED in §6 above. Sub2API uses `toolu_<hex>` upstream → `fc_toolu_<hex>` canonical → `call_<hex>` client. HUAKAI canonical strips prefix leakage.
- **Bedrock state machine**: VERIFIED. Bedrock has its own SSE handling but does NOT piggyback on Anthropic-conversion state machine. HUAKAI adapter pattern accommodates.

## 13. Provenance

- Sub2API: commit `b0a2252...`, files `pkg/apicompat/anthropic_to_responses_response.go`, `pkg/apicompat/responses_to_chatcompletions.go`, `pkg/apicompat/anthropic_to_responses.go`, `pkg/apicompat/chatcompletions_to_responses.go`, `pkg/apicompat/responses_to_anthropic_request.go`. Source-verified by Claude PM.
- Portkey: behavioral cross-verify by Codex 2026-04-28. MIT license. Comparison shows DIFFERENT-PATTERN (endpoint-as-canonical fan-out vs Sub2API hub-and-spoke).
- This synthesis: Claude PM, regenerated 2026-04-28 after F-PROTO-001-mislabel REJECT verdict.
- Reviewer-lane sign-off: pending Codex final review CL-001..011.

## 14. Review Sign-Off

| Field | Value |
| --- | --- |
| Reviewer | (pending — fresh agent session must run CL-001..011) |
| Review date | (pending) |
| Owner answers received | N/A (no Owner-decision questions in this feature) |
| Checks passed | (pending) |
| Notes | F-PROTO-002 synthesis (NOT F-PROTO-001 — corrected). Hub-and-spoke from Sub2API + adapter registry from Portkey + capability matrix and `protocol_loss` field per F-PROTO-002 mandate. 10 HUAKAI-design improvements clearly labeled. AT-PROTO-002-NN test IDs (no collision with F-PROTO-001 MCP/A2A). 3 prior TODOs all closed. |
