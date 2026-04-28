# F-GW-002 Streaming Forwarder + Usage Accounting - Codex Specifier Pass

| Field | Value |
| --- | --- |
| Status | Draft - specifier-lane decomposition, pending mutual review |
| Feature ID | F-GW-002 |
| Lane mode | Option C strict carve-out: gateway hot path intersects Provider Account failover, account-health, and Billing Ledger reconciliation |
| Date | 2026-04-28 |
| Sources read with commit hashes | Sub2API (E-LIC-001) `b0a2252ed19c3720e6adafde6083e64fbac2efa9`; one-api (E-LIC-004) `8df4a2670b98266bd287c698243fff327d9748cf`; New API (E-LIC-002) `fc377dae3e3994dc4b076e678704e1c5ef7a5e90` |
| Clean-room note | Behavior only. No reference source, file paths, function names, schema names, comments, or tests are copied into this document. |

## Sub2API Streaming Forwarder Behavior
### WHY
Sub2API is the strongest empirical reference for HUAKAI's relay-station hot path because it combines Provider Account pooling, Provider Account transport isolation, streaming conversion, inline usage capture, and post-stream billing behavior in one gateway flow. The relevant lesson is not a byte-copying proxy; it is a stream-aware forwarder that can preserve UX while still producing a Usage Record for billing.

### WHAT - Step By Step In HUAKAI Vocabulary
1. A request enters after API Key authentication, Pooling Group selection, Provider Account selection, and Tx1-style admission have produced a logical request context.
2. The gateway normalizes the client request before dispatch; streaming is treated as a protocol mode, not as an opaque byte pipe.
3. The upstream transport is selected through a Provider Account-aware pool; active streams are isolated and protected from idle eviction.
4. The response path classifies payload shape: SSE event stream, provider chunked JSON stream, or raw chunk stream for media-like bodies.
5. Each stream event is parsed, inspected, safely re-emitted, and flushed promptly; malformed empty events can be suppressed.
6. Usage is extracted inline from provider events and metadata, including cache, thinking/reasoning, media, and terminal usage metadata.
7. Slow-client handling uses blocking writes and bounded upstream read queues; downstream disconnect can trigger limited upstream drain for billing evidence.
8. Terminal markers, provider stop events, and missing terminal evidence drive usage-complete versus usage-partial classification.
9. Upstream errors are normalized into client-safe stream failures while richer diagnostics remain operator-facing.
10. Retry/failover is typed before output is visible; after client-visible output, no reliable automatic reroute behavior was observed.

### INPUTS
- `tenant_id`, API Key identity, User identity, requested Model, endpoint family, request payload class, stream preference.
- Pooling Group, selected Provider Account, Provider Account health state, Provider Account active-stream count, transport isolation policy.
- Tx1 claim/reservation context, estimated cost, `routing_reason`, per-request retry/failover policy, timeout policy.
- Upstream response status, headers, event frames, terminal marker, provider usage metadata, downstream write status.

### FAILURES HANDLED
- Pre-response network failure and non-success response can be classified before client-visible output.
- Rate-limit, auth failure, quota exhaustion, overload, malformed request, compatibility correction, stream timeout, and protocol violation can drive routing/cooldown decisions.
- Missing or malformed stream events do not corrupt prior deltas; partial content can still settle with partial usage.
- Downstream write failure can still preserve billing evidence through bounded drain.
- Transport pressure avoids evicting live streams while allowing idle cleanup.

### FAILURES NOT HANDLED
- Streaming body read duration is not fully split across all HUAKAI-required timeout axes.
- Post-disconnect drain can burn Provider Account quota without byte, time, and cost ceilings.
- Usage inference is not universal; some streams produce content but no reliable final token usage.
- Missing terminal marker can leave content success and billing certainty in tension.
- Automatic failover after emitted output is not a safe default.

