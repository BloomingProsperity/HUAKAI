# portkey - Streaming response handler

| Field | Value |
| --- | --- |
| Status | Draft |
| Reference | Portkey AI Gateway, MIT, E-LIC-006 |
| Feature in HUAKAI matrix | F-GW-002 (L1) |
| Evidence ledger row | E-PK-001, E-PK-002, E-PK-003, E-PK-005, E-PK-007, E-PK-DEEP-STREAMING-ROUND2-PENDING |
| Specifier session | Codex specifier-lane Round 2, 2026-04-29 |
| Specifier date | 2026-04-29 |
| Reviewer session | Pending reviewer-lane |
| Reviewer date | Pending |
| Source files read | Public source region: response dispatcher and hook post-processing; public source region: text and binary stream readers; public source region: retry and timeout orchestration; public docs: retries, request timeouts, Responses API; public source-indexed wiki: cache and transformation regions |

## 1. WHY (motivation / context)

The upstream feature exists because a gateway that claims OpenAI-compatible behavior cannot treat streaming as an optional transport detail.
Users expect token-by-token output to arrive with low latency, while operators still expect retries, fallbacks, cache, guardrails, usage accounting, and observability to behave consistently with non-streaming calls.
The source-verified picture is that the upstream attempts to preserve client-visible streaming while routing many provider-specific event shapes through one response layer.

The real pressure is a three-way constraint.
First, a stream is consumed as it is produced, so the gateway cannot wait for a complete response body before forwarding without destroying the streaming experience.
Second, the gateway still needs to transform provider-native chunks into client-facing chunks, and provider stream protocols are not uniform.
Third, once the first tenant-visible chunk leaves the gateway, ordinary retry and fallback semantics become unsafe because replay may duplicate content and double-count usage.

Critic C-001 is CONFIRMED from the response-dispatch and hook post-processing source regions.
The source pattern treats two transport statuses as stream-transformable success states, and the hook path can mark a streaming response as policy-failed while preserving the live body.
The behavior is not "HTTP 200 means success"; it is "the stream remains consumable under a policy-warning status when the hook result is non-denying."
HUAKAI must therefore avoid using tenant-visible HTTP status alone as billing, retry, or policy truth.

Critic C-002 is CONFIRMED from the same hook region.
The source pattern has no completed JSON response object for live streaming, so post-response checks do not have a full body to inspect before bytes are emitted.
The observable behavior is that a streaming body can continue even when hook metadata records failure unless a denial path exists before body emission.
HUAKAI must separate pre-stream policy, inline chunk policy, terminal policy, and operator-selected fail-closed cancellation.

Critic D-001 is CONFIRMED by comparing public product docs with source regions.
Docs describe gateway features as broadly available with streaming, but the source-dispatch path returns no terminal JSON body for streams.
That creates weaker final-body observability than the non-streaming path.
HUAKAI should keep the user-visible promise but implement a durable stream accumulator rather than relying on a missing final response object.

Critic D-003 is CONFIRMED from the retry docs and the source-indexed retry region.
Retries and fallbacks are marketed as reliability features, but a live stream is not equivalent to a buffered response after first byte.
HUAKAI must explicitly define where replay is allowed, where it is suppressed, and how partial usage survives.

The motivation for HUAKAI is stronger than the upstream because HUAKAI is a relay-station and quota-pooling product.
F-GW-002 is not only "send SSE to client"; it is the part of the gateway that prevents a mid-stream disconnect, stalled provider, cache replay, or guardrail warning from corrupting Usage Records, Billing Ledger entries, Provider Account health, or tenant isolation.

## 2. WHAT (algorithm in HUAKAI vocabulary)

HUAKAI should model the upstream feature as a stream lifecycle coordinator, not a simple byte pipe.
The coordinator receives a request after API Key auth, User and User Group resolution, Quota reservation, Route match, Channel selection, and Provider Account selection.
It then manages a stream attempt from upstream acceptance through final Usage Record reconciliation.
The upstream source proves that several behaviors are folded into one response layer: status-based dispatch, provider stream normalization, cache-hit stream reconstruction, hook-status rewriting, and no terminal JSON body for streaming.

### 2.1 Lifecycle phases

Phase A: pre-stream admission.
The gateway has not emitted tenant-visible content.
It may retry or fall back to another Channel or Provider Account if Route policy allows.
It may deny before emission when input policy, quota, auth, routing, or pre-response provider error says the request must stop.

