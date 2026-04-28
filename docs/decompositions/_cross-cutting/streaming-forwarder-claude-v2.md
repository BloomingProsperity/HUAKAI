# Streaming Forwarder + Usage Accounting — Claude v2 (Source-Verified Rewrite)

| Field | Value |
| --- | --- |
| Status | Specifier-lane draft (Claude pass v2, source-verified) |
| Author | Claude (PM-Orchestrator), specifier lane |
| Date | 2026-04-28 |
| Lane | Specifier — Option C strict spec input per [DR-000](../../decisions/DR-000-clean-room-methodology.md) carve-out for F-GW-002 |
| Feature | [F-GW-002](../../03_FEATURE_PARITY_MATRIX.md) (L1 MVP) |
| Supersedes | [streaming-forwarder-claude.md](streaming-forwarder-claude.md) (v1) — withdrawn per [2026-04-28-source-truth-corrections.md](../../reviews/2026-04-28-source-truth-corrections.md). v1 was paraphrased from prior prose; this v2 is read directly from source. |
| Sub2API verified | commit `b0a2252ed19c3720e6adafde6083e64fbac2efa9` |

## 1. What Sub2API Actually Does (Source-Verified)

Source files:
- `backend/internal/service/gateway_forward_as_chat_completions.go` (496 lines) — Chat Completions client → Anthropic upstream.
- `backend/internal/service/gateway_forward_as_responses.go` (526 lines, header through line 260 read) — OpenAI Responses client → Anthropic upstream.
- `backend/internal/service/gateway_service.go:7781–7789` — `detachStreamUpstreamContext`.
- `backend/internal/service/gateway_service.go:46` — `defaultMaxLineSize`.
- `backend/internal/service/gateway_service.go:3669–3676` — `shouldFailoverUpstreamError`.

### 1.1 Pre-Stream Decision Point

Source `gateway_forward_as_chat_completions.go:144–174`:
```go
if resp.StatusCode >= 400 {
    respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
    // …
    if s.shouldFailoverUpstreamError(resp.StatusCode) {
        // record ops event, hand to rateLimitService
        return nil, &UpstreamFailoverError{...}    // caller catches → re-select
    }
    writeGatewayCCError(c, mapUpstreamStatusCode(resp.StatusCode), "server_error", upstreamMsg)
    return nil, fmt.Errorf("upstream error: …")
}
```

**Key fact**: failover is **before** the streaming branch (lines 183–187). Once `resp.StatusCode < 400`, no failover hook fires from this file regardless of what the upstream stream subsequently does. Mid-stream failover is structurally absent.

### 1.2 Stream Branch (lines 183–187)

```go
if clientStream {
    result, handleErr = s.handleCCStreamingFromAnthropic(resp, c, originalModel, mappedModel, reasoningEffort, startTime, includeUsage)
} else {
    result, handleErr = s.handleCCBufferedFromAnthropic(resp, c, originalModel, mappedModel, reasoningEffort, startTime)
}
```

Buffered (line 212) reads upstream SSE entirely into a final response and returns once. Streaming (line 338) re-emits per event. Both use `bufio.Scanner`.

### 1.3 The Scanner Buffer Default

Source `gateway_service.go:46`:
```go
defaultMaxLineSize = 500 * 1024 * 1024  // 500 MiB
```

Operator-overridable via `cfg.Gateway.MaxLineSize`. **Default 500 MiB** — large because reasoning models can emit very long single events. Per-Route policy split was NOT in source.

### 1.4 The Streaming Loop

Source `gateway_forward_as_chat_completions.go:369–456`:

