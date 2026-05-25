# F-PROTO-002: Protocol Translation Across Provider Pairs

| Field | Value |
| --- | --- |
| Status | Released |
| Feature ID | F-PROTO-002 (NOT F-PROTO-001 — that ID is MCP/A2A external agent/tool protocol bridging, deferred to L3 Phase 9+) |
| Specifier | Claude (PM-Orchestrator) + Codex (Portkey cross-verify), 2026-04-28 |
| Specifier date | 2026-04-28 |
| Reviewer | Codex final reviewer-lane, 2026-04-28 (APPROVE-WITH-FIXES; 10 fixes applied this revision) |
| Review date | 2026-04-28 |
| Released date | 2026-04-28 |
| Lane mode | Option B |
| Supersedes | — |
| Superseded by | — |

## Sources

> Reference material consulted by specifier-lane only. Implementer lane MUST NOT open these.

- Sub2API — LGPL-3.0 ([E-LIC-001](../07_REFERENCE_EVIDENCE_LEDGER.md), commit `b0a2252ed19c3720e6adafde6083e64fbac2efa9`)
- Portkey AI Gateway — MIT ([E-LIC-006](../07_REFERENCE_EVIDENCE_LEDGER.md), commit pinned in input file)
- New API — AGPL-3.0 ([E-LIC-002](../07_REFERENCE_EVIDENCE_LEDGER.md), behavioral evidence E-NAI-003 only — no source consulted for clean-room policy)
- Specifier backing artifacts: [protocol-translation-synthesis.md](../decompositions/_cross-cutting/protocol-translation-synthesis.md), [protocol-translation-source-verified.md](../decompositions/sub2api/protocol-translation-source-verified.md), [portkey/protocol-translation-source-verified.md](../decompositions/portkey/protocol-translation-source-verified.md)

## Capability

This spec satisfies F-PROTO-002 from [03_FEATURE_PARITY_MATRIX.md](../03_FEATURE_PARITY_MATRIX.md): single client request shape translated across provider protocols (OpenAI ↔ Claude, Gemini → OpenAI), with **explicit per-pair capability matrix** exposing what is lost in translation, **operator-visible warning** on lossy conversion, and **`protocol_loss` field on Usage Record**.

## Actor

- **System** (Gateway): runs adapters per request.
- **External Provider**: source of upstream protocol responses.
- **Operator**: views capability matrix UI; tunes per-Route protocol policy.
- **Client developer**: optionally consumes `X-HUAKAI-Protocol-Loss` response header.

## Preconditions

1. Tenant context established; client request received.
2. Client protocol identified from request envelope (Chat Completions / Responses / Anthropic Messages).
3. Upstream protocol selected via F-POOL-001 Provider Account selection.
4. Per-Route protocol policy: capability_default (`exact_capability_only` or `safe_equivalent_allowed`), `signature_delta_carry_forward`, `unknown_event_warn_threshold`.

## Normal Path

Translation runs through a hub-and-spoke topology with HUAKAI Canonical Stream Format (HCSF) as the intermediate.

### Phase A — Client Request → Canonical

1. Resolve client adapter for the incoming protocol.
2. Translate client request to canonical request: parse + validate + normalize.
3. Each lossy translation produces a `protocol_loss_entry` (feature name, direction, verdict, note).
4. UNSUPPORTED features trigger 400 rejection with structured error.

### Phase B — Canonical → Upstream Request

5. Resolve upstream adapter for the selected Provider Account's upstream protocol.
6. Translate canonical request → upstream-format request.
7. Adapter handles upstream-specific bits: OAuth token injection, model id mapping, system-prompt conditioning.
8. Each lossy translation produces a `protocol_loss_entry`.

### Phase C — Upstream Response → Canonical (Streaming)

For streaming responses, the adapter runs a state machine over upstream events:

9. Initialize per-stream state (UpstreamState).
10. Per upstream event: translate to 0..N canonical events; update state; idempotent guards on terminal events.
11. Per provider quirk:
    - Anthropic SSE: 6 event types (`message_start`, `content_block_start/delta/stop`, `message_delta`, `message_stop`).
    - OpenAI Responses: ~7 event types.
    - Bedrock: separate state machine (does NOT piggyback on Anthropic).
