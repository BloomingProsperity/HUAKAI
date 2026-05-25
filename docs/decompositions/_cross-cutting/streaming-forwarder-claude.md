# Streaming Forwarder + Usage Accounting — Claude's Independent Pass

> ⚠️ **WITHDRAWN — SUPERSEDED BY [streaming-forwarder-claude-v2.md](streaming-forwarder-claude-v2.md)** (2026-04-28).
> This v1 file was paraphrased from prior prose decompositions, NOT from direct source reading. Multiple claims are hallucinated: 1 MiB scanner buffer (real default is 500 MiB), bounded post-disconnect drain budget (no drain exists in source), eight-axis timeout policy (only one timeout config knob exists), usage source taxonomy (real merge is last-non-zero-wins), Idempotent-Stream-Replay header (doesn't exist), AMBIGUOUS_USAGE no-charge gate (doesn't exist). See [docs/process/reviews/2026-04-28-source-truth-corrections.md](../../process/reviews/2026-04-28-source-truth-corrections.md) for full catalogue. Read v2 for the source-verified version.

| Field | Value |
| --- | --- |
| Status | **WITHDRAWN** (2026-04-28) — see banner above |
| Author | Claude (PM-Orchestrator), specifier lane |
| Date | 2026-04-28 |
| Lane | Specifier — Option C strict spec input per [DR-000](../../process/decisions/DR-000-clean-room-methodology.md) carve-out for F-GW-002 |
| Feature | [F-GW-002](../../03_FEATURE_PARITY_MATRIX.md) (L1 MVP) — protocol-aware streaming forwarder + inline usage extraction + Usage Record finalization inside Tx2 |
| Mutual review | This file is authored independently of Codex's parallel specifier pass per Owner directive 2026-04-28. Codex's parallel pass lives at [streaming-forwarder-codex.md](streaming-forwarder-codex.md). Synthesis follows after both are complete. |
| Becomes | After mutual review + reviewer-lane CL-001..010 sign-off, the synthesized version moves to `docs/specs/streaming-forwarder.md` Status=Released. |
| Source basis | [sub2api/streaming-forwarder.md](../sub2api/streaming-forwarder.md), evidence rows E-S2A-PROXY-018..026 (per [07_REFERENCE_EVIDENCE_LEDGER.md](../../07_REFERENCE_EVIDENCE_LEDGER.md)), [quota-billing-claim-gate-synthesis.md](quota-billing-claim-gate-synthesis.md) (Tx2 reconcile boundary), and [pool-selection-synthesis.md](pool-selection-synthesis.md) (slot release coupled to Tx2). |

## 1. Why Streaming Is The Highest-Stakes Hot Path

Owner directive 2026-04-28: "最主要的核心是 sub2api 里面的网关以及反代核心算法". Streaming is where the relay-station product simultaneously delivers UX *and* settles money:

- **UX surface**: token-by-token output is the user-visible product. Buffer-to-end breaks the experience.
- **Money settlement**: usage data arrives inline (mid-stream telemetry, terminal frame, or accumulated deltas). Misclassify the source → wrong charge.
- **Failure surface**: client disconnect, slow client, missing terminal marker, mid-stream rate-limit, oversized event, multi-source usage conflict — every one is a potential billing leak or stuck connection.
- **Cross-protocol**: OpenAI SSE, Anthropic SSE, Gemini chunked-JSON, raw-chunk fallback. A protocol-blind byte pipe loses usage and breaks event boundaries.

This is L1 MVP because Owner's Model-1 commercial launch sells streaming completion access. A mid-stream billing inaccuracy is a customer dispute; a stuck stream after disconnect is operator overspend.

## 2. Algorithm — Four-Phase Streaming Pipeline

The forwarder runs as a coroutine with four logical phases. Phases A and B execute per-event in a tight loop until terminal; phases C and D execute on stream end (graceful or otherwise).

### Phase A — Protocol-Aware Event Parsing

```
choose_parser(upstream_response_headers, upstream_protocol_hint) -> Parser
  if Content-Type matches text/event-stream:        return SSEParser
  if Content-Type matches application/x-ndjson
     OR provider hint = "gemini":                   return ChunkedJSONParser
  if Transfer-Encoding = chunked AND no SSE marker: return RawChunkParser
  default:                                          return RawChunkParser_with_warning
```