```go
scanner := bufio.NewScanner(resp.Body)
maxLineSize := defaultMaxLineSize
if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
    maxLineSize = s.cfg.Gateway.MaxLineSize
}
scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)

writeChunk := func(chunk apicompat.ChatCompletionsChunk) bool {
    sse, err := apicompat.ChatChunkToSSE(chunk)
    if err != nil { return false }
    out := string(reverseToolNamesIfPresent(c, []byte(sse)))
    if _, err := fmt.Fprint(c.Writer, out); err != nil {
        return true   // client disconnected
    }
    return false
}

processAnthropicEvent := func(event *apicompat.AnthropicStreamEvent) bool {
    if firstChunk {
        firstChunk = false
        ms := int(time.Since(startTime).Milliseconds())
        firstTokenMs = &ms
    }
    if event.Type == "message_delta" && event.Usage != nil {
        mergeAnthropicUsage(&usage, *event.Usage)
    }
    if event.Type == "message_start" && event.Message != nil {
        mergeAnthropicUsage(&usage, event.Message.Usage)
    }
    responsesEvents := apicompat.AnthropicEventToResponsesEvents(event, anthState)
    for _, resEvt := range responsesEvents {
        ccChunks := apicompat.ResponsesEventToChatChunks(&resEvt, ccState)
        for _, chunk := range ccChunks {
            if disconnected := writeChunk(chunk); disconnected {
                return true
            }
        }
    }
    c.Writer.Flush()
    return false
}

for scanner.Scan() {
    line := scanner.Text()
    if !strings.HasPrefix(line, "event: ") { continue }
    if !scanner.Scan() { break }
    dataLine := scanner.Text()
    if !strings.HasPrefix(dataLine, "data: ") { continue }
    payload := dataLine[6:]
    var event apicompat.AnthropicStreamEvent
    if err := json.Unmarshal([]byte(payload), &event); err != nil { continue }
    if processAnthropicEvent(&event) {
        return resultWithUsage(), nil    // EXIT IMMEDIATELY ON DISCONNECT
    }
}
```

**Confirmed**:
- ✅ Per-event flush (`c.Writer.Flush()` line 429).
- ✅ Buffered SSE parser (line+line: `event:` then `data:`).
- ✅ Inline usage extraction (`mergeAnthropicUsage` calls inside `processAnthropicEvent`).
- ✅ First-token latency tracking (`firstTokenMs`).
- ❌ **No drain after client disconnect**: when `processAnthropicEvent` returns `true`, the for-loop returns at line 454. Function returns; `defer resp.Body.Close()` (line 142) closes the upstream socket.

### 1.4b Bedrock Path Has a Drain (Source-Verified Correction)

**v2 first draft was wrong** — Codex source-verification 2026-04-28 caught this. The "no drain" claim above is scoped to the **Anthropic-conversion paths only** (`gateway_forward_as_chat_completions.go` + `gateway_forward_as_responses.go`). The **Bedrock path is different**.

Source `backend/internal/service/bedrock_stream.go:148-176`:
```go
if !clientDisconnected {
    // ... write to client ...
    if writeErr != nil {
        clientDisconnected = true
        logger.LegacyPrintf("service.gateway", "[Bedrock] Client disconnected during streaming, continue draining for usage: account=%d", account.ID)
    }
}
// note: case <-intervalCh: branch (line 163) returns only on inter-event timeout
```

Bedrock's loop: when downstream write fails, set `clientDisconnected = true` and **continue reading upstream**. The for-loop keeps fetching events and `parseSSEUsagePassthrough` keeps extracting usage — just suppressing the write. Drain ends only when `intervalCh` fires (per-stream `streamInterval` timeout) AND `clientDisconnected` is true (line 168-170).

So Sub2API has a **per-protocol drain policy**:
- **Anthropic-conversion paths**: no drain (loop exits immediately on disconnect).
- **Bedrock passthrough path**: drain-until-interval-timeout (no byte / cost cap, only time).

HUAKAI's design unifies these: **bounded drain with three budgets** (max_seconds / max_bytes / max_estimated_cost) for ALL paths, so neither "no drain" (chat-completions) nor "drain forever until interval timeout" (Bedrock) is the answer — both are operationally wrong.

### 1.5 The "Billing-Preserving" Primitive — Actual Behavior