Phase B: stream opening.
Provider headers and content type are known.
The response layer decides whether this is a text event stream, a binary provider event stream, a reconstructed stream from cached JSON, or a non-streaming body.
Critic F-001 is CONFIRMED from the stream-source regions: the upstream has separate text and binary readers, split patterns, chunk transformers, and JSON-to-stream conversion.
HUAKAI must not implement this as a generic `pipe`.

Phase C: live event loop.
Each upstream event is parsed, normalized, scanned for usage signals, optionally checked by inline policy, emitted to the client, flushed, and recorded into in-memory attempt state.
This phase has per-event concurrency pressure: slow clients, fast providers, and large chunks can race with cancellation and timeout signals.

Phase D: terminal classification.
The stream ends through a terminal marker, upstream EOF, parser error, timeout, client disconnect, policy cancel, or orchestrator cancel.
The coordinator emits exactly one terminal outcome.
If no completed terminal body exists, the stream accumulator becomes the Usage Record source.

Phase E: Tx2 finalization.
HUAKAI commits final state in PostgreSQL: Usage Record, claim settlement, Provider Account active-stream release, audit-grade event, and outbox signals.
This is a HUAKAI improvement over the source pattern where streaming returns body-first and downstream systems must infer outcome from side channels.

### 2.2 Sub-behaviors

S-1: Streaming success admission.
Trigger condition: Provider response has a stream-compatible content type and a status classified as consumable by gateway policy.
State transitions: request attempt moves from `headers_pending` to `stream_open`; per-request `stream_mode` is set; Provider Account active-stream lease remains held.
Concurrency interaction: if two streams for the same Provider Account open at once, each must hold a distinct lease; slot release is idempotent per lease, not per request id alone.

S-2: Policy-warning stream continuation.
Trigger condition: post-response policy checks report failure but not denial while a live stream body exists.
State transitions: per-request policy verdict becomes `warned`; stream outcome remains open; tenant response metadata may carry policy status; Usage Record must record policy warning.
Concurrency interaction: concurrent policy updates must not mutate another tenant's stream verdict; every verdict is scoped by tenant, request id, and attempt sequence.
This addresses C-001 and S-001: HUAKAI should not copy pseudo-success HTTP statuses as the source of truth.

S-3: Pre-stream denial.
Trigger condition: input guardrail, authorization, quota, or output policy can deny before any client-visible chunk.
State transitions: request attempt moves to `denied_before_output`; Provider Account lease is released; claim is aborted or charged according to policy; no stream loop starts.
Concurrency interaction: concurrent retries must see the aborted claim and cannot reopen the same attempt.

S-4: Inline chunk policy.
Trigger condition: a Route or tenant policy requires high-risk output scanning while chunks arrive.
State transitions: chunk buffer enters `policy_pending`; safe chunks may emit; denied chunks trigger `policy_cancelled_after_output` or `denied_before_output` depending on whether content has already been sent.
Concurrency interaction: policy scanner and upstream reader run with bounded queue; if scanner lags, upstream reads pause or the attempt terminates by policy timeout.
This addresses C-002, F-002, and S-002.

S-5: Text event stream normalization.
Trigger condition: upstream body is delimiter-based text event stream.
State transitions: per-attempt parser buffer appends bytes, emits normalized events, updates `last_event_at`, and increments observed event count.
Concurrency interaction: two chunks can split one logical event, so parser state is per attempt and cannot be shared across requests.

S-6: Binary provider event normalization.
Trigger condition: selected Provider sends binary event frames rather than textual SSE.
State transitions: binary frame decoder emits canonical stream events; raw binary metadata is discarded or summarized after audit-safe extraction; parser family is recorded.
Concurrency interaction: binary decoder errors terminate only the affected attempt and must not poison shared provider adapter state.
This confirms C-007 and D-002: "SSE" is only one stream family.

S-7: Cache-hit JSON-to-stream reconstruction.
Trigger condition: cache lookup returns a buffered JSON response while the client requested streaming.
State transitions: cache status becomes `hit`; no fresh upstream Provider Account call is made; cached body is transformed into tenant-visible stream events; Usage Record marks response origin as cache.
Concurrency interaction: concurrent cache hits must not share mutable stream generator state or leak cached metadata across tenants.
This confirms C-005, F-005, D-004, N-005, and S-005.

