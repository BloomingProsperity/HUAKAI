# Sub2API — Streaming Forwarder Hot Path

| Field | Value |
| --- | --- |
| Status | Draft |
| Reference | Sub2API (LGPL-3.0, [E-LIC-001](../../07_REFERENCE_EVIDENCE_LEDGER.md)) |
| Feature in HUAKAI matrix | [F-GW-002](../../03_FEATURE_PARITY_MATRIX.md) (L1 MVP) — streaming + accounting |
| Evidence ledger rows | [E-S2A-PROXY-021, E-S2A-PROXY-022, E-S2A-PROXY-023, E-S2A-PROXY-024](../../07_REFERENCE_EVIDENCE_LEDGER.md) |
| Specifier session | Codex (`omc ask codex --agent-prompt critic`, 2026-04-28T05:31 UTC) |
| Specifier date | 2026-04-28 |
| Reviewer session | TBD |
| Reviewer date | TBD |
| Source files read | Sub2API backend gateway streaming forwarder + usage extractor (paths verified by Codex; recorded in `.omc/artifacts/ask/codex-role-codex-specifier-lane-miner-for-huakai-owner-directive-2-2026-04-28T05-31-56-975Z.md`, gitignored) |

## 1. WHY

In a relay-station product, streaming is the customer experience: token-by-token output is what users feel. The forwarder is also where the gateway either correctly bills or silently leaks money. A naive design that just pipes upstream bytes to the client breaks on three fronts: (a) the client cannot be billed for tokens that are never observed, (b) the client may disconnect mid-stream and the upstream keeps emitting tokens that the operator pays for, (c) different upstream protocols (OpenAI SSE, Anthropic SSE, Gemini chunked-JSON) carry usage data in different envelopes. Sub2API's design pressure is "preserve billing across every weather pattern in streaming" — exactly HUAKAI's pressure given the relay-station identity ([01 §Product Identity](../../01_PROJECT_BRIEF.md), [DR-002](../../decisions/DR-002-product-editions.md)).

## 2. WHAT

The forwarder is a **protocol-aware** SSE line / event processor, not a raw chunk pipe. The flow:

1. Upstream response opens. The forwarder identifies the response style (event-stream, chunked-JSON, raw chunk) and selects the matching event parser.
2. The parser reads upstream byte-by-byte (or line-by-line for SSE) and reconstructs **events** (a complete event envelope, e.g. one `data: {...}` line group in SSE).
3. For each event, the forwarder runs three responsibilities **inline** (not buffered to end-of-stream):
   - **Rewrite safe fields**: anything that needs translation across formats (e.g. role names, tool-call shapes) is rewritten before re-emission.
   - **Extract usage**: when an event carries usage data (mid-stream telemetry, mid-stream cache hits, terminal usage frames), pull values into a streaming usage accumulator. Different sources (reported vs normalized vs inferred vs partial) are tracked separately.
   - **Re-emit**: the rewritten event is written to the downstream `ResponseWriter` and explicitly flushed so the client sees output in real time.
4. **Slow-client backpressure**: when downstream write blocks, the forwarder relies on bounded upstream read queues to apply backpressure. If the downstream write ultimately fails (client disconnect mid-stream), some paths in the upstream code continue to drain upstream until usage data is collected before terminating — this is the controversial "billing-preserving drain" behavior.
5. **Terminal detection**: the forwarder watches for the protocol's terminal marker (e.g. `data: [DONE]`). If the upstream stream ends without the marker, it is treated as `usage-incomplete` or `stream-error`. Content reconstructed from accumulated deltas may be returned to the client, but token usage is **not universally inferred** — it can remain ambiguous.
6. On stream end, the forwarder writes a final Usage Record carrying the accumulated values and a source label.

## 3. INPUTS (signals consumed, state mutated)

