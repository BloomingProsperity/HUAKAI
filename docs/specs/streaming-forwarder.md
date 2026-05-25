# F-GW-002: Streaming Forwarder + Usage Accounting

| Field | Value |
| --- | --- |
| Status | Released — Extended by A12a/A25/A26/A27 (DR-009 2026-05-02) |
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

---

## A12a Stream-Safe Retry Boundary FSM (DR-009 Phase B, P0, 硬底线)

> Authority: synthesis §2 A12a + §6 Q8 + §6.6 硬底线; DR-009 Q8 决议 + 客户响应头清单.

### Purpose

Prevent double-charge disputes and duplicate-content complaints by enforcing a strict FSM gate before any mid-stream retry or account failover is attempted. Once content bytes have been flushed to the client, a retry is unsafe by default.

### FSM States

| State | Entry Condition |
|---|---|
| `BEFORE_UPSTREAM` | Request received; upstream HTTP connection not yet opened. |
| `BEFORE_FIRST_TOKEN` | Upstream connection open; upstream response status 2xx received; no content event emitted to client yet. |
| `CONTENT_STARTED` | First content token (text chunk or partial tool-call delta) has been flushed to the client downstream writer. |
| `TOOL_SIDE_EFFECT_STARTED` | A tool-call event with side-effect semantics has been emitted (e.g. `tool_use` block with `id` present). |
| `TERMINAL` | Stream classified (any `StreamEndClass`); FSM frozen. |

State advances monotonically: `BEFORE_UPSTREAM` → `BEFORE_FIRST_TOKEN` → `CONTENT_STARTED` → `TOOL_SIDE_EFFECT_STARTED` → `TERMINAL`. Transitions are one-way and irreversible within a single stream lifecycle.

### Retry-Allowed Matrix

| FSM State at retry decision point | Default retry_allowed | With `Idempotent-Stream-Replay: true` |
|---|---|---|
| `BEFORE_UPSTREAM` | **YES** | YES |
| `BEFORE_FIRST_TOKEN` | **YES** | YES |
| `CONTENT_STARTED` | **NO** | **YES** (client opts in; self-bears risk) |
| `TOOL_SIDE_EFFECT_STARTED` | **NO** | **NO** (side effects cannot be replayed safely regardless of header) |
| `TERMINAL` | **NO** | NO |

### Q8 Client Opt-In: `Idempotent-Stream-Replay: true`

Per DR-009 Q8 (synthesis §6 Q8, Owner选项 B): clients who control their own idempotency layer may send the request header `Idempotent-Stream-Replay: true`. This header:

- Lifts the retry block in `CONTENT_STARTED` state only.
- Has no effect in `TOOL_SIDE_EFFECT_STARTED` or `TERMINAL` states (always blocked).
- Is the client's explicit acknowledgment that duplicate stream content is acceptable to them.
- Must be logged in the attempt audit row for downstream billing reconciliation.

### Response Header: `X-Huakai-Stream-Boundary` (debug mode only)

When the gateway operates in debug mode (`X-Huakai-Debug: true` request header or operator route flag), and A12a blocks a retry, the response includes:

```
X-Huakai-Stream-Boundary: <state>
```

where `<state>` is one of `BEFORE_FIRST_TOKEN`, `CONTENT_STARTED`, or `TOOL_SIDE_EFFECT_STARTED`. This header is suppressed in production mode to avoid leaking internal state. DR-009 客户响应头清单 entry: `X-Huakai-Stream-Boundary`.

### Acceptance Tests

- **AT-GW-002-021** — FSM blocks retry at `CONTENT_STARTED` by default; response contains no duplicate prefix bytes at client.
- **AT-GW-002-022** — `Idempotent-Stream-Replay: true` permits retry at `CONTENT_STARTED`; audit row records header presence; attempt count incremented.
- **AT-GW-002-023** — `TOOL_SIDE_EFFECT_STARTED` blocks retry unconditionally regardless of `Idempotent-Stream-Replay` header value.

---

## A25 Adaptive Stream Buffer Controller (DR-009 Phase C, P0)

