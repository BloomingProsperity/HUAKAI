# Streaming Forwarder + Usage Accounting — Synthesis (Source-Verified)

| Field | Value |
| --- | --- |
| Status | Action Plan (synthesized from source-verified inputs) |
| Feature ID | F-GW-002 |
| Lane mode | Option C (gateway hot path intersects Provider Account failover, account-health, and Billing Ledger reconciliation per [DR-000](../../decisions/DR-000-clean-room-methodology.md)) |
| Author | Claude (PM-Orchestrator) |
| Date | 2026-04-28 |
| Sources | Sub2API ([E-LIC-001](../../07_REFERENCE_EVIDENCE_LEDGER.md), LGPL-3.0, commit `b0a2252...`); cross-references to Helicone (analytics decoupling) and one-api (simpler streaming baseline) |
| Inputs | [streaming-forwarder-claude-v2.md](streaming-forwarder-claude-v2.md) (Sub2API source-verified Claude pass with Bedrock drain correction + atomic-billing framing fix); [streaming-forwarder-codex.md](streaming-forwarder-codex.md) is REJECTED (no CL-011 citations) and serves only as historical reference; [helicone observability cross-verify](../helicone/observability-source-verified.md) for analytics-decoupling discipline |
| Becomes | After CL-001..011 review APPROVE, file moves (cleaned of source identifiers) to `docs/specs/streaming-forwarder.md` Status=Released. |

## 1. The Sub2API Source Picture (per claude-v2 with Bedrock correction)

Per [streaming-forwarder-claude-v2.md](streaming-forwarder-claude-v2.md):

**Per-protocol streaming behavior (NOT uniform across paths)**:

| Path | Disconnect drain? | Buffer default | Failover at | Usage merge |
|------|-------------------|----------------|-------------|-------------|
| Anthropic-conversion (chat-completions / responses) | NO drain | 500 MiB scanner | pre-stream status≥400 only | last-non-zero-wins |
| Bedrock passthrough | YES — until intervalCh timeout | (likely same default) | pre-stream only | parsed via `parseSSEUsagePassthrough` |

**Common to both paths**:
- `bufio.Scanner` line-based SSE parsing.
- Per-event `c.Writer.Flush()` for real-time client visibility.
- `detachStreamUpstreamContext` returns `context.WithoutCancel(ctx)` for streaming — upstream HTTP context decoupled from request cancellation, but NOT a drain primitive.
- First-token latency tracking (`firstTokenMs`).
- Usage Record write detached from billing transaction (`writeUsageLogBestEffort` with sync-fallback + LRU dedup; NOT atomic with billing Apply).

**Failover status code list (hardcoded)**: 401, 403, 429, 529, plus all 5xx. Other 4xx codes flow through to client without account state change.

## 2. Critical Correction Carried Forward

**Earlier framing**: "Sub2API has fire-and-forget Usage Record" + "Sub2API has no atomic billing". Both wrong.

**Correct framing** (per F-OBS-001 atomic-billing finding):
- Sub2API's **billing IS atomic** (`Apply` runs claim + 5 effects in one PostgreSQL transaction).
- Sub2API's **Usage Record write IS detached** (best-effort batched/queued, with sync fallback).
- HUAKAI's improvement: **promote Usage Record into Tx2 alongside billing**, NOT "add atomic billing".

This synthesis carries the corrected framing.

## 3. Convergence (KEEP from Sub2API)

These behaviors are source-verified at commit `b0a2252...`:

1. **Per-event flush** for real-time visibility — KEEP.
2. **`bufio.Scanner` line-based SSE parser** with operator-tunable buffer — KEEP topology, but cap default lower (see H1).
3. **Inline usage extraction** from `message_start` and `message_delta` (Anthropic SSE format) — KEEP.
4. **First-token latency tracking** on first emitted chunk — KEEP.
5. **`detachStreamUpstreamContext`** decoupling — KEEP (rename to HUAKAI's `streamUpstreamContext()`).
6. **Pre-stream failover only** structurally — Mid-stream failover is hard, defer to opt-in (see H6).
7. **Atomic billing settlement** (`Apply` pattern) — KEEP, integrate per F-OBS-001 synthesis.

## 4. HUAKAI-Design Improvements (Clearly NOT in Sub2API)

These are HUAKAI-DESIGN (Sub2API anti-patterns or absences):

- **H1 — Bounded scanner buffer per Route**: HUAKAI default 1 MiB (vs Sub2API's 500 MiB), oversize → typed `RESPONSE_EVENT_TOO_LARGE` terminal failure. 500 MiB is a memory DoS surface in shared-tenant deployment.
- **H2 — Eight-axis timeout policy** per Route: connect / TLS / request-write / response-header / first-token / inter-event / total-stream / downstream-write. Sub2API has only transport-level coarse settings.
- **H3 — Bounded post-disconnect drain**: `drain_max_bytes`, `drain_max_seconds`, `drain_max_estimated_cost`. Applies to ALL paths uniformly. Sub2API has Bedrock-only unbounded drain + zero drain in Anthropic-conversion paths — both are operationally wrong.
- **H4 — Usage source taxonomy** (`reported` / `normalized` / `inferred` / `partial` / `ambiguous`) with explicit per-source action. Sub2API has none.
- **H5 — Tokenizer-based fallback for missing terminal usage**. Sub2API has no fallback.
- **H6 — Mid-stream failover with `Idempotent-Stream-Replay` opt-in header**. Sub2API has no mid-stream failover at all (structurally absent). HUAKAI adds it as opt-in with safety guards.
- **H7 — Configurable failover status codes per Account / Route**. Sub2API hardcodes 401/403/429/529 + 5xx.
- **H8 — Tx2 atomicity for slot release + Usage Record + claim status finalization** (per F-OBS-001 synthesis O2 invariant).
- **H9 — Multi-source usage reconciliation** with conflict logging (`rewrite_log`). Sub2API silently overwrites.
- **H10 — `AMBIGUOUS_USAGE` enum**: produces zero customer charge + operator alert. Sub2API charges based on accumulator (which may be zero, but no explicit "no-charge gate").
- **H11 — Mid-stream rate-limit detection**: Sub2API only handles rate-limit at pre-stream. HUAKAI watches for mid-stream `429 in event body` patterns (some providers send rate-limit info as terminal event).
- **H12 — Streaming-vs-buffered usage equivalence test**: same upstream → same final Usage Record values. Sub2API has no formal test.

## 5. The Synthesized HUAKAI Algorithm — Final

### 5.1 Scanner buffer

- Default 1 MiB (HUAKAI vs Sub2API 500 MiB).
- Operator-tunable per Route, max 64 MiB cap.
- Buffer overflow → `bufio.ErrTooLong` → typed `RESPONSE_EVENT_TOO_LARGE` terminal + operator alert.

### 5.2 Eight-axis timeout policy

| Timeout | Default | Triggered when | Action |
|---------|---------|----------------|--------|
| connect_timeout | 5s | TCP connect to upstream | Pre-stream fail; Phase A never starts |
| tls_handshake_timeout | 5s | TLS negotiation | Pre-stream fail |
| request_write_timeout | 30s | Cannot write request body | Pre-stream fail |
| response_header_timeout | 30s | Upstream takes too long for headers | Pre-stream fail |
| first_token_timeout | 60s | Headers OK but no first event | end_class = FIRST_TOKEN_TIMEOUT |
| inter_event_timeout | 30s | Gap between events too large | end_class = INTER_EVENT_TIMEOUT |
| total_stream_timeout | 600s | Whole stream too long | end_class = TOTAL_STREAM_TIMEOUT |
| downstream_write_timeout | 10s | Cannot flush to client | end_class = CLIENT_DISCONNECT |

All eight operator-tunable per Route, per Pool, with bounded ranges. Reasoning models (o1-style) need higher first_token + total_stream timeouts.

### 5.3 Stream pipeline (uniform across paths, NOT per-protocol-divergent like Sub2API)

```
Phase A — Upstream parse: bufio.Scanner with bounded buffer; produce UpstreamEvent records
Phase B — Inline event processing:
   1. Translate via F-PROTO-001 adapter (upstream → canonical → client)
   2. Extract usage into per-source accumulator (taxonomy H4)
   3. Update tool-call accumulator
   4. Write to ResponseWriter; flush
Phase C — Stream end classification:
   GRACEFUL | UPSTREAM_EOF_NO_TERMINAL | UPSTREAM_ERROR_<status> | UPSTREAM_RATE_LIMIT |
   FIRST_TOKEN_TIMEOUT | INTER_EVENT_TIMEOUT | TOTAL_STREAM_TIMEOUT |
   CLIENT_DISCONNECT | RESPONSE_EVENT_TOO_LARGE | ORCHESTRATOR_CANCEL |
   AMBIGUOUS_USAGE | UNKNOWN_TERMINATION
Phase C-bis — Bounded drain (CLIENT_DISCONNECT only):
   while not budget_exhausted: read upstream, extract usage, NO write
Phase D — Tx2 finalization (per F-OBS-001 synthesis O2):
   atomic: slot release + Usage Record (with usage_source taxonomy) + claim status + audit event + outbox cross-threshold
```

### 5.4 Failure taxonomy (15 reasons)

| Reason | Recovery Policy | Usage Record annotation |
|--------|-----------------|-------------------------|
| `GRACEFUL` | none | stream_end_graceful |
| `UPSTREAM_EOF_NO_TERMINAL` | retry_if_idempotent + alert | stream_end_no_terminal_marker |
| `UPSTREAM_ERROR_4xx` | classify_per_status (configurable) | upstream_error_<status> |
| `UPSTREAM_ERROR_5xx` | retry_with_backoff | upstream_error_<status> |
| `UPSTREAM_RATE_LIMIT` | retry_after_header + cooldown_account | upstream_rate_limit |
| `UPSTREAM_AUTH_FAILURE` | alert + cool_down_credential | upstream_auth_failure |
| `FIRST_TOKEN_TIMEOUT` | retry_with_different_account | first_token_timeout_<seconds> |
| `INTER_EVENT_TIMEOUT` | terminate_partial | inter_event_timeout |
| `TOTAL_STREAM_TIMEOUT` | terminate_partial | total_stream_timeout |
| `CLIENT_DISCONNECT` | drain_then_settle_partial | client_disconnect_<drain_outcome> |
| `RESPONSE_EVENT_TOO_LARGE` | terminate_no_charge + alert | event_size_exceeded |
| `ORCHESTRATOR_CANCEL` | terminate_no_charge | orchestrator_cancelled_<reason> |
| `AMBIGUOUS_USAGE` | terminate_no_charge + alert | usage_ambiguous |
| `UNKNOWN_TERMINATION` | terminate_partial + alert | unknown_termination |

`terminate_no_charge` aborts the claim with usage_values=0 (HUAKAI explicit gate, not Sub2API behavior).

### 5.5 Mid-stream failover (opt-in, HUAKAI-DESIGN)

- Default: NO mid-stream failover after `content_event_count > 0` (matches Sub2API absence + safer for normal users).
- Opt-in via `Idempotent-Stream-Replay: true` header on the request.
- When opted in: orchestrator may switch Provider Account on retryable failure mid-stream; new attempt_seq with same logical claim; client sees a `retry` event (HUAKAI canonical event type) followed by re-emitted content.
- Acceptance test must verify both default (refused) and opt-in (allowed) paths.

### 5.6 Usage source taxonomy (per F-OBS-001 H4)

```
select_usage_source(ctx) ∈ {
   reported           // upstream's terminal usage frame, clean
   normalized         // reported, but field-translated across format
   inferred           // tokenizer-based estimate (no terminal frame)
   partial            // stream ended mid-flight; usage only what observed
   ambiguous          // cannot determine usage even partially → no-charge gate
}
```

Multi-source reconciliation (when same request gets data from multiple sources mid-stream):
1. Higher-trust always overrides lower-trust on field-level merge.
2. Conflict at same trust level → terminal frame wins; conflict logged in rewrite_log.
3. Tool-call accumulator monotonic (deltas only grow).

## 6. Concurrency / Correctness Invariants

| # | Invariant | Source |
|---|-----------|--------|
| S1 | Slot release idempotent via UUID acquisition_token. | Aligned with F-POOL-001. |
| S2 | Usage Record finalized INSIDE Tx2 (atomic with slot release + claim status). | HUAKAI-DESIGN per F-OBS-001 synthesis O2. |
| S3 | Drain Mode never re-emits to downstream. | HUAKAI-DESIGN. |
| S4 | Drain budgets checked before every upstream read. | HUAKAI-DESIGN. |
| S5 | Usage source enum is closed; ambiguous produces zero charge + alert. | HUAKAI-DESIGN. |
| S6 | Per-Route timeout fields are independent; no global override. | HUAKAI-DESIGN. |
| S7 | Tenant isolation: every event-loop variable scoped to tenant_id. | HUAKAI-DESIGN. |
| S8 | Mid-stream failover requires Idempotent-Stream-Replay header. | HUAKAI-DESIGN. |
| S9 | Oversized event triggers typed terminal failure. | HUAKAI-DESIGN. |
| S10 | Multi-source usage conflict logged in rewrite_log. | HUAKAI-DESIGN. |
| S11 | Per-event flush (no batching). | KEEP from Sub2API. |
| S12 | Inline usage extraction (no buffer-to-end). | KEEP from Sub2API. |

## 7. Test Scenarios (AT-GW-002-01..19)

Sub2API-inheritable:
- AT-GW-002-01 / Per-event flush observable at client.
- AT-GW-002-02 / Anthropic→Canonical→Chat translation preserves usage.
- AT-GW-002-03 / Pre-stream failover on 401/403/429/529/5xx.
- AT-GW-002-04 / Pre-stream non-failover 4xx returns sanitized error.
- AT-GW-002-05 / Buffered missing message_start: 502 returned.
- AT-GW-002-06 / Scanner oversize warn log + partial response.
- AT-GW-002-07 / Client disconnect mid-stream: function returns with accumulated usage.
- AT-GW-002-08 / mergeAnthropicUsage last-non-zero behavior.

HUAKAI-design:
- AT-GW-002-09 / Bounded drain: client disconnect → drain runs to budget exhaust.
- AT-GW-002-10 / Drain cost cap: stops on max_estimated_cost_exhausted.
- AT-GW-002-11 / Eight-axis timeout independence: total_stream fires before inter_event when both apply.
- AT-GW-002-12 / Oversized event typed terminal: RESPONSE_EVENT_TOO_LARGE + no charge + alert.
- AT-GW-002-13 / Mid-stream failover blocked by default after content_event_count > 0.
- AT-GW-002-14 / Mid-stream failover allowed with Idempotent-Stream-Replay header.
- AT-GW-002-15 / Multi-source usage conflict logged in rewrite_log.
- AT-GW-002-16 / Tx2 atomicity: gateway crash mid-stream → orphan sweep finalizes.
- AT-GW-002-17 / Tenant isolation under load: 100 concurrent streams across 5 tenants → no cross-tenant data.
- AT-GW-002-18 / AMBIGUOUS_USAGE no-charge gate: zero accumulator + UNKNOWN_TERMINATION → claim aborted.
- AT-GW-002-19 / Tokenizer fallback: stream EOF without terminal → inferred usage with confidence_score.

## 8. Open TODOs

- **TODO-1**: Verify `gateway_helper.go` `AcquireAccountSlotWithWait` makes Pool slot atomic with usage (probably not — relevant for S2 invariant testing strategy).
- **TODO-2**: Cross-check one-api `relay/controller/text.go` streaming path to confirm one-api has even less than Sub2API.
- **TODO-3**: Check `bedrock_stream.go` for additional drain semantics not captured in claude-v2.

These do NOT block synthesis sign-off; they DO block Released spec (per CL-009).

## 9. Provenance

- Sub2API: commit `b0a2252...`, files `service/gateway_forward_as_chat_completions.go` (full 496 lines), `service/gateway_forward_as_responses.go` (lines 1-260), `service/gateway_service.go` (multiple sections), `service/bedrock_stream.go:148-176` (Bedrock drain). Source-verified by Claude PM 2026-04-28.
- Codex Streaming-forwarder pass REJECTED 2026-04-28 (no CL-011 citations); not used as input.
- Helicone analytics-decoupling discipline (Codex cross-verify, behavior-only per GPL-3.0).
- This synthesis: Claude PM, single source-verified pass; Codex final review will provide cross-source verification.
- Reviewer-lane sign-off: pending Codex final review CL-001..011.

## 10. Review Sign-Off

| Field | Value |
| --- | --- |
| Reviewer | (pending — fresh agent session must run CL-001..011) |
| Review date | (pending) |
| Owner answers received | N/A (no Owner-decision questions in this feature) |
| Checks passed | (pending) |
| Notes | F-GW-002 synthesis. Single source-verified specifier (Codex pass REJECTED earlier; Codex final reviewer provides cross-source check). 12 HUAKAI-design improvements clearly labeled. 3 open TODOs, none blocking synthesis. Bedrock drain correction + atomic-billing framing fix carried forward from claude-v2. |