Source `gateway_service.go:7781`:
```go
func detachStreamUpstreamContext(ctx context.Context, stream bool) (context.Context, context.CancelFunc) {
    if !stream {
        return ctx, func() {}
    }
    if ctx == nil {
        return context.Background(), func() {}
    }
    return context.WithoutCancel(ctx), func() {}
}
```

For `stream=true`, the upstream HTTP request is built with a context that **does not propagate cancellation** from the request context. This means:

- When client disconnects, request context is cancelled → does NOT cancel the upstream HTTP roundtrip.
- BUT the streaming loop above exits on first failed `writeChunk` and `defer resp.Body.Close()` closes the upstream socket.

So the "preservation" is just: avoid mid-event ctx-cancellation triggering a partial read. It is NOT a drain loop. It is NOT a "keep reading until usage data is captured" mechanism. The previous prose decomposition's "billing-preserving drain" framing was wrong.

### 1.6 Usage Merge Semantics

Source `gateway_forward_as_responses.go:200–216`:
```go
func mergeAnthropicUsage(dst *ClaudeUsage, src apicompat.AnthropicUsage) {
    if dst == nil { return }
    if src.InputTokens > 0 { dst.InputTokens = src.InputTokens }
    if src.OutputTokens > 0 { dst.OutputTokens = src.OutputTokens }
    if src.CacheReadInputTokens > 0 { dst.CacheReadInputTokens = src.CacheReadInputTokens }
    if src.CacheCreationInputTokens > 0 { dst.CacheCreationInputTokens = src.CacheCreationInputTokens }
}
```

**Last-non-zero-wins per field**. There is no "source taxonomy" (reported / inferred / partial). There is no conflict detection. Mid-stream events overwrite previous; terminal frame overwrites mid-stream.

Sub2API gets away with this because Anthropic's protocol guarantees: `message_start` has cache fields; `message_delta` has final input/output tokens. The two events update disjoint fields. But there's no defensive code if the protocol changes.

### 1.7 Failover Status Codes

Source `gateway_service.go:3669–3676`:
```go
func (s *GatewayService) shouldFailoverUpstreamError(statusCode int) bool {
    switch statusCode {
    case 401, 403, 429, 529:
        return true
    default:
        return statusCode >= 500
    }
}
```

Hard-coded list: 401, 403, 429, 529, plus all 5xx. Not configurable.

### 1.8 Buffered Path Usage Aggregation

Source `gateway_forward_as_chat_completions.go:212–334` (`handleCCBufferedFromAnthropic`):

For non-streaming clients, all SSE events are read into `finalResp`, with usage accumulated across `message_start`, `message_delta`, `content_block_delta`. After scan ends:
```go
if usage.InputTokens > 0 || usage.OutputTokens > 0 {
    finalResp.Usage = apicompat.AnthropicUsage{...}    // overwrite finalResp.Usage with accumulated
}
```

If scanner errors (line 285) on context.Canceled / DeadlineExceeded, only WARN-level log. No retry, no error to client (response already mostly assembled).

If `finalResp == nil` (no `message_start` ever arrived), 502 is written.

### 1.9 Streaming Path Final Marker

Source `gateway_forward_as_chat_completions.go:481–482`:
```go
fmt.Fprint(c.Writer, "data: [DONE]\n\n")
c.Writer.Flush()
```

`[DONE]` always emitted at end (whether stream completed normally or scanner ended cleanly). On disconnect, this never fires.

## 2. What Sub2API Does NOT Do

- **No drain after client disconnect**: stream-loop exits, body closes.
- **No tokenizer-based usage inference**: if upstream ends without usage frame, `usage` stays at whatever last `message_delta` set (or zero).
- **No multi-source reconciliation**: `mergeAnthropicUsage` is set-on-nonzero, not "trust hierarchy".
- **No mid-stream failover**: failover decision is pre-stream (status >= 400 branch).
- **No per-axis timeout policy**: timeouts live in transport layer (httpUpstream / DoWithTLS), not exposed as eight separate Route-policy knobs.
- **No structured `routing_reason` per Usage Record**: only `[StickyCacheMiss]` text logs (which are about selection, not streaming).
- **No "ambiguous billing" gate**: Sub2API charges based on whatever `usage` accumulator contains, including zero.
- **No "Idempotent-Stream-Replay" header**: doesn't exist.
- **No event-size-too-large terminal**: `defaultMaxLineSize=500MiB` makes oversize impractical, but if exceeded, `bufio.Scanner` returns `bufio.ErrTooLong`, which the source treats as a `scanner.Err()` warn-log and exits the loop (lines 285–292) — partial content already emitted to client, but no typed terminal failure mode.