S-8: Retry before output.
Trigger condition: pre-stream provider error, request timeout before first accepted stream event, or configured fallback status occurs before client-visible output.
State transitions: attempt sequence increments; failed Provider Account may receive health or cooldown signal; the same logical request claim remains reserved; new Channel or Provider Account may be selected.
Concurrency interaction: two fallback attempts must not both emit to the same client stream; only the active attempt owns downstream writer.

S-9: Retry suppression after output.
Trigger condition: retryable upstream failure occurs after at least one content event has been emitted.
State transitions: request attempt becomes `partial_after_output`; fallback is suppressed unless explicit replay-safe policy exists; Usage Record records partial usage and suppression reason.
Concurrency interaction: if retry orchestration races with writer flush, the emitted-content flag wins once a flush succeeds.
This addresses C-004, D-003, and F-003.

S-10: Client disconnect handling.
Trigger condition: downstream write fails, downstream context cancels, or flush times out.
State transitions: downstream state becomes `closed`; stream enters bounded drain if policy allows; no more bytes are written; accumulated usage remains eligible for reconciliation.
Concurrency interaction: upstream reader, drain loop, and Tx2 finalizer race; exactly one finalizer may close the attempt.
This addresses C-006.

S-11: Idle provider stream timeout.
Trigger condition: headers arrived but first event or subsequent event does not arrive within Route budget.
State transitions: stream outcome becomes `first_event_timeout` or `inter_event_timeout`; Provider Account health receives latency signal; Usage Record source becomes partial or ambiguous.
Concurrency interaction: timeout timer resets only when a complete logical event is parsed, not when an arbitrary byte arrives.
This confirms C-003 and F-003.

S-12: Scanner buffer overflow.
Trigger condition: a single logical event exceeds configured parser buffer cap.
State transitions: stream outcome becomes `event_too_large`; parser stops; downstream receives a terminal stream error if output is still possible; Usage Record is no-charge or partial according to emitted-content state.
Concurrency interaction: overflow handling must cancel upstream read once; if client disconnect fires simultaneously, the more specific parser failure is retained as primary and disconnect as secondary.

S-13: Terminal frame detection.
Trigger condition: normalized event matches the protocol's terminal signal or provider stream ends cleanly.
State transitions: `terminal_seen` flips true for graceful terminal; accumulator freezes; Tx2 finalization starts.
Concurrency interaction: terminal event and client disconnect can arrive near the same time; if terminal was parsed before failed write, billing may use terminal usage but downstream outcome records client loss.

S-14: Partial usage reconciliation.
Trigger condition: terminal usage is absent, inconsistent, or only partially observed because of disconnect, timeout, parser error, or cache reconstruction.
State transitions: usage source becomes `reported`, `normalized`, `inferred`, `partial`, or `ambiguous`; Billing Ledger action follows the source confidence policy.
Concurrency interaction: multiple usage sources may arrive from chunks and terminal frame; higher-trust source wins field-by-field, and conflicts are recorded.

S-15: Hook-result event injection.
Trigger condition: policy checks produce results that should be exposed to the stream consumer.
State transitions: normalized event stream gets a policy metadata event; Usage Record records hook summary; response status alone is not the policy contract.
Concurrency interaction: injection must happen in stream order and must not interleave metadata from another request.
This addresses D-001 and S-004.

S-16: Stream observability without final JSON.
Trigger condition: the response is streaming and no full response JSON exists.
State transitions: stream accumulator becomes the durable terminal response surrogate; trace spans and logs reference stream outcome, event counts, byte counts, usage source, and policy verdict.
Concurrency interaction: if logging service is asynchronous, Tx2 must still have enough data to finalize without waiting for logs.
This addresses C-008 and S-004.

### 2-bis. Three request lifecycles

Happy-path lifecycle.
The client sends a streaming request through an API Key.
HUAKAI resolves User, User Group, tenant, Route, Channel, Model, and Provider Account.
Tx1 reserves quota and a Provider Account active-stream slot.
The upstream Provider accepts the request and returns stream-compatible headers.
The response layer classifies the stream family, initializes parser state, emits normalized events to the client, flushes each event, updates first-event latency, and accumulates usage signals.
A terminal frame arrives with final usage or a complete semantic end signal.
The coordinator marks `terminal_seen`, freezes accumulators, releases the active-stream slot, writes Usage Record, commits Billing Ledger settlement, and records stream outcome in Tx2.
The client receives the terminal event, and the operator sees a graceful stream metric.