### KEEP-IMPROVE-AVOID
- KEEP protocol-aware stream parsing and inline usage extraction.
- KEEP Provider Account transport isolation and active-stream protection.
- KEEP client-safe error separation from operator diagnostics.
- IMPROVE timeout policy into eight axes: connect, TLS, request-write, response-header, first-token, inter-event, total-stream, downstream-write.
- IMPROVE post-disconnect drain with `drain_max_bytes`, `drain_max_seconds`, and `drain_max_estimated_cost`.
- IMPROVE usage sources into `reported / normalized / inferred / partial`, with explicit reconciliation.
- AVOID raw byte-pipe forwarding for text/event streams.
- AVOID returning reconstructed content while leaving Billing Ledger state ambiguous.
- AVOID automatic failover after any client-visible token unless a future protocol explicitly supports replay-safe resume.

### ATTRIBUTION
Behavior verified from Sub2API source at commit `b0a2252ed19c3720e6adafde6083e64fbac2efa9`. Used only as empirical behavior evidence under Option C strict specifier-lane rules.

## one-api / New API Streaming Behavior
### WHY
one-api is the MIT baseline for OpenAI-compatible streaming and quota reconciliation. New API is the richer AGPL comparison point for provider-format conversion, stream status tracking, event queueing, and fallback usage estimation. Together they show the minimum viable behavior and the production pain points HUAKAI must close.

### WHAT - Step By Step In HUAKAI Vocabulary
1. Both references accept stream and non-stream requests through the same relay surface, then branch response handling by stream mode.
2. one-api requests provider-side stream usage when supported, scans provider event lines, forwards valid events, accumulates text deltas, and emits a terminal marker if the upstream omits one.
3. one-api uses explicit provider usage when present; otherwise it estimates completion usage from accumulated text and reconciles quota after the response path.
4. one-api's post-response quota update is separate from the forwarding hot path and is not a money-grade Tx2 equivalent.
5. New API classifies normal end, timeout, client gone, scanner error, handler stop, EOF, panic, and keepalive failure.
6. New API decouples upstream scanning from downstream handling through a bounded data channel, adds keepalive events, and applies inter-event timeout.
7. New API extracts usage from late stream events, provider-native metadata, and text fallback, including cache/reasoning/media/tool-like usage.
8. New API can convert between provider stream formats while preserving client protocol shape and optionally emitting final usage.
9. Both references show that streaming accounting cannot rely only on a post-hoc buffered parse; inline extraction and source confidence are required.

### INPUTS
- Requested Model, mapped Provider Account model, stream preference, endpoint family, prompt estimate, maximum output hints.
- Provider response events, terminal marker, explicit usage payloads, provider-native error events, downstream write success/failure.
- User/Key quota snapshot, Provider Account selection result, pre-consumed or reserved amount, retry eligibility, final response metadata.

### FAILURES HANDLED
- Missing explicit usage can fall back to local token estimation from streamed text.
- Missing terminal marker can still produce a client terminal marker in OpenAI-compatible streams.
- Provider-native stream events can be converted into a different client protocol.
- Inter-event timeout, client disconnect, handler stop, scanner errors, and keepalive failures can be classified separately in the richer comparison reference.

### FAILURES NOT HANDLED
- one-api's reservation/refund model does not provide a strict Billing Ledger claim across all attempts.
- one-api can treat scanner errors after partial output closer to partial success than a fully typed billing state.
- New API has better stream state tracking, but final Usage Record settlement is still not HUAKAI's Tx2 boundary.
- Neither reference provides HUAKAI's required drain budget or replay-safe mid-stream failover after client-visible output.

### KEEP-IMPROVE-AVOID
- KEEP stream and non-stream sharing one accounting engine.
- KEEP explicit provider usage when available, plus local inference fallback when absent.
- KEEP bounded queueing and explicit stream end reasons from the richer comparison reference.
- IMPROVE with Tx1 reservation before upstream and Tx2 reconcile after stream resolution.
- IMPROVE by labeling every Usage Record source and keeping late reconciliation state.
- IMPROVE by making missing terminal marker a billable partial-state decision, not only a transport detail.
- AVOID async or batched money mutation for final Usage Record/Billing Ledger settlement.
- AVOID treating client-visible partial output as retry-safe.