## 3. Failure Modes That CAN Happen

Read off real source paths:

| Failure | Source path | Behavior |
|---------|-------------|----------|
| Upstream connect / TLS / write fail | `httpUpstream.DoWithTLS` returns err (line 125) | Sanitize, write 502, no failover |
| Upstream status 401/403/429/529/5xx | line 145 + 153 | Failover error returned |
| Upstream status 4xx other | line 145 + 172 | Sanitize, write to client, no failover |
| Upstream status 400 with thinking signature | gateway_service.go:4302 | Conservative two-stage retry |
| Body parse fail mid-stream | line 248 / line 449 | `continue` (skip event) |
| Scanner err (oversize / timeout) | line 285 / 458 | warn log, exit loop |
| `message_start` never arrived (buffered) | line 294 | 502 written |
| Client disconnect during streaming | `writeChunk` returns true line 397–401 | Loop exits, function returns with accumulated usage, body closes |
| Tool/signature 400 retry budget | line 4337 | Break inner retry, return body to client |

## 4. KEEP / IMPROVE / AVOID for HUAKAI

### KEEP (real Sub2API behavior worth inheriting)

- Per-event `c.Writer.Flush()` for real-time client visibility.
- `bufio.Scanner` line-based SSE parser.
- Inline usage extraction from `message_start` and `message_delta` (the two events that carry usage in Anthropic SSE).
- `detachStreamUpstreamContext` pattern: stream upstream context decoupled from request context to allow processing the in-flight event without ctx-cancel races. (Recreate as HUAKAI's `streamUpstreamContext()`.)
- First-token latency tracking on first chunk.
- Failover branch BEFORE streaming starts (mid-stream failover is hard).

### IMPROVE (HUAKAI design — clearly NOT in Sub2API)

These are HUAKAI-specific design improvements; the synthesis must label them as such, not attribute to Sub2API:

- **Bounded scanner buffer** (HUAKAI: per-Route policy with 1 MiB default; oversize → typed `RESPONSE_EVENT_TOO_LARGE` terminal). 500 MiB default is dangerous in shared memory.
- **Eight-axis timeout policy** (connect / TLS / request-write / response-header / first-token / inter-event / total-stream / downstream-write). Sub2API has only transport-level coarse settings.
- **Bounded post-disconnect drain** (`drain_max_bytes`, `drain_max_seconds`, `drain_max_estimated_cost`). Sub2API has no drain at all; HUAKAI adds drain to recover usage data when the client disconnects mid-stream.
- **Usage source taxonomy** (`reported` / `normalized` / `inferred` / `partial` / `ambiguous`) with explicit per-source action. Sub2API has none.
- **Tokenizer-based fallback for missing terminal usage**. Sub2API has no fallback.
- **Multi-source reconciliation rule** with conflict logging. Sub2API silently overwrites.
- **Mid-stream failover with Idempotent-Stream-Replay opt-in**. Sub2API has no mid-stream failover.
- **Configurable failover status codes per Account / Route**. Sub2API hardcodes 401/403/429/529 + 5xx.
- **Structured `routing_reason` on Usage Record** (already in F-POOL-001 synthesis).
- **Atomic Tx2 with Usage Record + slot release + claim status finalization**. Sub2API runs Usage Record creation as **best-effort, detached-context, non-atomic with billing** (`gateway_service.go:7812 writeUsageLogBestEffort` uses `detachedBillingContext(ctx)` so Usage Record creation continues if the request context cancels, and falls back to synchronous `repo.Create` if the best-effort writer rejects, but billing settlement and Usage Record write are NOT in one transaction). HUAKAI's improvement is Tx2 atomicity, not "Sub2API has fire-and-forget".

### AVOID (Sub2API anti-patterns)

- 500 MiB scanner buffer default.
- `mergeAnthropicUsage` last-non-zero-wins without conflict detection.
- Best-effort Usage Record creation outside the billing transaction.
- Hardcoded failover status code list.
- No per-Route timeout granularity.

## 5. Concurrency / Correctness Invariants HUAKAI Adds

| # | Invariant | Why Sub2API doesn't have it |
|---|-----------|------------------------------|
| S1 | Slot release is idempotent via UUID acquisition_token. | Sub2API releases via `ReleaseFunc` closure; idempotency depends on cache primitive. |
| S2 | Usage Record is finalized INSIDE Tx2 (atomic with slot release + claim status). | Sub2API uses `writeUsageLogBestEffort` — fire-and-forget after billing. |
| S3 | Drain Mode never re-emits to downstream. | N/A in Sub2API (no drain). |
| S4 | Drain budgets checked before every upstream read. | N/A in Sub2API (no drain). |
| S5 | Usage source enum is closed; `ambiguous` produces zero charge + operator alert. | N/A in Sub2API. |
| S6 | Per-Route timeout fields are independent; no global override. | N/A in Sub2API. |
| S7 | Tenant isolation: every event-loop var scoped to tenant_id. | Sub2API is single-tenant. |
| S8 | Mid-stream failover after client-visible output requires `Idempotent-Stream-Replay` header. | N/A in Sub2API. |
| S9 | Oversized event triggers typed `RESPONSE_EVENT_TOO_LARGE` terminal failure. | Sub2API silently truncates / partial-emits. |
| S10 | Multi-source usage conflict logged in `rewrite_log`, not silently overwritten. | Sub2API overwrites silently. |

## 6. Failure Taxonomy

This is HUAKAI's design (Sub2API has no comparable enum). Keep the v1 taxonomy but **label as HUAKAI-design**:

| Reason | Recovery Policy | Usage Record annotation |
|--------|-----------------|-------------------------|
| `GRACEFUL` | none | `stream_end_graceful` |
| `UPSTREAM_EOF_NO_TERMINAL` | retry_if_idempotent + alert_operator | `stream_end_no_terminal_marker` |
| `UPSTREAM_ERROR_4xx` | classify_and_retry_per_status (configurable per Account) | `upstream_error_<status>` |
| `UPSTREAM_ERROR_5xx` | retry_with_backoff | `upstream_error_<status>` |
| `UPSTREAM_RATE_LIMIT` | retry_after_header_or_default + cooldown_account | `upstream_rate_limit` |
| `UPSTREAM_AUTH_FAILURE` | alert_operator + cool_down_credential | `upstream_auth_failure` |
| `FIRST_TOKEN_TIMEOUT` | retry_with_different_account | `first_token_timeout_<seconds>` |
| `INTER_EVENT_TIMEOUT` | terminate_partial | `inter_event_timeout` |
| `TOTAL_STREAM_TIMEOUT` | terminate_partial | `total_stream_timeout` |
| `CLIENT_DISCONNECT` | drain_then_settle_partial (HUAKAI-specific) | `client_disconnect_<drain_outcome>` |
| `RESPONSE_EVENT_TOO_LARGE` | terminate_no_charge + alert_operator | `event_size_exceeded` |
| `ORCHESTRATOR_CANCEL` | terminate_no_charge | `orchestrator_cancelled_<reason>` |
| `AMBIGUOUS_USAGE` | terminate_no_charge + alert_operator | `usage_ambiguous` |
| `UNKNOWN_TERMINATION` | terminate_partial + alert_operator | `unknown_termination` |

## 7. Test Scenarios

### Sub2API-inheritable (verifiable against source as oracle)

- AT-GW-002-01 / Per-event flush: 100-event stream observed at client at <1s wall clock for first event.
- AT-GW-002-02 / Anthropic→Responses translation: protocol rewrite preserves usage.
- AT-GW-002-03 / Pre-stream failover on 401/403/429/529/5xx: caller catches `UpstreamFailoverError`, re-selects.
- AT-GW-002-04 / Pre-stream non-failover 4xx: client gets sanitized error, no failover.
- AT-GW-002-05 / Buffered path missing `message_start`: 502 returned.
- AT-GW-002-06 / Scanner oversize: warn log, partial response, no panic.
- AT-GW-002-07 / Client disconnect mid-stream: function returns with accumulated usage; body closes.
- AT-GW-002-08 / `mergeAnthropicUsage` last-non-zero: send `message_delta` with zero `OutputTokens`, then `message_delta` with 100 → `usage.OutputTokens=100`.

### HUAKAI-design (Sub2API has no equivalent)

- AT-GW-002-09 / Bounded drain: client disconnect → drain runs to budget exhaust, usage settled.
- AT-GW-002-10 / Drain cost cap: drain stops on `max_estimated_cost_exhausted`.
- AT-GW-002-11 / Eight-axis timeout: `total_stream_timeout` fires before `inter_event_timeout` when both apply.
- AT-GW-002-12 / Oversized event typed terminal: `RESPONSE_EVENT_TOO_LARGE` with no charge + operator alert.
- AT-GW-002-13 / Mid-stream failover blocked: orchestrator rejects after `content_event_count > 0` without `Idempotent-Stream-Replay` header.
- AT-GW-002-14 / Mid-stream failover allowed: same with header → re-select with same claim id.
- AT-GW-002-15 / Multi-source conflict logged: simulate divergent values; conflict in `rewrite_log`.
- AT-GW-002-16 / Tx2 atomicity: gateway crash mid-stream → orphan sweep finalizes.
- AT-GW-002-17 / Tenant isolation under load: 100 concurrent streams across 5 tenants → no cross-tenant data.

## 8. Open TODOs

- **TODO-1**: Verify whether `gateway_helper.go` `AcquireAccountSlotWithWait` (line 267) makes the Pool slot atomic with usage (probably not; more likely separate operations) — relevant for HUAKAI's S2 invariant.
- **TODO-2**: Verify what `cfg.Gateway.MaxLineSize` actual deployed value is in real Sub2API installs (the 500 MiB default may be reduced in production).
- **TODO-3**: Check `bedrock_stream.go` for any drain-after-disconnect pattern not in the chat-completions / responses path.
- **TODO-4**: Once F-GW-002 reviewer-lane completes, cross-check against one-api `relay/controller/text.go` streaming path to confirm `text.go` has even less than Sub2API (which it should, given one-api's simpler design).

## 9. Attribution

- Source files read directly:
  - `c:/HUAKAI/repo/.omc/reference-src/sub2api/backend/internal/service/gateway_forward_as_chat_completions.go` (full 496 lines)
  - `c:/HUAKAI/repo/.omc/reference-src/sub2api/backend/internal/service/gateway_forward_as_responses.go` (lines 1–260)
  - `c:/HUAKAI/repo/.omc/reference-src/sub2api/backend/internal/service/gateway_service.go` (lines 46, 3669–3676, 4267–4339, 7781–7789)
- All upstream identifier names (function names, struct fields, log keys) appear here only because this is a SPECIFIER-LANE file. Implementer-lane files (any future `docs/specs/*.md`) must use HUAKAI domain language only.
- This pass was authored AFTER reading source. Codex's parallel pass at `streaming-forwarder-codex.md` (output of background task `b8qpb5fzv`, completed 2026-04-28) was authored independently. Mutual review and synthesis follow this v2.
- v1 (`streaming-forwarder-claude.md`) is **withdrawn**; see `docs/reviews/2026-04-28-source-truth-corrections.md` for the catalogue of v1 hallucinations.