Partial-failure lifecycle.
The client receives several events and then disconnects.
The downstream writer reports failure.
HUAKAI flips downstream state to closed and stops writing.
If Route policy allows drain, the gateway reads upstream for a bounded time, byte count, or estimated cost, extracting usage only.
If the drain reaches terminal usage, usage source can be `reported` with disconnect annotation.
If the drain exhausts budget first, usage source is `partial` or `inferred`.
Tx2 commits a Usage Record with partial-failure outcome, releases Provider Account slot, and records that fallback was suppressed because output had already been emitted.
The state that survives is the claim, attempt id, emitted event count, usage accumulator, disconnect time, drain outcome, and tenant-scoped audit record.

Full-failure lifecycle.
The Provider sends an oversized event before any content is safely emitted, or the parser fails before a valid event can be normalized.
The coordinator terminates the attempt, cancels the upstream reader, and returns a gateway stream error if no response has started.
If no usage can be established, usage source becomes `ambiguous` and customer charge is zero pending operator investigation.
Provider Account slot is released in Tx2, quota reservation is aborted or corrected, and an operator alert records parser failure class, Provider, Model, Route, Channel, and tenant.
Cleanup obligations are upstream cancellation, parser buffer disposal, no dangling goroutine, no open writer, no unreleased slot, and no `reserving` claim left without sweep coverage.

## 3. INPUTS (signals consumed, state mutated)

Per-Request fields read:
tenant id, API Key id, User id, User Group id, logical request id, attempt sequence, endpoint family, requested Model, streaming flag, request method, request body type, request headers, cache controls, Route id, Channel id, Provider Account id, retry policy, fallback policy, timeout policy, policy pack ids, output guardrail mode, cache key candidate, trace id, and requested response protocol.

Per-Request fields written:
stream family, emitted-content flag, first-event timestamp, last-event timestamp, event count, byte count, parser buffer size, terminal marker flag, policy verdict, policy metadata event count, cache hit flag, response origin, attempt outcome, retry suppression reason, fallback attempt count, usage source, usage counters, cost estimate, disconnect timestamp, drain outcome, error class, and Tx2 finalization status.

Per-Account and per-Channel state read:
Provider Account lifecycle state, credential availability, Channel enabled/degraded state, Channel model allow-list, per-Channel limits, per-Provider-Account active-stream count, health score, cooldown state, cost cap, routing weight, and Provider stream adapter capability.

Per-Account and per-Channel state mutated:
active-stream lease acquire/release, health signal on timeout or protocol violation, cooldown signal on rate-limit or auth failure, per-account in-flight counters, Channel degraded metric, and operator-visible incident counters.
Lifetime: active-stream lease lives from upstream attempt start through Tx2 or orphan sweep; health and cooldown outlive the request; per-attempt parser state dies at finalization.

Per-Tenant isolation boundaries:
cache keys must include tenant boundary; policy packs are tenant-scoped; API Key and User counters are tenant-scoped; Usage Records and Billing Ledger entries carry tenant id; logs, trace ids, hook results, and reconstructed cache stream metadata cannot cross tenant.
DR-001 means the Personal Edition default tenant still uses the same boundary so SaaS Edition does not require a schema rewrite.

Per-Process state:
per-attempt parser buffers, transform adapters, downstream writer state, retry attempt counter, timeout timers, bounded queues between upstream reader and policy scanner, stream accumulators, temporary cache reconstruction generator, and cancellation handles.
These are in-memory and must be recoverable by durable Tx1/Tx2 rows and orphan sweep if the process exits.

Persistent tables and indexes touched in HUAKAI:
Usage Records table by tenant id, request id, attempt sequence, Provider Account id, Model, end class, and created time.
Billing Ledger table by tenant id, claim id, Usage Record id, and money movement type.
Quota reservation or claim table by tenant id, User or User Group id, claim id, status, and lease expiry.
Provider Account table or state table by account id, tenant/operator ownership boundary, active-stream count, cooldown state, and health fields.
Audit Event table by tenant id, actor, action, request id, and created time.
Outbox table for alerts and reconciliation tasks.
Cache table by tenant id, cache key, Provider, Model, request semantic hash, TTL, and response origin.

Transaction boundaries:
Tx1 reserves quota and stream slot before upstream spend.
Live stream events are not individually committed unless enterprise retention policy requires chunk audit.
Tx2 atomically releases stream slot, writes Usage Record, commits or aborts billing claim, writes audit-grade billing event, and enqueues recovery or alert outbox rows.
A separate orphan sweep resolves attempts stuck between Tx1 and Tx2.