### ATTRIBUTION
Behavior verified from one-api source at commit `8df4a2670b98266bd287c698243fff327d9748cf` and New API source at commit `fc377dae3e3994dc4b076e678704e1c5ef7a5e90`. Used only as behavior evidence; HUAKAI design below is independent.

## HUAKAI Algorithm Design - Option C Strict
### Design Position
HUAKAI implements F-GW-002 as a protocol-aware streaming forwarder plus a usage reconciler. The forwarder never owns money mutation directly; it produces a final usage vector and stream outcome that Tx2 consumes. Usage Record finalization happens inside Tx2, together with Billing Ledger finalization and Provider Account reservation reconciliation.

### Phase 0 - Preconditions
Tx1 has already reserved estimated cost under `tenant_id`, logical request identity, API Key, User, Pooling Group, Provider Account, endpoint family, requested Model, and `routing_reason`. The selected Provider Account's active-stream count is acquired before the upstream request is opened and is released only through the same lifecycle that closes the request attempt.

### Phase 1 - Parse
1. Detect stream protocol family from request/response contract: SSE, chunked JSON, or raw chunk.
2. Select a parser by protocol family. SSE and chunked JSON are event-parsed; raw chunks are copied through a metered byte path and cannot claim fine-grained token usage unless separate metadata appears.
3. Maintain in-flight state: bytes received, events parsed, first-token timestamp, last-event timestamp, terminal marker, downstream output started, and failure class.
4. Oversized events become a typed terminal stream error with partial usage settlement.

### Phase 2 - Extract
1. For every parsed event, update a usage accumulator before re-emitting the event.
2. The accumulator stores usage by axis: input, output, cache read/write, reasoning/thinking, image/media, tool/server-side work, and provider-reported cost.
3. Each axis carries a source label: `reported`, `normalized`, `inferred`, or `partial`.
4. Reported provider totals win for the axes they cover. Normalized provider-native fields win over local inference. Inference fills only missing axes and never subtracts a reported total.
5. If two explicit sources conflict beyond tolerance, keep the higher-confidence source, mark `usage_conflict`, and require operator-visible reconciliation.
6. If terminal marker is missing, downstream disconnects, or drain budget is exhausted, the final source becomes `partial` even if some axes are reported.
7. If usage is inferred, mark the Usage Record as pending late reconciliation; later provider usage reconciles by adjustment, never by mutating the original Billing Ledger entry.

### Phase 3 - Re-emit
1. Re-emit only client-safe events in the client's requested protocol.
2. Flush after event or configured batch interval.
3. Apply slow-client backpressure through a bounded upstream read queue; if full, upstream reads pause rather than growing memory.
4. Downstream write has its own timeout; on expiry, mark client disconnect/stall and enter bounded drain if billing preservation is allowed.
5. Post-disconnect drain stops at the first of `drain_max_bytes`, `drain_max_seconds`, or `drain_max_estimated_cost`.
6. If drain finishes with reliable terminal usage, settle as reported/normalized. If not, settle as partial and preserve drain-stop reason.

### Phase 4 - Reconcile
1. On terminal marker, EOF after complete terminal event, timeout, client disconnect, parser error, or provider stream error, produce exactly one stream outcome for Tx2.
2. Tx2 locks the same claim and billing rows defined by the quota-billing claim gate.
3. Tx2 computes actual cost from the reconciled usage vector and the versioned pricing context.
4. Tx2 applies delta from estimated cost to actual cost.
5. Tx2 inserts the Usage Record inside the same transaction.
6. Tx2 inserts the Billing Ledger entry referencing the claim and Usage Record.
7. Tx2 closes the Provider Account active-stream count and records `routing_reason`, terminal status, usage source, timeout axis if any, and drain outcome.