> Authority: synthesis §1 A25 (Codex memory_pressure signal + Claude AIMD boundary); DR-009 Phase C.

### Purpose

Dynamically size the per-stream upstream event scanner buffer based on observed tail latency and runtime memory pressure, replacing the static 1 MiB default with a buffer that self-tunes per (provider, model, event_class) sketch.

### Algorithm

```python
def compute_buffer_cap(provider: str, model: str, event_class: str,
                        memory_pressure: float,  # 0.0–1.0; 1.0 = OOM imminent
                        global_cap_bytes: int = 64 * 1024 * 1024) -> int:
    sketch_key = (provider, model, event_class)
    p99_bytes = sketch_get_p99(sketch_key)          # from rolling t-digest sketch
    if p99_bytes is None:
        p99_bytes = 1 * 1024 * 1024                 # cold-start default: 1 MiB

    target = p99_bytes * 4                           # 4× P99 headroom

    # AIMD memory-pressure clamp (synthesis Claude boundary)
    pressure_factor = max(0.25, 1.0 - memory_pressure)
    target = int(target * pressure_factor)

    # Hard caps
    target = max(target, 64 * 1024)                 # floor: 64 KiB
    target = min(target, global_cap_bytes)           # ceiling: operator-configured global cap

    return target
```

### Sketch Maintenance

- One t-digest sketch per `(provider, model, event_class)` triple; event_class values: `text_chunk`, `tool_delta`, `usage_event`, `control_frame`.
- Sketch updated on every completed stream: observed max single-event byte size fed as sample.
- Sketch TTL: 24 hours rolling; evicted if (provider, model) combo unseen for 7 days.
- `memory_pressure` signal sourced from the process/container memory monitor at buffer-allocation time, not at stream start.

### Acceptance Tests

- **AT-GW-002-024** — Buffer cap for a (provider, model, event_class) with observed P99 = 200 KiB computes to ≤ 800 KiB under zero memory pressure and ≤ 200 KiB under 75% memory pressure; overflow still emits typed `RESPONSE_EVENT_TOO_LARGE` terminal.

---

## A26 Expected-Value Drain Decision (DR-009 Phase C, P0, 硬底线)

> Authority: synthesis §1 A26 (Codex forensic_value + incident_probability + Claude three-budget early-stop) + §6.6 硬底线 + §6 Q5 drain privacy; DR-009 硬底线.

### Purpose

Replace the unconditional CLIENT_DISCONNECT drain (Phase C-bis) with an expected-value gate: drain only when E[value_remaining] > E[cost_to_drain]. Default is greedy-drain (billing capture); the budget caps act as anti-abuse stoppers, not the primary decision.

### Formula

```
E[value_remaining] = tokens_remaining_estimate
                     × price_per_output_token
                     × (forensic_value_weight + incident_probability_weight)

E[cost_to_drain]   = tokens_remaining_estimate
                     × cost_per_output_token_to_gateway
                     + drain_overhead_fixed_usd

drain_decision = DRAIN  if E[value_remaining] > E[cost_to_drain]
                 ABORT  otherwise
```

Where:
- `tokens_remaining_estimate`: derived from `reported` usage accumulator if available; else tokenizer estimate on bytes-remaining. **Never derived from prompt body content** (Q5 privacy boundary — see below).
- `forensic_value_weight`: operator-configured weight (default 1.0) reflecting value of capturing usage for audit/billing purposes.
- `incident_probability_weight`: operator-configured weight (default 0.1) reflecting probability the stream is part of an ongoing incident requiring forensic data.
- `cost_per_output_token_to_gateway`: from the versioned pricing snapshot (A15) for this provider/model.
- `drain_overhead_fixed_usd`: fixed per-drain operational cost estimate (default $0.0001).

### Q5 Privacy Boundary

Per DR-009 Q5 (Owner option A): the drain decision function reads **only token usage metadata** — accumulator values, byte counts, pricing snapshots. It must not read, parse, or hash the prompt body or any partial response content. This constraint applies to all sub-functions including `tokens_remaining_estimate`.

