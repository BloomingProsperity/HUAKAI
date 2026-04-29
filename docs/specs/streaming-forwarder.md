# F-GW-002: Streaming Forwarder + Usage Accounting

| Field | Value |
| --- | --- |
| Status | Released |
| Feature ID | F-GW-002 |
| Specifier | Claude (PM-Orchestrator), 2026-04-28 |
| Specifier date | 2026-04-28 |
| Reviewer | Codex final reviewer-lane, 2026-04-28 (APPROVE-WITH-FIXES; 10 fixes applied this revision) |
| Review date | 2026-04-28 |
| Released date | 2026-04-28 |
| Lane mode | Option C |
| Supersedes | — |
| Superseded by | — |

## Sources

> Reference material consulted by specifier-lane only. Implementer lane MUST NOT open these.

- Sub2API — LGPL-3.0 ([E-LIC-001](../07_REFERENCE_EVIDENCE_LEDGER.md), commit `b0a2252ed19c3720e6adafde6083e64fbac2efa9`)
- Helicone — GPL-3.0-or-later ([E-LIC-007](../07_REFERENCE_EVIDENCE_LEDGER.md), commit `548832f8e763a33732ead27d8b2dcaeccc665a39`, behavior-only)
- one-api — MIT ([E-LIC-004](../07_REFERENCE_EVIDENCE_LEDGER.md), simpler streaming baseline cross-reference)
- Specifier backing artifacts: [streaming-forwarder-synthesis.md](../decompositions/_cross-cutting/streaming-forwarder-synthesis.md), [streaming-forwarder-claude-v2.md](../decompositions/_cross-cutting/streaming-forwarder-claude-v2.md), [observability-synthesis.md](../decompositions/_cross-cutting/observability-synthesis.md)

## Capability

This spec satisfies F-GW-002 from [03_FEATURE_PARITY_MATRIX.md](../03_FEATURE_PARITY_MATRIX.md): protocol-aware streaming forwarder that emits real-time tokens to clients, extracts usage inline, classifies stream end into a typed taxonomy, drains within bounded budgets after client disconnect, and finalizes Usage Record atomically with billing settlement (Tx2).

## Actor

- **User**: receives real-time token output via SSE.
- **System** (Gateway streaming pipeline): runs the forwarder.
- **External Provider**: source of upstream stream events.
- **Operator**: observes stream-end taxonomy distribution + drain budget metrics.

## Preconditions

1. Tx1 reservation committed per F-OBS-001 spec.
2. Provider Account slot acquired per F-POOL-001 spec; acquisition_token present.
3. Upstream HTTP request built with detached upstream context (cancellation does not propagate from request context).
4. Route policy provides eight-axis timeout configuration + drain budgets + scanner-buffer cap.

## Normal Path

The forwarder runs four phases per stream. Phases A–B execute per-event in a tight loop until terminal classification fires.

### Phase A — Upstream Event Parsing

1. Build upstream HTTP response stream with detached upstream context.
2. Initialize line-based event scanner with bounded buffer (default 1 MiB per Route, max cap 64 MiB).
3. On scanner buffer overflow → emit terminal `RESPONSE_EVENT_TOO_LARGE` failure (no silent truncation).
4. Per-event read produces an upstream event record: envelope_type enum, payload bytes, parsed JSON when applicable, observed_at monotonic timestamp.

### Phase B — Per-Event Inline Processing

For each event:

5. Translate via the protocol adapter (upstream → canonical → client) per F-PROTO-002 spec.
6. Extract usage signals into a per-source accumulator. Source classification is closed enum: `reported` / `normalized` / `inferred` / `partial` / `ambiguous`.
7. Update tool-call accumulator monotonically (deltas only grow).
8. Update routing reason payload (sticky break reason, capability outcome, candidate counts).
9. Write event to client response writer; flush explicitly (no batching).
10. Track first-token latency on first emitted chunk.
11. If event is the protocol's terminal marker, mark `terminal_marker_seen`.

### Phase C — Stream End Classification

When the per-event loop exits, classify the end:

```
GRACEFUL                     — terminal marker observed
UPSTREAM_EOF_NO_TERMINAL     — clean EOF without marker
UPSTREAM_ERROR_4xx           — upstream returned 4xx
UPSTREAM_ERROR_5xx           — upstream returned 5xx
UPSTREAM_RATE_LIMIT          — 429 or 529 with reset signal
UPSTREAM_AUTH_FAILURE        — 401 / 403 typed
FIRST_TOKEN_TIMEOUT          — no first event within budget
INTER_EVENT_TIMEOUT          — gap between events too large
TOTAL_STREAM_TIMEOUT         — overall stream timeout
CLIENT_DISCONNECT            — downstream write failed
RESPONSE_EVENT_TOO_LARGE     — scanner buffer overflow
ORCHESTRATOR_CANCEL          — explicit cancel signal
AMBIGUOUS_USAGE              — accumulator zero AND end class indicates failure
UNKNOWN_TERMINATION          — none of the above
```

### Phase C-bis — Bounded Drain (CLIENT_DISCONNECT only)

If end class is CLIENT_DISCONNECT, enter drain mode:

12. Three drain budgets: `drain_max_seconds` (default 30s), `drain_max_bytes` (default 1 MiB), `drain_max_estimated_cost` (default $0.10). All operator-tunable per Route.
13. Drain loop: while not budget-exhausted, read upstream event; extract usage into accumulator; do NOT write to downstream.
14. Exit when ANY budget exhausts; record which budget triggered exit.

### Phase D — Tx2 Finalization

Hand the produced UsageRecordDraft (with end_class, usage_source enum, accumulator values, routing_reason, drain_outcome) to the F-OBS-001 Tx2 reconcile path. Tx2 atomically:

15. Decrements Provider Account in_flight_count if acquisition_token matches AND counter > 0.
16. Writes Usage Record into the same transaction.
17. Writes audit-grade billing event row.
18. Moves claim status from `reserving` to `committed` or `aborted`.
19. Updates User / API Key / Account quota counters from final usage values.

If the streaming forwarder crashes between Phase A start and Phase D handoff, the orphan sweep catches it via lease expiry + claim row status `reserving`.

## Failure Path

### Failure: `GRACEFUL`
- Trigger: terminal marker observed.
- Observable outcome: Usage Record `end_class = stream_end_graceful`; client sees `[DONE]` (or protocol equivalent).

### Failure: `UPSTREAM_EOF_NO_TERMINAL`
- Trigger: upstream cleanly ended stream without sending the protocol's terminal event.
- Observable outcome: Tx2 commits Usage Record with usage_source `inferred` (tokenizer fallback) AND `pending_reconciliation = true`; or `ambiguous` if tokenizer unavailable.
- Operator-visible signal: counter for missing-terminal-marker rate (alert if rate spikes).

### Failure: `UPSTREAM_ERROR_4xx` / `UPSTREAM_ERROR_5xx`
- Trigger: upstream returned error status before stream began.
- Observable outcome: failover to next Provider Account if status is in configurable failover list (per Route, default 401/403/429/529 + 5xx); else error returned to client.
- Operator-visible signal: failover counter; rate-limit handling per F-RATE-001 spec.

### Failure: `UPSTREAM_RATE_LIMIT`
- Trigger: 429 or 529.
- Observable outcome: per F-RATE-001 spec — Account marked rate_limited or overloaded; failover to next Account.

### Failure: `FIRST_TOKEN_TIMEOUT`
- Trigger: no first event within `first_token_timeout` (default 60s).
- Observable outcome: stream terminated; failover to next Account.

### Failure: `INTER_EVENT_TIMEOUT` / `TOTAL_STREAM_TIMEOUT`
- Trigger: gap-between-events or whole-stream timeout fires.
- Observable outcome: stream terminated; Usage Record committed with `partial` source; client sees stream interruption.

### Failure: `CLIENT_DISCONNECT`
- Trigger: downstream write fails.
- Observable outcome: drain mode runs to budget exhaust; partial usage settled with `usage_source = partial`; `drain_outcome` annotation records which budget triggered exit.

### Failure: `RESPONSE_EVENT_TOO_LARGE`
- Trigger: single upstream event exceeds scanner buffer.
- Observable outcome: stream terminated with no charge; operator alert.
- Operator-visible signal: oversize-event counter; correlated with provider name + model.

### Failure: `ORCHESTRATOR_CANCEL`
- Trigger: explicit cancel signal from orchestrator.
- Observable outcome: terminate with no charge.