## 4. FAILURE MODES HANDLED

F-1: Policy warning with consumable stream.
Trigger: non-denying policy failure on a streaming response.
Observable outcome: client may still receive stream; HUAKAI records policy warning separately from transport success.
Operator-visible signal: policy warning counter by tenant, Route, policy pack, Model.
Recovery action: tune policy pack, switch Route to fail-closed, or enable inline chunk scanning.
Blast radius: single request unless policy pack is globally misconfigured; then single tenant or cluster-wide depending scope.

F-2: Output policy denial before first chunk.
Trigger: deny-capable policy resolves before emission.
Observable outcome: stream is not opened; client receives typed gateway error.
Operator-visible signal: denied-before-output count and policy reason.
Recovery action: operator reviews false positive and can audited-bypass for future Route.
Blast radius: single request or single tenant policy.

F-3: Unsafe chunk after emission.
Trigger: inline policy detects forbidden content after earlier safe chunks.
Observable outcome: stream terminates with policy-cancel event; Usage Record is partial and policy-cancelled.
Operator-visible signal: high-severity content policy event.
Recovery action: increase buffering for high-risk Routes or require pre-generation moderation where possible.
Blast radius: single request; content already emitted is not recoverable.

F-4: Provider idle after headers.
Trigger: no first event or no subsequent event within configured budget.
Observable outcome: stream ends by timeout; retry only if no output has been emitted.
Operator-visible signal: first-event or inter-event timeout metric by Provider Account.
Recovery action: tune timeout by Model, fail over pre-output, or degrade slow Provider Account.
Blast radius: single Provider Account or Channel if systemic.

F-5: Scanner buffer overflow.
Trigger: one event exceeds parser cap.
Observable outcome: typed terminal error; no silent truncation.
Operator-visible signal: event-too-large alert with Provider and Model tags.
Recovery action: raise cap for legitimate models, quarantine malformed Provider stream, or add adapter fix.
Blast radius: single request or Provider adapter.

F-6: Malformed stream boundary.
Trigger: bytes cannot be parsed into valid event boundaries.
Observable outcome: parser failure; no further downstream writes; usage source partial or ambiguous.
Operator-visible signal: protocol violation counter.
Recovery action: inspect Provider adapter, add compatibility rule, or disable affected Channel.
Blast radius: single Provider adapter, possibly single-process if parser leak exists.

F-7: Client disconnect.
Trigger: write or flush fails, downstream cancellation, or downstream-write timeout.
Observable outcome: no more tenant-visible bytes; bounded drain may continue for reconciliation.
Operator-visible signal: disconnect and drain outcome metrics.
Recovery action: adjust drain budgets, investigate client network, or reduce stream chunk size.
Blast radius: single request.

F-8: Mid-stream provider failure after output.
Trigger: Provider stream terminates with error after content emitted.
Observable outcome: fallback suppressed by default; Usage Record partial; client sees stream interruption.
Operator-visible signal: partial-after-output counter and fallback-suppressed reason.
Recovery action: operator can enable replay-safe protocol for specific clients or improve Provider Account health routing.
Blast radius: single request; Provider Account if repeated.

F-9: Cache-hit reconstruction mismatch.
Trigger: cached JSON converted to stream does not match live stream terminal contract, headers, usage, or policy metadata.
Observable outcome: client receives stream-origin cache result with deterministic terminal event; if mismatch detected, cache bypass or invalidation.
Operator-visible signal: cache-stream-reconstruction mismatch metric.
Recovery action: invalidate cache key, disable streaming cache replay for Route, fix transformer.
Blast radius: single tenant cache key; cross-tenant if cache key isolation broken.

F-10: Binary event decoder failure.
Trigger: Provider sends corrupt or unsupported binary event frame.
Observable outcome: stream terminates with provider protocol error.
Operator-visible signal: binary decoder failure metric.
Recovery action: disable affected Provider adapter or update adapter.
Blast radius: Provider adapter or Channel.

F-11: Missing terminal usage.
Trigger: stream ends without terminal usage frame or usage fields.
Observable outcome: Usage Record source becomes inferred, partial, or ambiguous; billing follows confidence policy.
Operator-visible signal: missing-terminal-usage counter.
Recovery action: enable tokenizer fallback, provider adapter extraction, or no-charge ambiguous outcomes.
Blast radius: Provider, Model, or request class.