### Three Budget Caps (Hard Stoppers)

Even when the expected-value formula returns DRAIN, three budget caps provide unconditional exit gates:

| Budget | Default | Operator tunable |
|---|---|---|
| `drain_max_seconds` | 30s | Per-Route |
| `drain_max_bytes` | 1 MiB | Per-Route |
| `drain_max_estimated_cost` | $0.10 | Per-Route |

Exit triggers when ANY single budget exhausts. `drain_outcome` annotation records which budget fired. These caps exist in Phase C-bis of the Normal Path and remain unchanged; this section defines the pre-gate that runs before the drain loop begins.

### Acceptance Tests

- **AT-GW-002-025** — CLIENT_DISCONNECT with E[value_remaining] > E[cost_to_drain]: drain loop runs; usage captured; Tx2 commits with `usage_source = partial`.
- **AT-GW-002-026** — CLIENT_DISCONNECT with E[value_remaining] ≤ E[cost_to_drain] (e.g. upstream nearly complete, cost exceeds value): drain loop skipped; Tx2 commits immediately with available accumulator values; `drain_outcome = ev_abort`.

---

## A27 Stream-Time Dynamic Reserve Adjustment (DR-009 Phase E, P2)

> Authority: synthesis §3 gaps table (Claude A27, adopted as new ID) + §5 domain 4 A27; DR-009 Phase E.

### Purpose

For long-running streams where token generation may exceed the Tx1 reservation ceiling, periodically check whether the running cost is approaching the reserved amount and attempt to extend the reservation inline, or terminate gracefully before the quota is hard-exceeded.

### Algorithm

```python
RESERVE_CHECK_INTERVAL_TOKENS = 100   # check every N output tokens emitted

def on_token_emitted(stream_ctx: StreamContext) -> None:
    stream_ctx.tokens_emitted_since_last_check += 1
    if stream_ctx.tokens_emitted_since_last_check < RESERVE_CHECK_INTERVAL_TOKENS:
        return

    stream_ctx.tokens_emitted_since_last_check = 0
    running_cost_usd = estimate_cost(
        stream_ctx.usage_accumulator,
        stream_ctx.pricing_snapshot,
    )
    reserve_remaining_usd = (
        stream_ctx.tx1_reserved_usd - running_cost_usd
    )

    if reserve_remaining_usd > stream_ctx.reserve_low_water_usd:
        return  # ample headroom; no action

    # Approaching ceiling — attempt to extend reservation
    extended = try_extend_reserve(
        claim_id=stream_ctx.claim_id,
        additional_usd=stream_ctx.reserve_extension_usd,
    )

    if extended:
        stream_ctx.tx1_reserved_usd += stream_ctx.reserve_extension_usd
        log_metric("stream_reserve_extended", claim_id=stream_ctx.claim_id)
        return

    # Extension failed (quota exhausted or binding limit reached) — soft terminate
    soft_terminate(stream_ctx, reason="quota_exhausted")
```

### Parameters

| Parameter | Default | Description |
|---|---|---|
| `RESERVE_CHECK_INTERVAL_TOKENS` | 100 | Output tokens between reserve checks |
| `reserve_low_water_usd` | $0.05 | Remaining reserve threshold that triggers extension attempt |
| `reserve_extension_usd` | $0.50 | Amount requested per extension attempt |

### `soft_terminate` Behavior

`soft_terminate(reason="quota_exhausted")` closes the upstream connection cleanly, emits a protocol-level termination marker if possible (so client sees a well-formed stream end), sets `StreamEndClass = ORCHESTRATOR_CANCEL` with `cancel_reason = quota_exhausted`, and proceeds to Phase D (Tx2 finalization) with accumulated usage values. No content is truncated mid-token.

### Acceptance Tests

- **AT-GW-002-027** — Stream emitting tokens beyond Tx1 reservation: reserve check fires at N-token boundary; `try_extend_reserve` succeeds → stream continues; OR `try_extend_reserve` fails → `soft_terminate("quota_exhausted")` fires; Tx2 commits with partial usage; client stream closes with valid terminal marker.