The chosen parser produces a stream of `Event` records:

```
Event {
  envelope_type:  data | event | id | retry | comment | terminal_marker
  payload_bytes:  []byte
  parsed_json:    Option<JSON>
  observed_at:    monotonic_ts
}
```

**Constraint**: parsing reads upstream byte-by-byte (or line-by-line for SSE) into a bounded scanner buffer. Buffer size is per-Route policy, default 1 MiB. If a single event exceeds the buffer, the forwarder emits a terminal `RESPONSE_EVENT_TOO_LARGE` failure rather than truncating silently — closes the gap from upstream Sub2API where oversized events have undefined behavior.

### Phase B — Per-Event Inline Processing

For each event, three responsibilities run **inline before the next event is read**:

```
process_event(event, ctx):
  // 1. Rewrite — translate fields between provider formats
  rewritten = rewrite_for_downstream_protocol(event, ctx.client_protocol)

  // 2. Extract — pull usage / tool-call / cache-hit values
  if rewritten.parsed_json contains usage block:
      ctx.usage_accumulator.merge_from_event(rewritten, source_label)
  if rewritten.parsed_json contains tool_call delta:
      ctx.tool_call_accumulator.merge(rewritten)
  if rewritten.parsed_json contains content delta:
      ctx.content_length_accumulator += len(delta)
      ctx.content_event_count += 1

  // 3. Re-emit — write to downstream and FLUSH explicitly
  write_to_response_writer(rewritten)
  flush_response_writer()    // explicit flush; client sees event in real time

  // 4. Detect terminal marker
  if rewritten.envelope_type == terminal_marker:
      ctx.terminal_marker_seen = true
      return TerminalReached
```

**Constraint**: rewriting must preserve semantic equivalence across protocols. If the downstream client requested OpenAI SSE but upstream is Anthropic SSE, role / tool-call / usage envelope translation happens here. Rewriting is **never lossy** for usage data; lossy translation of optional fields is permitted but recorded in `rewrite_log` for the Usage Record.

**Constraint**: explicit flush after every event. No batching for "efficiency." Default Go `http.ResponseWriter` requires `.Flush()` calls; buffer-and-batch optimizations are forbidden because they break perceived latency and break SSE semantics.

### Phase C — Stream End Detection and Classification

```
classify_end(ctx) -> EndClass
  if ctx.terminal_marker_seen:           return GRACEFUL
  if ctx.upstream_eof_clean:             return UPSTREAM_EOF_NO_TERMINAL
  if ctx.upstream_error != nil:          return UPSTREAM_ERROR(error)
  if ctx.first_token_timeout:            return FIRST_TOKEN_TIMEOUT
  if ctx.inter_event_timeout:            return INTER_EVENT_TIMEOUT
  if ctx.total_stream_timeout:           return TOTAL_STREAM_TIMEOUT
  if ctx.downstream_write_error:         return CLIENT_DISCONNECT
  if ctx.cancelled_by_orchestrator:      return ORCHESTRATOR_CANCEL
  default:                               return UNKNOWN_TERMINATION
```

For `CLIENT_DISCONNECT` only, the forwarder enters Drain Mode (Phase C-bis) before proceeding to Phase D.

### Phase C-bis — Bounded Post-Disconnect Drain

When the client disconnects but upstream is still emitting tokens, raw close-the-upstream-now produces a billing leak: the operator paid for tokens already in flight. Sub2API drains forever; HUAKAI bounds the drain.

```
drain_after_disconnect(ctx):
  budget = ctx.route_policy.drain_budget
  drain_started_at = now()
  while upstream_has_more():
      if elapsed_since(drain_started_at) >= budget.max_seconds:    break
      if ctx.bytes_drained_after_disconnect >= budget.max_bytes:   break
      if ctx.usage_accumulator.estimated_cost_so_far >= budget.max_estimated_cost: break
      event = upstream.read_next_event_with_timeout(budget.inter_event_timeout)
      if event == nil OR event.is_error: break
      // No re-emit; client is gone. Only extract.
      process_event_extract_only(event, ctx)
  ctx.disconnect_reason = "client_disconnect"
  ctx.drain_outcome = (which budget exhausted)
```