F-12: Tx2 finalization failure.
Trigger: database transaction fails after stream loop ends.
Observable outcome: attempt remains in `reserving` or `finalization_pending` state until sweep.
Operator-visible signal: orphan sweep and Tx2 retry alert.
Recovery action: retry Tx2 idempotently; if repeated, pause affected Channel and investigate PostgreSQL.
Blast radius: single process if transient; cluster-wide if database incident.

## 5. FAILURE MODES NOT HANDLED (gaps)

The upstream pattern does not prove fail-closed live output guardrails after bytes have already been forwarded.
HUAKAI closes the gap by requiring pre-stream, inline, and terminal policy phases with Route-level policy mode.

The upstream docs and source-indexed regions do not prove mid-stream replay safety.
HUAKAI therefore defaults to no fallback after emitted content and requires explicit replay-safe client protocol for any future mid-stream failover.

The upstream cache-hit stream behavior is source-confirmed but not sufficient for HUAKAI tenant isolation.
HUAKAI must make cache-origin, tenant id, TTL, headers, terminal event, and usage metadata deterministic and auditable.

The upstream timeout docs explicitly state that streaming timeout does not fire if at least one chunk arrives before the configured duration.
HUAKAI must add first-event, inter-event, total-stream, and downstream-write budgets.

The upstream source regions split binary and text stream readers, but a single generic spec would hide adapter-specific failure.
HUAKAI must model provider stream normalization as F-PROTO-adjacent behavior.

The upstream stream path returns no final JSON body.
HUAKAI must not base Usage Record or Billing Ledger correctness on a body that does not exist.

The upstream public docs say retry attempts are not logged individually and response times are summed.
HUAKAI should record attempt-level events for operator diagnosis while still presenting one logical Usage Record to billing.

## 6. KEEP / IMPROVE / AVOID for HUAKAI

- KEEP: Preserve low-latency event flushing so Users receive real-time output.
- KEEP: Preserve provider stream normalization because provider protocols differ materially.
- KEEP: Preserve cache-hit streaming as a feature, but only with tenant-scoped deterministic reconstruction.
- KEEP: Preserve configurable retry and fallback before stream output.
- KEEP: Preserve request-timeout configurability per Route or target.

- IMPROVE: Replace pseudo-success transport statuses with typed internal policy verdicts and standard tenant-facing response taxonomy.
- IMPROVE: Add stream accumulator state because live streams do not produce a complete terminal JSON body.
- IMPROVE: Add eight-axis timeout policy: connect, TLS, request-write, response-header, first-event, inter-event, total-stream, downstream-write.
- IMPROVE: Add bounded drain after client disconnect so usage can reconcile without unlimited operator spend.
- IMPROVE: Add Tx2 PostgreSQL finalization for Usage Record, Billing Ledger, claim status, and Provider Account slot release.
- IMPROVE: Add tenant-scoped cache stream reconstruction with visible cache origin.
- IMPROVE: Add output policy modes: pre-stream deny, inline chunk scan, terminal-only audit, and fail-open warning.

- AVOID: Do not copy nonstandard policy statuses as public contract; DR-001 dashboards and alerts need unambiguous typed states.
- AVOID: Do not copy live-body reuse after failed streaming checks as HUAKAI's final safety model.
- AVOID: Do not copy provider-specific binary parsing into core routing; place it behind provider adapter boundaries.
- AVOID: Do not copy cache-hit streaming unless cache keys and metadata are tenant-scoped.
- AVOID: Do not copy a single edition story; DR-002 requires Personal Edition and SaaS Edition gates.
- AVOID: Do not rely on in-memory-only stream state; DR-006 requires durable PostgreSQL lifecycle and recovery.

HUAKAI-specific risk R-1 under DR-001: blindly copying cache stream replay can leak another tenant's cached content or trace metadata.
HUAKAI mitigation: tenant id is part of every cache key and every reconstructed stream metadata record.

HUAKAI-specific risk R-2 under DR-001: policy-warning streams can be misreported as ordinary successes in tenant dashboards.
HUAKAI mitigation: policy verdict is a typed Usage Record and audit field independent of transport status.

HUAKAI-specific risk R-3 under DR-002: enterprise-only stream retention could accidentally become required in Personal Edition.
HUAKAI mitigation: Personal Edition gets streaming, usage reconciliation, and basic audit; SaaS Edition adds retention, tenant admin policy, and cross-tenant abuse reports.