- Per-request: client `Accept` header (decides streaming vs non-streaming envelope), client API Key (resolved earlier), request id, target Provider protocol (decides parser).
- Per-Account: outbound HTTP client (selected via transport pool), per-Account read-queue size, per-Account first-token / inter-event timeout overrides.
- Per-event: event type (data / event / id / retry per SSE spec), payload bytes, parsed JSON envelope.
- Per-stream: accumulated content text, accumulated tool-call records, accumulated usage values, terminal-marker observed flag, downstream-write-error flag, drain-budget consumed.
- Time: monotonic clock for inter-event timeout and first-token timeout.
- State mutated: streaming usage accumulator, downstream `ResponseWriter` buffer, per-Account active-stream count (incremented at start, decremented at end so transport pool doesn't evict the client mid-stream).

## 4. FAILURE MODES HANDLED

- **Upstream silence after partial output**: inter-event timeout fires, the forwarder closes upstream and reports `stream-error`. Already-emitted content stays on the client.
- **Slow client**: bounded upstream read queue applies backpressure; if the queue saturates and the downstream still cannot drain, the forwarder eventually gives up the client (still drains upstream to collect usage in some paths).
- **Upstream returns non-streaming response despite streaming preference**: the forwarder buffers the terminal output and converts to a single-event SSE-style emission so the client contract holds.
- **Missing terminal marker**: usage labeled as incomplete; content delivered if reconstructable.
- **Tool-call event in mid-stream**: extracted into the running tool-call accumulator and re-emitted in the rewritten format.
- **Mid-stream rate-limit or auth-failure event from upstream**: terminal stream error emitted; the request enters the typed-failure taxonomy ([E-S2A-PROXY-026](../../07_REFERENCE_EVIDENCE_LEDGER.md)) for retry classification.

## 5. FAILURE MODES NOT HANDLED (gaps)

- **Universal usage inference**: when upstream finishes without usage data, the forwarder may return reconstructed content but does NOT have a tokenizer-based fallback to estimate the cost. Billing carries an `incomplete` flag but no estimate. ([E-S2A-PROXY-024](../../07_REFERENCE_EVIDENCE_LEDGER.md))
- **Post-disconnect drain budget**: the drain-after-disconnect behavior preserves billing but has no time / byte / estimated-cost cap. A pathological upstream can keep emitting forever after the client is gone. ([E-S2A-PROXY-022](../../07_REFERENCE_EVIDENCE_LEDGER.md))
- **Mid-stream failover after emitted output**: the forwarder does not implement a contract for rerouting to a different Account once the client has already received tokens. The only options observed are "complete on this Account" or "terminal error". Client-side duplicate-output recovery is not negotiated.
- **Stream-event size limits**: extremely large events may exceed scanner buffers without an explicit typed terminal condition. ([E-S2A-PROXY-021](../../07_REFERENCE_EVIDENCE_LEDGER.md) risk note.)
- **Multi-source usage reconciliation**: when the same request gets usage data from multiple sources (mid-stream telemetry, terminal frame, accumulated deltas), there is no explicit reconciliation rule for which one wins; the assumption is upstream is internally consistent.

## 6. KEEP / IMPROVE / AVOID for HUAKAI

- **KEEP**: protocol-aware SSE/chunked-JSON parsing, NOT raw byte pipe. Inline usage extraction during stream, not buffered.
- **KEEP**: explicit flush after each event so the client sees real-time output.
- **KEEP**: separate streaming usage accumulator with multiple sources tracked; usage carrying through to the final Usage Record.
- **KEEP**: bounded upstream read queue for slow-client backpressure.
- **IMPROVE**: split timeout policy from one global "stream timeout" into eight separate fields ([E-S2A-PROXY-019 IMPROVE](../../07_REFERENCE_EVIDENCE_LEDGER.md)): connect / TLS / request-write / response-header / first-token / inter-event / total-stream / downstream-write. Each is a Route policy field with sane defaults.
- **IMPROVE**: post-disconnect drain has explicit budgets — `drain_max_bytes`, `drain_max_seconds`, `drain_max_estimated_cost`. After any budget exhausts, drain stops and partial usage is settled with `disconnect_reason=client_disconnect` recorded.
- **IMPROVE**: Usage Record carries a `usage_source` enum with values `{reported, normalized, inferred, partial}`. When source is `inferred`, also include a `confidence_score`. When source is `partial`, the Usage Record can be reconciled later if upstream reports usage out-of-band.
- **IMPROVE**: explicit response-too-large terminal condition ([E-S2A-PROXY-021 risk note](../../07_REFERENCE_EVIDENCE_LEDGER.md)) so operators can debug rare provider event-size violations.
- **AVOID**: returning reconstructed content while leaving billing state ambiguous. Either bill an inferred cost (with confidence) or refuse content. Silent ambiguity is worse than either.
- **AVOID**: automatic mid-stream failover after client-visible output unless the client explicitly opted into duplicate-output recovery via a request flag. Default behavior: terminal stream error + partial usage settlement.
- **AVOID**: routing a slow-client into the same upstream Account as healthy clients. The transport-pool isolation rules (E-S2A-PROXY-018) plus per-Account active-stream count ([F-OPS-001](../../03_FEATURE_PARITY_MATRIX.md)) make a slow-client quarantine possible.

## 7. ATTRIBUTION

- Source files read: Sub2API backend gateway streaming and usage-extraction files (specifier session output retained in `.omc/artifacts/ask/codex-role-codex-specifier-lane-miner-for-huakai-owner-directive-2-2026-04-28T05-31-56-975Z.md`, gitignored). Behaviors above are paraphrased from that specifier-lane session output; no upstream function name, struct field, file path, or distinctive identifier appears here.
- Specifier-lane session: Codex (gpt-5.5 + xhigh, critic agent), 2026-04-28T05:31 UTC.
- Reviewer-lane session: TBD — must be a different agent session than the specifier above. Reviewer must verify the file against [_REVIEW_CHECKLIST.md](../specs/_REVIEW_CHECKLIST.md) CL-001..010 before Status moves to Reviewed. Particular attention: CL-005 (no algorithmic line-by-line translation); §2 prose was deliberately written in different sentence structure than upstream code ordering.

## Review Sign-Off

| Field | Value |
| --- | --- |
| Reviewer | (pending) |
| Review date | (pending) |
| Checks passed | (pending; CL-001..010 must all pass) |
| Notes | Second decomposition file under [22_DEEP_MINING_MANDATE.md](../../22_DEEP_MINING_MANDATE.md). The streaming forwarder is the highest-load-bearing decomposition for the relay-station product identity. |