Default budgets: `max_seconds = 30s`, `max_bytes = 1 MiB`, `max_estimated_cost = $0.10` (per-tenant operator-tunable). Any budget exhaustion stops drain immediately and the partial usage settles in Phase D.

### Phase D — Usage Record Finalization Inside Tx2

This is where streaming meets Quota+Billing reconcile. Per [quota-billing-claim-gate-synthesis.md](quota-billing-claim-gate-synthesis.md) the Usage Record is finalized **inside** Tx2; the streaming forwarder produces a `usage_record_draft` and hands it to the Quota+Billing reconcile path.

```
finalize_to_tx2(ctx, end_class):
  draft = UsageRecordDraft {
    tenant_id:                  ctx.tenant_id
    claim_id:                   ctx.claim_id
    provider_account_id:        ctx.acquired_account_id   // from Pool selection Phase B
    acquisition_token:          ctx.acquisition_token
    request_id:                 ctx.logical_request_id
    attempt_seq:                ctx.attempt_seq
    end_class:                  end_class
    usage_source:               select_usage_source(ctx)  // see §3
    usage_values:               ctx.usage_accumulator.snapshot()
    usage_confidence:           if usage_source == "inferred" then ctx.inferred_confidence else null
    pending_reconciliation:     usage_source ∈ {"inferred", "partial"}
    rewrite_log:                ctx.rewrite_log
    drain_outcome:              ctx.drain_outcome
    routing_reason:             ctx.routing_reason  // already populated by Pool selection
    timestamps:                 { upstream_request_at, first_byte_at, first_event_at, last_event_at, terminal_at }
  }
  hand_off_to_tx2(draft)        // Tx2 commits Usage Record + slot release + claim status atomically
```

Tx2's atomic transaction handles: (a) decrement Provider Account `in_flight_count` if `acquisition_token` matches, (b) write Usage Record, (c) move Billing Ledger claim to `committed` or `aborted` status, (d) update User / API Key / Account quota counters from `usage_values`, (e) emit Audit Event.

If the streaming forwarder crashes between Phase A start and Phase D handoff, the orphan sweep catches it via lease expiry on the Pool slot AND the claim row's `status = reserving`.

## 3. Usage Source Taxonomy + Reconciliation

When a single stream produces usage data from multiple sources, the source label is the auditable record of *how* the charge was determined. Four sources, ranked by trust:

| Source | Trust | Definition | Example |
|--------|-------|------------|---------|
| `reported` | highest | Upstream's terminal usage frame, delivered cleanly before stream end | OpenAI's final `{usage: {prompt_tokens, completion_tokens}}` frame |
| `normalized` | high | Reported, but with units / fields translated across provider format | Anthropic `input_tokens` mapped to OpenAI `prompt_tokens` |
| `inferred` | medium | Tokenizer-based estimate when no reported frame arrived | Tiktoken count of accumulated content + system prompt |
| `partial` | low | Stream ended mid-flight; usage covers only what was observed | Client disconnect at 50% of expected output |

**`select_usage_source`** decision tree:
```
if terminal_marker_seen AND ctx.usage_accumulator.has_terminal_frame:
    if frame.protocol_matches_upstream:                   return "reported"
    else:                                                  return "normalized"
elif end_class == GRACEFUL AND ctx.tokenizer_available:    return "inferred"
elif end_class == CLIENT_DISCONNECT:                       return "partial"
elif end_class ∈ {UPSTREAM_ERROR, *_TIMEOUT}:
    if ctx.usage_accumulator.has_any_data:                 return "partial"
    else:                                                  return "ambiguous"
else:                                                      return "ambiguous"
```