### Timeout Policy - Eight Axes
| Axis | HUAKAI behavior |
| --- | --- |
| connect | Failure before Provider Account write; retry/failover allowed if policy permits. |
| TLS | Transport identity failure; Provider Account may be cooled down or quarantined. |
| request-write | Failure before upstream accepted request body; retry/failover allowed if no client-visible output. |
| response-header | Upstream did not produce headers; retry/failover allowed if no output. |
| first-token | Headers arrived but no stream event; retry/failover depends on whether any output reached client. |
| inter-event | Stream stalled after one or more events; terminal stream error and partial settlement by default. |
| total-stream | Route-level wall-clock budget exceeded; terminal stream error and partial settlement. |
| downstream-write | Client is slow/gone; bounded drain for billing preservation, then partial settlement. |

### Failure Taxonomy Mapping
| Failure class | Default client result | Provider Account action | Billing result |
| --- | --- | --- | --- |
| pre_response_network | Retry/failover if policy allows | Record attempt failure | Tx1 retry path, no Usage Record yet |
| pre_response_rate_limit | Retry another Provider Account if available | Cooldown with reset hint | Tx1 retry path |
| pre_response_auth_failure | Terminal error unless credential refresh succeeds | Mark credential problem | Release reservation or retry after refresh |
| malformed_request | Client-safe 4xx | No cooldown | Release reservation, no billable output |
| provider_protocol_violation_before_output | Retry/failover if policy allows | Degrade Provider Account health | No Usage Record unless usage evidence exists |
| provider_protocol_violation_after_output | Terminal stream error | Degrade Provider Account health | Partial Usage Record in Tx2 |
| mid_stream_rate_limit | Terminal stream error | Cooldown Provider Account | Partial Usage Record in Tx2 |
| mid_stream_auth_failure | Terminal stream error | Mark credential problem | Partial Usage Record in Tx2 |
| upstream_disconnect_mid_stream | Terminal stream error | Health signal by class | Partial Usage Record in Tx2 |
| client_disconnect | No further client output | No provider penalty by default | Drain-limited Usage Record in Tx2 |
| missing_terminal_marker | Terminal stream error if protocol requires marker | Protocol-quality signal | Partial or inferred Usage Record in Tx2 |
| downstream_write_timeout | Terminal from gateway perspective | No provider penalty by default | Drain-limited Usage Record in Tx2 |

### Mid-Stream Failover Rule
HUAKAI default is no automatic failover after client-visible output. Before any output is emitted, Route policy may retry/fail over using the same Billing Ledger claim. After output is emitted, the gateway emits a terminal stream error, finalizes partial usage in Tx2, and records why failover was suppressed. A future replay-safe resume protocol may be a separate feature flag, not the default.

### Per-Account Active-Stream Count
Each Provider Account has an active-stream counter independent from request-per-minute and token-per-minute limits. The counter is acquired before upstream dispatch, increments transport-pool occupancy, and releases only after Tx2 or release recovery closes the attempt. Pooling Group routing must treat active streams as a first-class load signal.

### Open Questions For PM
- Should `drain_max_estimated_cost` default be global, per Route, or per Pooling Group?
- For `inferred` usage, what tolerance allows late reported usage to auto-adjust versus require manual operator approval?
- Should client-visible stream errors expose `partial_usage_settled` as a protocol extension, or only record it in operator/audit surfaces?
- Should raw chunk media streams be L1 for F-GW-002, or deferred under multi-modal/Realtime roadmap while text SSE is L1?
- Which Provider Account health actions require operator confirmation versus automatic cooldown?

## Review Sign-Off

| Field | Value |
| --- | --- |
| Reviewer | Pending - must be a different reviewer-lane session |
| Review date | Pending |
| Checks passed | Pending CL-001 through CL-010 |
| Notes | Draft contains no reference source paths, code identifiers, schema names, comments, or tests by design. |