12. Synthetic terminal events on stream EOF without explicit terminator.
13. `signature_delta` events: by default skipped; carry-forward when Route policy `signature_delta_carry_forward = true` (typically only for Anthropic OAuth roundtrips).

### Phase D — Canonical → Client Response (Streaming)

14. Resolve client adapter; translate each canonical event to 0..N client chunks.
15. Per-event `c.Writer.Flush()` for real-time visibility.
16. Track first-token latency on first emitted chunk.
17. Idempotent finalization on stream end (no double-emit).

### Phase E — Capability Loss Reporting

18. Accumulated `protocol_loss` array attached to Usage Record (F-OBS-001 Tx2).
19. Operator-tunable response header `X-HUAKAI-Protocol-Loss: <feature_list>` for client developers.
20. Counter increments on operator dashboard per (feature, direction) tuple.

## Capability Matrix

The matrix is operator-visible; verdict per cell decided by these rules:

- **PRESERVED** = round-trip semantic equivalence verified by acceptance test (AT-PROTO-002-15). Client encoding → canonical → upstream → response → canonical → client preserves the feature.
- **LOSSY** = at least one direction loses sub-feature; translation succeeds with approximation; operator warning on `protocol_loss`.
- **UNSUPPORTED** = no defined translation path; request rejected at Phase A; client gets 400 with structured error.

Initial matrix cells (15 features × N pairs):

```
ProtocolCapabilityMatrix[client_protocol][upstream_protocol] = {
   text_streaming, tool_use, reasoning_summary, parallel_tool_calls,
   structured_output_schema, image_input, audio_input, image_output,
   max_tokens_finish_reason, max_completion_tokens, stop_sequence_emit,
   cache_breakpoints, signature_delta, system_prompt_array, multi_role_messages
}
```

Each cell: PRESERVED | LOSSY | UNSUPPORTED.

## `protocol_loss` Field on Usage Record

Schema (lives on `usage_records.protocol_loss` jsonb column per F-OBS-001 schema):

```
protocol_loss: [
  {
    feature: "<feature name from matrix>",
    direction: "<client_to_canonical | canonical_to_upstream | upstream_to_canonical | canonical_to_client>",
    verdict: "LOSSY",
    note: "<HUAKAI-domain explanation; no upstream identifier names>"
  },
  ...
]
```

Empty array when fully PRESERVED. Operator queries `WHERE protocol_loss != '[]'::jsonb` to find systematic loss patterns.

## Tool-Call ID Translation

Bidirectional helpers per upstream protocol; HUAKAI canonical strips upstream-format prefix leakage:

| Boundary | Format |
|---|---|
| Anthropic upstream `tool_use_id` | `toolu_<hex>` |
| HUAKAI canonical `call_id` | `call_<hex>` (no upstream-format prefix) |
| OpenAI client `tool_call_id` | `call_<hex>` |

Round-trip stability is a tested invariant (AT-PROTO-002-12).

## Failure Path

| Reason | Trigger | Recovery |
|--------|---------|----------|
| `UNSUPPORTED_REQUEST_FEATURE` | Phase A: client request uses feature with UNSUPPORTED verdict for selected upstream | 400 + structured error explaining missing feature |
| `UNKNOWN_EVENT_TYPE` | Phase C: upstream emits event type not in adapter's switch | typed warning + telemetry counter + protocol_loss entry; event silently skipped |
| `UNKNOWN_BLOCK_TYPE` / `UNKNOWN_DELTA_TYPE` | similar | same |
| `UNKNOWN_STOP_REASON` | upstream stop_reason not enumerated | typed canonical status (NOT default-completed); protocol_loss entry |
| `RESPONSE_EVENT_TOO_LARGE` | Phase A scanner buffer overflow | typed terminal failure (per F-GW-002) |
| `EMPTY_UPSTREAM_RESPONSE` | upstream returns zero blocks | synthesize empty message item (Sub2API defensive pattern) |
| `STREAM_ENDED_WITHOUT_TERMINAL` | upstream EOF without proper terminator | synthetic terminal events (idempotent finalize) |
| `TOOL_CALL_ID_TRANSLATION_FAIL` | malformed upstream tool_use_id | reject with structured error; protocol_loss entry |
| `STREAMING_VS_BUFFERED_USAGE_MISMATCH` | property test only | test failure (NOT runtime failure) |
| `TRANSLATION_LATENCY_SLO_VIOLATION` | p99 > 200µs/event | telemetry counter; alert if sustained |