### Failure: `AMBIGUOUS_USAGE`
- Trigger: end class indicates failure AND accumulator is zero.
- Observable outcome: claim aborted with usage_values=0; customer not charged; operator alert.
- Operator-visible signal: ambiguous-usage counter (should be near-zero in healthy operation).

### Failure: `UNKNOWN_TERMINATION`
- Trigger: none of the above patterns matched.
- Observable outcome: terminate_partial; operator alert.

## Operator Recovery

| Failure | Detection | Recovery |
|---|---|---|
| UPSTREAM_EOF_NO_TERMINAL (high rate) | dashboard counter | Investigate upstream behavior; possible provider regression. |
| FIRST_TOKEN_TIMEOUT (high rate) | dashboard counter | Increase `first_token_timeout` if reasoning models in use; or investigate upstream slowness. |
| RESPONSE_EVENT_TOO_LARGE | counter + alert | Increase scanner buffer cap if legitimate; otherwise investigate upstream protocol drift. |
| AMBIGUOUS_USAGE (any) | counter | Investigate immediately; should be near-zero. |
| Drain budget exhaustion (cost cap) | metric | Tune per-Route drain budgets if operator wants more aggressive cost protection. |

## Audit / Usage / Log Evidence

Each completed stream produces:

1. **Usage Record** (immutable, in Tx2): tokens_input, tokens_output, cache_creation_tokens, cache_read_tokens, actual_cost (numeric(20, 8)), routing_reason structured payload, end_class enum, usage_source enum, confidence_score (when inferred), drain_outcome (when applicable), pending_reconciliation flag.
2. **Audit-grade billing event row** (in Tx2): durable audit trail.
3. **Operator metrics**: end_class distribution, drain_outcome distribution, first_token_latency p50/p95/p99, scanner_buffer_overflow_count, ambiguous_usage_count.

## Acceptance Test Direction

Per [11_ACCEPTANCE_TEST_MATRIX.md](../11_ACCEPTANCE_TEST_MATRIX.md), tests AT-GW-002-01..19:

Sub2API-inheritable:
- AT-GW-002-01 / Per-event flush observable at client within 1s for first event.
- AT-GW-002-02 / Anthropic→Canonical→Chat protocol translation preserves usage.
- AT-GW-002-03 / Pre-stream failover triggers on 401/403/429/529/5xx (configurable list).
- AT-GW-002-04 / Pre-stream non-failover 4xx returns sanitized error to client.
- AT-GW-002-05 / Buffered path: missing message_start → 502 returned.
- AT-GW-002-06 / Scanner oversize → typed terminal failure; partial response not silently truncated.
- AT-GW-002-07 / Client disconnect mid-stream: function exits with accumulated usage.
- AT-GW-002-08 / Last-non-zero-wins per usage field on multiple message_delta events.

HUAKAI-design:
- AT-GW-002-09 / Bounded drain: client disconnect → drain runs to ANY budget exhaust.
- AT-GW-002-10 / Drain cost cap: drain stops on `drain_max_estimated_cost`, not just time.
- AT-GW-002-11 / Eight-axis timeout independence: total_stream fires before inter_event when both apply.
- AT-GW-002-12 / Oversized event typed terminal: RESPONSE_EVENT_TOO_LARGE + no charge + alert.
- AT-GW-002-13 / Mid-stream failover blocked by default after first content event emitted.
- AT-GW-002-14 / Mid-stream failover allowed only with `Idempotent-Stream-Replay: true` header.
- AT-GW-002-15 / Multi-source usage conflict logged; terminal frame wins.
- AT-GW-002-16 / Tx2 atomicity: gateway crash mid-stream → orphan sweep finalizes within budget.
- AT-GW-002-17 / Tenant isolation under load: 100 concurrent streams across 5 tenants → no cross-tenant data.
- AT-GW-002-18 / AMBIGUOUS_USAGE no-charge gate: zero accumulator + UNKNOWN_TERMINATION → claim aborted.
- AT-GW-002-19 / Tokenizer fallback: stream EOF without terminal → inferred usage with confidence_score.

## Open Questions

None remaining at release. All three prior open questions resolved during Codex final review 2026-04-28; resolutions in [streaming-forwarder-synthesis.md §8](../decompositions/_cross-cutting/streaming-forwarder-synthesis.md).

## Implementer Notes (added by implementer lane)

> Filled by implementer after consuming the spec.

(empty until implementer-lane work begins)