`ambiguous` is a fifth implicit value: the gateway cannot determine usage even partially. Customer is **not charged** for an ambiguous request; operator is alerted (this is HUAKAI's improvement over Sub2API which leaves billing in a quiet `incomplete` flag).

**Multi-source reconciliation rule** (when same request gets data from multiple sources mid-stream):
1. Higher-trust always overrides lower-trust on field-level merge: `reported.prompt_tokens > normalized.prompt_tokens > inferred.prompt_tokens > partial.prompt_tokens`.
2. If two sources report conflicting values at the same trust level (e.g. mid-stream cache-hit telemetry says 100 prompt tokens but terminal frame says 120), the **terminal frame wins** and the conflict is recorded in `rewrite_log` for operator review.
3. Tool-call accumulator is independent of token usage and merged from delta events monotonically (tool-call deltas only grow, never shrink).

## 4. Eight-Axis Timeout Policy

Sub2API has one global "stream timeout" — Codex evidence row E-S2A-PROXY-019 IMPROVE flagged this as too coarse. HUAKAI splits into eight Route policy fields:

| Timeout | Default | Triggered when | Action |
|---------|---------|----------------|--------|
| `connect_timeout` | 5s | TCP connect to upstream takes too long | Pre-stream failure; Phase A never starts |
| `tls_handshake_timeout` | 5s | TLS negotiation stalls | Pre-stream failure |
| `request_write_timeout` | 30s | Cannot write request body to upstream | Pre-stream failure |
| `response_header_timeout` | 30s | Upstream takes too long to send response headers | Pre-stream failure |
| `first_token_timeout` | 60s | Upstream sends headers but no first event | Phase A → end_class = FIRST_TOKEN_TIMEOUT |
| `inter_event_timeout` | 30s | Gap between consecutive upstream events too large | Phase A → end_class = INTER_EVENT_TIMEOUT |
| `total_stream_timeout` | 600s | Whole stream too long even if events flow | Phase A → end_class = TOTAL_STREAM_TIMEOUT |
| `downstream_write_timeout` | 10s | Cannot flush event to downstream client | Phase B → end_class = CLIENT_DISCONNECT |

All eight are operator-tunable per Route, per Pool, with bounded ranges. Sane defaults are chosen for OpenAI-class providers; reasoning models (o1-style) need higher `first_token_timeout` and `total_stream_timeout`, configurable per Route policy.

## 5. Concurrency Invariants

| # | Invariant | Enforcement |
|---|-----------|-------------|
| S1 | Every Acquired slot is released exactly once via Tx2 (graceful end) OR orphan sweep (crash). | Acquisition token + idempotent decrement (mirrors Pool I5). |
| S2 | Usage Record is never written outside Tx2. | Phase D produces draft only; commit happens in Tx2. |
| S3 | Buffer-to-end is forbidden; every event flushes downstream before next read. | Inline pipeline; no batching layer permitted. |
| S4 | Drain Mode never re-emits to downstream. | `process_event_extract_only` has no write path. |
| S5 | Drain budgets are checked before every upstream read in Drain Mode. | Loop guard at start of each iteration. |
| S6 | Usage source taxonomy is closed enum {`reported`, `normalized`, `inferred`, `partial`, `ambiguous`}. | `select_usage_source` returns from this enum only. |
| S7 | `ambiguous` source produces zero customer charge AND operator alert. | Tx2 charge gate checks source; alert path always fires for ambiguous. |
| S8 | Per-Route timeout fields are independent; no global timeout overrides them. | Route policy validation rejects "global timeout" config keys. |
| S9 | Tenant isolation: every event-loop variable scoped to tenant_id; downstream writer cannot serve cross-tenant data. | Forwarder allocates per-request, never per-process; tenant_id passed through every internal call. |
| S10 | Mid-stream failover after client-visible output is forbidden by default. | Orchestrator failover hook refuses to switch Account once `content_event_count > 0`, unless client opted in via `Idempotent-Stream-Replay: true` header. |
| S11 | Oversized event triggers terminal `RESPONSE_EVENT_TOO_LARGE`, never truncation. | Scanner buffer overflow path explicit in Phase A. |
| S12 | Multi-source usage conflict is logged, never silently overwritten. | Reconciliation rule §3.2 records to `rewrite_log`. |

## 6. Failure Taxonomy

Maps to `recovery_policy` enum used by orchestrator's retry path. Aligned with [pool-selection-synthesis.md](pool-selection-synthesis.md) failure taxonomy where overlap exists.

| Reason | Recovery Policy | Usage Record annotation |
|--------|-----------------|-------------------------|
| `GRACEFUL` | none | `stream_end_graceful` |
| `UPSTREAM_EOF_NO_TERMINAL` | retry_if_idempotent + alert_operator | `stream_end_no_terminal_marker` |
| `UPSTREAM_ERROR_4xx` | classify_and_retry_per_status | `upstream_error_<status>` |
| `UPSTREAM_ERROR_5xx` | retry_with_backoff | `upstream_error_<status>` |
| `UPSTREAM_RATE_LIMIT` | retry_after_header_or_default + cooldown_account | `upstream_rate_limit` |
| `UPSTREAM_AUTH_FAILURE` | alert_operator + cool_down_credential | `upstream_auth_failure` |
| `FIRST_TOKEN_TIMEOUT` | retry_with_different_account | `first_token_timeout_<seconds>` |
| `INTER_EVENT_TIMEOUT` | terminate_partial | `inter_event_timeout` |
| `TOTAL_STREAM_TIMEOUT` | terminate_partial | `total_stream_timeout` |
| `CLIENT_DISCONNECT` | drain_then_settle_partial | `client_disconnect_<drain_outcome>` |
| `RESPONSE_EVENT_TOO_LARGE` | terminate_no_charge + alert_operator | `event_size_exceeded` |
| `ORCHESTRATOR_CANCEL` | terminate_no_charge | `orchestrator_cancelled_<reason>` |
| `AMBIGUOUS_USAGE` | terminate_no_charge + alert_operator | `usage_ambiguous` |
| `UNKNOWN_TERMINATION` | terminate_partial + alert_operator | `unknown_termination` |

`terminate_no_charge` means Tx2 aborts the claim with `usage_values = 0`; customer pays nothing. `terminate_partial` means Tx2 commits the claim with whatever usage was observed. The decision is recovery-policy-driven, not source-driven.

## 7. Failure Modes the Algorithm Does NOT Handle

| Gap | Why out-of-scope | Remediation track |
|-----|------------------|-------------------|
| G-STR-1 | Negotiated mid-stream failover with duplicate-output recovery | Requires client-side coordination protocol; HUAKAI default is no-failover-after-output | Phase 8 client SDK feature |
| G-STR-2 | Per-token charging granularity (incremental Tx2 micro-commits) | Money-grade requires single Tx2 commit; per-token would multiply contention | Phase 11+ optional stream-charging mode |
| G-STR-3 | Adaptive timeout learning from per-Account historical p95 | Requires telemetry pipeline + tuning subsystem | Phase 9 with telemetry |
| G-STR-4 | Re-emission of cached identical responses (response cache) | Out of streaming scope; cache layer is separate | Phase 7 response cache |

## 8. Test Scenarios (informs AT-GW-002-..)

1. **AT-GW-002-01 / Graceful SSE OpenAI**: 100 events + terminal marker → Usage Record `usage_source = reported`; client sees real-time events; Tx2 commits.
2. **AT-GW-002-02 / Graceful Anthropic→OpenAI translation**: upstream Anthropic SSE; client expects OpenAI SSE → events rewritten; usage normalized; `usage_source = normalized`; `rewrite_log` populated.
3. **AT-GW-002-03 / Missing terminal marker + tokenizer fallback**: upstream EOF clean but no `[DONE]` → `usage_source = inferred`; `confidence_score` populated; Usage Record `pending_reconciliation = true`.
4. **AT-GW-002-04 / Client disconnect mid-stream**: client TCP RST at event 50 of 200 → Drain Mode runs until budget exhaust; `usage_source = partial`; `drain_outcome = max_seconds_exhausted`; partial charge.
5. **AT-GW-002-05 / Drain budget cost cap**: client disconnects on expensive long-output request → `drain_outcome = max_estimated_cost_exhausted` before max_seconds; charge is bounded.
6. **AT-GW-002-06 / Inter-event timeout**: upstream silent for 30s mid-stream → `INTER_EVENT_TIMEOUT`; Tx2 commits with partial usage.
7. **AT-GW-002-07 / Oversized event**: upstream emits 2 MiB event with default 1 MiB scanner → `RESPONSE_EVENT_TOO_LARGE`; no charge; operator alert.
8. **AT-GW-002-08 / Mid-stream rate limit**: upstream emits event then 429 → `UPSTREAM_RATE_LIMIT`; Account cooled down; retry routed to different Account via Pool selection (using same claim).
9. **AT-GW-002-09 / Mid-stream failover blocked**: orchestrator attempts to fail over after `content_event_count > 0` without `Idempotent-Stream-Replay` header → refused; terminal stream error returned to client.
10. **AT-GW-002-10 / Mid-stream failover allowed**: same scenario WITH header → orchestrator switches Account; same claim id; new attempt_seq; Usage Record records both attempts.
11. **AT-GW-002-11 / Multi-source usage conflict**: mid-stream cache-hit telemetry says 100 tokens; terminal frame says 120 → terminal wins; conflict recorded in `rewrite_log`.
12. **AT-GW-002-12 / Ambiguous usage**: upstream 5xx after 0 events → `UNKNOWN_TERMINATION` with no data → `AMBIGUOUS_USAGE`; customer not charged; operator alert.
13. **AT-GW-002-13 / Eight-axis timeout independence**: set `total_stream_timeout = 60s`, `inter_event_timeout = 90s`; stream that takes 70s with steady events → terminates on `TOTAL_STREAM_TIMEOUT`, not `INTER_EVENT_TIMEOUT`.
14. **AT-GW-002-14 / Tenant isolation under load**: 100 concurrent streams across 5 tenants → no cross-tenant data appears in any Usage Record; each forwarder coroutine's tenant_id pinned at allocation.
15. **AT-GW-002-15 / Crash recovery**: gateway killed mid-stream after Pool acquire but before Phase D handoff → orphan sweep within `lease_ttl + sweep_interval` releases slot AND aborts claim; quota not leaked.

## 9. Reference Gap Closures

| Gap | Reference | This design's closure |
|-----|-----------|------------------------|
| G-REF-1 | Sub2API: post-disconnect drain has no time / byte / cost cap | Phase C-bis with three explicit budgets, any one exhausted stops drain. |
| G-REF-2 | Sub2API: oversized event has undefined behavior | S11 invariant + scanner buffer overflow → `RESPONSE_EVENT_TOO_LARGE` terminal. |
| G-REF-3 | Sub2API: no tokenizer-based usage inference fallback | `usage_source = inferred` with `confidence_score`; `pending_reconciliation` flag for late upstream usage out-of-band. |
| G-REF-4 | Sub2API: single global stream timeout | Eight-axis timeout policy per Route. |
| G-REF-5 | Sub2API: ambiguous billing in `incomplete` state | `AMBIGUOUS_USAGE` is `terminate_no_charge` + operator alert; no silent ambiguous charge. |
| G-REF-6 | one-api / New API: no inline usage extraction; usage parsed only at end | Phase B inline extraction; mid-stream telemetry frames captured. |
| G-REF-7 | Sub2API: multi-source usage conflict not reconciled | §3.2 explicit reconciliation rule with `rewrite_log` audit. |
| G-REF-8 | Sub2API: mid-stream failover semantics undefined | S10 default-deny after client-visible output; opt-in via `Idempotent-Stream-Replay` header. |

## 10. Attribution

- Source basis: existing decomposition [sub2api/streaming-forwarder.md](../sub2api/streaming-forwarder.md), evidence rows E-S2A-PROXY-018..026, [quota-billing-claim-gate-synthesis.md](quota-billing-claim-gate-synthesis.md), [pool-selection-synthesis.md](pool-selection-synthesis.md).
- No upstream function name, struct field, file path, or distinctive identifier appears here. All algorithmic structure is described in HUAKAI domain language.
- This pass was authored without reading Codex's parallel pass; mutual review and synthesis follow.
- Reviewer-lane sign-off (CL-001..010 per [_REVIEW_CHECKLIST.md](../../specs/_REVIEW_CHECKLIST.md)) by a fresh agent session is required before any implementer-lane work may cite this design.