## Operator Recovery

| Failure | Detection | Recovery |
|---|---|---|
| Many `UNKNOWN_EVENT_TYPE` for one provider | counter spike | Investigate upstream protocol drift; update adapter to handle new event type. |
| Capability matrix UNSUPPORTED for desired feature | operator UI | Either implement adapter pair or accept rejection. |
| `TOOL_CALL_ID_TRANSLATION_FAIL` | counter | Investigate upstream protocol violation; add tolerant parser. |
| Latency SLO violation | metric | Profile adapter hot path; consider caching translation tables. |

## Audit / Usage / Log Evidence

Every Usage Record carries:
- `protocol_loss` jsonb array (empty when fully preserved).
- `client_protocol` enum.
- `upstream_protocol` enum.
- `capability_outcome` enum: `exact` / `safe_equivalent` / `none_required`.

Operator dashboards:
- Per-(client_protocol, upstream_protocol) pair distribution by verdict (PRESERVED / LOSSY / UNSUPPORTED ratio).
- Top 10 LOSSY (feature, direction) tuples by request count.
- `UNKNOWN_EVENT_TYPE` rate trend per upstream protocol.

## Acceptance Test Direction

Per [11_ACCEPTANCE_TEST_MATRIX.md](../11_ACCEPTANCE_TEST_MATRIX.md), tests AT-PROTO-002-01..16 (renumbered to avoid collision with F-PROTO-001 MCP/A2A test ID).

Sub2API-inheritable:
- AT-PROTO-002-01 / Anthropic SSE → Canonical → Chat chunks: 50-event stream graceful path.
- AT-PROTO-002-02 / Tool-call interleaving preserves output index; round-trip ID bijection.
- AT-PROTO-002-03 / Reasoning preserved through canonical.
- AT-PROTO-002-04 / `signature_delta` carry-forward governed by Route policy (default skip).
- AT-PROTO-002-05 / Stream ended without explicit terminator: synthetic terminal events.
- AT-PROTO-002-06 / Empty upstream Content: synthesize empty message item.
- AT-PROTO-002-07 / `max_tokens` stop_reason → buffered status incomplete + IncompleteDetails.

HUAKAI-design (F-PROTO-002 mandate):
- AT-PROTO-002-08 / Unknown event type → typed warning + protocol_loss entry (NOT silent drop).
- AT-PROTO-002-09 / Unknown stop_reason → typed canonical status (NOT default-completed).
- AT-PROTO-002-10 / Buffered-path interleaving: text → tool_use → text emits 3 items, NOT 1 batched message.
- AT-PROTO-002-11 / Streaming-vs-buffered usage equivalence (property test).
- AT-PROTO-002-12 / Tool-call ID round-trip bijection.
- AT-PROTO-002-13 / `length` finish_reason preserved end-to-end (incomplete-details propagates through canonical to Chat client).
- AT-PROTO-002-14 / Translation latency SLO p99 < 200µs over 1000-event stream (with payload < 4 KiB excluding JSON parse).
- AT-PROTO-002-15 / Capability matrix matches reality: every cell asserted via property test running each (client × upstream) pair through multi-feature canonical request.
- AT-PROTO-002-16 / `protocol_loss` field populated when conversion is LOSSY.

## Open Questions

None remaining at release. All prior open questions resolved during Codex final review 2026-04-28.

## Implementer Notes (added by implementer lane)

> Filled by implementer after consuming the spec.

(empty until implementer-lane work begins)