HUAKAI-specific risk R-4 under DR-002: removing guardrail or cache behavior from Personal Edition because it is complex would silently shrink feature parity.
HUAKAI mitigation: ship safe equivalents or plugin shells, not deletion.

HUAKAI-specific risk R-5 under DR-006: upstream in-memory stream side-channel patterns are insufficient for crash recovery.
HUAKAI mitigation: Tx1/Tx2 rows, lease expiry, orphan sweep, and outbox alerts in PostgreSQL.

HUAKAI-specific risk R-6 under DR-006: one-row final logs are not enough for partial usage reconciliation.
HUAKAI mitigation: store attempt sequence, end class, usage source, drain outcome, and pending reconciliation flags with indexes.

## 7. ATTRIBUTION

- Public source region: response dispatcher and hook post-processing. Contributed the two-status success pattern, streaming body preservation under policy-warning status, and no terminal JSON body for stream path.
- Public source region: stream readers. Contributed text stream reader, binary provider event reader, split-pattern transformation, chunk id fallback, and hook-result stream injection behavior.
- Public source region: JSON-to-stream conversion. Contributed cache-hit reconstruction behavior for streaming clients.
- Public source region: retry and orchestration. Contributed pre-output retry/fallback shape, inherited retry settings, target fallback flow, and request-attempt response mapping.
- Public docs: request timeouts. Contributed operator-tunable timeout behavior and the caveat that streaming timeout does not trigger once at least one chunk arrives before the timeout.
- Public docs: automatic retries. Contributed retry attempts, retry status-code configuration, retry-after support, cumulative cap, and single-log-entry behavior.
- Public docs: Responses API. Contributed the product claim that Responses API works with Configs, Caching, Guardrails, and Observability.
- Specifier-lane session: Codex specifier-lane Round 2, 2026-04-29.
- Reviewer-lane session: pending.
- Verified clean-room compliance: no upstream function names, struct fields, package names, file paths, directory layout, comments, schemas, or source-shaped pseudocode are included; behavior is described in HUAKAI vocabulary.

## 8. Review Sign-Off

| Field | Value |
| --- | --- |
| Reviewer | Pending fresh reviewer-lane |
| Review date | Pending |
| Checks passed | Pending CL-001 through CL-010 |
| Notes | Round 2 intentionally expands the rejected shallow decomposition into lifecycle, data, failure, and critic-addressed contracts. |

## 9. Open Questions / Implementation Gates

Q-1: Should HUAKAI expose policy-warning streams to clients as an SSE metadata event, response header, or both?
Default recommendation: both, but billing and dashboards use typed internal verdicts only.

Q-2: Which Routes require inline chunk policy rather than terminal-only policy?
Default recommendation: allow operator selection per Route, with fail-closed default for high-risk tenant routes.

Q-3: What is the default cache-hit stream pacing?
Default recommendation: deterministic immediate replay with cache-origin metadata, not artificial provider-like timing.

Q-4: What is the enterprise retention level for chunk-level stream audit?
Default recommendation: Personal Edition stores terminal accumulator only; SaaS Edition can enable chunk-retention plugin with tenant policy.

Q-5: What replay-safe protocol, if any, should permit mid-stream failover after content emission?
Default recommendation: no default replay; future feature flag must require idempotent client opt-in and clear duplicate-content semantics.

Q-6: Should ambiguous usage always no-charge?
Default recommendation: yes for customer charge; operator may record internal estimated cost separately for Provider Account burn analysis.

## 10. Source Coverage Proof

1. Source region `<response dispatcher around stream/non-stream branch>` contributed: streaming response classification, consumable policy-warning status, cache-hit conversion branch, and absence of completed terminal JSON for stream paths.

2. Source region `<post-response hook handling for streaming body>` contributed: failed non-denying hook checks can preserve the live streaming body while changing status metadata; denying behavior is structurally stronger for non-streaming because a JSON body exists.

3. Source region `<text stream reader and stream transformer>` contributed: source uses a chunk loop, provider-specific split patterns, per-chunk transformation, fallback chunk identity, hook-result event insertion, and live body return.

4. Source region `<binary provider event reader>` contributed: AWS-style provider event streams are parsed through a separate binary reader, so HUAKAI must define provider stream normalization rather than one generic SSE proxy.

5. Source region `<JSON response to event stream converter>` contributed: cached JSON can be turned into tenant-visible event stream, proving cache-hit streaming is a distinct lifecycle with different source, pacing, headers, and audit semantics.

6. Source region `<retry and fallback orchestration>` contributed: retry/fallback are configured at request-attempt level; once a live stream is returned, per-chunk retry is not proven and public source-indexed docs state mid-stream retry is not replayed.

7. Source region `<request timeout docs and timeout path>` contributed: operator can configure request timeout, but public docs caveat that streaming timeout does not fire if at least one chunk arrives before the specified duration; this drives HUAKAI's separate idle and total stream timers.

8. Source region `<automatic retry docs>` contributed: retry count, status-code triggers, retry-after behavior, exponential backoff, cumulative cap, and response header signal; HUAKAI maps these to pre-output retry only by default.

9. Source region `<Responses API docs>` contributed: product-level claim that Responses API works with Configs, Caching, Guardrails, and Observability; HUAKAI resolves the source/docs drift by making stream accumulator and policy phases explicit.

10. Local HUAKAI region `<F-GW-002 released spec and cross-cutting synthesis>` contributed: Tx1/Tx2 settlement, usage source taxonomy, bounded drain, timeout axes, and AT-GW-002 acceptance test spine that this Portkey-specific decomposition must align with.

## 11. Round-2 critic-finding addressed table

| Critic finding ID | This round's status | Where addressed in this file |
|---|---|---|
| C-001 | CONFIRMED | §1, §2.2 S-2, §6 |
| C-002 | CONFIRMED | §1, §2.2 S-4, §5 |
| C-003 | CONFIRMED | §2.2 S-11, §4 F-4, §5 |
| C-004 | CONFIRMED | §2.2 S-8/S-9, §2-bis, §5 |
| C-005 | CONFIRMED | §2.2 S-7, §4 F-9, §6 |
| C-006 | CONFIRMED | §2.2 S-10, §2-bis partial-failure, §4 F-7 |
| C-007 | CONFIRMED | §2.2 S-6, §4 F-10, §10 |
| C-008 | CONFIRMED | §2.2 S-16, §3, §5 |
| F-001 | CONFIRMED | §2.1 Phase B, §2.2 S-5/S-6/S-7 |
| F-002 | CONFIRMED | §1, §2.2 S-4, §5 |
| F-003 | CONFIRMED | §2.2 S-11, §4 F-4 |
| F-004 | CONFIRMED | §2.1, §2.2 S-5/S-6/S-7, §10 |
| F-005 | CONFIRMED | §2.2 S-7, §4 F-9, §6 |
| D-001 | CONFIRMED | §1, §2.2 S-16, §10 |
| D-002 | CONFIRMED | §2.2 S-5/S-6/S-7, §10 |
| D-003 | CONFIRMED | §1, §2.2 S-9, §5 |
| D-004 | CONFIRMED | §2.2 S-7, §4 F-9 |
| N-001 | CONFIRMED / AVOID | §6 |
| N-002 | CONFIRMED / AVOID | §6 |
| N-003 | CONFIRMED / AVOID | §3, §6 |
| N-004 | CONFIRMED / AVOID | §6 |
| N-005 | CONFIRMED / AVOID | §2.2 S-7, §6 |
| N-006 | CONFIRMED / AVOID | §2.2 S-6, §6 |
| S-001 | CONFIRMED | §2.2 S-2, §6 |
| S-002 | CONFIRMED | §1, §2.2 S-4, §5 |
| S-003 | CONFIRMED | §1, §6 |
| S-004 | CONFIRMED | §2.2 S-16, §3 |
| S-005 | CONFIRMED | §2.2 S-7, §6 |
| S-006 | CONFIRMED | §2.1, §2.2, §4 |

Owner 中文总结：本轮把 portkey 的 streaming handler 从 Round 1 的浅层“转发 SSE”扩展为 2500+ 词级别的深拆：按生命周期、16 个子行为、3 条请求链路、完整输入/状态/持久化边界、12 类失败模式、HUAKAI DR-001/DR-002/DR-006 风险和 10 个 source coverage 区域逐项拆开；critic 的 29 条 finding 均已在正文确认并在 §11 定位，没有选择性遗漏；与浅版的关键差异是明确了 policy-warning stream、live-body reuse、cache-hit stream reconstruction、binary stream normalization、idle timeout、client disconnect drain、mid-stream retry suppression 和 partial usage Tx2 reconciliation；HUAKAI 应吸收的是“协议感知 streaming + durable usage reconciliation”的能力，避免照搬非标准状态码、fail-open guardrail、非租户化 cache replay 和内存态生命周期。
